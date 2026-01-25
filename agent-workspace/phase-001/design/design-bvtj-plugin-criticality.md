# Design: Plugin Criticality & Failure Modes

**Issue:** KOR-bvtj
**Status:** Complete
**Dependencies:** KOR-ejeu

## Overview

Plugin criticality defines how the system behaves when a plugin becomes unavailable. Some plugins are essential to operation (authentication), while others enable optional features (analytics). This design specifies how criticality is declared and how failures are handled at startup.

## Design Goals

1. **Extreme simplicity**: Two criticality levels, not four
2. **Startup-time decisions**: Fail at startup or continue gracefully
3. **Application-owned state**: No framework-level "degraded mode" state machine
4. **Observable**: Plugin availability is visible and monitorable
5. **Simple defaults**: Most plugins should be optional

## Criticality Levels

```go
// Criticality defines how plugin failures are handled at startup.
type Criticality int

const (
    // CriticalityOptional - plugin failure is logged but app continues normally.
    // Features depending on this plugin become unavailable. Application should
    // implement fallback behavior (no-op implementation, degraded mode, etc.).
    // This is the default for all plugins.
    CriticalityOptional Criticality = iota

    // CriticalityRequired - plugin failure prevents app startup.
    // The application cannot function without this plugin.
    // Use for foundational plugins (authentication, primary database, etc.).
    CriticalityRequired
)
```

### When to Use Each Level

**Use CriticalityOptional (default) when:**
- The plugin enables optional features (analytics, metrics, caching)
- You can provide a fallback implementation (no-op, in-memory, degraded behavior)
- The application core functionality works without it
- Missing the feature is acceptable for users
- Examples: analytics, feature flags, non-critical caching, recommendations

**Use CriticalityRequired when:**
- The application fundamentally cannot operate without this plugin
- No reasonable fallback exists
- Users cannot accomplish their primary tasks without it
- Examples: authentication, primary data store, session management

**Default assumption:** When in doubt, use CriticalityOptional and implement a fallback. Most plugins should be optional.

## Configuration

Criticality is configured via a simple map in ClientConfig:

```go
type ClientConfig struct {
    // ... other fields

    // PluginCriticality maps plugin names to criticality levels.
    // Plugins not in this map default to CriticalityOptional.
    PluginCriticality map[string]Criticality

    // OnPluginUnavailable is called during startup when a plugin is unavailable.
    // Called for all criticality levels before Connect() returns.
    // For CriticalityRequired plugins, Connect() will return an error after this callback.
    OnPluginUnavailable func(plugin string, err error, criticality Criticality)
}

// Example: API service with required auth
client := connectplugin.NewClient(&connectplugin.ClientConfig{
    Endpoint: "http://plugin:8080",
    Plugins:  pluginSet,
    PluginCriticality: map[string]Criticality{
        "auth":      connectplugin.CriticalityRequired, // Fail startup without auth
        "kv":        connectplugin.CriticalityRequired, // Fail startup without KV
        "cache":     connectplugin.CriticalityOptional, // Continue without cache
        "analytics": connectplugin.CriticalityOptional, // Continue without analytics
    },
    OnPluginUnavailable: func(plugin string, err error, crit Criticality) {
        log.Warn("plugin unavailable at startup",
            "plugin", plugin,
            "error", err,
            "criticality", crit)
    },
})

// Connect() will fail if auth or kv are unavailable
if err := client.Connect(ctx); err != nil {
    // Required plugin missing - cannot start
    log.Fatal("cannot start without required plugins", "error", err)
}
```

## Failure Handling

### Startup Behavior

**CriticalityRequired:**
```go
func (c *Client) Connect(ctx context.Context) error {
    // Handshake
    resp, err := c.doHandshake(ctx)
    if err != nil {
        return err
    }

    // Check required plugins are available
    var missingRequired []string
    for pluginName, crit := range c.cfg.PluginCriticality {
        if crit != CriticalityRequired {
            continue
        }

        if !isPluginAvailable(resp.Plugins, pluginName) {
            missingRequired = append(missingRequired, pluginName)

            // Notify via callback
            if c.cfg.OnPluginUnavailable != nil {
                c.cfg.OnPluginUnavailable(
                    pluginName,
                    fmt.Errorf("plugin not available"),
                    crit,
                )
            }
        }
    }

    // Fail startup if any required plugin is missing
    if len(missingRequired) > 0 {
        return fmt.Errorf(
            "required plugins unavailable: %s",
            strings.Join(missingRequired, ", "),
        )
    }

    return nil
}
```

**CriticalityOptional:**
```go
// Optional plugins are checked at startup but don't block Connect()
func (c *Client) Connect(ctx context.Context) error {
    // ... handshake

    // Track which optional plugins are unavailable
    for pluginName, crit := range c.cfg.PluginCriticality {
        if crit != CriticalityOptional {
            continue
        }

        if !isPluginAvailable(resp.Plugins, pluginName) {
            // Mark as unavailable for Dispense() checks
            c.unavailablePlugins[pluginName] = true

            // Notify via callback
            if c.cfg.OnPluginUnavailable != nil {
                c.cfg.OnPluginUnavailable(
                    pluginName,
                    fmt.Errorf("plugin not available"),
                    crit,
                )
            }
        }
    }

    // Continue even if optional plugins are missing
    return nil
}
```

### Runtime Behavior

Criticality only affects **startup decisions**. After startup:

- **All plugins** return errors from `Dispense()` if unavailable
- **Applications** decide how to handle runtime failures
- **No framework-level degraded mode** - applications manage their own state

```go
// Application handles runtime failures via error checking
cacheSvc, err := connectplugin.DispenseTyped[cache.Service](client, "cache")
if err != nil {
    // Cache plugin unavailable - application decides what to do
    // Option 1: Use fallback
    cacheSvc = &inMemoryCache{}

    // Option 2: Enter application-level degraded mode
    app.setDegraded("cache unavailable")

    // Option 3: Return error to caller
    return fmt.Errorf("cache required: %w", err)
}
```

### Why No Runtime Criticality Enforcement?

The framework **does not** enforce criticality after startup because:

1. **Application context matters**: What's "critical" may depend on the operation
2. **State management complexity**: Framework-level degraded mode adds significant complexity
3. **Recovery is application-specific**: Each application knows best how to recover
4. **Observability is sufficient**: Health monitoring and metrics expose plugin status

Applications that need runtime degraded mode should implement it themselves:

```go
type Application struct {
    mu       sync.RWMutex
    degraded bool
    reason   string
}

func (a *Application) handlePluginFailure(pluginName string, err error) {
    if pluginName == "critical-service" {
        a.mu.Lock()
        a.degraded = true
        a.reason = fmt.Sprintf("%s failed: %v", pluginName, err)
        a.mu.Unlock()

        // Application-specific handling
        a.notifyOncall()
        a.updateHealthEndpoint()
    }
}
```

## Fallback Patterns

Applications should implement fallback behavior for optional plugins:

### Pattern 1: No-op Implementation

```go
// Application with optional analytics plugin
type Application struct {
    analytics analytics.Service
}

func NewApplication(client *connectplugin.Client) *Application {
    app := &Application{}

    // Try to get analytics plugin
    analyticsPlugin, err := connectplugin.DispenseTyped[analytics.Service](client, "analytics")
    if err != nil {
        // Optional plugin unavailable - use no-op fallback
        log.Info("analytics plugin unavailable, using no-op implementation")
        app.analytics = &noopAnalytics{}
    } else {
        app.analytics = analyticsPlugin
    }

    return app
}

// Noop implementation for optional feature
type noopAnalytics struct{}

func (n *noopAnalytics) Track(ctx context.Context, event *Event) error {
    // Do nothing - analytics disabled
    return nil
}
```

### Pattern 2: Degraded Implementation

```go
// Cache with in-memory fallback
type Application struct {
    cache cache.Service
}

func NewApplication(client *connectplugin.Client) *Application {
    app := &Application{}

    cachePlugin, err := connectplugin.DispenseTyped[cache.Service](client, "cache")
    if err != nil {
        // Redis cache unavailable - use in-memory fallback
        log.Warn("cache plugin unavailable, using in-memory fallback (degraded)")
        app.cache = newInMemoryCache()
    } else {
        app.cache = cachePlugin
    }

    return app
}
```

### Pattern 3: Error Propagation

```go
// Some operations require the plugin
func (a *Application) PerformOperation(ctx context.Context) error {
    // Try to get plugin
    svc, err := connectplugin.DispenseTyped[service.Service](a.client, "optional-service")
    if err != nil {
        // Plugin unavailable - this specific operation fails
        return fmt.Errorf("operation requires optional-service plugin: %w", err)
    }

    return svc.DoWork(ctx)
}
```

### fx Integration with Optional Dependencies

```go
type AppParams struct {
    fx.In

    Client *connectplugin.Client
}

func ProvideAuth(p AppParams) (auth.Service, error) {
    // Required plugin - must be available
    return connectplugin.DispenseTyped[auth.Service](p.Client, "auth")
}

func ProvideAnalytics(p AppParams) analytics.Service {
    // Optional plugin - fallback if unavailable
    svc, err := connectplugin.DispenseTyped[analytics.Service](p.Client, "analytics")
    if err != nil {
        log.Info("analytics unavailable, using no-op")
        return &noopAnalytics{}
    }
    return svc
}

func NewApplication(
    auth auth.Service,
    analytics analytics.Service,
) *Application {
    // Auth is guaranteed available (required)
    // Analytics may be no-op (optional)
    return &Application{
        auth:      auth,
        analytics: analytics,
    }
}
```

## Observability Hooks

The framework provides observability into plugin availability without enforcing runtime behavior:

### Plugin Status Tracking

```go
type Client struct {
    mu                 sync.RWMutex
    unavailablePlugins map[string]bool
}

// IsPluginAvailable reports whether a plugin is currently available.
// This is informational only - criticality is not enforced after startup.
func (c *Client) IsPluginAvailable(name string) bool {
    c.mu.RLock()
    defer c.mu.RUnlock()
    return !c.unavailablePlugins[name]
}

// ListUnavailablePlugins returns names of plugins that are unavailable.
func (c *Client) ListUnavailablePlugins() []string {
    c.mu.RLock()
    defer c.mu.RUnlock()

    var unavailable []string
    for name := range c.unavailablePlugins {
        unavailable = append(unavailable, name)
    }
    return unavailable
}
```

### Health Endpoint Integration

```go
func healthHandler(client *connectplugin.Client) http.HandlerFunc {
    return func(w http.ResponseWriter, r *http.Request) {
        unavailable := client.ListUnavailablePlugins()

        if len(unavailable) > 0 {
            w.WriteHeader(http.StatusServiceUnavailable)
            json.NewEncoder(w).Encode(map[string]any{
                "status":      "degraded",
                "unavailable": unavailable,
            })
            return
        }

        w.WriteHeader(http.StatusOK)
        json.NewEncoder(w).Encode(map[string]any{
            "status": "healthy",
        })
    }
}
```


## Failure Mode Catalog

| Failure | CriticalityRequired | CriticalityOptional |
|---------|---------------------|---------------------|
| **Startup: Plugin unavailable** | `Connect()` returns error, app fails to start | `Connect()` succeeds, plugin marked unavailable |
| **Startup: Handshake fails** | `Connect()` returns error | `Connect()` returns error |
| **Runtime: Dispense() called** | Returns error (app handles) | Returns error (app uses fallback) |
| **Runtime: Health check fails** | Status exposed via observability | Status exposed via observability |
| **Runtime: Plugin recovers** | Subsequent `Dispense()` succeeds | Subsequent `Dispense()` succeeds |

**Key principle:** Framework handles startup decisions. Applications handle runtime decisions.

### Metrics Integration

```go
// Prometheus metrics for plugin availability
var (
    pluginAvailability = promauto.NewGaugeVec(prometheus.GaugeOpts{
        Name: "connectplugin_available",
        Help: "1 if plugin is available, 0 if unavailable",
    }, []string{"plugin", "criticality"})

    pluginStartupFailures = promauto.NewCounterVec(prometheus.CounterOpts{
        Name: "connectplugin_startup_failures_total",
        Help: "Number of plugins that were unavailable at startup",
    }, []string{"plugin", "criticality"})
)

// In client initialization
func (c *Client) Connect(ctx context.Context) error {
    // ... check plugins

    for pluginName, crit := range c.cfg.PluginCriticality {
        available := isPluginAvailable(resp.Plugins, pluginName)

        // Record metrics
        if available {
            pluginAvailability.WithLabelValues(
                pluginName,
                crit.String(),
            ).Set(1)
        } else {
            pluginAvailability.WithLabelValues(
                pluginName,
                crit.String(),
            ).Set(0)

            pluginStartupFailures.WithLabelValues(
                pluginName,
                crit.String(),
            ).Inc()
        }
    }

    // ... rest of startup logic
}
```

## Example Configurations

### Microservice with Required Auth and Database

```go
client := connectplugin.NewClient(&connectplugin.ClientConfig{
    Endpoint: "http://plugin:8080",
    Plugins: connectplugin.PluginSet{
        "auth":      &authPlugin{},
        "database":  &databasePlugin{},
        "cache":     &cachePlugin{},
        "analytics": &analyticsPlugin{},
    },
    PluginCriticality: map[string]Criticality{
        "auth":     connectplugin.CriticalityRequired, // Cannot operate without auth
        "database": connectplugin.CriticalityRequired, // Cannot operate without DB
        // cache and analytics default to CriticalityOptional
    },
    OnPluginUnavailable: func(plugin string, err error, crit Criticality) {
        log.Warn("plugin unavailable",
            "plugin", plugin,
            "error", err,
            "criticality", crit)
    },
})

// Connect will fail if auth or database are unavailable
if err := client.Connect(ctx); err != nil {
    log.Fatal("cannot start application", "error", err)
}

// Application code can safely assume auth and database are available
auth, _ := connectplugin.DispenseTyped[auth.Service](client, "auth")
db, _ := connectplugin.DispenseTyped[database.Store](client, "database")

// Optional plugins may be unavailable - use fallbacks
cache, err := connectplugin.DispenseTyped[cache.Service](client, "cache")
if err != nil {
    cache = newInMemoryCache() // Fallback
}

analytics, err := connectplugin.DispenseTyped[analytics.Service](client, "analytics")
if err != nil {
    analytics = &noopAnalytics{} // Fallback
}
```

### CLI Tool with All Optional Plugins

```go
// CLI tools often work with degraded functionality
client := connectplugin.NewClient(&connectplugin.ClientConfig{
    Endpoint: "http://plugin:8080",
    Plugins:  pluginSet,
    // All plugins default to CriticalityOptional
    OnPluginUnavailable: func(plugin string, err error, crit Criticality) {
        fmt.Fprintf(os.Stderr, "Warning: %s unavailable, some features disabled\n", plugin)
    },
})

if err := client.Connect(ctx); err != nil {
    // Only handshake-level errors cause failure
    log.Fatal("cannot connect to plugin server", "error", err)
}

// Try to use features, degrade gracefully
if formatter, err := client.Dispense("formatter"); err == nil {
    // Pretty formatting available
    output = formatter.Format(data)
} else {
    // Fallback to basic formatting
    output = fmt.Sprintf("%v", data)
}
```

### Web Service with Phased Startup

```go
// Start with minimal required plugins, add optional ones later
client := connectplugin.NewClient(&connectplugin.ClientConfig{
    Endpoint: "http://plugin:8080",
    Plugins:  pluginSet,
    PluginCriticality: map[string]Criticality{
        "auth":    connectplugin.CriticalityRequired, // Needed for requests
        "session": connectplugin.CriticalityRequired, // Needed for requests
        // All others optional
    },
})

if err := client.Connect(ctx); err != nil {
    return fmt.Errorf("cannot start server: %w", err)
}

// Server can start - required plugins available
// Optional plugins might be unavailable, but we can still serve requests
server.Start()
```

## Plugin Recovery

When a plugin becomes available again after being unavailable:

```go
// Health monitoring updates plugin availability status
func (c *Client) updatePluginStatus(plugin string, available bool) {
    c.mu.Lock()
    defer c.mu.Unlock()

    wasUnavailable := c.unavailablePlugins[plugin]

    if available {
        delete(c.unavailablePlugins, plugin)

        if wasUnavailable {
            log.Info("plugin recovered", "plugin", plugin)
            pluginAvailability.WithLabelValues(
                plugin,
                c.cfg.PluginCriticality[plugin].String(),
            ).Set(1)
        }
    } else {
        c.unavailablePlugins[plugin] = true

        if !wasUnavailable {
            log.Warn("plugin became unavailable", "plugin", plugin)
            pluginAvailability.WithLabelValues(
                plugin,
                c.cfg.PluginCriticality[plugin].String(),
            ).Set(0)
        }
    }
}
```

### Application-Level Recovery Handling

Applications that implement their own degraded mode can react to recovery:

```go
type Application struct {
    client   *connectplugin.Client
    degraded atomic.Bool
}

func (a *Application) monitorPluginHealth(ctx context.Context) {
    ticker := time.NewTicker(10 * time.Second)
    defer ticker.Stop()

    for {
        select {
        case <-ctx.Done():
            return
        case <-ticker.C:
            // Check if previously unavailable plugins have recovered
            if a.degraded.Load() {
                unavailable := a.client.ListUnavailablePlugins()
                if len(unavailable) == 0 {
                    log.Info("all plugins recovered, exiting degraded mode")
                    a.degraded.Store(false)
                    a.onRecovered()
                }
            }
        }
    }
}

func (a *Application) onRecovered() {
    // Application-specific recovery logic
    metrics.RecordRecovery()
    alerting.ResolveIncident()
}
```

## Design Rationale

### Why Only Two Levels?

The original design had four levels (Optional, Required, Hard, Lazy). This was **too complex**:

1. **CriticalityHard** (runtime crash): Dangerous and unpredictable. Applications should decide crash policy.
2. **CriticalityLazy** (validate on first use): Niche use case that adds complexity. Apps can implement this themselves.
3. **Degraded mode state machine**: Framework-level state management adds significant complexity with minimal benefit.

**Two levels is sufficient:**
- **Optional**: Continue without plugin (99% of plugins)
- **Required**: Cannot start without plugin (authentication, primary DB)

### Why No Runtime Enforcement?

Criticality only affects **startup** because:

1. **Context matters**: Whether a failure is "critical" depends on the operation
2. **Application knows best**: Applications understand their own degradation needs
3. **State complexity**: Framework-level degraded mode adds too much complexity
4. **Observability is enough**: Health monitoring exposes status; apps decide what to do

### Comparison with go-plugin

| Aspect | go-plugin | connect-plugin |
|--------|-----------|----------------|
| **Criticality** | All or nothing (subprocess crash) | Per-plugin configurable |
| **Startup failure** | Client.Start() fails, process exits | Configurable per plugin |
| **Runtime failure** | Entire process exits | Returns errors, app handles |
| **Recovery** | Restart entire process | Automatic via health checks |
| **Observable** | Binary (running/crashed) | Per-plugin availability + metrics |
| **State management** | None (process is the state) | Applications manage their own state |

## Implementation Checklist

- [ ] `Criticality` enum with two values (Optional, Required)
- [ ] `PluginCriticality` map in `ClientConfig`
- [ ] `OnPluginUnavailable` callback in `ClientConfig`
- [ ] Startup validation in `Connect()` method
- [ ] `IsPluginAvailable()` and `ListUnavailablePlugins()` observability methods
- [ ] Plugin status tracking in health monitor
- [ ] Prometheus metrics for plugin availability
- [ ] Tests for required plugin startup failure
- [ ] Tests for optional plugin fallback patterns
- [ ] Documentation with clear guidance on choosing criticality levels

## Next Steps

1. Implement `Criticality` type and configuration in `client.go`
2. Add startup validation logic in `Connect()` method
3. Integrate with health monitoring for status tracking
4. Add observability methods (`IsPluginAvailable`, etc.)
5. Implement Prometheus metrics
6. Write comprehensive tests for both criticality levels
7. Document best practices and example patterns
