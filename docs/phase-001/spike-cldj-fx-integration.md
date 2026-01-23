# Spike: fx Integration Patterns

**Issue:** KOR-cldj
**Status:** Complete

## Executive Summary

uber-go/fx is a dependency injection framework that enables modular application composition. For connect-plugin, fx integration allows plugins to be treated as injectable dependencies, with their lifecycles managed by fx. The key insight is that a plugin IS the component - the plugin system provides proxies that implement Go interfaces and can be injected like any other dependency.

## fx Fundamentals

### Core Concepts

1. **Provide**: Register a constructor that creates a dependency
2. **Invoke**: Call a function with injected dependencies (causes construction)
3. **Module**: Named group of options (scoped provides/invokes)
4. **Lifecycle**: OnStart/OnStop hooks for managing long-running services
5. **In/Out**: Marker types for parameter/result structs
6. **Annotations**: Tags for named dependencies and groups

### Basic Usage

```go
fx.New(
    fx.Provide(NewLogger),           // Register constructor
    fx.Provide(NewDatabase),         // Dependencies resolved automatically
    fx.Invoke(func(db *Database) {   // Trigger construction
        // Application code
    }),
).Run()
```

### Lifecycle Hooks

```go
func NewServer(lc fx.Lifecycle, db *Database) *http.Server {
    srv := &http.Server{Handler: newHandler(db)}

    lc.Append(fx.Hook{
        OnStart: func(ctx context.Context) error {
            go srv.ListenAndServe()
            return nil
        },
        OnStop: func(ctx context.Context) error {
            return srv.Shutdown(ctx)
        },
    })

    return srv
}
```

### Value Groups

Collect multiple implementations of an interface:

```go
// Provider uses group tag
func NewFooHandler() Route { ... }
fx.Provide(fx.Annotate(NewFooHandler, fx.ResultTags(`group:"routes"`)))

// Consumer receives slice
func NewMux(routes []Route) *http.ServeMux {
    mux := http.NewServeMux()
    for _, route := range routes {
        mux.Handle(route.Pattern(), route)
    }
    return mux
}
fx.Provide(fx.Annotate(NewMux, fx.ParamTags(`group:"routes"`)))
```

## Plugin Integration Patterns

### Pattern 1: Plugin as Proxy (Recommended)

The plugin client creates a proxy that implements a Go interface. This proxy is provided to fx and can be injected anywhere the interface is needed.

```go
// The KVStore interface (defined by application)
type KVStore interface {
    Get(ctx context.Context, key string) ([]byte, error)
    Put(ctx context.Context, key string, value []byte) error
}

// Plugin client creates a proxy that implements KVStore
func NewKVStorePlugin(endpoint string) func(fx.Lifecycle) (KVStore, error) {
    return func(lc fx.Lifecycle) (KVStore, error) {
        client, err := connectplugin.NewClient(endpoint,
            connectplugin.WithPlugins(map[string]connectplugin.Plugin{
                "kv": &kvPlugin{},
            }),
        )
        if err != nil {
            return nil, err
        }

        lc.Append(fx.Hook{
            OnStart: func(ctx context.Context) error {
                return client.Connect(ctx)
            },
            OnStop: func(ctx context.Context) error {
                return client.Close()
            },
        })

        // Dispense returns a proxy implementing KVStore
        raw, err := client.Dispense("kv")
        if err != nil {
            return nil, err
        }
        return raw.(KVStore), nil
    }
}

// Usage in application
fx.New(
    fx.Provide(NewKVStorePlugin("http://kvstore-plugin:8080")),
    fx.Invoke(func(store KVStore) {
        // Use the plugin like any other KVStore
        store.Put(ctx, "key", []byte("value"))
    }),
).Run()
```

### Pattern 2: Plugin Module

Package plugin configuration as a reusable fx.Module:

```go
// plugins/kv/module.go
package kv

var Module = func(endpoint string) fx.Option {
    return fx.Module("kv-plugin",
        fx.Provide(NewKVStorePlugin(endpoint)),
        fx.Decorate(wrapWithMetrics),  // Add metrics
    )
}

// Metric decorator
func wrapWithMetrics(store KVStore, metrics *prometheus.Registry) KVStore {
    return &metricsWrapper{store: store, registry: metrics}
}

// Usage
fx.New(
    kv.Module("http://kvstore-plugin:8080"),
    fx.Invoke(func(store KVStore) { ... }),
)
```

### Pattern 3: Plugin Set via Value Groups

Register multiple plugins that implement the same interface:

```go
// Define helper for plugin annotation
func AsPlugin(f any) any {
    return fx.Annotate(f,
        fx.As(new(Plugin)),
        fx.ResultTags(`group:"plugins"`),
    )
}

// Register multiple plugins
fx.Provide(
    AsPlugin(NewKVPlugin("http://kv-plugin:8080")),
    AsPlugin(NewAuthPlugin("http://auth-plugin:8080")),
    AsPlugin(NewCachePlugin("http://cache-plugin:8080")),
)

// PluginManager receives all plugins
func NewPluginManager(plugins []Plugin) *PluginManager {
    return &PluginManager{plugins: plugins}
}

fx.Provide(fx.Annotate(NewPluginManager, fx.ParamTags(`group:"plugins"`)))
```

### Pattern 4: Optional Plugin Dependencies

Handle plugins that may not be available:

```go
type Params struct {
    fx.In

    // Required dependency
    Logger *zap.Logger

    // Optional plugin (nil if not available)
    Cache CachePlugin `optional:"true"`
}

func NewService(p Params) *Service {
    s := &Service{logger: p.Logger}

    if p.Cache != nil {
        s.cache = p.Cache
    } else {
        s.cache = &noopCache{}  // Fallback
    }

    return s
}
```

### Pattern 5: Named Plugin Variants

Multiple instances of the same plugin type:

```go
fx.Provide(
    fx.Annotate(
        NewKVStorePlugin("http://primary-kv:8080"),
        fx.ResultTags(`name:"primary"`),
    ),
    fx.Annotate(
        NewKVStorePlugin("http://replica-kv:8080"),
        fx.ResultTags(`name:"replica"`),
    ),
)

// Consumer requests specific instance
type Params struct {
    fx.In

    Primary KVStore `name:"primary"`
    Replica KVStore `name:"replica"`
}
```

## connect-plugin fx Module Design

### Module Structure

```go
// connectpluginfx/module.go
package connectpluginfx

// Config for a single plugin
type PluginConfig struct {
    Name     string
    Endpoint string
    Critical bool  // If true, startup fails if plugin unavailable
    Timeout  time.Duration
}

// Options for the module
type Options struct {
    Plugins          []PluginConfig
    DefaultTimeout   time.Duration
    HealthCheckInterval time.Duration
}

// Module returns an fx.Option that provides plugin clients
func Module(opts Options) fx.Option {
    providers := make([]any, 0, len(opts.Plugins))

    for _, cfg := range opts.Plugins {
        cfg := cfg  // capture
        providers = append(providers, fx.Annotate(
            newPluginClient(cfg, opts),
            fx.ResultTags(fmt.Sprintf(`name:"%s"`, cfg.Name)),
        ))
    }

    return fx.Module("connect-plugin",
        fx.Provide(providers...),
        fx.Provide(newPluginRegistry),  // Central registry
        fx.Invoke(healthCheckLoop),      // Start health checking
    )
}
```

### Plugin Client Constructor

```go
func newPluginClient(cfg PluginConfig, opts Options) func(fx.Lifecycle, *zap.Logger) (*connectplugin.Client, error) {
    return func(lc fx.Lifecycle, log *zap.Logger) (*connectplugin.Client, error) {
        timeout := cfg.Timeout
        if timeout == 0 {
            timeout = opts.DefaultTimeout
        }

        client, err := connectplugin.NewClient(cfg.Endpoint,
            connectplugin.WithTimeout(timeout),
            connectplugin.WithLogger(log),
        )
        if err != nil {
            if cfg.Critical {
                return nil, fmt.Errorf("critical plugin %s failed: %w", cfg.Name, err)
            }
            log.Warn("optional plugin unavailable", zap.String("name", cfg.Name), zap.Error(err))
            return nil, nil  // Return nil for optional plugins
        }

        lc.Append(fx.Hook{
            OnStart: func(ctx context.Context) error {
                if err := client.Connect(ctx); err != nil {
                    if cfg.Critical {
                        return err
                    }
                    log.Warn("plugin connect failed", zap.String("name", cfg.Name), zap.Error(err))
                }
                return nil
            },
            OnStop: func(ctx context.Context) error {
                return client.Close()
            },
        })

        return client, nil
    }
}
```

### Plugin Registry

```go
// Registry provides access to all plugins by name
type Registry struct {
    clients map[string]*connectplugin.Client
    mu      sync.RWMutex
}

func newPluginRegistry() *Registry {
    return &Registry{
        clients: make(map[string]*connectplugin.Client),
    }
}

func (r *Registry) Register(name string, client *connectplugin.Client) {
    r.mu.Lock()
    defer r.mu.Unlock()
    r.clients[name] = client
}

func (r *Registry) Get(name string) (*connectplugin.Client, bool) {
    r.mu.RLock()
    defer r.mu.RUnlock()
    c, ok := r.clients[name]
    return c, ok
}

func (r *Registry) Dispense(name, pluginType string) (any, error) {
    client, ok := r.Get(name)
    if !ok {
        return nil, fmt.Errorf("plugin %q not found", name)
    }
    return client.Dispense(pluginType)
}
```

### Typed Plugin Accessors

```go
// ProvideTypedPlugin creates a constructor for a specific plugin interface
func ProvideTypedPlugin[T any](name, pluginType string) any {
    return func(registry *Registry) (T, error) {
        raw, err := registry.Dispense(name, pluginType)
        if err != nil {
            var zero T
            return zero, err
        }
        typed, ok := raw.(T)
        if !ok {
            var zero T
            return zero, fmt.Errorf("plugin %q does not implement %T", name, zero)
        }
        return typed, nil
    }
}

// Usage
fx.Provide(
    connectpluginfx.ProvideTypedPlugin[KVStore]("kv-plugin", "kv"),
    connectpluginfx.ProvideTypedPlugin[AuthService]("auth-plugin", "auth"),
)
```

## Integration with Plugin Capabilities

### Host Capabilities from fx Container

The host can provide capabilities to plugins by exposing fx-managed services:

```go
// Host exposes fx services as plugin capabilities
func NewCapabilityBroker(
    logger *zap.Logger,
    metrics *prometheus.Registry,
    store KVStore,  // Could be another plugin!
) *connectplugin.CapabilityBroker {
    broker := connectplugin.NewCapabilityBroker()

    // Wrap fx dependencies as capabilities
    broker.RegisterCapability("logger", &LoggerCapability{Logger: logger})
    broker.RegisterCapability("metrics", &MetricsCapability{Registry: metrics})
    broker.RegisterCapability("storage", &StorageCapability{Store: store})

    return broker
}

fx.Provide(NewCapabilityBroker)
```

### Circular Dependency Handling

When plugins need host services that depend on other plugins:

```go
// Use lazy initialization to break cycles
type LazyKVStore struct {
    once    sync.Once
    store   KVStore
    resolve func() KVStore
}

func (l *LazyKVStore) Get(ctx context.Context, key string) ([]byte, error) {
    l.once.Do(func() { l.store = l.resolve() })
    return l.store.Get(ctx, key)
}

// Provide lazy version to break cycle
fx.Provide(func(in struct {
    fx.In
    Store KVStore `optional:"true"`
}) *LazyKVStore {
    return &LazyKVStore{
        resolve: func() KVStore { return in.Store },
    }
})
```

## Error Handling

### Startup Error Patterns

```go
// ErrorHandler for plugin failures
type PluginErrorHandler struct {
    log      *zap.Logger
    alerter  AlertService
    critical []string
}

func (h *PluginErrorHandler) HandleError(err error) {
    var pluginErr *connectplugin.Error
    if errors.As(err, &pluginErr) {
        h.log.Error("plugin error",
            zap.String("plugin", pluginErr.Plugin),
            zap.Error(pluginErr.Cause),
        )

        if slices.Contains(h.critical, pluginErr.Plugin) {
            h.alerter.Critical("Critical plugin failed: " + pluginErr.Plugin)
        }
    }
}

fx.New(
    fx.ErrorHook(&PluginErrorHandler{...}),
    // ...
)
```

### Graceful Degradation

```go
// Service that handles plugin unavailability
type Service struct {
    cache  CachePlugin  // May be nil
    logger *zap.Logger
}

func (s *Service) GetWithCache(ctx context.Context, key string) ([]byte, error) {
    if s.cache != nil {
        if cached, err := s.cache.Get(ctx, key); err == nil {
            return cached, nil
        }
    }

    // Fall through to primary source
    return s.getPrimary(ctx, key)
}
```

## Testing with fx

### Test Doubles

```go
func TestService(t *testing.T) {
    var svc *Service

    app := fxtest.New(t,
        fx.Provide(NewService),
        // Replace plugin with test double
        fx.Provide(func() KVStore {
            return &mockKVStore{
                data: map[string][]byte{"key": []byte("value")},
            }
        }),
        fx.Populate(&svc),
    )

    app.RequireStart()
    defer app.RequireStop()

    result, err := svc.Get(context.Background(), "key")
    require.NoError(t, err)
    assert.Equal(t, []byte("value"), result)
}
```

### Integration Testing

```go
func TestPluginIntegration(t *testing.T) {
    // Start real plugin in test container
    container := testcontainers.RunContainer(t, "my-plugin:test")

    app := fxtest.New(t,
        connectpluginfx.Module(connectpluginfx.Options{
            Plugins: []connectpluginfx.PluginConfig{
                {Name: "test-kv", Endpoint: container.Endpoint()},
            },
        }),
        fx.Provide(NewService),
        fx.Populate(&svc),
    )

    app.RequireStart()
    defer app.RequireStop()

    // Test with real plugin
}
```

## Module API Design

### Final Module Interface

```go
// connectpluginfx.Module returns fx options for plugin integration
func Module(opts ...Option) fx.Option

// Options
type Option func(*config)

func WithPlugin(name, endpoint string) Option
func WithCriticalPlugin(name, endpoint string) Option
func WithTimeout(d time.Duration) Option
func WithHealthCheck(interval time.Duration) Option
func WithCapabilities(caps ...Capability) Option
func WithInterceptors(interceptors ...connectplugin.Interceptor) Option

// Usage
fx.New(
    connectpluginfx.Module(
        connectpluginfx.WithPlugin("kv", "http://kv-plugin:8080"),
        connectpluginfx.WithCriticalPlugin("auth", "http://auth-plugin:8080"),
        connectpluginfx.WithTimeout(5 * time.Second),
        connectpluginfx.WithHealthCheck(30 * time.Second),
        connectpluginfx.WithCapabilities(
            connectpluginfx.LoggerCapability(),
            connectpluginfx.MetricsCapability(),
        ),
    ),
    // Typed accessors
    fx.Provide(
        connectpluginfx.ProvideTypedPlugin[KVStore]("kv", "kv"),
        connectpluginfx.ProvideTypedPlugin[AuthService]("auth", "auth"),
    ),
)
```

## Conclusions

1. **Plugin IS the component**: Plugins provide proxies implementing Go interfaces
2. **Lifecycle hooks essential**: Connect/disconnect tied to fx.Lifecycle
3. **Value groups for multiple plugins**: Collect plugins implementing same interface
4. **Named variants for multiple instances**: Same plugin type, different endpoints
5. **Optional dependencies for graceful degradation**: Handle unavailable plugins
6. **Capabilities from fx container**: Host services exposed via capability broker

## Next Steps

1. Implement `connectpluginfx` package
2. Add support for plugin discovery (Kubernetes, Consul)
3. Implement health check integration
4. Add metrics and tracing interceptors
5. Create example applications with fx integration
