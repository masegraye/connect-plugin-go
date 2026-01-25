package connectplugin

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	"connectrpc.com/connect"
)

func TestDefaultCircuitBreakerConfig(t *testing.T) {
	config := DefaultCircuitBreakerConfig()

	if config.FailureThreshold != 5 {
		t.Errorf("Expected FailureThreshold 5, got %d", config.FailureThreshold)
	}

	if config.SuccessThreshold != 2 {
		t.Errorf("Expected SuccessThreshold 2, got %d", config.SuccessThreshold)
	}

	if config.Timeout != 10*time.Second {
		t.Errorf("Expected Timeout 10s, got %v", config.Timeout)
	}

	if config.IsFailure == nil {
		t.Error("Expected IsFailure to be set")
	}
}

func TestCircuitBreaker_OpensAfterFailures(t *testing.T) {
	cb := NewCircuitBreaker(CircuitBreakerConfig{
		FailureThreshold: 3,
		Timeout:          1 * time.Second,
	})

	if cb.State() != CircuitClosed {
		t.Errorf("Expected initial state Closed, got %v", cb.State())
	}

	unavailableErr := connect.NewError(connect.CodeUnavailable, errors.New("service down"))

	// First 2 failures - should stay closed
	for i := 0; i < 2; i++ {
		cb.Call(context.Background(), func() error { return unavailableErr })
		if cb.State() != CircuitClosed {
			t.Errorf("After %d failures, expected Closed, got %v", i+1, cb.State())
		}
	}

	// 3rd failure - should open
	cb.Call(context.Background(), func() error { return unavailableErr })
	if cb.State() != CircuitOpen {
		t.Errorf("After 3 failures, expected Open, got %v", cb.State())
	}
}

func TestCircuitBreaker_StaysOpenForTimeout(t *testing.T) {
	cb := NewCircuitBreaker(CircuitBreakerConfig{
		FailureThreshold: 2,
		SuccessThreshold: 1, // Close after 1 success in half-open
		Timeout:          200 * time.Millisecond,
	})

	unavailableErr := connect.NewError(connect.CodeUnavailable, errors.New("down"))

	// Open the circuit
	cb.Call(context.Background(), func() error { return unavailableErr })
	cb.Call(context.Background(), func() error { return unavailableErr })

	if cb.State() != CircuitOpen {
		t.Fatal("Circuit should be open")
	}

	// Try to call immediately - should be rejected
	err := cb.Call(context.Background(), func() error {
		t.Error("Should not execute function when circuit is open")
		return nil
	})

	if err == nil {
		t.Error("Expected error when circuit is open")
	}

	if connect.CodeOf(err) != connect.CodeUnavailable {
		t.Errorf("Expected Unavailable error, got %v", connect.CodeOf(err))
	}

	// Wait for timeout
	time.Sleep(250 * time.Millisecond)

	// Circuit should transition to half-open and allow the call
	called := false
	cb.Call(context.Background(), func() error {
		called = true
		return nil
	})

	if !called {
		t.Error("Function should be called after timeout (half-open state)")
	}

	if cb.State() != CircuitClosed {
		t.Errorf("Expected Closed after successful call in half-open, got %v", cb.State())
	}
}

func TestCircuitBreaker_HalfOpenAllowsProbe(t *testing.T) {
	cb := NewCircuitBreaker(CircuitBreakerConfig{
		FailureThreshold: 2,
		SuccessThreshold: 2,
		Timeout:          100 * time.Millisecond,
	})

	unavailableErr := connect.NewError(connect.CodeUnavailable, errors.New("down"))

	// Open the circuit
	cb.Call(context.Background(), func() error { return unavailableErr })
	cb.Call(context.Background(), func() error { return unavailableErr })

	if cb.State() != CircuitOpen {
		t.Fatal("Circuit should be open")
	}

	// Wait for timeout → half-open
	time.Sleep(150 * time.Millisecond)

	// First probe - should be allowed
	attempts := 0
	cb.Call(context.Background(), func() error {
		attempts++
		return nil // Success
	})

	if attempts != 1 {
		t.Errorf("Expected 1 attempt in half-open, got %d", attempts)
	}

	if cb.State() != CircuitHalfOpen {
		t.Errorf("Expected HalfOpen after 1 success (need 2), got %v", cb.State())
	}
}

func TestCircuitBreaker_ClosesAfterSuccesses(t *testing.T) {
	cb := NewCircuitBreaker(CircuitBreakerConfig{
		FailureThreshold: 2,
		SuccessThreshold: 3,
		Timeout:          100 * time.Millisecond,
	})

	unavailableErr := connect.NewError(connect.CodeUnavailable, errors.New("down"))

	// Open the circuit
	cb.Call(context.Background(), func() error { return unavailableErr })
	cb.Call(context.Background(), func() error { return unavailableErr })

	// Wait for timeout → half-open
	time.Sleep(150 * time.Millisecond)

	// 2 successes - should stay half-open (need 3)
	cb.Call(context.Background(), func() error { return nil })
	cb.Call(context.Background(), func() error { return nil })

	if cb.State() != CircuitHalfOpen {
		t.Errorf("Expected HalfOpen after 2 successes (need 3), got %v", cb.State())
	}

	// 3rd success - should close
	cb.Call(context.Background(), func() error { return nil })

	if cb.State() != CircuitClosed {
		t.Errorf("Expected Closed after 3 successes, got %v", cb.State())
	}
}

func TestCircuitBreaker_ReopensOnHalfOpenFailure(t *testing.T) {
	cb := NewCircuitBreaker(CircuitBreakerConfig{
		FailureThreshold: 2,
		SuccessThreshold: 2,
		Timeout:          100 * time.Millisecond,
	})

	unavailableErr := connect.NewError(connect.CodeUnavailable, errors.New("down"))

	// Open the circuit
	cb.Call(context.Background(), func() error { return unavailableErr })
	cb.Call(context.Background(), func() error { return unavailableErr })

	// Wait for timeout → half-open
	time.Sleep(150 * time.Millisecond)

	// First probe succeeds
	cb.Call(context.Background(), func() error { return nil })

	if cb.State() != CircuitHalfOpen {
		t.Fatalf("Expected HalfOpen, got %v", cb.State())
	}

	// Second probe fails → reopen
	cb.Call(context.Background(), func() error { return unavailableErr })

	if cb.State() != CircuitOpen {
		t.Errorf("Expected Open after failure in half-open, got %v", cb.State())
	}
}

func TestCircuitBreaker_OnStateChange(t *testing.T) {
	var stateChanges []string

	cb := NewCircuitBreaker(CircuitBreakerConfig{
		FailureThreshold: 2,
		SuccessThreshold: 1,
		Timeout:          100 * time.Millisecond,
		OnStateChange: func(from, to CircuitState) {
			stateChanges = append(stateChanges, fmt.Sprintf("%s→%s", from, to))
		},
	})

	unavailableErr := connect.NewError(connect.CodeUnavailable, errors.New("down"))

	// Open the circuit
	cb.Call(context.Background(), func() error { return unavailableErr })
	cb.Call(context.Background(), func() error { return unavailableErr })

	// Wait for half-open
	time.Sleep(150 * time.Millisecond)

	// Probe succeeds → close
	cb.Call(context.Background(), func() error { return nil })

	expectedChanges := []string{
		"Closed→Open",
		"Open→HalfOpen",
		"HalfOpen→Closed",
	}

	if len(stateChanges) != len(expectedChanges) {
		t.Fatalf("Expected %d state changes, got %d: %v",
			len(expectedChanges), len(stateChanges), stateChanges)
	}

	for i, expected := range expectedChanges {
		if stateChanges[i] != expected {
			t.Errorf("State change %d: expected %s, got %s", i, expected, stateChanges[i])
		}
	}
}

func TestCircuitBreaker_NonFailureErrorsDoNotTrigger(t *testing.T) {
	cb := NewCircuitBreaker(CircuitBreakerConfig{
		FailureThreshold: 2,
	})

	invalidArgErr := connect.NewError(connect.CodeInvalidArgument, errors.New("bad request"))

	// Send many non-failure errors
	for i := 0; i < 10; i++ {
		cb.Call(context.Background(), func() error { return invalidArgErr })
	}

	// Circuit should remain closed (InvalidArgument is not a circuit breaker failure)
	if cb.State() != CircuitClosed {
		t.Errorf("Expected circuit to stay Closed for non-failure errors, got %v", cb.State())
	}
}

func TestCircuitBreakerInterceptor_Integration(t *testing.T) {
	cb := NewCircuitBreaker(CircuitBreakerConfig{
		FailureThreshold: 2,
		Timeout:          100 * time.Millisecond,
	})

	interceptor := CircuitBreakerInterceptor(cb)

	attempts := 0
	unavailableErr := connect.NewError(connect.CodeUnavailable, errors.New("down"))

	// Mock unary function that always fails
	mockFunc := func(ctx context.Context, req connect.AnyRequest) (connect.AnyResponse, error) {
		attempts++
		return nil, unavailableErr
	}

	wrapped := interceptor(mockFunc)

	// First 2 calls - circuit is closed, calls go through
	wrapped(context.Background(), &connect.Request[string]{})
	wrapped(context.Background(), &connect.Request[string]{})

	if attempts != 2 {
		t.Errorf("Expected 2 attempts before circuit opens, got %d", attempts)
	}

	if cb.State() != CircuitOpen {
		t.Fatalf("Expected circuit to be open, got %v", cb.State())
	}

	// Next call - circuit is open, should be rejected immediately
	_, err := wrapped(context.Background(), &connect.Request[string]{})
	if err == nil {
		t.Fatal("Expected error when circuit is open")
	}

	if attempts != 2 {
		t.Errorf("Expected no additional attempts (circuit open), got %d total", attempts)
	}

	if connect.CodeOf(err) != connect.CodeUnavailable {
		t.Errorf("Expected Unavailable error from circuit breaker, got %v", connect.CodeOf(err))
	}
}

func TestCircuitBreakerInterceptor_RecoversAfterTimeout(t *testing.T) {
	cb := NewCircuitBreaker(CircuitBreakerConfig{
		FailureThreshold: 2,
		SuccessThreshold: 1,
		Timeout:          150 * time.Millisecond,
	})

	interceptor := CircuitBreakerInterceptor(cb)

	failureCount := 0
	unavailableErr := connect.NewError(connect.CodeUnavailable, errors.New("down"))

	mockFunc := func(ctx context.Context, req connect.AnyRequest) (connect.AnyResponse, error) {
		failureCount++
		if failureCount <= 2 {
			return nil, unavailableErr
		}
		// Service recovered
		return &connect.Response[string]{}, nil
	}

	wrapped := interceptor(mockFunc)

	// Open the circuit (2 failures)
	wrapped(context.Background(), &connect.Request[string]{})
	wrapped(context.Background(), &connect.Request[string]{})

	if cb.State() != CircuitOpen {
		t.Fatal("Circuit should be open")
	}

	// Wait for timeout
	time.Sleep(200 * time.Millisecond)

	// Next call should succeed (half-open allows probe, service recovered)
	_, err := wrapped(context.Background(), &connect.Request[string]{})
	if err != nil {
		t.Fatalf("Expected success after recovery, got error: %v", err)
	}

	// Circuit should be closed (1 success in half-open with threshold=1)
	if cb.State() != CircuitClosed {
		t.Errorf("Expected Closed after successful recovery, got %v", cb.State())
	}
}

func TestCircuitState_String(t *testing.T) {
	tests := []struct {
		state    CircuitState
		expected string
	}{
		{CircuitClosed, "Closed"},
		{CircuitOpen, "Open"},
		{CircuitHalfOpen, "HalfOpen"},
		{CircuitState(99), "Unknown(99)"},
	}

	for _, tt := range tests {
		t.Run(tt.expected, func(t *testing.T) {
			if tt.state.String() != tt.expected {
				t.Errorf("Expected %s, got %s", tt.expected, tt.state.String())
			}
		})
	}
}

func TestCircuitBreaker_SuccessResetsFailureCount(t *testing.T) {
	cb := NewCircuitBreaker(CircuitBreakerConfig{
		FailureThreshold: 3,
	})

	unavailableErr := connect.NewError(connect.CodeUnavailable, errors.New("down"))

	// 2 failures
	cb.Call(context.Background(), func() error { return unavailableErr })
	cb.Call(context.Background(), func() error { return unavailableErr })

	// 1 success - should reset failure count
	cb.Call(context.Background(), func() error { return nil })

	// Circuit should still be closed
	if cb.State() != CircuitClosed {
		t.Errorf("Expected Closed after success reset, got %v", cb.State())
	}

	// 2 more failures - should NOT open (count was reset)
	cb.Call(context.Background(), func() error { return unavailableErr })
	cb.Call(context.Background(), func() error { return unavailableErr })

	if cb.State() != CircuitClosed {
		t.Errorf("Expected still Closed (failures reset by success), got %v", cb.State())
	}

	// One more failure (3rd consecutive) - should open
	cb.Call(context.Background(), func() error { return unavailableErr })

	if cb.State() != CircuitOpen {
		t.Errorf("Expected Open after 3 consecutive failures, got %v", cb.State())
	}
}

func TestDefaultIsFailure(t *testing.T) {
	tests := []struct {
		name      string
		err       error
		isFailure bool
	}{
		{
			name:      "nil",
			err:       nil,
			isFailure: false,
		},
		{
			name:      "unavailable",
			err:       connect.NewError(connect.CodeUnavailable, errors.New("down")),
			isFailure: true,
		},
		{
			name:      "resource exhausted",
			err:       connect.NewError(connect.CodeResourceExhausted, errors.New("throttled")),
			isFailure: true,
		},
		{
			name:      "internal",
			err:       connect.NewError(connect.CodeInternal, errors.New("oops")),
			isFailure: true,
		},
		{
			name:      "deadline exceeded",
			err:       connect.NewError(connect.CodeDeadlineExceeded, errors.New("timeout")),
			isFailure: true,
		},
		{
			name:      "invalid argument",
			err:       connect.NewError(connect.CodeInvalidArgument, errors.New("bad")),
			isFailure: false,
		},
		{
			name:      "not found",
			err:       connect.NewError(connect.CodeNotFound, errors.New("missing")),
			isFailure: false,
		},
		{
			name:      "permission denied",
			err:       connect.NewError(connect.CodePermissionDenied, errors.New("forbidden")),
			isFailure: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := defaultIsFailure(tt.err)
			if result != tt.isFailure {
				t.Errorf("defaultIsFailure() = %v, want %v", result, tt.isFailure)
			}
		})
	}
}

func TestCircuitBreaker_CustomIsFailure(t *testing.T) {
	// Only treat NotFound as failure (unusual but demonstrates custom logic)
	cb := NewCircuitBreaker(CircuitBreakerConfig{
		FailureThreshold: 2,
		IsFailure: func(err error) bool {
			return connect.CodeOf(err) == connect.CodeNotFound
		},
	})

	unavailableErr := connect.NewError(connect.CodeUnavailable, errors.New("down"))
	notFoundErr := connect.NewError(connect.CodeNotFound, errors.New("missing"))

	// Unavailable errors don't count as failures
	cb.Call(context.Background(), func() error { return unavailableErr })
	cb.Call(context.Background(), func() error { return unavailableErr })

	if cb.State() != CircuitClosed {
		t.Error("Circuit should stay closed for Unavailable with custom IsFailure")
	}

	// NotFound errors count as failures
	cb.Call(context.Background(), func() error { return notFoundErr })
	cb.Call(context.Background(), func() error { return notFoundErr })

	if cb.State() != CircuitOpen {
		t.Errorf("Circuit should open for NotFound with custom IsFailure, got %v", cb.State())
	}
}
