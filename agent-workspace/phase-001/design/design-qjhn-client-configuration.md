# Design: Client Configuration & Connection Model

**Issue:** KOR-qjhn
**Status:** Complete
**Dependencies:** KOR-fzki, KOR-bpyd, KOR-munj

## Overview

The client configuration defines how a host application connects to and interacts with plugins. Unlike go-plugin which requires subprocess management, connect-plugin clients are simpler but need more network-aware configuration for discovery, resilience, and health monitoring.

## Design Philosophy

**Problem:** Initial design had 30+ configuration fields, overwhelming users and making simple cases unnecessarily complex.

**Solution:** Split configuration into two levels:

1. **ClientConfig** (2-3 required fields) - Minimal configuration for simple cases
   - Just `Endpoint` and `Plugins`
   - No optional fields exposed
   - Clear required vs optional separation

2. **ClientOptions** (all optional) - Advanced features opt-in
   - Retries: disabled by default (MaxAttempts=1)
   - Health monitoring: disabled by default (HealthCheckInterval=0)
   - Discovery, circuit breakers, etc.: all nil by default

**Key Decisions:**

- **Removed SkipHandshake**: Handshake is always performed for safety
- **Retries opt-in**: MaxAttempts=1 by default (no retries, no hidden latency)
- **Health monitoring opt-in**: HealthCheckInterval=0 by default (no background goroutines)
- **Retry attempts reduced**: MaxAttempts=2 recommended (not 3) to reduce overhead
- **Removed dual config**: Simplified Plugins vs VersionedPlugins (VersionedPlugins moved to Options)
- **Clear timeout docs**: RequestTimeout applies to all retry attempts combined

## Design Goals

1. **Simple defaults**: Minimal config for common cases (2-3 fields)
2. **Progressive enhancement**: Advanced features available but optional
3. **Opt-in complexity**: No hidden costs (retries, health checks, etc.)
4. **Network-aware**: Support discovery, retry, circuit breakers when needed
5. **Lazy connection**: Don't connect until needed
6. **Explicit control**: Connection lifecycle is clear

## Configuration Structure

The configuration is split into two parts:

1. **ClientConfig** - Minimal required fields (3-5 fields for simple cases)
2. **ClientOptions** - Optional advanced features

### ClientConfig (Required)

```go
// ClientConfig is the minimal configuration required to create a plugin client.
// For most use cases, only Endpoint and Plugins are needed.
type ClientConfig struct {
    // Endpoint is the plugin service URL.
    // Required. Supports: http://, https://, dns:///, k8s:///
    // Examples:
    //   - "http://localhost:8080"
    //   - "https://plugin.example.com"
    //   - "dns:///plugin-service" (with Discovery)
    Endpoint string

    // Plugins defines available plugin types.
    // Required. Maps plugin name to implementation.
    Plugins PluginSet

    // Discovery provides dynamic endpoint resolution (optional).
    // If set, Endpoint is treated as a service name to discover.
    // Most users can leave this nil.
    Discovery DiscoveryService
}
```

### ClientOptions (Optional)

```go
// ClientOptions provides optional advanced configuration.
// All fields are optional and have sensible defaults.
type ClientOptions struct {
    // ===== Protocol Configuration =====

    // AppProtocolVersions are supported plugin API versions.
    // Negotiated during handshake. First = most preferred.
    // Default: []int{1}
    AppProtocolVersions []int

    // VersionedPlugins maps protocol versions to plugin sets.
    // Only needed when supporting multiple plugin API versions.
    // If set, overrides ClientConfig.Plugins.
    VersionedPlugins map[int]PluginSet

    // MagicCookieKey and Value for validation (not security).
    // Default: DefaultMagicCookieKey/Value
    MagicCookieKey   string
    MagicCookieValue string

    // ===== Network & Transport =====

    // HTTPClient for making requests.
    // Default: creates client with 30s timeout
    HTTPClient connect.HTTPClient

    // TLSConfig for HTTPS connections.
    // Default: system TLS config for https:// endpoints
    TLSConfig *tls.Config

    // RequestTimeout is the default timeout for plugin RPCs.
    // This is the total time including retries.
    // Can be overridden per-request via context.
    // Default: 30 seconds
    //
    // NOTE: If RetryPolicy is configured, this timeout applies to
    // all retry attempts combined. For example, with RequestTimeout=10s
    // and MaxAttempts=2, each attempt gets ~5s before timeout.
    RequestTimeout time.Duration

    // ===== Resilience (Opt-in) =====

    // RetryPolicy configures automatic retry behavior.
    // Default: single attempt (MaxAttempts=1, no retries)
    // Set MaxAttempts > 1 to enable retries.
    RetryPolicy *RetryPolicy

    // CircuitBreaker configures circuit breaker behavior.
    // Default: nil (circuit breaking disabled)
    CircuitBreaker *CircuitBreakerConfig

    // ===== Health Monitoring (Opt-in) =====

    // HealthCheckInterval for background health checks.
    // Default: 0 (health monitoring disabled)
    // Set to non-zero (e.g., 30s) to enable health monitoring.
    HealthCheckInterval time.Duration

    // HealthCheckTimeout for each health check RPC.
    // Only used when HealthCheckInterval > 0.
    // Default: 5 seconds
    HealthCheckTimeout time.Duration

    // OnHealthChange callback when plugin health changes.
    // Only called when HealthCheckInterval > 0.
    OnHealthChange func(status HealthStatus)

    // ===== Capabilities =====

    // CapabilityBroker for host→plugin capabilities.
    // If set, advertised during handshake.
    CapabilityBroker *CapabilityBroker

    // ===== Observability =====

    // Interceptors for cross-cutting concerns.
    // Applied in order (first = outermost).
    Interceptors []connect.Interceptor

    // Logger for client operations.
    // Default: noop logger
    Logger Logger

    // ===== Metadata =====

    // ClientName identifies this client instance.
    // Sent in handshake metadata.
    ClientName string

    // ClientMetadata is custom metadata sent in handshake.
    ClientMetadata map[string]string
}
```

## Client Creation

```go
// NewClient creates a plugin client with minimal required configuration.
// Advanced features are opt-in via ClientOptions.
func NewClient(cfg ClientConfig, opts *ClientOptions) (*Client, error) {
    // Validate required fields
    if err := cfg.Validate(); err != nil {
        return nil, err
    }

    // Apply defaults to options
    if opts == nil {
        opts = &ClientOptions{}
    }
    opts = opts.withDefaults()

    client := &Client{
        cfg:  cfg,
        opts: opts,
    }

    return client, nil
}

// withDefaults applies default values to ClientOptions.
func (o *ClientOptions) withDefaults() *ClientOptions {
    result := &ClientOptions{}
    *result = *o // Copy

    // Protocol defaults
    if len(result.AppProtocolVersions) == 0 {
        result.AppProtocolVersions = []int{1}
    }
    if result.MagicCookieKey == "" {
        result.MagicCookieKey = DefaultMagicCookieKey
        result.MagicCookieValue = DefaultMagicCookieValue
    }

    // Network defaults
    if result.RequestTimeout == 0 {
        result.RequestTimeout = 30 * time.Second
    }

    // Retry defaults (single attempt by default)
    if result.RetryPolicy == nil {
        result.RetryPolicy = &RetryPolicy{
            MaxAttempts: 1, // No retries by default
        }
    }

    // Health monitoring disabled by default
    // (HealthCheckInterval defaults to 0)

    if result.HealthCheckTimeout == 0 {
        result.HealthCheckTimeout = 5 * time.Second
    }

    // Logger defaults to noop
    if result.Logger == nil {
        result.Logger = NoopLogger{}
    }

    return result
}
```

## Configuration Examples

### Simple Case (3 fields - most common)

```go
// Minimal configuration: just endpoint and plugins
// No retries, no health checks, no advanced features
client, err := connectplugin.NewClient(
    connectplugin.ClientConfig{
        Endpoint: "http://plugin:8080",
        Plugins: connectplugin.PluginSet{
            "kv": &kvplugin.KVServicePlugin{},
        },
    },
    nil, // Use default options
)
if err != nil {
    return err
}

// Connection is lazy - happens on first use
kvStore := connectplugin.MustDispenseTyped[kv.KVStore](client, "kv")
```

**What you get:**
- 30s request timeout (total time including any retry attempts)
- Single attempt (no retries)
- No health monitoring
- Lazy connection (connects on first plugin use)

### With Basic Retry (Production-Ready)

```go
// Add simple retry for transient failures
client, err := connectplugin.NewClient(
    connectplugin.ClientConfig{
        Endpoint: "http://plugin:8080",
        Plugins: connectplugin.PluginSet{
            "kv": &kvplugin.KVServicePlugin{},
        },
    },
    &connectplugin.ClientOptions{
        RetryPolicy: &connectplugin.RetryPolicy{
            MaxAttempts:  2,                       // 1 initial + 1 retry
            InitialDelay: 100 * time.Millisecond,  // Wait before retry
        },
    },
)
```

**What you get:**
- 30s request timeout (shared across both attempts)
- 2 attempts total (1 initial + 1 retry)
- 100ms delay before retry
- Exponential backoff (2x multiplier by default)
- Retries on: Unavailable, DeadlineExceeded, ResourceExhausted

**Timeout behavior:**
- With RequestTimeout=30s and MaxAttempts=2:
  - First attempt: up to ~15s
  - Retry delay: 100ms
  - Second attempt: remaining time (~14.9s)

### With Discovery

```go
// Dynamic endpoint resolution in Kubernetes
client, err := connectplugin.NewClient(
    connectplugin.ClientConfig{
        Endpoint:  "kv-plugin", // Service name, not URL
        Discovery: connectplugin.NewKubernetesDiscovery(clientset, "default"),
        Plugins: connectplugin.PluginSet{
            "kv": &kvplugin.KVServicePlugin{},
        },
    },
    nil,
)
```

### With Health Monitoring (Opt-In)

```go
// Enable background health checks (disabled by default)
client, err := connectplugin.NewClient(
    connectplugin.ClientConfig{
        Endpoint: "http://plugin:8080",
        Plugins: connectplugin.PluginSet{
            "kv": &kvplugin.KVServicePlugin{},
        },
    },
    &connectplugin.ClientOptions{
        HealthCheckInterval: 30 * time.Second, // Enable monitoring
        OnHealthChange: func(status connectplugin.HealthStatus) {
            log.Printf("Plugin health: %v", status)
        },
    },
)
```

**Health monitoring cost:**
- Background goroutine running
- Health check RPC every 30s (5s timeout each)
- Only enable if you need proactive monitoring

### Full Production Setup (All Features)

```go
// Production configuration with all resilience features
client, err := connectplugin.NewClient(
    connectplugin.ClientConfig{
        Endpoint: "http://plugin:8080",
        Plugins: connectplugin.PluginSet{
            "kv": &kvplugin.KVServicePlugin{},
        },
    },
    &connectplugin.ClientOptions{
        // Retry configuration
        RetryPolicy: &connectplugin.RetryPolicy{
            MaxAttempts:  2,
            InitialDelay: 100 * time.Millisecond,
            MaxDelay:     2 * time.Second,
        },

        // Circuit breaker (fail fast when plugin is down)
        CircuitBreaker: &connectplugin.CircuitBreakerConfig{
            FailureThreshold: 5,
            SuccessThreshold: 2,
            Timeout:          30 * time.Second,
        },

        // Health monitoring (optional)
        HealthCheckInterval: 30 * time.Second,

        // Observability
        Interceptors: []connect.Interceptor{
            loggingInterceptor,
            tracingInterceptor,
        },
        Logger: logger,
    },
)
```

## Retry Policy Configuration

```go
// RetryPolicy configures automatic retry behavior.
// Retries are opt-in - set MaxAttempts > 1 to enable.
type RetryPolicy struct {
    // MaxAttempts is the maximum number of attempts (including initial).
    // 1 = no retries (just initial attempt)
    // 2 = one retry (recommended for production)
    // 3+ = multiple retries (use sparingly)
    // Default: 1 (no retries)
    MaxAttempts int

    // InitialDelay before first retry.
    // Default: 100ms
    InitialDelay time.Duration

    // MaxDelay caps retry backoff.
    // Default: 2 seconds
    MaxDelay time.Duration

    // Multiplier for exponential backoff.
    // Default: 2.0
    Multiplier float64

    // Jitter adds randomness to backoff.
    // Default: 0.1 (10%)
    Jitter float64

    // RetryableErrors are Connect error codes to retry.
    // Default: [Unavailable, DeadlineExceeded, ResourceExhausted]
    RetryableErrors []connect.Code

    // OnRetry callback before each retry attempt.
    OnRetry func(attempt int, err error)
}

// Validate checks RetryPolicy for errors.
func (p *RetryPolicy) Validate() error {
    if p.MaxAttempts < 1 {
        return errors.New("MaxAttempts must be >= 1")
    }
    if p.MaxAttempts > 5 {
        return errors.New("MaxAttempts > 5 is not recommended (adds significant latency)")
    }
    return nil
}
```

**Retry Defaults Rationale:**

- **MaxAttempts: 1** (no retries) by default to avoid hidden latency
  - Users must explicitly opt into retries
  - Recommended: 2 for production (adds ~100ms overhead on transient failures)

- **InitialDelay: 100ms** - Quick retry without overwhelming the plugin

- **MaxDelay: 2s** - Reduced from 5s to keep total timeout reasonable
  - With 2 attempts: 100ms + 200ms = 300ms total retry overhead
  - With 3 attempts: 100ms + 200ms + 400ms = 700ms total retry overhead

## Circuit Breaker Configuration

```go
// CircuitBreakerConfig configures circuit breaker behavior.
type CircuitBreakerConfig struct {
    // FailureThreshold consecutive failures to open circuit.
    // Default: 5
    FailureThreshold int

    // SuccessThreshold consecutive successes to close circuit.
    // Default: 2
    SuccessThreshold int

    // Timeout while circuit is open before trying half-open.
    // Default: 30 seconds
    Timeout time.Duration

    // HalfOpenMaxAttempts while half-open before re-opening.
    // Default: 1
    HalfOpenMaxAttempts int

    // OnStateChange callback when circuit state changes.
    OnStateChange func(from, to CircuitState)
}

// CircuitState represents circuit breaker states.
type CircuitState int

const (
    CircuitClosed CircuitState = iota
    CircuitOpen
    CircuitHalfOpen
)
```

## Connection Establishment Flow

### Lazy Connection (Default)

```
┌─────────────┐
│  NewClient  │  Creates client, validates config only
└──────┬──────┘
       │
       ▼
┌─────────────┐
│ Dispense()  │  First plugin use triggers connection
└──────┬──────┘
       │
       ▼
┌──────────────────────────────────────────────────────────┐
│                    Connect()                              │
│                                                           │
│  1. Discover endpoint (if using discovery)                │
│     ┌─────────────────────────────────────────────┐      │
│     │ Discovery.Discover(ctx, serviceName)        │      │
│     └─────────────────────────────────────────────┘      │
│                                                           │
│  2. Perform handshake                                     │
│     ┌─────────────────────────────────────────────┐      │
│     │ HandshakeService.Handshake(...)             │      │
│     │ - Validate magic cookie                     │      │
│     │ - Negotiate protocol versions               │      │
│     │ - Discover available plugins                │      │
│     │ - Exchange capabilities (if configured)     │      │
│     └─────────────────────────────────────────────┘      │
│                                                           │
│  3. Start health monitoring (if HealthCheckInterval > 0)  │
│     ┌─────────────────────────────────────────────┐      │
│     │ go healthMonitor.Start()                    │      │
│     └─────────────────────────────────────────────┘      │
│                                                           │
│  4. Start endpoint watcher (if using discovery)           │
│     ┌─────────────────────────────────────────────┐      │
│     │ go watchEndpoints()                         │      │
│     └─────────────────────────────────────────────┘      │
│                                                           │
└───────────────────────────────────────────────────────────┘
       │
       ▼
┌─────────────┐
│   Ready     │  Plugin client returned
└─────────────┘
```

**Note:** Handshake is always performed (SkipHandshake option removed for simplicity and safety).

## Interceptor Composition

Interceptors are applied in order, with built-in retry and circuit breaker interceptors added automatically:

```go
client := connectplugin.NewClient(&connectplugin.ClientConfig{
    Endpoint: "http://plugin:8080",
    Plugins:  pluginSet,

    // User interceptors (outermost)
    Interceptors: []connect.Interceptor{
        loggingInterceptor,
        tracingInterceptor,
        metricsInterceptor,
    },

    // Built-in interceptors (added automatically, innermost)
    RetryPolicy:    retryPolicy,     // → RetryInterceptor
    CircuitBreaker: circuitBreaker,  // → CircuitBreakerInterceptor
})
```

**Interceptor chain (outermost to innermost):**
```
loggingInterceptor
  → tracingInterceptor
    → metricsInterceptor
      → retryInterceptor (if RetryPolicy set)
        → circuitBreakerInterceptor (if CircuitBreaker set)
          → transport
```

## Health Monitoring Integration

Health monitoring is **opt-in** (disabled by default). Enable it by setting `HealthCheckInterval > 0`.

```go
// Health monitoring disabled by default
client, err := connectplugin.NewClient(
    connectplugin.ClientConfig{
        Endpoint: "http://plugin:8080",
        Plugins:  pluginSet,
    },
    nil, // HealthCheckInterval defaults to 0
)

// Explicitly enable health monitoring
client, err := connectplugin.NewClient(
    connectplugin.ClientConfig{
        Endpoint: "http://plugin:8080",
        Plugins:  pluginSet,
    },
    &connectplugin.ClientOptions{
        HealthCheckInterval: 30 * time.Second, // Enable monitoring
        OnHealthChange: func(status connectplugin.HealthStatus) {
            log.Printf("Plugin health: %v", status)
            if status == connectplugin.HealthStatusUnhealthy {
                // Optionally trigger circuit breaker
                client.CircuitBreaker().Trip()
            }
        },
    },
)
```

**Why opt-in?**
- Background goroutine cost
- Additional RPC every 30s (adds network traffic)
- Most applications prefer request-time failure detection via retries
- Useful for long-running connections with infrequent requests

**Health check flow:**
```go
type healthMonitor struct {
    client   *Client
    interval time.Duration
    timeout  time.Duration
}

func (m *healthMonitor) Start(ctx context.Context) {
    // Only start if interval > 0
    if m.interval == 0 {
        return
    }

    ticker := time.NewTicker(m.interval)
    defer ticker.Stop()

    for {
        select {
        case <-ctx.Done():
            return
        case <-ticker.C:
            m.check(ctx)
        }
    }
}

func (m *healthMonitor) check(ctx context.Context) {
    ctx, cancel := context.WithTimeout(ctx, m.timeout)
    defer cancel()

    resp, err := m.client.healthClient.Check(ctx, &HealthCheckRequest{})

    var newStatus HealthStatus
    if err != nil || resp.Status != HealthCheckResponse_SERVING {
        newStatus = HealthStatusUnhealthy
    } else {
        newStatus = HealthStatusHealthy
    }

    if newStatus != m.lastStatus {
        m.lastStatus = newStatus
        if m.client.opts.OnHealthChange != nil {
            m.client.opts.OnHealthChange(newStatus)
        }
    }
}
```

## Discovery Integration

When using discovery, the client watches for endpoint changes:

```go
client := connectplugin.NewClient(&connectplugin.ClientConfig{
    Endpoint: "kv-plugin", // Service name, not URL
    Discovery: discoveryService,
})

// Connection flow with discovery:
// 1. Discover(ctx, "kv-plugin") → initial endpoints
// 2. Watch(ctx, "kv-plugin") → endpoint updates
// 3. On update: refresh connections, re-handshake if needed
```

**Endpoint watching:**
```go
func (c *Client) watchEndpoints(ctx context.Context) {
    watch, err := c.cfg.Discovery.Watch(ctx, c.serviceName)
    if err != nil {
        c.logger.Error("failed to start endpoint watch", "error", err)
        return
    }

    for {
        select {
        case <-ctx.Done():
            return
        case endpoints, ok := <-watch:
            if !ok {
                return
            }
            c.updateEndpoints(endpoints)
        }
    }
}

func (c *Client) updateEndpoints(endpoints []Endpoint) {
    c.mu.Lock()
    defer c.mu.Unlock()

    // Filter to ready endpoints
    ready := filterReady(endpoints)
    if len(ready) == 0 {
        c.logger.Warn("no ready endpoints available")
        return
    }

    // Update endpoint list
    c.endpoints = ready

    // Optionally re-handshake if primary endpoint changed
    if c.endpoints[0].URL != c.currentEndpoint {
        c.currentEndpoint = c.endpoints[0].URL
        go c.refreshHandshake(context.Background())
    }
}
```

## Client Lifecycle

```go
// Client lifecycle states
type Client struct {
    cfg   *ClientConfig
    state clientState
    mu    sync.RWMutex
}

type clientState int

const (
    stateCreated clientState = iota
    stateConnecting
    stateConnected
    stateClosed
)

// Connect establishes the connection (called explicitly or lazily).
func (c *Client) Connect(ctx context.Context) error {
    c.mu.Lock()
    if c.state == stateConnected {
        c.mu.Unlock()
        return nil // Already connected
    }
    if c.state == stateConnecting {
        c.mu.Unlock()
        return ErrAlreadyConnecting
    }
    c.state = stateConnecting
    c.mu.Unlock()

    defer func() {
        c.mu.Lock()
        if c.state == stateConnecting {
            c.state = stateConnected
        }
        c.mu.Unlock()
    }()

    return c.doConnect(ctx)
}

// Close gracefully shuts down the client.
func (c *Client) Close() error {
    c.mu.Lock()
    if c.state == stateClosed {
        c.mu.Unlock()
        return nil
    }
    c.state = stateClosed
    c.mu.Unlock()

    // Stop health monitoring
    if c.healthMonitor != nil {
        c.healthMonitor.Stop()
    }

    // Stop endpoint watcher
    if c.endpointCancel != nil {
        c.endpointCancel()
    }

    return nil
}
```

## Configuration Validation

```go
// Validate checks ClientConfig for errors.
func (cfg *ClientConfig) Validate() error {
    if cfg.Endpoint == "" {
        return errors.New("Endpoint is required")
    }

    if cfg.Plugins == nil {
        return errors.New("Plugins is required")
    }

    if len(cfg.Plugins) == 0 {
        return errors.New("Plugins must contain at least one plugin")
    }

    return nil
}

// Validate checks ClientOptions for errors.
func (opts *ClientOptions) Validate() error {
    if opts.RetryPolicy != nil {
        if err := opts.RetryPolicy.Validate(); err != nil {
            return fmt.Errorf("invalid retry policy: %w", err)
        }
    }

    if opts.CircuitBreaker != nil {
        if err := opts.CircuitBreaker.Validate(); err != nil {
            return fmt.Errorf("invalid circuit breaker: %w", err)
        }
    }

    if opts.HealthCheckInterval < 0 {
        return errors.New("HealthCheckInterval cannot be negative")
    }

    if opts.HealthCheckTimeout <= 0 && opts.HealthCheckInterval > 0 {
        return errors.New("HealthCheckTimeout must be > 0 when HealthCheckInterval is enabled")
    }

    if opts.RequestTimeout <= 0 {
        return errors.New("RequestTimeout must be > 0")
    }

    // Validate VersionedPlugins vs Plugins consistency
    if opts.VersionedPlugins != nil && len(opts.VersionedPlugins) == 0 {
        return errors.New("VersionedPlugins must contain at least one version if set")
    }

    return nil
}
```

## Timeout Interaction Clarification

Understanding how timeouts interact with retries is critical:

```go
// Example: RequestTimeout with Retries
client, err := connectplugin.NewClient(
    connectplugin.ClientConfig{
        Endpoint: "http://plugin:8080",
        Plugins:  pluginSet,
    },
    &connectplugin.ClientOptions{
        RequestTimeout: 10 * time.Second, // Total time budget
        RetryPolicy: &connectplugin.RetryPolicy{
            MaxAttempts:  2,                      // 1 initial + 1 retry
            InitialDelay: 100 * time.Millisecond,
        },
    },
)
```

**How it works:**

1. **RequestTimeout** is the total time budget for the entire request, including all retry attempts
2. Each retry attempt gets a proportional share of the remaining time
3. The retry interceptor respects context deadlines

**Example timeline (10s timeout, 2 attempts):**
```
t=0s:     First attempt starts
t=5s:     First attempt fails (Unavailable)
t=5.1s:   Wait 100ms (InitialDelay)
t=5.1s:   Second attempt starts with ~4.9s remaining
t=8s:     Second attempt succeeds
Total:    8s
```

**Example timeline (timeout exceeded):**
```
t=0s:     First attempt starts
t=9s:     First attempt fails (Unavailable)
t=9.1s:   Wait 100ms (InitialDelay)
t=9.2s:   Second attempt starts with ~0.8s remaining
t=10s:    Second attempt times out (DeadlineExceeded)
Total:    10s (timeout)
```

**Per-request timeout override:**
```go
// Override timeout for a specific request
ctx, cancel := context.WithTimeout(context.Background(), 5 * time.Second)
defer cancel()

// This request has 5s total (overrides default 10s)
err := kvStore.Set(ctx, "key", "value")
```

**Recommendation:**
- Keep `RequestTimeout` reasonable (30s default)
- Use `MaxAttempts=2` for production (adds ~100-200ms overhead)
- For critical paths, use per-request context timeouts

## Comparison with go-plugin

| Aspect | go-plugin | connect-plugin (simple) | connect-plugin (advanced) |
|--------|-----------|------------------------|---------------------------|
| **Config Fields** | ~10 required | 2-3 required | 2-3 required + options |
| **Endpoint** | Subprocess command | URL | URL + discovery |
| **Connection** | Eager (subprocess) | Lazy (default) | Lazy (default) |
| **Discovery** | N/A | N/A | Pluggable (DNS, K8s) |
| **Retry** | None | Opt-in (MaxAttempts > 1) | Opt-in with backoff |
| **Circuit Breaker** | None | None | Optional |
| **Health Checks** | Basic ping | None (use retries) | Opt-in continuous monitoring |
| **TLS** | AutoMTLS | TLS config | TLS config |
| **Timeouts** | Per-RPC context | 30s default | Configurable |

## Implementation Checklist

- [x] ClientConfig structure (minimal 2-3 fields)
- [x] ClientOptions structure (all optional)
- [x] Default configuration with opt-in features
- [x] Retry policy configuration (MaxAttempts=1 default, recommend 2)
- [x] Circuit breaker configuration (disabled by default)
- [x] Health monitoring (opt-in, disabled by default)
- [x] Connection establishment flow (handshake always performed)
- [x] Lazy connection (default behavior)
- [x] Discovery integration
- [x] Interceptor composition
- [x] Configuration validation
- [x] Timeout interaction documentation
- [x] Simple vs advanced examples

## Review Feedback Addressed

✅ **Too many config options**: Split into ClientConfig (2-3 fields) + ClientOptions (all optional)
✅ **Retry defaults too aggressive**: MaxAttempts=1 by default, recommend 2 (not 3)
✅ **Health monitoring should be opt-in**: HealthCheckInterval=0 by default
✅ **Dual config confusing**: VersionedPlugins moved to Options, Plugins in Config
✅ **Timeout interaction unclear**: Added detailed documentation with examples
✅ **SkipHandshake removed**: Handshake always performed for safety

## Summary: Simple Case vs Advanced Case

### Simple Case (90% of users)

```go
client, err := connectplugin.NewClient(
    connectplugin.ClientConfig{
        Endpoint: "http://plugin:8080",
        Plugins: connectplugin.PluginSet{
            "kv": &kvplugin.KVServicePlugin{},
        },
    },
    nil,
)
```

**What you get:**
- 2-3 lines of code
- 30s request timeout
- Single attempt (no retries, no hidden latency)
- No health monitoring (no background goroutines)
- Lazy connection (connects on first use)

### Advanced Case (production with retries)

```go
client, err := connectplugin.NewClient(
    connectplugin.ClientConfig{
        Endpoint: "http://plugin:8080",
        Plugins:  pluginSet,
    },
    &connectplugin.ClientOptions{
        RetryPolicy: &connectplugin.RetryPolicy{
            MaxAttempts:  2, // 1 initial + 1 retry
            InitialDelay: 100 * time.Millisecond,
        },
    },
)
```

**What you get:**
- Simple case + retries
- ~100-200ms overhead on transient failures
- Still no health monitoring (use retries instead)

### Full Production Case (all features)

```go
client, err := connectplugin.NewClient(
    connectplugin.ClientConfig{
        Endpoint: "http://plugin:8080",
        Plugins:  pluginSet,
    },
    &connectplugin.ClientOptions{
        RetryPolicy:         retryPolicy,
        CircuitBreaker:      circuitBreaker,
        HealthCheckInterval: 30 * time.Second, // Opt-in
        Interceptors:        interceptors,
    },
)
```

**What you get:**
- Everything explicitly configured
- Background health monitoring (you opted in)
- Circuit breaker (fail fast when plugin is down)
- Custom interceptors (logging, tracing, etc.)

## Next Steps

1. Implement ClientConfig and ClientOptions in `client.go`
2. Implement connection establishment logic with lazy connection
3. Implement retry interceptor with opt-in behavior
4. Implement circuit breaker interceptor (disabled by default)
5. Implement health monitoring with opt-in behavior
6. Design server configuration (KOR-koba)
