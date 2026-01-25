package connectplugin

import (
	"context"
	"errors"
	"testing"
	"time"

	"connectrpc.com/connect"
)

func TestDefaultRetryPolicy(t *testing.T) {
	policy := DefaultRetryPolicy()

	if policy.MaxAttempts != 3 {
		t.Errorf("Expected MaxAttempts 3, got %d", policy.MaxAttempts)
	}

	if policy.InitialBackoff != 100*time.Millisecond {
		t.Errorf("Expected InitialBackoff 100ms, got %v", policy.InitialBackoff)
	}

	if policy.MaxBackoff != 10*time.Second {
		t.Errorf("Expected MaxBackoff 10s, got %v", policy.MaxBackoff)
	}

	if policy.BackoffMultiplier != 2.0 {
		t.Errorf("Expected BackoffMultiplier 2.0, got %f", policy.BackoffMultiplier)
	}

	if !policy.Jitter {
		t.Error("Expected Jitter to be true")
	}

	if policy.IsRetryable == nil {
		t.Error("Expected IsRetryable to be set")
	}
}

func TestDefaultIsRetryable(t *testing.T) {
	tests := []struct {
		name       string
		err        error
		retryable  bool
	}{
		{
			name:      "nil error",
			err:       nil,
			retryable: false,
		},
		{
			name:      "unavailable (retryable)",
			err:       connect.NewError(connect.CodeUnavailable, errors.New("service down")),
			retryable: true,
		},
		{
			name:      "resource exhausted (retryable)",
			err:       connect.NewError(connect.CodeResourceExhausted, errors.New("rate limited")),
			retryable: true,
		},
		{
			name:      "internal error (retryable)",
			err:       connect.NewError(connect.CodeInternal, errors.New("oops")),
			retryable: true,
		},
		{
			name:      "invalid argument (not retryable)",
			err:       connect.NewError(connect.CodeInvalidArgument, errors.New("bad request")),
			retryable: false,
		},
		{
			name:      "not found (not retryable)",
			err:       connect.NewError(connect.CodeNotFound, errors.New("missing")),
			retryable: false,
		},
		{
			name:      "permission denied (not retryable)",
			err:       connect.NewError(connect.CodePermissionDenied, errors.New("forbidden")),
			retryable: false,
		},
		{
			name:      "cancelled (not retryable)",
			err:       connect.NewError(connect.CodeCanceled, errors.New("cancelled")),
			retryable: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := defaultIsRetryable(tt.err)
			if result != tt.retryable {
				t.Errorf("defaultIsRetryable(%v) = %v, want %v", tt.err, result, tt.retryable)
			}
		})
	}
}

func TestRetryPolicy_CalculateBackoff(t *testing.T) {
	policy := RetryPolicy{
		InitialBackoff:    100 * time.Millisecond,
		MaxBackoff:        1 * time.Second,
		BackoffMultiplier: 2.0,
		Jitter:            false, // Disable jitter for predictable testing
	}

	tests := []struct {
		attempt  int
		expected time.Duration
	}{
		{attempt: 1, expected: 100 * time.Millisecond},  // 100 * 2^0
		{attempt: 2, expected: 200 * time.Millisecond},  // 100 * 2^1
		{attempt: 3, expected: 400 * time.Millisecond},  // 100 * 2^2
		{attempt: 4, expected: 800 * time.Millisecond},  // 100 * 2^3
		{attempt: 5, expected: 1000 * time.Millisecond}, // Capped at MaxBackoff
		{attempt: 6, expected: 1000 * time.Millisecond}, // Still capped
	}

	for _, tt := range tests {
		t.Run("", func(t *testing.T) {
			backoff := policy.calculateBackoff(tt.attempt)
			if backoff != tt.expected {
				t.Errorf("Attempt %d: expected backoff %v, got %v", tt.attempt, tt.expected, backoff)
			}
		})
	}
}

func TestRetryPolicy_CalculateBackoff_WithJitter(t *testing.T) {
	policy := RetryPolicy{
		InitialBackoff:    100 * time.Millisecond,
		MaxBackoff:        10 * time.Second,
		BackoffMultiplier: 2.0,
		Jitter:            true,
	}

	backoff := policy.calculateBackoff(1)

	// With jitter, backoff should be between 50ms and 100ms (0.5x to 1.0x)
	if backoff < 50*time.Millisecond || backoff > 100*time.Millisecond {
		t.Errorf("Expected backoff with jitter between 50ms-100ms, got %v", backoff)
	}
}

func TestRetryInterceptor_SuccessFirstAttempt(t *testing.T) {
	policy := RetryPolicy{
		MaxAttempts:    3,
		InitialBackoff: 10 * time.Millisecond,
	}

	attempts := 0
	interceptor := RetryInterceptor(policy)

	// Wrap a function that succeeds on first call
	wrapped := interceptor(func(ctx context.Context, req connect.AnyRequest) (connect.AnyResponse, error) {
		attempts++
		return &connect.Response[string]{}, nil
	})

	_, err := wrapped(context.Background(), &connect.Request[string]{})
	if err != nil {
		t.Fatalf("Expected success, got error: %v", err)
	}

	if attempts != 1 {
		t.Errorf("Expected 1 attempt, got %d", attempts)
	}
}

func TestRetryInterceptor_RetryOnTransientError(t *testing.T) {
	policy := RetryPolicy{
		MaxAttempts:    3,
		InitialBackoff: 10 * time.Millisecond,
		MaxBackoff:     50 * time.Millisecond,
		Jitter:         false,
	}

	attempts := 0
	interceptor := RetryInterceptor(policy)

	// Fail twice, succeed on third
	wrapped := interceptor(func(ctx context.Context, req connect.AnyRequest) (connect.AnyResponse, error) {
		attempts++
		if attempts < 3 {
			return nil, connect.NewError(connect.CodeUnavailable, errors.New("service down"))
		}
		return &connect.Response[string]{}, nil
	})

	start := time.Now()
	_, err := wrapped(context.Background(), &connect.Request[string]{})
	duration := time.Since(start)

	if err != nil {
		t.Fatalf("Expected success after retries, got error: %v", err)
	}

	if attempts != 3 {
		t.Errorf("Expected 3 attempts, got %d", attempts)
	}

	// Should have waited for backoffs (10ms + 20ms = 30ms minimum)
	if duration < 30*time.Millisecond {
		t.Errorf("Expected at least 30ms duration for backoffs, got %v", duration)
	}
}

func TestRetryInterceptor_NoRetryOnPermanentError(t *testing.T) {
	policy := RetryPolicy{
		MaxAttempts:    3,
		InitialBackoff: 10 * time.Millisecond,
	}

	attempts := 0
	interceptor := RetryInterceptor(policy)

	// Always fail with non-retryable error
	wrapped := interceptor(func(ctx context.Context, req connect.AnyRequest) (connect.AnyResponse, error) {
		attempts++
		return nil, connect.NewError(connect.CodeInvalidArgument, errors.New("bad request"))
	})

	_, err := wrapped(context.Background(), &connect.Request[string]{})
	if err == nil {
		t.Fatal("Expected error")
	}

	if attempts != 1 {
		t.Errorf("Expected 1 attempt (no retries for permanent error), got %d", attempts)
	}

	if connect.CodeOf(err) != connect.CodeInvalidArgument {
		t.Errorf("Expected InvalidArgument error, got %v", connect.CodeOf(err))
	}
}

func TestRetryInterceptor_MaxAttemptsRespected(t *testing.T) {
	policy := RetryPolicy{
		MaxAttempts:    5,
		InitialBackoff: 1 * time.Millisecond,
		MaxBackoff:     5 * time.Millisecond,
	}

	attempts := 0
	interceptor := RetryInterceptor(policy)

	// Always fail with retryable error
	wrapped := interceptor(func(ctx context.Context, req connect.AnyRequest) (connect.AnyResponse, error) {
		attempts++
		return nil, connect.NewError(connect.CodeUnavailable, errors.New("always fails"))
	})

	_, err := wrapped(context.Background(), &connect.Request[string]{})
	if err == nil {
		t.Fatal("Expected error after max attempts")
	}

	if attempts != 5 {
		t.Errorf("Expected exactly 5 attempts, got %d", attempts)
	}
}

func TestRetryInterceptor_ContextCancellation(t *testing.T) {
	policy := RetryPolicy{
		MaxAttempts:    10, // Many attempts
		InitialBackoff: 50 * time.Millisecond,
		MaxBackoff:     1 * time.Second,
	}

	attempts := 0
	interceptor := RetryInterceptor(policy)

	ctx, cancel := context.WithCancel(context.Background())

	// Cancel after first failure
	wrapped := interceptor(func(ctx context.Context, req connect.AnyRequest) (connect.AnyResponse, error) {
		attempts++
		if attempts == 1 {
			cancel() // Cancel context after first attempt
		}
		return nil, connect.NewError(connect.CodeUnavailable, errors.New("service down"))
	})

	_, err := wrapped(ctx, &connect.Request[string]{})
	if err == nil {
		t.Fatal("Expected error")
	}

	// Should stop immediately after context cancellation
	if attempts > 2 {
		t.Errorf("Expected at most 2 attempts before context cancellation stopped retries, got %d", attempts)
	}
}

func TestRetryInterceptor_ContextDeadline(t *testing.T) {
	policy := RetryPolicy{
		MaxAttempts:    10,
		InitialBackoff: 100 * time.Millisecond,
	}

	attempts := 0
	interceptor := RetryInterceptor(policy)

	// Context with very short deadline
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()

	wrapped := interceptor(func(ctx context.Context, req connect.AnyRequest) (connect.AnyResponse, error) {
		attempts++
		time.Sleep(20 * time.Millisecond) // Simulate slow call
		return nil, connect.NewError(connect.CodeUnavailable, errors.New("slow"))
	})

	start := time.Now()
	_, err := wrapped(ctx, &connect.Request[string]{})
	duration := time.Since(start)

	if err == nil {
		t.Fatal("Expected error")
	}

	// Should stop quickly due to deadline
	if duration > 200*time.Millisecond {
		t.Errorf("Expected to stop quickly due to deadline, took %v", duration)
	}

	// Should have tried at least once, but not all 10 attempts
	if attempts < 1 || attempts >= 10 {
		t.Errorf("Expected 1-9 attempts before deadline, got %d", attempts)
	}
}

func TestRetryInterceptor_CustomIsRetryable(t *testing.T) {
	// Only retry on ResourceExhausted
	policy := RetryPolicy{
		MaxAttempts:    3,
		InitialBackoff: 1 * time.Millisecond,
		IsRetryable: func(err error) bool {
			return connect.CodeOf(err) == connect.CodeResourceExhausted
		},
	}

	attempts := 0
	interceptor := RetryInterceptor(policy)

	// Fail with Unavailable (would normally be retryable, but not with custom func)
	wrapped := interceptor(func(ctx context.Context, req connect.AnyRequest) (connect.AnyResponse, error) {
		attempts++
		return nil, connect.NewError(connect.CodeUnavailable, errors.New("down"))
	})

	_, err := wrapped(context.Background(), &connect.Request[string]{})
	if err == nil {
		t.Fatal("Expected error")
	}

	// Should NOT retry (custom IsRetryable only retries ResourceExhausted)
	if attempts != 1 {
		t.Errorf("Expected 1 attempt (no retry), got %d", attempts)
	}
}
