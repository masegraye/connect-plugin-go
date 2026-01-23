// Package connectplugin provides a plugin system over Connect RPC.
//
// This package enables plugins to run as remote services (sidecars, containers,
// or separate hosts) while maintaining an interface-oriented programming model
// similar to HashiCorp's go-plugin.
//
// Unlike go-plugin which is designed for local subprocess communication,
// connect-plugin is designed for network communication with built-in support
// for service discovery, health checking, retries, and circuit breakers.
package connectplugin

// Protocol is the type of RPC protocol in use.
type Protocol string

const (
	// ProtocolConnect uses the Connect protocol (HTTP/1.1 or HTTP/2).
	ProtocolConnect Protocol = "connect"

	// ProtocolGRPC uses the gRPC protocol (HTTP/2 only).
	ProtocolGRPC Protocol = "grpc"

	// ProtocolGRPCWeb uses the gRPC-Web protocol.
	ProtocolGRPCWeb Protocol = "grpcweb"
)

// Plugin is the interface that must be implemented by all plugin types.
// Plugin authors implement this interface to define how to create client
// and server implementations for their plugin.
type Plugin interface {
	// Name returns the unique name of this plugin type.
	Name() string
}

// ConnectPlugin is the interface for plugins that use Connect RPC.
// This is the primary interface for connect-plugin.
type ConnectPlugin interface {
	Plugin

	// Client returns a client-side implementation of the plugin interface.
	// The implementation should use the provided ClientConn to make RPC calls.
	Client(conn ClientConn) (interface{}, error)

	// Server returns the HTTP handler for this plugin.
	// The implementation is registered with the plugin server.
	Server(impl interface{}) (Handler, error)
}

// PluginSet is a map of plugin name to Plugin implementation.
type PluginSet map[string]Plugin
