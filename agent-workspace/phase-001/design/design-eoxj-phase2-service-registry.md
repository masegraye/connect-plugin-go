# Design: Phase 2 Service Registry (Plugin-to-Plugin)

**Task:** KOR-eoxj
**Status:** Draft
**Depends On:** design-mdxm-broker-architecture (Phase 1)

## Overview

Phase 2 extends the capability broker to support **plugin-to-plugin service discovery and communication**. This enables a platform where plugins are both providers and consumers of services, with the host acting as a service registry, traffic controller, and observability layer.

## Platform Vision

**Plugins as Services:**
- Plugins can provide services to other plugins (e.g., Logger plugin provides logging)
- Plugins can consume services from other plugins (e.g., App plugin uses Logger)
- Plugins declare dependencies in metadata (`provides`, `requires`)
- Plugins start in dependency order (Logger starts before App)
- Plugins discover services at runtime via service registry

**Host as Platform:**
- Service registry (who provides what)
- Dependency graph resolution
- Ordered plugin startup
- Service endpoint routing (all calls go through host)
- Multi-provider support with host-controlled selection
- Traffic management based on health state
- Centralized observability of all plugin interactions

**Plugin Autonomy:**
- Plugins control their own degradation/recovery behavior
- Plugins report their health state to host
- Host notifies, doesn't prescribe responses
- Plugins treat runtime identity opaquely

## Key Design Decisions

### 1. Host-Mediated Routing

**All plugin-to-plugin calls route through the host.**

```
App Plugin → Host /services/logger/{provider-id}/Log → Logger Plugin
```

**Why this is correct:**
- Centralized logging/observability of all plugin interactions
- Controlled failure blast radius
- Transparent hot reload (host swaps providers without plugins knowing)
- Network isolation (plugins don't need to be routable to each other)
- Policy enforcement (rate limits, auth, quotas)
- Simpler security (no service mesh required)

### 2. Host Selects Provider

When multiple providers exist for a service, the **host selects** which one to use. Plugins are agnostic to multi-provider existence.

```
Plugin asks: "I need logger"
Host returns: Single endpoint (host has already selected)
Plugin uses: That endpoint (doesn't know others exist)
```

Host-level configuration determines selection (round-robin, weighted, etc.). Plugins don't participate in selection.

### 3. Three-State Health Model

```
Healthy   → Full traffic routed
Degraded  → Traffic continues, plugin decides what to return
Unhealthy → No traffic routed, plugin stays alive
```

This mirrors the Kubernetes readiness vs liveness distinction. A plugin can be alive but not ready.

### 4. Plugin-Controlled Degradation

Plugins decide their own behavior when dependencies change:

```
Host: "Logger is unavailable"
Plugin decides:
  - Continue degraded (buffer logs locally)
  - Mark self unhealthy (can't function)
  - Exit (wants restart when logger returns)
```

Host notifies, plugin responds. Host routes based on plugin-reported state.

### 5. Plugin Identity Model

```
Plugin starts → Has self-declared ID ("my-app")
Host connects → Asks for self-ID
Host assigns → Runtime ID ("my-app-x7k2") + Token
Plugin uses  → Runtime ID and token opaquely
```

This separates identity (who am I?) from authentication (prove it) from authorization (what can I do?).

## Architecture

```
┌─────────────────────────────────────────────────────────────────┐
│                          Host Platform                          │
│                                                                 │
│  ┌──────────────────────────────────────────────────────────┐  │
│  │               Service Registry & Router                   │  │
│  │                                                           │  │
│  │  Services (host selects provider):                        │  │
│  │    "logger" → [logger-a, logger-b] → selected: logger-a  │  │
│  │    "cache"  → [cache]              → selected: cache     │  │
│  │                                                           │  │
│  │  Health States:                                           │  │
│  │    logger-a: Healthy    → routing traffic                │  │
│  │    logger-b: Degraded   → routing traffic (limited)      │  │
│  │    cache:    Healthy    → routing traffic                │  │
│  │    app:      Healthy    → routing traffic                │  │
│  │                                                           │  │
│  │  Dependencies:                                            │  │
│  │    cache → requires: [logger]                            │  │
│  │    app   → requires: [logger, cache]                     │  │
│  │                                                           │  │
│  │  Startup Order: [logger-a, logger-b, cache, app]         │  │
│  └──────────────────────────────────────────────────────────┘  │
│                                                                 │
│  All plugin-to-plugin calls routed through host                │
│  ↓                    ↓                    ↓                    │
└──┼────────────────────┼────────────────────┼────────────────────┘
   │                    │                    │
┌──▼────────────┐  ┌───▼────────────┐  ┌────▼──────────────┐
│ Logger A      │  │ Cache Plugin   │  │  App Plugin       │
│ runtime-id:   │  │ runtime-id:    │  │  runtime-id:      │
│ logger-a-x7k2 │  │ cache-m3p9     │  │  app-q2w8         │
│               │  │                │  │                   │
│ State:Healthy │  │ State:Healthy  │  │  State:Healthy    │
│               │  │                │  │                   │
│ Provides:     │  │ Provides:      │  │  Requires:        │
│ - logger.v1   │  │ - cache.v1     │  │  - logger.v1      │
│               │  │                │  │  - cache.v1       │
│ Requires:     │  │ Requires:      │  │                   │
│ - (none)      │  │ - logger.v1    │  │  Provides:        │
│               │  │                │  │  - app.v1         │
└───────────────┘  └────────────────┘  └───────────────────┘
                         │
                         │ Calls logger via:
                         │ Host/services/logger/Log
                         └─────────────────────────
```

## Proto Definitions

### Plugin Identity in Handshake

```protobuf
// Updated handshake.proto

message HandshakeRequest {
    int32 core_protocol_version = 1;
    int32 app_protocol_version = 2;
    string magic_cookie_key = 3;
    string magic_cookie_value = 4;
    repeated string requested_plugins = 5;

    // NEW: Plugin identity
    string self_id = 10;       // Plugin's notion of its identity
    string self_version = 11;  // Plugin's version
}

message HandshakeResponse {
    repeated PluginInfo plugins = 1;
    map<string, string> server_metadata = 2;
    repeated Capability host_capabilities = 3;

    // NEW: Assigned identity
    string runtime_id = 10;    // Host-assigned, globally unique
    string runtime_token = 11; // For authenticating calls to host
}
```

### Service Declaration in Plugin Metadata

```protobuf
// Updated handshake.proto

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
    // Service type (e.g., "logger", "cache", "metrics")
    string type = 1;

    // Service version (semver)
    string version = 2;

    // Service endpoint path (relative to plugin base URL)
    string path = 3;
}

message ServiceDependency {
    // Service type required
    string type = 1;

    // Minimum version (semver)
    string min_version = 2;

    // Block startup if unavailable?
    bool required_for_startup = 3;

    // Send WatchService events for this dependency?
    bool watch_for_changes = 4;
}
```

### Service Registry API

```protobuf
// registry.proto

syntax = "proto3";

package connectplugin.v1;

option go_package = "github.com/masegraye/connect-plugin-go/gen/pluginv1";

service ServiceRegistry {
    // RegisterService registers a service this plugin provides.
    // Called by plugins during startup.
    rpc RegisterService(RegisterServiceRequest) returns (RegisterServiceResponse);

    // UnregisterService removes a service registration.
    rpc UnregisterService(UnregisterServiceRequest) returns (UnregisterServiceResponse);

    // DiscoverService finds the provider for a service type.
    // Host selects provider - returns single endpoint.
    rpc DiscoverService(DiscoverServiceRequest) returns (DiscoverServiceResponse);

    // WatchService streams service availability updates.
    rpc WatchService(WatchServiceRequest) returns (stream WatchServiceEvent);
}

message RegisterServiceRequest {
    // Service type (e.g., "logger", "cache")
    string service_type = 1;

    // Service version
    string version = 2;

    // Service endpoint path (relative to plugin base)
    string endpoint_path = 3;

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
}

message DiscoverServiceResponse {
    // Single endpoint - host has already selected provider
    ServiceEndpoint endpoint = 1;

    // True if this is the only provider (informational)
    bool single_provider = 2;
}

message ServiceEndpoint {
    // Provider plugin runtime ID
    string provider_id = 1;

    // Service version
    string version = 2;

    // Endpoint URL (routes through host)
    // e.g., "/services/logger/logger-a-x7k2"
    string endpoint_url = 3;

    // Service metadata
    map<string, string> metadata = 4;
}

message WatchServiceRequest {
    string service_type = 1;
}

message WatchServiceEvent {
    string service_type = 1;
    ServiceState state = 2;

    // If state is AVAILABLE, this is the endpoint to use
    ServiceEndpoint endpoint = 3;
}

enum ServiceState {
    SERVICE_STATE_UNSPECIFIED = 0;
    SERVICE_STATE_AVAILABLE = 1;    // Service is available
    SERVICE_STATE_UNAVAILABLE = 2;  // No providers
    SERVICE_STATE_DEGRADED = 3;     // Provider is degraded
}
```

### Health State Model

```protobuf
// health.proto (extended)

enum HealthState {
    HEALTH_STATE_UNSPECIFIED = 0;
    HEALTH_STATE_HEALTHY = 1;    // Fully functional, route traffic
    HEALTH_STATE_DEGRADED = 2;   // Limited functionality, still route traffic
    HEALTH_STATE_UNHEALTHY = 3;  // Cannot serve requests, stop routing
}
```

### Plugin Lifecycle Service

```protobuf
// lifecycle.proto

syntax = "proto3";

package connectplugin.v1;

option go_package = "github.com/masegraye/connect-plugin-go/gen/pluginv1";

// PluginLifecycle is implemented by the HOST, called by plugins
service PluginLifecycle {
    // ReportHealth allows plugin to report its health state
    rpc ReportHealth(ReportHealthRequest) returns (ReportHealthResponse);
}

// PluginControl is implemented by PLUGINS, called by host
service PluginControl {
    // Shutdown requests graceful shutdown
    rpc Shutdown(ShutdownRequest) returns (ShutdownResponse);

    // GetHealth checks plugin's current state
    rpc GetHealth(GetHealthRequest) returns (GetHealthResponse);
}

message ReportHealthRequest {
    HealthState state = 1;
    string reason = 2;
    repeated string unavailable_dependencies = 3;
}

message ReportHealthResponse {}

message ShutdownRequest {
    int32 grace_period_seconds = 1;
    string reason = 2;
}

message ShutdownResponse {
    bool acknowledged = 1;
}

message GetHealthRequest {}

message GetHealthResponse {
    HealthState state = 1;
    string reason = 2;
    repeated string unavailable_dependencies = 3;
}
```

## Go Types

### Plugin Metadata

```go
type PluginMetadata struct {
    Name    string
    Path    string
    Version string

    // Services this plugin provides
    Provides []ServiceDeclaration

    // Services this plugin requires
    Requires []ServiceDependency
}

type ServiceDeclaration struct {
    Type    string // "logger", "cache", etc.
    Version string // semver
    Path    string // relative endpoint path
}

type ServiceDependency struct {
    Type              string
    MinVersion        string
    RequiredForStartup bool // Block startup if unavailable
    WatchForChanges   bool // Receive availability notifications
}
```

### Health States

```go
type HealthState int

const (
    HealthStateUnspecified HealthState = iota
    HealthStateHealthy                        // Full traffic
    HealthStateDegraded                       // Traffic continues, limited
    HealthStateUnhealthy                      // No traffic routed
)

// Routing behavior
func (h *Host) shouldRouteTraffic(state HealthState) bool {
    return state == HealthStateHealthy || state == HealthStateDegraded
}
```

### Service Registry

```go
type ServiceRegistry struct {
    mu        sync.RWMutex
    services  map[string][]*ServiceProvider  // type -> providers
    selection map[string]SelectionStrategy   // type -> strategy (host config)
}

type ServiceProvider struct {
    RegistrationID string
    RuntimeID      string // Plugin's runtime ID
    ServiceType    string
    Version        string
    EndpointPath   string
    Metadata       map[string]string
    HealthState    HealthState
    RegisteredAt   time.Time
}

type SelectionStrategy int

const (
    SelectionFirst SelectionStrategy = iota
    SelectionRoundRobin
    SelectionRandom
    SelectionWeighted
)

// Host selects provider - plugin doesn't participate
func (r *ServiceRegistry) SelectProvider(serviceType string) (*ServiceProvider, error) {
    r.mu.RLock()
    defer r.mu.RUnlock()

    providers := r.services[serviceType]
    if len(providers) == 0 {
        return nil, fmt.Errorf("no providers for service %q", serviceType)
    }

    // Filter to healthy/degraded only
    available := filterAvailable(providers)
    if len(available) == 0 {
        return nil, fmt.Errorf("no available providers for service %q", serviceType)
    }

    // Apply host-configured selection strategy
    strategy := r.selection[serviceType]
    return r.applyStrategy(available, strategy), nil
}
```

### Dependency Graph

```go
type DependencyGraph struct {
    mu     sync.RWMutex
    nodes  map[string]*PluginNode           // runtime-id -> node
    edges  map[string][]string              // runtime-id -> dependency service types
    byType map[string][]string              // service-type -> provider runtime-ids
}

type PluginNode struct {
    RuntimeID   string
    SelfID      string
    Metadata    PluginMetadata
    HealthState HealthState
    Started     bool
}

func (g *DependencyGraph) StartupOrder() ([]string, error) {
    // Topological sort based on service dependencies
    // Returns runtime-ids in dependency order
}

func (g *DependencyGraph) GetImpact(runtimeID string) *ImpactAnalysis {
    // What plugins depend on services provided by this plugin?
}
```

## Service Discovery and Call Flow

### Plugin Initialization

```go
// Cache plugin discovers logger during initialization
func NewCachePlugin(hostURL, runtimeToken string) (*CachePlugin, error) {
    // Create authenticated HTTP client
    httpClient := &authenticatedClient{
        token: runtimeToken,
        inner: http.DefaultClient,
    }

    // Create registry client
    registry := pluginv1connect.NewServiceRegistryClient(httpClient, hostURL)

    // Discover logger (host selects provider)
    resp, err := registry.DiscoverService(ctx, connect.NewRequest(&pluginv1.DiscoverServiceRequest{
        ServiceType: "logger",
        MinVersion:  "1.0.0",
    }))
    if err != nil {
        return nil, fmt.Errorf("logger required: %w", err)
    }

    // Create logger client pointing at HOST route
    // endpoint_url is: "/services/logger/logger-a-x7k2"
    loggerClient := loggerv1connect.NewLoggerClient(
        httpClient,
        hostURL + resp.Msg.Endpoint.EndpointUrl,
    )

    return &CachePlugin{
        logger: loggerClient,
        // ...
    }, nil
}
```

### Service Call Flow

```
Cache Plugin wants to call Logger:

1. Cache → Host POST /services/logger/logger-a-x7k2/Log
   Headers:
     Authorization: Bearer {runtime_token}
     X-Plugin-Runtime-ID: cache-m3p9

2. Host receives request:
   - Extracts caller from header (cache-m3p9)
   - Looks up provider (logger-a-x7k2)
   - Checks provider health (Healthy ✓)
   - Logs: "cache-m3p9 → logger-a-x7k2 /Log"
   - Proxies to Logger A

3. Host → Logger A POST /logger.v1.Logger/Log

4. Logger A processes, responds

5. Logger A → Host → Cache (response)

6. Host logs: "cache-m3p9 → logger-a-x7k2 /Log 200 15ms"
```

### Host Router Implementation

```go
func (h *Host) ServeHTTP(w http.ResponseWriter, r *http.Request) {
    // Route: /services/{type}/{provider-runtime-id}/{method...}
    if !strings.HasPrefix(r.URL.Path, "/services/") {
        h.next.ServeHTTP(w, r)
        return
    }

    parts := strings.SplitN(strings.TrimPrefix(r.URL.Path, "/services/"), "/", 3)
    if len(parts) < 3 {
        http.Error(w, "invalid service path", http.StatusBadRequest)
        return
    }

    serviceType := parts[0]
    providerID := parts[1]
    method := "/" + parts[2]

    // Extract caller from headers
    callerID := r.Header.Get("X-Plugin-Runtime-ID")
    token := strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")

    // Validate caller token
    if !h.validateToken(callerID, token) {
        http.Error(w, "unauthorized", http.StatusUnauthorized)
        return
    }

    // Look up provider
    provider, err := h.registry.GetProvider(providerID)
    if err != nil {
        http.Error(w, "provider not found", http.StatusNotFound)
        return
    }

    // Check provider health
    if !h.shouldRouteTraffic(provider.HealthState) {
        http.Error(w, "service unavailable", http.StatusServiceUnavailable)
        return
    }

    // Log the call
    start := time.Now()
    h.logger.Info("service call",
        "caller", callerID,
        "service", serviceType,
        "provider", providerID,
        "method", method,
    )

    // Proxy request
    targetURL := provider.InternalEndpoint + method
    resp, err := h.proxy(r, targetURL)

    // Log completion
    h.logger.Info("service call complete",
        "caller", callerID,
        "provider", providerID,
        "method", method,
        "status", resp.StatusCode,
        "duration", time.Since(start),
    )

    // Return response
    copyResponse(w, resp)
}
```

## Plugin Lifecycle

### Startup Flow

```
1. Host loads plugin configuration
   - Parses metadata (provides, requires)
   - Adds to dependency graph

2. Host computes startup order
   - Topological sort on service dependencies
   - Detects cycles (error if found)

3. For each plugin in order:
   a. Start plugin process/container
   b. Connect and handshake
      - Plugin sends self_id, self_version
      - Host assigns runtime_id, runtime_token
   c. Validate dependencies available
   d. Wait for plugin to register services
   e. Wait for plugin to report Healthy

4. All plugins started, platform ready
```

### Dependency Notification

```go
// Plugin subscribes to dependency changes
func (p *CachePlugin) watchDependencies(ctx context.Context) {
    stream, _ := p.registry.WatchService(ctx, connect.NewRequest(&pluginv1.WatchServiceRequest{
        ServiceType: "logger",
    }))

    for stream.Receive() {
        event := stream.Msg()
        switch event.State {
        case pluginv1.ServiceState_SERVICE_STATE_AVAILABLE:
            p.onLoggerAvailable(event.Endpoint)
        case pluginv1.ServiceState_SERVICE_STATE_UNAVAILABLE:
            p.onLoggerUnavailable()
        case pluginv1.ServiceState_SERVICE_STATE_DEGRADED:
            p.onLoggerDegraded(event.Endpoint)
        }
    }
}

func (p *CachePlugin) onLoggerUnavailable() {
    // Plugin decides its own response
    p.mu.Lock()
    defer p.mu.Unlock()

    // Option 1: Degrade gracefully
    p.logger = newBufferingLogger(1000)
    p.reportHealth(HealthStateDegraded, "logger unavailable, buffering")

    // Option 2: Mark unhealthy (can't function)
    // p.reportHealth(HealthStateUnhealthy, "logger required")

    // Option 3: Exit (wants restart)
    // os.Exit(1)
}

func (p *CachePlugin) onLoggerAvailable(endpoint *pluginv1.ServiceEndpoint) {
    p.mu.Lock()
    defer p.mu.Unlock()

    // Reconnect to logger
    p.logger = loggerv1connect.NewLoggerClient(p.httpClient, p.hostURL+endpoint.EndpointUrl)

    // Flush any buffered logs
    p.flushBufferedLogs()

    // Report recovery
    p.reportHealth(HealthStateHealthy, "recovered")
}
```

### Health Reporting

```go
func (p *CachePlugin) reportHealth(state HealthState, reason string) {
    p.lifecycle.ReportHealth(context.Background(), connect.NewRequest(&pluginv1.ReportHealthRequest{
        State:  pluginv1.HealthState(state),
        Reason: reason,
    }))
}
```

### Host Handles Health Changes

```go
func (h *Host) onPluginHealthChange(runtimeID string, state HealthState) {
    // Update registry
    h.registry.UpdateHealth(runtimeID, state)

    // Update routing
    if !h.shouldRouteTraffic(state) {
        h.logger.Warn("stopping traffic to plugin",
            "plugin", runtimeID,
            "state", state,
        )
    }

    // Notify dependent plugins if this plugin provides services
    for _, svc := range h.registry.GetServicesBy(runtimeID) {
        h.notifyWatchers(svc.Type, toServiceState(state))
    }
}
```

## Dynamic Lifecycle Management

### Adding a Plugin at Runtime

```go
func (h *Host) AddPlugin(ctx context.Context, config PluginConfig) error {
    // 1. Validate dependencies are available
    for _, dep := range config.Metadata.Requires {
        if dep.RequiredForStartup && !h.registry.HasService(dep.Type, dep.MinVersion) {
            return fmt.Errorf("required service %q not available", dep.Type)
        }
    }

    // 2. Start the plugin
    instance, err := h.startPlugin(ctx, config)
    if err != nil {
        return err
    }

    // 3. Handshake (assigns runtime ID and token)
    if err := h.handshake(ctx, instance); err != nil {
        h.stopPlugin(instance)
        return err
    }

    // 4. Wait for plugin to register its services
    if err := h.waitForServices(ctx, instance); err != nil {
        h.stopPlugin(instance)
        return err
    }

    // 5. Wait for healthy
    if err := h.waitForHealthy(ctx, instance); err != nil {
        h.stopPlugin(instance)
        return err
    }

    // 6. Add to dependency graph
    h.depGraph.Add(instance)

    // 7. Notify watchers
    for _, svc := range instance.Metadata.Provides {
        h.notifyWatchers(svc.Type, ServiceStateAvailable)
    }

    return nil
}
```

### Removing a Plugin (Impact Analysis)

```go
func (h *Host) RemovePlugin(ctx context.Context, runtimeID string) error {
    // 1. Compute impact
    impact := h.depGraph.GetImpact(runtimeID)

    h.logger.Info("removing plugin",
        "plugin", runtimeID,
        "affected_plugins", impact.AffectedPlugins,
        "affected_services", impact.AffectedServices,
    )

    // 2. Notify dependents first
    for _, svc := range h.registry.GetServicesBy(runtimeID) {
        h.notifyWatchers(svc.Type, ServiceStateUnavailable)
    }

    // 3. Wait for dependents to handle (grace period)
    time.Sleep(5 * time.Second)

    // 4. Unregister services
    h.registry.UnregisterPluginServices(runtimeID)

    // 5. Request graceful shutdown
    h.requestShutdown(runtimeID, 30*time.Second)

    // 6. Remove from graph
    h.depGraph.Remove(runtimeID)

    return nil
}

type ImpactAnalysis struct {
    TargetPlugin     string
    AffectedPlugins  []string // Plugins with required deps on this
    AffectedServices []string // Services that will become unavailable
    OptionalImpact   []string // Plugins with optional deps (won't fail)
}
```

### Hot Reload (Zero Downtime)

```go
func (h *Host) ReplacePlugin(ctx context.Context, runtimeID string, newConfig PluginConfig) error {
    old := h.plugins[runtimeID]

    // 1. Start new version in parallel
    newInstance, err := h.startPlugin(ctx, newConfig)
    if err != nil {
        return fmt.Errorf("failed to start new version: %w", err)
    }

    // 2. Handshake new instance
    if err := h.handshake(ctx, newInstance); err != nil {
        h.stopPlugin(newInstance)
        return err
    }

    // 3. Wait for new version healthy
    if err := h.waitForHealthy(ctx, newInstance); err != nil {
        h.stopPlugin(newInstance)
        return err
    }

    // 4. Atomic switch in registry
    for _, svc := range old.Metadata.Provides {
        h.registry.SwitchProvider(svc.Type, old.RuntimeID, newInstance.RuntimeID)
    }

    // 5. Drain old version
    h.drain(old, 30*time.Second)

    // 6. Stop old version
    h.requestShutdown(old.RuntimeID, 10*time.Second)
    h.stopPlugin(old)

    // 7. Update graph
    h.depGraph.Replace(old.RuntimeID, newInstance)

    h.logger.Info("plugin replaced",
        "old", old.RuntimeID,
        "new", newInstance.RuntimeID,
    )

    return nil
}
```

## Comparison: Phase 1 vs Phase 2

| Aspect | Phase 1 (Host Capabilities) | Phase 2 (Service Registry) |
|--------|----------------------------|---------------------------|
| **Providers** | Host only | Any plugin |
| **Purpose** | Security/auth/policy | Functional composition |
| **Direction** | Host → Plugin | Plugin ↔ Plugin (via host) |
| **Discovery** | Advertised in handshake | Registry RPC |
| **Communication** | Via host proxy + tokens | Via host router + tokens |
| **Provider Selection** | N/A (host is single) | Host selects (plugin agnostic) |
| **Dependencies** | N/A (host always available) | Declared, graph-resolved |
| **Multi-provider** | N/A | Yes (host manages) |
| **Hot reload** | N/A | Yes (transparent swap) |
| **Health Model** | Binary (serving/not) | Three-state (healthy/degraded/unhealthy) |
| **Degradation** | N/A | Plugin-controlled |

## Implementation Checklist

### Proto Definitions
- [ ] Update handshake.proto with self_id/runtime_id/runtime_token
- [ ] Update handshake.proto with provides/requires in PluginInfo
- [ ] Create registry.proto (ServiceRegistry service)
- [ ] Create lifecycle.proto (PluginLifecycle, PluginControl services)
- [ ] Add HealthState enum (healthy/degraded/unhealthy)

### Service Registry
- [ ] Implement ServiceRegistry with multi-provider support
- [ ] Implement host-controlled provider selection (not plugin choice)
- [ ] Implement RegisterService/UnregisterService
- [ ] Implement DiscoverService (returns single endpoint)
- [ ] Implement WatchService for change notifications
- [ ] Integrate with health state updates

### Plugin Identity
- [ ] Update handshake to accept self_id from plugin
- [ ] Generate runtime_id in host (combine self_id + unique suffix)
- [ ] Generate runtime_token during handshake
- [ ] Validate token on all host API calls

### Health Model
- [ ] Implement three-state health (Healthy/Degraded/Unhealthy)
- [ ] Implement ReportHealth RPC (plugin → host)
- [ ] Implement GetHealth RPC (host → plugin)
- [ ] Update routing to respect health states

### Host Router
- [ ] Implement /services/{type}/{provider-id}/{method} routing
- [ ] Extract and validate caller identity from headers
- [ ] Check provider health before routing
- [ ] Centralized call logging
- [ ] Request/response metrics

### Dependency Graph
- [ ] Implement DependencyGraph with topological sort
- [ ] Implement cycle detection
- [ ] Implement StartupOrder()
- [ ] Implement GetImpact() for impact analysis

### Dynamic Lifecycle
- [ ] Implement AddPlugin() with dependency validation
- [ ] Implement RemovePlugin() with impact-aware notification
- [ ] Implement ReplacePlugin() for zero-downtime hot reload
- [ ] Implement graceful drain mechanism
- [ ] Implement Shutdown RPC

### Testing
- [ ] Unit tests for dependency graph
- [ ] Unit tests for impact analysis
- [ ] Unit tests for multi-provider selection
- [ ] Integration test: Logger + Cache + App plugins
- [ ] Integration test: Dependency-ordered startup
- [ ] Integration test: Hot reload (zero downtime)
- [ ] Integration test: Plugin degradation and recovery
- [ ] Integration test: Service watch notifications

## Migration from Phase 1

**Existing Phase 1 users:**
- No changes required
- Host capabilities continue to work identically
- Can opt into Phase 2 by adding provides/requires to metadata

**New Phase 2 users:**
1. Declare provides/requires in plugin metadata
2. Call RegisterService during plugin startup
3. Use DiscoverService to find dependencies
4. Implement WatchService handler for dynamic updates
5. Report health state changes to host

## Summary

This design enables a true plugin platform where:

1. **Plugins are autonomous services** - they control their own degradation and recovery
2. **Host is traffic controller** - routes based on health, logs everything, enables hot reload
3. **Identity is layered** - self-ID → runtime-ID → token, with clear separation
4. **Selection is host's concern** - plugins don't know about multi-provider
5. **Notifications not prescriptions** - host notifies, plugins decide response
6. **Cloud-native patterns** - readiness vs liveness, graceful degradation, service mesh-like routing without the mesh
