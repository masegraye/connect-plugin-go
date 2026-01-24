package connectplugin

import (
	"context"
	"testing"
	"time"

	"connectrpc.com/connect"
	connectpluginv1 "github.com/masegraye/connect-plugin-go/gen/plugin/v1"
	"github.com/masegraye/connect-plugin-go/internal/depgraph"
)

func TestPlatform_AddPlugin(t *testing.T) {
	handshake := NewHandshakeServer(&ServeConfig{})
	lifecycle := NewLifecycleServer()
	registry := NewServiceRegistry(lifecycle)
	router := NewServiceRouter(handshake, registry, lifecycle)
	platform := NewPlatform(registry, lifecycle, router)

	// Add a logger plugin
	config := PluginConfig{
		SelfID:      "logger-plugin",
		SelfVersion: "1.0.0",
		Endpoint:    "http://localhost:8081",
		Metadata: PluginMetadata{
			Name:    "logger",
			Version: "1.0.0",
			Provides: []ServiceDeclaration{
				{Type: "logger", Version: "1.0.0", Path: "/logger.v1.Logger/"},
			},
		},
	}

	// Note: This will timeout waiting for health because we don't have a real plugin
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()

	err := platform.AddPlugin(ctx, config)

	// Should timeout because no real plugin is reporting health
	if err == nil {
		t.Error("Expected error (timeout waiting for health)")
	}
}

func TestPlatform_AddPlugin_MissingDependency(t *testing.T) {
	handshake := NewHandshakeServer(&ServeConfig{})
	lifecycle := NewLifecycleServer()
	registry := NewServiceRegistry(lifecycle)
	router := NewServiceRouter(handshake, registry, lifecycle)
	platform := NewPlatform(registry, lifecycle, router)

	// Try to add cache plugin that requires logger (not available)
	config := PluginConfig{
		SelfID:      "cache-plugin",
		SelfVersion: "1.0.0",
		Endpoint:    "http://localhost:8082",
		Metadata: PluginMetadata{
			Name:    "cache",
			Version: "1.0.0",
			Provides: []ServiceDeclaration{
				{Type: "cache", Version: "1.0.0", Path: "/cache.v1.Cache/"},
			},
			Requires: []ServiceDependency{
				{Type: "logger", MinVersion: "1.0.0", RequiredForStartup: true},
			},
		},
	}

	err := platform.AddPlugin(context.Background(), config)

	// Should fail immediately (dependency not available)
	if err == nil {
		t.Error("Expected error for missing dependency")
	}

	if err != nil && err.Error() != `required service "logger" not available for plugin "cache-plugin"` {
		t.Logf("Got error: %v", err)
	}
}

func TestPlatform_GetStartupOrder(t *testing.T) {
	handshake := NewHandshakeServer(&ServeConfig{})
	lifecycle := NewLifecycleServer()
	registry := NewServiceRegistry(lifecycle)
	router := NewServiceRouter(handshake, registry, lifecycle)
	platform := NewPlatform(registry, lifecycle, router)

	// Manually add nodes to dependency graph (simulating plugins already added)
	platform.depGraph.Add(&depgraph.Node{
		RuntimeID: "logger-abc",
		SelfID:    "logger",
		Provides:  []depgraph.ServiceDeclaration{{Type: "logger", Version: "1.0.0"}},
	})

	platform.depGraph.Add(&depgraph.Node{
		RuntimeID: "cache-def",
		SelfID:    "cache",
		Provides:  []depgraph.ServiceDeclaration{{Type: "cache", Version: "1.0.0"}},
		Requires: []depgraph.ServiceDependency{
			{Type: "logger", RequiredForStartup: true},
		},
	})

	platform.depGraph.Add(&depgraph.Node{
		RuntimeID: "app-ghi",
		SelfID:    "app",
		Requires: []depgraph.ServiceDependency{
			{Type: "cache", RequiredForStartup: true},
		},
	})

	order, err := platform.GetStartupOrder()
	if err != nil {
		t.Fatalf("GetStartupOrder failed: %v", err)
	}

	if len(order) != 3 {
		t.Fatalf("Expected 3 plugins, got %d", len(order))
	}

	// Verify order: logger → cache → app
	if order[0] != "logger-abc" {
		t.Errorf("Expected logger first, got %s", order[0])
	}
	if order[1] != "cache-def" {
		t.Errorf("Expected cache second, got %s", order[1])
	}
	if order[2] != "app-ghi" {
		t.Errorf("Expected app last, got %s", order[2])
	}
}

func TestPlatform_GetImpact(t *testing.T) {
	handshake := NewHandshakeServer(&ServeConfig{})
	lifecycle := NewLifecycleServer()
	registry := NewServiceRegistry(lifecycle)
	router := NewServiceRouter(handshake, registry, lifecycle)
	platform := NewPlatform(registry, lifecycle, router)

	// Add plugins to graph
	platform.depGraph.Add(&depgraph.Node{
		RuntimeID: "logger-abc",
		Provides:  []depgraph.ServiceDeclaration{{Type: "logger", Version: "1.0.0"}},
	})

	platform.depGraph.Add(&depgraph.Node{
		RuntimeID: "cache-def",
		Requires: []depgraph.ServiceDependency{
			{Type: "logger", RequiredForStartup: true},
		},
	})

	// Get impact of removing logger
	impact := platform.GetImpact("logger-abc")

	if impact.TargetPlugin != "logger-abc" {
		t.Errorf("Expected target logger-abc, got %s", impact.TargetPlugin)
	}

	if len(impact.AffectedPlugins) != 1 || impact.AffectedPlugins[0] != "cache-def" {
		t.Errorf("Expected affected [cache-def], got %v", impact.AffectedPlugins)
	}

	if len(impact.AffectedServices) != 1 || impact.AffectedServices[0] != "logger" {
		t.Errorf("Expected affected services [logger], got %v", impact.AffectedServices)
	}
}

func TestPlatform_RemovePlugin(t *testing.T) {
	handshake := NewHandshakeServer(&ServeConfig{})
	lifecycle := NewLifecycleServer()
	registry := NewServiceRegistry(lifecycle)
	router := NewServiceRouter(handshake, registry, lifecycle)
	platform := NewPlatform(registry, lifecycle, router)

	// Simulate a plugin being added and registered
	runtimeID := "logger-xyz"

	// Add to graph
	platform.depGraph.Add(&depgraph.Node{
		RuntimeID: runtimeID,
		SelfID:    "logger",
		Provides:  []depgraph.ServiceDeclaration{{Type: "logger", Version: "1.0.0"}},
	})

	// Add to plugins map
	platform.plugins[runtimeID] = &PluginInstance{
		RuntimeID: runtimeID,
		SelfID:    "logger",
		Endpoint:  "http://localhost:8081",
	}

	// Register service
	regReq := connect.NewRequest(&connectpluginv1.RegisterServiceRequest{
		ServiceType:  "logger",
		Version:      "1.0.0",
		EndpointPath: "/logger.v1.Logger/",
	})
	regReq.Header().Set("X-Plugin-Runtime-ID", runtimeID)
	registry.RegisterService(context.Background(), regReq)

	// Verify service is registered
	if !registry.HasService("logger", "1.0.0") {
		t.Fatal("Expected logger service to be registered")
	}

	// Remove plugin
	ctx := context.Background()
	err := platform.RemovePlugin(ctx, runtimeID)
	if err != nil {
		t.Fatalf("RemovePlugin failed: %v", err)
	}

	// Verify service is unregistered
	if registry.HasService("logger", "1.0.0") {
		t.Error("Expected logger service to be unregistered")
	}

	// Verify removed from graph
	if platform.depGraph.GetNode(runtimeID) != nil {
		t.Error("Expected node to be removed from graph")
	}

	// Verify removed from plugins map
	if _, ok := platform.plugins[runtimeID]; ok {
		t.Error("Expected plugin to be removed from map")
	}
}

func TestPlatform_RemovePlugin_NotFound(t *testing.T) {
	handshake := NewHandshakeServer(&ServeConfig{})
	lifecycle := NewLifecycleServer()
	registry := NewServiceRegistry(lifecycle)
	router := NewServiceRouter(handshake, registry, lifecycle)
	platform := NewPlatform(registry, lifecycle, router)

	err := platform.RemovePlugin(context.Background(), "nonexistent")
	if err == nil {
		t.Error("Expected error for nonexistent plugin")
	}
}

func TestPlatform_ReplacePlugin_CreatesNewInstance(t *testing.T) {
	handshake := NewHandshakeServer(&ServeConfig{})
	lifecycle := NewLifecycleServer()
	registry := NewServiceRegistry(lifecycle)
	router := NewServiceRouter(handshake, registry, lifecycle)
	platform := NewPlatform(registry, lifecycle, router)

	// Add old version
	oldRuntimeID := "logger-old-xyz"
	platform.depGraph.Add(&depgraph.Node{
		RuntimeID: oldRuntimeID,
		SelfID:    "logger",
		Provides:  []depgraph.ServiceDeclaration{{Type: "logger", Version: "1.0.0"}},
	})

	platform.plugins[oldRuntimeID] = &PluginInstance{
		RuntimeID: oldRuntimeID,
		SelfID:    "logger",
		Endpoint:  "http://localhost:8081",
	}

	// Report old version as healthy
	healthReq := connect.NewRequest(&connectpluginv1.ReportHealthRequest{
		State: connectpluginv1.HealthState_HEALTH_STATE_HEALTHY,
	})
	healthReq.Header().Set("X-Plugin-Runtime-ID", oldRuntimeID)
	lifecycle.ReportHealth(context.Background(), healthReq)

	// Replace with new version
	newConfig := PluginConfig{
		SelfID:      "logger",
		SelfVersion: "2.0.0",
		Endpoint:    "http://localhost:8082",
		Metadata: PluginMetadata{
			Name:    "logger",
			Version: "2.0.0",
			Provides: []ServiceDeclaration{
				{Type: "logger", Version: "2.0.0", Path: "/logger.v2.Logger/"},
			},
		},
	}

	// This will timeout waiting for new version health
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()

	err := platform.ReplacePlugin(ctx, oldRuntimeID, newConfig)

	// Should timeout (no real plugin reporting health)
	if err == nil {
		t.Error("Expected timeout error")
	}

	// Note: In real usage, the new plugin would report health and replace would succeed
}
