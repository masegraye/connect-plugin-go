# Design: Client Configuration & Connection Model

**Issue:** KOR-qjhn
**Status:** Complete
**Dependencies:** KOR-fzki, KOR-bpyd, KOR-munj

## Overview

The client configuration defines how a host application connects to and interacts with plugins. Unlike go-plugin which requires subprocess management, connect-plugin clients are simpler but need more network-aware configuration for discovery, resilience, and health monitoring.

## Design Goals

1. **Simple defaults**: Minimal config for common cases
2. **Network-aware**: Support discovery, retry, circuit breakers
3. **Flexible**: Allow customization without overwhelming users
4. **Lazy connection**: Don't connect until needed
5. **Explicit control**: Connection lifecycle is clear

## ClientConfig Structure

```go
// ClientConfig configures a plugin client.
type ClientConfig struct {
    // ===== Connection Target =====

    // Endpoint is the plugin service URL.
    // Supports schemes: http://, https://, dns:///, k8s:///
    // Required if Discovery is nil.
    Endpoint string

    // Discovery provides dynamic endpoint resolution.
    // If set, Endpoint is used as the service name to discover.
    Discovery DiscoveryService

    // ===== Protocol Configuration =====

    // CoreProtocolVersion is the handshake protocol version.
    // Currently must be 1 (default).
    CoreProtocolVersion int

    // AppProtocolVersions are supported plugin API versions.
    // Negotiated during handshake. First = most preferred.
    // Default: []int{1}
    AppProtocolVersions []int

    // Plugins defines available plugin types for each version.
    // Key = app protocol version.
    Plugins PluginSet

    // VersionedPlugins maps protocol versions to plugin sets.
    // Used when supporting multiple plugin API versions.
    // Overrides Plugins field if set.
    VersionedPlugins map[int]PluginSet

    // MagicCookieKey and Value for validation (not security).
    // Default: DefaultMagicCookieKey/Value
    MagicCookieKey   string
    MagicCookieValue string

    // ===== Connection Behavior =====

    // LazyConnect defers connection until first plugin use.
    // If false, Connect() establishes connection immediately.
    // Default: true
    LazyConnect bool

    // SkipHandshake bypasses handshake negotiation.
    // Only use in trusted single-version environments.
    // Default: false
    SkipHandshake bool

    // HandshakeTimeout is the max time for handshake RPC.
    // Default: 10 seconds
    HandshakeTimeout time.Duration

    // HandshakeCacheTTL is how long to cache handshake results.
    // Set to 0 to disable caching.
    // Default: 5 minutes
    HandshakeCacheTTL time.Duration

    // ===== Network & Transport =====

    // HTTPClient for making requests.
    // If nil, creates default client with reasonable settings.
    HTTPClient connect.HTTPClient

    // TLSConfig for HTTPS connections.
    // If nil and endpoint is https://, uses default TLS.
    TLSConfig *tls.Config

    // RequestTimeout is default timeout for plugin RPCs.
    // Can be overridden per-request via context.
    // Default: 30 seconds
    RequestTimeout time.Duration

    // ===== Resilience =====

    // RetryPolicy configures automatic retry behavior.
    // If nil, uses DefaultRetryPolicy.
    RetryPolicy *RetryPolicy

    // CircuitBreaker configures circuit breaker behavior.
    // If nil, circuit breaking is disabled.
    CircuitBreaker *CircuitBreakerConfig

    // ===== Health Monitoring =====

    // HealthCheckInterval for background health checks.
    // Set to 0 to disable health monitoring.
    // Default: 30 seconds
    HealthCheckInterval time.Duration

    // HealthCheckTimeout for each health check RPC.
    // Default: 5 seconds
    HealthCheckTimeout time.Duration

    // OnHealthChange callback when plugin health changes.
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
    // If nil, uses default logger.
    Logger Logger

    // ===== Metadata =====

    // ClientName identifies this client instance.
    // Sent in handshake metadata.
    ClientName string

    // ClientMetadata is custom metadata sent in handshake.
    ClientMetadata map[string]string
}
```

## Default Configuration

```go
// DefaultClientConfig returns sensible defaults.
func DefaultClientConfig() *ClientConfig {
    return &ClientConfig{
        CoreProtocolVersion:  1,
        AppProtocolVersions:  []int{1},
        MagicCookieKey:       DefaultMagicCookieKey,
        MagicCookieValue:     DefaultMagicCookieValue,
        LazyConnect:          true,
        SkipHandshake:        false,
        HandshakeTimeout:     10 * time.Second,
        HandshakeCacheTTL:    5 * time.Minute,
        RequestTimeout:       30 * time.Second,
        RetryPolicy:          DefaultRetryPolicy(),
        HealthCheckInterval:  30 * time.Second,
        HealthCheckTimeout:   5 * time.Second,
    }
}

// NewClient creates a client with defaults.
func NewClient(cfg *ClientConfig) (*Client, error) {
    // Merge with defaults
    if cfg.CoreProtocolVersion == 0 {
        cfg.CoreProtocolVersion = 1
    }
    if len(cfg.AppProtocolVersions) == 0 {
        cfg.AppProtocolVersions = []int{1}
    }
    if cfg.MagicCookieKey == "" {
        cfg.MagicCookieKey = DefaultMagicCookieKey
        cfg.MagicCookieValue = DefaultMagicCookieValue
    }
    // ... apply other defaults

    return &Client{cfg: cfg}, nil
}
```

## Minimal Configuration Examples

### Simple Case (Direct Endpoint)

```go
client := connectplugin.NewClient(&connectplugin.ClientConfig{
    Endpoint: "http://plugin:8080",
    Plugins: connectplugin.PluginSet{
        "kv": &kvplugin.KVServicePlugin{},
    },
})

client.Connect(ctx)
kvStore := connectplugin.MustDispenseTyped[kv.KVStore](client, "kv")
```

### With Discovery

```go
client := connectplugin.NewClient(&connectplugin.ClientConfig{
    Endpoint: "kv-plugin", // Service name
    Discovery: connectplugin.NewKubernetesDiscovery(
        clientset,
        "default",
    ),
    Plugins: connectplugin.PluginSet{
        "kv": &kvplugin.KVServicePlugin{},
    },
})
```

### With Resilience

```go
client := connectplugin.NewClient(&connectplugin.ClientConfig{
    Endpoint: "http://plugin:8080",
    Plugins: connectplugin.PluginSet{
        "kv": &kvplugin.KVServicePlugin{},
    },
    RetryPolicy: &connectplugin.RetryPolicy{
        MaxAttempts:  3,
        InitialDelay: 100 * time.Millisecond,
        MaxDelay:     5 * time.Second,
        Multiplier:   2.0,
    },
    CircuitBreaker: &connectplugin.CircuitBreakerConfig{
        FailureThreshold: 5,
        SuccessThreshold: 2,
        Timeout:          30 * time.Second,
    },
})
```

## Retry Policy Configuration

```go
// RetryPolicy configures automatic retry behavior.
type RetryPolicy struct {
    // MaxAttempts is the maximum number of attempts (including initial).
    // Set to 1 to disable retries.
    // Default: 3
    MaxAttempts int

    // InitialDelay before first retry.
    // Default: 100ms
    InitialDelay time.Duration

    // MaxDelay caps retry backoff.
    // Default: 5 seconds
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

// DefaultRetryPolicy returns sensible retry defaults.
func DefaultRetryPolicy() *RetryPolicy {
    return &RetryPolicy{
        MaxAttempts:  3,
        InitialDelay: 100 * time.Millisecond,
        MaxDelay:     5 * time.Second,
        Multiplier:   2.0,
        Jitter:       0.1,
        RetryableErrors: []connect.Code{
            connect.CodeUnavailable,
            connect.CodeDeadlineExceeded,
            connect.CodeResourceExhausted,
        },
    }
}
```

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
│  NewClient  │  Creates client, doesn't connect
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
│  2. Perform handshake (unless SkipHandshake)              │
│     ┌─────────────────────────────────────────────┐      │
│     │ HandshakeService.Handshake(...)             │      │
│     │ - Negotiate versions                        │      │
│     │ - Discover available plugins                │      │
│     │ - Exchange capabilities                     │      │
│     └─────────────────────────────────────────────┘      │
│                                                           │
│  3. Start health monitoring (if enabled)                  │
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

### Eager Connection

```go
client := connectplugin.NewClient(&connectplugin.ClientConfig{
    Endpoint:    "http://plugin:8080",
    LazyConnect: false,  // Connect immediately
    Plugins:     pluginSet,
})

// Connect() called automatically during NewClient()
// Returns error if connection fails

kvStore := connectplugin.MustDispenseTyped[kv.KVStore](client, "kv")
```

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

When health monitoring is enabled, the client continuously checks plugin health:

```go
client := connectplugin.NewClient(&connectplugin.ClientConfig{
    Endpoint:            "http://plugin:8080",
    HealthCheckInterval: 30 * time.Second,
    OnHealthChange: func(status HealthStatus) {
        log.Printf("Plugin health changed: %v", status)
        if status == HealthStatusUnhealthy {
            // Optionally trigger circuit breaker
            client.circuitBreaker.Trip()
        }
    },
})
```

**Health check flow:**
```go
type healthMonitor struct {
    client   *Client
    interval time.Duration
    timeout  time.Duration
}

func (m *healthMonitor) Start(ctx context.Context) {
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
        if m.client.cfg.OnHealthChange != nil {
            m.client.cfg.OnHealthChange(newStatus)
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
    if cfg.Endpoint == "" && cfg.Discovery == nil {
        return errors.New("either Endpoint or Discovery must be set")
    }

    if cfg.Plugins == nil && cfg.VersionedPlugins == nil {
        return errors.New("either Plugins or VersionedPlugins must be set")
    }

    if cfg.CoreProtocolVersion != 0 && cfg.CoreProtocolVersion != 1 {
        return fmt.Errorf("unsupported core protocol version: %d", cfg.CoreProtocolVersion)
    }

    if cfg.RetryPolicy != nil {
        if err := cfg.RetryPolicy.Validate(); err != nil {
            return fmt.Errorf("invalid retry policy: %w", err)
        }
    }

    if cfg.CircuitBreaker != nil {
        if err := cfg.CircuitBreaker.Validate(); err != nil {
            return fmt.Errorf("invalid circuit breaker: %w", err)
        }
    }

    return nil
}
```

## Comparison with go-plugin

| Aspect | go-plugin | connect-plugin |
|--------|-----------|----------------|
| **Endpoint** | Subprocess command | URL or discovery |
| **Connection** | Eager (subprocess start) | Lazy (default) or eager |
| **Discovery** | N/A | Pluggable (DNS, K8s) |
| **Retry** | None | Built-in policy |
| **Circuit Breaker** | None | Optional |
| **Health Checks** | Basic ping | Continuous monitoring |
| **TLS** | AutoMTLS | TLS config |
| **Timeouts** | Per-RPC context | Default + per-RPC |

## Implementation Checklist

- [x] ClientConfig structure
- [x] Default configuration
- [x] Retry policy configuration
- [x] Circuit breaker configuration
- [x] Connection establishment flow
- [x] Lazy vs eager connection
- [x] Health monitoring integration
- [x] Discovery integration
- [x] Interceptor composition
- [x] Configuration validation

## Next Steps

1. Implement ClientConfig in `client.go`
2. Implement connection establishment logic
3. Implement retry interceptor
4. Implement circuit breaker interceptor
5. Design server configuration (KOR-koba)
