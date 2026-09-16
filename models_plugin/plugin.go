package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"strings"
	"time"
)

func (p *Plugin) Start() error {
	if err := p.registerEndpoints(); err != nil {
		return fmt.Errorf("register endpoints: %w", err)
	}

	p.startHTTPServer()
	p.startBackgroundTasks()

	return nil
}

func (p *Plugin) Stop() {
	close(p.stopChan)

	if p.httpServer != nil {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		p.httpServer.Shutdown(ctx)
	}

	p.wg.Wait()
	log.Println("Plugin stopped")
}

func (p *Plugin) registerEndpoints() error {
	endpoints := []EndpointRegistration{
		{
			NodePath:      "/v1/chat/completions",
			ServicePath:   "18011/v1/chat/completions",
			Plugin:        true,
			Description:   "router",
			MaxConcurrent: 100,
		},
		{
			NodePath:      "/v1/models",
			ServicePath:   "18011/v1/models",
			Plugin:        true,
			Description:   "router",
			MaxConcurrent: 100,
		},
	}

	data, err := json.Marshal(endpoints)
	if err != nil {
		return fmt.Errorf("marshal endpoints: %w", err)
	}

	url := fmt.Sprintf("http://%s/v1/endpoints", p.config.RouterAddr)
	resp, err := http.Post(url, "application/json", strings.NewReader(string(data)))
	if err != nil {
		return fmt.Errorf("register endpoints request: %w", err)
	}
	defer resp.Body.Close()

	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("register failed: %s", string(body))
	}

	var result struct {
		ServiceID string   `json:"service_id"`
		Endpoints []string `json:"endpoints"`
	}
	if err := json.Unmarshal(body, &result); err != nil {
		return fmt.Errorf("parse response: %w", err)
	}

	p.serviceID = result.ServiceID
	log.Printf("Registered with service_id: %s", p.serviceID)

	return nil
}

func (p *Plugin) startHTTPServer() {
	mux := http.NewServeMux()

	mux.HandleFunc("/v1/chat/completions", p.handleChat)
	mux.HandleFunc("/v1/models", p.handleModels)
	mux.HandleFunc("/v1/plugin/receive", p.handlePluginMessage)
	mux.HandleFunc("/health", p.handleHealth)
	mux.HandleFunc("/v1/status", p.handleStatus)

	p.httpServer = &http.Server{
		Addr:    p.config.PluginAddr,
		Handler: mux,
	}

	p.wg.Add(1)
	go func() {
		defer p.wg.Done()
		if err := p.httpServer.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.Printf("HTTP server error: %v", err)
		}
	}()
}

func (p *Plugin) startBackgroundTasks() {
	p.wg.Add(1)
	go p.sendHeartbeatToRouter()

	p.wg.Add(1)
	go p.syncClusterInfo()

	p.wg.Add(1)
	go p.cleanupExpiredSessions()

	p.wg.Add(1)
	go p.monitorHeartbeat()

	p.wg.Add(1)
	go p.getPeersAndStartElection()
}

type EndpointRegistration struct {
	NodePath      string `json:"node_path"`
	ServicePath   string `json:"service_path"`
	Plugin        bool   `json:"plugin"`
	Description   string `json:"description"`
	MaxConcurrent int    `json:"max_concurrent"`
}
