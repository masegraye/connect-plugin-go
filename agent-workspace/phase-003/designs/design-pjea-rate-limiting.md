# Design: Rate Limiting Middleware for connect-plugin-go

## Problem Statement

The connect-plugin-go server is vulnerable to Denial of Service (DoS) attacks due to lack of rate limiting on public RPC endpoints. Attackers can exploit the following attack vectors:

- **Handshake Flooding** (PROTO-HIGH-005): Attacker sends unlimited `Handshake()` requests without authentication, exhausting server resources
- **Registration Flooding** (REG-HIGH-001): Attacker sends unlimited `RegisterService()` requests, polluting the service registry
- **Watcher Flooding** (DOS-001): Attacker opens unlimited `WatchService()` streaming connections, consuming memory and connections
- **Capability Flooding**: Attacker sends unlimited `RequestCapability()` requests, exhausting grants and memory
- **Generic Endpoint Flooding**: Any RPC endpoint can be hammered with requests

Without rate limiting, these endpoints are vulnerable to low-effort DoS attacks that can degrade or crash the server.

## Vulnerability Assessment

### Exposed Endpoints

| Endpoint | Service | Method | Authentication | Risk |
|----------|---------|--------|-----------------|------|
| `/plugin.v1.HandshakeService/Handshake` | HandshakeService | Unary | None | HIGH - Protocol negotiation |
| `/plugin.v1.ServiceRegistry/RegisterService` | ServiceRegistry | Unary | Runtime Token | HIGH - Mutable state |
| `/plugin.v1.ServiceRegistry/UnregisterService` | ServiceRegistry | Unary | Runtime Token | MEDIUM - Requires valid registration |
| `/plugin.v1.ServiceRegistry/DiscoverService` | ServiceRegistry | Unary | Runtime Token | HIGH - Frequent operation |
| `/plugin.v1.ServiceRegistry/WatchService` | ServiceRegistry | Server Stream | Runtime Token | HIGH - Long-lived connections |
| `/plugin.v1.CapabilityBroker/RequestCapability` | CapabilityBroker | Unary | Runtime Token | HIGH - Memory per grant |
| `/capabilities/*` | CapabilityBroker | HTTP passthrough | Bearer Token | HIGH - Capability access |
| `/services/*` | ServiceRouter | HTTP passthrough | Runtime Token | MEDIUM - Service calls |

### Attack Scenarios

1. **Unauthenticated Handshake Flooding**: Send 10,000 Handshake requests/sec from single IP → server CPU saturated
2. **WatchService Connection Storm**: Open 10,000 concurrent WatchService streams → memory exhausted, no graceful degradation
3. **RegisterService Spam**: Register 1,000 services/sec with valid token → registry polluted, discovery slow
4. **Grant Exhaustion**: Request 100,000 capability grants/sec → memory exhausted, legitimate requests fail

## Rate Limiting Strategy

### Design Principles

1. **Per-Client Isolation**: Rate limits keyed by client identity to prevent cross-client impact
2. **Adaptive Configuration**: Different limits for different endpoint risk levels
3. **Graceful Degradation**: Return informative error codes instead of crashing
4. **Observability**: Metrics and logging for monitoring attack patterns
5. **Zero-Overhead in Success Path**: Fast path when under limits
6. **Configurable Defaults**: Production-ready defaults that can be customized

### Keying Strategy

Rate limits can be keyed by one or more of:

1. **Per-IP (Network)**: Client IP address from request
   - Simple, stateless
   - Breaks down with proxies/load balancers
   - Best for internal networks

2. **Per-Runtime-ID (Plugin Identity)**: Plugin's authenticated identity
   - Requires successful authentication
   - Per-plugin budgets
   - Can be spoofed without proper token validation

3. **Per-Connection (Network + Port)**: Full client address
   - More granular than IP
   - Still vulnerable to distributed attacks

4. **Global**: Single bucket for all clients
   - Protects server, but doesn't isolate clients
   - All clients share same budget

**Recommendation**: Use **Per-Runtime-ID** for authenticated endpoints (Service Registry, Capability Broker) and **Per-IP** for unauthenticated endpoints (Handshake).

### Token Bucket Algorithm

The token bucket algorithm is chosen because:
- Allows burst traffic (tokens accumulate)
- Provides steady-state rate control
- Simple, well-understood, industry standard
- Efficient O(1) operations

```
Bucket State:
  - capacity: maximum tokens (burst size)
  - tokens: current token count (0..capacity)
  - last_refill: timestamp of last refill
  - refill_rate: tokens per second

On Request:
  1. Calculate time elapsed since last_refill
  2. Add (time_elapsed * refill_rate) tokens, cap at capacity
  3. Update last_refill to now
  4. If tokens >= 1:
       - Decrement tokens by 1
       - Allow request
     Else:
       - Reject request (rate limited)
       - Return 429 Too Many Requests
```

### Per-Endpoint Limits

Recommended defaults based on endpoint risk and typical usage:

| Endpoint | Per-Client Rate | Burst | Rationale |
|----------|-----------------|-------|-----------|
| Handshake | 10/sec | 20 | Protocol negotiation at startup |
| RegisterService | 1/sec | 5 | Service registration (once per startup) |
| UnregisterService | 1/sec | 5 | Service unregistration (once per shutdown) |
| DiscoverService | 100/sec | 200 | Frequent discovery queries |
| WatchService | 1/sec | 3 | Single stream per plugin |
| RequestCapability | 10/sec | 20 | Occasional capability requests |
| Capability HTTP | 100/sec | 200 | Capability method calls |
| Service Router | 1000/sec | 2000 | Plugin-to-plugin service calls |

## RateLimiter Interface

### Core Interface

```go
// RateLimiter provides rate limiting for RPC endpoints.
// Thread-safe for concurrent access.
type RateLimiter interface {
  // Allow checks if the request is allowed under the rate limit.
  // key: identifies the client (IP, runtime ID, etc.)
  // limit: operations per second
  // burst: maximum burst size
  //
  // Returns true if request is allowed, false if rate limited.
  // This method must be fast (O(1)) as it's in the critical path.
  Allow(key string, limit Rate) bool

  // Close releases resources (cleanup goroutines, etc.)
  // Safe to call multiple times.
  Close()
}

// Rate represents rate limit parameters
type Rate struct {
  // PerSecond is the steady-state rate (operations per second)
  PerSecond float64

  // Burst is the maximum burst size (in operations)
  Burst int
}
```

### Implementation

```go
// TokenBucket implements RateLimiter using the token bucket algorithm.
type TokenBucket struct {
  mu      sync.RWMutex
  buckets map[string]*tokenBucketState

  // cleanupTicker periodically removes old buckets
  cleanupTicker *time.Ticker
  stopCh        chan struct{}
}

type tokenBucketState struct {
  tokens    float64
  lastRefill time.Time
}

// New creates a new token bucket rate limiter
// cleanupInterval: how often to remove old buckets (e.g., 1 minute)
func NewTokenBucket(cleanupInterval time.Duration) *TokenBucket

// Allow implements RateLimiter
func (tb *TokenBucket) Allow(key string, limit Rate) bool

// Close implements RateLimiter
func (tb *TokenBucket) Close()
```

### Key Properties

- **Thread-Safe**: Uses sync.RWMutex for safe concurrent access
- **Memory Efficient**: Per-key buckets only created on first request
- **Automatic Cleanup**: Background goroutine removes old buckets (no access for 1+ minute)
- **O(1) Operations**: Token check and refill is constant time
- **No External Dependencies**: Pure Go standard library

## Connect Interceptor Integration

### Interceptor Architecture

Connect provides interceptors for both unary and streaming RPCs. Rate limiting is implemented as a Connect interceptor that:

1. Extracts the rate limit key from request
2. Checks rate limit
3. Returns 429 error if limited
4. Continues to actual handler if allowed

```go
// RateLimitInterceptor is a Connect interceptor that applies rate limiting
type RateLimitInterceptor struct {
  limiter   RateLimiter
  keyExtractor KeyExtractor
  limits    map[string]Rate // map[full_procedure_name]Rate
}

// KeyExtractor extracts the rate limit key from a request
type KeyExtractor interface {
  // Extract returns the rate limit key and whether extraction succeeded
  // Returns ("", false) if key cannot be extracted (e.g., no auth header)
  Extract(req *connect.Request[any]) (key string, ok bool)
}

// IPKeyExtractor extracts client IP from request
type IPKeyExtractor struct{}
func (e *IPKeyExtractor) Extract(req *connect.Request[any]) (string, bool)

// RuntimeIDKeyExtractor extracts runtime ID from X-Plugin-Runtime-ID header
type RuntimeIDKeyExtractor struct{}
func (e *RuntimeIDKeyExtractor) Extract(req *connect.Request[any]) (string, bool)
```

### Handler Wrapping

```go
// WrapHandlers returns rate-limited handlers for all services
func (rl *RateLimitInterceptor) WrapHandlers(
  handshakePath string, handshakeHandler http.Handler,
  registryPath string, registryHandler http.Handler,
  brokerPath string, brokerHandler http.Handler,
) (map[string]http.Handler)
```

### Integration Points in server.go

In `Serve()` function, after creating handlers but before registering:

```go
// Create rate limiter
limiter := NewTokenBucket(1 * time.Minute)
defer limiter.Close()

// Create interceptor with per-endpoint limits
interceptor := NewRateLimitInterceptor(
  limiter,
  map[string]Rate{
    "/plugin.v1.HandshakeService/Handshake": {
      PerSecond: 10,
      Burst: 20,
    },
    "/plugin.v1.ServiceRegistry/RegisterService": {
      PerSecond: 1,
      Burst: 5,
    },
    // ... other endpoints
  },
)

// Wrap handlers with rate limiting
limitedHandlers := interceptor.WrapHandlers(...)

// Register limited handlers instead of originals
mux.Handle(handshakePath, limitedHandlers[handshakePath])
```

### Error Response

When rate limited:

```
HTTP/1.1 429 Too Many Requests
Content-Type: application/json

{
  "code": "resource_exhausted",
  "message": "rate limit exceeded: too many requests per second"
}
```

For Connect over HTTP/2:

```
HTTP/2 429
Content-Type: application/json

{
  "code": 8,
  "message": "rate limit exceeded: too many requests per second"
}
```

## Configuration

### ServeConfig Extension

Add to `ServeConfig` struct:

```go
// RateLimitConfig configures rate limiting middleware
RateLimitConfig *RateLimitConfig

// RateLimitConfig defines rate limits for all endpoints
type RateLimitConfig struct {
  // Enabled turns rate limiting on/off
  // Default: true (rate limiting enabled)
  Enabled bool

  // Handshake rate limit
  Handshake Rate

  // RegisterService rate limit
  RegisterService Rate

  // UnregisterService rate limit
  UnregisterService Rate

  // DiscoverService rate limit
  DiscoverService Rate

  // WatchService rate limit
  WatchService Rate

  // RequestCapability rate limit
  RequestCapability Rate

  // CapabilityHTTP rate limit (for /capabilities/* endpoints)
  CapabilityHTTP Rate

  // ServiceRouter rate limit (for /services/* endpoints)
  ServiceRouter Rate

  // KeyStrategy determines how to key rate limits
  // "per-ip": Key by client IP (for unauthenticated endpoints)
  // "per-runtime-id": Key by plugin runtime ID (for authenticated endpoints)
  // "per-connection": Key by client IP + port
  // "global": Single bucket for all clients
  // Default: "per-ip" for Handshake, "per-runtime-id" for others
  KeyStrategy string

  // CleanupInterval is how often to clean up old buckets
  // Default: 1 minute
  CleanupInterval time.Duration
}
```

### Defaults

```go
// DefaultRateLimitConfig returns production-ready rate limit defaults
func DefaultRateLimitConfig() *RateLimitConfig {
  return &RateLimitConfig{
    Enabled: true,
    Handshake: Rate{PerSecond: 10, Burst: 20},
    RegisterService: Rate{PerSecond: 1, Burst: 5},
    UnregisterService: Rate{PerSecond: 1, Burst: 5},
    DiscoverService: Rate{PerSecond: 100, Burst: 200},
    WatchService: Rate{PerSecond: 1, Burst: 3},
    RequestCapability: Rate{PerSecond: 10, Burst: 20},
    CapabilityHTTP: Rate{PerSecond: 100, Burst: 200},
    ServiceRouter: Rate{PerSecond: 1000, Burst: 2000},
    KeyStrategy: "per-ip", // Will be overridden per endpoint
    CleanupInterval: 1 * time.Minute,
  }
}
```

### Usage

```go
// Use defaults
cfg := &ServeConfig{
  // ... other config
  RateLimitConfig: DefaultRateLimitConfig(),
}

// Or customize
cfg := &ServeConfig{
  // ... other config
  RateLimitConfig: &RateLimitConfig{
    Enabled: true,
    Handshake: Rate{PerSecond: 5, Burst: 10}, // More restrictive
    // ... other endpoints with defaults
  },
}

// Or disable
cfg := &ServeConfig{
  // ... other config
  RateLimitConfig: &RateLimitConfig{Enabled: false},
}
```

## Implementation Plan

### Phase 1: Core Infrastructure

1. **Create `ratelimit.go`**
   - Implement `Rate` type
   - Implement `TokenBucket` struct
   - Implement `TokenBucket.Allow()` method
   - Implement background cleanup goroutine
   - Add comprehensive tests

2. **Add to `server.go`**
   - Add `RateLimitConfig` to `ServeConfig`
   - Add `DefaultRateLimitConfig()` helper
   - Add validation in `ServeConfig.Validate()`

### Phase 2: Key Extraction

3. **Create `ratelimit_key.go`**
   - Implement `KeyExtractor` interface
   - Implement `IPKeyExtractor`
   - Implement `RuntimeIDKeyExtractor`
   - Add helper to extract request IP from various headers

### Phase 3: Interceptor Integration

4. **Create `ratelimit_interceptor.go`**
   - Implement `RateLimitInterceptor` struct
   - Implement unary RPC wrapping
   - Implement streaming RPC wrapping
   - Implement HTTP passthrough wrapping for `/capabilities/*` and `/services/*`

### Phase 4: Server Integration

5. **Update `server.go` Serve() function**
   - Create rate limiter instance
   - Create interceptor with configured limits
   - Wrap all handlers before registration
   - Ensure graceful cleanup on shutdown

6. **Create `ratelimit_test.go`**
   - Test token bucket algorithm
   - Test cleanup of old buckets
   - Test concurrent access
   - Test boundary conditions (burst size, zero tokens, etc.)

### Phase 5: Documentation

7. **Add comments and examples**
   - Document rate limiting in `doc.go`
   - Add example configuration in integration tests
   - Document attack mitigation strategy

## Thread Safety

All components are thread-safe:

- `TokenBucket`: Uses `sync.RWMutex` for concurrent map access
- Key extraction: Stateless, no shared state
- Interceptor: Stateless after initialization
- Cleanup goroutine: Coordinates via mutex-protected map

Lock contention is minimized:
- Read locks used for most common case (bucket exists and has tokens)
- Write locks only on first-time bucket creation and cleanup
- Fast path (tokens available) is O(1) with minimal lock holding time

## Error Handling

### Rate Limit Exceeded

```
HTTP/1.1 429 Too Many Requests
Content-Type: application/json

{
  "code": "resource_exhausted",
  "message": "rate limit exceeded for client <key>: <limit> ops/sec, burst <burst>"
}
```

### Invalid Key Extraction

If key cannot be extracted (e.g., missing runtime ID header on authenticated endpoint), return 401 Unauthorized (existing auth error, not rate limit issue).

### Rate Limiter Errors

If rate limiter fails internally (e.g., clock drift), allow request and log error. Fail-open is better than false positives.

## Metrics and Observability

While not part of this initial design, the rate limiter should be instrumented for:

- Total requests per endpoint
- Rate-limited requests per endpoint (429 errors)
- Top rate-limited clients (top 10 by IP/runtime ID)
- Token bucket state (tokens available, refill rate)

These can be added via metrics hooks after core implementation.

## Testing Strategy

### Unit Tests

- Token bucket refill logic
- Token bucket burst handling
- Cleanup of old buckets
- Concurrent access patterns
- Boundary conditions (exactly at limit, just over limit)

### Integration Tests

- End-to-end rate limiting on each endpoint
- Multiple concurrent clients
- Verify burst works correctly
- Verify cleanup removes old buckets

### Load Tests

- Sustained load at limit
- Burst traffic
- Multiple endpoints simultaneously
- Memory usage with many clients

## Migration and Rollout

1. **Initial Release**: Rate limiting enabled by default with conservative limits
2. **Configuration Option**: Allow operators to disable or adjust limits
3. **Metrics Collection**: Log rate-limit events for analysis
4. **Feedback Loop**: Adjust limits based on real-world usage patterns
5. **Documentation**: Explain limits and attack mitigation

## Future Enhancements

1. **Distributed Rate Limiting**: For load-balanced deployments (Redis backend)
2. **Sliding Window Counter**: More accurate than token bucket for some use cases
3. **Leaky Bucket**: Smoother traffic shaping
4. **Circuit Breaker Integration**: Fail fast when consistently over limit
5. **Per-Endpoint Metrics**: Detailed instrumentation and dashboards
6. **DDoS Detection**: Anomaly detection for attack patterns
7. **Cost-Based Limiting**: Different costs for different operations
8. **Adaptive Limits**: Auto-adjust based on server load

## Implementation Checklist

- [ ] Create `ratelimit.go` with `TokenBucket` implementation
- [ ] Add tests for token bucket algorithm
- [ ] Create `ratelimit_key.go` with key extractors
- [ ] Create `ratelimit_interceptor.go` with handler wrapping
- [ ] Update `server.go` with rate limiter initialization
- [ ] Update `ServeConfig` with rate limit configuration
- [ ] Add integration tests with rate limiting
- [ ] Document in code comments and examples
- [ ] Security test for DoS scenarios
- [ ] Load test for performance impact
- [ ] Update README with rate limiting documentation

## Security Considerations

1. **Timing Attacks**: Token bucket comparison should be timing-safe (already is, simple floating point comparison)
2. **Lock Contention**: Minimize time spent in critical sections
3. **Resource Cleanup**: Ensure cleanup goroutine can't be exhausted
4. **Key Space Explosion**: Rate limiter can be attacked via high cardinality keys (many unique IPs)
   - Mitigation: Configure cleanup to remove old buckets frequently
   - Mitigation: Use approximate data structures (Bloom filter) for key counts in future
5. **Bypass via Proxies**: IP-based keying can be bypassed if attacker spoofs IPs via proxy
   - Mitigation: Use per-runtime-id for authenticated endpoints
   - Mitigation: Use X-Forwarded-For only from trusted proxies

## References

- Token Bucket Algorithm: https://en.wikipedia.org/wiki/Token_bucket
- Connect RPC Interceptors: https://connectrpc.com/docs/go/interceptors
- HTTP 429 Status Code: https://httpwg.org/specs/rfc6585.html#status.429
- OWASP Rate Limiting: https://owasp.org/www-community/attacks/Denial_of_Service

