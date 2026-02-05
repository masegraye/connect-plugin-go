# Rate Limiting Guide

Protect your plugin platform from DoS attacks and resource exhaustion with built-in rate limiting.

## Overview

connect-plugin-go provides token bucket rate limiting to control request rates from plugins. Rate limits are enforced per runtime ID, preventing any single plugin from overwhelming the platform.

## Quick Start

### Basic Setup

```go
package main

import (
    "time"
    connectplugin "github.com/masegraye/connect-plugin-go"
)

func main() {
    // Create rate limiter
    limiter := connectplugin.NewTokenBucketLimiter()
    defer limiter.Close()  // Important: stops cleanup goroutine

    // Serve with rate limiting enabled
    connectplugin.Serve(&connectplugin.ServeConfig{
        Plugins:     pluginSet,
        Impls:       impls,
        RateLimiter: limiter,  // Enable rate limiting
    })
}
```

That's it! Rate limiting is now active on all public endpoints.

## How It Works

### Token Bucket Algorithm

Each runtime ID gets an independent token bucket:

```
Initial state: Bucket has BURST tokens
┌──────────────┐
│ ●●●●●●●●●●   │  10 tokens (Burst = 10)
└──────────────┘

Request arrives: Consume 1 token
┌──────────────┐
│ ●●●●●●●●●    │  9 tokens remaining
└──────────────┘

Time passes: Tokens refill at rate
┌──────────────┐
│ ●●●●●●●●●●   │  Back to 10 (capped at Burst)
└──────────────┘

Burst exhausted: No tokens available
┌──────────────┐
│              │  0 tokens → Request DENIED
└──────────────┘
```

### Rate Parameters

```go
type Rate struct {
    RequestsPerSecond float64  // Sustained request rate
    Burst             int      // Maximum burst size
}
```

**RequestsPerSecond**: How fast tokens refill
- `10.0` = 10 requests/second sustained
- `0.5` = 1 request every 2 seconds
- `100.0` = 100 requests/second

**Burst**: Maximum tokens in bucket
- Allows temporary spikes above sustained rate
- Bucket accumulates tokens when idle (up to Burst)
- Higher burst = more tolerance for bursty traffic

## Configuration Examples

### Conservative (Low Traffic)

```go
rate := connectplugin.Rate{
    RequestsPerSecond: 10,   // 10 req/sec sustained
    Burst:             5,    // Allow bursts of 5
}
```

**Use case**: Development, low-traffic plugins, strict DoS protection

### Moderate (Production)

```go
rate := connectplugin.Rate{
    RequestsPerSecond: 100,  // 100 req/sec sustained
    Burst:             20,   // Allow bursts of 20
}
```

**Use case**: Production plugins, typical workloads

### Permissive (High Traffic)

```go
rate := connectplugin.Rate{
    RequestsPerSecond: 1000, // 1000 req/sec sustained
    Burst:             100,  // Allow bursts of 100
}
```

**Use case**: High-throughput plugins, data streaming

### One-Time Burst (No Refills)

```go
rate := connectplugin.Rate{
    RequestsPerSecond: 0,    // No refills
    Burst:             50,   // One-time budget of 50
}
```

**Use case**: Rate limit handshakes (plugins shouldn't re-handshake frequently)

## Default Behavior

When `RateLimiter` is set in `ServeConfig`, rate limits are applied to:

- **Handshake**: Prevents handshake flooding
- **Service Registration**: Prevents registry pollution
- **Capability Requests**: Prevents grant exhaustion

**Default Rate** (currently no default, must be configured):
- Not applied by default
- Must explicitly set `RateLimiter` in ServeConfig
- Recommended: Start with moderate settings and tune based on metrics

## Distributed Deployments

### Multi-Replica Limitation

⚠️ **Important:** The token bucket rate limiter maintains state **in-memory within each process**. In distributed deployments with multiple host replicas, rate limits are enforced **per replica**, not globally.

**Example Scenario:**

```
Configuration:
- Rate limit: 100 req/s, burst 10
- Host replicas: 3

Effective behavior:
- Replica 1: Allows up to 100 req/s for runtime-123
- Replica 2: Allows up to 100 req/s for runtime-123
- Replica 3: Allows up to 100 req/s for runtime-123
- Total: ~300 req/s (3× intended limit)
```

**Why this happens:**
- Each replica has independent `TokenBucketLimiter` instance
- No shared state between replicas
- Load balancer distributes requests across replicas
- Plugin can send requests to multiple replicas simultaneously

### Mitigation Strategies

#### Option 1: Adjust for Replica Count (Recommended)

Divide your target rate by the number of replicas:

```go
// Target: 300 req/s total across all replicas
targetRate := 300.0
replicaCount := 3
perReplicaRate := targetRate / float64(replicaCount)  // 100 req/s

limiter := connectplugin.NewTokenBucketLimiter()

rate := connectplugin.Rate{
    RequestsPerSecond: perReplicaRate,  // 100 req/s per replica
    Burst:             int(perReplicaRate) / 5,  // 20 burst
}

connectplugin.Serve(&connectplugin.ServeConfig{
    RateLimiter: limiter,
    // Apply rate to specific endpoints
})
```

**Helper function:**

```go
// AdjustForReplicas calculates per-replica rate for distributed deployment
func AdjustForReplicas(targetRate float64, replicaCount int) float64 {
    if replicaCount <= 0 {
        replicaCount = 1
    }
    return targetRate / float64(replicaCount)
}

// Usage:
perReplicaRate := AdjustForReplicas(300.0, 3)  // 100 req/s
```

**Kubernetes example:**

```yaml
# deployment.yaml
apiVersion: apps/v1
kind: Deployment
metadata:
  name: plugin-host
spec:
  replicas: 3  # ← Replica count
  template:
    spec:
      containers:
      - name: host
        env:
        - name: REPLICA_COUNT
          value: "3"
        - name: TARGET_RATE
          value: "300"  # Total desired rate
```

```go
// Use env vars in configuration
replicaCount, _ := strconv.Atoi(os.Getenv("REPLICA_COUNT"))
targetRate, _ := strconv.ParseFloat(os.Getenv("TARGET_RATE"), 64)
perReplicaRate := targetRate / float64(replicaCount)
```

#### Option 2: External API Gateway

Use an API gateway with distributed rate limiting:

**Kong (Redis-backed):**

```yaml
# kong.yml
plugins:
  - name: rate-limiting
    config:
      minute: 300
      policy: redis
      redis_host: redis.svc.cluster.local
      redis_port: 6379
```

**Envoy (Global rate limit service):**

```yaml
# envoy.yaml
rate_limits:
  - actions:
    - generic_key:
        descriptor_value: plugin_requests
    descriptors:
      - key: runtime_id
        rate_limit:
          requests_per_unit: 300
          unit: minute
```

**nginx:**

```nginx
# nginx.conf
http {
    limit_req_zone $http_x_plugin_runtime_id zone=plugin_limit:10m rate=5r/s;

    server {
        location / {
            limit_req zone=plugin_limit burst=10;
            proxy_pass http://plugin-host:8080;
        }
    }
}
```

**Pros:**
- True distributed rate limiting
- Shared state across all replicas
- Production-grade implementations
- Additional features (metrics, dashboards)

**Cons:**
- External dependency (Redis, rate limit service)
- Additional operational complexity
- Network hop adds latency

#### Option 3: Sticky Sessions

Route requests from same plugin to same replica:

```yaml
# kubernetes service
apiVersion: v1
kind: Service
metadata:
  name: plugin-host
spec:
  sessionAffinity: ClientIP
  sessionAffinityConfig:
    clientIP:
      timeoutSeconds: 3600
```

**Pros:**
- Simple configuration
- No code changes
- Each plugin gets consistent rate limit

**Cons:**
- Uneven load distribution
- Plugin can still bypass by changing source IP
- Not effective if plugins use multiple IPs

### Choosing the Right Approach

| Scenario | Recommended Approach |
|----------|---------------------|
| **Single replica** | No adjustment needed |
| **2-5 replicas, predictable traffic** | Adjust for replica count (Option 1) |
| **5+ replicas, strict limits required** | External API gateway (Option 2) |
| **Auto-scaling replicas** | External API gateway (Option 2) |
| **Development/staging** | No adjustment (over-provisioned is fine) |

### Future Enhancement: Distributed Rate Limiter

Phase 3 will include pluggable rate limiter interface for distributed backends:

```go
// Planned interface
type RateLimiter interface {
    Allow(key string, limit Rate) bool
    Close()
}

// Implementations:
// - TokenBucketLimiter (in-memory, current)
// - RedisRateLimiter (distributed, shared state via Redis)
// - NoOpRateLimiter (testing/development, always allows)
```

**Timeline:** Phase 3 (Q2 2026)
**Tracking:** See Phase 3 roadmap

## Advanced Usage

### Per-Endpoint Rate Limits

Apply different limits to different endpoints:

```go
limiter := connectplugin.NewTokenBucketLimiter()

// Handshake: Strict (infrequent operation)
handshakeRate := connectplugin.Rate{
    RequestsPerSecond: 1,
    Burst:             3,
}

// Registration: Moderate (happens on startup + updates)
registrationRate := connectplugin.Rate{
    RequestsPerSecond: 10,
    Burst:             5,
}

// Capability requests: Permissive (frequent operation)
capabilityRate := connectplugin.Rate{
    RequestsPerSecond: 100,
    Burst:             20,
}

// Note: Current implementation uses same rate for all endpoints
// Per-endpoint rates planned for future release
```

### Custom Rate Limit Interceptor

Use the rate limiter in custom code:

```go
import "connectrpc.com/connect"

limiter := connectplugin.NewTokenBucketLimiter()

rate := connectplugin.Rate{RequestsPerSecond: 50, Burst: 10}

interceptor := connectplugin.RateLimitInterceptor(
    limiter,
    connectplugin.DefaultRateLimitKeyExtractor,  // Extract runtime ID from header
    rate,
)

// Apply to Connect RPC client
client := myv1connect.NewMyServiceClient(
    httpClient,
    baseURL,
    connect.WithInterceptors(interceptor),
)
```

### HTTP Handler Wrapper

Apply rate limiting to HTTP endpoints:

```go
limiter := connectplugin.NewTokenBucketLimiter()
rate := connectplugin.Rate{RequestsPerSecond: 100, Burst: 20}

// Wrap HTTP handler
handler := connectplugin.RateLimitHTTPHandler(
    limiter,
    connectplugin.HTTPRateLimitKeyExtractor,  // Extract from HTTP headers
    rate,
    myHandler,  // Your original handler
)

http.Handle("/my-endpoint", handler)
```

## Monitoring

### Detect Rate Limiting

When a plugin is rate limited, it receives:

```
Code: ResourceExhausted
Message: "rate limit exceeded"
```

Plugin should:
1. Implement exponential backoff
2. Log rate limit errors
3. Alert on sustained rate limiting (indicates need for higher limits)

### Metrics to Track

Monitor these metrics in production:

- **Rate limit rejections per plugin**: High rejections = increase limits or investigate abuse
- **Bucket utilization**: How close plugins are to their limits
- **Refill rate vs request rate**: Sustained at limit indicates under-provisioned
- **Idle bucket cleanup**: Number of buckets removed (indicates churn)

## Production Recommendations

### Setting Appropriate Limits

**Start conservative, increase based on metrics:**

1. **Initial deployment**: Set moderate limits (100 req/sec, burst 20)
2. **Monitor**: Track rate limit rejections for 1 week
3. **Tune**: Increase limits for plugins hitting limits frequently
4. **Alert**: Alert on sustained rate limiting (>1% rejection rate)

### Per-Plugin Limits

Different plugins need different limits:

```go
// Heavy traffic plugin (API gateway)
apiRate := Rate{RequestsPerSecond: 1000, Burst: 100}

// Normal plugin (business logic)
normalRate := Rate{RequestsPerSecond: 100, Burst: 20}

// Light plugin (periodic tasks)
lightRate := Rate{RequestsPerSecond: 10, Burst: 5}

// Note: Current implementation uses same rate for all plugins
// Per-plugin rates require custom interceptor setup
```

### Preventing False Positives

Avoid rate limiting legitimate traffic:

- **Burst size**: Set to 2-3x normal peak
- **Sustained rate**: Set to 150% of expected peak
- **Monitoring**: Alert on rate limits before blocking
- **Gradual rollout**: Test limits in staging first

### Security vs Usability

Balance security with usability:

**Too strict:**
- Legitimate plugins get blocked
- Poor user experience
- Support burden increases

**Too permissive:**
- DoS attacks not prevented
- Resource exhaustion possible
- Platform instability

**Right balance:**
- 99% of requests succeed
- Anomalous spikes blocked
- Graceful degradation under load

## Troubleshooting

### Plugin Getting Rate Limited

**Symptoms:**
```
Error: resource_exhausted: rate limit exceeded
```

**Solutions:**
1. Check if plugin is making excessive requests
2. Implement client-side rate limiting
3. Use exponential backoff on retries
4. Request higher limits from platform operator
5. Batch requests instead of individual calls

### Rate Limiter Not Working

**Check:**
- `RateLimiter` set in `ServeConfig`
- Runtime ID present in `X-Plugin-Runtime-ID` header
- Rate struct has valid values (Burst > 0, RequestsPerSecond >= 0)

**Debug:**
```go
// Add logging to rate limiter
allowed := limiter.Allow("runtime-id", rate)
if !allowed {
    log.Printf("[RateLimit] Denied request from %s", runtimeID)
}
```

### Memory Leak Concerns

Rate limiters maintain per-key state. To prevent leaks:

- **Idle cleanup**: Buckets unused for >5 minutes are removed automatically
- **Close on shutdown**: Call `limiter.Close()` to stop cleanup goroutine
- **Monitor bucket count**: Track `len(limiter.buckets)` (requires internal access)

## Examples

See working examples:
- `ratelimit_test.go` - Unit tests with various scenarios
- `examples/fx-managed/` - Production-like setup with rate limiting

## Next Steps

- [Security Guide](../security.md) - Complete security overview
- [Configuration Reference](../reference/configuration.md) - All config options
- [Interceptors Guide](interceptors.md) - Interceptor patterns
