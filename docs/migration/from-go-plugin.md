# Migration from go-plugin

Guide for migrating from HashiCorp's `go-plugin` to `connect-plugin-go`.

## Why Migrate?

| Feature | go-plugin | connect-plugin-go |
|---------|-----------|-------------------|
| **Protocol** | gRPC (custom framing) | HTTP/2 (Connect RPC) |
| **Network mode** | Optional (default local) | Always network-based |
| **Type safety** | `interface{}` casting | Generated typed clients |
| **Service discovery** | Not built-in | Service Registry |
| **Health checking** | Basic ping | Three-state model |
| **Dependencies** | Manual | Dependency graph |
| **Hot reload** | Manual | Platform.ReplacePlugin() |
| **Deployment** | Primarily local | Local or distributed |

## Conceptual Mapping

### go-plugin Concepts

```go
// go-plugin: Define interface
type KV interface {
    Get(key string) (string, error)
    Set(key, value string) error
}

// Implement gRPC plugin
type KVGRPCPlugin struct {
    Impl KV
}

func (p *KVGRPCPlugin) GRPCServer(broker *plugin.GRPCBroker, s *grpc.Server) error {
    proto.RegisterKVServer(s, &GRPCServer{Impl: p.Impl})
    return nil
}

func (p *KVGRPCPlugin) GRPCClient(ctx context.Context, broker *plugin.GRPCBroker, c *grpc.ClientConn) (interface{}, error) {
    return &GRPCClient{client: proto.NewKVClient(c)}, nil
}
```

### connect-plugin-go Equivalent

```go
// connect-plugin-go: Define in proto
service KVService {
    rpc Get(GetRequest) returns (GetResponse);
    rpc Set(SetRequest) returns (SetResponse);
}

// Generate plugin wrapper (automatic)
// protoc --connect-plugin_out=gen proto/kv.proto

// Use generated plugin
plugin := &kvplugin.KVServicePlugin{}
```

**Key difference:** connect-plugin-go generates the plugin wrapper from proto, eliminating manual gRPC boilerplate.

## Migration Steps

### 1. Convert Interface to Proto

**Before (go-plugin):**

```go
type Greeter interface {
    Greet(name string) string
}
```

**After (connect-plugin-go):**

```protobuf
service GreeterService {
    rpc Greet(GreetRequest) returns (GreetResponse);
}

message GreetRequest {
    string name = 1;
}

message GreetResponse {
    string greeting = 1;
}
```

### 2. Implement Service Handler

**Before (go-plugin):**

```go
type GreeterImpl struct{}

func (g *GreeterImpl) Greet(name string) string {
    return "Hello, " + name
}
```

**After (connect-plugin-go):**

```go
type GreeterImpl struct{}

func (g *GreeterImpl) Greet(
    ctx context.Context,
    req *connect.Request[greeter.GreetRequest],
) (*connect.Response[greeter.GreetResponse], error) {
    return connect.NewResponse(&greeter.GreetResponse{
        Greeting: "Hello, " + req.Msg.Name,
    }), nil
}
```

**Changes:**

- Add `context.Context` parameter
- Use Connect request/response wrappers
- Return errors instead of panicking

### 3. Update Server Code

**Before (go-plugin):**

```go
plugin.Serve(&plugin.ServeConfig{
    HandshakeConfig: handshakeConfig,
    Plugins: map[string]plugin.Plugin{
        "greeter": &GreeterGRPCPlugin{Impl: &GreeterImpl{}},
    },
    GRPCServer: plugin.DefaultGRPCServer,
})
```

**After (connect-plugin-go):**

```go
connectplugin.Serve(&connectplugin.ServeConfig{
    Plugins: connectplugin.PluginSet{
        "greeter": &greeterplugin.GreeterServicePlugin{},
    },
    Impls: map[string]any{
        "greeter": &GreeterImpl{},
    },
})
```

**Changes:**

- No handshake config (built-in)
- No gRPC server config (uses Connect)
- Use generated plugin wrapper
- Simpler, less boilerplate

### 4. Update Client Code

**Before (go-plugin):**

```go
client := plugin.NewClient(&plugin.ClientConfig{
    HandshakeConfig: handshakeConfig,
    Plugins: map[string]plugin.Plugin{
        "greeter": &GreeterGRPCPlugin{},
    },
    Cmd: exec.Command("./greeter-plugin"),
})
defer client.Kill()

rpcClient, _ := client.Client()
raw, _ := rpcClient.Dispense("greeter")
greeter := raw.(Greeter)

greeting := greeter.Greet("World")
```

**After (connect-plugin-go):**

```go
client, _ := connectplugin.NewClient(connectplugin.ClientConfig{
    Endpoint: "http://localhost:8080",
    Plugins: connectplugin.PluginSet{
        "greeter": &greeterplugin.GreeterServicePlugin{},
    },
})
defer client.Close()

greeter := connectplugin.MustDispenseTyped[greeter.GreeterServiceClient](client, "greeter")

resp, _ := greeter.Greet(context.Background(), &greeter.GreetRequest{
    Name: "World",
})
greeting := resp.Msg.Greeting
```

**Changes:**

- No process management (plugins are separate processes)
- Network endpoint instead of Cmd
- Typed dispense (no casting)
- Context required for all calls

## Feature Migration

### Health Checking

**Before (go-plugin):**

```go
// Basic ping/ping protocol
_, err := rpcClient.Ping()
```

**After (connect-plugin-go):**

```go
// Three-state health model
client.ReportHealth(ctx,
    connectpluginv1.HealthState_HEALTH_STATE_HEALTHY,
    "all systems operational",
    nil,
)

// Host checks health
state := lifecycle.GetHealthState(runtimeID)
shouldRoute := lifecycle.ShouldRouteTraffic(runtimeID)
```

### Bidirectional Communication

**Before (go-plugin):**

```go
// GRPCBroker for bidirectional calls
broker := plugin.GRPCBroker

// Plugin acquires host capability
conn, _ := broker.Dial(capabilityID)
loggerClient := proto.NewLoggerClient(conn)
```

**After (connect-plugin-go):**

```go
// Host provides capabilities
Capabilities: map[string]*Capability{
    "logger": {
        Type:     "logger",
        Endpoint: "/capabilities/logger",
    },
}

// Plugin requests capability
cap, _ := client.RequestCapability(ctx, "logger")
loggerClient := loggerv1connect.NewLoggerClient(httpClient, cap.Endpoint)
```

Or use Service Registry registry:

```go
// Plugin discovers service from registry
endpoint, _ := regClient.DiscoverService(ctx, &DiscoverServiceRequest{
    ServiceType: "logger",
})

loggerClient := loggerv1connect.NewLoggerClient(httpClient, endpoint.EndpointUrl)
```

### Version Negotiation

**Before (go-plugin):**

```go
HandshakeConfig: plugin.HandshakeConfig{
    ProtocolVersion:  1,
    MagicCookieKey:   "MY_PLUGIN",
    MagicCookieValue: "secretvalue",
}
```

**After (connect-plugin-go):**

```go
ClientConfig{
    ProtocolVersion:  1,  // Optional, defaults to 1
    MagicCookieKey:   "MY_PLUGIN",  // Optional
    MagicCookieValue: "secretvalue", // Optional
}
```

**Changes:**

- Defaults provided (usually don't need to set)
- Validated during handshake
- Magic cookie for basic validation (not security)

## Breaking Changes

### Process Management

**go-plugin:** Host manages plugin processes

```go
Cmd: exec.Command("./plugin"),
```

**connect-plugin-go:** Plugins are independent processes

```
# Start plugin separately:
./plugin-server &

# Or use docker-compose, kubernetes, etc.
```

**Migration:** Use systemd, docker-compose, or Kubernetes to manage processes.

### Synchronous to Context-Based

**go-plugin:** Synchronous calls

```go
result := plugin.SomeMethod(arg)
```

**connect-plugin-go:** Context-based calls

```go
resp, err := plugin.SomeMethod(ctx, &Request{Arg: arg})
result := resp.Msg.Result
```

**Migration:** Add `context.Context` to all plugin methods.

### Type Assertions

**go-plugin:** Manual casting

```go
raw, _ := rpcClient.Dispense("greeter")
greeter := raw.(Greeter)  // Runtime type assertion
```

**connect-plugin-go:** Compile-time type safety

```go
greeter := connectplugin.MustDispenseTyped[GreeterServiceClient](client, "greeter")
// No casting needed, compile-time type check
```

## Common Patterns

### Plugin Discovery

**go-plugin:** Fixed set of plugins

```go
Plugins: map[string]plugin.Plugin{
    "kv": &KVPlugin{},
    "auth": &AuthPlugin{},
}
```

**connect-plugin-go:** Dynamic service discovery

```go
// Service Registry: Discover available services at runtime
services, _ := regClient.ListServices(ctx)

for _, svc := range services {
    endpoint, _ := regClient.DiscoverService(ctx, svc.Type)
    // Connect to discovered service
}
```

### Error Handling

**go-plugin:** Errors via return values or panic

```go
value, err := kv.Get(key)
if err != nil {
    return err
}
```

**connect-plugin-go:** Connect errors with codes

```go
resp, err := kv.Get(ctx, &GetRequest{Key: key})
if err != nil {
    code := connect.CodeOf(err)
    switch code {
    case connect.CodeNotFound:
        // Handle not found
    case connect.CodeUnavailable:
        // Handle unavailable
    }
}
```

### Streaming

**go-plugin:** gRPC streams

```go
stream, _ := client.Stream(ctx)
for {
    item, err := stream.Recv()
    if err == io.EOF {
        break
    }
}
```

**connect-plugin-go:** Connect streams

```go
stream, _ := client.Stream(ctx, &StreamRequest{})
for stream.Receive() {
    item := stream.Msg()
    // Process item
}
if stream.Err() != nil {
    // Handle error
}
```

## Coexistence Strategy

Run both systems during migration:

```go
// Keep go-plugin for legacy plugins
legacyClient := plugin.NewClient(pluginConfig)
defer legacyClient.Kill()

// Use connect-plugin-go for new plugins
modernClient, _ := connectplugin.NewClient(clientConfig)
defer modernClient.Close()

// Gradually migrate plugins one at a time
```

## Migration Checklist

- [ ] Define proto files for all plugin interfaces
- [ ] Generate code with protoc-gen-connect-plugin
- [ ] Update plugin implementations to Connect handlers
- [ ] Add context.Context to all methods
- [ ] Update error handling to Connect errors
- [ ] Change client to use network endpoints (not Cmd)
- [ ] Update tests (no process management in tests)
- [ ] Deploy plugins as separate services
- [ ] Update documentation

## Example Migration

See full before/after example in `agent-workspace/migration-example/`.

## Benefits After Migration

✅ **HTTP-based**: Works with standard HTTP infrastructure
✅ **Type-safe**: No runtime casting
✅ **Service registry**: Plugin-to-plugin communication
✅ **Health model**: Graceful degradation
✅ **Dependencies**: Automatic ordering
✅ **Cloud-native**: Kubernetes-friendly
✅ **Interceptors**: Retry, circuit breaker, auth built-in

## Need Help?

- [Quick Start](../getting-started/quickstart.md) - Get started quickly
- [KV Example](../guides/kv-example.md) - Complete working example
- [Configuration Reference](../reference/configuration.md) - All config options
