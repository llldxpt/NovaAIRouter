package circuitbreaker

import (
	"sync"
	"sync/atomic"
	"time"

	"github.com/rs/zerolog"
)

type State int

const (
	Closed State = iota
	Open
	HalfOpen
)

const (
	DefaultFailureThreshold = 5
	DefaultSuccessThreshold = 2
	DefaultTimeout          = 30 * time.Second
	DefaultHalfOpenMaxReqs  = 1
)

type CircuitBreaker struct {
	mu               sync.Mutex
	state            State
	failureCount     int
	successCount     int
	failureThreshold int
	successThreshold int
	timeout          time.Duration
	lastFailureTime  time.Time
	halfOpenMaxReqs  int
	activeHalfOpen   int32
	log              zerolog.Logger
}

func New(log zerolog.Logger) *CircuitBreaker {
	return &CircuitBreaker{
		state:            Closed,
		failureThreshold: DefaultFailureThreshold,
		successThreshold: DefaultSuccessThreshold,
		timeout:          DefaultTimeout,
		halfOpenMaxReqs:  DefaultHalfOpenMaxReqs,
		log:              log,
	}
}

func (cb *CircuitBreaker) Allow() bool {
	cb.mu.Lock()
	defer cb.mu.Unlock()

	switch cb.state {
	case Closed:
		return true
	case Open:
		if time.Since(cb.lastFailureTime) >= cb.timeout {
			cb.state = HalfOpen
			cb.successCount = 0
			cb.failureCount = 0
			cb.log.Info().Msg("Circuit breaker transitioning to HalfOpen")
			return atomic.LoadInt32(&cb.activeHalfOpen) < int32(cb.halfOpenMaxReqs)
		}
		return false
	case HalfOpen:
		return atomic.LoadInt32(&cb.activeHalfOpen) < int32(cb.halfOpenMaxReqs)
	}
	return false
}

func (cb *CircuitBreaker) RecordSuccess() {
	cb.mu.Lock()
	defer cb.mu.Unlock()

	if cb.state == HalfOpen {
		atomic.AddInt32(&cb.activeHalfOpen, -1)
		cb.successCount++
		if cb.successCount >= cb.successThreshold {
			cb.state = Closed
			cb.failureCount = 0
			cb.log.Info().Msg("Circuit breaker closed after successful recovery")
		}
	} else if cb.state == Closed {
		cb.failureCount = 0
	}
}

func (cb *CircuitBreaker) RecordFailure() {
	cb.mu.Lock()
	defer cb.mu.Unlock()

	cb.lastFailureTime = time.Now()

	if cb.state == HalfOpen {
		atomic.AddInt32(&cb.activeHalfOpen, -1)
		cb.state = Open
		cb.log.Warn().Msg("Circuit breaker opened after HalfOpen failure")
		return
	}

	cb.failureCount++
	if cb.failureCount >= cb.failureThreshold {
		cb.state = Open
		cb.log.Warn().Int("failures", cb.failureCount).Msg("Circuit breaker opened")
	}
}

func (cb *CircuitBreaker) State() State {
	cb.mu.Lock()
	defer cb.mu.Unlock()
	return cb.state
}

func (cb *CircuitBreaker) Reset() {
	cb.mu.Lock()
	defer cb.mu.Unlock()
	cb.state = Closed
	cb.failureCount = 0
	cb.successCount = 0
	atomic.StoreInt32(&cb.activeHalfOpen, 0)
}
