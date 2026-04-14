package router

import (
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/rs/zerolog"

	"novaairouter/internal/business/balancer"
	"novaairouter/internal/config"
	"novaairouter/internal/metrics"
	"novaairouter/internal/models"
	"novaairouter/internal/pool"
	"novaairouter/internal/registry"
)

const maxRetries = 2

// Router 路由管理器
type Router struct {
	config   *config.Config
	registry *registry.Registry
	poolMgr  *pool.PoolManager
	log      zerolog.Logger
	balancer *balancer.Balancer
}

// New 创建新的路由管理器
func New(cfg *config.Config, reg *registry.Registry, poolMgr *pool.PoolManager, log zerolog.Logger) *Router {
	return &Router{
		config:   cfg,
		registry: reg,
		poolMgr:  poolMgr,
		log:      log,
		balancer: balancer.New(),
	}
}

// HandleRequest 处理请求
func (r *Router) HandleRequest(w http.ResponseWriter, req *http.Request) interface{} {
	path := req.URL.Path

	r.log.Info().
		Str("method", req.Method).
		Str("path", path).
		Msg("Incoming request")

	allEndpoints := r.registry.GetAllLocalEndpoints()
	r.log.Debug().Str("registry_addr", fmt.Sprintf("%p", r.registry)).Int("total", len(allEndpoints)).Msg("Router registry status")
	r.log.Debug().Int("total_endpoints", len(allEndpoints)).Msg("Registered endpoints")

	// 从 X-Forwarded-By 中提取已经处理过该请求的节点，避免转发回去形成环路
	forwardedBy := req.Header.Get("X-Forwarded-By")
	alreadySeen := make(map[string]bool)
	if forwardedBy != "" {
		for _, nid := range strings.Split(forwardedBy, ",") {
			alreadySeen[strings.TrimSpace(nid)] = true
		}
	}

	pluginNodes, regularNodes := r.getNodesForPath(path, alreadySeen)

	r.log.Debug().Int("plugin_nodes", len(pluginNodes)).Int("regular_nodes", len(regularNodes)).Msg("Node separation")

	if len(pluginNodes) == 0 && len(regularNodes) == 0 {
		r.log.Info().Str("path", path).Msg("Endpoint not found")
		metrics.New().IncRequestTotal(path, r.config.NodeID, "404")
		http.Error(w, `{"error": "endpoint not found"}`, http.StatusNotFound)
		return nil
	}

	for attempt := 0; attempt <= maxRetries; attempt++ {
		if attempt > 0 {
			r.log.Warn().Int("attempt", attempt).Str("path", path).Msg("Retrying request after failure")
			sleepDuration := time.Duration(100*attempt*attempt) * time.Millisecond
			if sleepDuration > 2*time.Second {
				sleepDuration = 2 * time.Second
			}
			time.Sleep(sleepDuration)
		}

		selectedNode, isPluginRequest, err := r.selectNodeWithFallback(path, pluginNodes, regularNodes, attempt)
		if err != nil {
			r.log.Warn().Err(err).Str("path", path).Msg("Node selection failed")
			continue
		}

		if selectedNode == nil {
			r.log.Warn().Str("path", path).Msg("No healthy node found")
			continue
		}

		if selectedNode.NodeID == r.config.NodeID {
			if isPluginRequest {
				req.Header.Set("X-Plugin-Request", "true")
			}
			return selectedNode
		}

		if isPluginRequest {
			req.Header.Set("X-Plugin-Request", "true")
		}

		return selectedNode
	}

	metrics.New().IncRequestTotal(path, r.config.NodeID, "503")
	r.log.Error().Str("path", path).Msg("All retry attempts failed")
	http.Error(w, `{"error": "no healthy node"}`, http.StatusServiceUnavailable)
	return nil
}

// selectNodeWithFallback 选择节点，带有回退机制
func (r *Router) selectNodeWithFallback(path string, pluginNodes, regularNodes []*models.RemoteNode, attempt int) (*models.RemoteNode, bool, error) {
	var candidates []*models.RemoteNode
	isPluginRequest := false

	if len(pluginNodes) > 0 {
		candidates = pluginNodes
		isPluginRequest = true
	} else if len(regularNodes) > 0 {
		candidates = regularNodes
	} else {
		return nil, false, ErrNoHealthyNode
	}

	if attempt > 0 && len(candidates) > 1 {
		var filtered []*models.RemoteNode
		for _, n := range candidates {
			if n.NodeID != r.config.NodeID {
				filtered = append(filtered, n)
			}
		}
		if len(filtered) > 0 {
			candidates = filtered
		}
	}

	// 选择负载最低的节点（使用 balancer）
	selectedNode := r.balancer.SelectNode(candidates, path)

	if selectedNode == nil {
		return candidates[0], isPluginRequest, nil
	}

	return selectedNode, isPluginRequest, nil
}

// matchNodePath 判断请求路径是否匹配某个 nodePath
func matchNodePath(requestPath, nodePath string) bool {
	hasTrailingSlash := strings.HasSuffix(nodePath, "/")
	if hasTrailingSlash {
		return strings.HasPrefix(requestPath, nodePath) ||
			requestPath+"/" == nodePath ||
			requestPath == nodePath[:len(nodePath)-1]
	}
	return requestPath == nodePath || requestPath == nodePath+"/"
}

// getNodesForPath 获取路径对应的所有候选节点（本机 + 远程统一候选池）
// alreadySeen 中的节点 ID 会被排除，避免转发环路
func (r *Router) getNodesForPath(path string, alreadySeen map[string]bool) (pluginNodes, regularNodes []*models.RemoteNode) {
	// 1. 收集本机匹配的 endpoint，找最长匹配 nodePath
	bestMatchLen := 0
	bestNodePath := ""
	for _, ep := range r.registry.GetAllLocalEndpoints() {
		if !ep.Healthy {
			continue
		}
		if matchNodePath(path, ep.NodePath) && len(ep.NodePath) > bestMatchLen {
			bestMatchLen = len(ep.NodePath)
			bestNodePath = ep.NodePath
		}
	}

	if bestNodePath != "" {
		// 本机多个 endpoint 聚合成一个候选，与远程节点粒度一致
		var totalMaxConcurrent, totalActive, totalQueueLen int32
		hasPlugin := false
		hasRegular := false

		for _, ep := range r.registry.ListEndpointsByNodePath(bestNodePath) {
			if !ep.Healthy {
				continue
			}
			totalMaxConcurrent += ep.MaxConcurrent
			if r.poolMgr != nil {
				if requestPool, ok := r.poolMgr.GetPool(bestNodePath, ep.EpID); ok {
					m := requestPool.GetMetrics()
					totalActive += m.Active
					totalQueueLen += m.QueueLen
				}
			}
			if ep.Plugin {
				hasPlugin = true
			} else {
				hasRegular = true
			}
		}

		if totalMaxConcurrent > 0 && !alreadySeen[r.config.NodeID] {
			localNode := &models.RemoteNode{
				NodeID:      r.config.NodeID,
				Address:     "127.0.0.1",
				ServicePort: 0,
				ServicePath: "",
				NodePath:    bestNodePath,
				EndpointStates: map[string]*models.EndpointState{
					bestNodePath: {
						Healthy:       true,
						Active:        totalActive,
						QueueLen:      totalQueueLen,
						MaxConcurrent: totalMaxConcurrent,
						Plugin:        hasPlugin && !hasRegular,
					},
				},
			}
			if hasPlugin && !hasRegular {
				pluginNodes = append(pluginNodes, localNode)
			} else {
				regularNodes = append(regularNodes, localNode)
			}
		}
	}

	// 2. 收集远程节点中匹配的 endpoint，同样找最长匹配
	remoteBestMatchLen := 0
	for _, node := range r.registry.GetRemoteNodes() {
		for nodePath := range node.EndpointStates {
			if matchNodePath(path, nodePath) && len(nodePath) > remoteBestMatchLen {
				remoteBestMatchLen = len(nodePath)
			}
		}
	}

	if remoteBestMatchLen > 0 {
		for _, node := range r.registry.GetRemoteNodes() {
			// 跳过已经处理过该请求的节点，避免环路
			if alreadySeen[node.NodeID] {
				continue
			}
			for nodePath, state := range node.EndpointStates {
				if !state.Healthy {
					continue
				}
				if !matchNodePath(path, nodePath) || len(nodePath) != remoteBestMatchLen {
					continue
				}
				isPlugin := state.Plugin
				if desc, ok := node.EndpointDescriptions[nodePath]; ok {
					isPlugin = desc.Plugin
				}
				// 估算远程节点真实负载：
				// 用本机当前转发给该节点的飞行中请求数（localActive）直接替代增量估算，
				// 避免 gossip 广播间隔内 (localActive - snapshot) 持续累积导致高估
				localActive := r.poolMgr.GetRemoteActive(node.NodeID, nodePath)
				estimatedActive := state.Active + localActive
				if estimatedActive < 0 {
					estimatedActive = 0
				}
				forwardNode := &models.RemoteNode{
					NodeID:      node.NodeID,
					Address:     node.Address,
					ServicePort: node.ServicePort,
					ServicePath: "",
					NodePath:    nodePath,
					EndpointStates: map[string]*models.EndpointState{
						nodePath: {
							Healthy:       true,
							Active:        estimatedActive,
							QueueLen:      state.QueueLen,
							MaxConcurrent: state.MaxConcurrent,
							Plugin:        isPlugin,
						},
					},
				}
				if isPlugin {
					pluginNodes = append(pluginNodes, forwardNode)
				} else {
					regularNodes = append(regularNodes, forwardNode)
				}
			}
		}
	}

	r.log.Debug().
		Str("request_path", path).
		Str("local_best_match", bestNodePath).
		Int("local_candidates", len(regularNodes)+len(pluginNodes)).
		Msg("Collected candidates")
	return
}
