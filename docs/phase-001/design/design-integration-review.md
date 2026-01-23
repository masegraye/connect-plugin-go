# Design Integration Review

**Created:** 2026-01-23
**Purpose:** Holistic validation that all component designs fit together

## Overview

This document validates that the 6 core design documents form a cohesive, implementable system. We trace through complete scenarios to verify interfaces align and no critical gaps exist.

## Component Summary

| Component | Design Doc | Key Exports |
|-----------|------------|-------------|
| **Plugin Interface** | design-gfuh | `Plugin`, `PluginSet` |
| **Handshake** | design-mbgw | `HandshakeService`, version negotiation |
| **Broker** | design-mdxm | Host capabilities, service registry |
| **Client Config** | design-qjhn | `ClientConfig`, connection lifecycle |
| **Server Config** | design-koba | `ServeConfig`, graceful shutdown |
| **Streaming** | design-uxvj | Channel/stream adapters |

## End-to-End Scenario 1: Simple KV Plugin

### Step 1: Plugin Author Defines Service

```protobuf
// kv/v1/kv.proto
service KVService {
    rpc Get(GetRequest) returns (GetResponse);
    rpc Put(PutRequest) returns (PutResponse);
}
```

Run codegen:
```bash
protoc --go_out=. --connect-go_out=. --connect-plugin_out=. kv/v1/kv.proto
```

Generates (design-uxvj, design-gfuh):
- `kv/v1/kvv1.pb.go` - Protobuf messages
- `kv/v1/kvv1connect/kv.connect.go` - Connect service
- `kv/v1/kvv1plugin/kv.connectplugin.go` - **Plugin wrapper**

### Step 2: Plugin Author Implements

```go
// plugin/main.go
type kvStore struct {
    data map[string][]byte
}

func (kv *kvStore) Get(ctx context.Context, req *GetRequest) (*GetResponse, error) {
    return &GetResponse{Value: kv.data[req.Key]}, nil
}

func (kv *kvStore) Put(ctx context.Context, req *PutRequest) (*PutResponse, error) {
    kv.data[req.Key] = req.Value
    return &PutResponse{}, nil
}
```

### Step 3: Plugin Author Serves (design-koba)

```go
func main() {
    connectplugin.Serve(&connectplugin.ServeConfig{
        Plugins: connectplugin.PluginSet{
            "kv": &kvv1plugin.KVServicePlugin{}, // Generated
        },
        Impls: map[string]any{
            "kv": &kvStore{data: make(map[string][]byte)},
        },
    })
}
```

**What happens (design-koba):**
- Creates HTTP mux
- Registers handshake service at `/connectplugin.v1.HandshakeService/Handshake`
- Registers health service at `/connectplugin.health.v1.HealthService/Check`
- Calls `kvv1plugin.KVServicePlugin.ConnectServer(&kvStore{})` → gets handler
- Registers KV handler at `/kv.v1.KVService/*`
- Starts HTTP server on `:8080`
- Sets health status to SERVING

### Step 4: Host Connects (design-qjhn)

```go
client := connectplugin.NewClient(&connectplugin.ClientConfig{
    Endpoint: "http://kv-plugin:8080",
    Plugins: connectplugin.PluginSet{
        "kv": &kvv1plugin.KVServicePlugin{},
    },
})

client.Connect(ctx)
```

**What happens (design-qjhn, design-mbgw):**
- Discovers endpoint (static: "http://kv-plugin:8080")
- Performs handshake:
  ```
  POST /connectplugin.v1.HandshakeService/Handshake
  {
    core_protocol_version: 1,
    app_protocol_versions: [1],
    magic_cookie_key: "CONNECT_PLUGIN",
    magic_cookie_value: "...",
    requested_plugins: ["kv"]
  }
  ```
- Server responds:
  ```
  {
    core_protocol_version: 1,
    app_protocol_version: 1,
    plugins: [
      {
        name: "kv",
        version: "1.0.0",
        service_paths: ["/kv.v1.KVService/"]
      }
    ]
  }
  ```
- Starts health monitor (polls `/connectplugin.health.v1.HealthService/Check` every 30s)

### Step 5: Host Uses Plugin (design-gfuh)

```go
raw, _ := client.Dispense("kv")
kvStore := raw.(kvv1connect.KVServiceClient)

// Make RPC
resp, err := kvStore.Get(ctx, connect.NewRequest(&GetRequest{Key: "hello"}))
// → POST /kv.v1.KVService/Get
```

**Flow through interceptors (design-qjhn, design-bpyd):**
```
User call
  → Logging interceptor (outermost)
    → Tracing interceptor
      → Metrics interceptor
        → Retry interceptor (if enabled)
          → Circuit breaker interceptor (if enabled)
            → HTTP transport
              → Server receives
                → Server interceptors (reverse order)
                  → Plugin implementation
```

**✅ Integration Point Validated**: Plugin interface (design-gfuh) produces handlers that work with ServeConfig (design-koba) and clients work with ClientConfig (design-qjhn).

## End-to-End Scenario 2: Plugin Requiring Host Capability

### Setup: Host with Secrets Capability (design-mdxm, design-koba)

```go
// Host side
connectplugin.Serve(&connectplugin.ServeConfig{
    Plugins: pluginSet,
    Impls:   impls,
    HostCapabilities: map[string]connectplugin.CapabilityHandler{
        "secrets": &SecretsCapability{vault: vaultClient},
    },
})
```

**What happens:**
- Server registers capability broker at `/capabilities/*`
- Broker can issue capability grants for "secrets"

### Plugin Requests Capability (design-mbgw, design-mdxm)

During handshake, plugin advertises required capabilities:

```
// In PluginInfo (design-mbgw)
{
    name: "database",
    required_capabilities: ["secrets"], // Plugin needs secrets!
}
```

Plugin requests capability grant:

```go
// Plugin side
func NewDatabasePlugin(broker CapabilityClient) (*DatabasePlugin, error) {
    // Request secrets capability
    resp, err := broker.RequestCapability(ctx, &RequestCapabilityRequest{
        CapabilityType: "secrets",
    })
    if err != nil {
        return nil, err
    }

    // Use capability grant
    secretsClient := NewSecretsClient(
        resp.Grant.EndpointUrl,
        resp.Grant.BearerToken,
    )

    dbPassword, err := secretsClient.GetSecret(ctx, "database/password")

    return &DatabasePlugin{
        db: connectToDatabase(dbPassword),
    }, nil
}
```

**Flow (design-mdxm):**
```
Plugin: POST /capabilities/request
        {capability_type: "secrets"}

Host Broker:
  1. Validates plugin is authenticated
  2. Generates capability grant with JWT token
  3. Returns grant

Plugin: POST /capabilities/secrets/grant-456/GetSecret
        Authorization: Bearer eyJ...
        {name: "database/password"}

Host Broker:
  1. Validates JWT token
  2. Extracts grant ID (grant-456)
  3. Routes to SecretsCapability handler
  4. Returns secret
```

**✅ Integration Point Validated**: Handshake (design-mbgw) advertises capabilities, broker (design-mdxm) fulfills them, server config (design-koba) registers capability handlers.

## End-to-End Scenario 3: Plugin-to-Plugin Communication

### Setup: Logger Plugin Provides Service

```go
// Logger plugin serves
connectplugin.Serve(&connectplugin.ServeConfig{
    Plugins: connectplugin.PluginSet{
        "logger": &loggerv1plugin.LoggerServicePlugin{},
    },
    Impls: map[string]any{
        "logger": &myLogger{},
    },
})
```

**On startup (design-mdxm):**
- Plugin calls `ServiceRegistry.RegisterService()`:
  ```
  {
    service_type: "logger",
    version: "1.0.0",
    endpoint_path: "/logger.v1.LoggerService/"
  }
  ```
- Host broker stores in registry

### App Plugin Discovers Logger Service

```go
// App plugin startup
func NewAppPlugin(serviceRegistry ServiceRegistryClient) (*AppPlugin, error) {
    // Discover logger service
    resp, err := serviceRegistry.DiscoverService(ctx, &DiscoverServiceRequest{
        ServiceType: "logger",
        MinVersion:  "1.0.0",
    })
    if err != nil {
        return nil, err
    }

    // Get capability grant for logger
    grant := resp.Providers[0].Grant

    // Create client using grant
    loggerClient := loggerv1connect.NewLoggerServiceClient(
        http.DefaultClient,
        grant.EndpointUrl, // Points to broker proxy
    )

    return &AppPlugin{
        logger: loggerClient,
    }, nil
}
```

**Usage:**
```go
func (p *AppPlugin) DoWork(ctx context.Context, req *WorkRequest) (*WorkResponse, error) {
    // Log via logger plugin (through host broker)
    p.logger.Log(ctx, &LogRequest{
        Level:   "info",
        Message: "doing work",
    })

    return &WorkResponse{}, nil
}
```

**Flow (design-mdxm):**
```
App Plugin: POST /capabilities/logger/grant-789/Log
            Authorization: Bearer eyJ...
  ↓
Host Broker:
  1. Validates token
  2. Looks up logger provider in registry
  3. Proxies to: http://logger-plugin:8080/logger.v1.LoggerService/Log
  ↓
Logger Plugin: Handles Log RPC
  ↓
Host Broker: Returns response to App Plugin
```

**✅ Integration Point Validated**: Service registry (design-mdxm) enables plugin-to-plugin communication, broker mediates with capability grants.

## End-to-End Scenario 4: Streaming with Channels

### Proto with Streaming (design-uxvj)

```protobuf
service WatchService {
    rpc Watch(WatchRequest) returns (stream WatchEvent);
}
```

### Generated Interface (design-uxvj, design-neyu)

```go
// Plugin author implements channel-based API
type WatchService interface {
    Watch(ctx context.Context, req *WatchRequest) (<-chan *WatchEvent, error)
}
```

### Plugin Implementation

```go
type myWatcher struct {}

func (w *myWatcher) Watch(ctx context.Context, req *WatchRequest) (<-chan *WatchEvent, error) {
    ch := make(chan *WatchEvent)

    go func() {
        defer close(ch)
        ticker := time.NewTicker(1 * time.Second)
        defer ticker.Stop()

        for {
            select {
            case <-ctx.Done():
                return
            case <-ticker.C:
                ch <- &WatchEvent{Timestamp: time.Now().Unix()}
            }
        }
    }()

    return ch, nil
}
```

### Generated Adapter (design-uxvj)

```go
// Adapter converts channel to Connect stream
func (h *watchServiceHandler) Watch(
    ctx context.Context,
    req *connect.Request[WatchRequest],
    stream *connect.ServerStream[WatchEvent],
) error {
    ch, err := h.impl.Watch(ctx, req.Msg)
    if err != nil {
        return err
    }

    // Pump channel to stream
    for {
        select {
        case <-ctx.Done():
            return ctx.Err()
        case event, ok := <-ch:
            if !ok {
                return nil // Stream complete
            }
            if err := stream.Send(event); err != nil {
                return err
            }
        }
    }
}
```

**✅ Integration Point Validated**: Streaming adapters (design-uxvj) work with plugin interface (design-gfuh) and are registered via ServeConfig (design-koba).

## End-to-End Scenario 5: Discovery & Health Integration

### Plugin Deployment (design-koba)

```go
// Plugin serves with health enabled
connectplugin.Serve(&connectplugin.ServeConfig{
    Addr:    ":8080",
    Plugins: pluginSet,
    Impls:   impls,
    // Health service enabled by default
})
```

**Endpoints available:**
- `/connectplugin.v1.HandshakeService/Handshake`
- `/connectplugin.health.v1.HealthService/Check`
- `/connectplugin.health.v1.HealthService/Watch`
- `/healthz` (liveness)
- `/readyz` (readiness)
- `/kv.v1.KVService/*` (plugin services)

### Client with Discovery & Health (design-qjhn, design-munj, design-ejeu)

```go
client := connectplugin.NewClient(&connectplugin.ClientConfig{
    Endpoint: "kv-plugin", // Service name
    Discovery: connectplugin.NewKubernetesDiscovery(
        clientset,
        "default",
    ),
    Plugins:             pluginSet,
    HealthCheckInterval: 30 * time.Second,
    CircuitBreaker: &connectplugin.CircuitBreakerConfig{
        FailureThreshold: 5,
    },
})

client.Connect(ctx)
```

**What happens:**

1. **Discovery (design-munj):**
   - `Discovery.Discover(ctx, "kv-plugin")` → queries K8s for service endpoints
   - Returns: `[{URL: "http://10.0.0.1:8080", Ready: true}]`
   - Starts watch: `Discovery.Watch(ctx, "kv-plugin")` → channel of endpoint updates

2. **Handshake (design-mbgw):**
   - `POST http://10.0.0.1:8080/connectplugin.v1.HandshakeService/Handshake`
   - Negotiates version, discovers plugins

3. **Health Monitoring (design-ejeu):**
   - Starts background goroutine
   - Polls: `POST /connectplugin.health.v1.HealthService/Check` every 30s
   - On failure: trips circuit breaker

4. **Plugin RPC (design-qjhn):**
   - Goes through interceptor chain (retry, circuit breaker)
   - Circuit breaker allows if: health OK + failure count < threshold

**✅ Integration Point Validated**: Discovery (design-munj), health checking (design-ejeu), and circuit breaker (design-qjhn) work together seamlessly.

## Cross-Cutting Concerns

### Interceptor Chain Composition

From designs: qjhn (client), koba (server), bpyd (interceptor patterns)

**Client-side chain:**
```
User code
  → Custom interceptors (design-qjhn: ClientConfig.Interceptors)
    → Retry interceptor (design-qjhn: RetryPolicy)
      → Circuit breaker interceptor (design-qjhn: CircuitBreaker)
        → HTTP transport
```

**Server-side chain:**
```
HTTP request
  → Custom interceptors (design-koba: ServeConfig.Interceptors)
    → Plugin handler (design-gfuh: Plugin.ConnectServer)
      → Implementation
```

**✅ Validated**: Interceptor composition is consistent across client and server configs.

### Lifecycle Coordination

From designs: qjhn (client lifecycle), koba (server lifecycle)

**Client lifecycle:**
```
NewClient() → [Created]
  → Connect() → [Connecting] → [Connected]
    → Dispense() → [Ready]
      → Close() → [Closed]
```

**Server lifecycle:**
```
Serve() → [Starting]
  → Listening → [Running]
    → <-StopCh → [Shutting Down]
      → Graceful Shutdown → [Stopped]
```

**Shutdown coordination:**
1. Server: Health → NOT_SERVING
2. K8s removes from endpoints (design-munj)
3. Client: Discovery sees endpoint removed
4. Client: Stops sending new requests
5. Server: Drains active requests (30s timeout, design-koba)
6. Server: Exits

**✅ Validated**: Client and server lifecycles coordinate via health and discovery.

## Dependency Flow

### Client Dependencies

```
ClientConfig (qjhn)
  → requires: Plugin interface (gfuh)
  → requires: HandshakeService (mbgw)
  → optional: Discovery (munj)
  → optional: Health monitoring (ejeu)
  → optional: Retry/CircuitBreaker (bpyd)
  → optional: CapabilityBroker (mdxm)
```

### Server Dependencies

```
ServeConfig (koba)
  → requires: Plugin interface (gfuh)
  → provides: HandshakeService (mbgw)
  → provides: HealthService (ejeu)
  → optional: HostCapabilities (mdxm)
  → optional: StreamingAdapters (uxvj)
```

**✅ Validated**: All dependencies flow correctly, no circular dependencies.

## Interface Consistency Checks

### Plugin Interface Usage

**Client side (design-gfuh):**
```go
raw, err := plugin.ConnectClient(baseURL, httpClient)
// Returns: kvv1connect.KVServiceClient
```

**Server side (design-gfuh):**
```go
path, handler, err := plugin.ConnectServer(impl)
// Returns: "/kv.v1.KVService/", http.Handler
```

**Registration (design-koba):**
```go
for name, plugin := range cfg.Plugins {
    impl := cfg.Impls[name]
    path, handler, err := plugin.ConnectServer(impl)
    mux.Handle(path, handler)
}
```

**✅ Validated**: Plugin interface works on both sides as designed.

### Handshake Integration

**Client initiates (design-qjhn):**
```go
handshakeClient := connectpluginv1connect.NewHandshakeServiceClient(
    c.httpClient,
    c.endpoint,
)
resp, err := handshakeClient.Handshake(ctx, req)
```

**Server handles (design-koba):**
```go
handshakeServer := newHandshakeServer(cfg)
path, handler := connectpluginv1connect.NewHandshakeServiceHandler(handshakeServer)
mux.Handle(path, handler)
```

**✅ Validated**: Handshake service is automatically registered by Serve() and used by Connect().

### Capability Broker Integration

**Server exposes (design-koba, design-mdxm):**
```go
if len(cfg.HostCapabilities) > 0 {
    broker := newCapabilityBroker(cfg.HostCapabilities)
    mux.Handle("/capabilities/", broker)
}
```

**Client advertises (design-qjhn, design-mbgw):**
```go
// In handshake request
client_capabilities: [
    {type: "logger", version: "1"},
]
```

**Plugin requests (design-mdxm):**
```go
grant, err := broker.RequestCapability(ctx, &RequestCapabilityRequest{
    CapabilityType: "secrets",
})
```

**✅ Validated**: Capability flow works: handshake advertises, broker fulfills, clients use.

## Gap Analysis

### Identified Gaps

1. **✅ No gaps in core flow**: Simple plugin scenario works end-to-end
2. **✅ No gaps in capability flow**: Host capabilities work as designed
3. **✅ No gaps in streaming**: Adapters bridge channels and streams
4. **✅ No gaps in resilience**: Retry, circuit breaker, health all integrated

### Design Assumptions to Validate

1. **Assumption**: Plugin.ConnectServer() can be called with nil impl for validation
   - **Status**: Valid - used in PluginSet.Validate() (design-gfuh)
   - **Note**: Need to document this behavior

2. **Assumption**: HTTP mux can handle multiple plugins on different paths
   - **Status**: Valid - standard Go http.ServeMux behavior
   - **Note**: Must validate no path conflicts in ServeConfig.Validate()

3. **Assumption**: Discovery and health work independently
   - **Status**: Valid - separate concerns, optional features
   - **Note**: Health can inform discovery (ready filtering)

4. **Assumption**: Handshake is idempotent
   - **Status**: Valid - designed as pure function of request
   - **Note**: Caching optional (design-qjhn)

**✅ All assumptions validated**

## Consistency Checks

### Error Codes

All designs use Connect error codes consistently:

| Scenario | Code | Source Design |
|----------|------|---------------|
| Invalid magic cookie | InvalidArgument | design-mbgw |
| Plugin not found | NotFound | design-gfuh |
| Version mismatch | FailedPrecondition | design-mbgw |
| Circuit open | Unavailable | design-qjhn |
| Health check fail | Unavailable | design-ejeu |

**✅ Consistent error code usage**

### Timeout Handling

All designs respect context timeouts:

| Operation | Timeout | Design |
|-----------|---------|--------|
| Handshake | 10s default | design-qjhn |
| Health check | 5s default | design-qjhn |
| Plugin RPC | 30s default | design-qjhn |
| Discovery | Per-implementation | design-munj |
| Graceful shutdown | 30s default | design-koba |

**✅ Consistent timeout patterns**

### Naming Conventions

| Entity | Pattern | Example |
|--------|---------|---------|
| Plugin name | lowercase | "kv", "auth", "logger" |
| Service path | Protobuf full name | "/kv.v1.KVService/" |
| Capability type | lowercase | "secrets", "logger" |
| Endpoint scheme | URI scheme | "http://", "k8s:///" |

**✅ Consistent naming**

## Integration with fx (design-cldj)

From spike KOR-cldj, the plugin system should integrate with fx:

```go
// Host application with fx
fx.New(
    // Provide plugin client
    fx.Provide(func() (*connectplugin.Client, error) {
        return connectplugin.NewClient(&connectplugin.ClientConfig{
            Endpoint: "kv-plugin",
            Discovery: kubernetesDiscovery,
            Plugins: pluginSet,
        })
    }),

    // Provide typed plugin
    fx.Provide(func(client *connectplugin.Client) (kv.KVStore, error) {
        return connectplugin.DispenseTyped[kv.KVStore](client, "kv")
    }),

    // Use in application
    fx.Invoke(func(store kv.KVStore) {
        store.Put(ctx, "key", []byte("value"))
    }),
).Run()
```

**✅ Validated**: Design supports fx integration via provider pattern.

## Verification Summary

All integration points validated:

✅ Plugin interface works with client/server configs
✅ Handshake integrates with connection flow
✅ Capabilities flow from handshake through broker
✅ Service registry enables plugin-to-plugin
✅ Discovery and health work together
✅ Streaming adapters bridge channels and streams
✅ Interceptors compose correctly
✅ Lifecycles coordinate on shutdown
✅ Error codes are consistent
✅ Timeouts are reasonable
✅ fx integration is natural

## Minor Refinements Needed

### 1. Plugin.ConnectServer(nil) Behavior

**Issue**: Used for validation but not documented

**Fix**: Add to design-gfuh:
```go
// ConnectServer returns a handler for this plugin.
// If impl is nil, should return path without error (for validation).
// Otherwise, impl must be the correct type.
```

### 2. PluginSet.Validate() Location

**Issue**: Used in design-gfuh but implementation in serve.go?

**Fix**: Clarify in design-koba:
```go
// Serve validates config before starting
func Serve(cfg *ServeConfig) error {
    if err := cfg.Validate(); err != nil {
        return err
    }
    // ... serve
}
```

### 3. Health Integration with Discovery

**Issue**: Ready filtering mentioned but not specified

**Fix**: Add to design-munj:
```go
func filterReady(endpoints []Endpoint) []Endpoint {
    ready := make([]Endpoint, 0, len(endpoints))
    for _, ep := range endpoints {
        if ep.Ready {
            ready = append(ready, ep)
        }
    }
    return ready
}
```

### 4. Capability Request Timing

**Issue**: When does plugin request capabilities?

**Fix**: Add to design-mdxm:
- **Option A**: During Connect() before plugin is dispensed
- **Option B**: Lazy - when plugin first requests capability
- **Recommended**: Option A (eager) for required capabilities

## Conclusion

All 6 component designs integrate correctly with only minor documentation refinements needed. The system is:

- **Cohesive**: Components reference each other correctly
- **Complete**: No critical gaps identified
- **Consistent**: Naming, errors, timeouts align
- **Implementable**: Clear path from design to code

## Next Steps

1. Apply minor refinements to design docs
2. Create proto definitions for core services
3. Begin implementation phase
