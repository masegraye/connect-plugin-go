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
1. ❌ **7 network hops**: App → Host Broker → Logger (proxy model)
2. ❌ **Single-provider**: Registry only tracked one provider per service type
3. ❌ **No dependencies**: Plugins didn't declare what they need

**New Phase 2 design:**
1. ✅ **Direct communication**: App → Logger (host only does discovery)
2. ✅ **Multi-provider**: Multiple plugins can provide same service type
3. ✅ **Dependency declaration**: Plugins declare `provides` and `requires`

## Architecture

```
┌─────────────────────────────────────────────────────────────────┐
│                          Host Platform                           │
│                                                                  │
│  ┌──────────────────────────────────────────────────────────┐   │
│  │               Service Registry                             │   │
│  │                                                            │   │
│  │  Services:                                                 │   │
│  │    "logger" -> [Logger Plugin A:8081, Logger Plugin B:8082]│   │
│  │    "cache"  -> [Cache Plugin:8083]                         │   │
│  │    "metrics" -> [Metrics Plugin:8084]                      │   │
│  │                                                            │   │
│  │  Dependencies:                                             │   │
│  │    App Plugin -> requires: [logger, cache]                 │   │
│  │    Logger Plugin B -> requires: [metrics]                  │   │
│  │    Cache Plugin -> requires: [logger]                      │   │
│  │                                                            │   │
│  │  Startup Order: [metrics, logger, cache, app]              │   │
│  └──────────────────────────────────────────────────────────┘   │
│                                                                  │
└─────────────────┬────────────────┬─────────────────┬────────────┘
                  │                │                 │
             HTTP Direct      HTTP Direct       HTTP Direct
                  │                │                 │
┌─────────────────▼───┐  ┌─────────▼────────┐  ┌────▼──────────────┐
│  Logger Plugin A    │  │  Cache Plugin     │  │  App Plugin       │
│  Port: 8081         │  │  Port: 8083       │  │  Port: 8085       │
│                     │  │                   │  │                   │
│  Provides:          │  │  Provides:        │  │  Requires:        │
│  - logger.v1        │  │  - cache.v1       │  │  - logger.v1      │
│                     │  │                   │  │  - cache.v1       │
│  Requires:          │  │  Requires:        │  │                   │
│  - (none)           │  │  - logger.v1      │  │  Provides:        │
│                     │  │                   │  │  - app.v1         │
└─────────────────────┘  └───────────────────┘  └───────────────────┘
                              │                        │
                              │                        │
                              └────────────────────────┘
                                   Direct HTTP calls
                              (no 7-hop proxy through host)
```

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

## Direct Communication (No 7-Hop Proxy)

**Key design decision:** Plugins call each other directly, not through the host.

```
# OLD (Phase 2 v1): 7 hops through proxy
App Plugin
  → Host Broker /capabilities/logger/grant-123
    → Host validates token
      → Host proxies to Logger Plugin
        → Logger handles request
      → Host returns response
    → App receives response

# NEW (Phase 2 v2): 2 hops direct
App Plugin discovers logger:
  → Host Registry /registry/discover (service_type=logger)
  → Host returns: http://logger-plugin:8081/logger.v1.Logger/

App Plugin calls logger directly:
  → Logger Plugin http://logger-plugin:8081/logger.v1.Logger/Log
  → Logger handles request
  → App receives response
```

**Benefits:**
- Eliminates host as bottleneck
- Lower latency (2 hops vs 7)
- Simpler implementation (no proxy)
- Better scalability

**Tradeoffs:**
- No centralized auth/auditing of plugin-to-plugin calls
- Plugins must be network-reachable from each other
- No token-based access control between plugins

**Mitigation:**
- Use service mesh (Istio, Linkerd) for mTLS and auth
- Host enforces required dependencies at startup
- Optional: Add plugin-level auth via interceptors

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

## Service Discovery Flow (Direct Calls)

```
┌──────────────┐       ┌──────────────┐       ┌──────────────┐
│ App Plugin   │       │    Host      │       │Logger Plugin │
│ Port: 8085   │       │   Registry   │       │  Port: 8081  │
└──────┬───────┘       └──────┬───────┘       └──────┬───────┘
       │                      │                       │
       │  ────────────────────▶                       │
       │  DiscoverService     │                       │
       │  {service_type: "logger"}                    │
       │                      │                       │
       │  ◀────────────────────                       │
       │  {                   │                       │
       │    endpoints: [      │                       │
       │      {               │                       │
       │        plugin: "logger",                     │
       │        endpoint_url: "http://logger:8081/logger.v1.Logger/"
       │      }               │                       │
       │    ]                 │                       │
       │  }                   │                       │
       │                      │                       │
       │  Cache endpoint URL locally                  │
       │  loggerURL = "http://logger:8081/logger.v1.Logger/"
       │                      │                       │
       │  ──────────────────────────────────────────────▶
       │  POST http://logger:8081/logger.v1.Logger/Log
       │  { level: "info", message: "..." }           │
       │                      │                       │
       │  ◀──────────────────────────────────────────────
       │  { success }         │                       │
       │                      │                       │
```

**Key: Only 2 network calls (discovery + actual call), no host proxy!**

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

Plugins request services during initialization:

```go
// Cache plugin initialization
type cachePlugin struct {
    logger loggerv1connect.LoggerClient
}

func NewCachePlugin(registryURL string) (*cachePlugin, error) {
    // Create registry client
    registryClient := connectpluginv1connect.NewServiceRegistryClient(
        &http.Client{},
        registryURL,
    )

    // Discover logger service
    resp, err := registryClient.DiscoverService(context.Background(),
        connect.NewRequest(&connectpluginv1.DiscoverServiceRequest{
            ServiceType: "logger",
            MinVersion:  "1.0.0",
            Strategy:    connectpluginv1.SelectionStrategyFirst,
        }))
    if err != nil {
        return nil, fmt.Errorf("logger service not available: %w", err)
    }

    // Create logger client (direct call to logger plugin)
    loggerEndpoint := resp.Msg.Endpoints[0].EndpointUrl
    loggerClient := loggerv1connect.NewLoggerClient(
        &http.Client{},
        loggerEndpoint,
    )

    return &cachePlugin{
        logger: loggerClient,
    }, nil
}

// Use logger service directly
func (c *cachePlugin) Set(ctx context.Context, req *CacheSetRequest) (*CacheSetResponse, error) {
    // Log the operation (direct call to logger plugin)
    c.logger.Log(ctx, connect.NewRequest(&loggerv1.LogRequest{
        Level:   "info",
        Message: "cache set",
        Fields:  map[string]string{"key": req.Key},
    }))

    // ... cache logic
}
```

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
| **Direction** | Host → Plugin | Plugin ↔ Plugin |
| **Discovery** | Advertised in handshake | Registry RPC |
| **Communication** | Via host proxy | Direct plugin-to-plugin |
| **Access Control** | Bearer tokens | mTLS (service mesh) |
| **Dependencies** | N/A (host always available) | Declared, graph-resolved |
| **Multi-provider** | N/A (host is single) | Yes (multiple loggers) |

## Security Model

**Phase 1 (Host Capabilities):**
- Token-based grants from host
- Host validates all requests
- Fine-grained permissions

**Phase 2 (Plugin Services):**
- Service mesh mTLS (Istio/Linkerd)
- Network policies (K8s NetworkPolicy)
- Plugin-level auth via interceptors
- No token proxy (performance over centralized auth)

**Rationale:**
- Phase 1 services are security-critical (secrets, auth) → need token validation
- Phase 2 services are functional (logger, cache) → use service mesh for auth
- Direct calls eliminate latency and host bottleneck

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

### Dependency Graph
- [ ] Implement DependencyGraph with topological sort
- [ ] Implement cycle detection
- [ ] Implement StartupOrder() computation
- [ ] Add dependency validation during handshake

### Plugin Integration
- [ ] Update PluginMetadata to include Provides/Requires
- [ ] Generate service declarations in protoc-gen-connect-plugin
- [ ] Add helper for discovering services
- [ ] Add example showing plugin-to-plugin communication

### Testing
- [ ] Unit tests for dependency graph resolution
- [ ] Unit tests for multi-provider registry
- [ ] Integration test: Logger + App plugins
- [ ] Integration test: Dependency-ordered startup
- [ ] Integration test: Service becomes unavailable (watch notifications)

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

## Open Questions

1. **Service versioning conflicts:**
   - What if App requires logger>=2.0.0 but only logger 1.0.0 is available?
   - Fail at startup or allow graceful degradation?

2. **Dynamic service addition:**
   - Can plugins be added after platform startup?
   - How do dependent plugins discover newly added services?

3. **Service removal:**
   - What happens if logger plugin crashes and app depends on it?
   - Fail app or use fallback?

4. **Circular dependencies:**
   - How to handle A requires B, B requires A?
   - Detect and reject, or allow with lazy initialization?

5. **Service mesh integration:**
   - How does this work with Istio/Linkerd?
   - Do we need to generate service mesh config?

## Next Steps

1. Implement registry.proto
2. Implement ServiceRegistry with multi-provider support
3. Implement DependencyGraph
4. Update PluginMetadata with Provides/Requires
5. Add integration test showing plugin-to-plugin communication
6. Document security model (service mesh requirement)
