# connect-plugin-go

[![Go Reference](https://pkg.go.dev/badge/github.com/masegraye/connect-plugin-go.svg)](https://pkg.go.dev/github.com/masegraye/connect-plugin-go)
[![Go Report Card](https://goreportcard.com/badge/github.com/masegraye/connect-plugin-go)](https://goreportcard.com/report/github.com/masegraye/connect-plugin-go)

A production-ready plugin system for Go using [Connect RPC](https://connectrpc.com). Build extensible applications where plugins run as independent processes and communicate over HTTP/2.

## ⚠️ Security Notice

**TLS is REQUIRED for production.** Without TLS, authentication tokens are transmitted in plaintext.

```go
// Development only
Endpoint: "http://localhost:8080"

// Production
Endpoint: "https://plugin-host.example.com"
```

See [Security Guide](docs/security.md) for complete deployment guidance.

## Key Features

**Core Plugin System:**
- Type-safe interfaces with Protocol Buffer code generation
- Handshake protocol for version negotiation
- Health monitoring with three-state model (Healthy/Degraded/Unhealthy)
- Capability broker for bidirectional host↔plugin communication

**Service Registry:**
- Plugin-to-plugin service discovery and communication
- Dependency management with topological startup ordering
- Dynamic lifecycle (add/remove/replace plugins at runtime)
- Two deployment models: platform-managed and self-registering

**Security (Production-Ready):**
- Runtime identity tokens with automatic expiration (24h default)
- Capability grant tokens with TTL (1h default)
- Constant-time token comparison (timing attack resistant)
- Rate limiting with token bucket algorithm
- Service registration authorization
- Input validation and sanitization
- TLS warnings for insecure configurations

**Reliability:**
- Retry interceptor with exponential backoff
- Circuit breaker (Closed/Open/HalfOpen state machine)
- Graceful shutdown and cleanup

## Quick Start

```bash
# Install
go get github.com/masegraye/connect-plugin-go

# Clone and build examples
git clone https://github.com/masegraye/connect-plugin-go
cd connect-plugin-go
task build

# Run example (two terminals)
task example:server  # Terminal 1
task example:client  # Terminal 2
```

## Example Usage

### Plugin Server

```go
package main

import connectplugin "github.com/masegraye/connect-plugin-go"

func main() {
    connectplugin.Serve(&connectplugin.ServeConfig{
        Addr: ":8080",
        Plugins: connectplugin.PluginSet{
            "kv": &kvplugin.KVServicePlugin{},
        },
        Impls: map[string]any{
            "kv": &MyKVStore{},
        },
        HealthService: connectplugin.NewHealthServer(),
    })
}
```

### Plugin Client

```go
client, _ := connectplugin.NewClient(connectplugin.ClientConfig{
    Endpoint: "http://localhost:8080",
    Plugins:  connectplugin.PluginSet{"kv": &kvplugin.KVServicePlugin{}},
})
defer client.Close()

client.Connect(ctx)
kv := connectplugin.MustDispenseTyped[kvv1connect.KVServiceClient](client, "kv")
resp, _ := kv.Get(ctx, connect.NewRequest(&kvv1.GetRequest{Key: "hello"}))
```

### Production with Security

```go
// Rate limiting
limiter := connectplugin.NewTokenBucketLimiter()
defer limiter.Close()

// Capability broker
broker := connectplugin.NewCapabilityBroker("https://platform.example.com")
broker.RegisterCapability(loggerCapability)

connectplugin.Serve(&connectplugin.ServeConfig{
    Addr:               ":443",
    Plugins:            pluginSet,
    Impls:              impls,
    RuntimeTokenTTL:    24 * time.Hour,
    CapabilityGrantTTL: 1 * time.Hour,
    RateLimiter:        limiter,
    HealthService:      connectplugin.NewHealthServer(),
    CapabilityBroker:   broker,
})
```

## Documentation

📚 **[Complete Documentation](https://yoursite.github.io/connect-plugin-go)**

**Getting Started:**
- [Quick Start Guide](docs/getting-started/quickstart.md)
- [Deployment Models](docs/getting-started/deployment-models.md)
- [KV Example Walkthrough](docs/guides/kv-example.md)

**Security:**
- [Security Overview](docs/security.md) - Threat model, TLS setup, best practices
- [Security Best Practices](docs/guides/security-best-practices.md) - Production patterns
- [Rate Limiting Guide](docs/guides/rate-limiting.md) - DoS protection

**Guides:**
- [Service Registry](docs/guides/service-registry.md) - Plugin-to-plugin communication
- [Interceptors](docs/guides/interceptors.md) - Retry, circuit breaker, auth
- [Docker Compose](docs/guides/docker-compose.md) - Containerized deployment
- [Kubernetes](docs/guides/kubernetes.md) - Helm chart deployment
- [Performance](docs/guides/performance.md) - Benchmarking and optimization

**Reference:**
- [Configuration](docs/reference/configuration.md) - All config options
- [API Reference](docs/reference/api.md) - Complete API documentation

**Build docs locally:**
```bash
task docs:serve  # http://localhost:8000
task docs:build  # ./site/
```

## Examples

**Production Deployments:**
- [`examples/docker-compose/`](examples/docker-compose/) - Multi-service containerized app with service discovery
- [`examples/helm-chart/`](examples/helm-chart/) - Kubernetes deployment with sidecar pattern

**Local Development:**
- [`examples/kv/`](examples/kv/) - Complete key-value plugin with streaming
- [`examples/fx-managed/`](examples/fx-managed/) - Mixed in-memory and process plugins
- [`examples/logger-plugin/`](examples/logger-plugin/) - Simple logger service
- [`examples/cache-plugin/`](examples/cache-plugin/) - Cache with logger dependency

Run integration tests:
```bash
task integ:all  # Runs all integrated examples
```

## Architecture

```
┌─────────────────────────────────────────┐
│         Host Platform                   │
│  • Handshake & authentication           │
│  • Service registry & routing           │
│  • Health tracking & lifecycle          │
│  • Capability broker                    │
│  • Rate limiting & security             │
└────────┬──────────────────────┬─────────┘
         │                      │
    HTTP/2 (Connect RPC)   HTTP/2 (Connect RPC)
         │                      │
    ┌────▼─────┐          ┌─────▼────┐
    │ Plugin A │          │ Plugin B │
    │ Provides │─────────▶│ Consumes │
    │ Service  │  routed  │ Service  │
    └──────────┘  by host └──────────┘
```

## Testing

```bash
task test              # Unit tests
task test:security     # Security tests (timing attacks, expiration, auth)
task test:integration  # Integration tests with real processes
task test:all          # Everything (116 tests)
task test:coverage     # Generate coverage report
```

## Project Status

**Current Version:** 0.2.0 (Security Hardening Release)

**Production Ready:**
- ✅ Core plugin system with handshake protocol
- ✅ Service registry with dependency management
- ✅ Health tracking and lifecycle management
- ✅ Security features (token expiration, rate limiting, authorization)
- ✅ Reliability patterns (retry, circuit breaker)
- ✅ Comprehensive test suite (116 tests)

**In Development:**
- 🚧 mTLS authentication (Phase 3)
- 🚧 Distributed rate limiting
- 🚧 Audit logging

See [Phase 3 Roadmap](agent-workspace/phase-003/05-recommendations.md) for future enhancements.

## Why connect-plugin-go?

**vs hashicorp/go-plugin:**
- HTTP/2 instead of gRPC (better proxy/LB support)
- Service registry for plugin-to-plugin communication
- Modern security (token expiration, rate limiting)
- Production-ready deployment models

**vs direct Connect RPC:**
- Built-in plugin patterns (handshake, versioning, health)
- Service registry and dependency management
- Capability broker for host services
- Generated type-safe interfaces

**vs microservices:**
- Unified plugin model with consistent patterns
- Simplified service discovery
- Built-in lifecycle management
- Lower operational overhead

## Contributing

Contributions welcome! See [CLAUDE.md](CLAUDE.md) for build instructions.

## License

MIT License - see LICENSE file for details.
