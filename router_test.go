package connectplugin

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"connectrpc.com/connect"
	connectpluginv1 "github.com/masegraye/connect-plugin-go/gen/plugin/v1"
)

func TestServiceRouter_ValidRequest(t *testing.T) {
	// Set up dependencies
	handshake := NewHandshakeServer(&ServeConfig{})
	lifecycle := NewLifecycleServer()
	registry := NewServiceRegistry(lifecycle)
	router := NewServiceRouter(handshake, registry, lifecycle)

	// Generate runtime identity
	runtimeID := generateRuntimeID("test-plugin")
	token := generateToken()
	handshake.mu.Lock()
	handshake.tokens[runtimeID] = token
	handshake.mu.Unlock()

	// Register service provider
	regReq := connect.NewRequest(&connectpluginv1.RegisterServiceRequest{
		ServiceType:  "logger",
		Version:      "1.0.0",
		EndpointPath: "/logger.v1.Logger/",
	})
	regReq.Header().Set("X-Plugin-Runtime-ID", runtimeID)
	registry.RegisterService(context.Background(), regReq)

	// Mark provider as healthy
	healthReq := connect.NewRequest(&connectpluginv1.ReportHealthRequest{
		State: connectpluginv1.HealthState_HEALTH_STATE_HEALTHY,
	})
	healthReq.Header().Set("X-Plugin-Runtime-ID", runtimeID)
	lifecycle.ReportHealth(context.Background(), healthReq)

	// Create mock provider server
	providerCalled := false
	providerServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		providerCalled = true
		if r.URL.Path != "/logger.v1.Logger/Log" {
			t.Errorf("Expected path /logger.v1.Logger/Log, got %s", r.URL.Path)
		}
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`{"success": true}`))
	}))
	defer providerServer.Close()

	// Register provider endpoint
	router.RegisterPluginEndpoint(runtimeID, providerServer.URL)

	// Create request from caller plugin
	callerID := generateRuntimeID("caller-plugin")
	callerToken := generateToken()
	handshake.mu.Lock()
	handshake.tokens[callerID] = callerToken
	handshake.mu.Unlock()

	req := httptest.NewRequest(
		"POST",
		"/services/logger/"+runtimeID+"/Log",
		strings.NewReader(`{"message": "test"}`),
	)
	req.Header.Set("X-Plugin-Runtime-ID", callerID)
	req.Header.Set("Authorization", "Bearer "+callerToken)
	req.Header.Set("Content-Type", "application/json")

	// Execute request
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	// Verify response
	if w.Code != http.StatusOK {
		t.Errorf("Expected status 200, got %d", w.Code)
	}

	if !providerCalled {
		t.Error("Provider was not called")
	}

	body := w.Body.String()
	if !strings.Contains(body, "success") {
		t.Errorf("Expected success in response, got: %s", body)
	}
}

func TestServiceRouter_MissingRuntimeID(t *testing.T) {
	handshake := NewHandshakeServer(&ServeConfig{})
	lifecycle := NewLifecycleServer()
	registry := NewServiceRegistry(lifecycle)
	router := NewServiceRouter(handshake, registry, lifecycle)

	req := httptest.NewRequest("POST", "/services/logger/some-provider/Log", nil)
	// No X-Plugin-Runtime-ID header

	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	if w.Code != http.StatusUnauthorized {
		t.Errorf("Expected 401 Unauthorized, got %d", w.Code)
	}
}

func TestServiceRouter_InvalidToken(t *testing.T) {
	handshake := NewHandshakeServer(&ServeConfig{})
	lifecycle := NewLifecycleServer()
	registry := NewServiceRegistry(lifecycle)
	router := NewServiceRouter(handshake, registry, lifecycle)

	// Register valid caller but use wrong token
	callerID := generateRuntimeID("caller")
	validToken := generateToken()
	handshake.mu.Lock()
	handshake.tokens[callerID] = validToken
	handshake.mu.Unlock()

	req := httptest.NewRequest("POST", "/services/logger/some-provider/Log", nil)
	req.Header.Set("X-Plugin-Runtime-ID", callerID)
	req.Header.Set("Authorization", "Bearer wrong-token")

	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	if w.Code != http.StatusUnauthorized {
		t.Errorf("Expected 401 Unauthorized, got %d", w.Code)
	}
}

func TestServiceRouter_ProviderNotFound(t *testing.T) {
	handshake := NewHandshakeServer(&ServeConfig{})
	lifecycle := NewLifecycleServer()
	registry := NewServiceRegistry(lifecycle)
	router := NewServiceRouter(handshake, registry, lifecycle)

	// Register valid caller
	callerID := generateRuntimeID("caller")
	token := generateToken()
	handshake.mu.Lock()
	handshake.tokens[callerID] = token
	handshake.mu.Unlock()

	req := httptest.NewRequest("POST", "/services/logger/unknown-provider/Log", nil)
	req.Header.Set("X-Plugin-Runtime-ID", callerID)
	req.Header.Set("Authorization", "Bearer "+token)

	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	if w.Code != http.StatusNotFound {
		t.Errorf("Expected 404 Not Found, got %d", w.Code)
	}
}

func TestServiceRouter_UnhealthyProvider(t *testing.T) {
	handshake := NewHandshakeServer(&ServeConfig{})
	lifecycle := NewLifecycleServer()
	registry := NewServiceRegistry(lifecycle)
	router := NewServiceRouter(handshake, registry, lifecycle)

	// Register provider
	providerID := generateRuntimeID("logger-plugin")
	providerToken := generateToken()
	handshake.mu.Lock()
	handshake.tokens[providerID] = providerToken
	handshake.mu.Unlock()

	regReq := connect.NewRequest(&connectpluginv1.RegisterServiceRequest{
		ServiceType:  "logger",
		Version:      "1.0.0",
		EndpointPath: "/logger.v1.Logger/",
	})
	regReq.Header().Set("X-Plugin-Runtime-ID", providerID)
	registry.RegisterService(context.Background(), regReq)

	// Mark provider as UNHEALTHY
	healthReq := connect.NewRequest(&connectpluginv1.ReportHealthRequest{
		State: connectpluginv1.HealthState_HEALTH_STATE_UNHEALTHY,
	})
	healthReq.Header().Set("X-Plugin-Runtime-ID", providerID)
	lifecycle.ReportHealth(context.Background(), healthReq)

	// Register caller
	callerID := generateRuntimeID("caller")
	callerToken := generateToken()
	handshake.mu.Lock()
	handshake.tokens[callerID] = callerToken
	handshake.mu.Unlock()

	req := httptest.NewRequest("POST", "/services/logger/"+providerID+"/Log", nil)
	req.Header.Set("X-Plugin-Runtime-ID", callerID)
	req.Header.Set("Authorization", "Bearer "+callerToken)

	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	if w.Code != http.StatusServiceUnavailable {
		t.Errorf("Expected 503 Service Unavailable, got %d", w.Code)
	}
}

func TestServiceRouter_DegradedProviderStillRoutes(t *testing.T) {
	handshake := NewHandshakeServer(&ServeConfig{})
	lifecycle := NewLifecycleServer()
	registry := NewServiceRegistry(lifecycle)
	router := NewServiceRouter(handshake, registry, lifecycle)

	// Register provider
	providerID := generateRuntimeID("cache-plugin")
	providerToken := generateToken()
	handshake.mu.Lock()
	handshake.tokens[providerID] = providerToken
	handshake.mu.Unlock()

	regReq := connect.NewRequest(&connectpluginv1.RegisterServiceRequest{
		ServiceType:  "cache",
		Version:      "1.0.0",
		EndpointPath: "/cache.v1.Cache/",
	})
	regReq.Header().Set("X-Plugin-Runtime-ID", providerID)
	registry.RegisterService(context.Background(), regReq)

	// Mark provider as DEGRADED
	healthReq := connect.NewRequest(&connectpluginv1.ReportHealthRequest{
		State:  connectpluginv1.HealthState_HEALTH_STATE_DEGRADED,
		Reason: "using in-memory fallback",
	})
	healthReq.Header().Set("X-Plugin-Runtime-ID", providerID)
	lifecycle.ReportHealth(context.Background(), healthReq)

	// Create mock provider server
	providerCalled := false
	providerServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		providerCalled = true
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`{"degraded": true}`))
	}))
	defer providerServer.Close()

	router.RegisterPluginEndpoint(providerID, providerServer.URL)

	// Register caller
	callerID := generateRuntimeID("app")
	callerToken := generateToken()
	handshake.mu.Lock()
	handshake.tokens[callerID] = callerToken
	handshake.mu.Unlock()

	req := httptest.NewRequest("POST", "/services/cache/"+providerID+"/Get", nil)
	req.Header.Set("X-Plugin-Runtime-ID", callerID)
	req.Header.Set("Authorization", "Bearer "+callerToken)

	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	// Should still route to degraded provider
	if w.Code != http.StatusOK {
		t.Errorf("Expected 200 OK for degraded provider, got %d", w.Code)
	}

	if !providerCalled {
		t.Error("Provider should still be called when degraded")
	}
}

func TestServiceRouter_InvalidPath(t *testing.T) {
	handshake := NewHandshakeServer(&ServeConfig{})
	lifecycle := NewLifecycleServer()
	registry := NewServiceRegistry(lifecycle)
	router := NewServiceRouter(handshake, registry, lifecycle)

	tests := []struct {
		name string
		path string
	}{
		{"not services path", "/other/path"},
		{"missing provider id", "/services/logger/"},
		{"missing method", "/services/logger/provider-123"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := httptest.NewRequest("GET", tt.path, nil)
			w := httptest.NewRecorder()
			router.ServeHTTP(w, req)

			if w.Code != http.StatusNotFound && w.Code != http.StatusBadRequest {
				t.Errorf("Expected 400 or 404, got %d for path %s", w.Code, tt.path)
			}
		})
	}
}

func TestServiceRouter_ProxiesHeaders(t *testing.T) {
	handshake := NewHandshakeServer(&ServeConfig{})
	lifecycle := NewLifecycleServer()
	registry := NewServiceRegistry(lifecycle)
	router := NewServiceRouter(handshake, registry, lifecycle)

	// Register provider
	providerID := generateRuntimeID("api")
	providerToken := generateToken()
	handshake.mu.Lock()
	handshake.tokens[providerID] = providerToken
	handshake.mu.Unlock()

	regReq := connect.NewRequest(&connectpluginv1.RegisterServiceRequest{
		ServiceType:  "api",
		Version:      "1.0.0",
		EndpointPath: "/api.v1.API/",
	})
	regReq.Header().Set("X-Plugin-Runtime-ID", providerID)
	registry.RegisterService(context.Background(), regReq)

	healthReq := connect.NewRequest(&connectpluginv1.ReportHealthRequest{
		State: connectpluginv1.HealthState_HEALTH_STATE_HEALTHY,
	})
	healthReq.Header().Set("X-Plugin-Runtime-ID", providerID)
	lifecycle.ReportHealth(context.Background(), healthReq)

	// Create mock provider that checks headers
	receivedHeaders := make(http.Header)
	providerServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		receivedHeaders = r.Header.Clone()
		w.Header().Set("X-Custom-Response", "test-value")
		w.WriteHeader(http.StatusOK)
		w.Write([]byte("ok"))
	}))
	defer providerServer.Close()

	router.RegisterPluginEndpoint(providerID, providerServer.URL)

	// Register caller
	callerID := generateRuntimeID("caller")
	callerToken := generateToken()
	handshake.mu.Lock()
	handshake.tokens[callerID] = callerToken
	handshake.mu.Unlock()

	req := httptest.NewRequest("POST", "/services/api/"+providerID+"/DoSomething", strings.NewReader("body"))
	req.Header.Set("X-Plugin-Runtime-ID", callerID)
	req.Header.Set("Authorization", "Bearer "+callerToken)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Custom-Request", "custom-value")

	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	// Verify request headers forwarded (except auth headers)
	if receivedHeaders.Get("Content-Type") != "application/json" {
		t.Error("Content-Type header not forwarded")
	}
	if receivedHeaders.Get("X-Custom-Request") != "custom-value" {
		t.Error("Custom header not forwarded")
	}
	if receivedHeaders.Get("Authorization") != "" {
		t.Error("Authorization header should not be forwarded to provider")
	}
	if receivedHeaders.Get("X-Plugin-Runtime-ID") != "" {
		t.Error("X-Plugin-Runtime-ID header should not be forwarded to provider")
	}

	// Verify response headers copied
	if w.Header().Get("X-Custom-Response") != "test-value" {
		t.Error("Response headers not copied back to caller")
	}
}

func TestServiceRouter_ProxiesBody(t *testing.T) {
	handshake := NewHandshakeServer(&ServeConfig{})
	lifecycle := NewLifecycleServer()
	registry := NewServiceRegistry(lifecycle)
	router := NewServiceRouter(handshake, registry, lifecycle)

	// Register provider
	providerID := generateRuntimeID("echo")
	token := generateToken()
	handshake.mu.Lock()
	handshake.tokens[providerID] = token
	handshake.mu.Unlock()

	regReq := connect.NewRequest(&connectpluginv1.RegisterServiceRequest{
		ServiceType:  "echo",
		Version:      "1.0.0",
		EndpointPath: "/echo.v1.Echo/",
	})
	regReq.Header().Set("X-Plugin-Runtime-ID", providerID)
	registry.RegisterService(context.Background(), regReq)

	healthReq := connect.NewRequest(&connectpluginv1.ReportHealthRequest{
		State: connectpluginv1.HealthState_HEALTH_STATE_HEALTHY,
	})
	healthReq.Header().Set("X-Plugin-Runtime-ID", providerID)
	lifecycle.ReportHealth(context.Background(), healthReq)

	// Create echo server
	providerServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Echo request body back
		body, _ := io.ReadAll(r.Body)
		w.WriteHeader(http.StatusOK)
		w.Write(body)
	}))
	defer providerServer.Close()

	router.RegisterPluginEndpoint(providerID, providerServer.URL)

	// Register caller
	callerID := generateRuntimeID("caller")
	callerToken := generateToken()
	handshake.mu.Lock()
	handshake.tokens[callerID] = callerToken
	handshake.mu.Unlock()

	testBody := `{"test": "data", "number": 123}`
	req := httptest.NewRequest("POST", "/services/echo/"+providerID+"/Echo", strings.NewReader(testBody))
	req.Header.Set("X-Plugin-Runtime-ID", callerID)
	req.Header.Set("Authorization", "Bearer "+callerToken)

	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	// Verify body echoed back
	responseBody := w.Body.String()
	if responseBody != testBody {
		t.Errorf("Expected body %q, got %q", testBody, responseBody)
	}
}

func TestServiceRouter_ProviderEndpointNotRegistered(t *testing.T) {
	handshake := NewHandshakeServer(&ServeConfig{})
	lifecycle := NewLifecycleServer()
	registry := NewServiceRegistry(lifecycle)
	router := NewServiceRouter(handshake, registry, lifecycle)

	// Register provider in registry but NOT in router endpoints
	providerID := generateRuntimeID("logger")
	providerToken := generateToken()
	handshake.mu.Lock()
	handshake.tokens[providerID] = providerToken
	handshake.mu.Unlock()

	regReq := connect.NewRequest(&connectpluginv1.RegisterServiceRequest{
		ServiceType:  "logger",
		Version:      "1.0.0",
		EndpointPath: "/logger.v1.Logger/",
	})
	regReq.Header().Set("X-Plugin-Runtime-ID", providerID)
	registry.RegisterService(context.Background(), regReq)

	healthReq := connect.NewRequest(&connectpluginv1.ReportHealthRequest{
		State: connectpluginv1.HealthState_HEALTH_STATE_HEALTHY,
	})
	healthReq.Header().Set("X-Plugin-Runtime-ID", providerID)
	lifecycle.ReportHealth(context.Background(), healthReq)

	// Note: NOT calling router.RegisterPluginEndpoint()

	// Register caller
	callerID := generateRuntimeID("caller")
	callerToken := generateToken()
	handshake.mu.Lock()
	handshake.tokens[callerID] = callerToken
	handshake.mu.Unlock()

	req := httptest.NewRequest("POST", "/services/logger/"+providerID+"/Log", nil)
	req.Header.Set("X-Plugin-Runtime-ID", callerID)
	req.Header.Set("Authorization", "Bearer "+callerToken)

	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	if w.Code != http.StatusNotFound {
		t.Errorf("Expected 404 when endpoint not registered, got %d", w.Code)
	}

	if !strings.Contains(w.Body.String(), "endpoint not registered") {
		t.Errorf("Expected error message about endpoint not registered, got: %s", w.Body.String())
	}
}
