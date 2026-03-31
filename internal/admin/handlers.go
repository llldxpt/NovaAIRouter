package admin

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/prometheus/client_golang/prometheus/promhttp"

	"novaairouter/internal/models"
	"novaairouter/internal/utils"
)

// pluginHTTPClient 带超时的 HTTP client，用于插件消息转发
var pluginHTTPClient = &http.Client{
	Timeout: 30 * time.Second,
}

func (s *AdminServer) handleEndpoints(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		s.handleGetEndpoints(w, r)
	case http.MethodPost:
		s.handlePostEndpoints(w, r)
	case http.MethodDelete:
		s.handleDeleteEndpoints(w, r)
	default:
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
	}
}

func (s *AdminServer) handleLocal(w http.ResponseWriter, r *http.Request) {
	s.log.Info().Msg("Received local request")

	if r.Method != http.MethodGet {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	localEndpoints := s.registry.ListEndpoints()

	localNode := map[string]interface{}{
		"node_id":    s.config.NodeID,
		"healthy":    true,
		"endpoints":  []map[string]interface{}{},
	}

	for _, ep := range localEndpoints {
		localNode["endpoints"] = append(localNode["endpoints"].([]map[string]interface{}), map[string]interface{}{
			"register_path":   ep.NodePath,
			"service_path":   ep.ServicePath,
			"active":         ep.Active,
			"queue_len":      ep.QueueLen,
			"healthy":        ep.Healthy,
			"max_concurrent": ep.MaxConcurrent,
			"plugin":         ep.Plugin,
		})
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(localNode)
}

func (s *AdminServer) handleGlobal(w http.ResponseWriter, r *http.Request) {
	s.log.Info().Msg("Received global request")

	if r.Method != http.MethodGet {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	allNodes := []map[string]interface{}{}

	localEndpoints := s.registry.ListEndpoints()
	localNode := map[string]interface{}{
		"node_id":     s.config.NodeID,
		"healthy":     true,
		"path_infos": []map[string]interface{}{},
	}
	
	pathInfosLocal := make(map[string]map[string]interface{})
	for _, ep := range localEndpoints {
		if _, exists := pathInfosLocal[ep.NodePath]; !exists {
			pathInfosLocal[ep.NodePath] = map[string]interface{}{
				"path":            ep.NodePath,
				"service_path":    ep.ServicePath,
				"description":     ep.Description,
				"max_concurrent":  int32(0),
				"plugin":          false,
				"active":          int32(0),
				"queue_len":       int32(0),
				"healthy":         true,
			}
		}
		info := pathInfosLocal[ep.NodePath]
		if mc, ok := info["max_concurrent"].(int32); ok {
			info["max_concurrent"] = mc + ep.MaxConcurrent
		}
		if ep.Plugin {
			info["plugin"] = true
		}
		if active, ok := info["active"].(int32); ok {
			info["active"] = active + ep.Active
		}
		if ql, ok := info["queue_len"].(int32); ok {
			info["queue_len"] = ql + ep.QueueLen
		}
	}
	for _, info := range pathInfosLocal {
		localNode["path_infos"] = append(localNode["path_infos"].([]map[string]interface{}), info)
	}
	allNodes = append(allNodes, localNode)

	remoteNodes := s.registry.GetRemoteNodes()
	for _, node := range remoteNodes {
		nodeHealthy := time.Since(node.LastSeen) < s.config.HeartbeatTimeout
		remoteNode := map[string]interface{}{
			"node_id":     node.NodeID,
			"healthy":     nodeHealthy,
			"last_seen":   node.LastSeen,
			"path_infos": []map[string]interface{}{},
		}
		
		pathInfos := make(map[string]map[string]interface{})
		
		for path, desc := range node.EndpointDescriptions {
			pathInfos[path] = map[string]interface{}{
				"path":            path,
				"description":     desc.Description,
				"max_concurrent":  desc.MaxConcurrent,
				"plugin":          desc.Plugin,
				"active":          int32(0),
				"queue_len":       int32(0),
				"healthy":         true,
			}
		}
		
		for path, state := range node.EndpointStates {
			epHealthy := state.Healthy
			if info, exists := pathInfos[path]; exists {
				info["healthy"] = epHealthy
				if active, ok := info["active"].(int32); ok {
					info["active"] = active + state.Active
				}
				if ql, ok := info["queue_len"].(int32); ok {
					info["queue_len"] = ql + state.QueueLen
				}
				// max_concurrent 已从 EndpointDescriptions 中设置，不再累加
				if state.Plugin {
					info["plugin"] = true
				}
				if !state.Healthy {
					info["healthy"] = false
				}
			} else {
				pathInfos[path] = map[string]interface{}{
					"path":            path,
					"description":     "",
					"max_concurrent":  state.MaxConcurrent,
					"plugin":          state.Plugin,
					"active":          state.Active,
					"queue_len":       state.QueueLen,
					"healthy":         state.Healthy,
				}
			}
		}
		
		for _, info := range pathInfos {
			remoteNode["path_infos"] = append(remoteNode["path_infos"].([]map[string]interface{}), info)
		}
		allNodes = append(allNodes, remoteNode)
	}

	response := map[string]interface{}{
		"nodes": allNodes,
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(response)
}

func (s *AdminServer) handleGetEndpoints(w http.ResponseWriter, r *http.Request) {
	path := r.URL.Query().Get("path")
	healthyOnly := r.URL.Query().Get("healthy_only") == "true"

	response := models.EndpointsResponse{
		Endpoints: make(map[string]models.EndpointDetail),
		Timestamp: time.Now(),
	}

	localEndpoints := s.registry.ListEndpoints()
	remoteNodes := s.registry.GetRemoteNodes()
	s.log.Info().Int("remote_count", len(remoteNodes)).Msg("GetRemoteNodes result")
	for _, node := range remoteNodes {
		s.log.Info().Str("node_id", node.NodeID).Int("descriptions", len(node.EndpointDescriptions)).Msg("Remote node info")
	}

	if path != "" {
		detail := s.buildEndpointDetail(path, healthyOnly, localEndpoints, remoteNodes)
		if detail != nil {
			response.Endpoints[path] = *detail
		}
	} else {
		paths := make(map[string]bool)
		for _, ep := range localEndpoints {
			paths[ep.NodePath] = true
		}
		for _, node := range remoteNodes {
			for path := range node.EndpointDescriptions {
				paths[path] = true
			}
		}

		for p := range paths {
			detail := s.buildEndpointDetail(p, healthyOnly, localEndpoints, remoteNodes)
			if detail != nil {
				response.Endpoints[p] = *detail
			}
		}
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(response)
}

func (s *AdminServer) buildEndpointDetail(path string, healthyOnly bool, localEndpoints []*models.LocalEndpoint, remoteNodes []*models.RemoteNode) *models.EndpointDetail {
	var description string
	var nodes []models.EndpointNode

	for _, ep := range localEndpoints {
		if ep.NodePath == path {
			description = ep.Description
			if healthyOnly && !ep.Healthy {
				continue
			}
			address := fmt.Sprintf("%s:%d", utils.GetHost(s.config.ListenAddr), utils.GetPort(s.config.ListenAddr))
			nodes = append(nodes, models.EndpointNode{
				NodeID:        s.config.NodeID,
				Address:       address,
				Healthy:       ep.Healthy,
				Active:        ep.Active,
				QueueLen:      ep.QueueLen,
				Plugin:        ep.Plugin,
				MaxConcurrent: ep.MaxConcurrent,
			})
			break
		}
	}

	for _, node := range remoteNodes {
		hasDesc := false
		if _, ok := node.EndpointDescriptions[path]; ok {
			hasDesc = true
		}

		if state, ok := node.EndpointStates[path]; ok {
			if healthyOnly && !state.Healthy {
				continue
			}
			addr := fmt.Sprintf("%s:%d", node.Address, node.ServicePort)
			nodes = append(nodes, models.EndpointNode{
				NodeID:        node.NodeID,
				Address:       addr,
				Healthy:       state.Healthy,
				Active:        state.Active,
				QueueLen:      state.QueueLen,
				Plugin:        state.Plugin,
				MaxConcurrent: state.MaxConcurrent,
			})
		} else if hasDesc {
			addr := fmt.Sprintf("%s:%d", node.Address, node.ServicePort)
			nodes = append(nodes, models.EndpointNode{
				NodeID:        node.NodeID,
				Address:       addr,
				Healthy:       false,
				Active:        0,
				QueueLen:      0,
				Plugin:        false,
				MaxConcurrent: 0,
			})
		}
	}

	if len(nodes) == 0 && healthyOnly {
		return nil
	}

	return &models.EndpointDetail{
		Description: description,
		Nodes:       nodes,
	}
}

func (s *AdminServer) handlePostEndpoints(w http.ResponseWriter, r *http.Request) {
	s.log.Info().Msg("Received endpoint registration request")
	s.log.Info().Str("registry_addr", fmt.Sprintf("%p", s.registry)).Msg("Admin registry address")
	
	if !s.requireAPIKeyOrLocal(w, r) {
		s.log.Warn().Str("remote_addr", r.RemoteAddr).Msg("Rejected unauthenticated endpoint registration")
		return
	}

	body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, 1<<20)) // 1MB limit
	if err != nil {
		s.log.Error().Err(err).Msg("Failed to read request body")
		http.Error(w, "Bad request", http.StatusBadRequest)
		return
	}
	defer r.Body.Close()

	var endpoints []models.EndpointInfoRequest
	if err := json.Unmarshal(body, &endpoints); err != nil {
		s.log.Error().Err(err).Msg("Failed to parse JSON")
		http.Error(w, "Invalid JSON", http.StatusBadRequest)
		return
	}

	s.log.Info().Int("count", len(endpoints)).Msg("Registering endpoints")

	descriptions := make(map[string]models.EndpointMetadata)
	localEndpoints := make([]*models.LocalEndpoint, 0, len(endpoints))
	paths := make([]string, 0, len(endpoints))

	for _, ep := range endpoints {
		servicePath := ep.ServicePath
		nodePath := ep.NodePath

		s.log.Info().Str("service_path", servicePath).Str("node_path", nodePath).Msg("Parsed endpoint fields")

		if servicePath == "" || nodePath == "" {
			s.log.Error().Msg("service_path and node_path are required")
			http.Error(w, "service_path and node_path are required", http.StatusBadRequest)
			return
		}

		hasTrailingSlashService := strings.HasSuffix(servicePath, "/")
		hasTrailingSlashNode := strings.HasSuffix(nodePath, "/")
		if hasTrailingSlashService != hasTrailingSlashNode {
			s.log.Error().Str("service_path", servicePath).Str("node_path", nodePath).Msg("service_path and node_path trailing slash mismatch")
			http.Error(w, fmt.Sprintf("service_path and node_path trailing slash must match: service_path ends with / = %v, node_path ends with / = %v", hasTrailingSlashService, hasTrailingSlashNode), http.StatusBadRequest)
			return
		}

		maxConcurrent := int32(ep.MaxConcurrent)
		if maxConcurrent < 1 {
			maxConcurrent = 1
		}
		s.log.Info().Str("service_path", servicePath).Str("node_path", nodePath).Int32("max_concurrent", maxConcurrent).Bool("local_only", ep.LocalOnly).Bool("plugin", ep.Plugin).Msg("Registering endpoint")
		localEp := &models.LocalEndpoint{
			ServicePath:   servicePath,
			NodePath:      nodePath,
			MaxConcurrent: maxConcurrent,
			Description:   ep.Description,
			Healthy:       true,
			LastHeartbeat: time.Now(),
			LocalOnly:     ep.LocalOnly,
			Plugin:        ep.Plugin,
		}
		localEndpoints = append(localEndpoints, localEp)
		paths = append(paths, nodePath)

		descriptions[nodePath] = models.EndpointMetadata{
			ServicePath:   servicePath,
			NodePath:      nodePath,
			MaxConcurrent: maxConcurrent,
			Description:   ep.Description,
			Plugin:        ep.Plugin,
		}
	}

	serviceID, err := s.registry.RegisterEndpoints(localEndpoints)
	if err != nil {
		s.log.Error().Err(err).Msg("Failed to register endpoints")
		http.Error(w, err.Error(), http.StatusConflict)
		return
	}

	for nodePath, desc := range descriptions {
		desc.ServiceID = serviceID
		descriptions[nodePath] = desc
	}
	
	// 在注册端点时同时创建对应的 pool
	s.log.Info().Bool("poolMgr_nil", s.poolMgr == nil).Msg("Checking poolMgr")
	for _, ep := range localEndpoints {
		// 解析 service_path 获取端口和路径
		// service_path 格式: "18001/v1/" 或 "18001/v1" 或 "18001"
		parts := strings.SplitN(ep.ServicePath, "/", 2)
		
		var servicePort string
		var servicePathBase string
		
		if len(parts) >= 2 {
			servicePort = parts[0]
			servicePathBase = "/" + parts[1]
		} else {
			// 只有端口，没有路径
			servicePort = ep.ServicePath
			servicePathBase = "/"
		}
		
		port, err := strconv.Atoi(servicePort)
		if err != nil {
			s.log.Warn().Str("service_port", servicePort).Msg("Invalid port in service_path, skipping pool creation")
			continue
		}
		
		targetURL := fmt.Sprintf("http://localhost:%d%s", port, servicePathBase)

		// 使用 poolMgr 创建 pool，设置正确的 maxConcurrent
		if s.poolMgr != nil {
			s.poolMgr.CreatePool(ep.NodePath, ep.ServiceID, targetURL, int(ep.MaxConcurrent))

			// 设置 metrics 回调，实时更新 endpoint 状态
			if requestPool, ok := s.poolMgr.GetPool(ep.NodePath, ep.ServiceID); ok {
				nodePath := ep.NodePath
				requestPool.SetMetricsCallback(func(active, queueLen int32) {
					s.registry.UpdateEndpointMetrics(nodePath, active, queueLen)
				})
			}

			s.log.Info().Str("node_path", ep.NodePath).Str("service_id", ep.ServiceID).Str("target_url", targetURL).Int32("max_concurrent", ep.MaxConcurrent).Msg("Created pool for endpoint")
		}
	}
	
	for _, ep := range localEndpoints {
		if ep, ok := s.registry.GetEndpoint(ep.NodePath); ok {
			s.log.Info().Str("node_path", ep.NodePath).Bool("healthy", ep.Healthy).Msg("Endpoint registered successfully")
		} else {
			s.log.Error().Str("node_path", ep.NodePath).Msg("Failed to register endpoint")
		}
	}

	// Broadcast endpoint metadata to other nodes via gossip
	if s.gossip != nil {
		s.gossip.BroadcastSync()
		s.log.Info().Msg("Broadcasted local state to cluster")
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(models.EndpointRegistrationResponse{
		ServiceID: serviceID,
		Endpoints: paths,
	})
}

func (s *AdminServer) handleDeleteEndpoints(w http.ResponseWriter, r *http.Request) {
	s.log.Info().Msg("Received endpoint deletion request")
	
	if !s.requireAPIKeyOrLocal(w, r) {
		s.log.Warn().Str("remote_addr", r.RemoteAddr).Msg("Rejected unauthenticated endpoint deletion")
		return
	}

	serviceID := r.URL.Query().Get("service_id")
	if serviceID == "" {
		s.log.Error().Msg("service_id is required for endpoint deletion")
		http.Error(w, "service_id is required", http.StatusBadRequest)
		return
	}

	decodedServiceID, err := url.QueryUnescape(serviceID)
	if err != nil {
		s.log.Error().Err(err).Str("service_id", serviceID).Msg("Invalid service_id")
		http.Error(w, "Invalid service_id", http.StatusBadRequest)
		return
	}

	// 在删除前收集受影响的 nodePath 和 serviceID，用于清理 pool
	queryNodePath := r.URL.Query().Get("path")
	type poolKey struct {
		path      string
		serviceID string
	}
	var deletedPools []poolKey
	for _, ep := range s.registry.ListEndpoints() {
		if ep.ServiceID == decodedServiceID {
			if queryNodePath == "" || ep.NodePath == queryNodePath {
				deletedPools = append(deletedPools, poolKey{path: ep.NodePath, serviceID: ep.ServiceID})
			}
		}
	}

	nodePath := r.URL.Query().Get("path")
	if nodePath != "" {
		decodedNodePath, err := url.QueryUnescape(nodePath)
		if err != nil {
			s.log.Error().Err(err).Str("path", nodePath).Msg("Invalid path")
			http.Error(w, "Invalid path", http.StatusBadRequest)
			return
		}
		s.log.Info().Str("service_id", decodedServiceID).Str("path", decodedNodePath).Msg("Deleting single endpoint by service_id and path")
		removedCount := s.registry.RemoveByServiceIDAndPath(decodedServiceID, decodedNodePath)
		s.log.Info().Int("removed", removedCount).Msg("Endpoint removed by service_id and path")
	} else {
		s.log.Info().Str("service_id", decodedServiceID).Msg("Deleting all endpoints by service_id")
		removedCount := s.registry.RemoveByServiceID(decodedServiceID)
		s.log.Info().Int("removed", removedCount).Msg("Endpoints removed by service_id")
	}

	// 清理已删除端点的 pool
	if s.poolMgr != nil {
		for _, pk := range deletedPools {
			s.poolMgr.RemovePool(pk.path, pk.serviceID)
			s.log.Info().Str("path", pk.path).Str("serviceID", pk.serviceID).Msg("Removed pool for deleted endpoint")
		}
	}

	if s.gossip != nil {
		s.gossip.BroadcastSync()
		if len(deletedPools) > 0 {
			deletedPaths := make([]string, len(deletedPools))
			for i, pk := range deletedPools {
				deletedPaths[i] = pk.path
			}
			s.gossip.BroadcastDeletedPaths(deletedPaths)
		}
		s.log.Info().Msg("Broadcasted local state to cluster after endpoint deletion")
	}

	w.WriteHeader(http.StatusOK)
}

func (s *AdminServer) handleHeartbeat(w http.ResponseWriter, r *http.Request) {
	s.log.Info().Msg("Received heartbeat request")
	
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	if !s.requireAPIKeyOrLocal(w, r) {
		s.log.Warn().Str("remote_addr", r.RemoteAddr).Msg("Rejected unauthenticated heartbeat")
		return
	}

	body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, 1<<20)) // 1MB limit
	if err != nil {
		s.log.Error().Err(err).Msg("Failed to read heartbeat body")
		http.Error(w, "Bad request", http.StatusBadRequest)
		return
	}
	defer r.Body.Close()

	var hb models.HeartbeatRequest
	if err := json.Unmarshal(body, &hb); err != nil {
		s.log.Error().Err(err).Msg("Failed to parse heartbeat JSON")
		http.Error(w, "Invalid JSON", http.StatusBadRequest)
		return
	}

	s.log.Info().Str("service_id", hb.ServiceID).Bool("healthy", hb.Healthy).Msg("Updating heartbeat")
	if err := s.registry.UpdateHeartbeat(hb.ServiceID, hb.Healthy); err != nil {
		s.log.Warn().Err(err).Msg("Failed to update heartbeat")
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	w.WriteHeader(http.StatusOK)
}

func (s *AdminServer) handleNodes(w http.ResponseWriter, r *http.Request) {
	s.log.Info().Msg("Received nodes request")

	if r.Method != http.MethodGet {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	nodeIDs := []string{s.config.NodeID}

	// 从 gossip sync 获取远程节点
	if s.gossip != nil {
		for _, peer := range s.gossip.GetNodes() {
			nodeIDs = append(nodeIDs, peer.NodeID)
		}
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]interface{}{
		"nodes": nodeIDs,
	})
}

func (s *AdminServer) handleNodesWithAuth(w http.ResponseWriter, r *http.Request) {
	if !s.requireAPIKeyOrLocal(w, r) {
		return
	}
	s.handleNodes(w, r)
}

func (s *AdminServer) handleLocalWithAuth(w http.ResponseWriter, r *http.Request) {
	if !s.requireAPIKeyOrLocal(w, r) {
		return
	}
	s.handleLocal(w, r)
}

func (s *AdminServer) handleGlobalWithAuth(w http.ResponseWriter, r *http.Request) {
	if !s.requireAPIKeyOrLocal(w, r) {
		return
	}
	s.handleGlobal(w, r)
}

func (s *AdminServer) handleNodeByIDWithAuth(w http.ResponseWriter, r *http.Request) {
	if !s.requireAPIKeyOrLocal(w, r) {
		return
	}
	s.handleNodeByID(w, r)
}

func (s *AdminServer) handleMetricsWithAuth(w http.ResponseWriter, r *http.Request) {
	if !s.requireAPIKeyOrLocal(w, r) {
		return
	}
	promhttp.Handler().ServeHTTP(w, r)
}

func (s *AdminServer) handleNodeByID(w http.ResponseWriter, r *http.Request) {
	s.log.Info().Msg("Received node by ID request")

	if r.Method != http.MethodGet {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	nodeID := strings.TrimPrefix(r.URL.Path, "/v1/node/")
	if nodeID == "" {
		http.Error(w, "Node ID required", http.StatusBadRequest)
		return
	}

	if nodeID == s.config.NodeID {
		localEndpoints := s.registry.ListEndpoints()
		localNode := map[string]interface{}{
			"node_id":       s.config.NodeID,
			"address":       utils.GetHost(s.config.ListenAddr),
			"service_port":  utils.GetPort(s.config.ListenAddr),
			"healthy":       true,
			"endpoints":     []map[string]interface{}{},
		}
		for _, ep := range localEndpoints {
			localNode["endpoints"] = append(localNode["endpoints"].([]map[string]interface{}), map[string]interface{}{
				"path":            ep.NodePath,
				"service_path":   ep.ServicePath,
				"active":          ep.Active,
				"queue_len":       ep.QueueLen,
				"healthy":         ep.Healthy,
				"max_concurrent":  ep.MaxConcurrent,
				"plugin":          ep.Plugin,
			})
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(localNode)
		return
	}

	remoteNodes := s.registry.GetRemoteNodes()
	for _, node := range remoteNodes {
		if node.NodeID == nodeID {
			remoteNode := map[string]interface{}{
				"node_id":       node.NodeID,
				"address":       node.Address,
				"service_port":  node.ServicePort,
				"healthy":       true,
				"endpoints":     []map[string]interface{}{},
			}
			for path, state := range node.EndpointStates {
				remoteNode["endpoints"] = append(remoteNode["endpoints"].([]map[string]interface{}), map[string]interface{}{
					"path":            path,
					"active":          state.Active,
					"queue_len":       state.QueueLen,
					"healthy":         state.Healthy,
					"max_concurrent":  state.MaxConcurrent,
					"plugin":          state.Plugin,
				})
			}
			for path := range node.EndpointDescriptions {
				// 跳过已在 EndpointStates 中存在的端点，避免重复
				if _, exists := node.EndpointStates[path]; exists {
					continue
				}
				remoteNode["endpoints"] = append(remoteNode["endpoints"].([]map[string]interface{}), map[string]interface{}{
					"path":            path,
					"active":          0,
					"queue_len":       0,
					"healthy":         false,
					"max_concurrent":  0,
					"plugin":          false,
				})
			}
			w.Header().Set("Content-Type", "application/json")
			json.NewEncoder(w).Encode(remoteNode)
			return
		}
	}

	http.Error(w, "Node not found", http.StatusNotFound)
}

// handleHealth 健康检查处理函数
func (s *AdminServer) handleHealth(w http.ResponseWriter, r *http.Request) {
	w.WriteHeader(http.StatusOK)
	w.Write([]byte("OK"))
}

func (s *AdminServer) handlePluginPeers(w http.ResponseWriter, r *http.Request) {
	s.log.Info().Msg("Received plugin peers request")

	if r.Method != http.MethodGet && r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	var req struct {
		ServiceID string `json:"service_id"`
	}

	if r.Method == http.MethodPost {
		body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, 1<<20)) // 1MB limit
		if err != nil {
			http.Error(w, "Failed to read request body", http.StatusBadRequest)
			return
		}
		defer r.Body.Close()

		if err := json.Unmarshal(body, &req); err != nil {
			http.Error(w, "Invalid JSON", http.StatusBadRequest)
			return
		}
	} else {
		req.ServiceID = r.URL.Query().Get("service_id")
	}

	if req.ServiceID == "" {
		http.Error(w, "service_id is required", http.StatusBadRequest)
		return
	}

	s.log.Info().Str("service_id", req.ServiceID).Msg("Finding peers for service")

	var targetNodePath string
	var targetServiceID string

	for _, ep := range s.registry.ListEndpoints() {
		if ep.ServiceID == req.ServiceID {
			targetNodePath = ep.NodePath
			targetServiceID = ep.ServiceID
			break
		}
	}

	if targetNodePath == "" {
		for _, node := range s.registry.GetRemoteNodes() {
			for path, meta := range node.EndpointDescriptions {
				if meta.ServiceID == req.ServiceID {
					targetNodePath = path
					targetServiceID = meta.ServiceID
					break
				}
			}
			if targetNodePath != "" {
				break
			}
		}
	}

	if targetNodePath == "" {
		http.Error(w, "service not found", http.StatusNotFound)
		return
	}

	s.log.Info().Str("node_path", targetNodePath).Msg("Found node path for service")

	peers := make([]map[string]string, 0)

	for _, ep := range s.registry.ListEndpoints() {
		if ep.NodePath == targetNodePath && ep.Plugin && ep.ServiceID != targetServiceID {
			peers = append(peers, map[string]string{"service_id": ep.ServiceID})
		}
	}

	for _, node := range s.registry.GetRemoteNodes() {
		for path, meta := range node.EndpointDescriptions {
			if path == targetNodePath && meta.Plugin && meta.ServiceID != targetServiceID {
				peers = append(peers, map[string]string{"service_id": meta.ServiceID})
			}
		}
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]interface{}{
		"service_id": targetServiceID,
		"node_path":  targetNodePath,
		"peers":      peers,
	})
}

func (s *AdminServer) handlePluginSend(w http.ResponseWriter, r *http.Request) {
	s.log.Info().Msg("Received plugin send request")

	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, 10<<20)) // 10MB limit
	if err != nil {
		http.Error(w, "Failed to read request body", http.StatusBadRequest)
		return
	}
	defer r.Body.Close()

	var req struct {
		ToServiceID string          `json:"to_service_id"`
		Message     json.RawMessage `json:"message"`
	}

	if err := json.Unmarshal(body, &req); err != nil {
		http.Error(w, "Invalid JSON", http.StatusBadRequest)
		return
	}

	if req.ToServiceID == "" {
		http.Error(w, "to_service_id is required", http.StatusBadRequest)
		return
	}

	s.log.Info().Str("to_service_id", req.ToServiceID).Msg("Finding target service")

	var targetAddress string
	var targetPort int

	for _, ep := range s.registry.ListEndpoints() {
		if ep.ServiceID == req.ToServiceID {
			targetAddress = utils.GetHost(s.config.ListenAddr)
			targetPort = utils.ExtractPortFromServicePath(ep.ServicePath)
			break
		}
	}

	if targetAddress == "" || targetPort == 0 {
		for _, node := range s.registry.GetRemoteNodes() {
			for _, meta := range node.EndpointDescriptions {
				if meta.ServiceID == req.ToServiceID {
					targetAddress = node.Address
					targetPort = node.ServicePort
					break
				}
			}
			if targetAddress != "" {
				break
			}
		}
	}

	if targetAddress == "" || targetPort == 0 {
		http.Error(w, "service not found", http.StatusNotFound)
		return
	}

	s.log.Info().Str("address", targetAddress).Int("port", targetPort).Msg("Forwarding message to target")

	url := fmt.Sprintf("http://%s:%d/v1/plugin/receive", targetAddress, targetPort)
	resp, err := pluginHTTPClient.Post(url, "application/json", bytes.NewReader(body))
	if err != nil {
		s.log.Error().Err(err).Msg("Failed to forward message")
		http.Error(w, err.Error(), http.StatusBadGateway)
		return
	}
	defer resp.Body.Close()

	w.Header().Set("Content-Type", "application/json")
	io.Copy(w, resp.Body)
}

func (s *AdminServer) handlePluginBroadcast(w http.ResponseWriter, r *http.Request) {
	s.log.Info().Msg("Received plugin broadcast request")

	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, 10<<20)) // 10MB limit
	if err != nil {
		http.Error(w, "Failed to read request body", http.StatusBadRequest)
		return
	}
	defer r.Body.Close()

	var req struct {
		Message json.RawMessage `json:"message"`
	}

	if err := json.Unmarshal(body, &req); err != nil {
		http.Error(w, "Invalid JSON", http.StatusBadRequest)
		return
	}

	var senderServiceID string
	var msgMap map[string]interface{}
	if err := json.Unmarshal(req.Message, &msgMap); err == nil {
		if sid, ok := msgMap["from"].(string); ok {
			senderServiceID = sid
		}
	}

	pluginServiceIDs := make(map[string]bool)

	for _, ep := range s.registry.ListEndpoints() {
		if ep.Plugin && ep.ServiceID != senderServiceID {
			pluginServiceIDs[ep.ServiceID] = true
		}
	}

	for _, node := range s.registry.GetRemoteNodes() {
		for _, meta := range node.EndpointDescriptions {
			if meta.Plugin && meta.ServiceID != senderServiceID {
				pluginServiceIDs[meta.ServiceID] = true
			}
		}
	}

	s.log.Info().Int("count", len(pluginServiceIDs)).Msg("Broadcasting to plugins")

	successCount := 0
	for serviceID := range pluginServiceIDs {
		var targetAddress string
		var targetPort int

		for _, ep := range s.registry.ListEndpoints() {
			if ep.ServiceID == serviceID {
				targetAddress = utils.GetHost(s.config.ListenAddr)
				targetPort = utils.ExtractPortFromServicePath(ep.ServicePath)
				break
			}
		}

		if targetAddress == "" || targetPort == 0 {
			for _, node := range s.registry.GetRemoteNodes() {
				for _, meta := range node.EndpointDescriptions {
					if meta.ServiceID == serviceID {
						targetAddress = node.Address
						targetPort = node.ServicePort
						break
					}
				}
				if targetAddress != "" {
					break
				}
			}
		}

		if targetAddress == "" || targetPort == 0 {
			continue
		}

		url := fmt.Sprintf("http://%s:%d/v1/plugin/receive", targetAddress, targetPort)
		resp, err := pluginHTTPClient.Post(url, "application/json", bytes.NewReader(body))
		if err != nil {
			s.log.Warn().Str("service_id", serviceID).Err(err).Msg("Failed to broadcast to plugin")
			continue
		}
		resp.Body.Close()
		successCount++
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]interface{}{
		"status":    "ok",
		"recipients": successCount,
	})
}

func (s *AdminServer) handleConnect(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, 1<<20))
	if err != nil {
		http.Error(w, "Failed to read request body", http.StatusBadRequest)
		return
	}
	defer r.Body.Close()

	var req struct {
		IP string `json:"ip"`
	}
	if err := json.Unmarshal(body, &req); err != nil {
		http.Error(w, "Invalid JSON", http.StatusBadRequest)
		return
	}

	if req.IP == "" {
		http.Error(w, "ip is required", http.StatusBadRequest)
		return
	}

	// 计算 gossip 端口: base_port + 1
	var basePort int
	fmt.Sscanf(s.config.ListenAddr, ":%d", &basePort)
	if basePort == 0 {
		basePort = 15050
	}
	gossipPort := basePort + 1

	// 向远程节点的 gossip 端口发送 join 请求
	joinURL := fmt.Sprintf("http://%s:%d/v1/gossip/join", req.IP, gossipPort)
	joinBody, _ := json.Marshal(map[string]string{
		"node_id": s.config.NodeID,
	})

	s.log.Info().Str("ip", req.IP).Str("url", joinURL).Msg("Connecting to remote node")

	client := &http.Client{Timeout: 10 * time.Second}
	resp, err := client.Post(joinURL, "application/json", bytes.NewReader(joinBody))
	if err != nil {
		s.log.Error().Err(err).Str("ip", req.IP).Msg("Failed to connect to remote node")
		http.Error(w, "Failed to connect to remote node: "+err.Error(), http.StatusBadGateway)
		return
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		respBody, _ := io.ReadAll(resp.Body)
		s.log.Error().Int("status", resp.StatusCode).Str("body", string(respBody)).Msg("Remote node returned error")
		http.Error(w, "Remote node returned error: "+string(respBody), http.StatusBadGateway)
		return
	}

	// 解析远程节点的响应
	var joinResp struct {
		NodeID  string `json:"node_id"`
		Healthy bool   `json:"healthy"`
	}

	if err := json.NewDecoder(resp.Body).Decode(&joinResp); err != nil {
		s.log.Error().Err(err).Msg("Failed to decode join response")
		http.Error(w, "Failed to decode remote response", http.StatusInternalServerError)
		return
	}

	// 将远程节点加入本地集群
	if s.gossip != nil && joinResp.NodeID != "" {
		s.gossip.JoinNode(joinResp.NodeID, req.IP)
		s.log.Info().Str("remote_node_id", joinResp.NodeID).Str("ip", req.IP).Msg("Remote node joined successfully")
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]string{
		"remote_node_id": joinResp.NodeID,
	})
}
