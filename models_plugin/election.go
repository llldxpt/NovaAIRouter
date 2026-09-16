package main

import (
	"encoding/json"
	"fmt"
	"log"
	"math/rand"
	"net/http"
	"strings"
	"sync/atomic"
	"time"
)

func (p *Plugin) getPeersAndStartElection() {
	time.Sleep(2 * time.Second)
	p.getPeers()

	if len(p.peers) == 0 {
		p.mu.Lock()
		p.isMaster = true
		p.masterID = p.serviceID
		p.mu.Unlock()
		log.Println("No peers found, becoming master")
		return
	}

	go p.startElection()
}

func (p *Plugin) getPeers() {
	if p.serviceID == "" {
		return
	}

	data, _ := json.Marshal(map[string]string{
		"service_id": p.serviceID,
	})

	url := fmt.Sprintf("http://%s/v1/plugin/peers", p.config.RouterAddr)
	resp, err := http.Post(url, "application/json", strings.NewReader(string(data)))
	if err != nil {
		log.Printf("Get peers error: %v", err)
		return
	}
	defer resp.Body.Close()

	var result struct {
		Peers []string `json:"peers"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		log.Printf("Parse peers error: %v", err)
		return
	}

	p.mu.Lock()
	p.peers = result.Peers
	p.mu.Unlock()

	log.Printf("Found %d peers", len(result.Peers))
}

func (p *Plugin) startElection() {
	p.mu.Lock()
	p.term++
	p.isCandidate = true
	candidateID := p.serviceID
	p.mu.Unlock()

	voteCount := int32(1)

	for _, peer := range p.peers {
		if peer == p.serviceID {
			continue
		}

		if p.sendVoteRequest(peer, p.term, candidateID) {
			atomic.AddInt32(&voteCount, 1)
		}
	}

	if atomic.LoadInt32(&voteCount) > int32(len(p.peers))/2 {
		atomic.StoreInt32(&p.electionAttempts, 0)
		p.becomeMaster()
	} else {
		p.mu.Lock()
		p.isCandidate = false
		p.mu.Unlock()

		if atomic.AddInt32(&p.electionAttempts, 1) > 5 {
			log.Println("Max election retries reached, pausing elections for 15s")
			time.Sleep(15 * time.Second)
			atomic.StoreInt32(&p.electionAttempts, 0)
		} else {
			waitTime := time.Duration(rand.Intn(1000)) * time.Millisecond
			time.Sleep(waitTime)
		}
		go p.startElection()
	}
}

func (p *Plugin) becomeMaster() {
	p.mu.Lock()
	p.isMaster = true
	p.masterID = p.serviceID
	p.isCandidate = false
	p.mu.Unlock()

	p.broadcast(map[string]interface{}{
		"type":        "election",
		"action":      "victory",
		"term":        p.term,
		"master_id":   p.serviceID,
		"master_addr": p.config.PluginAddr,
	})

	log.Printf("Became master, term: %d", p.term)

	go p.sendHeartbeat()
}

func (p *Plugin) sendVoteRequest(peer string, term int64, candidateID string) bool {
	data, _ := json.Marshal(map[string]interface{}{
		"type":         "election",
		"action":       "vote",
		"term":         term,
		"candidate_id": candidateID,
		"from":         p.serviceID,
	})

	url := fmt.Sprintf("http://%s/v1/plugin/send", p.config.RouterAddr)
	resp, err := http.Post(url, "application/json", strings.NewReader(string(data)))
	if err != nil {
		return false
	}
	defer resp.Body.Close()

	return resp.StatusCode == http.StatusOK
}

func (p *Plugin) broadcast(msg map[string]interface{}) {
	data, _ := json.Marshal(msg)

	url := fmt.Sprintf("http://%s/v1/plugin/broadcast", p.config.RouterAddr)
	resp, err := http.Post(url, "application/json", strings.NewReader(string(data)))
	if err != nil {
		log.Printf("Broadcast error: %v", err)
		return
	}
	defer resp.Body.Close()
}

func (p *Plugin) sendPluginMessage(toServiceID string, msg map[string]interface{}) error {
	data, _ := json.Marshal(map[string]interface{}{
		"to_service_id": toServiceID,
		"message":       msg,
	})

	url := fmt.Sprintf("http://%s/v1/plugin/send", p.config.RouterAddr)
	resp, err := http.Post(url, "application/json", strings.NewReader(string(data)))
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	return nil
}

func (p *Plugin) sendHeartbeat() {
	ticker := time.NewTicker(p.config.HeartbeatInterval())
	defer ticker.Stop()

	for {
		select {
		case <-p.stopChan:
			return
		case <-ticker.C:
			p.mu.RLock()
			isMaster := p.isMaster
			p.mu.RUnlock()

			if !isMaster {
				return
			}

			p.broadcast(map[string]interface{}{
				"type":        "heartbeat",
				"master_id":   p.serviceID,
				"master_addr": p.config.PluginAddr,
				"timestamp":   time.Now().Unix(),
			})
		}
	}
}

func (p *Plugin) monitorHeartbeat() {
	ticker := time.NewTicker(p.config.ElectionTimeout())
	defer ticker.Stop()

	for {
		select {
		case <-p.stopChan:
			return
		case <-ticker.C:
			p.mu.RLock()
			isMaster := p.isMaster
			masterID := p.masterID
			p.mu.RUnlock()

			if isMaster {
				atomic.StoreInt64(&p.lastHeartbeat, time.Now().UnixNano())
			} else if masterID != "" {
				lastHb := time.Unix(0, atomic.LoadInt64(&p.lastHeartbeat))
				if time.Since(lastHb) > p.config.ElectionTimeout() {
					log.Println("Master heartbeat timeout, starting election")
					go p.startElection()
				}
			}
		}
	}
}

func (p *Plugin) handleMessage(msg map[string]interface{}) {
	msgType, _ := msg["type"].(string)

	switch msgType {
	case "election":
		action, _ := msg["action"].(string)
		switch action {
		case "vote":
			p.handleVoteRequest(msg)
		case "victory":
			p.handleVictory(msg)
		}
	case "heartbeat":
		p.handleHeartbeat(msg)
	case "session_sync":
		p.handleSessionSync(msg)
	}
}

func (p *Plugin) handleVoteRequest(msg map[string]interface{}) {
	term, _ := msg["term"].(int64)
	candidateID, _ := msg["candidate_id"].(string)

	p.mu.Lock()
	defer p.mu.Unlock()

	if term > p.term {
		p.term = term

		p.sendPluginMessage(candidateID, map[string]interface{}{
			"type":   "election",
			"action": "vote_response",
			"term":   p.term,
			"voted":  true,
		})
	}
}

func (p *Plugin) handleVictory(msg map[string]interface{}) {
	masterID, _ := msg["master_id"].(string)
	masterAddr, _ := msg["master_addr"].(string)
	term, _ := msg["term"].(int64)

	p.mu.Lock()
	defer p.mu.Unlock()

	if term >= p.term {
		p.isMaster = false
		p.masterID = masterID
		p.masterAddr = masterAddr
		p.term = term
		p.isCandidate = false

		log.Printf("Accepted master: %s (%s), term: %d", masterID, masterAddr, term)
	}
}

func (p *Plugin) handleHeartbeat(msg map[string]interface{}) {
	masterID, _ := msg["master_id"].(string)
	masterAddr, _ := msg["master_addr"].(string)

	atomic.StoreInt64(&p.lastHeartbeat, time.Now().UnixNano())

	p.mu.Lock()
	p.masterID = masterID
	if masterAddr != "" {
		p.masterAddr = masterAddr
	}
	p.mu.Unlock()
}

func (p *Plugin) handleSessionSync(msg map[string]interface{}) {
	action, _ := msg["action"].(string)
	session, _ := msg["session"].(map[string]interface{})

	p.mu.Lock()
	defer p.mu.Unlock()

	switch action {
	case "bind":
		sessionID, _ := session["session_id"].(string)
		addr, _ := session["backend_addr"].(string)
		port, _ := session["backend_port"].(int)
		lastAct, _ := session["last_activity"].(int64)

		p.sessionToBackend[sessionID] = &SessionInfo{
			SessionID:    sessionID,
			BackendAddr:  addr,
			BackendPort:  port,
			LastActivity: time.Unix(lastAct, 0),
		}

	case "unbind":
		sessionID, _ := session["session_id"].(string)
		delete(p.sessionToBackend, sessionID)
	}
}
