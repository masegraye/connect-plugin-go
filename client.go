package connectplugin

import (
	"context"
	"fmt"
	"net/http"
	"sync"

	"connectrpc.com/connect"
	connectpluginv1 "github.com/masegraye/connect-plugin-go/gen/plugin/v1"
	"github.com/masegraye/connect-plugin-go/gen/plugin/v1/connectpluginv1connect"
)

// ClientConfig is the minimal configuration required to create a plugin client.
// For most use cases, only Endpoint and Plugins are needed.
type ClientConfig struct {
	// Endpoint is the plugin service URL.
	// Required. Examples: "http://localhost:8080", "https://plugin.example.com"
	Endpoint string

	// HostURL is an alias for Endpoint (Phase 2 naming).
	// If both are provided, Endpoint takes precedence.
	HostURL string

	// Plugins defines available plugin types.
	// Required for Phase 1. Optional for Phase 2 (service providers).
	Plugins PluginSet

	// ProtocolVersion is the application protocol version.
	// Default: 1
	ProtocolVersion int

	// MagicCookieKey and Value for validation (not security).
	// Default: DefaultMagicCookieKey/Value
	MagicCookieKey   string
	MagicCookieValue string

	// Phase 2: SelfID is the plugin's self-declared identity.
	// Optional. If provided, host will assign a runtime_id.
	// Example: "cache-plugin", "my-app"
	SelfID string

	// Phase 2: SelfVersion is the plugin's self-declared version.
	// Optional. Used for debugging/logging.
	SelfVersion string

	// Phase 2: Metadata describes services this plugin provides/requires.
	// Optional. Used for service registration and dependency declaration.
	Metadata PluginMetadata
}

// Validate checks ClientConfig for errors.
func (cfg *ClientConfig) Validate() error {
	// Accept either Endpoint or HostURL
	if cfg.Endpoint == "" && cfg.HostURL == "" {
		return fmt.Errorf("%w: Endpoint or HostURL is required", ErrInvalidConfig)
	}

	// Normalize: use HostURL if Endpoint is empty
	if cfg.Endpoint == "" {
		cfg.Endpoint = cfg.HostURL
	}

	// Phase 2: Plugins is optional (service providers don't need to dispense plugins)
	if cfg.Plugins != nil && len(cfg.Plugins) > 0 {
		// Validate the plugin set if provided
		if err := cfg.Plugins.Validate(); err != nil {
			return fmt.Errorf("%w: %v", ErrInvalidConfig, err)
		}
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

	// Phase 2: Runtime identity assigned by host
	runtimeID    string
	runtimeToken string

	// Phase 2: Lifecycle client for reporting health
	lifecycleClient connectpluginv1connect.PluginLifecycleClient

	// Phase 2: Registry client for service discovery
	registryClient connectpluginv1connect.ServiceRegistryClient
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

	// Perform handshake
	if err := c.doHandshake(ctx); err != nil {
		return fmt.Errorf("handshake failed: %w", err)
	}

	// TODO: Start health monitoring if configured
	// TODO: Start endpoint watcher if using discovery

	c.connected = true
	return nil
}

// doHandshake performs the handshake protocol with the server.
func (c *Client) doHandshake(ctx context.Context) error {
	// Create handshake client
	handshakeClient := connectpluginv1connect.NewHandshakeServiceClient(
		c.httpClient,
		c.cfg.Endpoint,
	)

	// Set defaults
	protocolVersion := c.cfg.ProtocolVersion
	if protocolVersion == 0 {
		protocolVersion = 1
	}

	magicKey := c.cfg.MagicCookieKey
	magicValue := c.cfg.MagicCookieValue
	if magicKey == "" {
		magicKey = DefaultMagicCookieKey
		magicValue = DefaultMagicCookieValue
	}

	// Build handshake request
	req := &connectpluginv1.HandshakeRequest{
		CoreProtocolVersion: 1,
		AppProtocolVersion:  int32(protocolVersion),
		MagicCookieKey:      magicKey,
		MagicCookieValue:    magicValue,
		RequestedPlugins:    c.cfg.Plugins.Keys(),
		ClientMetadata: map[string]string{
			"client_version": "0.1.0", // TODO: Get from build version
		},
	}

	// Phase 2: Include self-identity if provided
	if c.cfg.SelfID != "" {
		req.SelfId = c.cfg.SelfID
		req.SelfVersion = c.cfg.SelfVersion
	}

	// Call handshake
	resp, err := handshakeClient.Handshake(ctx, connect.NewRequest(req))
	if err != nil {
		return err
	}

	// Validate response
	if resp.Msg.CoreProtocolVersion != 1 {
		return fmt.Errorf("core protocol version mismatch: got %d, want 1", resp.Msg.CoreProtocolVersion)
	}

	if resp.Msg.AppProtocolVersion != int32(protocolVersion) {
		return fmt.Errorf("app protocol version mismatch: got %d, want %d",
			resp.Msg.AppProtocolVersion, protocolVersion)
	}

	// Validate requested plugins are available
	availablePlugins := make(map[string]*connectpluginv1.PluginInfo)
	for _, p := range resp.Msg.Plugins {
		availablePlugins[p.Name] = p
	}

	for _, requested := range c.cfg.Plugins.Keys() {
		if _, ok := availablePlugins[requested]; !ok {
			return fmt.Errorf("requested plugin %q not available on server", requested)
		}
	}

	// Phase 2: Store runtime identity if assigned
	if resp.Msg.RuntimeId != "" {
		c.runtimeID = resp.Msg.RuntimeId
		c.runtimeToken = resp.Msg.RuntimeToken

		// Initialize lifecycle client for health reporting
		c.lifecycleClient = connectpluginv1connect.NewPluginLifecycleClient(
			c.httpClient,
			c.cfg.Endpoint,
		)

		// Initialize registry client for service discovery
		c.registryClient = connectpluginv1connect.NewServiceRegistryClient(
			c.httpClient,
			c.cfg.Endpoint,
		)
	}

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

// RuntimeID returns the host-assigned runtime ID for this client.
// Returns empty string if no runtime ID was assigned (Phase 1 mode).
func (c *Client) RuntimeID() string {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.runtimeID
}

// RuntimeToken returns the host-assigned runtime token for authentication.
// Returns empty string if no runtime token was assigned (Phase 1 mode).
func (c *Client) RuntimeToken() string {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.runtimeToken
}

// RegistryClient returns the service registry client for discovering services.
// This is a Phase 2 feature - returns nil if runtime identity was not assigned.
func (c *Client) RegistryClient() connectpluginv1connect.ServiceRegistryClient {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.registryClient
}

// Config returns the client configuration.
// This allows plugins to access their own metadata.
func (c *Client) Config() ClientConfig {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.cfg
}

// SetRuntimeIdentity sets the runtime identity assigned by the host (Model A).
// This is called by the plugin's PluginIdentity.SetRuntimeIdentity handler.
// For Model B (self-registering), identity is set during handshake automatically.
func (c *Client) SetRuntimeIdentity(runtimeID, runtimeToken, hostURL string) {
	c.mu.Lock()
	defer c.mu.Unlock()

	c.runtimeID = runtimeID
	c.runtimeToken = runtimeToken

	// Update endpoint if host URL provided
	if hostURL != "" && c.cfg.Endpoint != hostURL {
		c.cfg.Endpoint = hostURL
		c.cfg.HostURL = hostURL
	}

	// Initialize Phase 2 clients if not already done
	if c.lifecycleClient == nil && c.httpClient != nil {
		c.lifecycleClient = connectpluginv1connect.NewPluginLifecycleClient(
			c.httpClient,
			c.cfg.Endpoint,
		)
		c.registryClient = connectpluginv1connect.NewServiceRegistryClient(
			c.httpClient,
			c.cfg.Endpoint,
		)
	}
}

// ReportHealth reports the plugin's health state to the host.
// This is a Phase 2 feature - only works if runtime identity was assigned.
func (c *Client) ReportHealth(ctx context.Context, state connectpluginv1.HealthState, reason string, unavailableDeps []string) error {
	c.mu.RLock()
	lifecycleClient := c.lifecycleClient
	runtimeID := c.runtimeID
	runtimeToken := c.runtimeToken
	c.mu.RUnlock()

	if lifecycleClient == nil {
		return fmt.Errorf("ReportHealth requires Phase 2 runtime identity (provide SelfID in ClientConfig)")
	}

	// Create request with runtime identity in headers
	req := connect.NewRequest(&connectpluginv1.ReportHealthRequest{
		State:                   state,
		Reason:                  reason,
		UnavailableDependencies: unavailableDeps,
	})

	// Add runtime identity headers
	req.Header().Set("X-Plugin-Runtime-ID", runtimeID)
	req.Header().Set("Authorization", "Bearer "+runtimeToken)

	_, err := lifecycleClient.ReportHealth(ctx, req)
	return err
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
