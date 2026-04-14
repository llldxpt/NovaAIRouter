package pool

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/rs/zerolog"
)

// noProxyTransport disables proxy for all backend connections (backends are always local)
var noProxyTransport = &http.Transport{
	Proxy:               func(*http.Request) (*url.URL, error) { return nil, nil },
	MaxIdleConns:        100,
	MaxIdleConnsPerHost: 10,
	IdleConnTimeout:     90 * time.Second,
}

type PoolMetrics struct {
	Active        int32
	QueueLen      int32
	Waiting       int32
	Healthy       bool
	MaxConcurrent int32
}

type RequestPool struct {
	path            string
	serviceID       string
	targetURL       string
	maxConcur       int32
	queue           chan *queueRequest
	log             zerolog.Logger
	client          *http.Client
	backendTimeout  time.Duration
	activeCount     int32
	waitingCount    int32
	cancel          context.CancelFunc
	context         context.Context
	workers         []*worker
	workerCount     int32
	metricsCallback func(active, queueLen int32)
	shutdownOnce    sync.Once
	workerWG        sync.WaitGroup
}

type worker struct {
	id      int
	pool    *RequestPool
	running chan struct{}
}

type queueRequest struct {
	W    http.ResponseWriter
	R    *http.Request
	Ctx  context.Context
	Done chan struct{}
}

type PoolManager struct {
	pools          map[string]map[string]*RequestPool // key1=path, key2=serviceID
	maxConcur      int
	queueCapacity  int
	backendTimeout time.Duration
	log            zerolog.Logger
	mu             sync.RWMutex

	// 远程节点飞行中请求计数器，key = nodeID + "|" + nodePath
	remoteCounters   map[string]*int32
	remoteCountersMu sync.RWMutex

	// 广播时刻的本机 localActive 快照，key = nodeID + "|" + nodePath
	remoteSnapshots   map[string]int32
	remoteSnapshotsMu sync.RWMutex
}

func NewManager(maxConcur int, backendTimeout time.Duration, log zerolog.Logger) *PoolManager {
	return NewManagerWithQueueCapacity(maxConcur, 1000, backendTimeout, log)
}

func NewManagerWithQueueCapacity(maxConcur int, queueCapacity int, backendTimeout time.Duration, log zerolog.Logger) *PoolManager {
	return &PoolManager{
		pools:           make(map[string]map[string]*RequestPool),
		remoteCounters:  make(map[string]*int32),
		remoteSnapshots: make(map[string]int32),
		maxConcur:       maxConcur,
		queueCapacity:   queueCapacity,
		backendTimeout:  backendTimeout,
		log:             log,
	}
}

// remoteKey 生成远程计数器的 key
func remoteKey(nodeID, nodePath string) string {
	return nodeID + "|" + nodePath
}

// IncRemoteActive 远程节点请求开始，飞行中计数 +1
func (m *PoolManager) IncRemoteActive(nodeID, nodePath string) {
	key := remoteKey(nodeID, nodePath)
	m.remoteCountersMu.Lock()
	if _, ok := m.remoteCounters[key]; !ok {
		var zero int32
		m.remoteCounters[key] = &zero
	}
	ptr := m.remoteCounters[key]
	m.remoteCountersMu.Unlock()
	atomic.AddInt32(ptr, 1)
}

// DecRemoteActive 远程节点请求结束，飞行中计数 -1
func (m *PoolManager) DecRemoteActive(nodeID, nodePath string) {
	key := remoteKey(nodeID, nodePath)
	m.remoteCountersMu.RLock()
	ptr, ok := m.remoteCounters[key]
	m.remoteCountersMu.RUnlock()
	if ok {
		atomic.AddInt32(ptr, -1)
	}
}

// GetRemoteActive 获取指定远程节点端点的飞行中请求数
func (m *PoolManager) GetRemoteActive(nodeID, nodePath string) int32 {
	key := remoteKey(nodeID, nodePath)
	m.remoteCountersMu.RLock()
	ptr, ok := m.remoteCounters[key]
	m.remoteCountersMu.RUnlock()
	if !ok {
		return 0
	}
	return atomic.LoadInt32(ptr)
}

// SnapshotRemoteActives 在广播前调用，将当前所有 remoteCounters 的值存入快照
func (m *PoolManager) SnapshotRemoteActives() {
	m.remoteCountersMu.RLock()
	snapshots := make(map[string]int32, len(m.remoteCounters))
	for key, ptr := range m.remoteCounters {
		snapshots[key] = atomic.LoadInt32(ptr)
	}
	m.remoteCountersMu.RUnlock()

	m.remoteSnapshotsMu.Lock()
	m.remoteSnapshots = snapshots
	m.remoteSnapshotsMu.Unlock()
}

// GetRemoteActiveSnapshot 获取上次广播时的 localActive 快照值
func (m *PoolManager) GetRemoteActiveSnapshot(nodeID, nodePath string) int32 {
	key := remoteKey(nodeID, nodePath)
	m.remoteSnapshotsMu.RLock()
	v := m.remoteSnapshots[key]
	m.remoteSnapshotsMu.RUnlock()
	return v
}

func (m *PoolManager) GetPool(path, serviceID string) (*RequestPool, bool) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	if subMap, ok := m.pools[path]; ok {
		pool, ok := subMap[serviceID]
		return pool, ok
	}
	return nil, false
}

func (m *PoolManager) CreatePool(path, serviceID, targetURL string, maxConcur int) {
	m.mu.Lock()
	defer m.mu.Unlock()

	if m.pools[path] == nil {
		m.pools[path] = make(map[string]*RequestPool)
	}

	if pool, ok := m.pools[path][serviceID]; ok {
		pool.maxConcur = int32(maxConcur)
		pool.targetURL = targetURL
		m.log.Info().Str("path", path).Str("serviceID", serviceID).Int("maxConcur", maxConcur).Msg("Updated existing pool")
		return
	}

	pool := NewRequestPool(path, serviceID, targetURL, maxConcur, m.queueCapacity, m.backendTimeout, m.log)
	m.pools[path][serviceID] = pool
	m.log.Info().Str("path", path).Str("serviceID", serviceID).Int("maxConcur", maxConcur).Int("queueCapacity", m.queueCapacity).Msg("Created pool at endpoint registration")
}

func (m *PoolManager) RemovePool(path, serviceID string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if subMap, ok := m.pools[path]; ok {
		if pool, ok := subMap[serviceID]; ok {
			pool.Shutdown()
			delete(subMap, serviceID)
			if len(subMap) == 0 {
				delete(m.pools, path)
			}
			m.log.Info().Str("path", path).Str("serviceID", serviceID).Msg("Removed request pool")
		}
	}
}

func (m *PoolManager) GetAllStates() map[string]*PoolMetrics {
	m.mu.RLock()
	defer m.mu.RUnlock()
	states := make(map[string]*PoolMetrics)
	for path, subMap := range m.pools {
		for serviceID, p := range subMap {
			key := path + "|" + serviceID
			states[key] = p.GetMetrics()
		}
	}
	return states
}

// GetPoolsByPath 获取指定 path 下的所有 pool
func (m *PoolManager) GetPoolsByPath(path string) []*RequestPool {
	m.mu.RLock()
	defer m.mu.RUnlock()
	var pools []*RequestPool
	if subMap, ok := m.pools[path]; ok {
		for _, pool := range subMap {
			pools = append(pools, pool)
		}
	}
	return pools
}

// ShutdownAll 关闭所有 pool（用于测试清理）
func (m *PoolManager) ShutdownAll() {
	m.mu.Lock()
	defer m.mu.Unlock()
	for path, subMap := range m.pools {
		for serviceID, pool := range subMap {
			pool.Shutdown()
			delete(subMap, serviceID)
		}
		delete(m.pools, path)
	}
}

func NewRequestPool(path, serviceID, targetURL string, maxConcur int, queueCapacity int, backendTimeout time.Duration, log zerolog.Logger) *RequestPool {
	// 确保 maxConcur 至少为 1（修复 maxConcur=0 边界问题）
	if maxConcur < 1 {
		maxConcur = 1
	}
	ctx, cancel := context.WithCancel(context.Background())
	p := &RequestPool{
		path:           path,
		serviceID:      serviceID,
		targetURL:      targetURL,
		maxConcur:      int32(maxConcur),
		queue:          make(chan *queueRequest, queueCapacity),
		log:            log,
		backendTimeout: backendTimeout,
		client: &http.Client{
			Timeout:   backendTimeout,
			Transport: noProxyTransport,
		},
		cancel:      cancel,
		context:     ctx,
		workers:     make([]*worker, maxConcur),
		workerCount: 0,
	}

	for i := 0; i < maxConcur; i++ {
		w := &worker{
			id:      i,
			pool:    p,
			running: make(chan struct{}),
		}
		p.workers[i] = w
		atomic.AddInt32(&p.workerCount, 1)
		p.workerWG.Add(1)
		go w.run()
	}

	log.Info().Str("path", path).Int("maxConcur", maxConcur).Int("queue_cap", cap(p.queue)).Msg("Created new request pool with workers")
	return p
}

func (w *worker) run() {
	defer w.pool.workerWG.Done()
	w.pool.log.Info().Int("worker_id", w.id).Msg("Worker started")

	for {
		select {
		case <-w.running:
			return
		case <-w.pool.context.Done():
			return
		case req, ok := <-w.pool.queue:
			if !ok {
				return
			}
			atomic.AddInt32(&w.pool.activeCount, 1)
			w.pool.notifyMetricsChanged()
			atomic.AddInt32(&w.pool.waitingCount, -1)
			w.pool.notifyMetricsChanged()
			w.processRequest(req)
			atomic.AddInt32(&w.pool.activeCount, -1)
			w.pool.notifyMetricsChanged()
		}
	}
}

func (w *worker) processRequest(req *queueRequest) {
	w.pool.log.Info().Msg("processRequest: START")
	var body io.Reader
	if req.R.Body != nil {
		bodyBytes, err := io.ReadAll(req.R.Body)
		if err != nil {
			req.R.Body.Close()
			http.Error(req.W, "Bad Request", http.StatusBadRequest)
			close(req.Done)
			return
		}
		req.R.Body.Close()
		if len(bodyBytes) > 0 {
			body = bytes.NewReader(bodyBytes)
		}
	}

	// Compute dynamic target URL: append the sub-path beyond the registered pool path
	subPath := strings.TrimPrefix(req.R.URL.Path, w.pool.path)
	if subPath == req.R.URL.Path {
		// pool.path has trailing slash but request doesn't — strip it and retry
		subPath = strings.TrimPrefix(req.R.URL.Path, strings.TrimSuffix(w.pool.path, "/"))
	}
	targetURL := w.pool.targetURL + subPath
	if req.R.URL.RawQuery != "" {
		targetURL += "?" + req.R.URL.RawQuery
	}
	w.pool.log.Info().Str("pool_path", w.pool.path).Str("req_path", req.R.URL.Path).Str("subPath", subPath).Str("targetURL", targetURL).Msg("processRequest: computed targetURL, calling forwardRequestWithHeaders")
	w.pool.forwardRequestWithHeaders(req.Ctx, req.W, req.R.Method, targetURL, req.R.Header.Clone(), body, nil)
	w.pool.log.Info().Msg("processRequest: forwardRequestWithHeaders returned, closing Done")
	close(req.Done)
	w.pool.log.Info().Msg("processRequest: END")
}

func (p *RequestPool) Serve(ctx context.Context, w http.ResponseWriter, r *http.Request) error {
	return p.ServeWithHeaders(ctx, w, r, nil)
}

func (p *RequestPool) ServeWithHeaders(ctx context.Context, w http.ResponseWriter, r *http.Request, responseHeaders http.Header) error {
	req := &queueRequest{
		W:    w,
		R:    r,
		Ctx:  ctx,
		Done: make(chan struct{}),
	}

	atomic.AddInt32(&p.waitingCount, 1)
	p.notifyMetricsChanged()

	select {
	case p.queue <- req:
	default:
		atomic.AddInt32(&p.waitingCount, -1)
		p.notifyMetricsChanged()
		http.Error(w, "Service Unavailable - Queue Full", http.StatusServiceUnavailable)
		return fmt.Errorf("queue full")
	}

	select {
	case <-req.Done:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (p *RequestPool) forwardRequestWithHeaders(ctx context.Context, w http.ResponseWriter, method, url string, headers http.Header, body io.Reader, responseHeaders http.Header) {
	reqCtx, cancel := context.WithTimeout(ctx, p.backendTimeout)
	defer cancel()
	req, err := http.NewRequestWithContext(reqCtx, method, url, body)
	if err != nil {
		p.log.Error().Err(err).Msg("Failed to create request")
		// 检查 context 是否已取消，避免在连接关闭后写入响应
		select {
		case <-ctx.Done():
			return
		default:
			http.Error(w, "Bad Gateway", http.StatusBadGateway)
		}
		return
	}

	for key, values := range headers {
		for _, value := range values {
			req.Header.Add(key, value)
		}
	}

	resp, err := p.client.Do(req)
	p.log.Info().Str("url", url).Err(err).Msg("forwardRequestWithHeaders: client.Do returned")
	if err != nil {
		p.log.Error().Err(err).Msg("Failed to forward request")
		select {
		case <-ctx.Done():
			return
		default:
			http.Error(w, "BadGateway", http.StatusBadGateway)
		}
		return
	}
	defer resp.Body.Close()

	for key, values := range resp.Header {
		for _, value := range values {
			w.Header().Add(key, value)
		}
	}

	w.WriteHeader(resp.StatusCode)

	// Check if streaming response
	ct := resp.Header.Get("Content-Type")
	isStreaming := strings.Contains(ct, "text/event-stream") ||
		strings.Contains(ct, "application/x-ndjson") ||
		resp.Header.Get("Transfer-Encoding") == "chunked"

	if isStreaming {
		if flusher, ok := w.(http.Flusher); ok {
			buf := make([]byte, 4096)
			for {
				select {
				case <-ctx.Done():
					return
				default:
				}
				n, err := resp.Body.Read(buf)
				if n > 0 {
					w.Write(buf[:n])
					flusher.Flush()
				}
				if err != nil {
					break
				}
			}
			return
		}
	}
	io.Copy(w, resp.Body)
}

// EpID 返回该 pool 对应的 serviceID（即 epID）
func (p *RequestPool) EpID() string {
	return p.serviceID
}

func (p *RequestPool) GetMetrics() *PoolMetrics {
	metrics := &PoolMetrics{
		Active:        atomic.LoadInt32(&p.activeCount),
		QueueLen:      int32(len(p.queue)),
		Waiting:       atomic.LoadInt32(&p.waitingCount),
		Healthy:       true,
		MaxConcurrent: p.maxConcur,
	}
	return metrics
}

func (p *RequestPool) SetMetricsCallback(callback func(active, queueLen int32)) {
	p.metricsCallback = callback
}

func (p *RequestPool) notifyMetricsChanged() {
	if p.metricsCallback != nil {
		active := atomic.LoadInt32(&p.activeCount)
		waitingCount := atomic.LoadInt32(&p.waitingCount)
		p.metricsCallback(active, waitingCount)
	}
}

func (p *RequestPool) Shutdown() {
	p.shutdownOnce.Do(func() {
		p.cancel()
		for _, w := range p.workers {
			if w != nil {
				close(w.running)
			}
		}
		close(p.queue)
		p.workerWG.Wait()
	})
}

func (p *RequestPool) GetPoolInfo() string {
	return fmt.Sprintf("Pool[%s]: max=%d, active=%d, queue=%d, waiting=%d",
		p.path,
		p.maxConcur,
		atomic.LoadInt32(&p.activeCount),
		len(p.queue),
		atomic.LoadInt32(&p.waitingCount))
}
