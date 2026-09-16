package main

import (
	"embed"
	"encoding/json"
	"io/fs"
	"net/http"
	"strings"
	"sync"
	"time"
)

//go:embed webui
var webui embed.FS

type ManagerServer struct {
	gateway *ManagedProcess
	plugin  *ManagedProcess
	mu      sync.RWMutex
	events  []Event
}

type Event struct {
	Time      time.Time    `json:"time"`
	Component string       `json:"component"`
	State     ProcessState `json:"state"`
}

type StatusResponse struct {
	Gateway ProcessStatus `json:"gateway"`
	Plugin  ProcessStatus `json:"plugin"`
}

type ProcessStatus struct {
	State  ProcessState `json:"state"`
	Uptime string       `json:"uptime"`
}

func NewManagerServer(gatewayBin, gatewayDir, pluginBin, pluginDir string) *ManagerServer {
	ms := &ManagerServer{
		events: make([]Event, 0, 100),
	}

	ms.gateway = NewManagedProcess("gateway", gatewayBin, gatewayDir, nil)
	ms.gateway.onStateChange = ms.recordEvent

	ms.plugin = NewManagedProcess("plugin", pluginBin, pluginDir, nil)
	ms.plugin.onStateChange = ms.recordEvent

	return ms
}

func (ms *ManagerServer) recordEvent(component string, state ProcessState) {
	ms.mu.Lock()
	defer ms.mu.Unlock()
	ms.events = append(ms.events, Event{
		Time:      time.Now(),
		Component: component,
		State:     state,
	})
	if len(ms.events) > 50 {
		ms.events = ms.events[len(ms.events)-50:]
	}
}

func (ms *ManagerServer) getStatus() StatusResponse {
	return StatusResponse{
		Gateway: ProcessStatus{
			State:  ms.gateway.GetState(),
			Uptime: ms.gateway.Uptime().String(),
		},
		Plugin: ProcessStatus{
			State:  ms.plugin.GetState(),
			Uptime: ms.plugin.Uptime().String(),
		},
	}
}

func (ms *ManagerServer) Start() error {
	mux := http.NewServeMux()

	// API endpoints
	mux.HandleFunc("/api/status", ms.handleStatus)
	mux.HandleFunc("/api/start", ms.handleStart)
	mux.HandleFunc("/api/stop", ms.handleStop)
	mux.HandleFunc("/api/logs", ms.handleLogs)
	mux.HandleFunc("/api/events", ms.handleEvents)

	// Web UI
	subFS, _ := fs.Sub(webui, "webui")
	mux.Handle("/", http.FileServer(http.FS(subFS)))

	return http.ListenAndServe(":15048", mux)
}

func (ms *ManagerServer) handleStatus(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(ms.getStatus())
}

func (ms *ManagerServer) handleStart(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	var req struct {
		Component string   `json:"component"`
		Args      []string `json:"args"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "invalid json", http.StatusBadRequest)
		return
	}

	var mp *ManagedProcess
	switch strings.ToLower(req.Component) {
	case "gateway":
		mp = ms.gateway
		if len(req.Args) > 0 {
			mp.Args = req.Args
		}
	case "plugin":
		mp = ms.plugin
	default:
		http.Error(w, "unknown component: "+req.Component, http.StatusBadRequest)
		return
	}

	if err := mp.Start(); err != nil {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusConflict)
		json.NewEncoder(w).Encode(map[string]string{"error": err.Error()})
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]string{"status": "ok"})
}

func (ms *ManagerServer) handleStop(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	var req struct {
		Component string `json:"component"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "invalid json", http.StatusBadRequest)
		return
	}

	var mp *ManagedProcess
	switch strings.ToLower(req.Component) {
	case "gateway":
		mp = ms.gateway
	case "plugin":
		mp = ms.plugin
	default:
		http.Error(w, "unknown component: "+req.Component, http.StatusBadRequest)
		return
	}

	if err := mp.Stop(); err != nil {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusConflict)
		json.NewEncoder(w).Encode(map[string]string{"error": err.Error()})
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]string{"status": "ok"})
}

func (ms *ManagerServer) handleLogs(w http.ResponseWriter, r *http.Request) {
	component := r.URL.Query().Get("component")

	var logs []string
	switch strings.ToLower(component) {
	case "gateway":
		logs = ms.gateway.GetLogs()
	case "plugin":
		logs = ms.plugin.GetLogs()
	default:
		http.Error(w, "unknown component", http.StatusBadRequest)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]interface{}{
		"component": component,
		"logs":      logs,
		"count":     len(logs),
	})
}

func (ms *ManagerServer) handleEvents(w http.ResponseWriter, r *http.Request) {
	ms.mu.RLock()
	events := make([]Event, len(ms.events))
	copy(events, ms.events)
	ms.mu.RUnlock()

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]interface{}{
		"events": events,
	})
}
