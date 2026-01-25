# Spike: Capability-Based Dynamic Dispatch Patterns

**Issue:** KOR-adry
**Status:** Complete

## Executive Summary

Capability-based dispatch enables host applications to provide services to plugins at runtime. Unlike static interfaces, capabilities are dynamically granted, can be revoked, and follow the principle of least privilege. For connect-plugin, this pattern is essential for bidirectional communication where the host provides services (logging, storage, callbacks) to plugins.

## Core Concepts

### Object-Capability Model (OCap)

The object-capability model treats capabilities as unforgeable references:

1. **Capabilities are references**: A capability is a reference to an object/service that grants authority
2. **No ambient authority**: Plugins can only access what they're explicitly given
3. **Delegation**: Capabilities can be passed between parties
4. **Attenuation**: Derived capabilities can have reduced authority
5. **Unforgeable**: Capabilities cannot be guessed or manufactured

### Capability vs Permission

| Aspect | Permission-Based | Capability-Based |
|--------|-----------------|------------------|
| Check | "Am I allowed to do X?" | "Do I have a reference to X?" |
| Grant | Modify ACL | Pass reference |
| Revoke | Modify ACL | Revoke reference |
| Principle | Identity-based | Possession-based |

## Patterns in Existing Systems

### Pattern 1: go-plugin GRPCBroker

go-plugin uses broker IDs as capabilities:

```go
// Host grants capability by starting server on broker ID
brokerID := broker.NextId()
go broker.AcceptAndServe(brokerID, func(opts []grpc.ServerOption) *grpc.Server {
    s := grpc.NewServer(opts...)
    proto.RegisterAddHelperServer(s, &AddHelperServer{Impl: helper})
    return s
})

// Pass capability (broker ID) in request
client.Put(ctx, &PutRequest{
    AddServer: brokerID,  // Capability reference
    Key:       key,
    Value:     value,
})
```

**Characteristics:**
- Capability = uint32 broker ID
- Ephemeral (valid only for this call)
- No security (anyone with ID can dial)
- Process-local optimization

### Pattern 2: WASI Capabilities

WASI uses explicit capability grants for sandbox resources:

```rust
// Host grants filesystem capability
let wasi = WasiCtxBuilder::new()
    .preopened_dir("/data", ".", DirPerms::read(), FilePerms::read())
    .build();

// Plugin receives capability via fd
let fd = wasi_snapshot_preview1::path_open(
    dir_fd,      // Capability reference
    flags,
    path,
    oflags,
    rights,
    rights_inheriting,
    fdflags
);
```

**Characteristics:**
- Capability = file descriptor + rights
- Pre-opened directories define scope
- Fine-grained (read/write/seek)
- Kernel-enforced

### Pattern 3: Service Capability URLs

Modern cloud systems use URLs with embedded tokens:

```
https://api.example.com/capabilities/abc123?token=jwt...
```

**Characteristics:**
- Capability = URL + bearer token
- Network-accessible
- Time-limited (JWT expiry)
- Revocable (token invalidation)

### Pattern 4: Context Injection (Go)

Common Go pattern for passing capabilities:

```go
type PluginContext struct {
    Logger    Logger
    Storage   Storage
    Config    ConfigProvider
    Metrics   MetricsRecorder
}

func (p *MyPlugin) Execute(ctx PluginContext) error {
    ctx.Logger.Info("executing")
    data, _ := ctx.Storage.Get("key")
    // ...
}
```

**Characteristics:**
- Capability = interface value
- Compile-time type safety
- In-process only
- Not network-friendly

## Connect-Plugin Capability Design

### Goals

1. **Network-friendly**: Capabilities work across process/container boundaries
2. **Secure**: Capabilities include authentication
3. **Dynamic**: Grant/revoke at runtime
4. **Type-safe**: Strong typing for capability interfaces
5. **Discoverable**: Plugins can query available capabilities

### Proposed Architecture

```
┌─────────────────────────────────────────────────────────────────────┐
│                              Host                                    │
│  ┌─────────────────┐    ┌──────────────────────────────────────┐   │
│  │ Capability      │    │ Host Services                        │   │
│  │ Registry        │    │ ┌─────────────┐ ┌─────────────────┐  │   │
│  │                 │───▶│ │   Logger    │ │   KV Store      │  │   │
│  │ - logger        │    │ │   Service   │ │   Service       │  │   │
│  │ - storage       │    │ └─────────────┘ └─────────────────┘  │   │
│  │ - metrics       │    │ ┌─────────────┐ ┌─────────────────┐  │   │
│  │                 │    │ │  Callback   │ │   Metrics       │  │   │
│  └────────┬────────┘    │ │  Handler    │ │   Recorder      │  │   │
│           │             │ └─────────────┘ └─────────────────┘  │   │
│           │             └──────────────────────────────────────┘   │
│           │                                                         │
│  ┌────────▼────────────────────────────────────────────────────┐   │
│  │               Capability Broker                              │   │
│  │  - Generates capability grants                               │   │
│  │  - Routes incoming capability calls                          │   │
│  │  - Validates tokens                                          │   │
│  │  - Tracks active capabilities                                │   │
│  └────────┬────────────────────────────────────────────────────┘   │
└───────────┼─────────────────────────────────────────────────────────┘
            │
       HTTP/Connect
            │
┌───────────▼─────────────────────────────────────────────────────────┐
│                              Plugin                                  │
│  ┌──────────────────────────────────────────────────────────────┐   │
│  │               Capability Client                               │   │
│  │  - Holds capability grants                                    │   │
│  │  - Creates typed clients from grants                          │   │
│  │  - Refreshes expiring capabilities                            │   │
│  └────────┬────────────────────────────────────────────────────┘    │
│           │                                                          │
│  ┌────────▼────────┐    ┌─────────────────────────────────────┐    │
│  │ Logger Client   │    │ Plugin Implementation               │    │
│  │ (from cap)      │───▶│ - Uses logger, storage, etc.        │    │
│  └─────────────────┘    │ - Invokes callbacks                 │    │
│                         └─────────────────────────────────────┘    │
└─────────────────────────────────────────────────────────────────────┘
```

### Capability Grant Protocol

```protobuf
// connectplugin.broker.v1/capability.proto

message CapabilityGrant {
    string capability_id = 1;      // Unique ID for this grant
    string capability_type = 2;    // e.g., "logger", "storage", "callback"
    string endpoint_url = 3;       // URL to call
    string bearer_token = 4;       // JWT for authentication
    google.protobuf.Timestamp expires_at = 5;
    map<string, string> metadata = 6;  // Type-specific metadata
}

message RequestCapabilitiesRequest {
    repeated string required = 1;   // Required capability types
    repeated string optional = 2;   // Optional capability types
}

message RequestCapabilitiesResponse {
    repeated CapabilityGrant grants = 1;
    repeated string unavailable = 2;  // Types that couldn't be granted
}

service CapabilityBroker {
    // Plugin requests capabilities from host
    rpc RequestCapabilities(RequestCapabilitiesRequest) returns (RequestCapabilitiesResponse);

    // Plugin can release capabilities early
    rpc ReleaseCapabilities(ReleaseCapabilitiesRequest) returns (ReleaseCapabilitiesResponse);

    // Host can push capability updates
    rpc CapabilityUpdates(stream CapabilityUpdateRequest) returns (stream CapabilityUpdateResponse);
}
```

### Capability Types

#### Built-in Capabilities

```protobuf
// connectplugin.capabilities.v1/logger.proto
service Logger {
    rpc Log(LogRequest) returns (LogResponse);
}

message LogRequest {
    string level = 1;     // debug, info, warn, error
    string message = 2;
    map<string, string> fields = 3;
}

// connectplugin.capabilities.v1/metrics.proto
service Metrics {
    rpc RecordCounter(CounterRequest) returns (MetricsResponse);
    rpc RecordGauge(GaugeRequest) returns (MetricsResponse);
    rpc RecordHistogram(HistogramRequest) returns (MetricsResponse);
}

// connectplugin.capabilities.v1/storage.proto
service Storage {
    rpc Get(GetRequest) returns (GetResponse);
    rpc Put(PutRequest) returns (PutResponse);
    rpc Delete(DeleteRequest) returns (DeleteResponse);
    rpc List(ListRequest) returns (stream ListResponse);
}
```

#### Custom Capabilities

Host applications define custom capabilities:

```protobuf
// myapp.capabilities.v1/notification.proto
service Notification {
    rpc Send(SendNotificationRequest) returns (SendNotificationResponse);
    rpc Subscribe(SubscribeRequest) returns (stream NotificationEvent);
}
```

### Go API Design

#### Host Side: Registering Capabilities

```go
// Host registers capability handlers
broker := connectplugin.NewCapabilityBroker()

// Register built-in logger capability
broker.RegisterCapability("logger", &LoggerCapabilityHandler{
    Logger: slog.Default(),
})

// Register custom capability
broker.RegisterCapability("notification", &NotificationCapabilityHandler{
    Service: notificationService,
})

// Create plugin client with capability broker
client := connectplugin.NewClient(pluginURL,
    connectplugin.WithCapabilityBroker(broker),
)
```

#### Plugin Side: Requesting Capabilities

```go
// Plugin requests capabilities during startup
func (p *MyPlugin) Init(ctx context.Context, broker CapabilityClient) error {
    // Request required capabilities
    grants, err := broker.RequestCapabilities(ctx, &RequestCapabilitiesRequest{
        Required: []string{"logger"},
        Optional: []string{"metrics", "storage"},
    })
    if err != nil {
        return err
    }

    // Create typed clients from grants
    for _, grant := range grants.Grants {
        switch grant.CapabilityType {
        case "logger":
            p.logger = NewLoggerClient(grant)
        case "metrics":
            p.metrics = NewMetricsClient(grant)
        case "storage":
            p.storage = NewStorageClient(grant)
        }
    }

    return nil
}
```

#### Plugin Side: Using Capabilities

```go
func (p *MyPlugin) Process(ctx context.Context, req *ProcessRequest) (*ProcessResponse, error) {
    // Use logger capability
    p.logger.Info(ctx, "processing request", map[string]string{
        "request_id": req.Id,
    })

    // Use storage capability
    data, err := p.storage.Get(ctx, &GetRequest{Key: req.Key})
    if err != nil {
        p.logger.Error(ctx, "storage error", map[string]string{
            "error": err.Error(),
        })
        return nil, err
    }

    return &ProcessResponse{Data: data}, nil
}
```

### Callback Pattern (Ephemeral Capabilities)

For request-scoped callbacks:

```go
// Host side: Pass callback as ephemeral capability
func (h *HostHandler) ProcessWithCallback(ctx context.Context, req *ProcessRequest) (*ProcessResponse, error) {
    // Create ephemeral capability for this request
    callbackGrant := h.broker.CreateEphemeralCapability("callback", &CallbackHandler{
        OnProgress: func(progress int) {
            h.notifyClient(req.ClientId, progress)
        },
    })
    defer h.broker.RevokeCapability(callbackGrant.CapabilityId)

    // Call plugin with callback capability
    return h.pluginClient.Process(ctx, &ProcessRequest{
        Data:             req.Data,
        CallbackCapability: callbackGrant,
    })
}

// Plugin side: Use callback capability
func (p *MyPlugin) Process(ctx context.Context, req *ProcessRequest) (*ProcessResponse, error) {
    callback := NewCallbackClient(req.CallbackCapability)

    for i := 0; i < 100; i++ {
        // Do work...
        callback.OnProgress(ctx, &ProgressRequest{Percent: i})
    }

    return &ProcessResponse{}, nil
}
```

### Security Model

#### Token Structure

```json
{
    "iss": "connect-plugin-host",
    "sub": "plugin-instance-123",
    "cap": {
        "id": "cap-456",
        "type": "logger",
        "permissions": ["log:info", "log:warn", "log:error"]
    },
    "exp": 1706054400,
    "iat": 1706050800
}
```

#### Validation Chain

1. **Token signature**: Verify JWT signature
2. **Expiration**: Check token hasn't expired
3. **Capability ID**: Validate capability still active
4. **Permissions**: Check operation is allowed
5. **Rate limit**: Apply per-capability rate limits

### Routing Architecture

```go
// CapabilityRouter routes incoming requests to registered handlers
type CapabilityRouter struct {
    handlers map[string]CapabilityHandler
    tokens   TokenValidator
}

func (r *CapabilityRouter) ServeHTTP(w http.ResponseWriter, req *http.Request) {
    // Extract capability ID from path: /capabilities/{capability_id}/{method}
    capID := extractCapabilityID(req.URL.Path)

    // Validate token
    token := extractBearerToken(req)
    claims, err := r.tokens.Validate(token)
    if err != nil {
        http.Error(w, "unauthorized", http.StatusUnauthorized)
        return
    }

    // Check capability ID matches
    if claims.CapabilityID != capID {
        http.Error(w, "forbidden", http.StatusForbidden)
        return
    }

    // Route to handler
    handler, ok := r.handlers[claims.CapabilityType]
    if !ok {
        http.Error(w, "not found", http.StatusNotFound)
        return
    }

    handler.ServeHTTP(w, req)
}
```

### Lifecycle Management

```
┌───────────────┐     ┌───────────────┐     ┌───────────────┐
│   Created     │────▶│    Active     │────▶│   Revoked     │
└───────────────┘     └───────┬───────┘     └───────────────┘
                              │
                              ▼
                      ┌───────────────┐
                      │   Expired     │
                      └───────────────┘
```

**States:**
- **Created**: Capability grant issued, not yet used
- **Active**: Capability in use, valid token
- **Expired**: Token TTL exceeded, must refresh
- **Revoked**: Explicitly revoked by host

**Refresh Pattern:**
```go
// Plugin monitors capability expiry
go func() {
    for {
        select {
        case <-ctx.Done():
            return
        case <-time.After(p.logger.ExpiresIn() / 2):
            // Refresh before expiry
            newGrant, err := broker.RefreshCapability(ctx, p.logger.CapabilityID())
            if err != nil {
                p.handleCapabilityLoss("logger", err)
                continue
            }
            p.logger.UpdateGrant(newGrant)
        }
    }
}()
```

## Comparison with Alternatives

| Approach | Security | Network | Complexity | Type Safety |
|----------|----------|---------|------------|-------------|
| Broker ID (go-plugin) | Low | No | Low | Low |
| Context Injection | N/A | No | Low | High |
| Capability URLs | High | Yes | Medium | Medium |
| gRPC Reflection | Medium | Yes | High | Low |

## Conclusions

1. **Capability URLs with JWT tokens** provide the best balance for connect-plugin
2. **Built-in capabilities** (logger, metrics, storage) cover common use cases
3. **Ephemeral capabilities** enable request-scoped callbacks
4. **Token-based auth** enables security without complex PKI
5. **Typed clients** from grants provide good developer experience

## Next Steps

1. Design `connectplugin.broker.v1` proto for capability exchange
2. Implement CapabilityBroker on host side
3. Implement CapabilityClient on plugin side
4. Add built-in capability types (logger, metrics, storage)
5. Add capability refresh and revocation
