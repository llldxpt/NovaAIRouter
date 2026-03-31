package local

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"sync"
	"time"

	"github.com/rs/zerolog"

	"novaairouter/internal/models"
)

func generateServiceID() string {
	bytes := make([]byte, 16)
	rand.Read(bytes)
	return hex.EncodeToString(bytes)
}

// LocalRegistry 本地端点注册表
type LocalRegistry struct {
	endpoints  *models.LocalEndpointStore
	serviceMap map[string]string // service_id -> first_endpoint_path
	mu         sync.RWMutex      // 保护 serviceMap
	log        zerolog.Logger
}

// NewLocalRegistry 创建新的本地端点注册表
func NewLocalRegistry() *LocalRegistry {
	return &LocalRegistry{
		endpoints:  models.NewLocalEndpointStore(),
		serviceMap: make(map[string]string),
		log:        zerolog.Nop(),
	}
}

// SetLogger 设置日志
func (r *LocalRegistry) SetLogger(log zerolog.Logger) {
	r.log = log
}

// RegisterEndpoints 注册本地端点
func (r *LocalRegistry) RegisterEndpoints(endpoints []*models.LocalEndpoint) (string, error) {
	r.mu.Lock()
	defer r.mu.Unlock()

	for _, ep := range endpoints {
		existingEp, exists := r.endpoints.Get(ep.NodePath)
		if exists && ep.Plugin && existingEp.Plugin {
			return "", fmt.Errorf("node_path %s already has a plugin registered", ep.NodePath)
		}
	}

	serviceID := generateServiceID()
	paths := make([]string, 0, len(endpoints))

	for _, ep := range endpoints {
		ep.ServiceID = serviceID
		r.endpoints.Set(ep.NodePath, ep)
		paths = append(paths, ep.NodePath)
	}

	r.serviceMap[serviceID] = paths[0]

	r.log.Info().Str("service_id", serviceID).Int("count", len(endpoints)).Msg("Endpoints registered")

	return serviceID, nil
}

// RemoveEndpoint 移除本地端点
func (r *LocalRegistry) RemoveEndpoint(path string) {
	r.endpoints.Delete(path)
	r.log.Info().Str("path", path).Msg("Endpoint removed")
}

// RemoveByServiceID 根据 service_id 移除端点
func (r *LocalRegistry) RemoveByServiceID(serviceID string) int {
	eps := r.endpoints.List()
	removedCount := 0
	for _, ep := range eps {
		if ep.ServiceID == serviceID {
			r.endpoints.Delete(ep.NodePath)
			removedCount++
		}
	}
	if removedCount > 0 {
		r.log.Info().Str("service_id", serviceID).Int("count", removedCount).Msg("Endpoints removed by service_id")
	}
	return removedCount
}

// RemoveByServiceIDAndPath 根据 service_id 和 path 移除单个端点
func (r *LocalRegistry) RemoveByServiceIDAndPath(serviceID, nodePath string) int {
	ep, exists := r.endpoints.Get(nodePath)
	if !exists {
		r.log.Warn().Str("service_id", serviceID).Str("path", nodePath).Msg("Endpoint not found")
		return 0
	}
	if ep.ServiceID != serviceID {
		r.log.Warn().Str("service_id", serviceID).Str("path", nodePath).Msg("Service ID mismatch, cannot delete endpoint")
		return 0
	}
	r.endpoints.Delete(nodePath)
	r.log.Info().Str("service_id", serviceID).Str("path", nodePath).Msg("Endpoint removed by service_id and path")
	return 1
}

// RemoveAllEndpoints 移除所有本地端点
func (r *LocalRegistry) RemoveAllEndpoints() {
	eps := r.endpoints.List()
	for _, ep := range eps {
		r.endpoints.Delete(ep.NodePath)
	}
	r.log.Info().Msg("All endpoints removed")
}

// GetEndpoint 获取本地端点
func (r *LocalRegistry) GetEndpoint(path string) (*models.LocalEndpoint, bool) {
	return r.endpoints.Get(path)
}

// ListEndpoints 列出所有本地端点
func (r *LocalRegistry) ListEndpoints() []*models.LocalEndpoint {
	return r.endpoints.List()
}

// GetAllEndpoints 获取所有本地端点
func (r *LocalRegistry) GetAllEndpoints() map[string]*models.LocalEndpoint {
	return r.endpoints.GetAll()
}

// GetAllEndpointMetadata 获取所有本地端点的元数据
func (r *LocalRegistry) GetAllEndpointMetadata() map[string]models.EndpointMetadata {
	result := make(map[string]models.EndpointMetadata)
	for path, ep := range r.endpoints.GetAll() {
		result[path] = models.EndpointMetadata{
			ServiceID:     ep.ServiceID,
			ServicePath:   ep.ServicePath,
			NodePath:      ep.NodePath,
			Plugin:        ep.Plugin,
			MaxConcurrent: ep.MaxConcurrent,
		}
	}
	return result
}

// UpdateHeartbeat 更新心跳
func (r *LocalRegistry) UpdateHeartbeat(serviceID string, healthy bool) error {
	if serviceID == "" {
		// 空 serviceID 是错误的，不应该更新所有端点
		// 这是为了防止测试或配置错误导致所有端点被意外标记
		r.log.Warn().Msg("Rejecting heartbeat with empty service_id")
		return fmt.Errorf("service_id is required")
	}

	eps := r.endpoints.List()
	for _, ep := range eps {
		if ep.ServiceID == serviceID {
			ep.Healthy = healthy
			ep.LastHeartbeat = time.Now()
			r.endpoints.Set(ep.NodePath, ep)
			r.log.Debug().Str("service_id", serviceID).Str("node_path", ep.NodePath).Bool("healthy", healthy).Msg("Heartbeat updated (by service_id)")
		}
	}
	return nil
}

// UpdateEndpointMetrics 更新端点的活跃请求数和队列长度
func (r *LocalRegistry) UpdateEndpointMetrics(nodePath string, active, queueLen int32) {
	r.log.Debug().Str("nodePath", nodePath).Int32("active", active).Int32("queueLen", queueLen).Msg("UpdateEndpointMetrics called")
	if ep, ok := r.endpoints.Get(nodePath); ok {
		ep.Active = active
		ep.QueueLen = queueLen
		r.endpoints.Set(nodePath, ep)
	} else {
		r.log.Warn().Str("nodePath", nodePath).Msg("Endpoint not found in registry")
	}
}

// CheckStaleEndpoints 检查过期端点
func (r *LocalRegistry) CheckStaleEndpoints(timeout time.Duration) {
	eps := r.endpoints.List()
	now := time.Now()
	for _, ep := range eps {
		if now.Sub(ep.LastHeartbeat) > timeout {
			if ep2, ok := r.endpoints.Get(ep.NodePath); ok {
				if ep2.Healthy {
					ep2.Healthy = false
					r.log.Warn().Str("node_path", ep.NodePath).Msg("Endpoint marked unhealthy due to timeout")
				} else if now.Sub(ep2.LastHeartbeat) > timeout*2 {
					r.endpoints.Delete(ep.NodePath)
					r.log.Warn().Str("node_path", ep.NodePath).Msg("Endpoint removed due to prolonged timeout")
				}
			}
		}
	}
}

// GetEndpointDescriptions 获取端点描述
func (r *LocalRegistry) GetEndpointDescriptions() map[string]models.EndpointMetadata {
	descriptions := make(map[string]models.EndpointMetadata)
	for path, ep := range r.endpoints.GetAll() {
		if !ep.LocalOnly {
			descriptions[path] = models.EndpointMetadata{
				ServiceID:     ep.ServiceID,
				ServicePath:   ep.ServicePath,
				NodePath:      ep.NodePath,
				Description:   ep.Description,
				Plugin:        ep.Plugin,
			}
		}
	}
	return descriptions
}
