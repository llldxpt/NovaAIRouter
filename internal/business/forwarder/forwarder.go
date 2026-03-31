package forwarder

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/rs/zerolog"

	"novaairouter/internal/config"
	"novaairouter/internal/metrics"
	"novaairouter/internal/models"
	"novaairouter/internal/pool"
	"novaairouter/internal/registry"
	"novaairouter/internal/utils"
)

// noProxyTransport disables proxy for all backend connections
var noProxyTransport = &http.Transport{
	Proxy: nil,
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

	servicePort := utils.ExtractPortFromServicePath(ep.ServicePath)
	servicePathBase := utils.ExtractBaseFromServicePath(ep.ServicePath)
	nodePath := ep.NodePath

	hasTrailingSlash := strings.HasSuffix(nodePath, "/")
	var targetPath string
	if hasTrailingSlash {
		targetPath = strings.TrimPrefix(path, nodePath)
	} else {
		targetPath = strings.TrimPrefix(path, nodePath)
		if targetPath == "" {
			targetPath = "/"
		}
	}

	targetURL := fmt.Sprintf("http://localhost:%d%s%s", servicePort, servicePathBase, targetPath)
	
	totalMaxConcurrent := int32(0)
	
	// 使用已获取的 ep 的 NodePath 来查找 endpoint
	// 注意：path 是完整请求路径（如 /v1/concurrent/chat/completions）
	// 而 ep.NodePath 是注册的端点路径（如 /v1/concurrent/）
	localEp, localOk := f.registry.GetEndpoint(ep.NodePath)
	if localOk && localEp.Healthy && localEp.MaxConcurrent > 0 {
		totalMaxConcurrent += localEp.MaxConcurrent
	}
	
	remoteNodes := f.registry.GetHealthyNodesForPath(ep.NodePath)
	for _, node := range remoteNodes {
		if state, ok := node.EndpointStates[ep.NodePath]; ok && state.Healthy {
			if state.MaxConcurrent > 0 {
				totalMaxConcurrent += state.MaxConcurrent
			}
		}
	}
	
	if totalMaxConcurrent < 1 {
		totalMaxConcurrent = 1
	}
	
	// 使用 ep.NodePath 和 ep.ServiceID 作为 pool key，这样同一个endpoint下的不同请求路径会复用同一个 pool
	f.log.Info().Str("ep.NodePath", ep.NodePath).Str("ep.ServiceID", ep.ServiceID).Str("path", path).Msg("=== Creating pool with ep.NodePath ===")
	requestPool := f.poolMgr.GetOrCreatePool(ep.NodePath, ep.ServiceID, targetURL, int(totalMaxConcurrent))
	if requestPool == nil {
		f.log.Error().Str("nodePath", ep.NodePath).Msg("Pool is nil! Pool must be created at endpoint registration!")
		http.Error(w, "Service Unavailable - Pool not configured", http.StatusServiceUnavailable)
		return ErrBackendFailed
	}
	// 从同 path 的所有 pool 中选择负载最低的
	requestPool = f.selectPool(ep.NodePath, requestPool)
	f.log.Info().Msg("Got request pool")

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

// selectPool 从同 path 的所有 pool 中选择负载最低的一个
func (f *Forwarder) selectPool(nodePath string, defaultPool *pool.RequestPool) *pool.RequestPool {
	pools := f.poolMgr.GetPoolsByPath(nodePath)
	if len(pools) == 0 {
		return defaultPool
	}
	if len(pools) == 1 {
		return pools[0]
	}

	// 选择负载最低的 pool（负载 = active + queueLen）
	var bestPool *pool.RequestPool
	bestLoad := int32(1 << 30)
	for _, p := range pools {
		m := p.GetMetrics()
		load := m.Active + m.QueueLen
		if load < bestLoad {
			bestLoad = load
			bestPool = p
		}
	}
	if bestPool == nil {
		return defaultPool
	}
	f.log.Debug().Str("nodePath", nodePath).Int("pool_count", len(pools)).Int32("best_load", bestLoad).Msg("Selected pool with lowest load")
	return bestPool
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
		return ErrForwardFailed
	}

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
