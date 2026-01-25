# Design: Core Plugin Interface & Type System

**Issue:** KOR-gfuh
**Status:** Complete
**Dependencies:** KOR-fzki, KOR-lfks

## Overview

The core plugin interface defines how plugins are identified, instantiated on the client side, and registered on the server side. Unlike go-plugin's separate Plugin/GRPCPlugin split, connect-plugin uses a unified interface that is simpler and more ergonomic for Connect RPC.

## Design Goals

1. **Familiar**: go-plugin users should recognize patterns
2. **Simple**: Single interface, not split by protocol
3. **Flexible**: Support both generated and hand-written plugins
4. **Type-safe**: Optional generics for compile-time safety
5. **No protobuf requirement**: Simple plugins shouldn't need protobuf

## Core Plugin Interface

```go
// Plugin defines a plugin that can be served or dispensed.
// This is the core abstraction - all plugins implement this interface.
type Plugin interface {
    // Metadata returns plugin metadata including the service path.
    // This is used for validation and path conflict detection.
    Metadata() PluginMetadata

    // ConnectServer returns a Connect HTTP handler for this plugin.
    // The impl parameter is the actual implementation (e.g., &KVStoreImpl{}).
    // Returns a handler that will be registered with the HTTP mux.
    ConnectServer(impl any) (string, http.Handler, error)

    // ConnectClient creates a client instance for this plugin.
    // The baseURL is the plugin's HTTP endpoint.
    // The httpClient is used for making requests.
    // Returns an interface{} that callers should type-assert.
    ConnectClient(baseURL string, httpClient connect.HTTPClient) (any, error)
}

// PluginMetadata contains metadata about a plugin.
type PluginMetadata struct {
    // Path is the service path (e.g., "/kv.v1.KVService/").
    Path string
}
```

**Key decisions:**

1. **Single interface**: No Protocol/GRPCPlugin split - Connect is the only protocol
2. **Metadata method**: Separate method for path validation without nil implementations
3. **Server returns handler**: More flexible than go-plugin's registration approach
4. **Server returns path**: Each plugin defines its URL path (e.g., `/kv.v1.KVService/`)
5. **Client returns baseURL**: Simpler than passing full connection
6. **Any types**: Flexibility for different plugin interfaces
7. **No broker parameter**: Bidirectional handled separately via capabilities

### Comparison with go-plugin

| Aspect | go-plugin | connect-plugin |
|--------|-----------|----------------|
| **Interface count** | 2 (Plugin, GRPCPlugin) | 1 (Plugin) |
| **Validation** | Via ConnectServer(nil) | Via Metadata() method |
| **Server method** | `GRPCServer(*Broker, *grpc.Server) error` | `ConnectServer(impl any) (string, http.Handler, error)` |
| **Client method** | `GRPCClient(ctx, *Broker, *grpc.ClientConn) (any, error)` | `ConnectClient(baseURL, httpClient) (any, error)` |
| **Broker** | Passed to methods | Separate capability system |
| **Return** | Client: interface, Server: registers | Both: concrete types |
| **Primary API** | Untyped Dispense() | Typed DispenseTyped[I]() |

## PluginSet Type

```go
// PluginSet is a set of plugins that can be served or consumed.
// Keys are plugin names (e.g., "kv", "auth").
type PluginSet map[string]Plugin

// Get returns a plugin by name.
func (ps PluginSet) Get(name string) (Plugin, bool) {
    p, ok := ps[name]
    return p, ok
}

// Keys returns all plugin names in the set.
func (ps PluginSet) Keys() []string {
    keys := make([]string, 0, len(ps))
    for k := range ps {
        keys = append(keys, k)
    }
    sort.Strings(keys)
    return keys
}

// Validate checks that all plugins have unique paths.
func (ps PluginSet) Validate() error {
    paths := make(map[string]string)
    for name, plugin := range ps {
        // Get path from metadata
        metadata := plugin.Metadata()
        path := metadata.Path
        if path == "" {
            return fmt.Errorf("plugin %q: empty path", name)
        }
        if existing, ok := paths[path]; ok {
            return fmt.Errorf("plugin %q path %q conflicts with plugin %q", name, path, existing)
        }
        paths[path] = name
    }
    return nil
}
```

## Typed Plugin Interface (Optional)

For type safety with generics:

```go
// TypedPlugin is a type-safe wrapper around Plugin.
// I is the interface type (e.g., KVStore).
type TypedPlugin[I any] interface {
    Plugin

    // TypedClient creates a type-safe client.
    TypedClient(baseURL string, httpClient connect.HTTPClient) (I, error)
}

// AsTyped converts a Plugin to a TypedPlugin.
// This is a convenience for type assertions.
func AsTyped[I any](p Plugin) (TypedPlugin[I], bool) {
    tp, ok := p.(TypedPlugin[I])
    return tp, ok
}
```

## Example: Hand-Written Plugin (No Codegen)

**Note:** Hand-written plugins require significant boilerplate (~140+ lines for a simple 3-method interface). For production use, **we strongly recommend using codegen** (`protoc-gen-connect-plugin`) which generates all plugin wrapper code automatically. Hand-written plugins are shown here for completeness but should be reserved for advanced use cases or custom plugin patterns.

For a complete hand-written plugin example with all adapter code, see the examples directory. The pattern requires:
1. Define your Go interface (e.g., `KVStore`)
2. Define protobuf schema matching the interface
3. Generate Connect code with `protoc-gen-connect-go`
4. Write a plugin wrapper implementing `Plugin` interface
5. Write adapter code for server (Go interface → Connect service)
6. Write adapter code for client (Connect client → Go interface)

### Basic Usage Pattern

For completeness, here's the minimal plugin wrapper structure (full adapter code omitted):

```go
// kv/plugin.go
package kv

import (
    "net/http"
    "connectrpc.com/connect"
    connectplugin "github.com/yourorg/connect-plugin-go"
    "myapp/gen/kv/v1/kvv1connect"
)

type ConnectPlugin struct{}

func (p *ConnectPlugin) Metadata() connectplugin.PluginMetadata {
    return connectplugin.PluginMetadata{
        Path: kvv1connect.KVServiceName, // e.g., "/kv.v1.KVService/"
    }
}

func (p *ConnectPlugin) ConnectServer(impl any) (string, http.Handler, error) {
    kvImpl, ok := impl.(KVStore)
    if !ok {
        return "", nil, fmt.Errorf("impl must implement KVStore, got %T", impl)
    }
    // Wrap impl with adapter → Connect service handler
    // ... adapter code omitted (~70 lines) ...
}

func (p *ConnectPlugin) ConnectClient(baseURL string, httpClient connect.HTTPClient) (any, error) {
    client := kvv1connect.NewKVServiceClient(httpClient, baseURL)
    // Wrap Connect client → KVStore interface
    // ... adapter code omitted (~70 lines) ...
}
```

**Host side:**
```go
import (
    "myapp/kv"
    connectplugin "github.com/yourorg/connect-plugin-go"
)

func main() {
    client := connectplugin.NewClient(&connectplugin.ClientConfig{
        Endpoint: "http://kv-plugin:8080",
        Plugins: connectplugin.PluginSet{
            "kv": &kv.ConnectPlugin{},
        },
    })

    client.Connect(context.Background())

    // Recommended: Type-safe dispensing
    kvStore := connectplugin.MustDispenseTyped[kv.KVStore](client, "kv")
    kvStore.Put(ctx, "hello", []byte("world"))
}
```

**Plugin side:**
```go
import (
    "myapp/kv"
    "github.com/yourorg/connect-plugin-go"
)

type myKVStore struct {
    data map[string][]byte
}

func (m *myKVStore) Get(ctx context.Context, key string) ([]byte, error) {
    return m.data[key], nil
}

// ... implement Put, Delete

func main() {
    connectplugin.Serve(&connectplugin.ServeConfig{
        Plugins: connectplugin.PluginSet{
            "kv": &kv.ConnectPlugin{},
        },
        Impls: map[string]any{
            "kv": &myKVStore{data: make(map[string][]byte)},
        },
    })
}
```

## Example: Generated Plugin (With Codegen)

The code generator (`protoc-gen-connect-plugin`) generates the plugin implementation:

```go
// gen/kv/v1/kvv1plugin/plugin.connectplugin.go
// Code generated by protoc-gen-connect-plugin. DO NOT EDIT.

package kvv1plugin

import (
    "net/http"

    "connectrpc.com/connect"
    connectplugin "github.com/yourorg/connect-plugin-go"
    "myapp/gen/kv/v1/kvv1connect"
)

// KVServicePlugin implements connectplugin.Plugin for KVService.
type KVServicePlugin struct{}

var _ connectplugin.Plugin = (*KVServicePlugin)(nil)
var _ connectplugin.TypedPlugin[kvv1connect.KVServiceClient] = (*KVServicePlugin)(nil)

func (p *KVServicePlugin) Metadata() connectplugin.PluginMetadata {
    return connectplugin.PluginMetadata{
        Path: kvv1connect.KVServiceName,
    }
}

func (p *KVServicePlugin) ConnectServer(impl any) (string, http.Handler, error) {
    server, ok := impl.(kvv1connect.KVServiceHandler)
    if !ok {
        return "", nil, fmt.Errorf("impl must implement KVServiceHandler, got %T", impl)
    }
    path, handler := kvv1connect.NewKVServiceHandler(server)
    return path, handler, nil
}

func (p *KVServicePlugin) ConnectClient(baseURL string, httpClient connect.HTTPClient) (any, error) {
    return kvv1connect.NewKVServiceClient(httpClient, baseURL), nil
}

// TypedClient provides compile-time type safety for client dispensing.
func (p *KVServicePlugin) TypedClient(baseURL string, httpClient connect.HTTPClient) (kvv1connect.KVServiceClient, error) {
    return kvv1connect.NewKVServiceClient(httpClient, baseURL), nil
}
```

Usage with generated plugin (using type-safe DispenseTyped):

```go
import kvv1plugin "myapp/gen/kv/v1/kvv1plugin"

client := connectplugin.NewClient(&connectplugin.ClientConfig{
    Endpoint: "http://kv-plugin:8080",
    Plugins: connectplugin.PluginSet{
        "kv": &kvv1plugin.KVServicePlugin{},
    },
})

// Primary API: type-safe dispensing
kvClient := connectplugin.MustDispenseTyped[kvv1connect.KVServiceClient](client, "kv")
resp, err := kvClient.Get(ctx, connect.NewRequest(&kvv1.GetRequest{Key: "hello"}))
```

## Helper Functions

### Primary API: DispenseTyped (Recommended)

```go
// DispenseTyped returns a typed plugin client.
// This is the PRIMARY and RECOMMENDED way to dispense plugins.
func DispenseTyped[I any](c *Client, name string) (I, error) {
    raw, err := c.Dispense(name)
    if err != nil {
        var zero I
        return zero, err
    }
    typed, ok := raw.(I)
    if !ok {
        var zero I
        return zero, fmt.Errorf("plugin %q does not implement %T, got %T", name, zero, raw)
    }
    return typed, nil
}

// MustDispenseTyped panics if the typed plugin is not found.
func MustDispenseTyped[I any](c *Client, name string) I {
    typed, err := DispenseTyped[I](c, name)
    if err != nil {
        panic(err)
    }
    return typed
}
```

### Secondary API: Untyped Dispense

```go
// Dispense returns an untyped plugin client.
// Use DispenseTyped instead for compile-time type safety.
func (c *Client) Dispense(name string) (any, error) {
    plugin, ok := c.config.Plugins.Get(name)
    if !ok {
        return nil, fmt.Errorf("plugin %q not found", name)
    }
    return plugin.ConnectClient(c.baseURL, c.httpClient)
}

// MustDispense panics if the plugin is not found.
// Use MustDispenseTyped instead for compile-time type safety.
func (c *Client) MustDispense(name string) any {
    raw, err := c.Dispense(name)
    if err != nil {
        panic(err)
    }
    return raw
}
```

Usage examples:

```go
// RECOMMENDED: Type-safe dispensing with compile-time checks
kvClient := connectplugin.MustDispenseTyped[kvv1connect.KVServiceClient](client, "kv")
resp, err := kvClient.Get(ctx, connect.NewRequest(&kvv1.GetRequest{Key: "hello"}))

// Alternative: Untyped dispensing (requires manual type assertion)
raw, err := client.Dispense("kv")
kvClient := raw.(kvv1connect.KVServiceClient)
```

## ServeConfig Extension

The Serve side needs to map plugin names to implementations:

```go
type ServeConfig struct {
    // Plugins defines available plugin types
    Plugins PluginSet

    // Impls maps plugin names to implementations
    // The impl is passed to Plugin.ConnectServer()
    Impls map[string]any

    // ... other config
}

func Serve(cfg *ServeConfig) error {
    mux := http.NewServeMux()

    for name, plugin := range cfg.Plugins {
        impl, ok := cfg.Impls[name]
        if !ok {
            return fmt.Errorf("no implementation for plugin %q", name)
        }

        path, handler, err := plugin.ConnectServer(impl)
        if err != nil {
            return fmt.Errorf("plugin %q: %w", name, err)
        }

        mux.Handle(path, handler)
    }

    // ... serve
}
```

## Plugin Versioning

**Note for v1:** The `VersionedPlugins` pattern from go-plugin adds complexity and is confusing when combined with the single `Plugins` field. For the initial v1 release, **we will support only a single plugin version** via the `Plugins` field in `ClientConfig` and `ServeConfig`.

Plugin evolution should be handled at the protobuf service level:
- Define new service versions (e.g., `kv.v2.KVService`)
- Register as separate plugin names (e.g., `"kv"` for v1, `"kv_v2"` for v2)
- Or use protobuf evolution (add fields, maintain compatibility)

```go
// v1 approach: Single plugin set
client := connectplugin.NewClient(&connectplugin.ClientConfig{
    Endpoint: "http://plugin:8080",
    Plugins: connectplugin.PluginSet{
        "kv": &kvv1plugin.KVServicePlugin{},
    },
})
```

Future versions may add protocol version negotiation if needed, but v1 will keep it simple.

## Limitations

### 1. No Built-in Bidirectional Communication

The core `Plugin` interface does **not** include a broker parameter for bidirectional RPC (plugin → host). This is intentional to keep the interface simple and focused.

**Rationale:**
- Most plugins only need unidirectional communication (host → plugin)
- Bidirectional communication adds significant complexity
- Can be implemented as a capability/extension when needed

**Workarounds for bidirectional needs:**
1. **Callback pattern**: Host passes callback interface to plugin during initialization
2. **Separate connection**: Plugin opens its own Connect client back to host
3. **Future capability system**: May be added as opt-in extension in future versions

Example callback pattern:
```go
// Host provides callback during initialization
type PluginCallbacks interface {
    OnEvent(ctx context.Context, event string) error
}

// Plugin receives callbacks
impl := &MyPlugin{
    callbacks: hostCallbacks,
}
```

### 2. Runtime Type Assertions on Server Side

The `ConnectServer(impl any)` method requires **runtime type assertions** to verify the implementation type. This cannot be checked at compile time.

**Impact:**
- Type mismatches only discovered at runtime (when `Serve()` is called)
- Error messages show the actual type vs expected type
- No compile-time guarantee that impl matches the service

**Mitigation:**
- Generated plugins include clear documentation of expected types
- Validation happens early during `Serve()` startup, not during requests
- Error messages are descriptive (e.g., "impl must implement KVServiceHandler, got *MyStruct")

**Alternative considered:** Make `Plugin` generic with implementation type, but this would complicate the interface and make `PluginSet` impossible (no `map[string]Plugin[T]` with different T values).

### 3. Code Generation Strongly Recommended

While hand-written plugins are possible, they require significant boilerplate:
- ~140+ lines for a simple 3-method interface
- Adapter code for both server (Go interface → Connect service)
- Adapter code for client (Connect client → Go interface)
- Easy to make mistakes in the adapter logic

**Recommendation:** Always use `protoc-gen-connect-plugin` for production plugins. Hand-written plugins should be reserved for advanced use cases only.

## Design Decisions Summary

### 1. Unified Plugin interface
**Decision:** Single `Plugin` interface instead of separate Protocol/GRPCPlugin.
**Rationale:** Connect is the only protocol, so no need for abstraction.

### 2. Metadata method for validation
**Decision:** Add `Metadata() PluginMetadata` method returning service path.
**Rationale:** Allows path validation without calling `ConnectServer(nil)`, which would fail type assertions. Separates validation concerns from instantiation.

### 3. Server returns handler
**Decision:** `ConnectServer` returns `(string, http.Handler, error)`.
**Rationale:** More flexible than direct registration. Allows path customization and middleware.

### 4. Any types instead of generics in base interface
**Decision:** Use `any` for impl and return types.
**Rationale:** Keeps interface simple. Typed wrappers available for type safety. Trade-off: runtime type assertions on server side.

### 5. DispenseTyped as primary API
**Decision:** Make `DispenseTyped[I]` the recommended/primary way to get plugin clients.
**Rationale:** Provides compile-time type safety. Untyped `Dispense()` still available but secondary.

### 6. No broker in Plugin interface
**Decision:** Bidirectional communication handled separately.
**Rationale:** Cleaner separation of concerns. Capabilities are opt-in. Most plugins don't need bidirectional communication.

### 7. Codegen strongly recommended
**Decision:** Hand-written plugins supported but codegen is primary path.
**Rationale:** Hand-written plugins require 140+ lines of boilerplate. Codegen eliminates this and prevents errors.

### 8. Simplified versioning for v1
**Decision:** Remove `VersionedPlugins` map from v1. Single `Plugins` field only.
**Rationale:** Less confusing. Version management at protobuf/service level. Can add protocol negotiation later if needed.

## Comparison with go-plugin

| Feature | go-plugin | connect-plugin |
|---------|-----------|----------------|
| **Interface count** | 2 (Plugin, GRPCPlugin) | 1 (Plugin) |
| **Path validation** | ConnectServer(nil) | Metadata() method |
| **Broker** | Required parameter | Separate capability system |
| **Server registration** | Called on grpc.Server | Returns http.Handler |
| **Client creation** | Receives grpc.ClientConn | Receives baseURL + httpClient |
| **Type safety** | Interface{} returns | DispenseTyped[I]() primary |
| **Versioning** | VersionedPlugins map | Single Plugins map (v1) |
| **Codegen** | Examples show manual | Primary path, strongly recommended |

## Implementation Checklist

- [x] Core Plugin interface design
- [x] PluginSet type and helpers
- [x] Hand-written plugin example
- [x] Generated plugin example
- [x] Typed wrapper pattern
- [x] ServeConfig integration
- [x] Helper functions (Dispense variants)
- [x] Versioning support

## Next Steps

1. Implement core types in `plugin.go`
2. Design handshake protocol (KOR-mbgw)
3. Design client configuration (KOR-qjhn)
4. Design server configuration (KOR-koba)
5. Implement code generator (KOR-eqvi)
