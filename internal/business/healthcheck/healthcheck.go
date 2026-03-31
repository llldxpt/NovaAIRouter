package healthcheck

import (
	"sync"
	"time"

	"github.com/rs/zerolog"
)

const (
	FailureThreshold = 3
	RecoveryInterval = 30 * time.Second
)

type EndpointHealth struct {
	ConsecutiveFailures int
	LastFailureTime     time.Time
	LastSuccessTime     time.Time
	IsHealthy           bool
}

type PassiveHealthChecker struct {
	mu     sync.RWMutex
	states map[string]*EndpointHealth
	log    zerolog.Logger
}

func New(log zerolog.Logger) *PassiveHealthChecker {
	return &PassiveHealthChecker{
		states: make(map[string]*EndpointHealth),
		log:    log,
	}
}

func (h *PassiveHealthChecker) RecordSuccess(path string) {
	h.mu.Lock()
	defer h.mu.Unlock()

	if _, exists := h.states[path]; !exists {
		h.states[path] = &EndpointHealth{IsHealthy: true}
	}

	state := h.states[path]
	state.ConsecutiveFailures = 0
	state.LastSuccessTime = time.Now()
	state.IsHealthy = true
}

func (h *PassiveHealthChecker) RecordFailure(path string) {
	h.mu.Lock()
	defer h.mu.Unlock()

	if _, exists := h.states[path]; !exists {
		h.states[path] = &EndpointHealth{IsHealthy: true}
	}

	state := h.states[path]
	state.ConsecutiveFailures++
	state.LastFailureTime = time.Now()

	if state.ConsecutiveFailures >= FailureThreshold {
		state.IsHealthy = false
		h.log.Warn().Str("path", path).Int("failures", state.ConsecutiveFailures).Msg("Endpoint marked unhealthy")
	}
}

func (h *PassiveHealthChecker) IsHealthy(path string) bool {
	h.mu.RLock()
	defer h.mu.RUnlock()

	state, exists := h.states[path]
	if !exists {
		return true
	}
	return state.IsHealthy
}

func (h *PassiveHealthChecker) ShouldAllowProbe(path string) bool {
	h.mu.RLock()
	defer h.mu.RUnlock()

	state, exists := h.states[path]
	if !exists || state.IsHealthy {
		return true
	}

	return time.Since(state.LastFailureTime) >= RecoveryInterval
}
