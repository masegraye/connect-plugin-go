package connectplugin

import (
	"context"
	"testing"

	"connectrpc.com/connect"
	connectpluginv1 "github.com/masegraye/connect-plugin-go/gen/plugin/v1"
)

func TestServiceRegistry_RegisterAndUnregister(t *testing.T) {
	registry := NewServiceRegistry(nil)

	// Register logger service
	req := connect.NewRequest(&connectpluginv1.RegisterServiceRequest{
		ServiceType:  "logger",
		Version:      "1.0.0",
		EndpointPath: "/logger.v1.Logger/",
		Metadata:     map[string]string{"provider": "logger-a"},
	})
	req.Header().Set("X-Plugin-Runtime-ID", "logger-a-x7k2")

	resp, err := registry.RegisterService(context.Background(), req)
	if err != nil {
		t.Fatalf("RegisterService failed: %v", err)
	}

	registrationID := resp.Msg.RegistrationId
	if registrationID == "" {
		t.Fatal("Expected registration ID")
	}

	// Should have service available
	if !registry.HasService("logger", "1.0.0") {
		t.Error("Expected logger service to be available")
	}

	// Unregister
	unreq := connect.NewRequest(&connectpluginv1.UnregisterServiceRequest{
		RegistrationId: registrationID,
	})

	_, err = registry.UnregisterService(context.Background(), unreq)
	if err != nil {
		t.Fatalf("UnregisterService failed: %v", err)
	}

	// Should no longer be available
	if registry.HasService("logger", "1.0.0") {
		t.Error("Expected logger service to be unavailable after unregister")
	}
}

func TestServiceRegistry_MultiProvider(t *testing.T) {
	registry := NewServiceRegistry(nil)

	// Register logger-a
	reqA := connect.NewRequest(&connectpluginv1.RegisterServiceRequest{
		ServiceType:  "logger",
		Version:      "1.0.0",
		EndpointPath: "/logger.v1.Logger/",
	})
	reqA.Header().Set("X-Plugin-Runtime-ID", "logger-a-x7k2")
	registry.RegisterService(context.Background(), reqA)

	// Register logger-b
	reqB := connect.NewRequest(&connectpluginv1.RegisterServiceRequest{
		ServiceType:  "logger",
		Version:      "1.0.0",
		EndpointPath: "/logger.v1.Logger/",
	})
	reqB.Header().Set("X-Plugin-Runtime-ID", "logger-b-y8m3")
	registry.RegisterService(context.Background(), reqB)

	// Both should be registered
	provider, err := registry.SelectProvider("logger", "1.0.0")
	if err != nil {
		t.Fatalf("Expected to find provider: %v", err)
	}
	if provider == nil {
		t.Fatal("Expected provider")
	}

	// Should have 2 providers
	registry.mu.RLock()
	providers := registry.providers["logger"]
	registry.mu.RUnlock()

	if len(providers) != 2 {
		t.Errorf("Expected 2 logger providers, got %d", len(providers))
	}
}

func TestServiceRegistry_SelectionStrategies(t *testing.T) {
	registry := NewServiceRegistry(nil)

	// Register 3 providers
	for i, runtimeID := range []string{"provider-1", "provider-2", "provider-3"} {
		req := connect.NewRequest(&connectpluginv1.RegisterServiceRequest{
			ServiceType:  "cache",
			Version:      "1.0.0",
			EndpointPath: "/cache.v1.Cache/",
			Metadata:     map[string]string{"index": string(rune('0' + i))},
		})
		req.Header().Set("X-Plugin-Runtime-ID", runtimeID)
		registry.RegisterService(context.Background(), req)
	}

	// Test First strategy
	registry.SetSelectionStrategy("cache", SelectionFirst)
	p1, _ := registry.SelectProvider("cache", "1.0.0")
	p2, _ := registry.SelectProvider("cache", "1.0.0")
	if p1.RuntimeID != p2.RuntimeID {
		t.Error("SelectionFirst should return same provider")
	}

	// Test RoundRobin strategy
	registry.SetSelectionStrategy("cache", SelectionRoundRobin)
	seen := make(map[string]bool)
	for i := 0; i < 5; i++ {
		p, _ := registry.SelectProvider("cache", "1.0.0")
		seen[p.RuntimeID] = true
	}
	if len(seen) < 3 {
		t.Errorf("RoundRobin should cycle through all providers, saw %d", len(seen))
	}

	// Test Random strategy
	registry.SetSelectionStrategy("cache", SelectionRandom)
	p, _ := registry.SelectProvider("cache", "1.0.0")
	if p == nil {
		t.Error("Random should return a provider")
	}
}

func TestServiceRegistry_VersionFiltering(t *testing.T) {
	registry := NewServiceRegistry(nil)

	// Register v1.0.0
	req1 := connect.NewRequest(&connectpluginv1.RegisterServiceRequest{
		ServiceType:  "api",
		Version:      "1.0.0",
		EndpointPath: "/api.v1.API/",
	})
	req1.Header().Set("X-Plugin-Runtime-ID", "api-v1")
	registry.RegisterService(context.Background(), req1)

	// Register v2.0.0
	req2 := connect.NewRequest(&connectpluginv1.RegisterServiceRequest{
		ServiceType:  "api",
		Version:      "2.0.0",
		EndpointPath: "/api.v2.API/",
	})
	req2.Header().Set("X-Plugin-Runtime-ID", "api-v2")
	registry.RegisterService(context.Background(), req2)

	// Request minVersion 1.0.0 - should find both, return first (v1)
	p, err := registry.SelectProvider("api", "1.0.0")
	if err != nil {
		t.Fatalf("Expected to find provider: %v", err)
	}
	// Simple string comparison means "2.0.0" >= "1.0.0"
	if p.Version != "1.0.0" && p.Version != "2.0.0" {
		t.Errorf("Expected version 1.0.0 or 2.0.0, got %s", p.Version)
	}

	// Request minVersion 2.0.0 - should only find v2
	p2, err := registry.SelectProvider("api", "2.0.0")
	if err != nil {
		t.Fatalf("Expected to find v2 provider: %v", err)
	}
	if p2.Version != "2.0.0" {
		t.Errorf("Expected version 2.0.0, got %s", p2.Version)
	}

	// Request minVersion 3.0.0 - should not find any
	_, err = registry.SelectProvider("api", "3.0.0")
	if err == nil {
		t.Error("Expected error when no compatible version")
	}
}

func TestServiceRegistry_HealthFiltering(t *testing.T) {
	lifecycle := NewLifecycleServer()
	registry := NewServiceRegistry(lifecycle)

	// Register 2 providers
	req1 := connect.NewRequest(&connectpluginv1.RegisterServiceRequest{
		ServiceType:  "db",
		Version:      "1.0.0",
		EndpointPath: "/db.v1.DB/",
	})
	req1.Header().Set("X-Plugin-Runtime-ID", "db-healthy")
	registry.RegisterService(context.Background(), req1)

	req2 := connect.NewRequest(&connectpluginv1.RegisterServiceRequest{
		ServiceType:  "db",
		Version:      "1.0.0",
		EndpointPath: "/db.v1.DB/",
	})
	req2.Header().Set("X-Plugin-Runtime-ID", "db-unhealthy")
	registry.RegisterService(context.Background(), req2)

	// Mark db-healthy as HEALTHY
	healthReq1 := connect.NewRequest(&connectpluginv1.ReportHealthRequest{
		State: connectpluginv1.HealthState_HEALTH_STATE_HEALTHY,
	})
	healthReq1.Header().Set("X-Plugin-Runtime-ID", "db-healthy")
	lifecycle.ReportHealth(context.Background(), healthReq1)

	// Mark db-unhealthy as UNHEALTHY
	healthReq2 := connect.NewRequest(&connectpluginv1.ReportHealthRequest{
		State: connectpluginv1.HealthState_HEALTH_STATE_UNHEALTHY,
	})
	healthReq2.Header().Set("X-Plugin-Runtime-ID", "db-unhealthy")
	lifecycle.ReportHealth(context.Background(), healthReq2)

	// SelectProvider should only return healthy one
	p, err := registry.SelectProvider("db", "1.0.0")
	if err != nil {
		t.Fatalf("Expected to find healthy provider: %v", err)
	}
	if p.RuntimeID != "db-healthy" {
		t.Errorf("Expected db-healthy, got %s", p.RuntimeID)
	}
}

func TestServiceRegistry_UnregisterPluginServices(t *testing.T) {
	registry := NewServiceRegistry(nil)

	// Register 2 services from same plugin
	req1 := connect.NewRequest(&connectpluginv1.RegisterServiceRequest{
		ServiceType:  "logger",
		Version:      "1.0.0",
		EndpointPath: "/logger.v1.Logger/",
	})
	req1.Header().Set("X-Plugin-Runtime-ID", "multi-plugin-abc")
	registry.RegisterService(context.Background(), req1)

	req2 := connect.NewRequest(&connectpluginv1.RegisterServiceRequest{
		ServiceType:  "metrics",
		Version:      "1.0.0",
		EndpointPath: "/metrics.v1.Metrics/",
	})
	req2.Header().Set("X-Plugin-Runtime-ID", "multi-plugin-abc")
	registry.RegisterService(context.Background(), req2)

	// Both should be available
	if !registry.HasService("logger", "1.0.0") {
		t.Error("Expected logger to be available")
	}
	if !registry.HasService("metrics", "1.0.0") {
		t.Error("Expected metrics to be available")
	}

	// Unregister all services from this plugin
	registry.UnregisterPluginServices("multi-plugin-abc")

	// Both should be unavailable
	if registry.HasService("logger", "1.0.0") {
		t.Error("Expected logger to be unavailable")
	}
	if registry.HasService("metrics", "1.0.0") {
		t.Error("Expected metrics to be unavailable")
	}
}

func TestServiceRegistry_MissingRuntimeID(t *testing.T) {
	registry := NewServiceRegistry(nil)

	// Request without runtime ID should fail
	req := connect.NewRequest(&connectpluginv1.RegisterServiceRequest{
		ServiceType:  "logger",
		Version:      "1.0.0",
		EndpointPath: "/logger.v1.Logger/",
	})
	// No X-Plugin-Runtime-ID header

	_, err := registry.RegisterService(context.Background(), req)
	if err == nil {
		t.Error("Expected error when X-Plugin-Runtime-ID header missing")
	}
}
