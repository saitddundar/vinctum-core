package relay

import (
	"errors"
	"sync"
	"time"
)

var ErrCircuitOpen = errors.New("circuit breaker is open")

type circuitState int

const (
	stateClosed circuitState = iota
	stateOpen
	stateHalfOpen
)

type CircuitBreaker struct {
	mu           sync.Mutex
	state        circuitState
	failures     int
	maxFailures  int
	cooldown     time.Duration
	lastFailTime time.Time
}

func NewCircuitBreaker(maxFailures int, cooldown time.Duration) *CircuitBreaker {
	return &CircuitBreaker{
		state:       stateClosed,
		maxFailures: maxFailures,
		cooldown:    cooldown,
	}
}

func (cb *CircuitBreaker) Allow() error {
	cb.mu.Lock()
	defer cb.mu.Unlock()

	switch cb.state {
	case stateClosed:
		return nil
	case stateOpen:
		if time.Since(cb.lastFailTime) > cb.cooldown {
			cb.state = stateHalfOpen
			return nil
		}
		return ErrCircuitOpen
	case stateHalfOpen:
		return nil
	}
	return nil
}

func (cb *CircuitBreaker) RecordSuccess() {
	cb.mu.Lock()
	defer cb.mu.Unlock()
	cb.failures = 0
	cb.state = stateClosed
}

func (cb *CircuitBreaker) RecordFailure() {
	cb.mu.Lock()
	defer cb.mu.Unlock()
	cb.failures++
	cb.lastFailTime = time.Now()
	if cb.failures >= cb.maxFailures {
		cb.state = stateOpen
	}
}

func (cb *CircuitBreaker) IsOpen() bool {
	cb.mu.Lock()
	defer cb.mu.Unlock()
	return cb.state == stateOpen && time.Since(cb.lastFailTime) <= cb.cooldown
}
