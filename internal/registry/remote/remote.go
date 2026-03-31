package remote

import (
	"time"

	"github.com/rs/zerolog"

	"novaairouter/internal/models"
)

// RemoteRegistry 远程节点注册表
type RemoteRegistry struct {
	nodes *models.RemoteNodeStore
	log   zerolog.Logger
}

// NewRemoteRegistry 创建新的远程节点注册表
func NewRemoteRegistry() *RemoteRegistry {
	return &RemoteRegistry{
		nodes: models.NewRemoteNodeStore(),
		log:   zerolog.Nop(),
	}
}

// SetLogger 设置日志
func (r *RemoteRegistry) SetLogger(log zerolog.Logger) {
	r.log = log
}

// UpdateRemoteNode 更新远程节点（upsert 模式）
// 如果节点已存在，仅更新地址、端口和 LastSeen，保留已有的端点数据
// 如果节点不存在，创建新节点
func (r *RemoteRegistry) UpdateRemoteNode(nodeID, address string, port int) {
	r.nodes.Upsert(nodeID, address, port)
	r.log.Info().Str("node_id", nodeID).Str("address", address).Int("port", port).Msg("Remote node updated in registry")
}

// GetRemoteNode 获取远程节点
func (r *RemoteRegistry) GetRemoteNode(nodeID string) (*models.RemoteNode, bool) {
	return r.nodes.Get(nodeID)
}

// HasRemoteNode 检查是否存在远程节点
func (r *RemoteRegistry) HasRemoteNode(nodeID string) bool {
	_, exists := r.nodes.Get(nodeID)
	return exists
}

// UpdateRemoteNodeState 更新远程节点状态
func (r *RemoteRegistry) UpdateRemoteNodeState(nodeID string, states map[string]*models.EndpointState) {
	r.nodes.UpdateState(nodeID, states)
	r.log.Debug().Str("node_id", nodeID).Int("states_count", len(states)).Msg("Remote node state updated")
}

// UpdateRemoteNodeMetadata 更新远程节点元数据
func (r *RemoteRegistry) UpdateRemoteNodeMetadata(nodeID string, descriptions map[string]models.EndpointMetadata) {
	r.nodes.UpdateMetadata(nodeID, descriptions)
	r.log.Debug().Str("node_id", nodeID).Int("descriptions_count", len(descriptions)).Msg("Remote node metadata updated")
}

// RemoveRemoteNode 移除远程节点
func (r *RemoteRegistry) RemoveRemoteNode(nodeID string) {
	r.nodes.Delete(nodeID)
	r.log.Info().Str("node_id", nodeID).Msg("Remote node removed")
}

// GetRemoteNodes 获取所有远程节点
func (r *RemoteRegistry) GetRemoteNodes() []*models.RemoteNode {
	return r.nodes.List()
}

// GetAllRemoteNodes 获取所有远程节点（映射形式）
func (r *RemoteRegistry) GetAllRemoteNodes() map[string]*models.RemoteNode {
	return r.nodes.GetAll()
}

// GetHealthyNodesForPath 获取指定路径的健康节点
func (r *RemoteRegistry) GetHealthyNodesForPath(path string) []*models.RemoteNode {
	return r.nodes.GetHealthyNodesForPath(path)
}

// CleanStaleRemoteNodes 清理过期的远程节点和端点
func (r *RemoteRegistry) CleanStaleRemoteNodes(nodeTimeout, endpointTimeout time.Duration) {
	r.nodes.CleanStale(nodeTimeout, endpointTimeout)
	r.log.Debug().Dur("node_timeout", nodeTimeout).Dur("endpoint_timeout", endpointTimeout).Msg("Cleaned stale nodes and endpoints")
}

// AddRemoteNode 添加远程节点
func (r *RemoteRegistry) AddRemoteNode(node *models.RemoteNode) {
	r.nodes.Set(node.NodeID, node)
	r.log.Debug().Str("node_id", node.NodeID).Msg("Remote node added")
}

// RemoveRemoteEndpoint 移除远程节点的端点
func (r *RemoteRegistry) RemoveRemoteEndpoint(nodeID, path string) {
	r.nodes.RemoveEndpoint(nodeID, path)
	r.log.Debug().Str("node_id", nodeID).Str("path", path).Msg("Remote endpoint removed")
}

// TouchRemoteNodeLastSeen 更新远程节点的 LastSeen 时间（线程安全）
func (r *RemoteRegistry) TouchRemoteNodeLastSeen(nodeID string) {
	r.nodes.TouchLastSeen(nodeID)
}
