package connectplugin

import (
	"context"

	"connectrpc.com/connect"
)

// LaunchStrategy defines how plugins are instantiated and started.
// Implementations include ProcessStrategy (child processes) and
// InMemoryStrategy (in-process net.Pipe transport).
type LaunchStrategy interface {
	// Launch starts a plugin and returns a LaunchResult.
	// The strategy is responsible for starting the plugin server and ensuring it's ready.
	Launch(ctx context.Context, spec PluginSpec) (LaunchResult, error)

	// Name returns the strategy name for registration (e.g., "process", "in-memory")
	Name() string
}

// LaunchResult contains the result of launching a plugin.
type LaunchResult struct {
	// Endpoint is the URL where the plugin is listening (e.g., "http://localhost:8081").
	// For InMemoryStrategy this is a placeholder like "http://in-memory.plugin-name".
	Endpoint string

	// HTTPClient is an optional in-memory HTTP client.
	// When non-nil, callers should use this instead of creating an HTTP client from Endpoint.
	// This is set by InMemoryStrategy (backed by memtransport) and nil for TCP-based strategies.
	HTTPClient connect.HTTPClient

	// Cleanup is called to stop/cleanup the plugin on shutdown.
	Cleanup func()
}

// PluginSpec describes a plugin and how to launch it.
type PluginSpec struct {
	// Name is the plugin identifier (e.g., "logger-plugin", "data-plugin")
	Name string

	// Provides lists all service types this plugin provides.
	// A single plugin can provide multiple services.
	// Example: []string{"cache", "storage"} for a data-plugin
	Provides []string

	// Strategy specifies which LaunchStrategy to use.
	// Must match a registered strategy name ("process", "in-memory", etc.)
	Strategy string

	// Port is the port the plugin will listen on.
	Port int

	// === Process Strategy Fields ===

	// BinaryPath is the path to the plugin binary (used by ProcessStrategy)
	BinaryPath string

	// HostURL is the URL of the host platform (e.g., "http://localhost:8080")
	// Used by ProcessStrategy to set HOST_URL environment variable
	HostURL string

	// === In-Memory Strategy Fields ===

	// Plugin is the plugin wrapper (e.g., &kvplugin.KVServicePlugin{}).
	// Required for InMemoryStrategy.
	Plugin Plugin

	// ImplFactory creates the plugin implementation.
	// Required for InMemoryStrategy.
	// Returns the handler interface (e.g., &LoggerImpl{}, &CacheImpl{})
	ImplFactory func() any

	// === Metadata ===

	// Metadata contains additional plugin metadata (version, description, etc.)
	Metadata map[string]string
}
