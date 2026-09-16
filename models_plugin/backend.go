package main

import (
	"fmt"
	"math/rand"
	"sync/atomic"
	"time"
)

const defaultMaxConcurrent = 10

func weightedRandomIndex(weights []int32) int {
	total := int32(0)
	for _, w := range weights {
		total += w
	}
	if total <= 0 {
		return rand.Intn(len(weights))
	}
	r := rand.Int31n(total)
	for i, w := range weights {
		r -= w
		if r < 0 {
			return i
		}
	}
	return len(weights) - 1
}

func (p *Plugin) selectBackend(model, sessionID string) (*Backend, error) {
	p.mu.RLock()
	mb, ok := p.modelToBackends[model]
	p.mu.RUnlock()

	if !ok {
		return nil, fmt.Errorf("model %s not found", model)
	}

	if sessionID != "" {
		if session, bound := p.getSessionBackend(sessionID); bound {
			if session.LastActivity.Add(p.config.SessionTimeout()).After(time.Now()) {
				backend := p.findBackend(session.BackendAddr, session.BackendPort, mb)
				if backend != nil && backend.Healthy {
					return backend, nil
				}
				p.unbindSession(sessionID)
			} else {
				p.unbindSession(sessionID)
			}
		}
	}

	backend := p.selectBackendByScore(mb.Backends)
	if backend == nil {
		return nil, fmt.Errorf("no healthy backend available")
	}

	if sessionID != "" {
		p.bindSession(sessionID, backend)
	}

	return backend, nil
}

// selectBackendByScore mirrors novaairouter's balancer.SelectNode scoring logic.
// Calculates a composite score from load ratio (60%) and response time (40%),
// then picks from the best candidates using max_concurrent-weighted random.
func (p *Plugin) selectBackendByScore(backends []*Backend) *Backend {
	// Filter healthy backends
	var healthy []*Backend
	for _, b := range backends {
		if b.Healthy {
			healthy = append(healthy, b)
		}
	}
	if len(healthy) == 0 {
		return nil
	}
	if len(healthy) == 1 {
		return healthy[0]
	}

	type backendScore struct {
		b     *Backend
		score float64
	}

	scores := make([]backendScore, 0, len(healthy))
	for _, b := range healthy {
		// 1. Load ratio = ActiveConn / MaxConcurrent (same as gateway)
		maxConc := float64(b.MaxConcurrent)
		if maxConc < 1 {
			maxConc = defaultMaxConcurrent
		}
		loadRatio := float64(atomic.LoadInt32(&b.ActiveConn)) / maxConc

		// 2. Response time factor (> 100ms starts to matter, capped at 1.0)
		latency := atomic.LoadInt64(&b.AvgLatency)
		rtFactor := 0.0
		if latency > 0 {
			rtFactor = float64(latency) / 100.0 // latency is in ms
			if rtFactor > 1.0 {
				rtFactor = 1.0
			}
		}

		// 3. Weighted composite score (lower is better)
		//    load_ratio 60%, response_time 40% — same as gateway
		totalScore := loadRatio*0.6 + rtFactor*0.4

		// 4. Random jitter ±5% to avoid always selecting the same backend
		randomFactor := 1.0 + (rand.Float64()*0.1 - 0.05)
		score := totalScore * randomFactor

		scores = append(scores, backendScore{b: b, score: score})
	}

	// Find best (minimum) score
	bestScore := scores[0].score
	for _, s := range scores[1:] {
		if s.score < bestScore {
			bestScore = s.score
		}
	}

	// Collect candidates within 5% of best score (tolerance for random jitter)
	var candidates []*Backend
	var weights []int32
	for _, s := range scores {
		if s.score <= bestScore*1.05+0.001 {
			candidates = append(candidates, s.b)
			w := s.b.MaxConcurrent
			if w < 1 {
				w = defaultMaxConcurrent
			}
			weights = append(weights, w)
		}
	}

	if len(candidates) == 1 {
		return candidates[0]
	}

	// Weighted random selection among best candidates by MaxConcurrent
	return candidates[weightedRandomIndex(weights)]
}

func (p *Plugin) findBackend(addr string, port int, mb *ModelBackend) *Backend {
	for _, b := range mb.Backends {
		if b.Address == addr && b.Port == port {
			return b
		}
	}
	return nil
}

func init() {
	rand.Seed(time.Now().UnixNano())
}
