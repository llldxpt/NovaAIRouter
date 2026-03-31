package gossip

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/rs/zerolog"

	"novaairouter/internal/config"
	"novaairouter/internal/gossip/discovery"
	"novaairouter/internal/gossip/message"
	syncsvc "novaairouter/internal/gossip/sync"
	"novaairouter/internal/registry"
)

// GossipServer Gossip服务
type GossipServer struct {
	config     *config.Config
	log        zerolog.Logger
	registry   *registry.Registry
	discovery  *discovery.Discovery
	sync       *syncsvc.Sync
	server     *http.Server
	mu         sync.RWMutex
	latestConfig *config.Config
	done         chan struct{}
}

// New 创建新的Gossip服务
func New(cfg *config.Config, log zerolog.Logger, reg *registry.Registry) (*GossipServer, error) {
	g := &GossipServer{
		config:       cfg,
		log:          log,
		registry:     reg,
		latestConfig: cfg,
		done:         make(chan struct{}),
	}

	discoverySvc := discovery.New(cfg, log)
	discoverySvc.OnJoinNode(func(nodeID, address string) {
		g.JoinNode(nodeID, address)
	})
	syncSvc := syncsvc.New(cfg, log, reg)

	g.discovery = discoverySvc
	g.sync = syncSvc

	return g, nil
}

func (g *GossipServer) requireGossipAuth(w http.ResponseWriter, r *http.Request) bool {
	if g.config.APIKey == "" || g.config.DisableAdminAuth {
		return true
	}
	apiKey := r.Header.Get("X-API-Key")
	if apiKey != g.config.APIKey {
		http.Error(w, "Unauthorized", http.StatusUnauthorized)
		return false
	}
	return true
}

func (g *GossipServer) GetClusterSize() int {
	return g.sync.GetClusterSize()
}

func (g *GossipServer) Shutdown() {
	// 广播节点下线消息，让其他节点及时清理
	g.sync.BroadcastNodeLeave()

	// 关闭 done channel 停止所有后台 goroutine
	close(g.done)

	if g.server != nil {
		g.server.Close()
	}
	if g.discovery != nil {
		g.discovery.Stop()
	}
	g.log.Info().Msg("Gossip server stopped")
}

func (g *GossipServer) AddPeer(nodeID, address string) {
	g.sync.AddPeer(nodeID, address)
}

func (g *GossipServer) RemovePeer(nodeID string) {
	g.sync.RemovePeer(nodeID)
}

func (g *GossipServer) GetNodes() []*syncsvc.Peer {
	return g.sync.GetNodes()
}

func (g *GossipServer) Start() error {
	g.log.Info().
		Str("node_id", g.config.NodeID).
		Msg("Starting HTTP gossip server")

	bindPort := 0
	if idx := strings.LastIndex(g.config.ListenAddr, ":"); idx > 0 {
		fmt.Sscanf(g.config.ListenAddr[idx:], ":%d", &bindPort)
	}
	if bindPort == 0 {
		bindPort = 15050
	}
	gossipPort := bindPort + 1

	mux := http.NewServeMux()
	mux.HandleFunc("/health", func(w http.ResponseWriter, r *http.Request) {
		g.log.Info().Str("path", r.URL.Path).Msg("Received health request")
		w.Write([]byte("ok"))
	})
	mux.HandleFunc("/v1/gossip/join", g.handleGossipJoin)
	mux.HandleFunc("/v1/gossip/sync", g.handleGossipSync)
	mux.HandleFunc("/v1/gossip/peer", g.handleGossipSync)
	mux.HandleFunc("/v1/gossip/state", g.handleGossipState)
	mux.HandleFunc("/v1/gossip/nodes", g.handleGossipNodes)
	mux.HandleFunc("/v1/gossip/config", g.handleGossipConfig)
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		g.log.Info().Str("path", r.URL.Path).Msg("Unknown request")
		http.NotFound(w, r)
	})

	addr := fmt.Sprintf("0.0.0.0:%d", gossipPort)
	g.server = &http.Server{Addr: addr, Handler: mux}

	go func() {
		if err := g.server.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			g.log.Error().Err(err).Msg("Gossip server error")
		} else {
			g.log.Info().Msg("Gossip HTTP server stopped")
		}
	}()

	time.Sleep(100 * time.Millisecond)
	g.log.Info().Str("addr", addr).Str("server", fmt.Sprintf("%v", g.server)).Msg("Gossip HTTP server started")

	return nil
}

func (g *GossipServer) handleGossipJoin(w http.ResponseWriter, r *http.Request) {
	if !g.requireGossipAuth(w, r) {
		return
	}
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	var req struct {
		NodeID string `json:"node_id"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "Invalid request", http.StatusBadRequest)
		return
	}

	if req.NodeID == "" {
		http.Error(w, "node_id is required", http.StatusBadRequest)
		return
	}

	remoteIP, _, _ := net.SplitHostPort(r.RemoteAddr)

	g.log.Info().
		Str("node_id", req.NodeID).
		Str("remote_ip", remoteIP).
		Msg("Received gossip join request")

	g.AddPeer(req.NodeID, remoteIP)

	// 获取本地路径信息用于响应
	localPathInfos := g.getLocalPathInfos()

	// 同时向请求节点发送本地状态，实现双向同步
	// 这样即使UDP discovery是单向的，两个节点也能互相发现
	go g.sendSyncToPeer(remoteIP, req.NodeID)

	response := struct {
		NodeID     string              `json:"node_id"`
		Healthy    bool                `json:"healthy"`
		PathInfos  []message.PathInfo  `json:"path_infos"`
	}{
		NodeID:    g.config.NodeID,
		Healthy:   true,
		PathInfos: localPathInfos,
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(response)
}

func (g *GossipServer) getLocalPathInfos() []message.PathInfo {
	endpoints := g.registry.ListEndpoints()
	pathInfos := make([]message.PathInfo, 0, len(endpoints))
	for _, ep := range endpoints {
		pathInfos = append(pathInfos, message.PathInfo{
			ServiceID:     ep.ServiceID,
			ServicePath:   ep.ServicePath,
			NodePath:      ep.NodePath,
			Active:        ep.Active,
			QueueLen:      ep.QueueLen,
			Healthy:       ep.Healthy,
			MaxConcurrent: ep.MaxConcurrent,
			Plugin:        ep.Plugin,
		})
	}
	return pathInfos
}

func (g *GossipServer) handleGossipState(w http.ResponseWriter, r *http.Request) {
	if !g.requireGossipAuth(w, r) {
		return
	}
	if r.Method == http.MethodGet {
		nodeID := r.URL.Query().Get("node_id")
		if nodeID == "" {
			http.Error(w, "node_id is required", http.StatusBadRequest)
			return
		}

		// 从sync模块获取节点信息
		nodes := g.sync.GetNodes()
		var peer *syncsvc.Peer
		for _, p := range nodes {
			if p.NodeID == nodeID {
				peer = p
				break
			}
		}

		if peer == nil {
			http.Error(w, "Node not found", http.StatusNotFound)
			return
		}

		remoteNodes := g.registry.GetRemoteNodes()
		var pathInfos []message.PathInfo
		for _, rn := range remoteNodes {
			if rn.NodeID == nodeID {
				for path, ep := range rn.EndpointStates {
					meta := rn.EndpointDescriptions[path]
					pathInfos = append(pathInfos, message.PathInfo{
						NodePath:      path,
						ServicePath:   meta.ServicePath,
						Active:        ep.Active,
						QueueLen:      ep.QueueLen,
						Healthy:       ep.Healthy,
						MaxConcurrent: ep.MaxConcurrent,
						Plugin:        ep.Plugin,
					})
				}
				break
			}
		}

		response := struct {
			NodeID    string              `json:"node_id"`
			Healthy   bool                `json:"healthy"`
			PathInfos []message.PathInfo  `json:"path_infos"`
		}{
			NodeID:    peer.NodeID,
			Healthy:   true,
			PathInfos: pathInfos,
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(response)
		return
	}

	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	var msg message.GossipMessage
	if err := json.NewDecoder(r.Body).Decode(&msg); err != nil {
		http.Error(w, "Invalid request", http.StatusBadRequest)
		return
	}

	g.sync.HandleStateMessage(msg)
}

func (g *GossipServer) handleGossipNodes(w http.ResponseWriter, r *http.Request) {
	if !g.requireGossipAuth(w, r) {
		return
	}
	if r.Method != http.MethodGet {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	nodes := g.sync.GetNodes()
	nodeIDs := make([]string, 0, len(nodes)+1)
	nodeIDs = append(nodeIDs, g.config.NodeID)
	for _, node := range nodes {
		nodeIDs = append(nodeIDs, node.NodeID)
	}

	response := struct {
		Nodes []string `json:"nodes"`
	}{
		Nodes: nodeIDs,
	}
	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(response); err != nil {
		g.log.Error().Err(err).Msg("Failed to encode nodes response")
		http.Error(w, "Internal Server Error", http.StatusInternalServerError)
	}
}

func (g *GossipServer) handleGossipSync(w http.ResponseWriter, r *http.Request) {
	if !g.requireGossipAuth(w, r) {
		return
	}
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	var msg message.GossipMessage
	if err := json.NewDecoder(r.Body).Decode(&msg); err != nil {
		http.Error(w, "Invalid request", http.StatusBadRequest)
		return
	}

	remoteIP, _, _ := net.SplitHostPort(r.RemoteAddr)

	if msg.NodeID != "" {
		g.AddPeer(msg.NodeID, remoteIP)

		if len(msg.PathInfos) > 0 || len(msg.DeletedPaths) > 0 || msg.DeletedNode {
			g.sync.HandleStateMessage(msg)
		}
	}

	w.Header().Set("Content-Type", "application/json")
	w.Write([]byte(`{"status":"ok"}`))
}

// handleGossipConfig 处理配置更新请求
func (g *GossipServer) handleGossipConfig(w http.ResponseWriter, r *http.Request) {
	if !g.requireGossipAuth(w, r) {
		return
	}
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	var msg message.GossipMessage
	if err := json.NewDecoder(r.Body).Decode(&msg); err != nil {
		http.Error(w, "Invalid request", http.StatusBadRequest)
		return
	}

	if msg.Config != nil {
		g.log.Info().Str("node_id", msg.NodeID).Msg("Received config update")

		// 注意：收到配置更新时不再重新广播，避免广播风暴
		// 配置更新由原始发送节点一次性广播，其他节点只应用配置
		// 这样可以避免配置在网络中无限循环传播
	}

	w.Header().Set("Content-Type", "application/json")
	w.Write([]byte(`{"status":"ok"}`))
}



func (g *GossipServer) StartAutoDiscovery() error {
	g.log.Info().Msg("Starting auto-discovery service")

	if g.server == nil {
		if err := g.Start(); err != nil {
			return err
		}
	}

	// 启动定期健康检查
	go g.periodicHealthCheck()

	// 初始化已知的节点 ID 列表
	g.log.Info().Int("known_nodes", len(g.GetNodes())).Msg("Initialized known node IDs")

	return g.discovery.Start()
}

// JoinNode 加入节点
func (g *GossipServer) JoinNode(nodeID, address string) error {
	oldCount := len(g.GetNodes())
	g.AddPeer(nodeID, address)
	newCount := len(g.GetNodes())
	if newCount > oldCount {
		g.log.Info().Str("node_id", nodeID).Str("address", address).Msg("Joining node")
	}
	return nil
}

// periodicHealthCheck 定期健康检查
func (g *GossipServer) periodicHealthCheck() {
	ticker := time.NewTicker(g.config.GossipSyncInterval)
	defer ticker.Stop()

	for {
		select {
		case <-g.done:
			return
		case <-ticker.C:
			g.checkPeerHealth()
		}
	}
}

// checkPeerHealth 检查节点健康状态
func (g *GossipServer) checkPeerHealth() {
	nodes := g.sync.GetNodes()
	for _, node := range nodes {
		g.checkSinglePeerHealth(node)
	}
}

// checkSinglePeerHealth 检查单个节点健康状态
func (g *GossipServer) checkSinglePeerHealth(node *syncsvc.Peer) {
	bindPort := 0
	fmt.Sscanf(g.config.ListenAddr, ":%d", &bindPort)
	gossipPort := bindPort + 1
	url := fmt.Sprintf("http://%s:%d/health", node.Address, gossipPort)

	req, err := http.NewRequest("GET", url, nil)
	if err != nil {
		g.log.Warn().Str("node_id", node.NodeID).Err(err).Msg("Failed to create health check request")
		return
	}
	if g.config.APIKey != "" {
		req.Header.Set("X-API-Key", g.config.APIKey)
	}

	client := &http.Client{Timeout: g.config.GossipHTTPTimeout}
	resp, err := client.Do(req)
	if err != nil {
		g.log.Warn().Str("node_id", node.NodeID).Str("url", url).Err(err).Msg("Health check failed")
		g.RemovePeer(node.NodeID)
		return
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		g.log.Warn().Str("node_id", node.NodeID).Str("url", url).Int("status", resp.StatusCode).Msg("Health check returned non-OK status")
		g.RemovePeer(node.NodeID)
		return
	}

	g.log.Debug().Str("node_id", node.NodeID).Msg("Health check passed")
}

func (g *GossipServer) BroadcastState(states map[string]*syncsvc.EndpointMetrics) {
	g.sync.BroadcastState(states)
}

func (g *GossipServer) BroadcastSync() {
	g.sync.BroadcastSync()
}

func (g *GossipServer) BroadcastDeletedPaths(paths []string) {
	g.sync.BroadcastDeletedPaths(paths)
}

func (g *GossipServer) BroadcastNodeLeave() {
	g.sync.BroadcastNodeLeave()
}

// BroadcastConfigUpdate 广播配置更新
func (g *GossipServer) BroadcastConfigUpdate(configUpdate *message.GossipMessage) {
	// 构建配置更新消息
	msg := message.GossipMessage{
		Type:   "config_update",
		NodeID: g.config.NodeID,
		Config: configUpdate.Config,
	}

	// 序列化消息
	data, err := json.Marshal(msg)
	if err != nil {
		g.log.Error().Err(err).Msg("Failed to marshal config update message")
		return
	}

	// 广播到所有节点
	nodes := g.sync.GetNodes()
	for _, node := range nodes {
		g.postToPeerAsync(node.Address, "/v1/gossip/config", data)
	}

	// 更新本地最新配置
	g.mu.Lock()
	// 暂时跳过更新，因为需要更复杂的类型转换
	g.mu.Unlock()
}

// BroadcastConfigUpdateExclude 广播配置更新，排除指定节点（避免广播风暴）
func (g *GossipServer) BroadcastConfigUpdateExclude(configUpdate *message.GossipMessage, excludeNodeID string) {
	// 构建配置更新消息
	msg := message.GossipMessage{
		Type:   "config_update",
		NodeID: g.config.NodeID,
		Config: configUpdate.Config,
	}

	// 序列化消息
	data, err := json.Marshal(msg)
	if err != nil {
		g.log.Error().Err(err).Msg("Failed to marshal config update message")
		return
	}

	// 广播到所有节点，排除原始发送节点
	nodes := g.sync.GetNodes()
	for _, node := range nodes {
		if node.NodeID == excludeNodeID {
			continue // 不发送回原始发送节点，避免广播风暴
		}
		g.postToPeerAsync(node.Address, "/v1/gossip/config", data)
	}

	g.log.Debug().Str("exclude_node_id", excludeNodeID).Msg("Broadcast config to other nodes")
}

// GetLatestConfig 获取最新配置
func (g *GossipServer) GetLatestConfig() *config.Config {
	g.mu.RLock()
	defer g.mu.RUnlock()
	return g.latestConfig
}

// postToPeerAsync 异步发送消息到节点（委托给 sync）
func (g *GossipServer) postToPeerAsync(peerIP, path string, data []byte) {
	g.sync.PostToPeerAsync(peerIP, path, data)
}

// sendSyncToPeer 向指定节点发送本地状态同步
// 这样即使UDP discovery是单向的，两个节点也能通过HTTP实现双向同步
func (g *GossipServer) sendSyncToPeer(peerIP, peerNodeID string) {
	g.log.Info().Str("peer_ip", peerIP).Str("peer_node_id", peerNodeID).Msg("Sending sync to peer after join")

	endpoints := g.registry.ListEndpoints()
	pathInfos := make([]message.PathInfo, 0, len(endpoints))
	for _, ep := range endpoints {
		pathInfos = append(pathInfos, message.PathInfo{
			ServicePath:   ep.ServicePath,
			NodePath:      ep.NodePath,
			Active:        ep.Active,
			QueueLen:      ep.QueueLen,
			Healthy:       ep.Healthy,
			MaxConcurrent: ep.MaxConcurrent,
			Plugin:        ep.Plugin,
		})
	}

	msg := message.GossipMessage{
		NodeID:    g.config.NodeID,
		PathInfos: pathInfos,
	}

	data, err := json.Marshal(msg)
	if err != nil {
		g.log.Error().Err(err).Msg("Failed to marshal sync message")
		return
	}

	bindPort := 0
	fmt.Sscanf(g.config.ListenAddr, ":%d", &bindPort)
	gossipPort := bindPort + 1
	url := fmt.Sprintf("http://%s:%d/v1/gossip/sync", peerIP, gossipPort)

	g.log.Info().Str("url", url).Msg("Posting sync to peer")

	req, err := http.NewRequest("POST", url, bytes.NewReader(data))
	if err != nil {
		g.log.Warn().Str("url", url).Err(err).Msg("Failed to create sync request")
		return
	}
	req.Header.Set("Content-Type", "application/json")
	if g.config.APIKey != "" {
		req.Header.Set("X-API-Key", g.config.APIKey)
	}

	client := &http.Client{Timeout: 5 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		g.log.Warn().Str("url", url).Err(err).Msg("Failed to send sync to peer")
		return
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusOK {
		g.log.Info().Str("peer_ip", peerIP).Msg("Successfully sent sync to peer")
	} else {
		g.log.Warn().Str("peer_ip", peerIP).Int("status", resp.StatusCode).Msg("Sync to peer returned non-OK status")
	}
}
