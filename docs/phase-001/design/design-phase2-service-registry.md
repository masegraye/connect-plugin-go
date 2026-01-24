# Design: Phase 2 Service Registry (Plugin-to-Plugin)

**Status:** Draft
**Depends On:** design-mdxm-broker-architecture (Phase 1)

## Overview

Phase 2 extends the capability broker to support **plugin-to-plugin service discovery and communication**. This enables a platform where plugins are both providers and consumers of services, with the host acting as a service registry and mediator.

## Platform Vision

Plugins can:
1. **Provide services** to other plugins (e.g., Logger plugin provides logging)
2. **Consume services** from other plugins (e.g., App plugin uses Logger)
3. **Declare dependencies** in metadata (`provides`, `requires`)
4. **Start in dependency order** (Logger starts before App)
5. **Discover services at runtime** via service registry

The host provides:
- Service registry (who provides what)
- Dependency graph resolution
- Ordered plugin startup
- Service endpoint routing
- Multi-provider support (multiple logger implementations)

## Key Changes from Phase 1

**Phase 1 (Host Capabilities):**
- Host provides security-critical capabilities
- One-way: host → plugin
- Token-based grants
- ✅ Already implemented

**Phase 2 (Service Registry):**
- Plugins provide functional services
- Bidirectional: plugin ↔ plugin
- Service discovery and routing
- Multi-provider support

## Review Feedback Addressed

**Original Phase 2 issues:**
1. ❌ **Single-provider**: Registry only tracked one provider per service type
2. ❌ **No dependencies**: Plugins didn't declare what they need
3. ❌ **No hot reload support**: Couldn't swap service implementations

**New Phase 2 design:**
1. ✅ **Multi-provider**: Registry tracks ALL providers for each service type
2. ✅ **Dependency declaration**: Plugins declare `provides` and `requires`
3. ✅ **Host-mediated routing**: All calls go through host (not direct)

**Why host-mediated routing is CORRECT:**
- ✅ Centralized logging/observability of all plugin interactions
- ✅ Controlled failure blast radius
- ✅ Transparent hot reload (host swaps providers without plugins knowing)
- ✅ Network isolation (plugins don't need to be routable to each other)
- ✅ Policy enforcement (rate limits, auth, quotas)
- ✅ Simpler security (no service mesh required)

## Architecture

```
┌─────────────────────────────────────────────────────────────────┐
│                          Host Platform                           │
│                                                                  │
│  ┌──────────────────────────────────────────────────────────┐   │
│  │               Service Registry & Router                    │   │
│  │                                                            │   │
│  │  Services:                                                 │   │
│  │    "logger" -> [                                           │   │
│  │      {provider: logger-a, endpoint: /services/logger/abc},│   │
│  │      {provider: logger-b, endpoint: /services/logger/def} │   │
│  │    ]                                                       │   │
│  │    "cache"  -> [{provider: cache, endpoint: /services/cache/xyz}]
│  │    "metrics" -> [{provider: metrics, endpoint: /services/metrics/123}]
│  │                                                            │   │
│  │  Dependencies:                                             │   │
│  │    App Plugin -> requires: [logger, cache]                 │   │
│  │    Logger B -> requires: [metrics]                         │   │
│  │    Cache -> requires: [logger]                             │   │
│  │                                                            │   │
│  │  Startup Order: [metrics, logger-a, logger-b, cache, app] │   │
│  │                                                            │   │
│  │  Router:                                                   │   │
│  │    /services/logger/abc/* → proxy to Logger A             │   │
│  │    /services/logger/def/* → proxy to Logger B             │   │
│  │    /services/cache/xyz/* → proxy to Cache                 │   │
│  │                                                            │   │
│  │  Observability:                                            │   │
│  │    - Log all plugin-to-plugin calls                       │   │
│  │    - Trace request flows                                  │   │
│  │    - Collect metrics                                      │   │
│  └──────────────────────────────────────────────────────────┘   │
│                                                                  │
│  All plugin-to-plugin calls routed through host                 │
│  ↓                    ↓                    ↓                     │
└──┼────────────────────┼────────────────────┼─────────────────────┘
   │ Internal HTTP      │                    │
   │ (not exposed)      │                    │
   │                    │                    │
┌──▼────────────┐  ┌───▼────────────┐  ┌────▼──────────────┐
│ Logger A      │  │ Cache Plugin    │  │  App Plugin       │
│ Port: 8081    │  │ Port: 8083      │  │  Port: 8085       │
│ (internal)    │  │ (internal)      │  │  (internal)       │
│               │  │                 │  │                   │
│ Provides:     │  │ Provides:       │  │ Requires:         │
│ - logger.v1   │  │ - cache.v1      │  │ - logger.v1       │
│               │  │                 │  │ - cache.v1        │
│ Requires:     │  │ Requires:       │  │                   │
│ - (none)      │  │ - logger.v1     │  │ Provides:         │
│               │  │                 │  │ - app.v1          │
└───────────────┘  └─────────────────┘  └───────────────────┘
                        │                      │
                        │ Calls logger via:    │
                        │ Host/services/logger/abc/Log
                        └──────────────────────┘
                          (routed through host)
```

**Key:** Plugins are network-isolated, all communication via host router

## Service Declaration in Plugin Metadata

```go
// Plugin metadata includes service declarations
type PluginMetadata struct {
    Name    string
    Path    string
    Version string

    // NEW: Services this plugin provides
    Provides []ServiceDeclaration

    // NEW: Services this plugin requires
    Requires []ServiceDependency
}

type ServiceDeclaration struct {
    // Service type (e.g., "logger", "cache", "metrics")
    Type string

    // Service version (semver)
    Version string

    // Service endpoint path (relative to plugin base URL)
    // e.g., "/logger.v1.Logger/"
    Path string
}

type ServiceDependency struct {
    // Service type required
    Type string

    // Minimum version (semver)
    MinVersion string

    // Whether this dependency is optional
    Optional bool
}
```

## Example: Plugin Metadata

```go
// Logger Plugin
func (p *LoggerPlugin) Metadata() connectplugin.PluginMetadata {
    return connectplugin.PluginMetadata{
        Name:    "logger",
        Path:    "/plugin.v1.PluginController/",
        Version: "1.0.0",
        Provides: []connectplugin.ServiceDeclaration{
            {
                Type:    "logger",
                Version: "1.0.0",
                Path:    "/logger.v1.Logger/",
            },
        },
        Requires: []connectplugin.ServiceDependency{
            // Logger has no dependencies
        },
    }
}

// Cache Plugin (requires logger)
func (p *CachePlugin) Metadata() connectplugin.PluginMetadata {
    return connectplugin.PluginMetadata{
        Name:    "cache",
        Path:    "/plugin.v1.PluginController/",
        Version: "1.0.0",
        Provides: []connectplugin.ServiceDeclaration{
            {
                Type:    "cache",
                Version: "1.0.0",
                Path:    "/cache.v1.Cache/",
            },
        },
        Requires: []connectplugin.ServiceDependency{
            {
                Type:       "logger",
                MinVersion: "1.0.0",
                Optional:   false,
            },
        },
    }
}

// App Plugin (requires logger and cache)
func (p *AppPlugin) Metadata() connectplugin.PluginMetadata {
    return connectplugin.PluginMetadata{
        Name:    "app",
        Path:    "/plugin.v1.PluginController/",
        Version: "1.0.0",
        Provides: []connectplugin.ServiceDeclaration{
            {
                Type:    "app",
                Version: "1.0.0",
                Path:    "/app.v1.App/",
            },
        },
        Requires: []connectplugin.ServiceDependency{
            {Type: "logger", MinVersion: "1.0.0"},
            {Type: "cache", MinVersion: "1.0.0"},
        },
    }
}
```

## Service Registry API

```protobuf
// connectplugin/v1/registry.proto

service ServiceRegistry {
    // RegisterService registers a service this plugin provides.
    // Called by plugins during startup.
    rpc RegisterService(RegisterServiceRequest) returns (RegisterServiceResponse);

    // UnregisterService removes a service registration.
    // Called during shutdown or if plugin becomes unavailable.
    rpc UnregisterService(UnregisterServiceRequest) returns (UnregisterServiceResponse);

    // DiscoverService finds all providers for a service type.
    rpc DiscoverService(DiscoverServiceRequest) returns (DiscoverServiceResponse);

    // WatchService streams service availability updates.
    rpc WatchService(WatchServiceRequest) returns (stream WatchServiceEvent);
}

message RegisterServiceRequest {
    // Service type (e.g., "logger", "cache")
    string service_type = 1;

    // Service version
    string version = 2;

    // Service endpoint URL (absolute)
    // e.g., "http://logger-plugin:8081/logger.v1.Logger/"
    string endpoint_url = 3;

    // Service metadata
    map<string, string> metadata = 4;
}

message RegisterServiceResponse {
    // Registration ID for this service
    string registration_id = 1;
}

message UnregisterServiceRequest {
    string registration_id = 1;
}

message UnregisterServiceResponse {}

message DiscoverServiceRequest {
    // Service type to discover
    string service_type = 1;

    // Minimum version required (semver)
    string min_version = 2;

    // Selection strategy (round_robin, random, first)
    SelectionStrategy strategy = 3;
}

enum SelectionStrategy {
    SELECTION_STRATEGY_UNSPECIFIED = 0;
    SELECTION_STRATEGY_FIRST = 1;        // Return first available
    SELECTION_STRATEGY_ROUND_ROBIN = 2;  // Rotate through providers
    SELECTION_STRATEGY_RANDOM = 3;       // Pick random provider
    SELECTION_STRATEGY_ALL = 4;          // Return all providers
}

message DiscoverServiceResponse {
    // Available service providers
    repeated ServiceEndpoint endpoints = 1;
}

message ServiceEndpoint {
    // Provider plugin name
    string plugin_name = 1;

    // Service version
    string version = 2;

    // Endpoint URL (absolute)
    // Plugins call this directly - NO proxy through host
    string endpoint_url = 3;

    // Service metadata
    map<string, string> metadata = 4;
}

message WatchServiceRequest {
    string service_type = 1;
}

message WatchServiceEvent {
    EventType type = 1;
    repeated ServiceEndpoint endpoints = 2;
}

enum EventType {
    EVENT_TYPE_UNSPECIFIED = 0;
    EVENT_TYPE_ADDED = 1;
    EVENT_TYPE_REMOVED = 2;
    EVENT_TYPE_UPDATED = 3;
}
```

## Host-Mediated Communication (Centralized Routing)

**Key design decision:** All plugin-to-plugin calls go through the host.

```
# Service Discovery + Call Flow
App Plugin wants to call Logger:

  1. Discovery (once, cached):
     App → Host Registry /services/logger/discover
     Host → App: [
       {provider: "logger-a", endpoint: "/services/logger/provider-123"},
       {provider: "logger-b", endpoint: "/services/logger/provider-456"}
     ]

  2. Service Call (every time):
     App → Host /services/logger/provider-123/Log
     Host:
       - Logs the call (observability)
       - Validates service is healthy
       - Routes to Logger Plugin A
     Logger A → processes request
     Logger A → Host → App (response)
```

**Host routing path:** `/services/{type}/{provider-id}/{method}`

**Benefits:**
1. **Centralized observability**: Host logs ALL plugin interactions
2. **Failure control**: Host detects failures immediately, can failover
3. **Hot reload**: Host can swap Logger v2 for v1 transparently
4. **Network isolation**: Plugins don't need to reach each other
5. **Policy enforcement**: Host applies rate limits, auth, quotas
6. **Simpler security**: No service mesh required
7. **Version migration**: Host can route old clients to old service versions

**Latency:**
- Discovery: One-time overhead (cached by plugin)
- Calls: Host adds ~1-2ms proxy overhead
- Acceptable tradeoff for platform benefits

**Scalability:**
- Host can load-balance across multiple providers
- Host can cache routing decisions
- Async logging/metrics don't block requests

## Dependency Graph Resolution

The host builds a dependency graph and determines startup order:

```go
type DependencyGraph struct {
    nodes map[string]*PluginNode
    edges map[string][]string // plugin -> dependencies
}

type PluginNode struct {
    name     string
    metadata PluginMetadata
    started  bool
}

func (g *DependencyGraph) Add(metadata PluginMetadata) {
    g.nodes[metadata.Name] = &PluginNode{
        name:     metadata.Name,
        metadata: metadata,
    }

    // Add edges for dependencies
    for _, dep := range metadata.Requires {
        if !dep.Optional {
            g.edges[metadata.Name] = append(g.edges[metadata.Name], dep.Type)
        }
    }
}

func (g *DependencyGraph) StartupOrder() ([]string, error) {
    // Topological sort
    var order []string
    visited := make(map[string]bool)
    temp := make(map[string]bool)

    var visit func(string) error
    visit = func(name string) error {
        if temp[name] {
            return fmt.Errorf("dependency cycle detected: %s", name)
        }
        if visited[name] {
            return nil
        }

        temp[name] = true

        // Visit dependencies first
        for _, dep := range g.edges[name] {
            if err := visit(dep); err != nil {
                return err
            }
        }

        temp[name] = false
        visited[name] = true
        order = append(order, name)
        return nil
    }

    for name := range g.nodes {
        if err := visit(name); err != nil {
            return nil, err
        }
    }

    return order, nil
}
```

## Service Registration Flow

```
┌──────────────┐                      ┌──────────────┐
│    Host      │                      │Logger Plugin │
│   Registry   │                      │  Port: 8081  │
└──────┬───────┘                      └──────┬───────┘
       │                                     │
       │  ◀───────────────────────────────────│
       │  1. RegisterService                  │
       │  {                                   │
       │    service_type: "logger",           │
       │    version: "1.0.0",                 │
       │    endpoint_url: "http://logger:8081/logger.v1.Logger/"
       │  }                                   │
       │                                      │
       │  ────────────────────────────────────▶
       │  { registration_id: "reg-123" }      │
       │                                      │
       │  Store in registry:                  │
       │  services["logger"] = [              │
       │    {                                 │
       │      plugin: "logger",               │
       │      endpoint: "http://logger:8081/logger.v1.Logger/"
       │    }                                 │
       │  ]                                   │
       │                                      │
```

## Service Discovery and Call Flow (Host-Mediated)

```
┌──────────────┐       ┌──────────────┐       ┌──────────────┐
│ App Plugin   │       │    Host      │       │Logger Plugin │
│ Port: 8085   │       │   Registry   │       │  Port: 8081  │
└──────┬───────┘       └──────┬───────┘       └──────┬───────┘
       │                      │                       │
       │  1. DiscoverService  │                       │
       │  ────────────────────▶                       │
       │  {service_type: "logger"}                    │
       │                      │                       │
       │  ◀────────────────────                       │
       │  {                   │                       │
       │    endpoints: [      │                       │
       │      {               │                       │
       │        plugin: "logger-a",                   │
       │        endpoint_url: "/services/logger/provider-abc"
       │      },              │                       │
       │      {               │                       │
       │        plugin: "logger-b",                   │
       │        endpoint_url: "/services/logger/provider-def"
       │      }               │                       │
       │    ]                 │                       │
       │  }                   │                       │
       │                      │                       │
       │  Cache endpoints locally (TTL: 5min)         │
       │                      │                       │
       │  2. Call service via host route              │
       │  ────────────────────▶                       │
       │  POST /services/logger/provider-abc/Log      │
       │  {level: "info", ...}│                       │
       │                      │                       │
       │                      │  Host:                │
       │                      │  - Logs call          │
       │                      │  - Checks health      │
       │                      │  - Routes to provider │
       │                      │                       │
       │                      │  ────────────────────────▶
       │                      │  POST /logger.v1.Logger/Log
       │                      │  {level: "info", ...} │
       │                      │                       │
       │                      │  ◀────────────────────────
       │                      │  { success }          │
       │  ◀────────────────────                       │
       │  { success }         │                       │
       │                      │                       │
```

**Key: All calls routed through host for observability and control**

## Multi-Provider Support

Multiple plugins can provide the same service type:

```go
// Service registry tracks all providers
type ServiceRegistry struct {
    mu        sync.RWMutex
    services  map[string][]*ServiceProvider
    watchers  map[string][]*serviceWatcher
}

type ServiceProvider struct {
    RegistrationID string
    PluginName     string
    ServiceType    string
    Version        string
    EndpointURL    string
    Metadata       map[string]string
    RegisteredAt   time.Time
}

func (r *ServiceRegistry) Register(req *RegisterServiceRequest) (*RegisterServiceResponse, error) {
    r.mu.Lock()
    defer r.mu.Unlock()

    provider := &ServiceProvider{
        RegistrationID: generateID(),
        PluginName:     extractPluginName(req), // From context/metadata
        ServiceType:    req.ServiceType,
        Version:        req.Version,
        EndpointURL:    req.EndpointUrl,
        Metadata:       req.Metadata,
        RegisteredAt:   time.Now(),
    }

    // Append to providers list (supports multiple)
    r.services[req.ServiceType] = append(r.services[req.ServiceType], provider)

    // Notify watchers
    r.notifyWatchers(req.ServiceType, EventTypeAdded)

    return &RegisterServiceResponse{
        RegistrationId: provider.RegistrationID,
    }, nil
}
```

## Selection Strategies

When multiple providers exist, consumer can choose:

```go
func (r *ServiceRegistry) Discover(req *DiscoverServiceRequest) (*DiscoverServiceResponse, error) {
    r.mu.RLock()
    defer r.mu.RUnlock()

    providers := r.services[req.ServiceType]
    if len(providers) == 0 {
        return nil, connect.NewError(connect.CodeNotFound,
            fmt.Errorf("no providers for service %q", req.ServiceType))
    }

    // Filter by version
    compatible := filterCompatibleVersions(providers, req.MinVersion)
    if len(compatible) == 0 {
        return nil, connect.NewError(connect.CodeNotFound,
            fmt.Errorf("no compatible providers for service %q (min version: %s)",
                req.ServiceType, req.MinVersion))
    }

    // Apply selection strategy
    var selected []*ServiceProvider
    switch req.Strategy {
    case SelectionStrategyFirst:
        selected = compatible[:1]

    case SelectionStrategyRoundRobin:
        // Round-robin state tracked per consumer
        selected = []*ServiceProvider{r.nextRoundRobin(req.ServiceType, compatible)}

    case SelectionStrategyRandom:
        idx := rand.Intn(len(compatible))
        selected = compatible[idx : idx+1]

    case SelectionStrategyAll:
        selected = compatible

    default:
        selected = compatible[:1]
    }

    // Build response
    endpoints := make([]*ServiceEndpoint, len(selected))
    for i, p := range selected {
        endpoints[i] = &ServiceEndpoint{
            PluginName:  p.PluginName,
            Version:     p.Version,
            EndpointUrl: p.EndpointURL,
            Metadata:    p.Metadata,
        }
    }

    return &DiscoverServiceResponse{Endpoints: endpoints}, nil
}
```

## Dependency-Ordered Startup

Host starts plugins in dependency order:

```go
type Platform struct {
    registry   *ServiceRegistry
    depGraph   *DependencyGraph
    plugins    map[string]*PluginInstance
}

type PluginInstance struct {
    metadata PluginMetadata
    endpoint string
    client   *http.Client
    stopCh   chan struct{}
}

func (p *Platform) StartPlugins(ctx context.Context) error {
    // Build dependency graph
    for name, plugin := range p.plugins {
        p.depGraph.Add(plugin.metadata)
    }

    // Get startup order
    order, err := p.depGraph.StartupOrder()
    if err != nil {
        return fmt.Errorf("dependency resolution failed: %w", err)
    }

    log.Printf("Plugin startup order: %v", order)

    // Start plugins in order
    for _, name := range order {
        plugin := p.plugins[name]

        log.Printf("Starting plugin: %s", name)

        // Start plugin process/container
        if err := p.startPlugin(ctx, plugin); err != nil {
            return fmt.Errorf("failed to start plugin %s: %w", name, err)
        }

        // Wait for plugin to register its services
        if err := p.waitForServices(ctx, plugin); err != nil {
            return fmt.Errorf("plugin %s did not register services: %w", name, err)
        }

        log.Printf("Plugin %s started and registered services", name)
    }

    return nil
}

func (p *Platform) waitForServices(ctx context.Context, plugin *PluginInstance) error {
    // Wait for plugin to register all services it declares
    timeout := time.After(30 * time.Second)
    ticker := time.NewTicker(500 * time.Millisecond)
    defer ticker.Stop()

    expectedServices := make(map[string]bool)
    for _, svc := range plugin.metadata.Provides {
        expectedServices[svc.Type] = true
    }

    for {
        select {
        case <-ctx.Done():
            return ctx.Err()
        case <-timeout:
            return fmt.Errorf("timeout waiting for service registration")
        case <-ticker.C:
            // Check if all expected services are registered
            allRegistered := true
            for svcType := range expectedServices {
                if !p.registry.IsRegistered(svcType, plugin.metadata.Name) {
                    allRegistered = false
                    break
                }
            }
            if allRegistered {
                return nil
            }
        }
    }
}
```

## Plugin Initialization with Service Discovery

Plugins discover services and create clients that route through the host:

```go
// Cache plugin initialization
type cachePlugin struct {
    logger loggerv1connect.LoggerClient
}

func NewCachePlugin(hostURL string) (*cachePlugin, error) {
    // Create registry client (to host)
    registryClient := connectpluginv1connect.NewServiceRegistryClient(
        &http.Client{},
        hostURL,
    )

    // Discover logger service from registry
    resp, err := registryClient.DiscoverService(context.Background(),
        connect.NewRequest(&connectpluginv1.DiscoverServiceRequest{
            ServiceType: "logger",
            MinVersion:  "1.0.0",
            Strategy:    connectpluginv1.SelectionStrategyFirst,
        }))
    if err != nil {
        return nil, fmt.Errorf("logger service not available: %w", err)
    }

    // Create logger client pointing at HOST route
    // endpoint_url is: /services/logger/provider-abc
    // NOT direct to logger plugin!
    loggerEndpoint := resp.Msg.Endpoints[0].EndpointUrl
    loggerClient := loggerv1connect.NewLoggerClient(
        &http.Client{},
        hostURL+loggerEndpoint, // Routes through host!
    )

    return &cachePlugin{
        logger: loggerClient,
    }, nil
}

// Use logger service (call goes through host)
func (c *cachePlugin) Set(ctx context.Context, req *CacheSetRequest) (*CacheSetResponse, error) {
    // Log the operation
    // Call path: Cache → Host /services/logger/provider-abc/Log → Logger
    c.logger.Log(ctx, connect.NewRequest(&loggerv1.LogRequest{
        Level:   "info",
        Message: "cache set",
        Fields:  map[string]string{"key": req.Key},
    }))

    // ... cache logic
}
```

**From plugin perspective:** Looks like direct call (Connect client API)
**Actually:** Routes through host for observability and control

## Integration with Handshake

Service declarations are advertised during handshake:

```protobuf
message PluginInfo {
    string name = 1;
    string version = 2;
    string service_path = 3;

    // NEW: Services this plugin provides
    repeated ServiceDeclaration provides = 4;

    // NEW: Services this plugin requires
    repeated ServiceDependency requires = 5;
}

message ServiceDeclaration {
    string type = 1;
    string version = 2;
    string path = 3;
}

message ServiceDependency {
    string type = 1;
    string min_version = 2;
    bool optional = 3;
}
```

Host validates dependencies during handshake:

```go
func (h *HandshakeServer) Handshake(...) {
    // ... existing handshake logic

    // NEW: Validate plugin dependencies
    for _, plugin := range resp.Plugins {
        for _, dep := range plugin.Requires {
            if !dep.Optional {
                // Check if required service is available
                if !h.registry.HasService(dep.Type, dep.MinVersion) {
                    return nil, connect.NewError(
                        connect.CodeFailedPrecondition,
                        fmt.Errorf("plugin %q requires service %q v%s which is not available",
                            plugin.Name, dep.Type, dep.MinVersion))
                }
            }
        }
    }

    // ... return response
}
```

## Lifecycle: Registration and Discovery

**Plugin Startup:**
1. Plugin starts listening on its port
2. Plugin calls `RegisterService` for each service it provides
3. Host adds to service registry
4. Other plugins can now discover this service

**Plugin Consuming Service:**
1. Plugin calls `DiscoverService` during initialization
2. Host returns available endpoints
3. Plugin creates client directly to provider endpoint
4. Plugin makes RPCs directly to provider (no host proxy)

**Plugin Shutdown:**
1. Plugin calls `UnregisterService` for its services (or host detects via health checks)
2. Host removes from registry
3. Host notifies watchers (plugins watching this service)
4. Dependent plugins can react (use fallback, degrade, etc.)

## Service Health Integration

Services are automatically unregistered if plugin becomes unhealthy:

```go
func (p *Platform) OnPluginUnhealthy(pluginName string) {
    // Unregister all services provided by this plugin
    p.registry.UnregisterPluginServices(pluginName)

    // Notify watchers
    p.registry.NotifyServiceUnavailable(pluginName)
}
```

## Example: Platform Configuration

```yaml
# platform.yaml
plugins:
  - name: metrics
    image: platform/metrics:v1.0.0
    port: 8081
    provides:
      - type: metrics
        version: 1.0.0

  - name: logger
    image: platform/logger:v1.0.0
    port: 8082
    provides:
      - type: logger
        version: 1.0.0
    requires:
      - type: metrics
        minVersion: 1.0.0

  - name: cache
    image: platform/cache:v1.0.0
    port: 8083
    provides:
      - type: cache
        version: 1.0.0
    requires:
      - type: logger
        minVersion: 1.0.0

  - name: app
    image: customer/my-app:v2.1.0
    port: 8085
    provides:
      - type: app
        version: 2.1.0
    requires:
      - type: logger
        minVersion: 1.0.0
      - type: cache
        minVersion: 1.0.0
        optional: true
```

**Computed startup order:** `[metrics, logger, cache, app]`

## Comparison: Phase 1 vs Phase 2

| Aspect | Phase 1 (Host Capabilities) | Phase 2 (Service Registry) |
|--------|----------------------------|---------------------------|
| **Providers** | Host only | Any plugin |
| **Purpose** | Security/auth/policy | Functional composition |
| **Direction** | Host → Plugin | Plugin ↔ Plugin (via host) |
| **Discovery** | Advertised in handshake | Registry RPC |
| **Communication** | Via host proxy + tokens | Via host router |
| **Access Control** | Bearer token grants | Plugin registration validation |
| **Dependencies** | N/A (host always available) | Declared, graph-resolved |
| **Multi-provider** | N/A (host is single) | Yes (multiple loggers) |
| **Hot reload** | N/A | Yes (transparent provider swap) |
| **Impact analysis** | N/A | Yes (dependency graph) |
| **Observability** | Host logs capability calls | Host logs ALL service calls |

## Host Routing Implementation

The host acts as a reverse proxy for all service calls:

```go
// Host service router
func (h *Host) ServeHTTP(w http.ResponseWriter, r *http.Request) {
    // Route: /services/{type}/{provider-id}/{method}
    if !strings.HasPrefix(r.URL.Path, "/services/") {
        http.NotFound(w, r)
        return
    }

    parts := strings.Split(strings.TrimPrefix(r.URL.Path, "/services/"), "/")
    if len(parts) < 3 {
        http.Error(w, "invalid service path", http.StatusBadRequest)
        return
    }

    serviceType := parts[0]
    providerID := parts[1]
    method := "/" + strings.Join(parts[2:], "/")

    // Look up provider
    provider, err := h.registry.GetProvider(providerID)
    if err != nil {
        http.Error(w, "provider not found", http.StatusNotFound)
        return
    }

    // Log the call (centralized observability)
    h.logger.Info("service call",
        "caller", extractCaller(r),
        "service", serviceType,
        "provider", provider.PluginName,
        "method", method,
    )

    // Check provider health
    if !h.healthMonitor.IsHealthy(provider.PluginName) {
        // Try failover to another provider
        if alternate := h.registry.GetAlternateProvider(serviceType, providerID); alternate != nil {
            provider = alternate
            h.logger.Warn("failed over to alternate provider",
                "service", serviceType,
                "from", providerID,
                "to", alternate.RegistrationID,
            )
        } else {
            http.Error(w, "service unavailable", http.StatusServiceUnavailable)
            return
        }
    }

    // Proxy request to provider
    targetURL := provider.EndpointURL + method
    h.proxyRequest(w, r, targetURL)
}
```

## Hot Reload Support

Host can transparently swap service providers:

```go
func (h *Host) HotReloadService(serviceType, oldVersion, newVersion string) error {
    // Register new provider
    newProvider := h.startPlugin(ctx, newServiceConfig)

    // Wait for new provider to be healthy
    h.waitForHealthy(newProvider, 30*time.Second)

    // Update registry to prefer new provider
    h.registry.SetPreferredVersion(serviceType, newVersion)

    // Gradually drain old provider
    h.registry.DrainProvider(oldProvider, 60*time.Second)

    // Stop old provider
    h.stopPlugin(oldProvider)

    return nil
}
```

**From plugin perspective:**
- Plugin calls logger service via host route
- Host transparently switches from Logger v1 to Logger v2
- Plugin code doesn't change
- No connection drops (host manages graceful switchover)

## Centralized Observability

Host intercepts all plugin-to-plugin calls:

```go
type CallLog struct {
    Timestamp    time.Time
    Caller       string // "cache-plugin"
    Service      string // "logger"
    Provider     string // "logger-a"
    Method       string // "/logger.v1.Logger/Log"
    Duration     time.Duration
    StatusCode   int
    Error        string
}

func (h *Host) proxyRequest(w http.ResponseWriter, r *http.Request, targetURL string) {
    start := time.Now()

    // Proxy the request
    resp, err := h.httpClient.Do(...)

    // Log the call
    h.callLog.Record(CallLog{
        Timestamp:  start,
        Caller:     extractCaller(r),
        Service:    extractService(r),
        Provider:   extractProvider(r),
        Method:     r.URL.Path,
        Duration:   time.Since(start),
        StatusCode: resp.StatusCode,
        Error:      errorString(err),
    })

    // Emit metrics
    h.metrics.RecordServiceCall(...)

    // Emit trace span
    h.tracer.RecordSpan(...)
}
```

**Observability benefits:**
- All plugin interactions logged centrally
- Trace entire request flow through platform
- Detect performance bottlenecks
- Understand failure cascades
- Audit security-sensitive operations

## Facade Pattern

Plugins see clean Connect client API, host handles routing:

```go
// Plugin code (looks like direct call)
logger := loggerv1connect.NewLoggerClient(
    &http.Client{},
    hostURL+"/services/logger/provider-abc", // Actually routes through host
)

// Usage is transparent
logger.Log(ctx, &LogRequest{
    Level:   "info",
    Message: "operation completed",
})

// Behind the scenes:
// 1. HTTP POST to host at /services/logger/provider-abc/Log
// 2. Host logs the call
// 3. Host checks logger-a health
// 4. Host proxies to logger-a at http://logger-a:8081/logger.v1.Logger/Log
// 5. Logger-a processes and responds
// 6. Host logs response
// 7. Host returns to caller
```

**Plugin doesn't know about:**
- Which specific logger provider handled the request
- Whether failover occurred
- That the call was logged/metered
- That hot reload might happen

**Plugin just knows:**
- "I need logger service"
- "I call logger via this endpoint"
- "It works"

## Security Model

**Phase 1 (Host Capabilities):**
- Token-based grants from host
- Host validates all requests
- Fine-grained permissions (e.g., secrets:read:database/*)

**Phase 2 (Plugin Services):**
- Host validates plugin is registered
- Host validates service exists and is healthy
- Host can enforce rate limits, quotas
- Optional: Tokens for plugin-level auth
- Network isolation (plugins only talk to host)

**Security benefits of host-mediated:**
- Single point for auth/authz policy
- Plugins can't bypass host to call each other
- Host can log all access for audit
- Host can revoke access by updating routes

## Implementation Checklist

### Proto Definitions
- [ ] Add registry.proto (ServiceRegistry service)
- [ ] Update handshake.proto (add provides/requires to PluginInfo)
- [ ] Add ServiceDeclaration and ServiceDependency messages

### Service Registry
- [ ] Implement ServiceRegistry with multi-provider support
- [ ] Implement RegisterService/UnregisterService
- [ ] Implement DiscoverService with selection strategies
- [ ] Implement WatchService for change notifications
- [ ] Integrate with health monitoring (auto-unregister unhealthy)
- [ ] Add provider preference/priority support

### Host Router
- [ ] Implement service router: /services/{type}/{provider-id}/{method}
- [ ] Proxy requests to plugin endpoints
- [ ] Add centralized call logging
- [ ] Add request/response metrics
- [ ] Add distributed tracing integration
- [ ] Add health check before routing
- [ ] Add automatic failover to alternate providers

### Dependency Graph
- [ ] Implement DependencyGraph with topological sort
- [ ] Implement cycle detection
- [ ] Implement StartupOrder() computation
- [ ] Implement GetImpact() for impact analysis
- [ ] Implement GetDependents() and GetDependencies()
- [ ] Add dependency validation during handshake
- [ ] Support optional dependencies

### Dynamic Lifecycle
- [ ] Implement AddPlugin() with dependency validation
- [ ] Implement RemovePlugin() with impact-aware shutdown
- [ ] Implement ReplacePlugin() for zero-downtime reload
- [ ] Implement drain mechanism for graceful provider removal
- [ ] Add platform CLI for lifecycle management
- [ ] Add impact analysis preview before operations

### Plugin Integration
- [ ] Update PluginMetadata to include Provides/Requires
- [ ] Generate service declarations in protoc-gen-connect-plugin
- [ ] Add helper for discovering services via host
- [ ] Update client to route service calls through host
- [ ] Add service watching for dynamic provider changes

### Testing
- [ ] Unit tests for dependency graph resolution
- [ ] Unit tests for impact analysis
- [ ] Unit tests for multi-provider registry
- [ ] Integration test: Logger + Cache + App plugins
- [ ] Integration test: Dependency-ordered startup
- [ ] Integration test: Hot reload logger (zero downtime)
- [ ] Integration test: Remove plugin (dependent plugins stop)
- [ ] Integration test: Optional dependency (graceful degradation)
- [ ] Integration test: Service watch notifications

## Migration Path

**Existing users (Phase 1 only):**
- No changes required
- Host capabilities continue to work
- Can opt into Phase 2 by setting up registry

**New users (want Phase 2):**
- Declare provides/requires in plugin metadata
- Call RegisterService during plugin startup
- Use DiscoverService to find other plugins
- Platform handles dependency ordering

## Dynamic Plugin Lifecycle Management

Plugins can be added, removed, or replaced at runtime without restarting the host.

### Adding a Plugin at Runtime

```go
func (h *Host) AddPlugin(ctx context.Context, config PluginConfig) error {
    // 1. Validate dependencies are available
    for _, dep := range config.Metadata.Requires {
        if !dep.Optional && !h.registry.HasService(dep.Type, dep.MinVersion) {
            return fmt.Errorf("required service %q not available", dep.Type)
        }
    }

    // 2. Start the plugin
    instance, err := h.startPlugin(ctx, config)
    if err != nil {
        return err
    }

    // 3. Wait for plugin to register its services
    if err := h.waitForServices(ctx, instance); err != nil {
        h.stopPlugin(instance)
        return err
    }

    // 4. Notify watchers (plugins waiting for this service)
    for _, svc := range instance.metadata.Provides {
        h.registry.NotifyServiceAdded(svc.Type)
    }

    log.Printf("Plugin %s added successfully", config.Metadata.Name)
    return nil
}
```

### Removing a Plugin (Impact Analysis)

```go
func (h *Host) RemovePlugin(ctx context.Context, pluginName string) error {
    // 1. Compute impact: what depends on this plugin?
    impact := h.depGraph.GetImpact(pluginName)

    log.Printf("Removing plugin %s will affect: %v", pluginName, impact.AffectedPlugins)

    // 2. Stop dependent plugins first (reverse dependency order)
    for i := len(impact.AffectedPlugins) - 1; i >= 0; i-- {
        dependent := impact.AffectedPlugins[i]

        // Check if dependency is optional
        if h.isOptionalDependency(dependent, pluginName) {
            log.Printf("Plugin %s has optional dependency on %s, continuing", dependent, pluginName)
            continue
        }

        log.Printf("Stopping dependent plugin: %s", dependent)
        if err := h.StopPlugin(ctx, dependent); err != nil {
            return fmt.Errorf("failed to stop dependent %s: %w", dependent, err)
        }
    }

    // 3. Unregister services
    h.registry.UnregisterPluginServices(pluginName)

    // 4. Stop the plugin
    if err := h.stopPlugin(h.plugins[pluginName]); err != nil {
        return err
    }

    // 5. Notify watchers
    for _, svc := range h.plugins[pluginName].metadata.Provides {
        h.registry.NotifyServiceRemoved(svc.Type, pluginName)
    }

    log.Printf("Plugin %s and %d dependents stopped", pluginName, len(impact.AffectedPlugins))
    return nil
}

// ImpactAnalysis shows what will be affected
type ImpactAnalysis struct {
    TargetPlugin     string
    AffectedPlugins  []string // Plugins that depend on target
    AffectedServices []string // Services that will become unavailable
    OptionalImpact   []string // Plugins with optional dependencies (won't stop)
}

func (g *DependencyGraph) GetImpact(pluginName string) *ImpactAnalysis {
    impact := &ImpactAnalysis{
        TargetPlugin: pluginName,
    }

    // Find all plugins that depend on this one
    for name, deps := range g.edges {
        for _, dep := range deps {
            if dep == pluginName {
                // Check if optional
                if g.isOptional(name, dep) {
                    impact.OptionalImpact = append(impact.OptionalImpact, name)
                } else {
                    impact.AffectedPlugins = append(impact.AffectedPlugins, name)
                }
            }
        }
    }

    // Recursively find transitive dependencies
    impact.AffectedPlugins = g.getTransitiveDependents(pluginName)

    // List services that will be unavailable
    for _, svc := range g.nodes[pluginName].metadata.Provides {
        impact.AffectedServices = append(impact.AffectedServices, svc.Type)
    }

    return impact
}
```

### Replacing a Plugin (Hot Reload)

```go
func (h *Host) ReplacePlugin(ctx context.Context, pluginName string, newConfig PluginConfig) error {
    // 1. Analyze impact
    impact := h.depGraph.GetImpact(pluginName)

    log.Printf("Replacing %s (affects %d plugins)", pluginName, len(impact.AffectedPlugins))

    // 2. Start new version in parallel (blue-green)
    newInstance, err := h.startPlugin(ctx, newConfig)
    if err != nil {
        return fmt.Errorf("failed to start new version: %w", err)
    }

    // 3. Wait for new version to be healthy
    if err := h.waitForServices(ctx, newInstance); err != nil {
        h.stopPlugin(newInstance)
        return fmt.Errorf("new version failed to start: %w", err)
    }

    // 4. Update registry routes (atomic swap)
    h.registry.SwitchProvider(pluginName, newInstance.metadata.Provides)

    // 5. Drain old version (finish in-flight requests)
    h.drainPlugin(h.plugins[pluginName], 30*time.Second)

    // 6. Stop old version
    h.stopPlugin(h.plugins[pluginName])

    // 7. Update plugin map
    h.plugins[pluginName] = newInstance

    log.Printf("Plugin %s replaced successfully (zero downtime)", pluginName)
    return nil
}
```

**Zero-downtime reload:**
1. Start new version while old version runs
2. Wait for new version healthy
3. Atomic switch in registry routes
4. Drain old version
5. Stop old version

**No dependent plugins need to restart!**

## Impact Analysis Example

```
Platform state:
  - Metrics plugin (provides: metrics)
  - Logger A plugin (provides: logger, requires: metrics)
  - Logger B plugin (provides: logger, requires: metrics)
  - Cache plugin (provides: cache, requires: logger)
  - App plugin (provides: app, requires: logger, cache)

Impact of removing Metrics plugin:
  AffectedPlugins: [logger-a, logger-b, cache, app]
  AffectedServices: [metrics]
  OptionalImpact: []

Impact of removing Logger A:
  AffectedPlugins: []  (cache and app can use logger-b)
  AffectedServices: []
  OptionalImpact: [cache, app]  (have alternative provider)

Impact of removing Logger B:
  AffectedPlugins: []
  AffectedServices: []
  OptionalImpact: [cache, app]

Impact of removing both Loggers:
  AffectedPlugins: [cache, app]
  AffectedServices: [logger]
  OptionalImpact: []
```

## Graceful Degradation for Optional Dependencies

Plugins with optional dependencies continue running when service unavailable:

```go
// App plugin with optional cache dependency
type appPlugin struct {
    logger loggerv1connect.LoggerClient // Required
    cache  cachev1connect.CacheClient   // Optional
}

func NewAppPlugin(hostURL string, registry ServiceRegistryClient) (*appPlugin, error) {
    // Discover logger (required)
    loggerResp, err := registry.DiscoverService(ctx, &DiscoverServiceRequest{
        ServiceType: "logger",
    })
    if err != nil {
        return nil, fmt.Errorf("logger required: %w", err)
    }
    logger := loggerv1connect.NewLoggerClient(http.DefaultClient,
        hostURL+loggerResp.Endpoints[0].EndpointUrl)

    // Discover cache (optional)
    var cache cachev1connect.CacheClient
    cacheResp, err := registry.DiscoverService(ctx, &DiscoverServiceRequest{
        ServiceType: "cache",
    })
    if err != nil {
        log.Warn("cache service unavailable, using in-memory fallback")
        cache = newInMemoryCache() // Fallback
    } else {
        cache = cachev1connect.NewCacheClient(http.DefaultClient,
            hostURL+cacheResp.Endpoints[0].EndpointUrl)
    }

    // Watch for cache service to become available
    go watchCacheAvailability(registry, func(available bool) {
        if available && cache == nil {
            // Cache came online, switch from fallback to real cache
            // ...
        }
    })

    return &appPlugin{
        logger: logger,
        cache:  cache,
    }, nil
}
```

## Platform API

Host provides platform-level API for lifecycle management:

```go
type Platform interface {
    // Lifecycle
    AddPlugin(ctx context.Context, config PluginConfig) error
    RemovePlugin(ctx context.Context, name string) error
    ReplacePlugin(ctx context.Context, name string, newConfig PluginConfig) error
    RestartPlugin(ctx context.Context, name string) error

    // Impact Analysis
    GetImpact(pluginName string) *ImpactAnalysis
    GetDependents(pluginName string) []string
    GetDependencies(pluginName string) []string

    // Service Management
    ListServices() map[string][]*ServiceProvider
    GetServiceProviders(serviceType string) []*ServiceProvider
    SetPreferredProvider(serviceType, providerName string) error

    // Health
    IsPluginHealthy(name string) bool
    GetPluginHealth(name string) HealthStatus
}
```

**CLI tool example:**
```bash
# Show impact before removing
$ platform impact logger-a
Removing logger-a will affect:
  - No plugins (logger-b provides same service)

Optional impact (can continue with fallback):
  - cache (can use logger-b instead)
  - app (can use logger-b instead)

# Safe to remove
$ platform remove logger-a
✓ Removed logger-a (0 plugins stopped)

# Show impact of removing ALL loggers
$ platform impact logger-a logger-b
Removing logger-a, logger-b will affect:
  - cache (requires logger)
  - app (requires logger)

This will stop 2 dependent plugins. Confirm? [y/N]

# Replace with zero downtime
$ platform replace logger-a --image=platform/logger:v2.0.0
Starting logger-a v2.0.0...
Waiting for health checks...
Switching routes (atomic)...
Draining old version...
✓ Replaced logger-a (zero downtime, 0 dependent plugins restarted)
```

## Implementation Priority

1. **Core registry** - Multi-provider, registration/discovery
2. **Dependency graph** - Topological sort, impact analysis
3. **Host routing** - Proxy implementation with logging
4. **Dynamic lifecycle** - Add/remove/replace at runtime
5. **Hot reload** - Zero-downtime plugin replacement

This gives you a true plugin platform with dependency management and runtime lifecycle control.

## Platform Use Case Summary

This design enables a true **plugin platform** where:

**User deploys platform runtime:**
```
Platform Runtime (Host)
  - Service registry
  - Dependency graph manager
  - Service router with observability
  - Hot reload controller
```

**Platform provides core services** (Phase 1):
```
Host Capabilities:
  - Secrets management
  - Authentication/authorization
  - Configuration
  - Metrics collection
```

**Platform runs plugin ecosystem:**
```
Platform Plugins (provided by platform):
  - Logger Plugin (provides: logger)
  - Cache Plugin (provides: cache, requires: logger)
  - Metrics Plugin (provides: metrics)

Customer Plugins (deployed by users):
  - My App v1 (provides: my-app, requires: logger, cache)
  - My App v2 (provides: my-app, requires: logger, cache, metrics)
```

**Runtime behavior:**
1. User deploys "My App v1" plugin
2. Platform analyzes dependencies: needs logger, cache
3. Platform checks: logger ✓ cache ✓
4. Platform starts "My App v1"
5. App discovers logger and cache via registry
6. App calls logger/cache via host routes
7. Host logs all interactions, applies policies

**User upgrades to "My App v2":**
1. User runs: `platform replace my-app --version=v2`
2. Platform starts "My App v2" in parallel
3. Platform validates dependencies: logger ✓ cache ✓ metrics ✓
4. Platform atomically switches routes
5. Platform drains old version
6. **Zero downtime upgrade** ✅

**Platform adds new Logger v2:**
1. Platform runs: `platform add logger-v2 --image=logger:v2`
2. Platform starts Logger v2
3. Registry now has: logger-a (v1), logger-b (v1), logger-v2 (v2)
4. Existing plugins continue using v1
5. New plugins can use v2
6. **No existing plugins restart** ✅

**Platform removes old logger:**
1. Platform runs: `platform impact logger-a`
2. Platform shows: "No required dependents (logger-b available)"
3. Platform runs: `platform remove logger-a`
4. Routes update to use logger-b
5. Logger-a stops
6. **Dependent plugins unaffected** ✅

## Why Host-Mediated Routing is Essential

**For a production platform, you MUST have:**
1. **Centralized logging** - Audit trail of all plugin interactions
2. **Failure isolation** - Detect and handle failures at choke point
3. **Hot reload** - Update services without downtime
4. **Network isolation** - Plugins don't need mutual reachability
5. **Policy enforcement** - Rate limits, auth, quotas at single point
6. **Version migration** - Route old clients to old versions
7. **Impact analysis** - Know what breaks before making changes

**Host-mediated routing provides ALL of these.**

Direct plugin-to-plugin communication would require:
- Service mesh for mTLS (operational complexity)
- Distributed logging (harder to analyze)
- Each plugin implements failover (duplicated logic)
- Network policies for isolation (K8s complexity)
- No hot reload without coordination
- No impact analysis

**The "7 hops" concern was wrong** - the latency cost (~1-2ms) is worth the platform capabilities.

## Next Steps

1. Implement registry.proto with ServiceRegistry service
2. Implement ServiceRegistry with multi-provider support
3. Implement DependencyGraph with impact analysis
4. Implement host service router (/services/{type}/{provider-id}/*)
5. Update PluginMetadata with Provides/Requires
6. Add dynamic lifecycle management (Add/Remove/Replace)
7. Add integration tests demonstrating platform scenarios
8. Document hot reload procedures
9. Add platform CLI for lifecycle operations
