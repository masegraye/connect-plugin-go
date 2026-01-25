# connect-plugin-go

[![Go Reference](https://pkg.go.dev/badge/github.com/masegraye/connect-plugin-go.svg)](https://pkg.go.dev/github.com/masegraye/connect-plugin-go)
[![Go Report Card](https://goreportcard.com/badge/github.com/masegraye/connect-plugin-go)](https://goreportcard.com/report/github.com/masegraye/connect-plugin-go)

A modern, remote-first plugin system for Go using [Connect RPC](https://connectrpc.com).

## Features

- 🌐 **Remote-first**: Plugins run as separate processes, communicate over HTTP/2
- 🔒 **Type-safe**: Generated code from Protocol Buffers
- 🔄 **Service Registry**: Plugin-to-plugin communication with dependency management
- 🏥 **Health Tracking**: Three-state health model (Healthy/Degraded/Unhealthy)
- 🔁 **Reliability**: Built-in retry, circuit breaker, authentication interceptors
- 📊 **Observable**: Health tracking, state transitions, lifecycle events
- 🚀 **Dynamic**: Add, remove, replace plugins at runtime
- ☸️ **Cloud-Native**: Two deployment models (platform-managed, self-registering)

## Quick Start

```bash
# Install
go get github.com/masegraye/connect-plugin-go

# Build
task build

# Run example
task example:server  # Terminal 1
task example:client  # Terminal 2
```

## Example

**Define plugin (proto):**

```protobuf
service KVService {
  rpc Get(GetRequest) returns (GetResponse);
  rpc Set(SetRequest) returns (SetResponse);
}
```

**Implement plugin:**

```go
type MyKVStore struct{}

func (s *MyKVStore) Get(ctx context.Context, req *connect.Request[kv.GetRequest]) (*connect.Response[kv.GetResponse], error) {
    return connect.NewResponse(&kv.GetResponse{Value: "data"}), nil
}
```

**Serve plugin:**

```go
server := connectplugin.Serve(&connectplugin.ServeConfig{
    Plugins: connectplugin.PluginSet{"kv": &kvplugin.KVServicePlugin{}},
    Impls:   map[string]any{"kv": &MyKVStore{}},
})
```

**Use plugin:**

```go
client, _ := connectplugin.NewClient(connectplugin.ClientConfig{
    Endpoint: "http://localhost:8080",
    Plugins:  connectplugin.PluginSet{"kv": &kvplugin.KVServicePlugin{}},
})

kv := connectplugin.MustDispenseTyped[kvv1connect.KVServiceClient](client, "kv")
resp, _ := kv.Get(ctx, &kv.GetRequest{Key: "mykey"})
```

## Documentation

📚 **[Full Documentation](https://yoursite.github.io/connect-plugin-go)** (MkDocs)

Quick links:
- [Getting Started](docs/getting-started/quickstart.md)
- [Deployment Models](docs/getting-started/deployment-models.md)
- [Service Registry Guide](docs/guides/service-registry.md)
- [Interceptors Guide](docs/guides/interceptors.md)
- [Migration from go-plugin](docs/migration/from-go-plugin.md)

### Building Documentation

```bash
# Install MkDocs
pip install mkdocs-material

# Serve docs locally
mkdocs serve

# Build static site
mkdocs build
```

## Architecture

```
┌──────────────────────────────────────────────────────┐
│                   Host Platform                       │
│  ┌─────────────┐  ┌──────────────┐  ┌─────────────┐ │
│  │   Client    │  │   Platform   │  │  Registry   │ │
│  │  (Phase 1)  │  │  (Phase 2)   │  │  (Phase 2)  │ │
│  └─────────────┘  └──────────────┘  └─────────────┘ │
└───────────┬─────────────────────────────┬────────────┘
            │ HTTP/2 (Connect RPC)        │
     ┌──────┴──────┐             ┌────────┴────────┐
     │  Plugin A   │             │    Plugin B     │
     │  (KV)       │             │    (Cache)      │
     │             │             │  depends on A   │
     └─────────────┘             └─────────────────┘
```

## Phase 2: Service Registry

Plugins can provide and consume services from each other:

```go
// Cache plugin requires logger
Metadata: PluginMetadata{
    Provides: []ServiceDeclaration{
        {Type: "cache", Version: "1.0.0"},
    },
    Requires: []ServiceDependency{
        {Type: "logger", MinVersion: "1.0.0", RequiredForStartup: true},
    },
}

// Discover logger
endpoint, _ := regClient.DiscoverService(ctx, &DiscoverServiceRequest{
    ServiceType: "logger",
})

// Call logger (routed through host)
loggerClient := loggerv1connect.NewLoggerClient(httpClient, hostURL+endpoint.EndpointUrl)
loggerClient.Log(ctx, &LogRequest{Message: "Cache started"})
```

## Deployment Models

### Model A: Platform-Managed

Host orchestrates plugin lifecycle:

```go
platform.AddPlugin(ctx, PluginConfig{
    SelfID:   "cache-plugin",
    Endpoint: "http://localhost:8082",
    Metadata: metadata,
})
```

### Model B: Self-Registering

Plugins connect independently (Kubernetes, docker-compose):

```yaml
# docker-compose.yml
services:
  host:
    image: myapp/host
    ports: ["8080:8080"]

  cache:
    image: myapp/cache-plugin
    environment:
      HOST_URL: http://host:8080
```

## Reliability

```go
// Retry with exponential backoff
retryPolicy := connectplugin.DefaultRetryPolicy()

// Circuit breaker (fail fast when down)
cb := connectplugin.NewCircuitBreaker(connectplugin.DefaultCircuitBreakerConfig())

// Authentication
auth := connectplugin.NewTokenAuth("secret-token", validateFunc)

// Compose interceptors
// Order: auth → circuit breaker → retry
```

## Testing

```bash
# Unit tests
task test

# Integration tests (real plugin processes)
task test:integration

# All tests
task test:all
```

**Test coverage:** 107 unit tests + 9 integration tests

## Examples

- `examples/kv/` - Basic key-value plugin
- `examples/logger-plugin/` - Logger service provider
- `examples/cache-plugin/` - Cache with logger dependency
- `examples/app-plugin/` - Application with cache dependency

## Project Status

✅ **Phase 1 Complete:**
- Core plugin types and interfaces
- Client and server implementation
- Code generator
- Handshake protocol
- Health checking
- Capability broker

✅ **Phase 2 Complete:**
- Service registry with multi-provider support
- Plugin-to-plugin communication
- Three-state health model
- Dependency graph and impact analysis
- Dynamic lifecycle (add/remove/replace)
- Both deployment models (A and B)

✅ **Reliability & Security Complete:**
- Static discovery
- Retry interceptor
- Circuit breaker
- Flexible auth (Token, API Key, mTLS)

🚧 **Planned:**
- Kubernetes service discovery
- Metrics and tracing integration
- Admin UI for platform management

## Contributing

Contributions welcome! See `CLAUDE.md` for build instructions.

## License

MIT License - see LICENSE file for details.

## Links

- [Documentation](https://yoursite.github.io/connect-plugin-go)
- [Connect RPC](https://connectrpc.com)
- [Protocol Buffers](https://protobuf.dev)
