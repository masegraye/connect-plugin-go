package connectplugin

import (
	"context"
	"crypto/rand"
	"crypto/subtle"
	"encoding/base64"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"time"

	"connectrpc.com/connect"
	connectpluginv1 "github.com/masegraye/connect-plugin-go/gen/plugin/v1"
	"github.com/masegraye/connect-plugin-go/gen/plugin/v1/connectpluginv1connect"
)

const (
	// DefaultCapabilityGrantTTL is the default time-to-live for capability grants.
	DefaultCapabilityGrantTTL = 1 * time.Hour
)

// CapabilityHandler is the interface for host capabilities.
// Hosts implement this to provide capabilities to plugins.
type CapabilityHandler interface {
	http.Handler

	// CapabilityType returns the capability type (e.g., "logger", "secrets").
	CapabilityType() string

	// Version returns the capability version (semver).
	Version() string
}

// CapabilityBroker manages host capabilities and issues capability grants.
type CapabilityBroker struct {
	mu           sync.RWMutex
	capabilities map[string]CapabilityHandler
	grants       map[string]*grantInfo
	baseURL      string
	grantTTL     time.Duration // Time-to-live for capability grants
}

type grantInfo struct {
	grantID        string
	capabilityType string
	token          string
	handler        CapabilityHandler
	issuedAt       time.Time
	expiresAt      time.Time
}

// NewCapabilityBroker creates a new capability broker.
func NewCapabilityBroker(baseURL string) *CapabilityBroker {
	return &CapabilityBroker{
		capabilities: make(map[string]CapabilityHandler),
		grants:       make(map[string]*grantInfo),
		baseURL:      baseURL,
		grantTTL:     DefaultCapabilityGrantTTL,
	}
}

// RegisterCapability registers a host capability.
func (b *CapabilityBroker) RegisterCapability(handler CapabilityHandler) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.capabilities[handler.CapabilityType()] = handler
}

// ListCapabilities returns available capabilities for handshake advertisement.
func (b *CapabilityBroker) ListCapabilities() []*connectpluginv1.Capability {
	b.mu.RLock()
	defer b.mu.RUnlock()

	caps := make([]*connectpluginv1.Capability, 0, len(b.capabilities))
	for _, handler := range b.capabilities {
		caps = append(caps, &connectpluginv1.Capability{
			Type:     handler.CapabilityType(),
			Version:  handler.Version(),
			Endpoint: b.baseURL + "/broker",
		})
	}
	return caps
}

// RequestCapability implements the CapabilityBroker RPC.
func (b *CapabilityBroker) RequestCapability(
	ctx context.Context,
	req *connect.Request[connectpluginv1.RequestCapabilityRequest],
) (*connect.Response[connectpluginv1.RequestCapabilityResponse], error) {
	b.mu.Lock()
	defer b.mu.Unlock()

	// Find capability handler
	handler, ok := b.capabilities[req.Msg.CapabilityType]
	if !ok {
		return nil, connect.NewError(
			connect.CodeNotFound,
			fmt.Errorf("capability %q not available", req.Msg.CapabilityType),
		)
	}

	// TODO: Check min_version compatibility

	// Generate grant
	grantID, err := generateGrantID()
	if err != nil {
		return nil, connect.NewError(connect.CodeInternal, err)
	}
	token, err := generateToken()
	if err != nil {
		return nil, connect.NewError(connect.CodeInternal, err)
	}

	now := time.Now()
	grant := &grantInfo{
		grantID:        grantID,
		capabilityType: req.Msg.CapabilityType,
		token:          token,
		handler:        handler,
		issuedAt:       now,
		expiresAt:      now.Add(b.grantTTL),
	}
	b.grants[grantID] = grant

	// Build response
	return connect.NewResponse(&connectpluginv1.RequestCapabilityResponse{
		Grant: &connectpluginv1.CapabilityGrant{
			GrantId:        grantID,
			EndpointUrl:    fmt.Sprintf("%s/capabilities/%s/%s", b.baseURL, req.Msg.CapabilityType, grantID),
			BearerToken:    token,
			CapabilityType: req.Msg.CapabilityType,
			Version:        handler.Version(),
		},
	}), nil
}

// Handler returns the HTTP handler for the broker.
// It routes capability requests to the appropriate handler.
func (b *CapabilityBroker) Handler() http.Handler {
	mux := http.NewServeMux()

	// Register broker RPC service
	brokerPath, brokerHandler := connectpluginv1connect.NewCapabilityBrokerHandler(b)
	mux.Handle(brokerPath, brokerHandler)

	// Register capability routing handler
	mux.HandleFunc("/capabilities/", b.handleCapabilityRequest)

	return mux
}

// handleCapabilityRequest routes capability requests to the appropriate handler.
// URL format: /capabilities/{type}/{grant_id}/{...}
func (b *CapabilityBroker) handleCapabilityRequest(w http.ResponseWriter, r *http.Request) {
	// Parse path: /capabilities/{type}/{grant_id}/{...}
	parts := strings.Split(strings.TrimPrefix(r.URL.Path, "/capabilities/"), "/")
	if len(parts) < 2 {
		http.Error(w, "invalid capability path", http.StatusBadRequest)
		return
	}

	grantID := parts[1]

	// Extract bearer token
	auth := r.Header.Get("Authorization")
	if !strings.HasPrefix(auth, "Bearer ") {
		http.Error(w, "missing or invalid authorization header", http.StatusUnauthorized)
		return
	}
	token := strings.TrimPrefix(auth, "Bearer ")

	// Validate grant (with expiration check and lazy cleanup)
	b.mu.Lock()
	defer b.mu.Unlock()

	grant, ok := b.grants[grantID]
	if !ok {
		http.Error(w, "invalid grant ID", http.StatusUnauthorized)
		return
	}

	// Check expiration (lazy cleanup)
	if time.Now().After(grant.expiresAt) {
		delete(b.grants, grantID)
		http.Error(w, "grant expired", http.StatusUnauthorized)
		return
	}

	// Use constant-time comparison to prevent timing attacks
	if len(grant.token) != len(token) {
		http.Error(w, "invalid token", http.StatusUnauthorized)
		return
	}
	if subtle.ConstantTimeCompare([]byte(grant.token), []byte(token)) != 1 {
		http.Error(w, "invalid token", http.StatusUnauthorized)
		return
	}

	// Strip capability prefix and route to handler
	// /capabilities/{type}/{grant_id}/Method -> /Method
	if len(parts) > 2 {
		r.URL.Path = "/" + strings.Join(parts[2:], "/")
	} else {
		r.URL.Path = "/"
	}

	// Route to capability handler
	grant.handler.ServeHTTP(w, r)
}

// generateGrantID generates a random grant ID.
// Returns an error if crypto/rand.Read fails.
func generateGrantID() (string, error) {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		return "", fmt.Errorf("failed to generate grant ID: %w", err)
	}
	return base64.URLEncoding.EncodeToString(b), nil
}

// generateToken generates a random bearer token.
// Returns an error if crypto/rand.Read fails.
func generateToken() (string, error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", fmt.Errorf("failed to generate secure token: %w", err)
	}
	return base64.URLEncoding.EncodeToString(b), nil
}
