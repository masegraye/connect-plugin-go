package connectplugin

import (
	"context"
	"fmt"
	"net/http"

	"connectrpc.com/connect"
	connectpluginv1 "github.com/example/connect-plugin-go/gen/plugin/v1"
	"github.com/example/connect-plugin-go/gen/plugin/v1/connectpluginv1connect"
)

const (
	// DefaultMagicCookieKey is the default magic cookie key.
	DefaultMagicCookieKey = "CONNECT_PLUGIN"

	// DefaultMagicCookieValue is the default magic cookie value.
	DefaultMagicCookieValue = "d3f40b3c2e1a5f8b9c4d7e6a1b2c3d4e"
)

// HandshakeServer implements the handshake protocol server.
type HandshakeServer struct {
	cfg *ServeConfig
}

// NewHandshakeServer creates a new handshake server for the given configuration.
func NewHandshakeServer(cfg *ServeConfig) *HandshakeServer {
	return &HandshakeServer{cfg: cfg}
}

// Handshake implements the handshake RPC.
func (h *HandshakeServer) Handshake(
	ctx context.Context,
	req *connect.Request[connectpluginv1.HandshakeRequest],
) (*connect.Response[connectpluginv1.HandshakeResponse], error) {
	// Validate magic cookie
	expectedKey := h.cfg.MagicCookieKey
	expectedValue := h.cfg.MagicCookieValue
	if expectedKey == "" {
		expectedKey = DefaultMagicCookieKey
		expectedValue = DefaultMagicCookieValue
	}

	if req.Msg.MagicCookieKey != expectedKey || req.Msg.MagicCookieValue != expectedValue {
		return nil, connect.NewError(
			connect.CodeInvalidArgument,
			fmt.Errorf("invalid magic cookie - this may not be a connect-plugin server"),
		)
	}

	// Validate core protocol version
	if req.Msg.CoreProtocolVersion != 1 {
		return nil, connect.NewError(
			connect.CodeInvalidArgument,
			fmt.Errorf("unsupported core protocol version: %d (server supports: 1)", req.Msg.CoreProtocolVersion),
		)
	}

	// Validate app protocol version (v1: exact match only)
	serverVersion := h.cfg.ProtocolVersion
	if serverVersion == 0 {
		serverVersion = 1
	}

	if req.Msg.AppProtocolVersion != int32(serverVersion) {
		return nil, connect.NewError(
			connect.CodeFailedPrecondition,
			fmt.Errorf("version mismatch: client=%d, server=%d", req.Msg.AppProtocolVersion, serverVersion),
		)
	}

	// Build plugin info for requested plugins
	plugins := make([]*connectpluginv1.PluginInfo, 0, len(req.Msg.RequestedPlugins))
	for _, requestedName := range req.Msg.RequestedPlugins {
		plugin, ok := h.cfg.Plugins.Get(requestedName)
		if !ok {
			// Plugin not available - skip it (client will error)
			continue
		}

		metadata := plugin.Metadata()
		plugins = append(plugins, &connectpluginv1.PluginInfo{
			Name:        metadata.Name,
			Version:     metadata.Version,
			ServicePath: metadata.Path,
		})
	}

	// Build server metadata
	serverMetadata := make(map[string]string)
	serverMetadata["server_version"] = "0.1.0" // TODO: Get from build version
	if h.cfg.ServerMetadata != nil {
		for k, v := range h.cfg.ServerMetadata {
			serverMetadata[k] = v
		}
	}

	// Get host capabilities from broker (if enabled)
	var hostCapabilities []*connectpluginv1.Capability
	if h.cfg.CapabilityBroker != nil {
		hostCapabilities = h.cfg.CapabilityBroker.ListCapabilities()
	}

	return connect.NewResponse(&connectpluginv1.HandshakeResponse{
		CoreProtocolVersion: 1,
		AppProtocolVersion:  int32(serverVersion),
		Plugins:             plugins,
		ServerMetadata:      serverMetadata,
		HostCapabilities:    hostCapabilities,
	}), nil
}

// HandshakeServerHandler returns the path and handler for the handshake service.
func HandshakeServerHandler(server *HandshakeServer) (string, http.Handler) {
	return connectpluginv1connect.NewHandshakeServiceHandler(server)
}
