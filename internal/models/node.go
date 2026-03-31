package models

import (
	"strings"
	"sync"
	"time"
)

type EndpointState struct {
	Active        int32
	QueueLen      int32
	Healthy       bool
	MaxConcurrent int32
	Plugin        bool      // 是否为插件端点
	ResponseTime  int64     // 平均响应时间（毫秒）
	LastSeen      time.Time // 端点最后活跃时间
}

type EndpointMetadata struct {
	ServiceID     string
	ServicePath   string
	NodePath      string
	Description   string
	Plugin        bool
	MaxConcurrent int32
}

type RemoteNode struct {
	NodeID               string
	Address              string
	ServicePort          int
	ServicePath          string
	NodePath             string
	EndpointStates       map[string]*EndpointState
	EndpointDescriptions map[string]EndpointMetadata
	LastSeen             time.Time
}

type RemoteNodeStore struct {
	sync.RWMutex
	nodes map[string]*RemoteNode
}

func NewRemoteNodeStore() *RemoteNodeStore {
	return &RemoteNodeStore{
		nodes: make(map[string]*RemoteNode),
	}
}

func (s *RemoteNodeStore) Set(nodeID string, node *RemoteNode) {
	s.Lock()
	defer s.Unlock()
	s.nodes[nodeID] = node
}

// Upsert 如果节点已存在，仅更新连接信息和 LastSeen；如果不存在则创建新节点。
// 这样可以避免覆盖已有的 EndpointStates 和 EndpointDescriptions 数据。
func (s *RemoteNodeStore) Upsert(nodeID, address string, servicePort int) {
	s.Lock()
	defer s.Unlock()
	if existing, ok := s.nodes[nodeID]; ok {
		existing.Address = address
		existing.ServicePort = servicePort
		existing.LastSeen = time.Now()
	} else {
		s.nodes[nodeID] = &RemoteNode{
			NodeID:               nodeID,
			Address:              address,
			ServicePort:          servicePort,
			EndpointStates:       make(map[string]*EndpointState),
			EndpointDescriptions: make(map[string]EndpointMetadata),
			LastSeen:             time.Now(),
		}
	}
}

func (s *RemoteNodeStore) Get(nodeID string) (*RemoteNode, bool) {
	s.RLock()
	defer s.RUnlock()
	node, ok := s.nodes[nodeID]
	return node, ok
}

func (s *RemoteNodeStore) Delete(nodeID string) {
	s.Lock()
	defer s.Unlock()
	delete(s.nodes, nodeID)
}

// TouchLastSeen 更新节点的 LastSeen 时间（线程安全）
func (s *RemoteNodeStore) TouchLastSeen(nodeID string) {
	s.Lock()
	defer s.Unlock()
	if node, ok := s.nodes[nodeID]; ok {
		node.LastSeen = time.Now()
	}
}

func (s *RemoteNodeStore) List() []*RemoteNode {
	s.RLock()
	defer s.RUnlock()
	result := make([]*RemoteNode, 0, len(s.nodes))
	for _, node := range s.nodes {
		result = append(result, node)
	}
	return result
}

func (s *RemoteNodeStore) GetAll() map[string]*RemoteNode {
	s.RLock()
	defer s.RUnlock()
	result := make(map[string]*RemoteNode, len(s.nodes))
	for k, v := range s.nodes {
		result[k] = v
	}
	return result
}

func (s *RemoteNodeStore) UpdateState(nodeID string, endpointStates map[string]*EndpointState) {
	s.Lock()
	defer s.Unlock()
	if node, ok := s.nodes[nodeID]; ok {
		if node.EndpointStates == nil {
			node.EndpointStates = make(map[string]*EndpointState)
		}
		for path, state := range endpointStates {
			state.LastSeen = time.Now()
			node.EndpointStates[path] = state
		}
		node.LastSeen = time.Now()
	}
}

func (s *RemoteNodeStore) UpdateMetadata(nodeID string, descriptions map[string]EndpointMetadata) {
	s.Lock()
	defer s.Unlock()
	if node, ok := s.nodes[nodeID]; ok {
		if node.EndpointDescriptions == nil {
			node.EndpointDescriptions = make(map[string]EndpointMetadata)
		}
		if node.EndpointStates == nil {
			node.EndpointStates = make(map[string]*EndpointState)
		}
		for path, desc := range descriptions {
			node.EndpointDescriptions[path] = desc
			if _, exists := node.EndpointStates[path]; !exists {
				node.EndpointStates[path] = &EndpointState{
					Healthy:       true,
					MaxConcurrent: desc.MaxConcurrent,
					ResponseTime:  0,
					LastSeen:      time.Now(),
				}
			} else {
				node.EndpointStates[path].Healthy = true
				node.EndpointStates[path].MaxConcurrent = desc.MaxConcurrent
				node.EndpointStates[path].LastSeen = time.Now()
			}
		}
	}
}

func (s *RemoteNodeStore) MarkUnhealthy(nodeID string) {
	s.Lock()
	defer s.Unlock()
	if node, ok := s.nodes[nodeID]; ok {
		for _, state := range node.EndpointStates {
			state.Healthy = false
		}
	}
}

func (s *RemoteNodeStore) GetHealthyNodesForPath(path string) []*RemoteNode {
	s.RLock()
	defer s.RUnlock()
	var result []*RemoteNode
	for _, node := range s.nodes {
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
				result = append(result, node)
				break
			}
		}
	}
	return result
}

func (s *RemoteNodeStore) CleanStale(nodeTimeout, endpointTimeout time.Duration) {
	s.Lock()
	defer s.Unlock()
	now := time.Now()
	for nodeID, node := range s.nodes {
		if now.Sub(node.LastSeen) > nodeTimeout {
			delete(s.nodes, nodeID)
			continue
		}
		// 清理单个过期端点
		for path, state := range node.EndpointStates {
			if !state.LastSeen.IsZero() && now.Sub(state.LastSeen) > endpointTimeout {
				delete(node.EndpointStates, path)
				delete(node.EndpointDescriptions, path)
			}
		}
	}
}

func (s *RemoteNodeStore) RemoveEndpoint(nodeID, path string) {
	s.Lock()
	defer s.Unlock()
	if node, ok := s.nodes[nodeID]; ok {
		delete(node.EndpointStates, path)
		delete(node.EndpointDescriptions, path)
	}
}
