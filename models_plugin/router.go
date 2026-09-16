package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"strings"
	"sync/atomic"
	"time"
)

func (p *Plugin) handleChat(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	if !p.IsMaster() {
		p.forwardToMaster(w, r, "/v1/chat/completions")
		return
	}

	body, err := io.ReadAll(r.Body)
	if err != nil {
		http.Error(w, "read request body", http.StatusBadRequest)
		return
	}

	var req struct {
		Model     string `json:"model"`
		SessionID string `json:"session_id"`
	}
	if err := json.Unmarshal(body, &req); err != nil {
		http.Error(w, "parse request", http.StatusBadRequest)
		return
	}

	backend, err := p.selectBackend(req.Model, req.SessionID)
	if err != nil {
		http.Error(w, err.Error(), http.StatusServiceUnavailable)
		return
	}

	atomic.AddInt32(&backend.ActiveConn, 1)
	defer atomic.AddInt32(&backend.ActiveConn, -1)

	url := fmt.Sprintf("http://%s:%d%s", backend.Address, backend.Port, backend.BackendPath)
	resp, err := p.forwardRequestWithTimeout(url, body, 30*time.Second)

	if err != nil {
		if p.tryFailover(req.Model, req.SessionID, backend, body, w) {
			return
		}
		http.Error(w, "backend error", http.StatusBadGateway)
		return
	}

	defer resp.Body.Close()
	respBody, _ := io.ReadAll(resp.Body)

	if req.SessionID != "" {
		p.updateSessionActivity(req.SessionID)
		p.syncSession(req.SessionID, backend)
	}

	p.updateBackendLatency(backend, resp.Header.Get("X-Latency"))

	w.Header().Set("Content-Type", "application/json")
	w.Write(respBody)
}

func (p *Plugin) handleModels(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	if !p.IsMaster() {
		p.forwardToMaster(w, r, "/v1/models")
		return
	}

	p.mu.RLock()
	defer p.mu.RUnlock()

	allModels := make([]ModelInfo, 0)
	seen := make(map[string]bool)

	for _, mb := range p.modelToBackends {
		for _, backend := range mb.Backends {
			models := p.fetchBackendModels(backend)
			for _, m := range models {
				if !seen[m.ID] {
					seen[m.ID] = true
					allModels = append(allModels, m)
				}
			}
		}
	}

	json.NewEncoder(w).Encode(map[string]interface{}{
		"data": allModels,
	})
}

func (p *Plugin) handlePluginMessage(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	body, err := io.ReadAll(r.Body)
	if err != nil {
		http.Error(w, "read request body", http.StatusBadRequest)
		return
	}

	var msg map[string]interface{}
	if err := json.Unmarshal(body, &msg); err != nil {
		http.Error(w, "parse message", http.StatusBadRequest)
		return
	}

	p.handleMessage(msg)

	w.WriteHeader(http.StatusOK)
	json.NewEncoder(w).Encode(map[string]string{"status": "ok"})
}

func (p *Plugin) handleHealth(w http.ResponseWriter, r *http.Request) {
	w.WriteHeader(http.StatusOK)
	json.NewEncoder(w).Encode(map[string]bool{"healthy": p.Healthy})
}

func (p *Plugin) handleStatus(w http.ResponseWriter, r *http.Request) {
	p.mu.RLock()
	defer p.mu.RUnlock()

	status := map[string]interface{}{
		"is_master":      p.isMaster,
		"master_id":      p.masterID,
		"service_id":     p.serviceID,
		"term":           p.term,
		"peer_count":     len(p.peers),
		"session_count":  len(p.sessionToBackend),
		"model_count":    len(p.modelToBackends),
		"lb_algorithm":   p.config.LBAlgorithm,
	}

	json.NewEncoder(w).Encode(status)
}

func (p *Plugin) forwardToMaster(w http.ResponseWriter, r *http.Request, path string) {
	p.mu.RLock()
	masterID := p.masterID
	masterAddr := p.masterAddr
	p.mu.RUnlock()

	if masterID == "" || masterAddr == "" {
		http.Error(w, "master not available", http.StatusServiceUnavailable)
		return
	}

	log.Printf("Forwarding request to master: %s (%s)", masterID, masterAddr)

	url := fmt.Sprintf("http://%s%s", masterAddr, path)
	resp, err := http.Post(url, r.Header.Get("Content-Type"), r.Body)
	if err != nil {
		log.Printf("Forward to master failed: %v", err)
		http.Error(w, "forward to master failed", http.StatusBadGateway)
		return
	}
	defer resp.Body.Close()

	w.Header().Set("Content-Type", "application/json")
	io.Copy(w, resp.Body)
}

var pluginHTTPClient = &http.Client{
	Timeout: 30 * time.Second,
	Transport: &http.Transport{
		MaxIdleConns:        20,
		MaxIdleConnsPerHost: 5,
		IdleConnTimeout:     90 * time.Second,
	},
}

func (p *Plugin) forwardRequestWithTimeout(url string, body []byte, timeout time.Duration) (*http.Response, error) {
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, strings.NewReader(string(body)))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")

	return pluginHTTPClient.Do(req)
}

func (p *Plugin) fetchBackendModels(backend *Backend) []ModelInfo {
	url := fmt.Sprintf("http://%s:%d/v1/models", backend.Address, backend.Port)
	client := &http.Client{Timeout: 10 * time.Second}
	resp, err := client.Get(url)
	if err != nil {
		return nil
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil
	}

	var result struct {
		Data []ModelInfo `json:"data"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil
	}

	return result.Data
}

func (p *Plugin) updateBackendLatency(backend *Backend, latencyStr string) {
	var latency int64
	fmt.Sscanf(latencyStr, "%d", &latency)

	oldLatency := atomic.LoadInt64(&backend.AvgLatency)
	if oldLatency == 0 {
		atomic.StoreInt64(&backend.AvgLatency, latency)
	} else {
		newLatency := (oldLatency + latency) / 2
		atomic.StoreInt64(&backend.AvgLatency, newLatency)
	}
}
