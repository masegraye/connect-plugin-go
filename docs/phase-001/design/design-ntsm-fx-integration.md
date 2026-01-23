# Design: fx Integration Layer

**Issue:** KOR-ntsm
**Status:** Complete
**Dependencies:** KOR-cldj, KOR-gfuh

## Overview

The fx integration layer wraps connect-plugin with uber-go/fx dependency injection, making plugins behave like any other injectable dependency. This is a separate optional layer on top of core connect-plugin - you can use connect-plugin without fx, but fx integration provides ergonomic DI and makes testing much simpler.

## Design Goals

1. **Optional layer**: Works with or without fx
2. **Ergonomic**: Plugins as simple fx.Provide sources
3. **Lifecycle integration**: Plugin Connect/Close map to fx OnStart/OnStop
4. **Type-safe**: Provide specific interfaces, not raw Plugin
5. **Test-friendly**: Easy to wire up test scenarios with fx.Module

## Package Structure

```
connectpluginfx/
  module.go         - PluginModule() and helpers
  client.go         - Client provider
  server.go         - Server provider
  typed.go          - Type-safe dispensing
  testing.go        - Test helpers
```

## Core API

### PluginModule for Client

Wraps a plugin client as an fx module:

```go
// PluginModule creates an fx module that provides a plugin client.
func PluginModule(cfg *connectplugin.ClientConfig, opts ...ModuleOption) fx.Option {
    return fx.Module("connect-plugin",
        // Provide the client
        fx.Provide(newPluginClient(cfg)),

        // Lifecycle hooks
        fx.Invoke(registerClientLifecycle),
    )
}

// Internal: creates the plugin client
func newPluginClient(cfg *connectplugin.ClientConfig) func(lc fx.Lifecycle) (*connectplugin.Client, error) {
    return func(lc fx.Lifecycle) (*connectplugin.Client, error) {
        client, err := connectplugin.NewClient(cfg)
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

        return client, nil
    }
}
```

### ProvideTypedPlugin

Provides a specific typed interface from a plugin:

```go
// ProvideTypedPlugin creates an fx.Option that provides interface I from a plugin.
func ProvideTypedPlugin[I any](pluginName string) fx.Option {
    return fx.Provide(func(client *connectplugin.Client) (I, error) {
        return connectplugin.DispenseTyped[I](client, pluginName)
    })
}

// Usage
fx.New(
    connectpluginfx.PluginModule(kvClientConfig),
    connectpluginfx.ProvideTypedPlugin[kv.KVStore]("kv"),

    fx.Invoke(func(store kv.KVStore) {
        // Use the plugin like any other dependency
        store.Put(ctx, "key", []byte("value"))
    }),
).Run()
```

### PluginServerModule

Wraps a plugin server as an fx module:

```go
// PluginServerModule creates an fx module that serves plugins.
func PluginServerModule(cfg *connectplugin.ServeConfig) fx.Option {
    return fx.Module("connect-plugin-server",
        fx.Invoke(func(lc fx.Lifecycle) error {
            lc.Append(fx.Hook{
                OnStart: func(ctx context.Context) error {
                    // Start serving in background
                    stopCh := make(chan struct{})
                    go func() {
                        connectplugin.Serve(cfg)
                    }()

                    // Wait for server ready
                    return waitForServerReady(cfg.Addr, 5*time.Second)
                },
                OnStop: func(ctx context.Context) error {
                    // Trigger graceful shutdown
                    close(cfg.StopCh)
                    return nil
                },
            })
            return nil
        }),
    )
}
```

## Test Harness Patterns

### Pattern 1: In-Process Plugin Testing

```go
func TestKVPlugin(t *testing.T) {
    app := fxtest.New(t,
        // Serve plugin in background
        connectpluginfx.PluginServerModule(&connectplugin.ServeConfig{
            Addr: "localhost:18080",
            Plugins: connectplugin.PluginSet{
                "kv": &kvplugin.KVServicePlugin{},
            },
            Impls: map[string]any{
                "kv": &testKVStore{data: make(map[string][]byte)},
            },
        }),

        // Client to plugin
        connectpluginfx.PluginModule(&connectplugin.ClientConfig{
            Endpoint: "http://localhost:18080",
            Plugins: connectplugin.PluginSet{
                "kv": &kvplugin.KVServicePlugin{},
            },
        }),

        // Provide typed interface
        connectpluginfx.ProvideTypedPlugin[kv.KVStore]("kv"),

        // Test code
        fx.Invoke(func(store kv.KVStore) {
            err := store.Put(context.Background(), "test", []byte("value"))
            require.NoError(t, err)

            val, err := store.Get(context.Background(), "test")
            require.NoError(t, err)
            assert.Equal(t, []byte("value"), val)
        }),
    )

    app.RequireStart()
    defer app.RequireStop()
}
```

### Pattern 2: Multi-Plugin Test Scenario

```go
func TestPluginComposition(t *testing.T) {
    app := fxtest.New(t,
        // Logger plugin server
        connectpluginfx.PluginServerModule(&connectplugin.ServeConfig{
            Addr:    "localhost:18081",
            Plugins: connectplugin.PluginSet{"logger": &loggerPlugin{}},
            Impls:   map[string]any{"logger": &testLogger{}},
        }),

        // App plugin server (uses logger)
        connectpluginfx.PluginServerModule(&connectplugin.ServeConfig{
            Addr:    "localhost:18082",
            Plugins: connectplugin.PluginSet{"app": &appPlugin{}},
            Impls:   map[string]any{"app": &testApp{}},
            HostCapabilities: map[string]connectplugin.CapabilityHandler{
                "service_registry": testServiceRegistry,
            },
        }),

        // Client to app plugin
        connectpluginfx.PluginModule(&connectplugin.ClientConfig{
            Endpoint: "http://localhost:18082",
            Plugins:  connectplugin.PluginSet{"app": &appPlugin{}},
        }),

        connectpluginfx.ProvideTypedPlugin[app.Service]("app"),

        fx.Invoke(func(svc app.Service) {
            // App plugin internally uses logger plugin via service registry
            result, err := svc.DoWork(context.Background(), &WorkRequest{})
            require.NoError(t, err)
        }),
    )

    app.RequireStart()
    defer app.RequireStop()
}
```

### Pattern 3: Mock Plugin for Testing

```go
func TestApplicationWithMockPlugin(t *testing.T) {
    mockStore := &mockKVStore{
        data: map[string][]byte{"key": []byte("value")},
    }

    app := fxtest.New(t,
        // Provide mock directly (no plugin layer)
        fx.Provide(func() kv.KVStore {
            return mockStore
        }),

        // Application code
        fx.Provide(NewApplication),

        fx.Invoke(func(app *Application) {
            result, err := app.GetValue(context.Background(), "key")
            require.NoError(t, err)
            assert.Equal(t, []byte("value"), result)
        }),
    )

    app.RequireStart()
    defer app.RequireStop()
}
```

**Key advantage**: Application code doesn't know if it's using real plugin or mock.

## Advanced Patterns

### Plugin with fx Dependencies

Plugin implementation can have fx-injected dependencies:

```go
// Plugin implementation with dependencies
type kvStoreImpl struct {
    logger *zap.Logger
    db     *sql.DB
}

func NewKVStore(logger *zap.Logger, db *sql.DB) kv.KVStore {
    return &kvStoreImpl{logger: logger, db: db}
}

// fx app
fx.New(
    fx.Provide(zap.NewProduction),
    fx.Provide(NewDatabase),
    fx.Provide(NewKVStore), // Receives logger and db via fx

    connectpluginfx.PluginServerModule(&connectplugin.ServeConfig{
        Addr: ":8080",
        Plugins: connectplugin.PluginSet{
            "kv": &kvplugin.KVServicePlugin{},
        },
    }),

    // Populate Impls from fx container
    fx.Invoke(func(lc fx.Lifecycle, impl kv.KVStore) {
        // Bind implementation to server config
        // (This is a bit clunky - could be improved)
    }),
)
```

### PluginSet from fx Container

Collect multiple plugin implementations from fx:

```go
// Tag interface implementations
fx.Provide(
    fx.Annotate(
        NewKVStore,
        fx.ResultTags(`name:"kv"`),
    ),
    fx.Annotate(
        NewAuthService,
        fx.ResultTags(`name:"auth"`),
    ),
)

// Collect into Impls map
type PluginImpls struct {
    fx.In
    KV   kv.KVStore       `name:"kv"`
    Auth auth.AuthService `name:"auth"`
}

fx.Invoke(func(impls PluginImpls) {
    connectplugin.Serve(&connectplugin.ServeConfig{
        Addr:    ":8080",
        Plugins: pluginSet,
        Impls: map[string]any{
            "kv":   impls.KV,
            "auth": impls.Auth,
        },
    })
})
```

### Host Capabilities from fx

Expose fx-managed services as host capabilities:

```go
fx.New(
    // Host services
    fx.Provide(NewVaultClient),
    fx.Provide(NewLogger),

    // Wrap as capabilities
    fx.Provide(func(vault *VaultClient) connectplugin.CapabilityHandler {
        return &SecretsCapability{vault: vault}
    }),

    // Collect capabilities
    type HostCaps struct {
        fx.In
        Secrets connectplugin.CapabilityHandler `name:"secrets"`
    }

    // Serve with capabilities
    connectpluginfx.PluginServerModule(&connectplugin.ServeConfig{
        Addr:    ":8080",
        Plugins: pluginSet,
        Impls:   impls,
        HostCapabilities: map[string]connectplugin.CapabilityHandler{
            "secrets": hostCaps.Secrets,
        },
    }),
)
```

## Simplified API: Module Builder

For common cases, provide a builder:

```go
// ModuleBuilder fluent API
type ModuleBuilder struct {
    name     string
    cfg      *connectplugin.ClientConfig
    provides []provideSpec
}

type provideSpec struct {
    pluginName string
    ifaceType  reflect.Type
}

func NewModule(name string) *ModuleBuilder {
    return &ModuleBuilder{
        name: name,
        cfg: &connectplugin.ClientConfig{
            Plugins: make(connectplugin.PluginSet),
        },
    }
}

func (b *ModuleBuilder) Endpoint(endpoint string) *ModuleBuilder {
    b.cfg.Endpoint = endpoint
    return b
}

func (b *ModuleBuilder) WithPlugin(name string, plugin connectplugin.Plugin) *ModuleBuilder {
    b.cfg.Plugins[name] = plugin
    return b
}

func (b *ModuleBuilder) Provide[I any](pluginName string) *ModuleBuilder {
    b.provides = append(b.provides, provideSpec{
        pluginName: pluginName,
        ifaceType:  reflect.TypeOf((*I)(nil)).Elem(),
    })
    return b
}

func (b *ModuleBuilder) Build() fx.Option {
    providers := make([]any, 0, len(b.provides)+1)

    // Provide client
    providers = append(providers, newPluginClient(b.cfg))

    // Provide typed plugins
    for _, spec := range b.provides {
        providers = append(providers, makeTypedProvider(spec.pluginName, spec.ifaceType))
    }

    return fx.Module(b.name, fx.Provide(providers...))
}

// Usage
kvModule := connectpluginfx.NewModule("kv-plugin").
    Endpoint("http://kv-plugin:8080").
    WithPlugin("kv", &kvplugin.KVServicePlugin{}).
    Provide[kv.KVStore]("kv").
    Build()

fx.New(
    kvModule,
    fx.Invoke(func(store kv.KVStore) {
        // Use plugin
    }),
).Run()
```

## Test Harness: LocalPluginHarness

Helper for in-process plugin testing:

```go
// LocalPluginHarness runs plugin client and server in same process
type LocalPluginHarness struct {
    addr    string
    plugins connectplugin.PluginSet
    impls   map[string]any
}

func NewLocalPluginHarness(plugins connectplugin.PluginSet, impls map[string]any) *LocalPluginHarness {
    return &LocalPluginHarness{
        addr:    "localhost:0", // Random port
        plugins: plugins,
        impls:   impls,
    }
}

func (h *LocalPluginHarness) Module() fx.Option {
    return fx.Options(
        // Server module
        fx.Invoke(func(lc fx.Lifecycle) error {
            stopCh := make(chan struct{})
            lc.Append(fx.Hook{
                OnStart: func(ctx context.Context) error {
                    listener, err := net.Listen("tcp", h.addr)
                    if err != nil {
                        return err
                    }
                    h.addr = listener.Addr().String()
                    listener.Close()

                    go connectplugin.Serve(&connectplugin.ServeConfig{
                        Addr:    h.addr,
                        Plugins: h.plugins,
                        Impls:   h.impls,
                        StopCh:  stopCh,
                    })

                    return waitForServerReady("http://"+h.addr, 5*time.Second)
                },
                OnStop: func(ctx context.Context) error {
                    close(stopCh)
                    return nil
                },
            })
            return nil
        }),

        // Client module
        fx.Provide(func(lc fx.Lifecycle) (*connectplugin.Client, error) {
            client, err := connectplugin.NewClient(&connectplugin.ClientConfig{
                Endpoint:    "http://" + h.addr,
                Plugins:     h.plugins,
                LazyConnect: false, // Eager for testing
            })
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

            return client, nil
        }),
    )
}
```

**Usage in tests:**

```go
func TestWithHarness(t *testing.T) {
    harness := connectpluginfx.NewLocalPluginHarness(
        connectplugin.PluginSet{
            "kv": &kvplugin.KVServicePlugin{},
        },
        map[string]any{
            "kv": &testKVStore{data: make(map[string][]byte)},
        },
    )

    var store kv.KVStore

    app := fxtest.New(t,
        harness.Module(),
        connectpluginfx.ProvideTypedPlugin[kv.KVStore]("kv"),
        fx.Populate(&store),
    )

    app.RequireStart()
    defer app.RequireStop()

    // Use store
    err := store.Put(context.Background(), "key", []byte("value"))
    require.NoError(t, err)
}
```

## Test Harness: Capability Testing

For testing plugins that require capabilities:

```go
func TestPluginWithCapabilities(t *testing.T) {
    // Mock secrets capability
    secretsHandler := &mockSecretsHandler{
        secrets: map[string]string{
            "database/password": "test-password",
        },
    }

    harness := connectpluginfx.NewLocalPluginHarness(
        connectplugin.PluginSet{
            "database": &databaseplugin.DatabaseServicePlugin{},
        },
        map[string]any{
            "database": &testDatabaseService{},
        },
    ).WithCapability("secrets", secretsHandler)

    var dbSvc database.Service

    app := fxtest.New(t,
        harness.Module(),
        connectpluginfx.ProvideTypedPlugin[database.Service]("database"),
        fx.Populate(&dbSvc),
    )

    app.RequireStart()
    defer app.RequireStop()

    // Plugin internally requested and used secrets capability
    assert.True(t, secretsHandler.WasCalled())
}
```

## Test Harness: Multi-Plugin Composition

For testing plugin-to-plugin communication:

```go
func TestPluginToPlugin(t *testing.T) {
    // Service registry for plugin-to-plugin discovery
    registry := connectplugin.NewServiceRegistry()

    // Logger plugin
    loggerHarness := connectpluginfx.NewLocalPluginHarness(
        connectplugin.PluginSet{"logger": &loggerPlugin{}},
        map[string]any{"logger": &testLogger{}},
    ).WithServiceRegistry(registry)

    // App plugin (needs logger)
    appHarness := connectpluginfx.NewLocalPluginHarness(
        connectplugin.PluginSet{"app": &appPlugin{}},
        map[string]any{"app": &testApp{}},
    ).WithServiceRegistry(registry)

    var appSvc app.Service

    app := fxtest.New(t,
        loggerHarness.Module(),
        appHarness.Module(),
        connectpluginfx.ProvideTypedPlugin[app.Service]("app"),
        fx.Populate(&appSvc),
    )

    app.RequireStart()
    defer app.RequireStop()

    // App plugin discovers and uses logger plugin via registry
    err := appSvc.DoWork(context.Background(), &WorkRequest{})
    require.NoError(t, err)
}
```

## Integration with Real Application

Non-test usage - production app with fx and plugins:

```go
func main() {
    app := fx.New(
        // Core services
        fx.Provide(NewLogger),
        fx.Provide(NewDatabase),

        // Plugin clients
        connectpluginfx.NewModule("kv").
            Endpoint("http://kv-plugin:8080").
            WithPlugin("kv", &kvplugin.KVServicePlugin{}).
            Provide[kv.KVStore]("kv").
            Build(),

        connectpluginfx.NewModule("auth").
            Endpoint("http://auth-plugin:8080").
            WithPlugin("auth", &authplugin.AuthServicePlugin{}).
            Provide[auth.AuthService]("auth").
            Build(),

        // Application services (depend on plugins)
        fx.Provide(NewAPIServer), // Receives kv.KVStore, auth.AuthService

        // HTTP server
        fx.Invoke(func(lc fx.Lifecycle, api *APIServer) {
            lc.Append(fx.Hook{
                OnStart: func(ctx context.Context) error {
                    return api.Start()
                },
                OnStop: func(ctx context.Context) error {
                    return api.Shutdown(ctx)
                },
            })
        }),
    )

    app.Run()
}
```

## Comparison: With vs Without fx

### Without fx (Vanilla)

```go
func main() {
    // Manual setup
    kvClient, err := connectplugin.NewClient(&connectplugin.ClientConfig{
        Endpoint: "http://kv-plugin:8080",
        Plugins:  pluginSet,
    })
    if err != nil {
        log.Fatal(err)
    }
    defer kvClient.Close()

    if err := kvClient.Connect(context.Background()); err != nil {
        log.Fatal(err)
    }

    kvStore, err := connectplugin.DispenseTyped[kv.KVStore](kvClient, "kv")
    if err != nil {
        log.Fatal(err)
    }

    // Use plugin
    app := NewApplication(kvStore)
    app.Run()
}
```

### With fx

```go
func main() {
    fx.New(
        connectpluginfx.NewModule("kv").
            Endpoint("http://kv-plugin:8080").
            WithPlugin("kv", &kvplugin.KVServicePlugin{}).
            Provide[kv.KVStore]("kv").
            Build(),

        fx.Provide(NewApplication), // Receives kv.KVStore

        fx.Invoke(func(app *Application) {
            app.Run()
        }),
    ).Run()
}
```

**Benefits of fx:**
- Automatic lifecycle management (OnStart/OnStop)
- Dependency injection (plugins as dependencies)
- Clean test setup with fxtest
- Easier mocking (just provide different implementation)

## Implementation Checklist

- [x] PluginModule() for client integration
- [x] PluginServerModule() for server integration
- [x] ProvideTypedPlugin() for type-safe dispensing
- [x] LocalPluginHarness for in-process testing
- [x] Capability testing support
- [x] Multi-plugin composition testing
- [x] Module builder fluent API
- [x] Production app example
- [x] Comparison with/without fx

## Next Steps

1. Implement connectpluginfx package
2. Write tests using LocalPluginHarness
3. Use fx harness to test core implementation
4. Document testing patterns in getting started guide
