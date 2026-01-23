# Design: Bidirectional Broker Architecture

**Issue:** KOR-mdxm
**Status:** Complete
**Dependencies:** KOR-lfks, KOR-yosi, KOR-adry

## Overview

The broker architecture enables plugins to communicate with the host (host capabilities) and with each other (plugin services). Unlike go-plugin's GRPCBroker which only supported host→plugin callbacks, connect-plugin's broker supports:

1. **Host Capabilities**: Security-sensitive bootstrap services provided by the host
2. **Plugin Services**: Discoverable services that plugins provide to each other
3. **Service Discovery**: Plugin-to-plugin communication mediated by the host

## Key Distinction: Capabilities vs Services

### Host Capabilities (Bootstrap, Security-Sensitive)

Capabilities are provided **exclusively by the host** and grant authority to perform privileged operations:

**Examples:**
- Authentication/authorization
- Secrets management
- Host filesystem access
- Resource quotas
- Network policies
- System metrics

**Characteristics:**
- Security-sensitive (grant authority)
- Must be provided by host (cannot be delegated to plugins)
- Requested during handshake (`required_capabilities`, `optional_capabilities`)
- Granted via capability tokens (see KOR-adry)

### Plugin Services (Discoverable, Composable)

Services are functional capabilities that **any plugin can provide** and other plugins can discover:

**Examples:**
- Logging
- Metrics collection
- Caching
- Notifications
- Message queues
- Business logic services

**Characteristics:**
- Lower security impact (functional, not authoritative)
- Any plugin can provide a service
- Plugins discover services via service registry
- Enables plugin composition (Logger plugin + Cache plugin + App plugin)

### Architectural Analogy

This maps to Android's Intent system:

| Android | connect-plugin |
|---------|----------------|
| System Services (LocationManager, NotificationManager) | Host Capabilities |
| App-provided Services (Intent handlers, ContentProviders) | Plugin Services |
| Intent resolution | Service Discovery |
| Binder | Capability Broker |

## Architecture Overview

```
┌─────────────────────────────────────────────────────────────────────┐
│                              Host                                    │
│                                                                      │
│  ┌────────────────────────────────────────────────────────────────┐ │
│  │                    Capability Broker                            │ │
│  │                                                                  │ │
│  │  ┌─────────────────────┐      ┌─────────────────────────────┐  │ │
│  │  │  Host Capabilities  │      │   Service Registry          │  │ │
│  │  │  (Bootstrap)        │      │   (Plugin Services)         │  │ │
│  │  │                     │      │                             │  │ │
│  │  │  - Auth/AuthZ       │      │  "logger" -> Logger Plugin  │  │ │
│  │  │  - Secrets          │      │  "cache"  -> Cache Plugin   │  │ │
│  │  │  - Filesystem       │      │  "metrics" -> Metrics Plugin│  │ │
│  │  └─────────────────────┘      └─────────────────────────────┘  │ │
│  │                                                                  │ │
│  │  ┌──────────────────────────────────────────────────────────┐  │ │
│  │  │            Capability Grant Manager                       │  │ │
│  │  │  - Issues capability tokens (JWT)                         │  │ │
│  │  │  - Routes requests to providers                           │  │ │
│  │  │  - Validates tokens                                       │  │ │
│  │  └──────────────────────────────────────────────────────────┘  │ │
│  └────────────────────────────────────────────────────────────────┘ │
│                                                                      │
└───────────────┬──────────────────────┬───────────────────┬──────────┘
                │                      │                   │
           HTTP/Connect           HTTP/Connect        HTTP/Connect
                │                      │                   │
┌───────────────▼──────┐  ┌────────────▼───────┐  ┌──────▼────────────┐
│   Logger Plugin      │  │   Cache Plugin     │  │   App Plugin      │
│                      │  │                    │  │                   │
│  Provides:           │  │  Provides:         │  │  Requires:        │
│  - logger.v1         │  │  - cache.v1        │  │  - logger.v1      │
│                      │  │                    │  │  - cache.v1       │
│  Requires:           │  │  Requires:         │  └───────────────────┘
│  - (none)            │  │  - logger.v1       │
└──────────────────────┘  └────────────────────┘
```

## Service Registry Protocol

### Service Registration

Plugins register services they provide during startup:

```protobuf
// connectplugin/v1/broker.proto

service ServiceRegistry {
    // RegisterService registers a service this plugin provides.
    rpc RegisterService(RegisterServiceRequest) returns (RegisterServiceResponse);

    // UnregisterService removes a service registration.
    rpc UnregisterService(UnregisterServiceRequest) returns (UnregisterServiceResponse);

    // DiscoverService finds providers for a service.
    rpc DiscoverService(DiscoverServiceRequest) returns (DiscoverServiceResponse);

    // WatchService streams service availability updates.
    rpc WatchService(WatchServiceRequest) returns (stream WatchServiceResponse);
}

message RegisterServiceRequest {
    // Service type (e.g., "logger", "cache", "metrics")
    string service_type = 1;

    // Service version
    string version = 2;

    // Service endpoint path (relative to plugin's base URL)
    string endpoint_path = 3;

    // Service metadata
    map<string, string> metadata = 4;
}

message RegisterServiceResponse {
    // Registration ID for this service
    string registration_id = 1;
}

message DiscoverServiceRequest {
    // Service type to discover
    string service_type = 1;

    // Minimum version required (semver)
    string min_version = 2;
}

message DiscoverServiceResponse {
    // Available providers for this service
    repeated ServiceProvider providers = 1;
}

message ServiceProvider {
    // Provider plugin name
    string plugin_name = 1;

    // Service version
    string version = 2;

    // Capability grant for accessing this service
    CapabilityGrant grant = 3;
}

message CapabilityGrant {
    // Unique ID for this grant
    string grant_id = 1;

    // Service endpoint URL
    string endpoint_url = 2;

    // Bearer token for authentication
    string bearer_token = 3;

    // Expiration time
    google.protobuf.Timestamp expires_at = 4;
}
```

### Service Registration Flow

```
┌─────────────┐                          ┌─────────────┐
│   Logger    │                          │    Host     │
│   Plugin    │                          │   Broker    │
└──────┬──────┘                          └──────┬──────┘
       │                                        │
       │  1. POST /connectplugin.v1.ServiceRegistry/RegisterService
       │  ──────────────────────────────────────▶│
       │  {                                      │
       │    service_type: "logger",              │
       │    version: "1.0.0",                    │
       │    endpoint_path: "/logger.v1.Logger/"  │
       │  }                                      │
       │                                         │
       │                          2. Store in registry
       │                          {
       │                            "logger": {
       │                              provider: "logger-plugin",
       │                              version: "1.0.0",
       │                              endpoint: "http://logger-plugin:8080/logger.v1.Logger/"
       │                            }
       │                          }
       │                                         │
       │  ◀──────────────────────────────────────│
       │  { registration_id: "reg-123" }         │
       │                                         │
```

### Service Discovery Flow

```
┌─────────────┐                          ┌─────────────┐         ┌─────────────┐
│    App      │                          │    Host     │         │   Logger    │
│   Plugin    │                          │   Broker    │         │   Plugin    │
└──────┬──────┘                          └──────┬──────┘         └──────┬──────┘
       │                                        │                       │
       │  1. POST /connectplugin.v1.ServiceRegistry/DiscoverService
       │  ──────────────────────────────────────▶│                       │
       │  {                                      │                       │
       │    service_type: "logger",              │                       │
       │    min_version: "1.0.0"                 │                       │
       │  }                                      │                       │
       │                                         │                       │
       │                          2. Lookup in registry
       │                          3. Generate capability grant
       │                          4. Create JWT token
       │                                         │                       │
       │  ◀──────────────────────────────────────│                       │
       │  {                                      │                       │
       │    providers: [{                        │                       │
       │      plugin_name: "logger-plugin",      │                       │
       │      version: "1.0.0",                  │                       │
       │      grant: {                           │                       │
       │        grant_id: "grant-456",           │                       │
       │        endpoint_url: "http://host:8080/capabilities/logger/grant-456",
       │        bearer_token: "eyJ...",          │                       │
       │        expires_at: "..."                │                       │
       │      }                                  │                       │
       │    }]                                   │                       │
       │  }                                      │                       │
       │                                         │                       │
       │  5. POST /capabilities/logger/grant-456/Log
       │     Authorization: Bearer eyJ...        │                       │
       │  ────────────────────────────────────────▶                      │
       │                                         │                       │
       │                          6. Validate token                      │
       │                          7. Route to provider                   │
       │                                         │  ──────────────────────▶
       │                                         │  POST /logger.v1.Logger/Log
       │                                         │                       │
       │                                         │  ◀──────────────────────
       │                                         │  { success }           │
       │  ◀──────────────────────────────────────│                       │
       │  { success }                            │                       │
       │                                         │                       │
```

## Capability Routing

The host acts as a reverse proxy for capability requests:

```go
// Capability handler routes requests to service providers
func (b *Broker) ServeHTTP(w http.ResponseWriter, r *http.Request) {
    // Extract capability ID from path: /capabilities/{service}/{grant_id}/{method}
    parts := strings.Split(r.URL.Path, "/")
    if len(parts) < 5 {
        http.Error(w, "invalid capability path", http.StatusBadRequest)
        return
    }

    grantID := parts[3]

    // Validate bearer token
    token := extractBearerToken(r)
    grant, err := b.validateCapabilityToken(token, grantID)
    if err != nil {
        http.Error(w, "unauthorized", http.StatusUnauthorized)
        return
    }

    // Route to provider
    provider := grant.Provider
    targetURL := provider.BaseURL + r.URL.Path[len("/capabilities/"+grant.ServiceType+"/"+grantID):]

    // Proxy request
    b.proxyRequest(w, r, targetURL)
}
```

## Host Capability Model

Host capabilities work similarly but are provided by the host itself:

```go
type HostCapabilities struct {
    // Map of capability type -> handler
    handlers map[string]http.Handler
}

func (hc *HostCapabilities) RegisterCapability(capType string, handler http.Handler) {
    hc.handlers[capType] = handler
}

// Example: Secrets capability
func (b *Broker) registerHostCapabilities() {
    b.hostCapabilities.RegisterCapability("secrets", &SecretsHandler{
        vault: b.vault,
    })

    b.hostCapabilities.RegisterCapability("filesystem", &FilesystemHandler{
        allowedPaths: b.cfg.AllowedPaths,
    })
}
```

When a plugin requests a host capability:

```go
// Plugin requests "secrets" capability
req := &RequestCapabilityRequest{
    CapabilityType: "secrets",
    Reason:         "need database password",
}

resp := broker.RequestCapability(ctx, req)
// Returns capability grant with token

// Use capability
secretsClient := NewSecretsClient(resp.Grant.EndpointURL, resp.Grant.BearerToken)
dbPassword := secretsClient.GetSecret(ctx, "database/password")
```

## fx Integration

This broker model maps directly to fx dependency injection:

```go
// Host side: Register capabilities and services in fx container
fx.Provide(
    // Host capabilities
    NewSecretsService,
    NewAuthService,

    // Plugin service registry
    NewServiceRegistry,

    // Capability broker
    NewCapabilityBroker,
)

// Plugin side: Request services via broker
func NewAppPlugin(broker ServiceRegistryClient) (*AppPlugin, error) {
    // Discover logger service
    loggerResp, err := broker.DiscoverService(ctx, &DiscoverServiceRequest{
        ServiceType: "logger",
        MinVersion:  "1.0.0",
    })
    if err != nil {
        return nil, err
    }

    // Create client from grant
    loggerClient := NewLoggerClient(
        loggerResp.Providers[0].Grant.EndpointURL,
        loggerResp.Providers[0].Grant.BearerToken,
    )

    return &AppPlugin{
        logger: loggerClient,
    }, nil
}
```

## Security Model

### Token-Based Access Control

Each capability grant includes a JWT token:

```json
{
    "iss": "connect-plugin-host",
    "sub": "app-plugin",
    "cap": {
        "grant_id": "grant-456",
        "service_type": "logger",
        "provider": "logger-plugin",
        "permissions": ["log:write"]
    },
    "exp": 1706054400,
    "iat": 1706050800
}
```

### Permission Model

**Host Capabilities**: Fine-grained permissions
```json
{
    "cap": {
        "type": "secrets",
        "permissions": ["secrets:read:database/*", "secrets:list:database"]
    }
}
```

**Plugin Services**: Coarser permissions (method-level)
```json
{
    "cap": {
        "type": "logger",
        "permissions": ["logger:Log", "logger:Flush"]
    }
}
```

### Revocation

Tokens can be revoked:

```go
// Revoke all grants for a plugin
broker.RevokePluginGrants(ctx, "app-plugin")

// Revoke specific grant
broker.RevokeGrant(ctx, "grant-456")
```

Revocation is checked on each request (via token validation).

## Service Lifecycle

### Registration on Startup

```go
func (p *LoggerPlugin) OnStart(ctx context.Context, broker ServiceRegistryClient) error {
    // Register services this plugin provides
    _, err := broker.RegisterService(ctx, &RegisterServiceRequest{
        ServiceType:  "logger",
        Version:      "1.0.0",
        EndpointPath: "/logger.v1.Logger/",
    })
    return err
}
```

### Deregistration on Shutdown

```go
func (p *LoggerPlugin) OnStop(ctx context.Context) error {
    // Unregister services
    return p.broker.UnregisterService(ctx, &UnregisterServiceRequest{
        RegistrationId: p.registrationID,
    })
}
```

### Health-Based Deregistration

If a plugin becomes unhealthy, the host automatically deregisters its services:

```go
func (b *Broker) OnPluginUnhealthy(pluginName string) {
    b.mu.Lock()
    defer b.mu.Unlock()

    // Remove all services provided by this plugin
    for serviceType, provider := range b.registry {
        if provider.PluginName == pluginName {
            delete(b.registry, serviceType)
        }
    }

    // Revoke all capability grants issued to this plugin
    b.revokePluginGrants(pluginName)
}
```

## Service Discovery Patterns

### Pull-Based (Query)

```go
// Discover logger service
resp, err := broker.DiscoverService(ctx, &DiscoverServiceRequest{
    ServiceType: "logger",
})
loggerClient := NewLoggerClient(resp.Providers[0].Grant)
```

### Push-Based (Watch)

```go
// Watch for logger service availability
stream, err := broker.WatchService(ctx, &WatchServiceRequest{
    ServiceType: "logger",
})

for {
    event, err := stream.Recv()
    if err != nil {
        break
    }

    switch event.Type {
    case EventType_SERVICE_ADDED:
        // New logger became available
        p.logger = NewLoggerClient(event.Providers[0].Grant)
    case EventType_SERVICE_REMOVED:
        // Logger went away
        p.logger = NewNoopLogger()
    }
}
```

## MVP Scope Decision

### Phase 1 (MVP)

- ✅ Host capabilities (bootstrap only)
- ✅ Capability grant system (tokens, routing)
- ❌ Plugin service registry (defer)
- ❌ Service discovery (defer)

**Rationale**: Host capabilities are essential for auth, secrets, etc. Plugin-to-plugin composition is valuable but can be added later without breaking changes.

### Phase 2 (Enhanced)

- ✅ Service registry
- ✅ Service discovery
- ✅ Plugin-to-plugin communication
- ✅ Watch-based discovery

## Comparison with go-plugin

| Feature | go-plugin GRPCBroker | connect-plugin Broker |
|---------|---------------------|---------------------|
| **Direction** | Host → Plugin only | Bidirectional |
| **Discovery** | None (hardcoded) | Service registry |
| **Providers** | Host only | Host + Plugins |
| **Security** | Broker ID (uint32) | JWT tokens |
| **Routing** | Direct gRPC | HTTP proxy with validation |
| **Lifecycle** | Manual cleanup | Automatic on health change |

## Implementation Checklist

- [x] Capability vs service distinction
- [x] Service registry protocol
- [x] Capability grant system
- [x] Token-based security model
- [x] Host capability model
- [x] Service lifecycle management
- [x] fx integration pattern
- [x] MVP scope decision

## Next Steps

1. Implement broker proto (`proto/connectplugin/v1/broker.proto`)
2. Implement capability grant manager (MVP: host capabilities only)
3. Implement service registry (Phase 2)
4. Design client configuration (KOR-qjhn)
5. Design server configuration (KOR-koba)
