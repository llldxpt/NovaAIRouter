package sync

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/rs/zerolog"

	"novaairouter/internal/config"
	"novaairouter/internal/gossip/message"
	"novaairouter/internal/models"
	"novaairouter/internal/registry"
)

// ConfigInterface 配置接口
type ConfigInterface interface {
	GetNodeID() string
	GetListenAddr() string
	GetSyncPeerTimeout() time.Duration
}

// Sync 状态同步服务
type Sync struct {
	config   ConfigInterface
	log      zerolog.Logger
	registry *registry.Registry
	peers    map[string]*Peer
	mu       sync.RWMutex
	wg       sync.WaitGroup
}

// Peer 集群节点
type Peer struct {
	NodeID   string
	Address  string
	LastSeen time.Time
}

// New 创建新的状态同步服务
func New(cfg ConfigInterface, log zerolog.Logger, reg *registry.Registry) *Sync {
	return &Sync{
		config:   cfg,
		log:      log,
		registry: reg,
		peers:    make(map[string]*Peer),
	}
}

// AddPeer 添加集群节点
func (s *Sync) AddPeer(nodeID, address string) {
	isNew := s.addPeerLocked(nodeID, address)

	if isNew {
		// 在锁外执行网络操作，避免死锁
		s.BroadcastSync()
		s.notifyPeersOfNewNode(nodeID, address)
	}
}

// addPeerLocked 在锁内添加或更新 peer，返回是否为新节点
func (s *Sync) addPeerLocked(nodeID, address string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.log.Info().Str("node_id", nodeID).Str("address", address).Msg("AddPeer called")

	if _, exists := s.peers[nodeID]; !exists {
		s.peers[nodeID] = &Peer{
			NodeID:   nodeID,
			Address:  address,
			LastSeen: time.Now(),
		}
		s.log.Info().Str("node_id", nodeID).Str("address", address).Msg("Added peer")

		// 解析地址和端口
		parts := strings.Split(address, ":")
		host := parts[0]
		var port int

		if len(parts) >= 2 && parts[1] != "" {
			fmt.Sscanf(parts[1], "%d", &port)
		} else {
			bindPort := 0
			fmt.Sscanf(s.config.GetListenAddr(), ":%d", &bindPort)
			if bindPort == 0 {
				bindPort = 15050
			}
			port = bindPort
		}

		if host == "" {
			host = "127.0.0.1"
		}
		s.registry.UpdateRemoteNode(nodeID, host, port)
		return true
	}

	s.peers[nodeID].LastSeen = time.Now()
	// 同时更新注册表中节点的 LastSeen，防止被清理
	s.registry.TouchRemoteNodeLastSeen(nodeID)
	return false
}

// RemovePeer 移除集群节点
func (s *Sync) RemovePeer(nodeID string) {
	s.mu.Lock()
	defer s.mu.Unlock()

	delete(s.peers, nodeID)
	s.log.Info().Str("node_id", nodeID).Msg("Removed peer")
	s.registry.RemoveRemoteNode(nodeID)
}

// GetNodes 获取所有集群节点
func (s *Sync) GetNodes() []*Peer {
	s.mu.RLock()
	defer s.mu.RUnlock()

	nodes := make([]*Peer, 0, len(s.peers))
	for _, peer := range s.peers {
		nodes = append(nodes, peer)
	}
	return nodes
}

// GetClusterSize 获取集群大小
func (s *Sync) GetClusterSize() int {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return len(s.peers) + 1
}

// notifyPeersOfNewNode 通知其他节点有新节点加入
func (s *Sync) notifyPeersOfNewNode(newNodeID, newNodeAddr string) {
	newNodeInfo := &models.NodeInfo{
		NodeID:  newNodeID,
		Address: newNodeAddr,
		Healthy: true,
	}

	msg := message.GossipMessage{
		Type: "peer_announce",
		Peer: newNodeInfo,
	}

	data, err := json.Marshal(msg)
	if err != nil {
		s.log.Error().Err(err).Msg("Failed to marshal peer announce message")
		return
	}

	s.mu.RLock()
	peers := make([]*Peer, 0, len(s.peers))
	for _, peer := range s.peers {
		peers = append(peers, peer)
	}
	s.mu.RUnlock()

	for _, peer := range peers {
		if peer.NodeID == newNodeID {
			continue
		}
		s.PostToPeerAsync(peer.Address, "/v1/gossip/peer", data)
	}
}

// BroadcastState 广播状态
func (s *Sync) BroadcastState(states map[string]*EndpointMetrics) {
	pathInfos := make([]message.PathInfo, 0, len(states))
	for _, metrics := range states {
		pathInfos = append(pathInfos, message.PathInfo{
			ServiceID:     metrics.ServiceID,
			ServicePath:   metrics.ServicePath,
			NodePath:      metrics.NodePath,
			Active:        metrics.Active,
			QueueLen:      metrics.QueueLen,
			Healthy:       metrics.Healthy,
			MaxConcurrent: metrics.MaxConcurrent,
			Plugin:        metrics.Plugin,
		})
	}

	msg := message.GossipMessage{
		NodeID:    s.config.GetNodeID(),
		PathInfos: pathInfos,
	}

	data, err := json.Marshal(msg)
	if err != nil {
		s.log.Error().Err(err).Msg("Failed to marshal state message")
		return
	}

	s.mu.RLock()
	peers := make([]*Peer, 0, len(s.peers))
	for _, peer := range s.peers {
		peers = append(peers, peer)
	}
	s.mu.RUnlock()

	for _, peer := range peers {
		s.PostToPeerAsync(peer.Address, "/v1/gossip/sync", data)
	}
}

// BroadcastSync 广播同步消息（只同步 node_path，不同步 service_path）
// 收到同步的节点会将请求转发到原始节点，而不是直接访问后端服务
func (s *Sync) BroadcastSync() {
	endpoints := s.registry.ListEndpoints()
	s.log.Info().Int("endpoint_count", len(endpoints)).Msg("Broadcasting sync")
	pathInfos := make([]message.PathInfo, 0, len(endpoints))
	for _, ep := range endpoints {
		s.log.Info().Str("node_path", ep.NodePath).Str("service_path", ep.ServicePath).Msg("Syncing endpoint")
		pathInfos = append(pathInfos, message.PathInfo{
			ServiceID:     ep.ServiceID,
			NodePath:      ep.NodePath,
			Active:        ep.Active,
			QueueLen:      ep.QueueLen,
			Healthy:       ep.Healthy,
			MaxConcurrent: ep.MaxConcurrent,
			Plugin:        ep.Plugin,
		})
	}

	msg := message.GossipMessage{
		NodeID:    s.config.GetNodeID(),
		PathInfos: pathInfos,
	}

	data, err := json.Marshal(msg)
	if err != nil {
		s.log.Error().Err(err).Msg("Failed to marshal sync message")
		return
	}

	s.mu.RLock()
	peers := make([]*Peer, 0, len(s.peers))
	for _, peer := range s.peers {
		peers = append(peers, peer)
	}
	s.mu.RUnlock()

	for _, peer := range peers {
		s.PostToPeerAsync(peer.Address, "/v1/gossip/sync", data)
	}
}

// HandleStateMessage 处理状态消息
func (s *Sync) HandleStateMessage(msg message.GossipMessage) {
	s.log.Info().
		Str("node_id", msg.NodeID).
		Int("path_infos", len(msg.PathInfos)).
		Msg("Received state message")

	for _, pi := range msg.PathInfos {
		s.log.Info().Str("node_id", msg.NodeID).Str("node_path", pi.NodePath).Msg("Received endpoint in state message")
	}

	if len(msg.PathInfos) > 0 {
		s.log.Info().Bool("has_remote_node", s.registry.HasRemoteNode(msg.NodeID)).Msg("Checking remote node")

		if !s.registry.HasRemoteNode(msg.NodeID) {
			s.log.Info().Str("node_id", msg.NodeID).Msg("Creating remote node for state")
			s.mu.RLock()
			if peer, ok := s.peers[msg.NodeID]; ok {
				bindPort := 0
				fmt.Sscanf(s.config.GetListenAddr(), ":%d", &bindPort)
				if bindPort == 0 {
					bindPort = 15050
				}
				s.registry.UpdateRemoteNode(msg.NodeID, peer.Address, bindPort)
				s.log.Info().Str("node_id", msg.NodeID).Str("address", peer.Address).Int("port", bindPort).Msg("Remote node created")
			} else {
				s.log.Warn().Str("node_id", msg.NodeID).Msg("Peer not found in peers map")
			}
			s.mu.RUnlock()
		}

		endpoints := make(map[string]*models.EndpointState, len(msg.PathInfos))
		metadata := make(map[string]models.EndpointMetadata, len(msg.PathInfos))
		for _, pi := range msg.PathInfos {
			nodePath := pi.NodePath
			endpoints[nodePath] = &models.EndpointState{
				Active:        pi.Active,
				QueueLen:      pi.QueueLen,
				Healthy:       pi.Healthy,
				MaxConcurrent: pi.MaxConcurrent,
				Plugin:        pi.Plugin,
			}
			metadata[nodePath] = models.EndpointMetadata{
				NodePath:      nodePath,
				MaxConcurrent: pi.MaxConcurrent,
				Plugin:        pi.Plugin,
			}
		}
		s.registry.UpdateRemoteNodeState(msg.NodeID, endpoints)
		s.registry.UpdateRemoteNodeMetadata(msg.NodeID, metadata)
	}

	// 处理端点删除消息
	for _, path := range msg.DeletedPaths {
		s.log.Info().Str("node_id", msg.NodeID).Str("path", path).Msg("Removing deleted remote endpoint")
		s.registry.RemoveRemoteEndpoint(msg.NodeID, path)
	}

	// 处理节点下线消息
	if msg.DeletedNode {
		s.log.Info().Str("node_id", msg.NodeID).Msg("Removing remote node (node_leave)")
		s.registry.RemoveRemoteNode(msg.NodeID)
		s.mu.Lock()
		delete(s.peers, msg.NodeID)
		s.mu.Unlock()
	}
}

// cleanupStalePeers 清理过期的节点
func (s *Sync) cleanupStalePeers() {
	s.mu.Lock()
	defer s.mu.Unlock()

	now := time.Now()
	peerTimeout := s.config.GetSyncPeerTimeout()
	for nodeID, peer := range s.peers {
		if now.Sub(peer.LastSeen) > peerTimeout {
			s.log.Info().Str("node_id", nodeID).Msg("Removing stale peer")
			delete(s.peers, nodeID)
			s.registry.RemoveRemoteNode(nodeID)
		}
	}
}

// gossipHTTPClient 带超时的 HTTP client，防止 goroutine 泄漏
var gossipHTTPClient = &http.Client{
	Timeout: 5 * time.Second,
}

// PostToPeerAsync 异步发送消息到节点（导出方法）
func (s *Sync) PostToPeerAsync(peerIP, path string, data []byte) {
	s.wg.Add(1)
	go func() {
		defer s.wg.Done()
		bindPort := 0
		fmt.Sscanf(s.config.GetListenAddr(), ":%d", &bindPort)
		gossipPort := bindPort + 1
		url := fmt.Sprintf("http://%s:%d%s", peerIP, gossipPort, path)

		req, err := http.NewRequest("POST", url, bytes.NewReader(data))
		if err != nil {
			s.log.Warn().Str("url", url).Err(err).Msg("Failed to create request")
			return
		}
		req.Header.Set("Content-Type", "application/json")

		// Add API Key if configured
		if cfg, ok := s.config.(*config.Config); ok && cfg.APIKey != "" {
			req.Header.Set("X-API-Key", cfg.APIKey)
		}

		resp, err := gossipHTTPClient.Do(req)
		if err != nil {
			s.log.Warn().Str("url", url).Err(err).Msg("Failed to post to peer")
			return
		}
		defer resp.Body.Close()
		if resp.StatusCode != http.StatusOK {
			s.log.Warn().Str("url", url).Int("status", resp.StatusCode).Msg("Peer post returned non-OK status")
			return
		}
		s.log.Debug().Str("url", url).Msg("TCP gossip sync success")
	}()
}

// WaitForPendingPosts 等待所有待处理的异步请求完成
func (s *Sync) WaitForPendingPosts() {
	s.wg.Wait()
}

// BroadcastDeletedPaths 广播已删除的端点路径
func (s *Sync) BroadcastDeletedPaths(paths []string) {
	msg := message.GossipMessage{
		NodeID:       s.config.GetNodeID(),
		DeletedPaths: paths,
	}

	data, err := json.Marshal(msg)
	if err != nil {
		s.log.Error().Err(err).Msg("Failed to marshal deleted paths message")
		return
	}

	s.mu.RLock()
	peers := make([]*Peer, 0, len(s.peers))
	for _, peer := range s.peers {
		peers = append(peers, peer)
	}
	s.mu.RUnlock()

	s.log.Info().Int("path_count", len(paths)).Int("peer_count", len(peers)).
		Msg("Broadcasting deleted paths")

	for _, peer := range peers {
		s.PostToPeerAsync(peer.Address, "/v1/gossip/sync", data)
	}
}

// BroadcastNodeLeave 广播节点下线消息
func (s *Sync) BroadcastNodeLeave() {
	msg := message.GossipMessage{
		NodeID:      s.config.GetNodeID(),
		DeletedNode: true,
	}

	data, err := json.Marshal(msg)
	if err != nil {
		s.log.Error().Err(err).Msg("Failed to marshal node leave message")
		return
	}

	s.mu.RLock()
	peers := make([]*Peer, 0, len(s.peers))
	for _, peer := range s.peers {
		peers = append(peers, peer)
	}
	s.mu.RUnlock()

	if len(peers) == 0 {
		s.log.Debug().Msg("No peers to notify of node leave")
		return
	}

	s.log.Info().Int("peer_count", len(peers)).Msg("Broadcasting node leave to peers")

	for _, peer := range peers {
		s.PostToPeerAsync(peer.Address, "/v1/gossip/sync", data)
	}
}

// EndpointMetrics 端点指标
type EndpointMetrics struct {
	ServiceID     string
	ServicePath   string
	NodePath      string
	Active        int32
	QueueLen      int32
	Healthy       bool
	MaxConcurrent int32
	Plugin        bool
}
