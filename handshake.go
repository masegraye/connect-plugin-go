package connectplugin

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"net/http"
	"strings"
	"sync"

	"connectrpc.com/connect"
	connectpluginv1 "github.com/masegraye/connect-plugin-go/gen/plugin/v1"
	"github.com/masegraye/connect-plugin-go/gen/plugin/v1/connectpluginv1connect"
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

	// Phase 2: Token storage for runtime identity
	mu     sync.RWMutex
	tokens map[string]string // runtime_id → token
}

// NewHandshakeServer creates a new handshake server for the given configuration.
func NewHandshakeServer(cfg *ServeConfig) *HandshakeServer {
	return &HandshakeServer{
		cfg:    cfg,
		tokens: make(map[string]string),
	}
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

	// Phase 2: Generate runtime identity
	var runtimeID, runtimeToken string
	if req.Msg.SelfId != "" {
		// Plugin provided self_id - generate runtime identity
		runtimeID = generateRuntimeID(req.Msg.SelfId)
		runtimeToken = generateToken()

		// Store token for later validation
		h.mu.Lock()
		h.tokens[runtimeID] = runtimeToken
		h.mu.Unlock()
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
		pluginInfo := &connectpluginv1.PluginInfo{
			Name:        metadata.Name,
			Version:     metadata.Version,
			ServicePath: metadata.Path,
		}

		// Phase 2: Add service declarations
		if len(metadata.Provides) > 0 {
			provides := make([]*connectpluginv1.ServiceDeclaration, len(metadata.Provides))
			for i, svc := range metadata.Provides {
				provides[i] = &connectpluginv1.ServiceDeclaration{
					Type:    svc.Type,
					Version: svc.Version,
					Path:    svc.Path,
				}
			}
			pluginInfo.Provides = provides
		}

		if len(metadata.Requires) > 0 {
			requires := make([]*connectpluginv1.ServiceDependency, len(metadata.Requires))
			for i, dep := range metadata.Requires {
				requires[i] = &connectpluginv1.ServiceDependency{
					Type:                dep.Type,
					MinVersion:          dep.MinVersion,
					RequiredForStartup:  dep.RequiredForStartup,
					WatchForChanges:     dep.WatchForChanges,
				}
			}
			pluginInfo.Requires = requires
		}

		plugins = append(plugins, pluginInfo)
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

	resp := &connectpluginv1.HandshakeResponse{
		CoreProtocolVersion: 1,
		AppProtocolVersion:  int32(serverVersion),
		Plugins:             plugins,
		ServerMetadata:      serverMetadata,
		HostCapabilities:    hostCapabilities,
	}

	// Phase 2: Include runtime identity if generated
	if runtimeID != "" {
		resp.RuntimeId = runtimeID
		resp.RuntimeToken = runtimeToken
	}

	return connect.NewResponse(resp), nil
}

// HandshakeServerHandler returns the path and handler for the handshake service.
func HandshakeServerHandler(server *HandshakeServer) (string, http.Handler) {
	return connectpluginv1connect.NewHandshakeServiceHandler(server)
}

// ValidateToken validates a runtime token for the given runtime ID.
// Returns true if the token is valid.
func (h *HandshakeServer) ValidateToken(runtimeID, token string) bool {
	h.mu.RLock()
	defer h.mu.RUnlock()

	expectedToken, ok := h.tokens[runtimeID]
	if !ok {
		return false
	}

	return expectedToken == token
}

// generateRuntimeID generates a unique runtime ID from the plugin's self-declared ID.
// Format: {self_id}-{random_suffix}
// Example: "cache-plugin" → "cache-plugin-x7k2"
func generateRuntimeID(selfID string) string {
	// Generate 4-character random suffix
	suffix := generateRandomHex(4)

	// Normalize self_id (lowercase, replace spaces with hyphens)
	normalized := strings.ToLower(strings.ReplaceAll(selfID, " ", "-"))

	return fmt.Sprintf("%s-%s", normalized, suffix)
}

// generateRandomHex generates a cryptographically secure random hex string.
func generateRandomHex(length int) string {
	bytes := make([]byte, (length+1)/2)
	if _, err := rand.Read(bytes); err != nil {
		// Fall back to a less secure but still random method
		// This should never happen in practice
		panic(fmt.Sprintf("crypto/rand.Read failed: %v", err))
	}
	return hex.EncodeToString(bytes)[:length]
}
