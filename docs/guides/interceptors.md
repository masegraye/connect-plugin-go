# Interceptors Guide

Interceptors add cross-cutting concerns to RPC calls: retry logic, circuit breakers, authentication, logging, and metrics.

## Overview

connect-plugin-go uses Connect's interceptor pattern. Interceptors wrap RPC calls and can:

- Modify requests before sending
- Retry failed requests
- Short-circuit calls (circuit breaker)
- Add authentication credentials
- Log calls and measure latency
- Transform responses

## Interceptor Composition

Interceptors are applied in order, wrapping each other:

```go
// Outermost → Innermost
interceptors := []connect.UnaryInterceptorFunc{
    authInterceptor,           // 1. Add auth (outermost)
    circuitBreakerInterceptor, // 2. Check circuit state
    retryInterceptor,          // 3. Retry if needed (innermost)
}

// Execution flow (request):
// auth → circuit breaker → retry → actual RPC call
//
// Response flow (reverse):
// actual RPC → retry logic → circuit breaker logic → auth cleanup
```

**Order matters!** Place interceptors carefully:

1. **Auth**: Outermost (applies to all attempts)
2. **Circuit Breaker**: Next (fail fast when service is down)
3. **Retry**: Innermost (retries individual attempts)

## Retry Interceptor

Automatically retries failed RPC calls with exponential backoff.

### Basic Usage

```go
// Use defaults (3 attempts, 100ms initial backoff)
policy := connectplugin.DefaultRetryPolicy()
interceptor := connectplugin.RetryInterceptor(policy)
```

### Custom Configuration

```go
policy := connectplugin.RetryPolicy{
    MaxAttempts:       5,
    InitialBackoff:    50 * time.Millisecond,
    MaxBackoff:        5 * time.Second,
    BackoffMultiplier: 2.0,
    Jitter:            true,  // Add randomness to prevent thundering herd
}

interceptor := connectplugin.RetryInterceptor(policy)
```

### Backoff Calculation

Exponential backoff with jitter:

```
Attempt 1: 100ms * 2^0 = 100ms
Attempt 2: 100ms * 2^1 = 200ms
Attempt 3: 100ms * 2^2 = 400ms
Attempt 4: 100ms * 2^3 = 800ms
...
Capped at MaxBackoff

With jitter: actual = calculated * random(0.5, 1.0)
```

### Retryable Errors

By default, retries these error codes:

- ✅ `Unavailable` - Service temporarily down
- ✅ `ResourceExhausted` - Rate limited
- ✅ `Internal` - Server error
- ✅ `Unknown` - Unknown error
- ✅ `DeadlineExceeded` - Timeout

Does NOT retry:

- ❌ `InvalidArgument` - Client error
- ❌ `NotFound` - Resource doesn't exist
- ❌ `PermissionDenied` - Not authorized
- ❌ `Cancelled` - Client cancelled
- ❌ `Unauthenticated` - Auth failed

### Custom Retry Logic

```go
policy := connectplugin.RetryPolicy{
    MaxAttempts: 3,
    IsRetryable: func(err error) bool {
        // Only retry on rate limiting
        return connect.CodeOf(err) == connect.CodeResourceExhausted
    },
}
```

### Context Awareness

Retry respects context deadlines and cancellation:

```go
ctx, cancel := context.WithTimeout(context.Background(), 1*time.Second)
defer cancel()

// Retry stops when context deadline exceeded
resp, err := client.SomeCall(ctx, req)
```

## Circuit Breaker

Fail fast when a service is consistently failing.

### State Machine

```
          failures >= threshold
Closed ──────────────────────────> Open
  ↑                                  │
  │                                  │ timeout elapsed
  │                                  ↓
  │                              HalfOpen
  │  successes >= threshold         │
  └─────────────────────────────────┘
                                     │ any failure
                                     └──> Open
```

**States:**

- **Closed**: Normal operation, track failures
- **Open**: Reject all calls immediately (fail fast)
- **HalfOpen**: Allow probe requests to test recovery

### Basic Usage

```go
// Use defaults (5 failures, 2 successes, 10s timeout)
cb := connectplugin.NewCircuitBreaker(
    connectplugin.DefaultCircuitBreakerConfig(),
)

interceptor := connectplugin.CircuitBreakerInterceptor(cb)
```

### Custom Configuration

```go
config := connectplugin.CircuitBreakerConfig{
    FailureThreshold: 3,     // Open after 3 consecutive failures
    SuccessThreshold: 2,     // Close after 2 consecutive successes
    Timeout:          5 * time.Second,  // Try half-open after 5s
    OnStateChange: func(from, to connectplugin.CircuitState) {
        log.Printf("Circuit %s → %s", from, to)
    },
}

cb := connectplugin.NewCircuitBreaker(config)
```

### Monitoring

Check circuit state:

```go
state := cb.State()  // CircuitClosed, CircuitOpen, or CircuitHalfOpen

switch state {
case connectplugin.CircuitClosed:
    // Normal operation
case connectplugin.CircuitOpen:
    // Service is down, calls fail immediately
case connectplugin.CircuitHalfOpen:
    // Testing recovery, allowing probes
}
```

### Custom Failure Detection

```go
config := connectplugin.CircuitBreakerConfig{
    FailureThreshold: 5,
    IsFailure: func(err error) bool {
        // Only count 500-level errors as failures
        code := connect.CodeOf(err)
        return code == connect.CodeInternal ||
               code == connect.CodeUnavailable
    },
}
```

## Authentication

Secure plugin communication with multiple auth mechanisms.

### Token-Based Auth

Bearer token authentication:

```go
// Client side: Add token to requests
auth := connectplugin.NewTokenAuth("my-secret-token", nil)
interceptor := auth.ClientInterceptor()

// Server side: Validate tokens
validateToken := func(token string) (string, map[string]string, error) {
    if token == "my-secret-token" {
        return "user-123", map[string]string{"role": "admin"}, nil
    }
    return "", nil, errors.New("invalid token")
}

auth := connectplugin.NewTokenAuth("", validateToken)
serverInterceptor := auth.ServerInterceptor()

// Require authentication
requireAuth := connectplugin.RequireAuth()
```

**Flow:**

1. Client adds: `Authorization: Bearer my-secret-token`
2. Server validates token
3. Server stores `AuthContext` in context
4. Handler accesses: `connectplugin.GetAuthContext(ctx)`

### API Key Auth

Simpler than tokens (no "Bearer" prefix):

```go
// X-API-Key header
auth := connectplugin.NewAPIKeyAuth("api-key-123", validateFunc)

// Client adds: X-API-Key: api-key-123
// Server validates and sets AuthContext
```

### mTLS

Mutual TLS for transport-level security:

```go
// Client side
auth := connectplugin.NewMTLSAuth(clientCert, rootCAs, nil)

httpClient := &http.Client{}
auth.ConfigureClientTLS(httpClient)  // Sets TLS config

// Server side
auth := connectplugin.NewMTLSAuth(nil, nil, clientCAs)
tlsConfig, _ := auth.ConfigureServerTLS()

server := &http.Server{
    TLSConfig: tlsConfig,
}
server.ListenAndServeTLS(":8443", certFile, keyFile)
```

### Multi-Mechanism Auth

Try multiple auth methods (first success wins):

```go
// Client: Add both mTLS and token
auth1 := mtlsAuth
auth2 := tokenAuth

composed := connectplugin.ComposeAuthClient(auth1, auth2)
// Adds both cert and token to requests

// Server: Try mTLS first, fallback to token
composed := connectplugin.ComposeAuthServer(mtlsAuth, tokenAuth)
// First provider that authenticates wins
```

### Access Auth Context

In your RPC handler:

```go
func (s *MyService) SomeMethod(
    ctx context.Context,
    req *connect.Request[SomeRequest],
) (*connect.Response[SomeResponse], error) {
    // Get authenticated identity
    auth := connectplugin.GetAuthContext(ctx)
    if auth != nil {
        log.Printf("Request from: %s (provider: %s)", auth.Identity, auth.Provider)

        if auth.Claims["role"] == "admin" {
            // Admin-only operation
        }
    }

    return connect.NewResponse(&SomeResponse{}), nil
}
```

## Complete Example

Combining all interceptors:

```go
// Configure retry
retryPolicy := connectplugin.RetryPolicy{
    MaxAttempts:    3,
    InitialBackoff: 100 * time.Millisecond,
}

// Configure circuit breaker
cb := connectplugin.NewCircuitBreaker(connectplugin.CircuitBreakerConfig{
    FailureThreshold: 5,
    SuccessThreshold: 2,
    Timeout:          10 * time.Second,
    OnStateChange: func(from, to connectplugin.CircuitState) {
        log.Printf("Circuit breaker: %s → %s", from, to)
    },
})

// Configure auth
auth := connectplugin.NewTokenAuth("my-token", nil)

// Compose interceptors (order: auth → CB → retry)
client, _ := connectplugin.NewClient(connectplugin.ClientConfig{
    Endpoint: "https://plugin.example.com",
    Plugins:  pluginSet,
    // TODO: Add interceptors to ClientConfig
})

// Now all calls have: auth + circuit breaker + retry
kvStore := connectplugin.MustDispenseTyped[KVStore](client, "kv")
value, err := kvStore.Get(ctx, "key")
```

## Interceptor Best Practices

### Order Matters

**Recommended order (outermost to innermost):**

1. **Logging/Metrics**: Capture all attempts
2. **Auth**: Apply to all calls
3. **Circuit Breaker**: Fail fast before retrying
4. **Retry**: Retry individual attempts
5. **Actual call**: The RPC

### Avoid Retry Storms

Circuit breaker prevents retry storms:

```
Without circuit breaker:
Service down → Retry 3x → Retry 3x → Retry 3x
(9 total attempts overwhelming dead service)

With circuit breaker:
Service down → Retry 3x → Open circuit → Fail fast
(Only 3 attempts, then immediate failures)
```

### Context Propagation

Interceptors preserve context:

```go
// Auth context flows through all layers
ctx = connectplugin.WithAuthContext(ctx, authContext)

// Available in handler even through retry/circuit breaker
auth := connectplugin.GetAuthContext(ctx)
```

### Monitoring

Hook state changes for observability:

```go
cb := connectplugin.NewCircuitBreaker(connectplugin.CircuitBreakerConfig{
    OnStateChange: func(from, to connectplugin.CircuitState) {
        metrics.RecordCircuitStateChange(from.String(), to.String())
        if to == connectplugin.CircuitOpen {
            alerts.NotifyCircuitOpen()
        }
    },
})
```

## Next Steps

- [Discovery Guide](discovery.md) - Endpoint discovery patterns
- [Configuration Reference](../reference/configuration.md) - Detailed config
- [Migration Guide](../migration/from-go-plugin.md) - Migrate from go-plugin
