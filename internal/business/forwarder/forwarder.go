package forwarder

import (
	"context"
	"errors"
	"fmt"
	"math/rand"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/rs/zerolog"

	"novaairouter/internal/config"
	"novaairouter/internal/metrics"
	"novaairouter/internal/models"
	"novaairouter/internal/pool"
	"novaairouter/internal/registry"
)

// noProxyTransport disables proxy for all backend connections
var noProxyTransport = &http.Transport{
	Proxy: func(*http.Request) (*url.URL, error) { return nil, nil },
}

var (
	ErrBackendFailed  = errors.New("backend failed")
	ErrForwardFailed = errors.New("forward failed")
)

// Forwarder 转发器
type Forwarder struct {
	config   *config.Config
	registry *registry.Registry
	poolMgr  *pool.PoolManager
	log      zerolog.Logger
	httpClient *http.Client
}

// New 创建新的转发器
func New(cfg *config.Config, reg *registry.Registry, poolMgr *pool.PoolManager, log zerolog.Logger) *Forwarder {
	return &Forwarder{
		config:   cfg,
		registry: reg,
		poolMgr:  poolMgr,
		log:      log,
		httpClient: &http.Client{
			Transport: noProxyTransport,
		},
	}
}

// HandleLocalRequestByPath 根据 nodePath 用负载均衡选择 pool 处理本地请求
func (f *Forwarder) HandleLocalRequestByPath(w http.ResponseWriter, r *http.Request, path, nodePath string, startTime time.Time) {
	// 用 selectPoolByLoad 在 nodePath 下所有 pool 中选负载最低的
	selectedPool := f.selectPoolByLoad(nodePath, "", nil)
	if selectedPool == nil {
		f.log.Error().Str("nodePath", nodePath).Msg("No pool available for nodePath")
		http.Error(w, "Service Unavailable", http.StatusServiceUnavailable)
		return
	}

	// 找到 pool 对应的 endpoint
	ep, ok := f.registry.GetEndpointByEpID(nodePath, selectedPool.EpID())
	if !ok {
		f.log.Error().Str("nodePath", nodePath).Str("epID", selectedPool.EpID()).Msg("Endpoint not found for selected pool")
		http.Error(w, "Service Unavailable", http.StatusServiceUnavailable)
		return
	}

	f.HandleLocalRequest(w, r, path, ep, startTime)
}

// HandleLocalRequest 处理本地请求
func (f *Forwarder) HandleLocalRequest(w http.ResponseWriter, r *http.Request, path string, ep *models.LocalEndpoint, startTime time.Time) {
	err := f.HandleLocalRequestWithError(w, r, path, ep, startTime)
	if err != nil {
		metrics.New().IncRequestTotal(path, f.config.NodeID, "502")
		metrics.New().IncProxyErrors("backend_error", f.config.NodeID)
		f.log.Error().Err(err).Str("path", path).Msg("Local request failed")
	}
}

// HandleLocalRequestWithError 处理本地请求并返回错误
func (f *Forwarder) HandleLocalRequestWithError(w http.ResponseWriter, r *http.Request, path string, ep *models.LocalEndpoint, startTime time.Time) error {
	f.log.Debug().Str("path", path).Str("ep.NodePath", ep.NodePath).Msg("handleLocalRequestWithError called")

	requestPool, ok := f.poolMgr.GetPool(ep.NodePath, ep.EpID)
	if !ok || requestPool == nil {
		f.log.Error().Str("nodePath", ep.NodePath).Str("epID", ep.EpID).Msg("Pool not found! Pool must be created at endpoint registration!")
		http.Error(w, "Service Unavailable - Pool not configured", http.StatusServiceUnavailable)
		return ErrBackendFailed
	}

	f.log.Info().Str("nodePath", ep.NodePath).Str("epID", ep.EpID).Int32("maxConcurrent", ep.MaxConcurrent).Msg("Using pool for endpoint")

	responseHeaders := make(http.Header)
	f.log.Info().Msg("Calling ServeWithHeaders")
	err := requestPool.ServeWithHeaders(r.Context(), w, r, responseHeaders)
	f.log.Info().Err(err).Msg("ServeWithHeaders returned")
	
	duration := time.Since(startTime).Seconds()
	metrics.New().ObserveRequestDuration(path, f.config.NodeID, duration)

	if err != nil && err != context.Canceled && err != context.DeadlineExceeded {
		return ErrBackendFailed
	}

	statusCode := "200"
	if err == context.Canceled {
		statusCode = "499"
	} else if err == context.DeadlineExceeded {
		statusCode = "504"
	}
	metrics.New().IncRequestTotal(path, f.config.NodeID, statusCode)
	// Update Prometheus metrics after request completion
	metrics.New().SetActiveRequests(path, f.config.NodeID, float64(requestPool.GetMetrics().Active))
	metrics.New().SetQueueLength(path, f.config.NodeID, float64(requestPool.GetMetrics().QueueLen))
	f.log.Info().Str("path", path).Float64("duration", duration).Msg("Request completed")

	if ep.Plugin {
		targetNodeID := responseHeaders.Get("X-Target-Node")
		if targetNodeID != "" {
			f.log.Info().Str("target_node", targetNodeID).Msg("Plugin requested forwarding to target node")
			targetPath := responseHeaders.Get("X-Target-Path")
			if targetPath == "" {
				targetPath = path
			}

			targetNode, ok := f.registry.GetRemoteNode(targetNodeID)
			if !ok {
				localNodeID := f.config.NodeID
				if targetNodeID != localNodeID {
					f.log.Warn().Str("target_node", targetNodeID).Msg("Target node not found")
					return nil
				}
				targetNode = nil
			}

			if targetNode == nil || targetNodeID == f.config.NodeID {
				localTargetEp, localTargetOk := f.registry.GetEndpoint(targetPath)
				if !localTargetOk || !localTargetEp.Healthy {
					f.log.Warn().Str("path", targetPath).Msg("Target endpoint not available")
					return nil
				}
				return f.HandleLocalRequestWithError(w, r, targetPath, localTargetEp, startTime)
			}
			return f.HandleForwardedRequestWithError(w, r, targetPath, targetNode, startTime)
		}
	}

	return nil
}

// selectPoolByLoad 根据负载选择 pool，负载相同时按 maxConcurrent 加权随机
func (f *Forwarder) selectPoolByLoad(nodePath, currentServiceID string, defaultPool *pool.RequestPool) *pool.RequestPool {
	pools := f.poolMgr.GetPoolsByPath(nodePath)
	if len(pools) == 0 {
		return defaultPool
	}
	if len(pools) == 1 {
		return pools[0]
	}

	type poolLoad struct {
		p    *pool.RequestPool
		load float64
	}

	items := make([]poolLoad, 0, len(pools))
	for _, p := range pools {
		m := p.GetMetrics()
		maxConcur := float64(m.MaxConcurrent)
		if maxConcur <= 0 {
			maxConcur = 1
		}
		items = append(items, poolLoad{p: p, load: float64(m.Active+m.QueueLen) / maxConcur})
	}

	// 找最低负载
	bestLoad := items[0].load
	for _, item := range items[1:] {
		if item.load < bestLoad {
			bestLoad = item.load
		}
	}

	// 收集负载最低的 pool 集合（容忍 0.001 误差）
	var candidates []*pool.RequestPool
	var weights []int32
	for _, item := range items {
		if item.load <= bestLoad+0.001 {
			candidates = append(candidates, item.p)
			m := item.p.GetMetrics()
			w := m.MaxConcurrent
			if w <= 0 {
				w = 1
			}
			weights = append(weights, w)
		}
	}

	if len(candidates) == 1 {
		return candidates[0]
	}

	// 按 maxConcurrent 加权随机
	total := int32(0)
	for _, w := range weights {
		total += w
	}
	r := rand.Int31n(total)
	for i, w := range weights {
		r -= w
		if r < 0 {
			return candidates[i]
		}
	}
	return candidates[len(candidates)-1]
}

// HandleForwardedRequest 处理转发请求
func (f *Forwarder) HandleForwardedRequest(w http.ResponseWriter, r *http.Request, path string, node *models.RemoteNode, startTime time.Time) {
	err := f.HandleForwardedRequestWithError(w, r, path, node, startTime)
	if err != nil {
		metrics.New().IncRequestTotal(path, f.config.NodeID, "502")
		metrics.New().IncProxyErrors("forward_error", f.config.NodeID)
		f.log.Error().Err(err).Str("path", path).Str("node", node.NodeID).Msg("Forward request failed")
	}
}

// HandleForwardedRequestWithError 处理转发请求并返回错误
func (f *Forwarder) HandleForwardedRequestWithError(w http.ResponseWriter, r *http.Request, path string, node *models.RemoteNode, startTime time.Time) error {
	forwardedBy := r.Header.Get("X-Forwarded-By")
	if strings.Contains(forwardedBy, f.config.NodeID) {
		f.log.Warn().Str("path", path).Msg("Loop detected")
		http.Error(w, "Loop detected", http.StatusBadGateway)
		return ErrForwardFailed
	}

	// 飞行中计数 +1，请求结束时 -1
	f.poolMgr.IncRemoteActive(node.NodeID, node.NodePath)
	defer f.poolMgr.DecRemoteActive(node.NodeID, node.NodePath)

	nodePath := node.NodePath
	servicePathBase := node.ServicePath

	var targetPath string
	if servicePathBase != "" {
		hasTrailingSlash := strings.HasSuffix(nodePath, "/")
		if hasTrailingSlash {
			targetPath = strings.TrimPrefix(path, nodePath)
		} else {
			targetPath = strings.TrimPrefix(path, nodePath)
			if targetPath == "" {
				targetPath = "/"
			}
		}
	} else {
		targetPath = path
	}

	targetURL := fmt.Sprintf("http://%s:%d%s%s", node.Address, node.ServicePort, servicePathBase, targetPath)
	if r.URL.RawQuery != "" {
		targetURL += "?" + r.URL.RawQuery
	}

	req, err := http.NewRequestWithContext(r.Context(), r.Method, targetURL, r.Body)
	if err != nil {
		return ErrForwardFailed
	}

	// 只转发安全的请求头
	safeHeaders := map[string]bool{
		"Content-Type":              true,
		"Content-Length":            true,
		"Accept":                    true,
		"Accept-Encoding":           true,
		"Accept-Language":           true,
		"User-Agent":                true,
		"X-Requested-With":          true,
		"Referer":                   true,
		"Authorization":             true, // 保留授权头
	}
	
	for key, values := range r.Header {
		if safeHeaders[key] || strings.HasPrefix(key, "X-") {
			for _, value := range values {
				if key != "X-Forwarded-By" {
					req.Header.Add(key, value)
				}
			}
		}
	}
	if forwardedBy != "" {
		req.Header.Set("X-Forwarded-By", forwardedBy+","+f.config.NodeID)
	} else {
		req.Header.Set("X-Forwarded-By", f.config.NodeID)
	}

	resp, err := f.httpClient.Do(req)
	if err != nil {
		f.log.Error().Err(err).Str("targetURL", targetURL).Msg("httpClient.Do failed")
		return ErrForwardFailed
	}
	defer resp.Body.Close()

	for key, values := range resp.Header {
		for _, value := range values {
			w.Header().Add(key, value)
		}
	}
	w.WriteHeader(resp.StatusCode)

	flusher, canFlush := w.(http.Flusher)
	buf := make([]byte, 4096)
	for {
		n, err := resp.Body.Read(buf)
		if n > 0 {
			if _, werr := w.Write(buf[:n]); werr != nil {
				break
			}
			if canFlush {
				flusher.Flush()
			}
		}
		if err != nil {
			break
		}
	}

	duration := time.Since(startTime).Seconds()
	metrics.New().IncRequestTotal(path, f.config.NodeID, fmt.Sprintf("%d", resp.StatusCode))
	metrics.New().ObserveRequestDuration(path, f.config.NodeID, duration)
	f.log.Info().Str("path", path).Str("node", node.NodeID).Float64("duration", duration).Msg("Forward request completed")
	
	if resp.StatusCode >= 500 {
		return ErrForwardFailed
	}
	return nil
}
