package main

import (
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"strconv"
	"strings"
	"time"
)

func (p *Plugin) sendHeartbeatToRouter() {
	ticker := time.NewTicker(p.config.HeartbeatToRouter())
	defer ticker.Stop()

	for {
		select {
		case <-p.stopChan:
			return
		case <-ticker.C:
			if p.serviceID == "" {
				continue
			}

			data, _ := json.Marshal(map[string]interface{}{
				"service_id": p.serviceID,
				"healthy":    p.Healthy,
			})

			url := fmt.Sprintf("http://%s/v1/heartbeat", p.config.RouterAddr)
			resp, err := http.Post(url, "application/json", jsonToReader(data))
			if err != nil {
				log.Printf("Heartbeat error: %v", err)
				continue
			}
			resp.Body.Close()
		}
	}
}

func (p *Plugin) syncClusterInfo() {
	p.doSync()

	ticker := time.NewTicker(p.config.SyncInterval())
	defer ticker.Stop()

	for {
		select {
		case <-p.stopChan:
			return
		case <-ticker.C:
			p.doSync()
		}
	}
}

func (p *Plugin) doSync() {
	url := fmt.Sprintf("http://%s/v1/global", p.config.RouterAddr)
	resp, err := http.Get(url)
	if err != nil {
		log.Printf("Sync cluster info error: %v", err)
		return
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return
	}

	var result struct {
		Nodes []NodeInfo `json:"nodes"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		log.Printf("Parse global info error: %v", err)
		return
	}

	newBackends := make(map[string]*ModelBackend)

	// Get plugin's own listen port to avoid querying itself
	ownPort := p.pluginPort()

	for _, node := range result.Nodes {
		if !node.Healthy {
			continue
		}

		for _, pathInfo := range node.PathInfos {
			// Skip endpoints without service_path — they are from remote nodes
			// that don't expose individual backend ports, so we can't route to them
			if pathInfo.ServicePath == "" {
				continue
			}

			// Parse backend port and path from service_path: format "PORT/path"
			parts := strings.SplitN(pathInfo.ServicePath, "/", 2)
			port, err := strconv.Atoi(parts[0])
			if err != nil {
				continue
			}

			// Skip plugin's own registrations to avoid self-referencing loop
			if pathInfo.Plugin && port == ownPort {
				continue
			}

			backendPort := port
			backendPath := "/"
			if len(parts) > 1 {
				backendPath = "/" + parts[1]
			}

			nodeAddr, err := p.getNodeAddress(node.NodeID)
			if err != nil {
				continue
			}

			models := p.fetchModelsFromBackend(nodeAddr.Address, backendPort)
			for _, model := range models {
				mb, ok := newBackends[model.ID]
				if !ok {
					mb = &ModelBackend{Model: model.ID, Backends: make([]*Backend, 0)}
					newBackends[model.ID] = mb
				}

				maxConc := pathInfo.MaxConcurrent
				if maxConc < 1 {
					maxConc = 10
				}
				mb.Backends = append(mb.Backends, &Backend{
					Address:       nodeAddr.Address,
					Port:          backendPort,
					NodePath:      pathInfo.NodePath,
					BackendPath:   backendPath,
					MaxConcurrent: maxConc,
					Healthy:       true,
				})
			}
		}
	}

	// Preserve ActiveConn counts from old backends when merging
	p.mu.Lock()
	for modelID, newMB := range newBackends {
		if oldMB, ok := p.modelToBackends[modelID]; ok {
			for _, newB := range newMB.Backends {
				for _, oldB := range oldMB.Backends {
					if newB.Address == oldB.Address && newB.Port == oldB.Port {
						newB.ActiveConn = oldB.ActiveConn
						newB.MaxConcurrent = oldB.MaxConcurrent
						break
					}
				}
			}
		}
	}
	p.modelToBackends = newBackends
	p.mu.Unlock()

	log.Printf("Synced %d models", len(newBackends))
}

func (p *Plugin) getNodeAddress(nodeID string) (NodeAddress, error) {
	url := fmt.Sprintf("http://%s/v1/node/%s", p.config.RouterAddr, nodeID)
	resp, err := http.Get(url)
	if err != nil {
		return NodeAddress{}, err
	}
	defer resp.Body.Close()

	var addr NodeAddress
	if err := json.NewDecoder(resp.Body).Decode(&addr); err != nil {
		return NodeAddress{}, err
	}

	return addr, nil
}

func (p *Plugin) fetchModelsFromBackend(address string, port int) []ModelInfo {
	url := fmt.Sprintf("http://%s:%d/v1/models", address, port)
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
		log.Printf("Fetch models from backend error: %v", err)
		return nil
	}

	return result.Data
}

type NodeInfo struct {
	NodeID    string     `json:"node_id"`
	Healthy   bool       `json:"healthy"`
	PathInfos []PathInfo `json:"path_infos"`
}

type PathInfo struct {
	NodePath      string `json:"path"`
	ServicePath   string `json:"service_path"`
	Description   string `json:"description"`
	Plugin        bool   `json:"plugin"`
	MaxConcurrent int32  `json:"max_concurrent"`
}

type NodeAddress struct {
	NodeID      string `json:"node_id"`
	Address     string `json:"address"`
	ServicePort int    `json:"service_port"`
}

// pluginPort returns the plugin's own listen port number parsed from PluginAddr (e.g. ":15057" → 15057).
func (p *Plugin) pluginPort() int {
	addr := p.config.PluginAddr
	if addr == "" {
		return 0
	}
	// Strip leading colon and optional host, e.g. ":15057" or "127.0.0.1:15057"
	idx := strings.LastIndex(addr, ":")
	if idx < 0 {
		return 0
	}
	port, err := strconv.Atoi(addr[idx+1:])
	if err != nil {
		return 0
	}
	return port
}

func jsonToReader(data []byte) *strings.Reader {
	return strings.NewReader(string(data))
}
