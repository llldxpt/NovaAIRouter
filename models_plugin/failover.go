package main

import (
	"fmt"
	"io"
	"log"
	"net/http"
	"time"
)

func (p *Plugin) tryFailover(model, sessionID string, failedBackend *Backend, originalBody []byte, w http.ResponseWriter) bool {
	p.mu.RLock()
	mb, ok := p.modelToBackends[model]
	p.mu.RUnlock()

	if !ok {
		return false
	}

	candidates := make([]*Backend, 0)
	for _, b := range mb.Backends {
		if b != failedBackend && b.Healthy {
			candidates = append(candidates, b)
		}
	}

	if len(candidates) == 0 {
		return false
	}

	newBackend := p.selectBackendByScore(candidates)

	if sessionID != "" {
		p.unbindSession(sessionID)
		p.bindSession(sessionID, newBackend)
	}

	url := fmt.Sprintf("http://%s:%d%s", newBackend.Address, newBackend.Port, newBackend.BackendPath)
	resp, err := p.forwardRequestWithTimeout(url, originalBody, 30*time.Second)

	if err != nil {
		log.Printf("Failover to %s failed: %v", newBackend.Address, err)
		return p.tryFailover(model, sessionID, newBackend, originalBody, w)
	}

	defer resp.Body.Close()
	respBody, _ := io.ReadAll(resp.Body)

	p.updateBackendLatency(newBackend, resp.Header.Get("X-Latency"))

	w.Header().Set("Content-Type", "application/json")
	w.Write(respBody)

	return true
}

func (p *Plugin) forwardRequest(url string, r *http.Request) (*http.Response, error) {
	client := &http.Client{
		Timeout: 30 * time.Second,
	}
	return client.Do(r)
}

func (p *Plugin) retryRequest(backend *Backend, model, sessionID string) bool {
	return true
}
