package connectplugin

import (
	"context"
	"fmt"
	"net/http"
	"sync"

	"connectrpc.com/connect"
)

// ClientConfig is the minimal configuration required to create a plugin client.
// For most use cases, only Endpoint and Plugins are needed.
type ClientConfig struct {
	// Endpoint is the plugin service URL.
	// Required. Examples: "http://localhost:8080", "https://plugin.example.com"
	Endpoint string

	// Plugins defines available plugin types.
	// Required. Maps plugin name to Plugin implementation.
	Plugins PluginSet
}

// Validate checks ClientConfig for errors.
func (cfg *ClientConfig) Validate() error {
	if cfg.Endpoint == "" {
		return fmt.Errorf("%w: Endpoint is required", ErrInvalidConfig)
	}

	if cfg.Plugins == nil {
		return fmt.Errorf("%w: Plugins is required", ErrInvalidConfig)
	}

	if len(cfg.Plugins) == 0 {
		return fmt.Errorf("%w: Plugins must contain at least one plugin", ErrInvalidConfig)
	}

	// Validate the plugin set
	if err := cfg.Plugins.Validate(); err != nil {
		return fmt.Errorf("%w: %v", ErrInvalidConfig, err)
	}

	return nil
}

// Client manages the connection to a plugin service and dispenses plugin implementations.
type Client struct {
	cfg       ClientConfig
	mu        sync.RWMutex
	connected bool
	closed    bool

	// HTTP client for Connect RPCs (created on Connect)
	httpClient connect.HTTPClient
}

// NewClient creates a new plugin client with the given configuration.
// The client uses lazy connection - it doesn't connect until the first
// plugin is dispensed or Connect() is called explicitly.
func NewClient(cfg ClientConfig) (*Client, error) {
	if err := cfg.Validate(); err != nil {
		return nil, err
	}

	return &Client{
		cfg: cfg,
	}, nil
}

// Connect establishes the connection to the plugin server.
// This is called automatically on first Dispense() but can be called
// explicitly for eager connection or to handle connection errors upfront.
func (c *Client) Connect(ctx context.Context) error {
	c.mu.Lock()
	defer c.mu.Unlock()

	if c.closed {
		return ErrClientClosed
	}

	if c.connected {
		return nil // Already connected
	}

	// Create HTTP client for Connect RPCs
	// TODO: Add TLS, timeouts, interceptors from ClientOptions
	c.httpClient = &http.Client{}

	// TODO: Perform handshake
	// TODO: Start health monitoring if configured
	// TODO: Start endpoint watcher if using discovery

	c.connected = true
	return nil
}

// Dispense returns an implementation of the named plugin.
// This is the secondary API - prefer DispenseTyped[I] for type safety.
//
// The plugin interface is returned as interface{} and must be type-asserted:
//
//	raw, err := client.Dispense("kv")
//	kvStore := raw.(kv.KVStore)
func (c *Client) Dispense(name string) (any, error) {
	// Ensure connected (lazy connection)
	if err := c.ensureConnected(); err != nil {
		return nil, err
	}

	c.mu.RLock()
	defer c.mu.RUnlock()

	// Get plugin from set
	plugin, ok := c.cfg.Plugins.Get(name)
	if !ok {
		return nil, fmt.Errorf("%w: %q", ErrPluginNotFound, name)
	}

	// Create client instance
	return plugin.ConnectClient(c.cfg.Endpoint, c.httpClient)
}

// Close closes the client and releases resources.
// This should be called when the client is no longer needed.
func (c *Client) Close() error {
	c.mu.Lock()
	defer c.mu.Unlock()

	if c.closed {
		return nil
	}

	c.closed = true

	// TODO: Stop health monitoring
	// TODO: Stop endpoint watcher
	// TODO: Close HTTP client if we created it

	return nil
}

// ensureConnected ensures the client is connected.
// Must be called with read lock NOT held (it needs write lock).
func (c *Client) ensureConnected() error {
	c.mu.RLock()
	if c.connected {
		c.mu.RUnlock()
		return nil
	}
	c.mu.RUnlock()

	// Need to connect - call Connect with background context
	// TODO: Make context configurable
	return c.Connect(context.Background())
}
