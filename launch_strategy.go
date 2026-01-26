package connectplugin

import "context"

// LaunchStrategy defines how plugins are instantiated and started.
// Implementations include ProcessStrategy (child processes), InMemoryStrategy (goroutines),
// and future strategies like RemoteStrategy (already-running services).
type LaunchStrategy interface {
	// Launch starts a plugin and returns its endpoint URL.
	// The strategy is responsible for starting the plugin server and ensuring it's ready.
	//
	// Returns:
	//   - endpoint: URL where plugin is listening (e.g., "http://localhost:8081")
	//   - cleanup: Function to stop/cleanup the plugin (called on shutdown)
	//   - error: If launch failed
	Launch(ctx context.Context, spec PluginSpec) (string, func(), error)

	// Name returns the strategy name for registration (e.g., "process", "in-memory")
	Name() string
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

	// Plugin is the plugin wrapper (e.g., &kvplugin.KVServicePlugin{})
	// Used by InMemoryStrategy
	Plugin Plugin

	// ImplFactory creates the plugin implementation (used by InMemoryStrategy)
	// Returns the handler interface (e.g., &LoggerImpl{}, &CacheImpl{})
	ImplFactory func() any

	// === Metadata ===

	// Metadata contains additional plugin metadata (version, description, etc.)
	Metadata map[string]string
}
