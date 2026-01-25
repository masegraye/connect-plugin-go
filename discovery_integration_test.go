package connectplugin_test

import (
	"testing"

	connectplugin "github.com/masegraye/connect-plugin-go"
)

func TestClient_WithStaticDiscovery(t *testing.T) {
	// Create static discovery with plugin-host endpoint
	discovery := connectplugin.NewStaticDiscovery(map[string][]connectplugin.Endpoint{
		"plugin-host": {
			{URL: "http://localhost:8080", Weight: 100},
		},
	})

	// Create client with discovery (no hardcoded endpoint)
	client, err := connectplugin.NewClient(connectplugin.ClientConfig{
		Discovery:            discovery,
		DiscoveryServiceName: "plugin-host",
		SelfID:               "test-client",
		SelfVersion:          "1.0.0",
	})
	if err != nil {
		t.Fatalf("NewClient() error = %v", err)
	}

	// Note: Connect would fail because no actual host is running
	// This test just verifies the configuration is valid
	if client == nil {
		t.Fatal("Expected client to be created with discovery")
	}
}

func TestClient_WithStaticDiscovery_MultipleEndpoints(t *testing.T) {
	// Multiple plugin-host endpoints (load balancing)
	discovery := connectplugin.NewStaticDiscovery(map[string][]connectplugin.Endpoint{
		"plugin-host": {
			{URL: "http://host-1:8080", Weight: 50, Metadata: map[string]string{"region": "us-west"}},
			{URL: "http://host-2:8080", Weight: 30, Metadata: map[string]string{"region": "us-east"}},
			{URL: "http://host-3:8080", Weight: 20, Metadata: map[string]string{"region": "eu-west"}},
		},
	})

	client, err := connectplugin.NewClient(connectplugin.ClientConfig{
		Discovery: discovery,
		SelfID:    "test-client",
	})
	if err != nil {
		t.Fatalf("NewClient() error = %v", err)
	}

	// Verify client uses discovery
	cfg := client.Config()
	if cfg.Discovery == nil {
		t.Error("Expected discovery to be set in client config")
	}
}

func TestClient_DiscoveryFallbackToEndpoint(t *testing.T) {
	// Client with both Discovery and Endpoint
	// Endpoint should be used if discovery is not needed
	discovery := connectplugin.NewStaticDiscovery(map[string][]connectplugin.Endpoint{
		"plugin-host": {
			{URL: "http://discovered:8080", Weight: 100},
		},
	})

	client, err := connectplugin.NewClient(connectplugin.ClientConfig{
		Endpoint:  "http://hardcoded:8080", // Explicit endpoint
		Discovery: discovery,                // Discovery available but not used
		SelfID:    "test-client",
	})
	if err != nil {
		t.Fatalf("NewClient() error = %v", err)
	}

	// When Endpoint is explicitly set, it takes precedence over discovery
	cfg := client.Config()
	if cfg.Endpoint != "http://hardcoded:8080" {
		t.Errorf("Expected hardcoded endpoint to take precedence, got %s", cfg.Endpoint)
	}
}
