package connectplugin

import "errors"

// Common errors returned by connect-plugin operations.
var (
	// ErrEmptyPluginSet is returned when a PluginSet has no plugins.
	ErrEmptyPluginSet = errors.New("plugin set is empty")

	// ErrPluginNotFound is returned when a requested plugin is not in the PluginSet.
	ErrPluginNotFound = errors.New("plugin not found")

	// ErrClientClosed is returned when operating on a closed client.
	ErrClientClosed = errors.New("client is closed")

	// ErrClientNotConnected is returned when operating on a client that hasn't connected.
	ErrClientNotConnected = errors.New("client not connected")

	// ErrNoEndpoints is returned when discovery finds no endpoints.
	ErrNoEndpoints = errors.New("no endpoints available")

	// ErrNoReadyEndpoints is returned when no endpoints are ready.
	ErrNoReadyEndpoints = errors.New("no ready endpoints")

	// ErrInvalidConfig is returned when configuration validation fails.
	ErrInvalidConfig = errors.New("invalid configuration")
)
