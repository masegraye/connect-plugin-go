package connectplugin

import (
	"context"
	"testing"
)

func TestStaticDiscovery_SingleEndpoint(t *testing.T) {
	discovery := NewStaticDiscovery(map[string][]Endpoint{
		"plugin-host": {
			{URL: "http://localhost:8080", Weight: 100},
		},
	})

	endpoints, err := discovery.Discover(context.Background(), "plugin-host")
	if err != nil {
		t.Fatalf("Discover() error = %v", err)
	}

	if len(endpoints) != 1 {
		t.Fatalf("Expected 1 endpoint, got %d", len(endpoints))
	}

	if endpoints[0].URL != "http://localhost:8080" {
		t.Errorf("Expected URL http://localhost:8080, got %s", endpoints[0].URL)
	}

	if endpoints[0].Weight != 100 {
		t.Errorf("Expected weight 100, got %d", endpoints[0].Weight)
	}
}

func TestStaticDiscovery_MultipleEndpoints(t *testing.T) {
	discovery := NewStaticDiscovery(map[string][]Endpoint{
		"plugin-cluster": {
			{URL: "http://plugin-1:8080", Weight: 50},
			{URL: "http://plugin-2:8080", Weight: 30},
			{URL: "http://plugin-3:8080", Weight: 20},
		},
	})

	endpoints, err := discovery.Discover(context.Background(), "plugin-cluster")
	if err != nil {
		t.Fatalf("Discover() error = %v", err)
	}

	if len(endpoints) != 3 {
		t.Fatalf("Expected 3 endpoints, got %d", len(endpoints))
	}

	// Verify endpoints
	expectedURLs := []string{
		"http://plugin-1:8080",
		"http://plugin-2:8080",
		"http://plugin-3:8080",
	}

	for i, ep := range endpoints {
		if ep.URL != expectedURLs[i] {
			t.Errorf("Endpoint %d: expected URL %s, got %s", i, expectedURLs[i], ep.URL)
		}
	}
}

func TestStaticDiscovery_EndpointMetadata(t *testing.T) {
	discovery := NewStaticDiscovery(map[string][]Endpoint{
		"api-service": {
			{
				URL:    "http://api-us-west:8080",
				Weight: 100,
				Metadata: map[string]string{
					"region": "us-west-2",
					"zone":   "us-west-2a",
					"env":    "production",
				},
			},
		},
	})

	endpoints, err := discovery.Discover(context.Background(), "api-service")
	if err != nil {
		t.Fatalf("Discover() error = %v", err)
	}

	if len(endpoints) != 1 {
		t.Fatalf("Expected 1 endpoint, got %d", len(endpoints))
	}

	ep := endpoints[0]
	if ep.Metadata["region"] != "us-west-2" {
		t.Errorf("Expected region us-west-2, got %s", ep.Metadata["region"])
	}

	if ep.Metadata["zone"] != "us-west-2a" {
		t.Errorf("Expected zone us-west-2a, got %s", ep.Metadata["zone"])
	}

	if ep.Metadata["env"] != "production" {
		t.Errorf("Expected env production, got %s", ep.Metadata["env"])
	}
}

func TestStaticDiscovery_ServiceNotFound(t *testing.T) {
	discovery := NewStaticDiscovery(map[string][]Endpoint{
		"existing-service": {
			{URL: "http://localhost:8080", Weight: 100},
		},
	})

	_, err := discovery.Discover(context.Background(), "nonexistent-service")
	if err == nil {
		t.Error("Expected error for nonexistent service")
	}

	expectedError := `service "nonexistent-service" not found in static discovery`
	if err.Error() != expectedError {
		t.Errorf("Expected error %q, got %q", expectedError, err.Error())
	}
}

func TestStaticDiscovery_NoEndpointsConfigured(t *testing.T) {
	discovery := NewStaticDiscovery(map[string][]Endpoint{
		"empty-service": {},
	})

	_, err := discovery.Discover(context.Background(), "empty-service")
	if err == nil {
		t.Error("Expected error for service with no endpoints")
	}
}

func TestStaticDiscovery_Watch(t *testing.T) {
	discovery := NewStaticDiscovery(map[string][]Endpoint{
		"plugin-host": {
			{URL: "http://localhost:8080", Weight: 100},
		},
	})

	ctx := context.Background()
	ch, err := discovery.Watch(ctx, "plugin-host")
	if err != nil {
		t.Fatalf("Watch() error = %v", err)
	}

	// Should receive one event with current endpoints
	event, ok := <-ch
	if !ok {
		t.Fatal("Channel closed without sending event")
	}

	if event.ServiceName != "plugin-host" {
		t.Errorf("Expected service name plugin-host, got %s", event.ServiceName)
	}

	if event.Error != nil {
		t.Errorf("Unexpected error in event: %v", event.Error)
	}

	if len(event.Endpoints) != 1 {
		t.Fatalf("Expected 1 endpoint in event, got %d", len(event.Endpoints))
	}

	// Channel should be closed (static discovery doesn't send updates)
	_, ok = <-ch
	if ok {
		t.Error("Expected channel to be closed after initial event")
	}
}

func TestStaticDiscovery_WatchServiceNotFound(t *testing.T) {
	discovery := NewStaticDiscovery(map[string][]Endpoint{})

	ctx := context.Background()
	ch, err := discovery.Watch(ctx, "nonexistent")
	if err != nil {
		t.Fatalf("Watch() should not error, got %v", err)
	}

	// Should receive error event
	event, ok := <-ch
	if !ok {
		t.Fatal("Channel closed without sending error event")
	}

	if event.Error == nil {
		t.Error("Expected error event for nonexistent service")
	}

	// Channel should be closed
	_, ok = <-ch
	if ok {
		t.Error("Expected channel to be closed after error event")
	}
}

func TestStaticDiscovery_AddEndpoint(t *testing.T) {
	discovery := NewStaticDiscovery(map[string][]Endpoint{
		"api": {
			{URL: "http://api-1:8080", Weight: 100},
		},
	})

	// Add second endpoint
	discovery.AddEndpoint("api", Endpoint{
		URL:    "http://api-2:8080",
		Weight: 50,
	})

	endpoints, err := discovery.Discover(context.Background(), "api")
	if err != nil {
		t.Fatalf("Discover() error = %v", err)
	}

	if len(endpoints) != 2 {
		t.Fatalf("Expected 2 endpoints after AddEndpoint, got %d", len(endpoints))
	}
}

func TestStaticDiscovery_RemoveService(t *testing.T) {
	discovery := NewStaticDiscovery(map[string][]Endpoint{
		"service-a": {{URL: "http://a:8080", Weight: 100}},
		"service-b": {{URL: "http://b:8080", Weight: 100}},
	})

	// Remove service-a
	discovery.RemoveService("service-a")

	// service-a should not be found
	_, err := discovery.Discover(context.Background(), "service-a")
	if err == nil {
		t.Error("Expected error after RemoveService")
	}

	// service-b should still exist
	endpoints, err := discovery.Discover(context.Background(), "service-b")
	if err != nil {
		t.Fatalf("service-b should still exist, got error: %v", err)
	}

	if len(endpoints) != 1 {
		t.Errorf("Expected 1 endpoint for service-b, got %d", len(endpoints))
	}
}

func TestStaticDiscovery_IsolatedCopy(t *testing.T) {
	discovery := NewStaticDiscovery(map[string][]Endpoint{
		"test": {
			{URL: "http://test:8080", Weight: 100},
		},
	})

	endpoints1, _ := discovery.Discover(context.Background(), "test")
	endpoints2, _ := discovery.Discover(context.Background(), "test")

	// Modify first result
	endpoints1[0].URL = "modified"

	// Second result should be unaffected (copy, not reference)
	if endpoints2[0].URL == "modified" {
		t.Error("Discover() should return copy, not reference")
	}

	if endpoints2[0].URL != "http://test:8080" {
		t.Errorf("Expected original URL, got %s", endpoints2[0].URL)
	}
}
