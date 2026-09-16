package main

import (
	"log"
	"sync/atomic"
	"time"
)

func (p *Plugin) bindSession(sessionID string, backend *Backend) {
	p.mu.Lock()
	defer p.mu.Unlock()

	atomic.AddInt32(&backend.ActiveConn, 1)

	p.sessionToBackend[sessionID] = &SessionInfo{
		SessionID:    sessionID,
		BackendAddr:  backend.Address,
		BackendPort:  backend.Port,
		LastActivity: time.Now(),
	}
}

func (p *Plugin) unbindSession(sessionID string) {
	p.mu.Lock()
	defer p.mu.Unlock()

	if session, ok := p.sessionToBackend[sessionID]; ok {
		for _, mb := range p.modelToBackends {
			for _, b := range mb.Backends {
				if b.Address == session.BackendAddr && b.Port == session.BackendPort {
					atomic.AddInt32(&b.ActiveConn, -1)
					break
				}
			}
		}
	}
	delete(p.sessionToBackend, sessionID)
}

func (p *Plugin) getSessionBackend(sessionID string) (*SessionInfo, bool) {
	p.mu.RLock()
	defer p.mu.RUnlock()

	session, ok := p.sessionToBackend[sessionID]
	return session, ok
}

func (p *Plugin) updateSessionActivity(sessionID string) {
	p.mu.Lock()
	defer p.mu.Unlock()

	if session, ok := p.sessionToBackend[sessionID]; ok {
		session.LastActivity = time.Now()
	}
}

func (p *Plugin) cleanupExpiredSessions() {
	ticker := time.NewTicker(1 * time.Minute)
	defer ticker.Stop()

	for {
		select {
		case <-p.stopChan:
			return
		case <-ticker.C:
			p.mu.Lock()
			now := time.Now()
			for sessionID, session := range p.sessionToBackend {
				if now.Sub(session.LastActivity) > p.config.SessionTimeout() {
					for _, mb := range p.modelToBackends {
						for _, b := range mb.Backends {
							if b.Address == session.BackendAddr && b.Port == session.BackendPort {
								atomic.AddInt32(&b.ActiveConn, -1)
								break
							}
						}
					}
					delete(p.sessionToBackend, sessionID)
					log.Printf("Session expired: %s", sessionID)
				}
			}
			p.mu.Unlock()
		}
	}
}

func (p *Plugin) syncSession(sessionID string, backend *Backend) {
	if !p.IsMaster() {
		return
	}

	p.broadcast(map[string]interface{}{
		"type": "session_sync",
		"action": "bind",
		"session": map[string]interface{}{
			"session_id":     sessionID,
			"backend_addr":   backend.Address,
			"backend_port":   backend.Port,
			"last_activity": time.Now().Unix(),
		},
	})
}
