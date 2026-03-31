package models

import (
	"sync"
	"time"
)

type LocalEndpoint struct {
	ServiceID     string
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

type LocalEndpointStore struct {
	sync.RWMutex
	endpoints map[string]*LocalEndpoint
}

func NewLocalEndpointStore() *LocalEndpointStore {
	return &LocalEndpointStore{
		endpoints: make(map[string]*LocalEndpoint),
	}
}

func (s *LocalEndpointStore) Set(path string, endpoint *LocalEndpoint) {
	s.Lock()
	defer s.Unlock()
	s.endpoints[path] = endpoint
}

func (s *LocalEndpointStore) Get(path string) (*LocalEndpoint, bool) {
	s.RLock()
	defer s.RUnlock()
	ep, ok := s.endpoints[path]
	return ep, ok
}

func (s *LocalEndpointStore) Delete(path string) {
	s.Lock()
	defer s.Unlock()
	delete(s.endpoints, path)
}

func (s *LocalEndpointStore) List() []*LocalEndpoint {
	s.RLock()
	defer s.RUnlock()
	result := make([]*LocalEndpoint, 0, len(s.endpoints))
	for _, ep := range s.endpoints {
		result = append(result, ep)
	}
	return result
}

func (s *LocalEndpointStore) GetAll() map[string]*LocalEndpoint {
	s.RLock()
	defer s.RUnlock()
	result := make(map[string]*LocalEndpoint, len(s.endpoints))
	for k, v := range s.endpoints {
		result[k] = v
	}
	return result
}

func (s *LocalEndpointStore) UpdateHealthy(healthy bool) {
	s.Lock()
	defer s.Unlock()
	for _, ep := range s.endpoints {
		ep.Healthy = healthy
	}
}

func (s *LocalEndpointStore) UpdateHeartbeat() {
	s.Lock()
	defer s.Unlock()
	now := time.Now()
	for _, ep := range s.endpoints {
		ep.LastHeartbeat = now
	}
}
