package connectplugin

import (
	"context"
	"math"
	"math/rand"
	"time"

	"connectrpc.com/connect"
)

// RetryPolicy configures retry behavior for RPC calls.
type RetryPolicy struct {
	// MaxAttempts is the maximum number of attempts (1 = no retries).
	// Default: 3
	MaxAttempts int

	// InitialBackoff is the delay before the first retry.
	// Default: 100ms
	InitialBackoff time.Duration

	// MaxBackoff is the maximum backoff delay.
	// Default: 10s
	MaxBackoff time.Duration

	// BackoffMultiplier is the multiplier for exponential backoff.
	// Default: 2.0
	BackoffMultiplier float64

	// Jitter adds randomness to backoff to avoid thundering herd.
	// If true, actual backoff = calculated * (0.5 + rand(0.5))
	// Default: true
	Jitter bool

	// IsRetryable determines if an error should trigger a retry.
	// If nil, uses defaultIsRetryable (network errors, unavailable, resource exhausted).
	IsRetryable func(error) bool
}

// DefaultRetryPolicy returns a retry policy with sensible defaults.
func DefaultRetryPolicy() RetryPolicy {
	return RetryPolicy{
		MaxAttempts:       3,
		InitialBackoff:    100 * time.Millisecond,
		MaxBackoff:        10 * time.Second,
		BackoffMultiplier: 2.0,
		Jitter:            true,
		IsRetryable:       defaultIsRetryable,
	}
}

// defaultIsRetryable determines if an error is retryable.
// Retries: unavailable, resource exhausted, internal errors
// Does not retry: invalid argument, not found, permission denied, cancelled
func defaultIsRetryable(err error) bool {
	if err == nil {
		return false
	}

	// Check Connect error codes
	code := connect.CodeOf(err)
	switch code {
	case connect.CodeUnavailable,
		connect.CodeResourceExhausted,
		connect.CodeInternal,
		connect.CodeUnknown,
		connect.CodeDeadlineExceeded:
		return true
	case connect.CodeCanceled,
		connect.CodeInvalidArgument,
		connect.CodeNotFound,
		connect.CodeAlreadyExists,
		connect.CodePermissionDenied,
		connect.CodeUnauthenticated,
		connect.CodeFailedPrecondition,
		connect.CodeAborted,
		connect.CodeOutOfRange,
		connect.CodeUnimplemented,
		connect.CodeDataLoss:
		return false
	default:
		return false
	}
}

// RetryInterceptor returns a Connect unary interceptor that retries failed calls.
func RetryInterceptor(policy RetryPolicy) connect.UnaryInterceptorFunc {
	// Set defaults
	if policy.MaxAttempts <= 0 {
		policy.MaxAttempts = 3
	}
	if policy.InitialBackoff == 0 {
		policy.InitialBackoff = 100 * time.Millisecond
	}
	if policy.MaxBackoff == 0 {
		policy.MaxBackoff = 10 * time.Second
	}
	if policy.BackoffMultiplier == 0 {
		policy.BackoffMultiplier = 2.0
	}
	if policy.IsRetryable == nil {
		policy.IsRetryable = defaultIsRetryable
	}

	return func(next connect.UnaryFunc) connect.UnaryFunc {
		return func(ctx context.Context, req connect.AnyRequest) (connect.AnyResponse, error) {
			var lastErr error

			for attempt := 1; attempt <= policy.MaxAttempts; attempt++ {
				// Check context before attempting
				if ctx.Err() != nil {
					if lastErr != nil {
						return nil, lastErr
					}
					return nil, ctx.Err()
				}

				// Attempt the call
				resp, err := next(ctx, req)
				if err == nil {
					return resp, nil
				}

				lastErr = err

				// Don't retry if not retryable
				if !policy.IsRetryable(err) {
					return nil, err
				}

				// Don't retry on last attempt
				if attempt == policy.MaxAttempts {
					return nil, err
				}

				// Calculate backoff
				backoff := policy.calculateBackoff(attempt)

				// Wait before retry (unless context cancelled)
				select {
				case <-ctx.Done():
					return nil, lastErr
				case <-time.After(backoff):
					// Continue to next attempt
				}
			}

			return nil, lastErr
		}
	}
}

// calculateBackoff computes the backoff delay for the given attempt.
func (p *RetryPolicy) calculateBackoff(attempt int) time.Duration {
	// Exponential: initial * multiplier^(attempt-1)
	backoff := float64(p.InitialBackoff) * math.Pow(p.BackoffMultiplier, float64(attempt-1))

	// Cap at max
	if backoff > float64(p.MaxBackoff) {
		backoff = float64(p.MaxBackoff)
	}

	// Add jitter
	if p.Jitter {
		jitter := 0.5 + rand.Float64()*0.5 // Random between 0.5 and 1.0
		backoff = backoff * jitter
	}

	return time.Duration(backoff)
}
