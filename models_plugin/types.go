package main

import (
	"net/http"
	"sync"
	"time"
)

type Backend struct {
	Address       string    `json:"address"`
	Port          int       `json:"port"`
	NodePath      string    `json:"node_path"`
	BackendPath   string    `json:"backend_path"`
	MaxConcurrent int32     `json:"max_concurrent"`
	Healthy       bool      `json:"healthy"`
	ActiveConn    int32     `json:"active_conn"`
	AvgLatency    int64     `json:"avg_latency"`
	LastSuccess   time.Time `json:"last_success"`
}

type ModelBackend struct {
	Model    string     `json:"model"`
	Backends []*Backend `json:"backends"`
}

type SessionInfo struct {
	SessionID    string    `json:"session_id"`
	BackendAddr  string    `json:"backend_addr"`
	BackendPort  int       `json:"backend_port"`
	LastActivity time.Time `json:"last_activity"`
}

type ModelInfo struct {
	ID      string `json:"id"`
	Object  string `json:"object"`
	Created int64  `json:"created"`
	OwnedBy string `json:"owned_by"`
}

type Plugin struct {
	config *Config

	isMaster     bool
	masterID     string
	masterAddr   string
	term         int64
	isCandidate  bool
	peers        []string
	serviceID    string

	modelToBackends map[string]*ModelBackend
	sessionToBackend map[string]*SessionInfo

	httpServer *http.Server

	lastHeartbeat    int64 // unix nano, updated by handleHeartbeat, read by monitorHeartbeat
	electionAttempts int32 // capped at maxElectionRetries

	mu sync.RWMutex
	wg sync.WaitGroup

	stopChan chan struct{}
	Healthy  bool
}

func NewPlugin(cfg *Config) *Plugin {
	return &Plugin{
		config:           cfg,
		modelToBackends:  make(map[string]*ModelBackend),
		sessionToBackend: make(map[string]*SessionInfo),
		stopChan:         make(chan struct{}),
		Healthy:          true,
	}
}

func (p *Plugin) GetMasterID() string {
	p.mu.RLock()
	defer p.mu.RUnlock()
	return p.masterID
}

func (p *Plugin) IsMaster() bool {
	p.mu.RLock()
	defer p.mu.RUnlock()
	return p.isMaster
}
