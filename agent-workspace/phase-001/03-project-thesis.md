# Project Thesis: connect-plugin

## A Remote-First Plugin System Using Connect RPC

### Executive Summary

This project aims to create a new Go plugin library that combines HashiCorp go-plugin's interface-oriented design with Connect RPC's network-friendly transport. The goal is to enable plugins running as sidecar containers or remote services while maintaining the ergonomic developer experience of go-plugin.

### Problem Statement

HashiCorp go-plugin is explicitly designed for **local, reliable networks**:

> "While the plugin system is over RPC, it is currently only designed to work over a local [reliable] network. Plugins over a real network are not supported and will lead to unexpected behavior."

This limitation prevents using go-plugin for:
- Plugins as sidecar containers in Kubernetes
- Plugins running on different hosts
- Plugin services with multiple instances
- Scenarios with network partitions or latency

### Vision

**From the plugin consumer's perspective, connect-plugin should look no different than go-plugin.**

```go
// Today with go-plugin (local subprocess)
client := plugin.NewClient(&plugin.ClientConfig{
    Cmd: exec.Command("./my-plugin"),
    Plugins: pluginMap,
})
raw, _ := client.Client()
kv := raw.Dispense("kv").(KV)
kv.Put("hello", []byte("world"))

// Tomorrow with connect-plugin (remote container)
client := connectplugin.NewClient(&connectplugin.ClientConfig{
    Endpoint: "http://plugin-svc:8080",  // or discovered via K8s
    Plugins: pluginMap,
})
raw, _ := client.Client()
kv := raw.Dispense("kv").(KV)
kv.Put("hello", []byte("world"))  // Same interface!
```

### Architecture Overview

```
┌─────────────────────────────────────────────────────────────────────┐
│                          HOST PROCESS                                │
│                                                                      │
│  ┌────────────────────────────────────────────────────────────────┐ │
│  │                    connect-plugin Client                        │ │
│  │  ┌──────────────┐  ┌──────────────┐  ┌────────────────────┐   │ │
│  │  │  Discovery   │  │ Health Check │  │   Circuit Breaker  │   │ │
│  │  │   Service    │  │   Monitor    │  │                    │   │ │
│  │  └──────────────┘  └──────────────┘  └────────────────────┘   │ │
│  │         │                 │                    │               │ │
│  │         └─────────────────┴────────────────────┘               │ │
│  │                           │                                     │ │
│  │  ┌────────────────────────▼─────────────────────────────────┐  │ │
│  │  │              Protocol Client (Connect RPC)                │  │ │
│  │  │   ┌─────────────────────────────────────────────────┐    │  │ │
│  │  │   │  Plugin Interface Proxy (generated or generic)  │    │  │ │
│  │  │   └─────────────────────────────────────────────────┘    │  │ │
│  │  └──────────────────────────────────────────────────────────┘  │ │
│  └────────────────────────────────────────────────────────────────┘ │
│                                                                      │
└──────────────────────────────────────┬──────────────────────────────┘
                                       │
                              Network (HTTP/1.1 or HTTP/2)
                                       │
┌──────────────────────────────────────▼──────────────────────────────┐
│                    PLUGIN (Container/Remote Service)                 │
│                                                                      │
│  ┌────────────────────────────────────────────────────────────────┐ │
│  │              Protocol Server (Connect RPC)                      │ │
│  │   ┌─────────────────────────────────────────────────────────┐  │ │
│  │   │           Plugin Implementation                          │  │ │
│  │   └─────────────────────────────────────────────────────────┘  │ │
│  │   ┌─────────────────────────────────────────────────────────┐  │ │
│  │   │           Health Service                                 │  │ │
│  │   └─────────────────────────────────────────────────────────┘  │ │
│  └────────────────────────────────────────────────────────────────┘ │
│                                                                      │
└─────────────────────────────────────────────────────────────────────┘
```

### Core Components

#### 1. Plugin Protocol Definition

```protobuf
syntax = "proto3";
package connectplugin.v1;

// Core plugin control service
service PluginController {
    // Handshake establishes plugin capabilities
    rpc Handshake(HandshakeRequest) returns (HandshakeResponse);

    // Shutdown gracefully terminates the plugin
    rpc Shutdown(ShutdownRequest) returns (ShutdownResponse);

    // Health is the health check endpoint
    rpc Health(HealthRequest) returns (HealthResponse);
}

message HandshakeRequest {
    int32 protocol_version = 1;
    repeated string requested_plugins = 2;
}

message HandshakeResponse {
    int32 protocol_version = 1;
    repeated PluginInfo available_plugins = 2;
}

message PluginInfo {
    string name = 1;
    string version = 2;
    repeated string capabilities = 3;
}
```

#### 2. Plugin Interface

```go
// Plugin is the interface implemented by all plugins
type Plugin interface {
    // Name returns the plugin name
    Name() string

    // NewClient creates a client for this plugin type
    // The client implements the user-facing interface
    NewClient(conn *connect.ClientConn) (interface{}, error)

    // RegisterServer registers the plugin implementation with the server
    RegisterServer(mux *http.ServeMux, impl interface{}) error
}
```

#### 3. Client Configuration

```go
type ClientConfig struct {
    // Endpoint is the plugin service URL
    // Can be a direct URL or a discovery scheme (e.g., "k8s://namespace/service")
    Endpoint string

    // Discovery provides plugin endpoint discovery
    // If nil, Endpoint is used directly
    Discovery DiscoveryService

    // Plugins are the plugins that can be consumed
    Plugins PluginSet

    // VersionedPlugins for protocol version negotiation
    VersionedPlugins map[int]PluginSet

    // HTTPClient is the HTTP client to use
    // If nil, http.DefaultClient is used
    HTTPClient connect.HTTPClient

    // TLSConfig for secure connections
    TLSConfig *tls.Config

    // RetryPolicy configures retry behavior
    RetryPolicy *RetryPolicy

    // CircuitBreaker configures circuit breaker behavior
    CircuitBreaker *CircuitBreakerConfig

    // HealthCheckInterval for background health monitoring
    HealthCheckInterval time.Duration

    // Interceptors for cross-cutting concerns
    Interceptors []connect.Interceptor
}
```

#### 4. Serve Configuration

```go
type ServeConfig struct {
    // Plugins to serve
    Plugins PluginSet

    // VersionedPlugins for version negotiation
    VersionedPlugins map[int]PluginSet

    // Addr to listen on (e.g., ":8080")
    Addr string

    // TLSConfig for secure connections
    TLSConfig *tls.Config

    // Logger for plugin logging
    Logger hclog.Logger

    // GracefulShutdownTimeout
    GracefulShutdownTimeout time.Duration
}
```

### Key Design Decisions

#### 1. Discovery Mechanism

Replace stdout handshake with network discovery:

```go
type DiscoveryService interface {
    // Discover returns endpoint(s) for a plugin service
    Discover(ctx context.Context, service string) ([]Endpoint, error)

    // Watch provides endpoint change notifications
    Watch(ctx context.Context, service string) (<-chan []Endpoint, error)
}

// Built-in implementations:
// - Static endpoint (direct URL)
// - Kubernetes service discovery
// - DNS SRV records
// - Consul/etcd (future)
```

#### 2. Health Checking

Continuous health monitoring vs. ping-on-demand:

```go
type HealthMonitor struct {
    // Periodic health checks
    CheckInterval time.Duration

    // Failure threshold before marking unhealthy
    FailureThreshold int

    // Success threshold before marking healthy again
    SuccessThreshold int

    // Callbacks
    OnHealthy   func(endpoint string)
    OnUnhealthy func(endpoint string, err error)
}
```

#### 3. Retry and Resilience

```go
type RetryPolicy struct {
    // MaxAttempts (0 = no retry)
    MaxAttempts int

    // InitialBackoff
    InitialBackoff time.Duration

    // MaxBackoff
    MaxBackoff time.Duration

    // BackoffMultiplier
    BackoffMultiplier float64

    // RetryableErrors codes to retry
    RetryableErrors []connect.Code
}

type CircuitBreakerConfig struct {
    // FailureThreshold to open circuit
    FailureThreshold int

    // SuccessThreshold to close circuit
    SuccessThreshold int

    // Timeout while open
    Timeout time.Duration
}
```

#### 4. Bidirectional Communication

For host-to-plugin callbacks (equivalent to GRPCBroker):

```go
type Broker interface {
    // RegisterCallback registers a callback service the plugin can call
    RegisterCallback(name string, handler http.Handler) (string, error)

    // CallbackEndpoint returns the callback endpoint for plugins
    CallbackEndpoint() string
}
```

This requires the host to also run an HTTP server that plugins can call back to.

### Phased Implementation Plan

#### Phase 1: Core Foundation (MVP)
- Basic client/server with Connect RPC
- Single-endpoint direct connection
- Handshake protocol
- Plugin interface and dispense mechanism
- Basic health checking

#### Phase 2: Resilience
- Retry interceptor
- Circuit breaker interceptor
- Connection pooling
- Graceful degradation

#### Phase 3: Discovery
- Kubernetes service discovery
- DNS-based discovery
- Endpoint watching and updates

#### Phase 4: Advanced Features
- Bidirectional broker
- Protocol versioning negotiation
- Hot plugin reload
- Multi-instance load balancing

#### Phase 5: Container Integration
- Sidecar container patterns
- Plugin container lifecycle helpers
- Kubernetes operator (optional)

### Comparison with go-plugin

| Feature | go-plugin | connect-plugin |
|---------|-----------|----------------|
| Transport | net/rpc, gRPC | Connect RPC (HTTP) |
| Network | Local only | Local or remote |
| Discovery | Subprocess stdout | Pluggable discovery |
| Health | Ping RPC | Continuous monitoring |
| Retry | None | Built-in policy |
| Circuit Breaker | None | Built-in |
| Streaming | gRPC only | All protocols |
| Browser | No | Connect protocol |
| Bidirectional | GRPCBroker | HTTP-based broker |

### Open Questions

1. **Code Generation Strategy**
   - Full generation like go-plugin examples?
   - Or runtime reflection?
   - Or hybrid approach?

2. **Backward Compatibility**
   - Support existing go-plugin protos?
   - Migration path?

3. **Security Model**
   - OAuth/JWT support?
   - mTLS only?
   - API key support?

4. **Multi-tenant Plugins**
   - One plugin serving multiple tenants?
   - Tenant isolation?

5. **Resource Limits**
   - Plugin timeout enforcement?
   - Request size limits?
   - Rate limiting?

### Success Criteria

1. **Interface Parity**: Plugin consumers use identical Go interfaces regardless of plugin location
2. **Network Resilience**: Graceful handling of network failures, latency, partitions
3. **Operational Excellence**: Easy deployment as containers, observability built-in
4. **Performance**: Comparable to go-plugin for local scenarios
5. **Simplicity**: Simple API surface, good defaults, minimal configuration

### Next Steps

1. **Prototype Phase 1** - Basic client/server with a simple KV example
2. **Validate Design** - Test with realistic network conditions (chaos engineering)
3. **Iterate** - Refine API based on prototype learnings
4. **Document** - API documentation, migration guide from go-plugin
