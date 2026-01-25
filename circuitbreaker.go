package connectplugin

import (
	"context"
	"fmt"
	"sync"
	"time"

	"connectrpc.com/connect"
)

// CircuitState represents the current state of a circuit breaker.
type CircuitState int

const (
	// CircuitClosed: normal operation, requests pass through.
	// Tracks failures; transitions to Open after consecutive failures exceed threshold.
	CircuitClosed CircuitState = iota

	// CircuitOpen: circuit is open, requests fail immediately without calling service.
	// Transitions to HalfOpen after timeout period.
	CircuitOpen

	// CircuitHalfOpen: testing if service has recovered.
	// Allows limited probe requests; closes on success threshold, reopens on failure.
	CircuitHalfOpen
)

func (s CircuitState) String() string {
	switch s {
	case CircuitClosed:
		return "Closed"
	case CircuitOpen:
		return "Open"
	case CircuitHalfOpen:
		return "HalfOpen"
	default:
		return fmt.Sprintf("Unknown(%d)", s)
	}
}

// CircuitBreakerConfig configures circuit breaker behavior.
type CircuitBreakerConfig struct {
	// FailureThreshold is the number of consecutive failures before opening circuit.
	// Default: 5
	FailureThreshold int

	// SuccessThreshold is the number of consecutive successes in half-open to close circuit.
	// Default: 2
	SuccessThreshold int

	// Timeout is how long the circuit stays open before transitioning to half-open.
	// Default: 10s
	Timeout time.Duration

	// IsFailure determines if an error should count as a failure.
	// If nil, uses defaultIsFailure (same as retry logic).
	IsFailure func(error) bool

	// OnStateChange is called when circuit state changes (optional, for monitoring).
	OnStateChange func(from, to CircuitState)
}

// DefaultCircuitBreakerConfig returns a circuit breaker config with sensible defaults.
func DefaultCircuitBreakerConfig() CircuitBreakerConfig {
	return CircuitBreakerConfig{
		FailureThreshold: 5,
		SuccessThreshold: 2,
		Timeout:          10 * time.Second,
		IsFailure:        defaultIsFailure,
	}
}

// defaultIsFailure determines if an error should count as a circuit breaker failure.
// Uses same logic as retry (unavailable, resource exhausted, internal, etc.)
func defaultIsFailure(err error) bool {
	if err == nil {
		return false
	}

	code := connect.CodeOf(err)
	switch code {
	case connect.CodeUnavailable,
		connect.CodeResourceExhausted,
		connect.CodeInternal,
		connect.CodeUnknown,
		connect.CodeDeadlineExceeded:
		return true
	default:
		return false
	}
}

// CircuitBreaker implements the circuit breaker pattern for RPC calls.
type CircuitBreaker struct {
	mu     sync.RWMutex
	config CircuitBreakerConfig

	state              CircuitState
	consecutiveFailures int
	consecutiveSuccesses int
	lastFailureTime    time.Time
}

// NewCircuitBreaker creates a new circuit breaker with the given config.
func NewCircuitBreaker(config CircuitBreakerConfig) *CircuitBreaker {
	// Set defaults
	if config.FailureThreshold <= 0 {
		config.FailureThreshold = 5
	}
	if config.SuccessThreshold <= 0 {
		config.SuccessThreshold = 2
	}
	if config.Timeout == 0 {
		config.Timeout = 10 * time.Second
	}
	if config.IsFailure == nil {
		config.IsFailure = defaultIsFailure
	}

	return &CircuitBreaker{
		config: config,
		state:  CircuitClosed,
	}
}

// State returns the current circuit state.
func (cb *CircuitBreaker) State() CircuitState {
	cb.mu.RLock()
	defer cb.mu.RUnlock()
	return cb.state
}

// Call executes the given function with circuit breaker protection.
func (cb *CircuitBreaker) Call(ctx context.Context, fn func() error) error {
	// Check if we can proceed
	if !cb.allowRequest() {
		return connect.NewError(connect.CodeUnavailable,
			fmt.Errorf("circuit breaker is open"))
	}

	// Execute the call
	err := fn()

	// Record result
	cb.recordResult(err)

	return err
}

// allowRequest checks if a request should be allowed based on circuit state.
func (cb *CircuitBreaker) allowRequest() bool {
	cb.mu.Lock()
	defer cb.mu.Unlock()

	switch cb.state {
	case CircuitClosed:
		return true

	case CircuitOpen:
		// Check if timeout has elapsed → transition to half-open
		if time.Since(cb.lastFailureTime) >= cb.config.Timeout {
			cb.setState(CircuitHalfOpen)
			cb.consecutiveSuccesses = 0
			cb.consecutiveFailures = 0
			return true
		}
		return false

	case CircuitHalfOpen:
		// Allow limited probes in half-open state
		return true

	default:
		return false
	}
}

// recordResult updates circuit state based on call result.
func (cb *CircuitBreaker) recordResult(err error) {
	cb.mu.Lock()
	defer cb.mu.Unlock()

	isFailure := cb.config.IsFailure(err)

	switch cb.state {
	case CircuitClosed:
		if isFailure {
			cb.consecutiveFailures++
			cb.consecutiveSuccesses = 0
			cb.lastFailureTime = time.Now()

			// Open circuit if threshold exceeded
			if cb.consecutiveFailures >= cb.config.FailureThreshold {
				cb.setState(CircuitOpen)
			}
		} else {
			// Success - reset failure count
			cb.consecutiveFailures = 0
			cb.consecutiveSuccesses++
		}

	case CircuitOpen:
		// Circuit is open, shouldn't reach here (allowRequest returns false)
		// But if we do (race condition), update last failure time
		if isFailure {
			cb.lastFailureTime = time.Now()
		}

	case CircuitHalfOpen:
		if isFailure {
			// Failure in half-open → reopen circuit
			cb.consecutiveFailures = 1
			cb.consecutiveSuccesses = 0
			cb.lastFailureTime = time.Now()
			cb.setState(CircuitOpen)
		} else {
			// Success in half-open
			cb.consecutiveSuccesses++
			cb.consecutiveFailures = 0

			// Close circuit if success threshold reached
			if cb.consecutiveSuccesses >= cb.config.SuccessThreshold {
				cb.setState(CircuitClosed)
				cb.consecutiveSuccesses = 0
			}
		}
	}
}

// setState changes the circuit state and calls OnStateChange callback.
// Caller must hold lock.
func (cb *CircuitBreaker) setState(newState CircuitState) {
	oldState := cb.state
	cb.state = newState

	if cb.config.OnStateChange != nil {
		// Call callback without holding lock to avoid deadlock
		cb.mu.Unlock()
		cb.config.OnStateChange(oldState, newState)
		cb.mu.Lock()
	}
}

// CircuitBreakerInterceptor returns a Connect unary interceptor with circuit breaker protection.
func CircuitBreakerInterceptor(cb *CircuitBreaker) connect.UnaryInterceptorFunc {
	return func(next connect.UnaryFunc) connect.UnaryFunc {
		return func(ctx context.Context, req connect.AnyRequest) (connect.AnyResponse, error) {
			var resp connect.AnyResponse
			var callErr error

			err := cb.Call(ctx, func() error {
				var innerErr error
				resp, innerErr = next(ctx, req)
				callErr = innerErr
				return innerErr
			})

			if err != nil && callErr == nil {
				// Circuit breaker rejected the call
				return nil, err
			}

			return resp, callErr
		}
	}
}
