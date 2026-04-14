package models

import (
	"sync"
	"time"
)

type LocalEndpoint struct {
	ServiceID     string // 对外的组 ID，一次注册行为的所有 endpoint 共享
	EpID          string // 内部唯一 ID，用于区分同一 nodePath 下的不同后端
	ServicePath   string
	NodePath      string
	Description   string
	Healthy       bool
	LastHeartbeat time.Time
	Active        int32
	QueueLen      int32
	MaxConcurrent int32
	LocalOnly     bool
	Plugin        bool
}

// AggregatedPathInfo 按 nodePath 聚合后的路径信息，用于 gossip 广播
type AggregatedPathInfo struct {
	NodePath      string
	Active        int32
	QueueLen      int32
	Healthy       bool
	MaxConcurrent int32
	Plugin        bool
}

type LocalEndpointStore struct {
	sync.RWMutex
	// map[nodePath]map[serviceID]*LocalEndpoint
	endpoints map[string]map[string]*LocalEndpoint
}

func NewLocalEndpointStore() *LocalEndpointStore {
	return &LocalEndpointStore{
		endpoints: make(map[string]map[string]*LocalEndpoint),
	}
}

func (s *LocalEndpointStore) Set(path string, endpoint *LocalEndpoint) {
	s.Lock()
	defer s.Unlock()
	if s.endpoints[path] == nil {
		s.endpoints[path] = make(map[string]*LocalEndpoint)
	}
	s.endpoints[path][endpoint.EpID] = endpoint
}

// Get returns any one endpoint for the given path (for compatibility).
func (s *LocalEndpointStore) Get(path string) (*LocalEndpoint, bool) {
	s.RLock()
	defer s.RUnlock()
	if sub, ok := s.endpoints[path]; ok {
		for _, ep := range sub {
			return ep, true
		}
	}
	return nil, false
}

// ListByNodePath returns all endpoints under a given nodePath.
func (s *LocalEndpointStore) ListByNodePath(path string) []*LocalEndpoint {
	s.RLock()
	defer s.RUnlock()
	var result []*LocalEndpoint
	if sub, ok := s.endpoints[path]; ok {
		for _, ep := range sub {
			result = append(result, ep)
		}
	}
	return result
}

// GetByEpID returns the endpoint matching the given EpID under a path.
func (s *LocalEndpointStore) GetByEpID(path, epID string) (*LocalEndpoint, bool) {
	s.RLock()
	defer s.RUnlock()
	if sub, ok := s.endpoints[path]; ok {
		for _, ep := range sub {
			if ep.EpID == epID {
				return ep, true
			}
		}
	}
	return nil, false
}

// GetByServiceID returns the first endpoint for a specific path whose ServiceID matches.
func (s *LocalEndpointStore) GetByServiceID(path, serviceID string) (*LocalEndpoint, bool) {
	s.RLock()
	defer s.RUnlock()
	if sub, ok := s.endpoints[path]; ok {
		for _, ep := range sub {
			if ep.ServiceID == serviceID {
				return ep, true
			}
		}
	}
	return nil, false
}

// Delete removes all endpoints under a path.
func (s *LocalEndpointStore) Delete(path string) {
	s.Lock()
	defer s.Unlock()
	delete(s.endpoints, path)
}

// DeleteByEpID removes a single backend by its EpID under a path.
func (s *LocalEndpointStore) DeleteByEpID(path, epID string) {
	s.Lock()
	defer s.Unlock()
	if sub, ok := s.endpoints[path]; ok {
		delete(sub, epID)
		if len(sub) == 0 {
			delete(s.endpoints, path)
		}
	}
}

// DeleteByServiceID removes all backends under a path whose ServiceID matches.
func (s *LocalEndpointStore) DeleteByServiceID(path, serviceID string) {
	s.Lock()
	defer s.Unlock()
	if sub, ok := s.endpoints[path]; ok {
		for epID, ep := range sub {
			if ep.ServiceID == serviceID {
				delete(sub, epID)
			}
		}
		if len(sub) == 0 {
			delete(s.endpoints, path)
		}
	}
}

func (s *LocalEndpointStore) List() []*LocalEndpoint {
	s.RLock()
	defer s.RUnlock()
	var result []*LocalEndpoint
	for _, sub := range s.endpoints {
		for _, ep := range sub {
			result = append(result, ep)
		}
	}
	return result
}

func (s *LocalEndpointStore) GetAll() map[string]*LocalEndpoint {
	s.RLock()
	defer s.RUnlock()
	result := make(map[string]*LocalEndpoint)
	for path, sub := range s.endpoints {
		for _, ep := range sub {
			// last writer wins per path — callers only need one representative per path
			result[path] = ep
		}
	}
	return result
}

func (s *LocalEndpointStore) UpdateHealthy(healthy bool) {
	s.Lock()
	defer s.Unlock()
	for _, sub := range s.endpoints {
		for _, ep := range sub {
			ep.Healthy = healthy
		}
	}
}

func (s *LocalEndpointStore) UpdateHeartbeat() {
	s.Lock()
	defer s.Unlock()
	now := time.Now()
	for _, sub := range s.endpoints {
		for _, ep := range sub {
			ep.LastHeartbeat = now
		}
	}
}
