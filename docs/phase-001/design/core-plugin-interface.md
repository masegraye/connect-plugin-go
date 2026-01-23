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
```

**Key decisions:**

1. **Single interface**: No Protocol/GRPCPlugin split - Connect is the only protocol
2. **Server returns handler**: More flexible than go-plugin's registration approach
3. **Server returns path**: Each plugin defines its URL path (e.g., `/kv.v1.KVService/`)
4. **Client returns baseURL**: Simpler than passing full connection
5. **Any types**: Flexibility for different plugin interfaces
6. **No broker parameter**: Bidirectional handled separately via capabilities

### Comparison with go-plugin

| Aspect | go-plugin | connect-plugin |
|--------|-----------|----------------|
| **Interface count** | 2 (Plugin, GRPCPlugin) | 1 (Plugin) |
| **Server method** | `GRPCServer(*Broker, *grpc.Server) error` | `ConnectServer(impl any) (string, http.Handler, error)` |
| **Client method** | `GRPCClient(ctx, *Broker, *grpc.ClientConn) (any, error)` | `ConnectClient(baseURL, httpClient) (any, error)` |
| **Broker** | Passed to methods | Separate capability system |
| **Return** | Client: interface, Server: registers | Both: concrete types |

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
        // Get path by creating a dummy handler
        path, _, err := plugin.ConnectServer(nil)
        if err != nil {
            return fmt.Errorf("plugin %q: %w", name, err)
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

### Step 1: Define the interface

```go
// kv/interface.go

package kv

// KVStore is the interface for key-value storage.
type KVStore interface {
    Get(ctx context.Context, key string) ([]byte, error)
    Put(ctx context.Context, key string, value []byte) error
    Delete(ctx context.Context, key string) error
}
```

### Step 2: Define protobuf schema

```protobuf
// kv/v1/kv.proto

syntax = "proto3";
package kv.v1;

service KVService {
    rpc Get(GetRequest) returns (GetResponse);
    rpc Put(PutRequest) returns (PutResponse);
    rpc Delete(DeleteRequest) returns (DeleteResponse);
}

message GetRequest {
    string key = 1;
}

message GetResponse {
    bytes value = 1;
}

message PutRequest {
    string key = 1;
    bytes value = 2;
}

message PutResponse {}

message DeleteRequest {
    string key = 1;
}

message DeleteResponse {}
```

### Step 3: Create plugin implementation

```go
// kv/plugin.go

package kv

import (
    "context"
    "net/http"

    "connectrpc.com/connect"
    kvv1 "myapp/gen/kv/v1"
    "myapp/gen/kv/v1/kvv1connect"
)

// ConnectPlugin implements connectplugin.Plugin for the KV interface.
type ConnectPlugin struct{}

func (p *ConnectPlugin) ConnectServer(impl any) (string, http.Handler, error) {
    kvImpl, ok := impl.(KVStore)
    if !ok {
        return "", nil, fmt.Errorf("impl must implement KVStore, got %T", impl)
    }

    // Wrap the implementation as a Connect service
    server := &kvServer{impl: kvImpl}
    path, handler := kvv1connect.NewKVServiceHandler(server)
    return path, handler, nil
}

func (p *ConnectPlugin) ConnectClient(baseURL string, httpClient connect.HTTPClient) (any, error) {
    client := kvv1connect.NewKVServiceClient(httpClient, baseURL)
    return &kvClient{client: client}, nil
}

// kvServer adapts KVStore to the Connect service interface
type kvServer struct {
    impl KVStore
}

func (s *kvServer) Get(ctx context.Context, req *connect.Request[kvv1.GetRequest]) (*connect.Response[kvv1.GetResponse], error) {
    value, err := s.impl.Get(ctx, req.Msg.Key)
    if err != nil {
        return nil, err
    }
    return connect.NewResponse(&kvv1.GetResponse{Value: value}), nil
}

func (s *kvServer) Put(ctx context.Context, req *connect.Request[kvv1.PutRequest]) (*connect.Response[kvv1.PutResponse], error) {
    err := s.impl.Put(ctx, req.Msg.Key, req.Msg.Value)
    if err != nil {
        return nil, err
    }
    return connect.NewResponse(&kvv1.PutResponse{}), nil
}

func (s *kvServer) Delete(ctx context.Context, req *connect.Request[kvv1.DeleteRequest]) (*connect.Response[kvv1.DeleteResponse], error) {
    err := s.impl.Delete(ctx, req.Msg.Key)
    if err != nil {
        return nil, err
    }
    return connect.NewResponse(&kvv1.DeleteResponse{}), nil
}

// kvClient adapts the Connect client to the KVStore interface
type kvClient struct {
    client kvv1connect.KVServiceClient
}

func (c *kvClient) Get(ctx context.Context, key string) ([]byte, error) {
    resp, err := c.client.Get(ctx, connect.NewRequest(&kvv1.GetRequest{Key: key}))
    if err != nil {
        return nil, err
    }
    return resp.Msg.Value, nil
}

func (c *kvClient) Put(ctx context.Context, key string, value []byte) error {
    _, err := c.client.Put(ctx, connect.NewRequest(&kvv1.PutRequest{Key: key, Value: value}))
    return err
}

func (c *kvClient) Delete(ctx context.Context, key string) error {
    _, err := c.client.Delete(ctx, connect.NewRequest(&kvv1.DeleteRequest{Key: key}))
    return err
}
```

### Step 4: Use the plugin

**Host side:**
```go
import (
    "myapp/kv"
    "github.com/yourorg/connect-plugin-go"
)

func main() {
    client := connectplugin.NewClient(&connectplugin.ClientConfig{
        Endpoint: "http://kv-plugin:8080",
        Plugins: connectplugin.PluginSet{
            "kv": &kv.ConnectPlugin{},
        },
    })

    client.Connect(context.Background())

    // Dispense the plugin
    raw, err := client.Dispense("kv")
    if err != nil {
        log.Fatal(err)
    }
    kvStore := raw.(kv.KVStore)

    // Use it
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
    "context"
    "net/http"

    "connectrpc.com/connect"
    connectplugin "github.com/yourorg/connect-plugin-go"
    kvv1 "myapp/gen/kv/v1"
    "myapp/gen/kv/v1/kvv1connect"
)

// KVServicePlugin implements connectplugin.Plugin for KVService.
type KVServicePlugin struct{}

var _ connectplugin.Plugin = (*KVServicePlugin)(nil)

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

// Typed variant
type TypedKVServicePlugin struct {
    KVServicePlugin
}

func (p *TypedKVServicePlugin) TypedClient(baseURL string, httpClient connect.HTTPClient) (kvv1connect.KVServiceClient, error) {
    raw, err := p.ConnectClient(baseURL, httpClient)
    if err != nil {
        return nil, err
    }
    return raw.(kvv1connect.KVServiceClient), nil
}
```

Usage with generated plugin:

```go
import kvv1plugin "myapp/gen/kv/v1/kvv1plugin"

client := connectplugin.NewClient(&connectplugin.ClientConfig{
    Endpoint: "http://kv-plugin:8080",
    Plugins: connectplugin.PluginSet{
        "kv": &kvv1plugin.KVServicePlugin{},
    },
})
```

## Helper Functions

```go
// MustPlugin panics if the plugin is not found.
func (c *Client) MustDispense(name string) any {
    raw, err := c.Dispense(name)
    if err != nil {
        panic(err)
    }
    return raw
}

// DispenseTyped returns a typed plugin client.
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

Usage:

```go
// Type-safe dispensing
kvStore := connectplugin.MustDispenseTyped[kv.KVStore](client, "kv")
kvStore.Put(ctx, "key", []byte("value"))
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

Support for multiple protocol versions:

```go
type ClientConfig struct {
    // ... other fields

    // VersionedPlugins maps protocol versions to plugin sets
    // Negotiated during handshake
    VersionedPlugins map[int]PluginSet
}

// Example
client := connectplugin.NewClient(&connectplugin.ClientConfig{
    Endpoint: "http://plugin:8080",
    VersionedPlugins: map[int]connectplugin.PluginSet{
        1: {
            "kv": &kvv1plugin.KVServicePlugin{},
        },
        2: {
            "kv": &kvv2plugin.KVServicePlugin{}, // Updated interface
        },
    },
})
```

## Design Decisions Summary

### 1. Unified Plugin interface
**Decision:** Single `Plugin` interface instead of separate Protocol/GRPCPlugin.
**Rationale:** Connect is the only protocol, so no need for abstraction.

### 2. Server returns handler
**Decision:** `ConnectServer` returns `(string, http.Handler, error)`.
**Rationale:** More flexible than direct registration. Allows path customization and middleware.

### 3. Any types instead of generics in base interface
**Decision:** Use `any` for impl and return types.
**Rationale:** Keeps interface simple. Typed wrappers available for type safety.

### 4. No broker in Plugin interface
**Decision:** Bidirectional communication handled separately.
**Rationale:** Cleaner separation of concerns. Capabilities are opt-in.

### 5. Support both hand-written and generated
**Decision:** Interface works for both patterns.
**Rationale:** Simple plugins shouldn't require codegen. Generated code for convenience.

## Comparison with go-plugin

| Feature | go-plugin | connect-plugin |
|---------|-----------|----------------|
| **Interface count** | 2 (Plugin, GRPCPlugin) | 1 (Plugin) |
| **Broker** | Required parameter | Separate capability system |
| **Server registration** | Called on grpc.Server | Returns http.Handler |
| **Client creation** | Receives grpc.ClientConn | Receives baseURL + httpClient |
| **Type safety** | Interface{} returns | Optional generics |
| **Versioning** | VersionedPlugins map | Same pattern |
| **Codegen** | Examples show manual | Tool generates plugin wrapper |

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
