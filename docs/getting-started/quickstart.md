# Quick Start

Get up and running with connect-plugin-go in 5 minutes.

## Prerequisites

- Go 1.21+
- `protoc` (Protocol Buffer compiler)
- `task` (Task runner) - `brew install go-task/tap/go-task`

## Installation

```bash
go get github.com/masegraye/connect-plugin-go
```

## 1. Define Your Plugin Interface

Create a proto file defining your plugin service:

```protobuf
// proto/kv.proto
syntax = "proto3";

package kv.v1;

service KVService {
  rpc Get(GetRequest) returns (GetResponse);
  rpc Set(SetRequest) returns (SetResponse);
}

message GetRequest {
  string key = 1;
}

message GetResponse {
  string value = 1;
}

message SetRequest {
  string key = 1;
  string value = 2;
}

message SetResponse {}
```

## 2. Generate Plugin Code

Install code generators:

```bash
task install-deps
```

This installs:
- `protoc-gen-go` - Go code generator
- `protoc-gen-connect-go` - Connect RPC generator
- `protoc-gen-connect-plugin` - Plugin wrapper generator

Generate code:

```bash
protoc --proto_path=proto \
  --go_out=gen --go_opt=paths=source_relative \
  --connect-go_out=gen --connect-go_opt=paths=source_relative \
  --connect-plugin_out=gen --connect-plugin_opt=paths=source_relative \
  proto/kv.proto
```

This generates:
- `gen/kv.pb.go` - Protobuf types
- `gen/kvconnect/kv.connect.go` - Connect RPC client/server
- `gen/kvplugin/plugin.go` - Plugin wrapper

## 3. Implement the Plugin

```go
// plugin/impl.go
package main

import (
    "context"
    "sync"
)

type KVStore struct {
    mu   sync.RWMutex
    data map[string]string
}

func (s *KVStore) Get(ctx context.Context, req *kv.GetRequest) (*kv.GetResponse, error) {
    s.mu.RLock()
    defer s.mu.RUnlock()

    return &kv.GetResponse{
        Value: s.data[req.Key],
    }, nil
}

func (s *KVStore) Set(ctx context.Context, req *kv.SetRequest) (*kv.SetResponse, error) {
    s.mu.Lock()
    defer s.mu.Unlock()

    s.data[req.Key] = req.Value
    return &kv.SetResponse{}, nil
}
```

## 4. Serve the Plugin

```go
// plugin/main.go
package main

import (
    "log"

    connectplugin "github.com/masegraye/connect-plugin-go"
    "github.com/yourorg/yourapp/gen/kvplugin"
)

func main() {
    server := connectplugin.Serve(&connectplugin.ServeConfig{
        Plugins: connectplugin.PluginSet{
            "kv": &kvplugin.KVServicePlugin{},
        },
        Impls: map[string]any{
            "kv": &KVStore{data: make(map[string]string)},
        },
    })

    log.Println("KV plugin serving on :8080")
    server.Wait()
}
```

Run the plugin:

```bash
go run ./plugin
```

## 5. Use the Plugin from Host

```go
// host/main.go
package main

import (
    "context"
    "fmt"
    "log"

    connectplugin "github.com/masegraye/connect-plugin-go"
    kv "github.com/yourorg/yourapp/gen"
    "github.com/yourorg/yourapp/gen/kvplugin"
)

func main() {
    // Connect to plugin
    client, err := connectplugin.NewClient(connectplugin.ClientConfig{
        Endpoint: "http://localhost:8080",
        Plugins: connectplugin.PluginSet{
            "kv": &kvplugin.KVServicePlugin{},
        },
    })
    if err != nil {
        log.Fatal(err)
    }
    defer client.Close()

    // Get strongly-typed plugin instance
    kvStore := connectplugin.MustDispenseTyped[kv.KVServiceClient](client, "kv")

    // Use the plugin
    ctx := context.Background()

    // Set a value
    _, err = kvStore.Set(ctx, &kv.SetRequest{
        Key:   "greeting",
        Value: "Hello, World!",
    })
    if err != nil {
        log.Fatal(err)
    }

    // Get a value
    resp, err := kvStore.Get(ctx, &kv.GetRequest{
        Key: "greeting",
    })
    if err != nil {
        log.Fatal(err)
    }

    fmt.Println(resp.Value) // Output: Hello, World!
}
```

## What's Next?

You now have a working plugin system! Explore more:

- **[Deployment Models](deployment-models.md)**: Learn about platform-managed vs self-registering plugins
- **[Service Registry](../guides/service-registry.md)**: Enable plugin-to-plugin communication
- **[Interceptors](../guides/interceptors.md)**: Add retry, circuit breaker, auth
- **[Configuration Reference](../reference/configuration.md)**: Full config options

## Common Patterns

### Health Checking

Add health reporting to your plugin:

```go
// Plugin reports its health
client.ReportHealth(ctx,
    connectpluginv1.HealthState_HEALTH_STATE_HEALTHY,
    "all systems operational",
    nil,
)
```

### Service Discovery

Use discovery instead of hardcoded endpoints:

```go
discovery := connectplugin.NewStaticDiscovery(map[string][]connectplugin.Endpoint{
    "plugin-host": {
        {URL: "http://localhost:8080", Weight: 100},
    },
})

client, err := connectplugin.NewClient(connectplugin.ClientConfig{
    Discovery:            discovery,
    DiscoveryServiceName: "plugin-host",
    Plugins:              pluginSet,
})
```

### Retry and Circuit Breaker

Add reliability interceptors:

```go
cb := connectplugin.NewCircuitBreaker(connectplugin.DefaultCircuitBreakerConfig())
retryPolicy := connectplugin.DefaultRetryPolicy()

// Interceptors are applied in order: auth → circuit breaker → retry
interceptors := []connect.UnaryInterceptorFunc{
    auth.ClientInterceptor(),
    connectplugin.CircuitBreakerInterceptor(cb),
    connectplugin.RetryInterceptor(retryPolicy),
}
```

### Authentication

Secure plugin communication:

```go
// Token-based auth
auth := connectplugin.NewTokenAuth("my-secret-token", validateFunc)

// Client side
client, _ := connectplugin.NewClient(connectplugin.ClientConfig{
    Endpoint: "https://plugin.example.com",
    // Add auth via interceptor
})

// Server side
server := connectplugin.Serve(&connectplugin.ServeConfig{
    // Add auth interceptor to validate incoming requests
})
```

## Testing

The repository includes comprehensive examples in `examples/`:

- `examples/kv/` - Complete KV plugin implementation
- `examples/logger-plugin/` - Simple logger service provider
- `examples/cache-plugin/` - Cache with logger dependency
- `examples/app-plugin/` - Application with cache dependency

Run examples:

```bash
# Terminal 1: Start plugin server
task example:server

# Terminal 2: Run client
task example:client
```

## Running Tests

```bash
# Unit tests only
task test

# Integration tests (requires built examples)
task test:integration

# All tests
task test:all
```

## Building

```bash
# Build library
task build

# Build example binaries
task build-examples

# Clean build artifacts
task clean
```
