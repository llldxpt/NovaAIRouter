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
	"novaairouter/internal/registry"
	"novaairouter/internal/utils"
)

const maxRetries = 2

// Router 路由管理器
type Router struct {
	config   *config.Config
	registry *registry.Registry
	log      zerolog.Logger
	balancer *balancer.Balancer
}

// New 创建新的路由管理器
func New(cfg *config.Config, reg *registry.Registry, log zerolog.Logger) *Router {
	return &Router{
		config:   cfg,
		registry: reg,
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

	pluginNodes, regularNodes := r.getNodesForPath(path)

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

// getNodesForPath 获取路径对应的节点
func (r *Router) getNodesForPath(path string) (pluginNodes, regularNodes []*models.RemoteNode) {
	registeredEndpoints := r.registry.GetAllLocalEndpoints()
	
	// 选择最长匹配的 endpoint
	var bestMatch *models.LocalEndpoint
	bestMatchLen := 0
	
	for _, ep := range registeredEndpoints {
		if !ep.Healthy {
			continue
		}

		nodePath := ep.NodePath
		hasTrailingSlash := strings.HasSuffix(nodePath, "/")

		matched := false
		if hasTrailingSlash {
			matched = strings.HasPrefix(path, nodePath) || path+"/" == nodePath || path == nodePath[:len(nodePath)-1]
		} else {
			matched = path == nodePath || path == nodePath+"/"
		}

		if matched && len(nodePath) > bestMatchLen {
			bestMatch = ep
			bestMatchLen = len(nodePath)
		}
	}
	
	// 如果找到最佳匹配，使用它
	if bestMatch != nil {
		ep := bestMatch
		nodePath := ep.NodePath
		servicePath := ep.ServicePath
		
		r.log.Debug().Str("request_path", path).Str("best_match", nodePath).Int("len", bestMatchLen).Msg("Selected best matching endpoint")
		
		targetPort := utils.ExtractPortFromServicePath(servicePath)
		if targetPort > 0 {
			serviceBasePath := utils.ExtractBaseFromServicePath(servicePath)

			localNode := &models.RemoteNode{
				NodeID:        r.config.NodeID,
				Address:       "127.0.0.1",
				ServicePort:   targetPort,
				ServicePath:   serviceBasePath,
				NodePath:      nodePath,
				EndpointStates: map[string]*models.EndpointState{
					nodePath: {
						Healthy:      ep.Healthy,
						MaxConcurrent: ep.MaxConcurrent,
						Plugin:        ep.Plugin,
					},
				},
			}

			if ep.Plugin {
				pluginNodes = append(pluginNodes, localNode)
			} else {
				regularNodes = append(regularNodes, localNode)
			}
		}
		
		return
	}

	remoteNodes := r.registry.GetRemoteNodes()
	for _, node := range remoteNodes {
		for nodePath, state := range node.EndpointStates {
			if !state.Healthy {
				continue
			}

			hasTrailingSlash := strings.HasSuffix(nodePath, "/")

			matched := false
			if hasTrailingSlash {
				matched = strings.HasPrefix(path, nodePath) || path+"/" == nodePath || path == nodePath[:len(nodePath)-1]
			} else {
				matched = path == nodePath || path == nodePath+"/"
			}

			if matched {
				isPlugin := false
				servicePath := ""
				if desc, ok := node.EndpointDescriptions[nodePath]; ok {
					isPlugin = desc.Plugin
					if desc.ServicePath != "" {
						servicePath = desc.ServicePath
					}
				} else if state.Plugin {
					isPlugin = true
				}

				targetPort := node.ServicePort
				serviceBasePath := utils.ExtractBaseFromServicePath(servicePath)

				forwardNode := &models.RemoteNode{
					NodeID:        node.NodeID,
					Address:       node.Address,
					ServicePort:   targetPort,
					ServicePath:   serviceBasePath,
					NodePath:      nodePath,
				}
				if isPlugin {
					pluginNodes = append(pluginNodes, forwardNode)
				} else {
					regularNodes = append(regularNodes, forwardNode)
				}
			}
		}
	}
	return
}
