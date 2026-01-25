# connect-plugin-go

A modern, remote-first plugin system for Go applications using [Connect RPC](https://connectrpc.com).

## What is connect-plugin-go?

connect-plugin-go enables you to build extensible Go applications with plugins that communicate over HTTP/2. Unlike traditional plugin systems that use Go's `plugin` package or gRPC, connect-plugin-go uses Connect RPC for:

- **Remote-first architecture**: Plugins run as separate processes or services
- **HTTP-based communication**: No special protocols, works with standard HTTP infrastructure
- **Type-safe interfaces**: Generated code ensures compile-time safety
- **Production-ready**: Built-in health checking, service discovery, and lifecycle management

## Key Features

### Phase 1: Core Plugin System
- ✅ Type-safe plugin interfaces with code generation
- ✅ Network-based plugin communication (HTTP/2)
- ✅ Handshake protocol for version negotiation
- ✅ Health checking and monitoring
- ✅ Host capability broker for bidirectional calls

### Phase 2: Service Registry
- ✅ Plugin-to-plugin service communication
- ✅ Service discovery and registration
- ✅ Multi-provider support with host-controlled selection
- ✅ Three-state health model (Healthy/Degraded/Unhealthy)
- ✅ Dependency graph and topological startup ordering
- ✅ Dynamic lifecycle (add/remove/replace plugins at runtime)
- ✅ Impact analysis for plugin removal
- ✅ Two deployment models: platform-managed and self-registering

### Reliability & Security
- ✅ Retry interceptor with exponential backoff
- ✅ Circuit breaker with state machine (Closed/Open/HalfOpen)
- ✅ Flexible authentication (Token, API Key, mTLS)
- ✅ Static endpoint discovery
- ✅ Composable interceptor chains

## Quick Example

```go
// Define your plugin interface
type KVStore interface {
    Get(ctx context.Context, key string) (string, error)
    Set(ctx context.Context, key, value string) error
}

// Plugin side: Implement and serve
type MyKVStore struct{}

func (s *MyKVStore) Get(ctx context.Context, key string) (string, error) {
    return "value", nil
}

server := connectplugin.Serve(&connectplugin.ServeConfig{
    Plugins: connectplugin.PluginSet{
        "kv": &kvv1plugin.KVServicePlugin{},
    },
    Impls: map[string]any{
        "kv": &MyKVStore{},
    },
})

// Host side: Connect and use
client, _ := connectplugin.NewClient(connectplugin.ClientConfig{
    Endpoint: "http://localhost:8080",
    Plugins: connectplugin.PluginSet{
        "kv": &kvv1plugin.KVServicePlugin{},
    },
})

kvStore := connectplugin.MustDispenseTyped[KVStore](client, "kv")
value, _ := kvStore.Get(context.Background(), "mykey")
```

## Why connect-plugin-go?

**vs go-plugin:**
- Network-based by default (no local-only mode)
- HTTP/2 instead of gRPC (better for proxies, load balancers)
- Simpler protocol (standard HTTP, no custom framing)
- Modern dependency management

**vs direct gRPC:**
- Built-in plugin patterns (handshake, versioning, health)
- Service registry for plugin-to-plugin communication
- Dependency management and startup ordering
- Code generation for type-safe interfaces

**vs microservices:**
- Unified plugin model with consistent patterns
- Built-in capability broker for host services
- Structured lifecycle management
- Type-safe interfaces with generated code

## Use Cases

- **Extensible applications**: Add plugins without recompiling the host
- **Multi-tenant systems**: Isolate tenant-specific code in plugins
- **Service mesh**: Coordinate multiple service plugins with dependencies
- **Plugin marketplaces**: Third-party plugins communicate via HTTP
- **Cloud-native deployments**: Kubernetes-based plugin orchestration

## Examples

### Docker Compose URL Shortener

Complete containerized example demonstrating **Model B (self-registering)** deployment:

```bash
cd examples/docker-compose
./setup.sh   # Build images
./run.sh     # Start services
./test.sh    # Validate end-to-end
./cleanup.sh # Stop and clean up
```

**Demonstrates:**
- 4 containerized services (host, logger, storage, api)
- Plugin-to-plugin communication (API→Storage→Logger)
- Service discovery across containers
- Health-based readiness
- Dependency graph managed by host

See [Docker Compose Guide](guides/docker-compose.md) for details.

### KV Plugin Example

Simple key-value plugin for local development:

```bash
task example:server  # Terminal 1
task example:client  # Terminal 2
```

See [KV Example Walkthrough](guides/kv-example.md).

## Next Steps

- [Quick Start Guide](getting-started/quickstart.md)
- [Deployment Models](getting-started/deployment-models.md)
- [Docker Compose Guide](guides/docker-compose.md)
- [Phase 2 Service Registry](guides/service-registry.md)

## License

MIT License - see LICENSE file for details.
