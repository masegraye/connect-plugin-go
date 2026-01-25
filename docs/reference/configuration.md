# Configuration Reference

Complete reference for all configuration options in connect-plugin-go.

## ClientConfig

Configuration for plugin clients (host applications):

```go
type ClientConfig struct {
    // Endpoint is the plugin service URL (required if no Discovery)
    Endpoint string

    // HostURL is an alias for Endpoint (Phase 2 naming)
    HostURL string

    // Plugins defines available plugin types (optional for Phase 2 service providers)
    Plugins PluginSet

    // ProtocolVersion is the application protocol version (default: 1)
    ProtocolVersion int

    // MagicCookieKey and Value for validation (default: DefaultMagicCookieKey/Value)
    MagicCookieKey   string
    MagicCookieValue string

    // Phase 2: SelfID is the plugin's self-declared identity
    SelfID string

    // Phase 2: SelfVersion is the plugin's version
    SelfVersion string

    // Phase 2: Metadata describes services provided/required
    Metadata PluginMetadata

    // Discovery service for dynamic endpoint discovery (optional)
    Discovery DiscoveryService

    // DiscoveryServiceName is the service to discover (default: "plugin-host")
    DiscoveryServiceName string
}
```

### Examples

**Basic client (Phase 1):**

```go
client, _ := connectplugin.NewClient(connectplugin.ClientConfig{
    Endpoint: "http://localhost:8080",
    Plugins: connectplugin.PluginSet{
        "kv": &kvplugin.KVServicePlugin{},
    },
})
```

**Service provider (Phase 2, Model B):**

```go
client, _ := connectplugin.NewClient(connectplugin.ClientConfig{
    HostURL:     "http://localhost:8080",
    SelfID:      "cache-plugin",
    SelfVersion: "1.0.0",
    Metadata: connectplugin.PluginMetadata{
        Name:    "Cache",
        Version: "1.0.0",
        Provides: []ServiceDeclaration{
            {Type: "cache", Version: "1.0.0", Path: "/cache.v1.Cache/"},
        },
        Requires: []ServiceDependency{
            {Type: "logger", MinVersion: "1.0.0", RequiredForStartup: true},
        },
    },
})
```

**With discovery:**

```go
discovery := connectplugin.NewStaticDiscovery(endpoints)

client, _ := connectplugin.NewClient(connectplugin.ClientConfig{
    Discovery:            discovery,
    DiscoveryServiceName: "plugin-host",
    Plugins:              pluginSet,
})
```

## ServeConfig

Configuration for plugin servers:

```go
type ServeConfig struct {
    // Plugins defines plugin types this server provides (required)
    Plugins PluginSet

    // Impls maps plugin names to implementations (required)
    Impls map[string]any

    // ProtocolVersion is the application protocol version (default: 1)
    ProtocolVersion int

    // MagicCookieKey and Value for validation
    MagicCookieKey   string
    MagicCookieValue string

    // Addr is the address to listen on (default: ":8080")
    Addr string

    // Capabilities are host-provided services (optional)
    Capabilities map[string]*Capability

    // CapabilityHandlers are HTTP handlers for capabilities (optional)
    CapabilityHandlers map[string]http.Handler

    // Phase 2: LifecycleService for health reporting (optional)
    LifecycleService *LifecycleServer

    // Phase 2: ServiceRegistry for service registration (optional)
    ServiceRegistry *ServiceRegistry

    // Phase 2: ServiceRouter for plugin-to-plugin routing (optional)
    ServiceRouter *ServiceRouter
}
```

### Examples

**Basic server (Phase 1):**

```go
server := connectplugin.Serve(&connectplugin.ServeConfig{
    Plugins: connectplugin.PluginSet{
        "kv": &kvplugin.KVServicePlugin{},
    },
    Impls: map[string]any{
        "kv": &MyKVStore{},
    },
})

server.Wait()
```

**With host capabilities:**

```go
server := connectplugin.Serve(&connectplugin.ServeConfig{
    Plugins: pluginSet,
    Impls:   impls,
    Capabilities: map[string]*connectplugin.Capability{
        "logger": {
            Type:     "logger",
            Version:  "1.0.0",
            Endpoint: "/capabilities/logger",
        },
    },
    CapabilityHandlers: map[string]http.Handler{
        "logger": loggerHandler,
    },
    Addr: ":8080",
})
```

**Phase 2 host platform:**

```go
handshake := connectplugin.NewHandshakeServer(&connectplugin.ServeConfig{})
lifecycle := connectplugin.NewLifecycleServer()
registry := connectplugin.NewServiceRegistry(lifecycle)
router := connectplugin.NewServiceRouter(handshake, registry, lifecycle)

mux := http.NewServeMux()
mux.Handle(connectpluginv1connect.NewHandshakeServiceHandler(handshake))
mux.Handle(connectpluginv1connect.NewPluginLifecycleHandler(lifecycle))
mux.Handle(connectpluginv1connect.NewServiceRegistryHandler(registry))
mux.Handle("/services/", router)

http.ListenAndServe(":8080", mux)
```

## PluginMetadata

Metadata describing plugin services (Phase 2):

```go
type PluginMetadata struct {
    Name    string                // Display name
    Path    string                // Service path
    Version string                // Plugin version

    // Phase 2: Services provided
    Provides []ServiceDeclaration

    // Phase 2: Services required
    Requires []ServiceDependency
}

type ServiceDeclaration struct {
    Type    string  // Service type (e.g., "logger", "cache")
    Version string  // Service version (semver)
    Path    string  // Endpoint path (e.g., "/logger.v1.Logger/")
}

type ServiceDependency struct {
    Type               string  // Service type required
    MinVersion         string  // Minimum version (semver)
    RequiredForStartup bool    // Block startup if unavailable?
    WatchForChanges    bool    // Subscribe to state changes?
}
```

## RetryPolicy

Retry configuration:

```go
type RetryPolicy struct {
    MaxAttempts       int           // Max attempts (default: 3)
    InitialBackoff    time.Duration // First retry delay (default: 100ms)
    MaxBackoff        time.Duration // Max backoff (default: 10s)
    BackoffMultiplier float64       // Exponential multiplier (default: 2.0)
    Jitter            bool          // Add randomness (default: true)
    IsRetryable       func(error) bool  // Custom retry logic
}
```

**Defaults:**

```go
policy := connectplugin.DefaultRetryPolicy()
// MaxAttempts: 3
// InitialBackoff: 100ms
// MaxBackoff: 10s
// BackoffMultiplier: 2.0
// Jitter: true
```

## CircuitBreakerConfig

Circuit breaker configuration:

```go
type CircuitBreakerConfig struct {
    FailureThreshold int           // Consecutive failures to open (default: 5)
    SuccessThreshold int           // Consecutive successes to close (default: 2)
    Timeout          time.Duration // Open duration before half-open (default: 10s)
    IsFailure        func(error) bool  // Custom failure detection
    OnStateChange    func(CircuitState, CircuitState)  // State change callback
}
```

**Defaults:**

```go
config := connectplugin.DefaultCircuitBreakerConfig()
// FailureThreshold: 5
// SuccessThreshold: 2
// Timeout: 10s
```

## PluginConfig

Configuration for Platform.AddPlugin() (Model A):

```go
type PluginConfig struct {
    SelfID      string         // Plugin's self-declared ID
    SelfVersion string         // Plugin's version
    Endpoint    string         // Plugin's HTTP endpoint
    Metadata    PluginMetadata // Service declarations
}
```

## Environment Variables

Standard environment variables for plugins:

| Variable | Description | Example |
|----------|-------------|---------|
| `PORT` | Plugin listen port | `8082` |
| `HOST_URL` | Host platform URL (Model B) | `http://localhost:8080` |
| `ENV` | Environment name | `production` |
| `LOG_LEVEL` | Logging level | `info` |

**Deployment model detection:**

```go
hostURL := os.Getenv("HOST_URL")
if hostURL == "" {
    // Model A: Platform-managed
} else {
    // Model B: Self-registering
}
```

## Selection Strategies

Host-controlled provider selection (Phase 2):

```go
type SelectionStrategy int

const (
    SelectionFirst      // Always first provider
    SelectionRoundRobin // Rotate through providers
    SelectionRandom     // Random selection
    SelectionWeighted   // Based on load/health (future)
)

// Usage:
registry.SetSelectionStrategy("logger", connectplugin.SelectionRoundRobin)
```

## Health States

Three-state health model (Phase 2):

```go
type HealthState int32

const (
    HEALTH_STATE_HEALTHY   // Fully functional, route traffic
    HEALTH_STATE_DEGRADED  // Limited functionality, still route traffic
    HEALTH_STATE_UNHEALTHY // Cannot serve, do not route
)
```

## Default Values

Summary of all defaults:

| Config | Field | Default |
|--------|-------|---------|
| ClientConfig | ProtocolVersion | 1 |
| ClientConfig | MagicCookieKey | `CONNECT_PLUGIN` |
| ClientConfig | MagicCookieValue | `d3e5f7a9b1c2` |
| ClientConfig | DiscoveryServiceName | `plugin-host` |
| ServeConfig | Addr | `:8080` |
| ServeConfig | ProtocolVersion | 1 |
| RetryPolicy | MaxAttempts | 3 |
| RetryPolicy | InitialBackoff | 100ms |
| RetryPolicy | MaxBackoff | 10s |
| RetryPolicy | BackoffMultiplier | 2.0 |
| RetryPolicy | Jitter | true |
| CircuitBreakerConfig | FailureThreshold | 5 |
| CircuitBreakerConfig | SuccessThreshold | 2 |
| CircuitBreakerConfig | Timeout | 10s |

## Next Steps

- [API Reference](api.md) - Detailed API documentation
- [Proto Definitions](proto.md) - Protocol buffer reference
- [Migration Guide](../migration/from-go-plugin.md) - Migrate from go-plugin
