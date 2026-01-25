# Design: fx Integration Layer

**Issue:** KOR-ntsm
**Status:** In Progress (Simplified)
**Dependencies:** KOR-cldj, KOR-gfuh

## Overview

The fx integration layer wraps connect-plugin with uber-go/fx dependency injection, making plugins behave like any other injectable dependency. This is a separate optional layer on top of core connect-plugin - you can use connect-plugin without fx, but fx integration provides ergonomic DI for applications already using fx.

**Important:** This is a lightweight wrapper for fx-based applications. For simple use cases or testing, prefer direct mocking via fx.Provide instead of complex test harnesses.

## Summary of Simplifications (Post-Review)

Based on review feedback, this design has been simplified:

**KEPT (Essential):**
- `PluginModule()` - Client lifecycle management with fx hooks
- `ProvideTypedPlugin[I]()` - Type-safe interface dispensing
- `ModuleBuilder` - Fluent API for common configuration patterns

**REMOVED (Unnecessary complexity):**
- `LocalPluginHarness` - **Too brittle and complex**. Users should inject mocks directly via `fx.Provide` for testing.
- Multi-plugin test harnesses - Compose simple primitives manually if truly needed.
- Specialized capability testing helpers - Use real or mock handlers directly.

**DEFERRED (Needs design fixes):**
- `PluginServerModule()` - Has `stopCh` lifecycle bug and coordination issues. Use vanilla `connectplugin.Serve()` with `fx.Populate` pattern instead.

**NEW:**
- "When NOT to use fx integration" guidance
- Direct mocking pattern as primary testing approach
- Simplified mental model: fx is for client-side wiring only

## Design Goals

1. **Optional layer**: Works with or without fx
2. **Ergonomic**: Plugins as simple fx.Provide sources
3. **Lifecycle integration**: Plugin Connect/Close map to fx OnStart/OnStop
4. **Type-safe**: Provide specific interfaces, not raw Plugin
5. **Test-friendly**: Prefer direct mocking over complex machinery

## When NOT to Use fx Integration

**Skip fx integration if:**
- You're writing a simple CLI tool or single-purpose binary
- Your application doesn't already use fx
- You're writing unit tests (prefer direct mocking instead)
- You need fine-grained control over plugin lifecycle
- You only have 1-2 dependencies total

**Use fx integration when:**
- Your application already uses fx for DI
- You have many dependencies and want automatic wiring
- You want declarative lifecycle management (OnStart/OnStop)
- You're building a complex service with multiple plugins

## Package Structure

```
connectpluginfx/
  module.go         - PluginModule() and ModuleBuilder
  typed.go          - ProvideTypedPlugin[I]() for type-safe dispensing
```

Note: Server-side fx integration is deferred. Use vanilla `connectplugin.Serve()` for serving plugins.

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

### PluginServerModule (DEFERRED)

Server-side fx integration is deferred due to lifecycle complexity:
- `stopCh` needs to be created and managed properly
- `connectplugin.Serve()` is blocking, making goroutine coordination tricky
- Most plugin servers are simple binaries that don't need fx

**Current recommendation:** Use vanilla `connectplugin.Serve()` in `main()`:

```go
func main() {
    connectplugin.Serve(&connectplugin.ServeConfig{
        Addr:    ":8080",
        Plugins: pluginSet,
        Impls:   impls,
    })
}
```

If you need fx for plugin implementation dependencies, compose manually:

```go
func main() {
    var impl kv.KVStore
    app := fx.New(
        fx.Provide(NewLogger),
        fx.Provide(NewDatabase),
        fx.Provide(NewKVStoreImpl), // Receives logger, db
        fx.Populate(&impl),
    )

    if err := app.Start(context.Background()); err != nil {
        log.Fatal(err)
    }

    // Serve plugin with fx-created implementation
    connectplugin.Serve(&connectplugin.ServeConfig{
        Addr:    ":8080",
        Plugins: connectplugin.PluginSet{"kv": &kvplugin.KVServicePlugin{}},
        Impls:   map[string]any{"kv": impl},
    })
}
```

## Testing Patterns

### Recommended: Direct Mocking with fx.Provide

For unit testing, skip the plugin layer entirely and inject mocks directly:

```go
func TestApplicationWithMockPlugin(t *testing.T) {
    mockStore := &mockKVStore{
        data: map[string][]byte{"key": []byte("value")},
    }

    app := fxtest.New(t,
        // Provide mock directly - no plugin machinery needed
        fx.Provide(func() kv.KVStore {
            return mockStore
        }),

        // Application code (receives kv.KVStore interface)
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

**Key advantages:**
- Application code doesn't know if it's using real plugin or mock
- No network overhead, port allocation, or process coordination
- Fast, simple, and easy to debug
- Recommended for 95% of test cases

### Integration Testing: Real Plugin with Test Server

For testing the actual plugin protocol (rare), use vanilla connect-plugin test helpers:

```go
func TestRealPluginProtocol(t *testing.T) {
    // Start plugin server manually
    stopCh := make(chan struct{})
    go func() {
        connectplugin.Serve(&connectplugin.ServeConfig{
            Addr: "localhost:18080",
            Plugins: connectplugin.PluginSet{
                "kv": &kvplugin.KVServicePlugin{},
            },
            Impls: map[string]any{
                "kv": &testKVStore{},
            },
            StopCh: stopCh,
        })
    }()
    defer close(stopCh)

    time.Sleep(100 * time.Millisecond) // Wait for server

    // Use fx for client side only
    app := fxtest.New(t,
        connectpluginfx.PluginModule(&connectplugin.ClientConfig{
            Endpoint: "http://localhost:18080",
            Plugins: connectplugin.PluginSet{
                "kv": &kvplugin.KVServicePlugin{},
            },
        }),
        connectpluginfx.ProvideTypedPlugin[kv.KVStore]("kv"),

        fx.Invoke(func(store kv.KVStore) {
            err := store.Put(context.Background(), "test", []byte("value"))
            require.NoError(t, err)
        }),
    )

    app.RequireStart()
    defer app.RequireStop()
}
```

**When to use this pattern:**
- Testing serialization/deserialization edge cases
- Testing plugin handshake or versioning
- Testing error handling across the wire
- Integration tests in CI/CD pipelines

**Note:** Avoid complex test harnesses like `LocalPluginHarness` - they're brittle and add unnecessary complexity. Prefer composition of simple primitives.

## Advanced Pattern: Plugin Implementation with fx Dependencies

If your plugin implementation needs dependencies (logger, database, etc.), use fx to build the implementation, then pass it to vanilla `Serve()`:

```go
func main() {
    var impl kv.KVStore

    app := fx.New(
        // Dependencies
        fx.Provide(NewLogger),
        fx.Provide(NewDatabase),
        fx.Provide(NewKVStoreImpl), // Receives logger, db via fx

        // Extract implementation
        fx.Populate(&impl),
    )

    ctx := context.Background()
    if err := app.Start(ctx); err != nil {
        log.Fatal(err)
    }
    defer app.Stop(ctx)

    // Serve plugin with fx-created implementation
    connectplugin.Serve(&connectplugin.ServeConfig{
        Addr: ":8080",
        Plugins: connectplugin.PluginSet{
            "kv": &kvplugin.KVServicePlugin{},
        },
        Impls: map[string]any{
            "kv": impl,
        },
    })
}
```

This is simpler than trying to integrate `Serve()` into fx lifecycle.

## Module Builder API

For ergonomic plugin client configuration:

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


## Production Usage Example

Real application using fx with multiple plugin clients:

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

### Core (Essential)
- [ ] PluginModule() for client integration
- [ ] ProvideTypedPlugin[I]() for type-safe dispensing
- [ ] ModuleBuilder fluent API
- [ ] Lifecycle management (OnStart/OnStop hooks)

### Documentation
- [ ] When NOT to use fx integration section
- [ ] Direct mocking pattern examples
- [ ] Production app example
- [ ] Migration guide from vanilla usage

### Deferred
- [ ] PluginServerModule() - needs lifecycle design review
  - Issue: stopCh coordination with fx lifecycle
  - Issue: blocking Serve() call in goroutine
  - Workaround: Use vanilla Serve() with fx.Populate pattern

### Removed
- ~~LocalPluginHarness~~ - Too complex and brittle. Use direct mocking instead.
- ~~Multi-plugin test harness~~ - Compose simple primitives manually if needed.
- ~~Capability test helpers~~ - Test with real/mock capability handlers directly.

## Next Steps

1. Implement core connectpluginfx package (PluginModule, ProvideTypedPlugin, ModuleBuilder)
2. Write examples showing direct mocking pattern
3. Document "when NOT to use fx" prominently
4. Defer server-side integration until lifecycle issues resolved
