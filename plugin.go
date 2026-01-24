package connectplugin

import (
	"fmt"
	"net/http"
	"sort"

	"connectrpc.com/connect"
)

// Plugin is the interface that must be implemented by all plugin types.
// This is the primary abstraction for connect-plugin.
type Plugin interface {
	// Metadata returns plugin metadata for validation and discovery.
	// This can be called with no implementation to extract plugin information.
	Metadata() PluginMetadata

	// ConnectServer creates a server-side HTTP handler for this plugin.
	// The impl parameter is the actual implementation (e.g., &KVStoreImpl{}).
	// Returns:
	//   - path: The HTTP path where this plugin's handler should be mounted (e.g., "/kv.v1.KVService/")
	//   - handler: The HTTP handler that implements the plugin's RPC service
	//   - error: If impl is the wrong type or handler creation fails
	ConnectServer(impl any) (path string, handler http.Handler, error error)

	// ConnectClient creates a client-side plugin instance.
	// The baseURL is the plugin server's HTTP endpoint.
	// The httpClient is used for making Connect RPC calls.
	// Returns an interface{} that callers should type-assert or use with DispenseTyped.
	ConnectClient(baseURL string, httpClient connect.HTTPClient) (any, error)
}

// PluginMetadata contains information about a plugin.
type PluginMetadata struct {
	// Name is the plugin's unique identifier (e.g., "kv", "auth").
	Name string

	// Path is the HTTP path where this plugin's handler is served (e.g., "/kv.v1.KVService/").
	Path string

	// Version is the plugin's semantic version (e.g., "1.0.0").
	Version string
}

// PluginSet is a map of plugin names to Plugin implementations.
// It represents the set of plugins available from a server or
// that can be consumed by a client.
type PluginSet map[string]Plugin

// Get returns the plugin with the given name, or false if not found.
func (ps PluginSet) Get(name string) (Plugin, bool) {
	p, ok := ps[name]
	return p, ok
}

// Keys returns all plugin names in sorted order.
func (ps PluginSet) Keys() []string {
	keys := make([]string, 0, len(ps))
	for k := range ps {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

// Validate checks that the plugin set is valid.
// It verifies there are no path conflicts between plugins.
func (ps PluginSet) Validate() error {
	if len(ps) == 0 {
		return ErrEmptyPluginSet
	}

	paths := make(map[string]string)
	for name, plugin := range ps {
		metadata := plugin.Metadata()

		// Check for path conflicts
		if existing, ok := paths[metadata.Path]; ok {
			return fmt.Errorf("path conflict: plugins %q and %q both use path %q",
				name, existing, metadata.Path)
		}
		paths[metadata.Path] = name

		// Verify plugin name matches map key
		if metadata.Name != name {
			return fmt.Errorf("plugin name mismatch: map key is %q but plugin.Metadata().Name is %q",
				name, metadata.Name)
		}
	}

	return nil
}

// DispenseTyped dispenses a typed plugin from the client.
// This is the primary API for dispensing plugins (compile-time type safety).
//
// Example:
//
//	kvStore, err := connectplugin.DispenseTyped[kv.KVStore](client, "kv")
//	if err != nil {
//	    return err
//	}
//	kvStore.Put(ctx, "key", []byte("value"))
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

// MustDispenseTyped is like DispenseTyped but panics on error.
// Use for required plugins where failure should crash the application.
//
// Example:
//
//	kvStore := connectplugin.MustDispenseTyped[kv.KVStore](client, "kv")
func MustDispenseTyped[I any](c *Client, name string) I {
	typed, err := DispenseTyped[I](c, name)
	if err != nil {
		panic(fmt.Sprintf("MustDispenseTyped failed: %v", err))
	}
	return typed
}
