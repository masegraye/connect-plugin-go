# Design: Plugin Criticality & Failure Modes

**Issue:** KOR-bvtj
**Status:** Complete
**Dependencies:** KOR-ejeu

## Overview

Plugin criticality defines how the system behaves when a plugin becomes unavailable. Some plugins are essential to operation (authentication), while others enable optional features (analytics). This design specifies how criticality is declared, how failures are handled, and how degradation modes work.

## Design Goals

1. **Explicit criticality**: Clear declaration of plugin importance
2. **Graceful degradation**: Optional plugins fail gracefully
3. **Fail-fast for critical**: Required plugins fail early and loudly
4. **Observable**: Degraded state is visible and monitorable
5. **Simple defaults**: Most plugins should be optional

## Criticality Levels

```go
// Criticality defines how plugin failures are handled.
type Criticality int

const (
    // CriticalityOptional - plugin failure is logged but app continues normally.
    // Features depending on this plugin become unavailable.
    // This is the default.
    CriticalityOptional Criticality = iota

    // CriticalityRequired - plugin failure prevents app startup.
    // If plugin fails after startup, app enters degraded mode.
    CriticalityRequired

    // CriticalityHard - plugin failure causes app to crash.
    // Use sparingly for truly essential plugins (e.g., auth).
    CriticalityHard

    // CriticalityLazy - plugin not needed at startup but required on first use.
    // Startup proceeds even if plugin is down, but first Dispense() fails if unavailable.
    CriticalityLazy
)
```

## Configuration

### Per-Plugin Criticality

```go
type ClientConfig struct {
    // ... other fields

    // PluginCriticality maps plugin names to criticality levels.
    // Plugins not in this map default to CriticalityOptional.
    PluginCriticality map[string]Criticality

    // OnPluginFailure callback when a plugin fails.
    // Called for all criticality levels.
    OnPluginFailure func(plugin string, err error, criticality Criticality)

    // OnDegraded callback when app enters degraded mode.
    OnDegraded func(reason string)
}

// Example
client := connectplugin.NewClient(&connectplugin.ClientConfig{
    Endpoint: "http://plugin:8080",
    Plugins:  pluginSet,
    PluginCriticality: map[string]Criticality{
        "auth":      connectplugin.CriticalityHard,     // Crash if auth fails
        "kv":        connectplugin.CriticalityRequired, // Degrade if KV fails
        "analytics": connectplugin.CriticalityOptional, // Continue if analytics fails
    },
    OnPluginFailure: func(plugin string, err error, crit Criticality) {
        log.Error("plugin failed",
            "plugin", plugin,
            "error", err,
            "criticality", crit)
    },
    OnDegraded: func(reason string) {
        metrics.RecordDegradation(reason)
        alerting.NotifyOncall("Application degraded: " + reason)
    },
})
```

### Fluent API

```go
client := connectplugin.NewClient(&connectplugin.ClientConfig{
    Endpoint: "http://plugin:8080",
    Plugins:  pluginSet,
}).
    RequirePlugin("auth").      // CriticalityHard
    RequirePlugin("kv").        // CriticalityRequired
    OptionalPlugin("analytics") // CriticalityOptional
```

## Failure Handling

### Startup Failures

**CriticalityHard:**
```go
func (c *Client) Connect(ctx context.Context) error {
    // Handshake
    resp, err := c.doHandshake(ctx)
    if err != nil {
        return err
    }

    // Validate critical plugins are available
    for pluginName, crit := range c.cfg.PluginCriticality {
        if crit != CriticalityHard {
            continue
        }

        if !isPluginAvailable(resp.Plugins, pluginName) {
            return fmt.Errorf("critical plugin %q is unavailable", pluginName)
        }
    }

    return nil
}
```

**CriticalityRequired:**
```go
func (c *Client) Connect(ctx context.Context) error {
    // ... handshake

    // Check required plugins
    missingRequired := []string{}
    for pluginName, crit := range c.cfg.PluginCriticality {
        if crit != CriticalityRequired {
            continue
        }

        if !isPluginAvailable(resp.Plugins, pluginName) {
            missingRequired = append(missingRequired, pluginName)
        }
    }

    if len(missingRequired) > 0 {
        // Enter degraded mode
        c.setDegraded("Required plugins unavailable: " + strings.Join(missingRequired, ", "))
        if c.cfg.OnDegraded != nil {
            c.cfg.OnDegraded(c.degradedReason)
        }
    }

    return nil
}
```

**CriticalityOptional:**
```go
// No special handling at startup
// Dispense() will return error if plugin unavailable
```

**CriticalityLazy:**
```go
func (c *Client) Connect(ctx context.Context) error {
    // Skip validation for lazy plugins at startup
    return nil
}

func (c *Client) Dispense(name string) (any, error) {
    if crit := c.cfg.PluginCriticality[name]; crit == CriticalityLazy {
        // First access - validate now
        if !c.isPluginAvailable(name) {
            return nil, fmt.Errorf("lazy plugin %q is unavailable", name)
        }
    }
    // ... normal dispense
}
```

### Runtime Failures

When a plugin fails during operation (detected via health monitoring):

```go
type Client struct {
    // ... other fields
    degraded       bool
    degradedReason string
    mu             sync.RWMutex
}

func (c *Client) onHealthStatusChange(status HealthStatus) {
    c.mu.Lock()
    defer c.mu.Unlock()

    if status == HealthStatusUnhealthy {
        // Check criticality of failed plugin
        for pluginName, crit := range c.cfg.PluginCriticality {
            switch crit {
            case CriticalityHard:
                // Crash the app
                log.Fatal("Critical plugin failed", "plugin", pluginName)

            case CriticalityRequired:
                // Enter degraded mode
                if !c.degraded {
                    c.setDegraded(fmt.Sprintf("Required plugin %q failed", pluginName))
                    if c.cfg.OnDegraded != nil {
                        c.cfg.OnDegraded(c.degradedReason)
                    }
                }

            case CriticalityOptional:
                // Log but continue
                log.Warn("Optional plugin failed", "plugin", pluginName)
            }

            // Callback for all levels
            if c.cfg.OnPluginFailure != nil {
                c.cfg.OnPluginFailure(pluginName, fmt.Errorf("health check failed"), crit)
            }
        }
    }
}

func (c *Client) IsDegraded() bool {
    c.mu.RLock()
    defer c.mu.RUnlock()
    return c.degraded
}

func (c *Client) DegradedReason() string {
    c.mu.RLock()
    defer c.mu.RUnlock()
    return c.degradedReason
}
```

## Degraded Mode State Machine

```
┌──────────────┐
│   Healthy    │
│              │
│ - All plugins OK
│ - Full functionality
└──────┬───────┘
       │
       │ Required plugin fails
       │ OR health check fails
       │
       ▼
┌──────────────┐
│  Degraded    │
│              │
│ - Some features unavailable
│ - Monitoring alerts fired
└──────┬───────┘
       │
       │ All required plugins recover
       │ AND health checks pass
       │
       ▼
┌──────────────┐
│  Recovering  │
│              │
│ - Success threshold checks
│ - Gradual re-enable
└──────┬───────┘
       │
       │ Success threshold met
       │
       ▼
┌──────────────┐
│   Healthy    │
└──────────────┘
```

## Fallback Implementations

For optional plugins, provide fallback implementations:

```go
// Application with optional analytics plugin
type Application struct {
    analytics analytics.Service
}

func NewApplication(
    analyticsPlugin analytics.Service, // May be nil
) *Application {
    app := &Application{}

    if analyticsPlugin != nil {
        app.analytics = analyticsPlugin
    } else {
        app.analytics = &noopAnalytics{} // Fallback
    }

    return app
}

// Noop implementation
type noopAnalytics struct{}

func (n *noopAnalytics) Track(ctx context.Context, event *Event) error {
    // Do nothing
    return nil
}
```

### fx Integration with Optional Dependencies

```go
type AppParams struct {
    fx.In

    Auth      auth.Service                `name:"auth"`      // Required
    KV        kv.KVStore                  `name:"kv"`        // Required
    Analytics analytics.Service           `name:"analytics"` `optional:"true"` // Optional
}

func NewApplication(p AppParams) *Application {
    app := &Application{
        auth: p.Auth,
        kv:   p.KV,
    }

    if p.Analytics != nil {
        app.analytics = p.Analytics
    } else {
        app.analytics = &noopAnalytics{}
    }

    return app
}
```

## Circuit Breaker Integration

Criticality affects circuit breaker behavior:

```go
type PluginCircuitBreaker struct {
    criticality Criticality
    breaker     *CircuitBreaker
}

func (pcb *PluginCircuitBreaker) Allow() error {
    if err := pcb.breaker.Allow(); err != nil {
        switch pcb.criticality {
        case CriticalityHard:
            // Critical plugin - escalate to panic or app-level error
            return fmt.Errorf("critical plugin circuit open: %w", err)

        case CriticalityRequired:
            // Required plugin - return error, enter degraded mode
            return err

        case CriticalityOptional:
            // Optional - return error but don't propagate to app level
            return err

        case CriticalityLazy:
            // Same as required for lazy
            return err
        }
    }
    return nil
}
```

## Partial Availability

Plugins may be partially available (some methods work, others fail):

```go
// PluginStatus tracks per-method health
type PluginStatus struct {
    Overall     HealthStatus
    PerMethod   map[string]HealthStatus
    Degraded    bool
    DegradedAt  time.Time
    LastError   error
}

// Example: Cache plugin with degraded mode
type cacheService struct {
    redis *redis.Client
    fallback map[string][]byte // In-memory fallback
    degraded bool
}

func (c *cacheService) Get(ctx context.Context, key string) ([]byte, error) {
    if !c.degraded {
        val, err := c.redis.Get(ctx, key).Bytes()
        if err == nil {
            return val, nil
        }

        // Redis failed - enter degraded mode
        c.degraded = true
        log.Warn("Cache degraded, using in-memory fallback")
    }

    // Use fallback
    return c.fallback[key], nil
}

func (c *cacheService) HealthCheck() HealthStatus {
    if err := c.redis.Ping(ctx).Err(); err != nil {
        return HealthStatusDegraded // Not fully down, but degraded
    }
    c.degraded = false
    return HealthStatusHealthy
}
```

## Failure Mode Catalog

| Failure | CriticalityHard | CriticalityRequired | CriticalityOptional | CriticalityLazy |
|---------|-----------------|---------------------|---------------------|-----------------|
| **Startup unavailable** | Fail startup | Enter degraded | Continue | Continue |
| **Health check fails** | Crash app | Enter degraded | Log warning | N/A (not checked) |
| **Circuit opens** | Propagate error | Enter degraded | Return fallback | Propagate error |
| **Transient error** | Retry, then crash | Retry, then degrade | Retry, then fallback | Retry, then error |
| **Plugin recovers** | Resume | Exit degraded | Resume | Resume |

## Degradation Signaling

### HTTP Header

Degraded applications should signal their state:

```go
// Middleware adds degradation header
func DegradationMiddleware(client *connectplugin.Client) func(http.Handler) http.Handler {
    return func(next http.Handler) http.Handler {
        return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
            if client.IsDegraded() {
                w.Header().Set("X-Service-Degraded", "true")
                w.Header().Set("X-Degraded-Reason", client.DegradedReason())
            }
            next.ServeHTTP(w, r)
        })
    }
}
```

### Health Endpoint

```go
// /healthz returns 200 if alive, even if degraded
// /readyz returns 503 if degraded
func healthzHandler(client *connectplugin.Client) http.HandlerFunc {
    return func(w http.ResponseWriter, r *http.Request) {
        if client.IsDegraded() {
            w.WriteHeader(http.StatusServiceUnavailable)
            json.NewEncoder(w).Encode(map[string]any{
                "status":   "degraded",
                "reason":   client.DegradedReason(),
                "critical": false, // Still serving
            })
            return
        }
        w.WriteHeader(http.StatusOK)
        w.Write([]byte("ok"))
    }
}
```

### Metrics

```go
// Prometheus metrics for degradation
var (
    degradedGauge = promauto.NewGauge(prometheus.GaugeOpts{
        Name: "app_degraded",
        Help: "1 if application is in degraded mode, 0 otherwise",
    })

    pluginAvailability = promauto.NewGaugeVec(prometheus.GaugeOpts{
        Name: "plugin_available",
        Help: "1 if plugin is available, 0 otherwise",
    }, []string{"plugin", "criticality"})
)

func (c *Client) onHealthStatusChange(status HealthStatus) {
    // ... degradation logic

    if c.degraded {
        degradedGauge.Set(1)
    } else {
        degradedGauge.Set(0)
    }
}
```

## Example Configurations

### Microservice with Critical Auth

```go
client := connectplugin.NewClient(&connectplugin.ClientConfig{
    Endpoint: "http://plugin:8080",
    Plugins: connectplugin.PluginSet{
        "auth":      &authPlugin{},
        "kv":        &kvPlugin{},
        "cache":     &cachePlugin{},
        "analytics": &analyticsPlugin{},
    },
    PluginCriticality: map[string]Criticality{
        "auth":      connectplugin.CriticalityHard,     // Must have
        "kv":        connectplugin.CriticalityRequired, // Degrade without
        "cache":     connectplugin.CriticalityOptional, // Nice to have
        "analytics": connectplugin.CriticalityOptional, // Nice to have
    },
    OnDegraded: func(reason string) {
        log.Error("Service degraded", "reason", reason)
        pagerduty.Alert("Production service degraded: " + reason)
    },
})
```

### CLI Tool with All Optional

```go
// CLI tools often don't need strict requirements
client := connectplugin.NewClient(&connectplugin.ClientConfig{
    Endpoint: "http://plugin:8080",
    Plugins:  pluginSet,
    // All default to CriticalityOptional
    OnPluginFailure: func(plugin string, err error, crit Criticality) {
        fmt.Fprintf(os.Stderr, "Warning: Plugin %s unavailable: %v\n", plugin, err)
        fmt.Fprintf(os.Stderr, "Continuing with reduced functionality.\n")
    },
})
```

### Batch Job with Lazy Dependencies

```go
client := connectplugin.NewClient(&connectplugin.ClientConfig{
    Endpoint:    "http://plugin:8080",
    LazyConnect: true,
    Plugins:     pluginSet,
    PluginCriticality: map[string]Criticality{
        "email": connectplugin.CriticalityLazy, // Only needed if job fails
    },
})

// Job runs
func runJob() error {
    if err := processData(); err != nil {
        // Only now do we need email plugin
        emailSvc, err := connectplugin.DispenseTyped[email.Service](client, "email")
        if err != nil {
            log.Error("Cannot send failure notification", "error", err)
            return err
        }
        emailSvc.SendAlert(ctx, err.Error())
    }
    return nil
}
```

## Recovery Behavior

### Required Plugin Recovery

When a required plugin recovers, exit degraded mode:

```go
func (c *Client) onHealthStatusChange(plugin string, status HealthStatus) {
    c.mu.Lock()
    defer c.mu.Unlock()

    if status == HealthStatusHealthy {
        crit := c.cfg.PluginCriticality[plugin]
        if crit == CriticalityRequired && c.degraded {
            // Check if all required plugins are now healthy
            if c.allRequiredPluginsHealthy() {
                log.Info("Exiting degraded mode - all required plugins recovered")
                c.degraded = false
                c.degradedReason = ""
                degradedGauge.Set(0)
            }
        }
    }
}
```

### Recovery Threshold

Prevent flapping between healthy and degraded:

```go
type RecoveryConfig struct {
    // SuccessThreshold consecutive health checks before exiting degraded mode
    SuccessThreshold int

    // MinDegradedDuration before allowing recovery
    MinDegradedDuration time.Duration
}

func (c *Client) canExitDegraded() bool {
    if !c.degraded {
        return false
    }

    // Must be degraded for minimum duration
    if time.Since(c.degradedAt) < c.cfg.RecoveryConfig.MinDegradedDuration {
        return false
    }

    // All required plugins must have consecutive successes
    for pluginName, crit := range c.cfg.PluginCriticality {
        if crit == CriticalityRequired {
            if c.healthMonitor.ConsecutiveSuccesses(pluginName) < c.cfg.RecoveryConfig.SuccessThreshold {
                return false
            }
        }
    }

    return true
}
```

## Comparison with go-plugin

| Aspect | go-plugin | connect-plugin |
|--------|-----------|----------------|
| **Criticality** | All or nothing (subprocess) | Per-plugin configurable |
| **Startup failure** | Entire client fails | Configurable per plugin |
| **Runtime failure** | Process exits | Degraded mode or crash |
| **Recovery** | Restart process | Automatic via health checks |
| **Observable** | Binary (up/down) | Degraded state + metrics |

## Implementation Checklist

- [x] Criticality enum definition
- [x] Per-plugin criticality configuration
- [x] Startup failure handling (hard, required, optional, lazy)
- [x] Runtime failure handling via health monitoring
- [x] Degraded mode state management
- [x] Recovery behavior with threshold
- [x] Degradation signaling (HTTP headers, metrics)
- [x] Fallback implementation pattern
- [x] fx integration with optional dependencies
- [x] Circuit breaker integration
- [x] Example configurations

## Next Steps

1. Implement criticality types in `client.go`
2. Integrate with health monitoring
3. Add degradation metrics
4. Write tests for each criticality level
5. Document best practices for choosing criticality
