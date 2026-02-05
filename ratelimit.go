package connectplugin

import (
	"context"
	"fmt"
	"net/http"
	"sync"
	"time"

	"connectrpc.com/connect"
)

// Rate defines a rate limit with requests per second and burst capacity.
type Rate struct {
	RequestsPerSecond float64
	Burst             int
}

// RateLimiter provides rate limiting for requests.
type RateLimiter interface {
	// Allow checks if a request with the given key should be allowed.
	// Returns true if the request is within rate limits.
	Allow(key string, limit Rate) bool

	// Close stops the rate limiter and cleans up resources.
	Close()
}

// TokenBucketLimiter implements rate limiting using the token bucket algorithm.
type TokenBucketLimiter struct {
	mu      sync.RWMutex
	buckets map[string]*bucket
	stopCh  chan struct{}
	stopped bool
}

// bucket represents a token bucket for a single key.
type bucket struct {
	tokens       float64
	capacity     float64
	refillRate   float64
	lastRefill   time.Time
	mu           sync.Mutex
}

// NewTokenBucketLimiter creates a new token bucket rate limiter.
// It starts a background goroutine to clean up old buckets.
func NewTokenBucketLimiter() *TokenBucketLimiter {
	limiter := &TokenBucketLimiter{
		buckets: make(map[string]*bucket),
		stopCh:  make(chan struct{}),
	}

	// Start cleanup goroutine
	go limiter.cleanup()

	return limiter
}

// Allow checks if a request should be allowed under the given rate limit.
func (l *TokenBucketLimiter) Allow(key string, limit Rate) bool {
	// Get or create bucket
	l.mu.RLock()
	b, exists := l.buckets[key]
	l.mu.RUnlock()

	if !exists {
		b = &bucket{
			tokens:     float64(limit.Burst),
			capacity:   float64(limit.Burst),
			refillRate: limit.RequestsPerSecond,
			lastRefill: time.Now(),
		}
		l.mu.Lock()
		l.buckets[key] = b
		l.mu.Unlock()
	}

	return b.take(limit)
}

// Close stops the rate limiter and cleanup goroutine.
func (l *TokenBucketLimiter) Close() {
	l.mu.Lock()
	defer l.mu.Unlock()

	if !l.stopped {
		close(l.stopCh)
		l.stopped = true
	}
}

// cleanup removes old buckets that haven't been used recently.
func (l *TokenBucketLimiter) cleanup() {
	ticker := time.NewTicker(1 * time.Minute)
	defer ticker.Stop()

	for {
		select {
		case <-l.stopCh:
			return
		case <-ticker.C:
			l.removeOldBuckets()
		}
	}
}

// removeOldBuckets removes buckets that haven't been accessed in 5 minutes.
func (l *TokenBucketLimiter) removeOldBuckets() {
	l.mu.Lock()
	defer l.mu.Unlock()

	threshold := time.Now().Add(-5 * time.Minute)
	for key, b := range l.buckets {
		b.mu.Lock()
		if b.lastRefill.Before(threshold) {
			delete(l.buckets, key)
		}
		b.mu.Unlock()
	}
}

// take attempts to take one token from the bucket.
// Returns true if a token was available, false otherwise.
func (b *bucket) take(limit Rate) bool {
	b.mu.Lock()
	defer b.mu.Unlock()

	// Update bucket capacity and refill rate if limit changed
	if b.capacity != float64(limit.Burst) || b.refillRate != limit.RequestsPerSecond {
		b.capacity = float64(limit.Burst)
		b.refillRate = limit.RequestsPerSecond
	}

	// Refill tokens based on elapsed time
	now := time.Now()
	elapsed := now.Sub(b.lastRefill).Seconds()
	b.tokens += elapsed * b.refillRate
	if b.tokens > b.capacity {
		b.tokens = b.capacity
	}
	b.lastRefill = now

	// Check if token available
	if b.tokens >= 1.0 {
		b.tokens -= 1.0
		return true
	}

	return false
}

// RateLimitInterceptor creates a Connect interceptor that applies rate limiting.
// The keyExtractor function determines the rate limit key from the request.
func RateLimitInterceptor(limiter RateLimiter, keyExtractor func(connect.AnyRequest) string, limit Rate) connect.UnaryInterceptorFunc {
	return func(next connect.UnaryFunc) connect.UnaryFunc {
		return func(ctx context.Context, req connect.AnyRequest) (connect.AnyResponse, error) {
			key := keyExtractor(req)
			if !limiter.Allow(key, limit) {
				return nil, connect.NewError(
					connect.CodeResourceExhausted,
					fmt.Errorf("rate limit exceeded for %s", key),
				)
			}
			return next(ctx, req)
		}
	}
}

// RateLimitHTTPHandler wraps an HTTP handler with rate limiting.
// This is used for non-Connect endpoints like /capabilities/* and /services/*.
func RateLimitHTTPHandler(handler http.Handler, limiter RateLimiter, keyExtractor func(*http.Request) string, limit Rate) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		key := keyExtractor(r)
		if !limiter.Allow(key, limit) {
			http.Error(w, "Rate limit exceeded", http.StatusTooManyRequests)
			return
		}
		handler.ServeHTTP(w, r)
	})
}

// DefaultRateLimitKeyExtractor extracts rate limit key from runtime ID header.
// Falls back to remote IP if runtime ID is not present.
func DefaultRateLimitKeyExtractor(req connect.AnyRequest) string {
	// Try to get runtime ID from header
	runtimeID := req.Header().Get("X-Plugin-Runtime-ID")
	if runtimeID != "" {
		return "runtime:" + runtimeID
	}

	// Fallback to peer address for unauthenticated requests
	peer := req.Peer()
	return "ip:" + peer.Addr
}

// HTTPRateLimitKeyExtractor extracts rate limit key from HTTP request.
func HTTPRateLimitKeyExtractor(r *http.Request) string {
	// Try to get runtime ID from header
	runtimeID := r.Header.Get("X-Plugin-Runtime-ID")
	if runtimeID != "" {
		return "runtime:" + runtimeID
	}

	// Fallback to remote IP
	return "ip:" + r.RemoteAddr
}
