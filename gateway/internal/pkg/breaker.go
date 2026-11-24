package pkg

import (
	"errors"
	"sync"
	"time"
)

const (
	DefaultBreakerFailureThreshold = 5
	DefaultBreakerOpenTimeout      = 30 * time.Second
)

// ErrCircuitOpen is returned when the circuit breaker is open and fast-failing requests.
var ErrCircuitOpen = errors.New("circuit breaker is open")

// BreakerState represents the current state of a CircuitBreaker.
type BreakerState int

const (
	StateClosed BreakerState = iota
	StateOpen
	StateHalfOpen
)

func (s BreakerState) String() string {
	switch s {
	case StateClosed:
		return "closed"
	case StateOpen:
		return "open"
	case StateHalfOpen:
		return "half-open"
	default:
		return "unknown"
	}
}

// CircuitBreaker is a lock-safe 3-state (Closed, Open, HalfOpen) upstream circuit breaker.
type CircuitBreaker struct {
	mu                  sync.Mutex
	state               BreakerState
	failureThreshold    int
	openTimeout         time.Duration
	consecutiveFailures int
	openedAt            time.Time
	probeInFlight       bool
	now                 func() time.Time
}

// NewCircuitBreaker constructs a CircuitBreaker with the given failure threshold and open cooldown.
// Zero or negative values fall back to DefaultBreakerFailureThreshold (5) and DefaultBreakerOpenTimeout (30s).
func NewCircuitBreaker(failureThreshold int, openTimeout time.Duration) *CircuitBreaker {
	if failureThreshold <= 0 {
		failureThreshold = DefaultBreakerFailureThreshold
	}
	if openTimeout <= 0 {
		openTimeout = DefaultBreakerOpenTimeout
	}
	return &CircuitBreaker{
		state:            StateClosed,
		failureThreshold: failureThreshold,
		openTimeout:      openTimeout,
		now:              time.Now,
	}
}

// WithClock overrides the clock function (primarily for deterministic testing).
func (cb *CircuitBreaker) WithClock(now func() time.Time) *CircuitBreaker {
	if now != nil {
		cb.mu.Lock()
		cb.now = now
		cb.mu.Unlock()
	}
	return cb
}

// State returns the current breaker state (accounting for open-timeout expiration).
func (cb *CircuitBreaker) State() BreakerState {
	cb.mu.Lock()
	defer cb.mu.Unlock()
	if cb.state == StateOpen && cb.now().Sub(cb.openedAt) >= cb.openTimeout {
		return StateHalfOpen
	}
	return cb.state
}

// Allow checks whether an upstream call is permitted.
// Returns ErrCircuitOpen if the circuit is currently open or a half-open probe is already in flight.
func (cb *CircuitBreaker) Allow() error {
	cb.mu.Lock()
	defer cb.mu.Unlock()

	switch cb.state {
	case StateClosed:
		return nil
	case StateOpen:
		if cb.now().Sub(cb.openedAt) >= cb.openTimeout {
			cb.state = StateHalfOpen
			cb.probeInFlight = true
			return nil
		}
		return ErrCircuitOpen
	case StateHalfOpen:
		if cb.probeInFlight {
			return ErrCircuitOpen
		}
		cb.probeInFlight = true
		return nil
	default:
		return nil
	}
}

// CancelProbe releases an in-flight half-open probe slot without recording a failure
// (for example, when the downstream client cancels the request before upstream responds).
func (cb *CircuitBreaker) CancelProbe() {
	cb.mu.Lock()
	defer cb.mu.Unlock()

	if cb.state == StateHalfOpen {
		cb.probeInFlight = false
	}
}

// RecordSuccess records a successful upstream call, closing the circuit and resetting failure count.
func (cb *CircuitBreaker) RecordSuccess() {
	cb.mu.Lock()
	defer cb.mu.Unlock()

	cb.consecutiveFailures = 0
	cb.probeInFlight = false
	cb.state = StateClosed
}

// RecordFailure records an upstream 5xx or transport failure.
// Opens the circuit once consecutive failures reach failureThreshold, or immediately if in HalfOpen.
func (cb *CircuitBreaker) RecordFailure() {
	cb.mu.Lock()
	defer cb.mu.Unlock()

	switch cb.state {
	case StateHalfOpen:
		cb.state = StateOpen
		cb.openedAt = cb.now()
		cb.probeInFlight = false
	case StateClosed:
		cb.consecutiveFailures++
		if cb.consecutiveFailures >= cb.failureThreshold {
			cb.state = StateOpen
			cb.openedAt = cb.now()
			cb.probeInFlight = false
		}
	case StateOpen:
		cb.openedAt = cb.now()
		cb.probeInFlight = false
	}
}
