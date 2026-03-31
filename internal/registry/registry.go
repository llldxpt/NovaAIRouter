package registry

import (
	"time"

	"github.com/rs/zerolog"

	"novaairouter/internal/models"
	"novaairouter/internal/registry/local"
	"novaairouter/internal/registry/remote"
)

// Registry 服务注册表
type Registry struct {
	local   *local.LocalRegistry
	remote  *remote.RemoteRegistry
	log     zerolog.Logger
}

// New 创建新的服务注册表
func New() *Registry {
	return &Registry{
		local:  local.NewLocalRegistry(),
		remote: remote.NewRemoteRegistry(),
		log:    zerolog.Nop(),
	}
}

// SetLogger 设置日志
func (r *Registry) SetLogger(log zerolog.Logger) {
	r.log = log
	r.local.SetLogger(log)
	r.remote.SetLogger(log)
}

// RegisterEndpoints 注册本地端点
func (r *Registry) RegisterEndpoints(endpoints []*models.LocalEndpoint) (string, error) {
	return r.local.RegisterEndpoints(endpoints)
}

// RemoveEndpoint 移除本地端点
func (r *Registry) RemoveEndpoint(path string) {
	r.local.RemoveEndpoint(path)
}

// RemoveByServiceID 根据 service_id 移除端点
func (r *Registry) RemoveByServiceID(serviceID string) int {
	return r.local.RemoveByServiceID(serviceID)
}

// RemoveByServiceIDAndPath 根据 service_id 和 path 移除单个端点
func (r *Registry) RemoveByServiceIDAndPath(serviceID, nodePath string) int {
	return r.local.RemoveByServiceIDAndPath(serviceID, nodePath)
}

// RemoveAllEndpoints 移除所有本地端点
func (r *Registry) RemoveAllEndpoints() {
	r.local.RemoveAllEndpoints()
}

// GetEndpoint 获取本地端点
func (r *Registry) GetEndpoint(path string) (*models.LocalEndpoint, bool) {
	return r.local.GetEndpoint(path)
}

// ListEndpoints 列出所有本地端点
func (r *Registry) ListEndpoints() []*models.LocalEndpoint {
	return r.local.ListEndpoints()
}

// GetAllLocalEndpoints 获取所有本地端点
func (r *Registry) GetAllLocalEndpoints() map[string]*models.LocalEndpoint {
	return r.local.GetAllEndpoints()
}

// GetAllEndpointMetadata 获取所有本地端点的元数据
func (r *Registry) GetAllEndpointMetadata() map[string]models.EndpointMetadata {
	return r.local.GetAllEndpointMetadata()
}

// UpdateHeartbeat 更新心跳
func (r *Registry) UpdateHeartbeat(serviceID string, healthy bool) error {
	return r.local.UpdateHeartbeat(serviceID, healthy)
}

// UpdateEndpointMetrics 更新端点的活跃请求数和队列长度
func (r *Registry) UpdateEndpointMetrics(nodePath string, active, queueLen int32) {
	r.local.UpdateEndpointMetrics(nodePath, active, queueLen)
}

// CheckStaleEndpoints 检查过期端点
func (r *Registry) CheckStaleEndpoints(timeout time.Duration) {
	r.local.CheckStaleEndpoints(timeout)
}

// UpdateRemoteNode 更新远程节点
func (r *Registry) UpdateRemoteNode(nodeID, address string, port int) {
	r.remote.UpdateRemoteNode(nodeID, address, port)
}

// GetRemoteNode 获取远程节点
func (r *Registry) GetRemoteNode(nodeID string) (*models.RemoteNode, bool) {
	return r.remote.GetRemoteNode(nodeID)
}

// HasRemoteNode 检查是否存在远程节点
func (r *Registry) HasRemoteNode(nodeID string) bool {
	return r.remote.HasRemoteNode(nodeID)
}

// UpdateRemoteNodeState 更新远程节点状态
func (r *Registry) UpdateRemoteNodeState(nodeID string, states map[string]*models.EndpointState) {
	r.remote.UpdateRemoteNodeState(nodeID, states)
}

// UpdateRemoteNodeMetadata 更新远程节点元数据
func (r *Registry) UpdateRemoteNodeMetadata(nodeID string, descriptions map[string]models.EndpointMetadata) {
	converted := make(map[string]models.EndpointMetadata)
	for nodePath, desc := range descriptions {
		converted[nodePath] = desc
	}
	r.remote.UpdateRemoteNodeMetadata(nodeID, converted)
}

// RemoveRemoteNode 移除远程节点
func (r *Registry) RemoveRemoteNode(nodeID string) {
	r.remote.RemoveRemoteNode(nodeID)
}

// TouchRemoteNodeLastSeen 更新远程节点的 LastSeen 时间（线程安全）
func (r *Registry) TouchRemoteNodeLastSeen(nodeID string) {
	r.remote.TouchRemoteNodeLastSeen(nodeID)
}

// GetRemoteNodes 获取所有远程节点
func (r *Registry) GetRemoteNodes() []*models.RemoteNode {
	return r.remote.GetRemoteNodes()
}

// GetAllRemoteNodes 获取所有远程节点（映射形式）
func (r *Registry) GetAllRemoteNodes() map[string]*models.RemoteNode {
	return r.remote.GetAllRemoteNodes()
}

// GetHealthyNodesForPath 获取指定路径的健康节点
func (r *Registry) GetHealthyNodesForPath(path string) []*models.RemoteNode {
	return r.remote.GetHealthyNodesForPath(path)
}

// CleanStaleRemoteNodes 清理过期的远程节点和端点
func (r *Registry) CleanStaleRemoteNodes(nodeTimeout, endpointTimeout time.Duration) {
	r.remote.CleanStaleRemoteNodes(nodeTimeout, endpointTimeout)
}

// GetEndpointDescriptions 获取端点描述
func (r *Registry) GetEndpointDescriptions() map[string]models.EndpointMetadata {
	return r.local.GetEndpointDescriptions()
}

// AddRemoteNode 添加远程节点
func (r *Registry) AddRemoteNode(node *models.RemoteNode) {
	r.remote.AddRemoteNode(node)
}

// RemoveRemoteEndpoint 移除远程节点的端点
func (r *Registry) RemoveRemoteEndpoint(nodeID, path string) {
	r.remote.RemoveRemoteEndpoint(nodeID, path)
}

