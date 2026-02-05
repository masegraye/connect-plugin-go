package connectplugin

import (
	"testing"
	"time"
)

func TestTokenBucketLimiter_Allow(t *testing.T) {
	limiter := NewTokenBucketLimiter()
	defer limiter.Close()

	limit := Rate{
		RequestsPerSecond: 10,
		Burst:             5,
	}

	// First 5 requests should be allowed (burst capacity)
	for i := 0; i < 5; i++ {
		if !limiter.Allow("test-key", limit) {
			t.Errorf("Request %d should be allowed (within burst)", i+1)
		}
	}

	// 6th request should be denied (burst exhausted)
	if limiter.Allow("test-key", limit) {
		t.Error("Request should be denied after burst exhausted")
	}

	// Wait for refill (100ms = 1 token at 10/sec)
	time.Sleep(150 * time.Millisecond)

	// Should allow 1 more request after refill
	if !limiter.Allow("test-key", limit) {
		t.Error("Request should be allowed after refill")
	}
}

func TestTokenBucketLimiter_MultipleKeys(t *testing.T) {
	limiter := NewTokenBucketLimiter()
	defer limiter.Close()

	limit := Rate{
		RequestsPerSecond: 10,
		Burst:             2,
	}

	// Exhaust key1
	limiter.Allow("key1", limit)
	limiter.Allow("key1", limit)
	if limiter.Allow("key1", limit) {
		t.Error("key1 should be rate limited")
	}

	// key2 should still be allowed (separate bucket)
	if !limiter.Allow("key2", limit) {
		t.Error("key2 should be allowed (independent bucket)")
	}
}

func TestTokenBucketLimiter_DynamicLimits(t *testing.T) {
	limiter := NewTokenBucketLimiter()
	defer limiter.Close()

	// Start with low limit
	lowLimit := Rate{
		RequestsPerSecond: 1,
		Burst:             1,
	}

	limiter.Allow("test", lowLimit)
	if limiter.Allow("test", lowLimit) {
		t.Error("Should be rate limited with low limit")
	}

	// Wait for refill
	time.Sleep(200 * time.Millisecond)

	// Change to higher limit - bucket should refill and allow
	highLimit := Rate{
		RequestsPerSecond: 100,
		Burst:             10,
	}

	// Should allow after refill with higher limit
	if !limiter.Allow("test", highLimit) {
		t.Error("Should be allowed with higher limit after refill")
	}
}

func BenchmarkRateLimiter_Allow(b *testing.B) {
	limiter := NewTokenBucketLimiter()
	defer limiter.Close()

	limit := Rate{
		RequestsPerSecond: 1000,
		Burst:             1000,
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		limiter.Allow("benchmark-key", limit)
	}
}

func BenchmarkRateLimiter_MultipleKeys(b *testing.B) {
	limiter := NewTokenBucketLimiter()
	defer limiter.Close()

	limit := Rate{
		RequestsPerSecond: 1000,
		Burst:             1000,
	}

	keys := []string{"key1", "key2", "key3", "key4", "key5"}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		key := keys[i%len(keys)]
		limiter.Allow(key, limit)
	}
}
