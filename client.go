package connectplugin

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"net/http"
	"sync"
	"time"

	"connectrpc.com/connect"
)

// ClientConfig is the configuration for creating a new plugin client.
type ClientConfig struct {
	// Endpoint is the URL of the plugin service.
	// Example: "http://localhost:8080" or "https://plugin-svc.default.svc:8080"
	Endpoint string

	// Plugins are the plugins that can be consumed from this endpoint.
	Plugins PluginSet

	// VersionedPlugins is a map of PluginSets for specific protocol versions.
	// This can be used to negotiate a compatible version between client and server.
	VersionedPlugins map[int]PluginSet

	// ProtocolVersion is the protocol version to use.
	// If VersionedPlugins is set, this is used as the preferred version.
	ProtocolVersion int

	// Protocol specifies which RPC protocol to use.
	// Defaults to ProtocolConnect.
	Protocol Protocol

	// HTTPClient is the HTTP client to use for requests.
	// If nil, a default client with reasonable timeouts is created.
	HTTPClient *http.Client

	// TLSConfig is the TLS configuration for secure connections.
	// If nil and the endpoint uses https, the system root CAs are used.
	TLSConfig *tls.Config

	// ConnectTimeout is the timeout for establishing the initial connection.
	// Defaults to 30 seconds.
	ConnectTimeout time.Duration

	// RequestTimeout is the default timeout for RPC requests.
	// Defaults to 60 seconds. Can be overridden per-request with context.
	RequestTimeout time.Duration

	// Interceptors are Connect interceptors applied to all RPC calls.
	Interceptors []connect.Interceptor
}

// Client manages the connection to a plugin service and dispenses
// plugin implementations.
type Client struct {
	config   *ClientConfig
	mu       sync.Mutex
	conn     ClientConn
	protocol ClientProtocol
	closed   bool
}

// NewClient creates a new plugin client with the given configuration.
func NewClient(config *ClientConfig) *Client {
	if config.ConnectTimeout == 0 {
		config.ConnectTimeout = 30 * time.Second
	}
	if config.RequestTimeout == 0 {
		config.RequestTimeout = 60 * time.Second
	}
	if config.Protocol == "" {
		config.Protocol = ProtocolConnect
	}
	if config.HTTPClient == nil {
		transport := &http.Transport{
			TLSClientConfig: config.TLSConfig,
		}
		config.HTTPClient = &http.Client{
			Transport: transport,
			Timeout:   config.RequestTimeout,
		}
	}

	return &Client{
		config: config,
	}
}

// Client returns the protocol client for this connection.
// This establishes the connection if not already connected.
func (c *Client) Client() (ClientProtocol, error) {
	c.mu.Lock()
	defer c.mu.Unlock()

	if c.closed {
		return nil, errors.New("client is closed")
	}

	if c.protocol != nil {
		return c.protocol, nil
	}

	// Create the connection
	conn := &connectClientConn{
		endpoint:     c.config.Endpoint,
		httpClient:   c.config.HTTPClient,
		interceptors: c.config.Interceptors,
		protocol:     c.config.Protocol,
	}
	c.conn = conn

	// Create the protocol client
	c.protocol = &connectProtocolClient{
		conn:    conn,
		plugins: c.config.Plugins,
	}

	return c.protocol, nil
}

// Close closes the client connection.
func (c *Client) Close() error {
	c.mu.Lock()
	defer c.mu.Unlock()

	if c.closed {
		return nil
	}
	c.closed = true

	if c.conn != nil {
		return c.conn.Close()
	}
	return nil
}

// ClientProtocol is the interface for dispensing plugins.
type ClientProtocol interface {
	// Dispense returns an implementation of the named plugin.
	Dispense(name string) (interface{}, error)

	// Ping checks if the plugin service is reachable.
	Ping(ctx context.Context) error

	// Close closes the protocol connection.
	Close() error
}

// ClientConn represents a connection to a plugin service.
type ClientConn interface {
	// Endpoint returns the endpoint URL.
	Endpoint() string

	// HTTPClient returns the HTTP client used for requests.
	HTTPClient() *http.Client

	// Interceptors returns the Connect interceptors.
	Interceptors() []connect.Interceptor

	// Close closes the connection.
	Close() error
}

// connectClientConn implements ClientConn for Connect RPC.
type connectClientConn struct {
	endpoint     string
	httpClient   *http.Client
	interceptors []connect.Interceptor
	protocol     Protocol
}

func (c *connectClientConn) Endpoint() string {
	return c.endpoint
}

func (c *connectClientConn) HTTPClient() *http.Client {
	return c.httpClient
}

func (c *connectClientConn) Interceptors() []connect.Interceptor {
	return c.interceptors
}

func (c *connectClientConn) Close() error {
	// HTTP client connections are pooled and managed automatically
	return nil
}

// connectProtocolClient implements ClientProtocol for Connect RPC.
type connectProtocolClient struct {
	conn    ClientConn
	plugins PluginSet
}

func (c *connectProtocolClient) Dispense(name string) (interface{}, error) {
	raw, ok := c.plugins[name]
	if !ok {
		return nil, fmt.Errorf("unknown plugin type: %s", name)
	}

	p, ok := raw.(ConnectPlugin)
	if !ok {
		return nil, fmt.Errorf("plugin %q does not implement ConnectPlugin", name)
	}

	return p.Client(c.conn)
}

func (c *connectProtocolClient) Ping(ctx context.Context) error {
	// TODO: Implement health check ping
	return nil
}

func (c *connectProtocolClient) Close() error {
	return c.conn.Close()
}
