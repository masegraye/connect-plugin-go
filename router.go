package connectplugin

import (
	"fmt"
	"io"
	"log"
	"net/http"
	"strings"
	"time"
)

// ServiceRouter routes plugin-to-plugin service calls through the host.
// All calls follow the pattern: /services/{type}/{provider-id}/{method...}
type ServiceRouter struct {
	handshakeServer *HandshakeServer
	registry        *ServiceRegistry
	lifecycleServer *LifecycleServer

	// Plugin base URLs for proxying
	pluginEndpoints map[string]string // runtime_id → base URL
}

// NewServiceRouter creates a new service router.
func NewServiceRouter(
	handshake *HandshakeServer,
	registry *ServiceRegistry,
	lifecycle *LifecycleServer,
) *ServiceRouter {
	return &ServiceRouter{
		handshakeServer: handshake,
		registry:        registry,
		lifecycleServer: lifecycle,
		pluginEndpoints: make(map[string]string),
	}
}

// RegisterPluginEndpoint registers a plugin's internal endpoint for routing.
// This is called during plugin startup to tell the router where to proxy calls.
func (r *ServiceRouter) RegisterPluginEndpoint(runtimeID, endpoint string) {
	r.pluginEndpoints[runtimeID] = endpoint
}

// ServeHTTP implements http.Handler for /services/* routes.
func (r *ServiceRouter) ServeHTTP(w http.ResponseWriter, req *http.Request) {
	// Only handle /services/* paths
	if !strings.HasPrefix(req.URL.Path, "/services/") {
		http.NotFound(w, req)
		return
	}

	// Parse path: /services/{type}/{provider-id}/{method...}
	pathRemainder := strings.TrimPrefix(req.URL.Path, "/services/")
	parts := strings.SplitN(pathRemainder, "/", 3)
	if len(parts) < 3 {
		log.Printf("[ROUTER] Invalid path: %s (parts: %d)", req.URL.Path, len(parts))
		http.Error(w, "invalid service path format, expected /services/{type}/{provider-id}/{method}", http.StatusBadRequest)
		return
	}

	serviceType := parts[0]
	providerID := parts[1]
	method := "/" + parts[2]

	// Extract caller identity from headers
	callerID := req.Header.Get("X-Plugin-Runtime-ID")
	if callerID == "" {
		log.Printf("[ROUTER] Missing X-Plugin-Runtime-ID header for %s", req.URL.Path)
		http.Error(w, "X-Plugin-Runtime-ID header required", http.StatusUnauthorized)
		return
	}

	// Extract and validate token
	authHeader := req.Header.Get("Authorization")
	if !strings.HasPrefix(authHeader, "Bearer ") {
		log.Printf("[ROUTER] Missing/invalid Authorization header for %s (caller: %s)", req.URL.Path, callerID)
		http.Error(w, "Authorization: Bearer <token> required", http.StatusUnauthorized)
		return
	}
	token := strings.TrimPrefix(authHeader, "Bearer ")

	// Validate token
	if !r.handshakeServer.ValidateToken(callerID, token) {
		log.Printf("[ROUTER] Invalid token for caller: %s", callerID)
		http.Error(w, "invalid token", http.StatusUnauthorized)
		return
	}

	// Look up provider by runtime ID
	provider, err := r.registry.GetProviderByRuntimeID(providerID)
	if err != nil {
		http.Error(w, fmt.Sprintf("provider not found: %s", providerID), http.StatusNotFound)
		return
	}

	// Check provider health
	if !r.lifecycleServer.ShouldRouteTraffic(providerID) {
		http.Error(w, "service unavailable (provider unhealthy)", http.StatusServiceUnavailable)
		return
	}

	// Get provider's internal endpoint
	// First try registered endpoint (Model A via Platform.AddPlugin)
	baseURL, ok := r.pluginEndpoints[providerID]
	if !ok {
		// Fall back to metadata base_url (Model B self-registration)
		if baseURLMeta, exists := provider.Metadata["base_url"]; exists {
			baseURL = baseURLMeta
		} else {
			http.Error(w, fmt.Sprintf("provider endpoint not registered: %s", providerID), http.StatusNotFound)
			return
		}
	}

	// Log the call
	start := time.Now()
	log.Printf("[ROUTER] %s → %s %s (service: %s)",
		callerID, providerID, method, serviceType)

	// Proxy the request
	targetURL := baseURL + provider.EndpointPath + strings.TrimPrefix(method, "/")
	statusCode, err := r.proxyRequest(w, req, targetURL)

	// Log completion
	duration := time.Since(start)
	if err != nil {
		log.Printf("[ROUTER] %s → %s %s FAILED: %v (duration: %s)",
			callerID, providerID, method, err, duration)
	} else {
		log.Printf("[ROUTER] %s → %s %s %d (duration: %s)",
			callerID, providerID, method, statusCode, duration)
	}
}

// proxyRequest proxies an HTTP request to the target URL.
// Returns status code and any error.
func (r *ServiceRouter) proxyRequest(w http.ResponseWriter, req *http.Request, targetURL string) (int, error) {
	// Create proxy request with query parameters from original request
	fullURL := targetURL
	if req.URL.RawQuery != "" {
		fullURL = targetURL + "?" + req.URL.RawQuery
	}

	proxyReq, err := http.NewRequestWithContext(req.Context(), req.Method, fullURL, req.Body)
	if err != nil {
		http.Error(w, "failed to create proxy request", http.StatusInternalServerError)
		return http.StatusInternalServerError, err
	}

	// Copy headers (except Authorization and X-Plugin-Runtime-ID)
	for key, values := range req.Header {
		// Header keys are canonicalized, so compare with canonical form
		canonicalKey := http.CanonicalHeaderKey(key)
		if canonicalKey == "Authorization" || canonicalKey == "X-Plugin-Runtime-Id" {
			continue
		}
		for _, value := range values {
			proxyReq.Header.Add(key, value)
		}
	}

	// Execute proxy request
	client := &http.Client{
		Timeout: 30 * time.Second,
	}
	resp, err := client.Do(proxyReq)
	if err != nil {
		http.Error(w, "failed to proxy request", http.StatusBadGateway)
		return http.StatusBadGateway, err
	}
	defer resp.Body.Close()

	// Copy response headers
	for key, values := range resp.Header {
		for _, value := range values {
			w.Header().Add(key, value)
		}
	}

	// Copy status code
	w.WriteHeader(resp.StatusCode)

	// Copy response body
	_, copyErr := io.Copy(w, resp.Body)

	return resp.StatusCode, copyErr
}
