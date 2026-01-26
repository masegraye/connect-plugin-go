# Service Registry Guide

The Service Registry enables plugins to provide and consume services from each other, creating a coordinated multi-plugin platform.

## Live Example

See the Service Registry in action with the **Docker Compose URL Shortener** example:

```bash
cd examples/docker-compose
./setup.sh && ./run.sh && ./test.sh
```

This demonstrates:
- Plugin-to-plugin communication (API→Storage→Logger)
- Service discovery and registration
- Health-based readiness
- Host-mediated routing

See [Docker Compose Guide](docker-compose.md) for details.

## Overview

The Service Registry adds plugin-to-plugin communication:

```
┌─────────────────────────────────────────────────────────────┐
│                      Host Platform                           │
│  ┌──────────────────┐  ┌───────────────┐  ┌──────────────┐ │
│  │ ServiceRegistry  │  │ ServiceRouter │  │  DepGraph    │ │
│  │                  │  │               │  │              │ │
│  │ • Multi-provider │  │ • Mediated    │  │ • Topo sort  │ │
│  │ • Discovery      │  │   routing     │  │ • Impact     │ │
│  │ • Watch events   │  │ • Auth/health │  │   analysis   │ │
│  └──────────────────┘  └───────────────┘  └──────────────┘ │
└────────┬──────────────────────┬──────────────────┬──────────┘
         │                      │                  │
    ┌────┴────┐           ┌─────┴─────┐      ┌────┴────┐
    │ Logger  │           │   Cache   │      │   App   │
    │ Plugin  │           │  Plugin   │      │ Plugin  │
    │         │           │ requires  │      │requires │
    │provides │           │  logger   │      │  cache  │
    │ logger  │           │           │      │         │
    └─────────┘           └───────────┘      └─────────┘
```

## Service Declarations

Plugins declare what they provide and require:

```go
Metadata: connectplugin.PluginMetadata{
    Name:    "Cache Plugin",
    Version: "1.0.0",

    // What this plugin provides
    Provides: []connectplugin.ServiceDeclaration{
        {
            Type:    "cache",
            Version: "1.0.0",
            Path:    "/cache.v1.Cache/",
        },
    },

    // What this plugin requires
    Requires: []connectplugin.ServiceDependency{
        {
            Type:               "logger",
            MinVersion:         "1.0.0",
            RequiredForStartup: true,  // Block startup if unavailable
            WatchForChanges:    true,  // Notify on logger state changes
        },
    },
}
```

## Service Registration

After connecting, plugins register their services:

```go
regClient := client.RegistryClient()

req := connect.NewRequest(&connectpluginv1.RegisterServiceRequest{
    ServiceType:  "cache",
    Version:      "1.0.0",
    EndpointPath: "/cache.v1.Cache/",
})

// Include runtime identity in headers
req.Header().Set("X-Plugin-Runtime-ID", client.RuntimeID())
req.Header().Set("Authorization", "Bearer "+client.RuntimeToken())

resp, err := regClient.RegisterService(ctx, req)
```

## Service Discovery

Plugins discover dependencies via the registry:

```go
regClient := client.RegistryClient()

req := connect.NewRequest(&connectpluginv1.DiscoverServiceRequest{
    ServiceType: "logger",
    MinVersion:  "1.0.0",
})

req.Header().Set("X-Plugin-Runtime-ID", client.RuntimeID())
req.Header().Set("Authorization", "Bearer "+client.RuntimeToken())

resp, err := regClient.DiscoverService(ctx, req)

// Host returns single endpoint (already selected provider)
endpoint := resp.Msg.Endpoint
// endpoint.EndpointUrl = "/services/logger/logger-plugin-abc123"
```

## Calling Other Services

All plugin-to-plugin calls route through the host:

```go
// Discover logger service
endpoint, _ := regClient.DiscoverService(ctx, loggerDiscReq)

// Create logger client using host-routed endpoint
loggerClient := loggerv1connect.NewLoggerClient(
    httpClient,
    hostURL + endpoint.EndpointUrl,  // Routes through host
)

// Call logger service
loggerClient.Log(ctx, &loggerv1.LogRequest{
    Level:   "INFO",
    Message: "Cache started",
})
```

**Host routing path:**
```
Cache → Host /services/logger/logger-abc/Log → Logger
        ↓
  1. Validate caller token
  2. Check logger health
  3. Proxy to logger endpoint
  4. Log call (caller→provider→method)
```

## Multi-Provider Support

Multiple plugins can provide the same service:

```go
// Two logger instances register
logger1.RegisterService(ctx, "logger", "1.0.0")
logger2.RegisterService(ctx, "logger", "1.0.0")

// Host selects one using strategy
registry.SetSelectionStrategy("logger", connectplugin.SelectionRoundRobin)

// Each DiscoverService() returns different provider (round-robin)
endpoint1, _ := cache1.DiscoverService(ctx, "logger") // → logger1
endpoint2, _ := cache2.DiscoverService(ctx, "logger") // → logger2
```

**Selection strategies:**

- `SelectionFirst`: Always first provider
- `SelectionRoundRobin`: Rotate through providers
- `SelectionRandom`: Random provider
- `SelectionWeighted`: Based on load/health (future)

## Health States

Plugins report three health states:

### Healthy

Plugin is fully functional:

```go
client.ReportHealth(ctx,
    connectpluginv1.HealthState_HEALTH_STATE_HEALTHY,
    "all dependencies available",
    nil,
)
```

**Host behavior:**
- ✅ Routes traffic to plugin
- ✅ Includes in service discovery
- ✅ Counts toward availability

### Degraded

Plugin functions but with limitations:

```go
client.ReportHealth(ctx,
    connectpluginv1.HealthState_HEALTH_STATE_DEGRADED,
    "logger unavailable, using fallback",
    []string{"logger"},
)
```

**Host behavior:**
- ✅ Still routes traffic
- ⚠️ Plugin decides what to return (degraded responses)
- ✅ Includes in service discovery

**Example degraded behavior:**

- Cache without logger: Buffer logs, return data from cache
- API without cache: Direct database queries (slower)
- App without metrics: Skip metrics collection

### Unhealthy

Plugin cannot serve requests:

```go
client.ReportHealth(ctx,
    connectpluginv1.HealthState_HEALTH_STATE_UNHEALTHY,
    "database connection failed",
    []string{"database"},
)
```

**Host behavior:**
- ❌ Does NOT route traffic
- ❌ Excludes from service discovery
- ⚠️ Plugin stays alive (no restart)

## Watch for Dependency Changes

Plugins can watch for service availability changes:

```go
req := connect.NewRequest(&connectpluginv1.WatchServiceRequest{
    ServiceType: "logger",
})
req.Header().Set("X-Plugin-Runtime-ID", client.RuntimeID())
req.Header().Set("Authorization", "Bearer "+client.RuntimeToken())

stream, _ := regClient.WatchService(ctx, req)

for stream.Receive() {
    event := stream.Msg()

    switch event.State {
    case connectpluginv1.ServiceState_SERVICE_STATE_AVAILABLE:
        // Logger became available - reconnect
        onLoggerAvailable(event.Endpoint)

    case connectpluginv1.ServiceState_SERVICE_STATE_UNAVAILABLE:
        // Logger went away - degrade gracefully
        onLoggerUnavailable()
    }
}
```

## Dependency Graph

The host maintains a dependency graph for:

### Startup Ordering

Host computes topological sort for dependency-ordered startup:

```go
// Add plugins to graph
platform.AddPlugin(ctx, loggerConfig)  // No dependencies
platform.AddPlugin(ctx, cacheConfig)   // Requires: logger
platform.AddPlugin(ctx, appConfig)     // Requires: cache

// Get startup order
order, _ := platform.GetStartupOrder()
// → ["logger-abc", "cache-def", "app-ghi"]
```

### Impact Analysis

Before removing a plugin, analyze what will break:

```go
impact := platform.GetImpact("logger-abc")

// impact.AffectedServices: ["logger"]
// impact.AffectedPlugins: ["cache-def", "app-ghi"]
// impact.OptionalImpact: []

// Warn user or reject removal
if len(impact.AffectedPlugins) > 0 {
    fmt.Printf("Removing logger will affect %d plugins\n",
        len(impact.AffectedPlugins))
}
```

## Dynamic Lifecycle

### Add Plugin at Runtime

```go
err := platform.AddPlugin(ctx, connectplugin.PluginConfig{
    SelfID:      "metrics-plugin",
    SelfVersion: "1.0.0",
    Endpoint:    "http://localhost:8083",
    Metadata: connectplugin.PluginMetadata{
        Provides: []connectplugin.ServiceDeclaration{
            {Type: "metrics", Version: "1.0.0"},
        },
    },
})
```

Platform orchestrates:
1. Validate dependencies available
2. Call GetPluginInfo() (Managed)
3. Assign runtime_id and token
4. Wait for service registration
5. Wait for healthy state
6. Add to dependency graph
7. Notify watchers

### Remove Plugin

```go
err := platform.RemovePlugin(ctx, runtimeID)
```

Platform orchestrates:
1. Compute impact analysis
2. Notify dependent plugins (5s grace period)
3. Unregister all services
4. Request graceful shutdown
5. Remove from dependency graph

### Hot Reload (Zero Downtime)

```go
err := platform.ReplacePlugin(ctx, oldRuntimeID, newConfig)
```

Blue-green deployment:
1. Start new version in parallel
2. Wait for new version healthy
3. Register new endpoints
4. Atomic switch in router
5. Drain old version (finish in-flight requests)
6. Shutdown old version
7. Remove old from graph

## Best Practices

### Graceful Degradation

Plugins should degrade, not crash:

```go
func (p *CachePlugin) onLoggerUnavailable() {
    // Option 1: Degrade with fallback
    p.logger = newBufferingLogger()
    p.ReportHealth(DEGRADED, "buffering logs")

    // Option 2: Mark unhealthy (no traffic)
    // p.ReportHealth(UNHEALTHY, "logger required")

    // Option 3: Exit for restart (risky)
    // os.Exit(1)
}
```

### Watch for Recovery

Automatically recover when dependencies return:

```go
func (p *CachePlugin) onLoggerAvailable(endpoint *ServiceEndpoint) {
    // Reconnect to logger
    p.logger = loggerv1connect.NewLoggerClient(
        p.httpClient,
        p.hostURL + endpoint.EndpointUrl,
    )

    // Flush buffered logs
    p.flushBufferedLogs()

    // Report recovered
    p.ReportHealth(HEALTHY, "logger reconnected")
}
```

### Required vs Optional Dependencies

```go
Requires: []ServiceDependency{
    {
        Type:               "logger",
        RequiredForStartup: true,   // Block startup
        WatchForChanges:    true,   // Notify on changes
    },
    {
        Type:               "metrics",
        RequiredForStartup: false,  // Optional
        WatchForChanges:    true,
    },
}
```

**Required:** Plugin cannot start without it
**Optional:** Plugin degrades if unavailable

## Example: Complete Plugin Chain

See `examples/logger-plugin`, `examples/cache-plugin`, `examples/app-plugin` for complete implementations showing:

- Service registration
- Dependency discovery
- Health reporting
- Graceful degradation
- Watch notifications
- Both deployment models

Run integration tests:

```bash
task test:integration
```

## Next Steps

- [Interceptors Guide](interceptors.md) - Add retry, circuit breaker, auth
- [Configuration Reference](../reference/configuration.md) - All config options
- [API Reference](../reference/api.md) - Detailed API docs
