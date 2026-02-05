# Performance Guide

Performance characteristics and optimization guidance for connect-plugin-go deployments.

## Overview

connect-plugin-go is designed for production use with minimal overhead. This guide helps you understand performance characteristics and optimize for your workload.

## Performance Characteristics

### Token Validation

**Operation:** Runtime token validation with constant-time comparison

**Typical Performance:**
- Throughput: 1-5 million validations/second
- Latency: 200-500 nanoseconds per validation
- Overhead: ~70ns vs standard string comparison

**Note:** Uses write lock for lazy cleanup of expired tokens. Under very high load (>1000 req/s), monitor for lock contention.

### Rate Limiting

**Operation:** Token bucket check per request

**Typical Performance:**
- Throughput: 500K-1M operations/second
- Latency: 500-1000 nanoseconds per check
- Memory: ~200 bytes per active runtime ID

**Cleanup:** Idle buckets removed after 5 minutes (prevents memory leaks)

### Token Generation

**Operation:** Cryptographic random token generation

**Typical Performance:**
- Latency: 1-5 microseconds per token
- Crypto/rand overhead: ~1-3 μs
- Base64 encoding: ~100-200 ns

**Frequency:** Only during handshake (infrequent operation)

## Pre-Production Testing

### Recommended Benchmarks

Run these benchmarks before deploying to production:

```bash
# 1. Token validation throughput
go test -bench=BenchmarkValidateToken -benchtime=10s -cpu=1,2,4,8

# Expected on modern hardware:
# BenchmarkValidateToken-8    5000000    250 ns/op

# 2. Rate limiter throughput
go test -bench=BenchmarkRateLimiter -benchtime=10s

# Expected:
# BenchmarkRateLimiter-8      2000000    600 ns/op

# 3. With race detector (slower but catches bugs)
go test -race -bench=. -benchtime=1s
```

### Load Testing

**Scenario 1: Steady State (Baseline)**

```bash
# Simulate normal production load
# - 100 concurrent plugins
# - 10 req/s per plugin = 1000 req/s total

vegeta attack -rate=1000 -duration=60s -targets=targets.txt | vegeta report

# Expected p99: < 10ms
# Alert if p99: > 50ms
```

**Scenario 2: Burst Traffic**

```bash
# Simulate traffic spike
# - Ramp from 100 to 5000 req/s
# - Maintain for 60s
# - Verify rate limiting works

hey -n 300000 -c 100 -q 5000 https://plugin-host:8443/health

# Expected:
# - Some 429 responses (rate limited)
# - p99 latency stable
# - No errors besides rate limits
```

**Scenario 3: Rate Limit Validation**

```bash
# Verify single plugin can't exceed limits
for i in {1..1000}; do
  curl -H "X-Plugin-Runtime-ID: test-plugin" \
       https://localhost:8443/plugin.v1.HandshakeService/Handshake &
done
wait

# Verify: Most requests return 429 after burst exhausted
```

## Monitoring

### Key Metrics

Monitor these in production:

**Latency:**
- Token validation p50/p95/p99
- Rate limiter check p50/p95/p99
- End-to-end request latency

**Throughput:**
- Requests per second (total)
- Requests per runtime ID
- Rate limit denials per second

**Resource Usage:**
- Active rate limit buckets
- Memory usage (rate limiter maps)
- Goroutine count (cleanup routines)

### Performance Alerts

Set up alerts for degradation:

```yaml
# Prometheus alerts
- alert: HighTokenValidationLatency
  expr: histogram_quantile(0.99, rate(token_validation_duration_seconds_bucket[5m])) > 0.005
  annotations:
    summary: "Token validation p99 > 5ms (possible lock contention)"

- alert: HighRateLimitDenialRate
  expr: rate(rate_limit_denials[5m]) / rate(requests_total[5m]) > 0.05
  annotations:
    summary: "More than 5% of requests being rate limited"
```

## Optimization Strategies

### When Performance Degrades

**Symptom:** Token validation latency increasing

**Diagnosis:**
```bash
# Profile with pprof
go test -cpuprofile=cpu.prof -bench=BenchmarkValidateToken
go tool pprof cpu.prof
```

**Solutions:**
1. Increase `RuntimeTokenTTL` (reduces cleanup frequency)
2. Add more replicas (horizontal scaling)
3. Upgrade to Phase 3 read-lock optimization (future)

**Symptom:** Rate limiter denying legitimate requests

**Diagnosis:**
- Check effective rate limit (target ÷ replica count)
- Monitor request patterns (bursty vs sustained)
- Review rate limit configuration

**Solutions:**
1. Increase `RequestsPerSecond` limit
2. Increase `Burst` capacity
3. Use external API gateway for distributed limiting

**Symptom:** Memory growth over time

**Diagnosis:**
```bash
# Check for leaked rate limit buckets
go test -memprofile=mem.prof -bench=BenchmarkRateLimiter
go tool pprof mem.prof
```

**Solutions:**
1. Verify `limiter.Close()` is called on shutdown
2. Check cleanup goroutine is running
3. Review bucket cleanup threshold (5 minutes default)

## Scalability Limits

### Tested Configurations

| Metric | Tested | Recommended Max |
|--------|--------|----------------|
| Concurrent plugins | 1,000 | 5,000 |
| Requests per second (per replica) | 10,000 | 50,000 |
| Active rate limit buckets | 10,000 | 50,000 |
| Token map size | 1,000 entries | 10,000 entries |

### Scaling Guidelines

**Vertical Scaling:**
- CPU: 2-4 cores per replica (token validation is CPU-bound)
- Memory: 512MB-1GB per replica (depends on active plugin count)
- Network: 1Gbps sufficient for most workloads

**Horizontal Scaling:**
- Add replicas for higher throughput
- Adjust rate limits for replica count (see Distributed Deployments)
- Use load balancer with health checks

## Benchmarking Your Deployment

### Custom Load Test

Create a realistic load test for your specific use case:

```go
// loadtest/main.go
package main

import (
    "context"
    "log"
    "sync"
    "time"

    connectplugin "github.com/masegraye/connect-plugin-go"
)

func main() {
    // Simulate 100 plugins
    var wg sync.WaitGroup
    for i := 0; i < 100; i++ {
        wg.Add(1)
        go func(id int) {
            defer wg.Done()

            // Each plugin makes 10 req/s
            ticker := time.NewTicker(100 * time.Millisecond)
            defer ticker.Stop()

            client := createClient(id)

            for j := 0; j < 600; j++ {  // Run for 1 minute
                <-ticker.C
                makeRequest(client)
            }
        }(i)
    }

    wg.Wait()
    log.Println("Load test complete")
}
```

Run and analyze:
```bash
go run loadtest/main.go
# Monitor:
# - CPU usage
# - Memory growth
# - Error rate
# - Latency distribution
```

## Known Performance Characteristics

### Lock Contention Points

**1. Token Validation (handshake.go)**
- Uses write lock for lazy cleanup
- Impact: Low at <1000 req/s, monitor at >1000 req/s
- Mitigation: Increase RuntimeTokenTTL or wait for Phase 3 optimization

**2. Rate Limiter Bucket Creation (ratelimit.go)**
- Write lock when creating new bucket for runtime ID
- Impact: One-time cost per new runtime ID
- Mitigation: None needed (infrequent operation)

**3. Service Registry Lookup (registry.go)**
- Read lock for service discovery
- Impact: Negligible (read-heavy workload)
- Optimization: Already uses RWMutex

### Memory Usage

**Per-Plugin Overhead:**
- Runtime token: ~100 bytes
- Rate limit bucket: ~200 bytes
- Service registration: ~500 bytes per service
- **Total: ~800 bytes per plugin**

**Example:** 1,000 active plugins = ~800KB memory (negligible)

### GC Impact

All hot paths use:
- Value types where possible (reduce allocations)
- Sync pools for frequent allocations (future)
- Bounded map sizes with cleanup

**GC pause impact:** Minimal (< 1ms) for typical workloads

## Next Steps

- [Security Guide](../security.md) - Security best practices
- [Rate Limiting Guide](rate-limiting.md) - Rate limit configuration
- [Configuration Reference](../reference/configuration.md) - All options
