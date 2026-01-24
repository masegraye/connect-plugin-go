package connectplugin

import (
	"context"
	"testing"

	"connectrpc.com/connect"
	connectpluginv1 "github.com/masegraye/connect-plugin-go/gen/plugin/v1"
)

func TestLifecycleServer_HealthStateTracking(t *testing.T) {
	server := NewLifecycleServer()

	// Initially no state - should route traffic (backward compat)
	if !server.ShouldRouteTraffic("test-plugin-abc123") {
		t.Error("Expected to route traffic to plugin with no reported state")
	}

	// Report HEALTHY state
	req := connect.NewRequest(&connectpluginv1.ReportHealthRequest{
		State:  connectpluginv1.HealthState_HEALTH_STATE_HEALTHY,
		Reason: "all systems operational",
	})
	req.Header().Set("X-Plugin-Runtime-ID", "test-plugin-abc123")

	_, err := server.ReportHealth(context.Background(), req)
	if err != nil {
		t.Fatalf("ReportHealth failed: %v", err)
	}

	// Should route traffic when HEALTHY
	if !server.ShouldRouteTraffic("test-plugin-abc123") {
		t.Error("Expected to route traffic to HEALTHY plugin")
	}

	// Report DEGRADED state
	req2 := connect.NewRequest(&connectpluginv1.ReportHealthRequest{
		State:                   connectpluginv1.HealthState_HEALTH_STATE_DEGRADED,
		Reason:                  "logger dependency unavailable",
		UnavailableDependencies: []string{"logger"},
	})
	req2.Header().Set("X-Plugin-Runtime-ID", "test-plugin-abc123")

	_, err = server.ReportHealth(context.Background(), req2)
	if err != nil {
		t.Fatalf("ReportHealth failed: %v", err)
	}

	// Should STILL route traffic when DEGRADED
	if !server.ShouldRouteTraffic("test-plugin-abc123") {
		t.Error("Expected to route traffic to DEGRADED plugin")
	}

	// Verify state details
	state := server.GetHealthState("test-plugin-abc123")
	if state == nil {
		t.Fatal("Expected to find health state")
	}
	if state.State != connectpluginv1.HealthState_HEALTH_STATE_DEGRADED {
		t.Errorf("Expected DEGRADED, got %v", state.State)
	}
	if state.Reason != "logger dependency unavailable" {
		t.Errorf("Expected reason to be saved, got %q", state.Reason)
	}
	if len(state.UnavailableDependencies) != 1 || state.UnavailableDependencies[0] != "logger" {
		t.Errorf("Expected unavailable deps to be saved, got %v", state.UnavailableDependencies)
	}

	// Report UNHEALTHY state
	req3 := connect.NewRequest(&connectpluginv1.ReportHealthRequest{
		State:  connectpluginv1.HealthState_HEALTH_STATE_UNHEALTHY,
		Reason: "database connection failed",
	})
	req3.Header().Set("X-Plugin-Runtime-ID", "test-plugin-abc123")

	_, err = server.ReportHealth(context.Background(), req3)
	if err != nil {
		t.Fatalf("ReportHealth failed: %v", err)
	}

	// Should NOT route traffic when UNHEALTHY
	if server.ShouldRouteTraffic("test-plugin-abc123") {
		t.Error("Expected NOT to route traffic to UNHEALTHY plugin")
	}
}

func TestLifecycleServer_MultiplePlugins(t *testing.T) {
	server := NewLifecycleServer()

	// Plugin A is healthy
	reqA := connect.NewRequest(&connectpluginv1.ReportHealthRequest{
		State: connectpluginv1.HealthState_HEALTH_STATE_HEALTHY,
	})
	reqA.Header().Set("X-Plugin-Runtime-ID", "plugin-a-xyz")
	server.ReportHealth(context.Background(), reqA)

	// Plugin B is unhealthy
	reqB := connect.NewRequest(&connectpluginv1.ReportHealthRequest{
		State: connectpluginv1.HealthState_HEALTH_STATE_UNHEALTHY,
	})
	reqB.Header().Set("X-Plugin-Runtime-ID", "plugin-b-123")
	server.ReportHealth(context.Background(), reqB)

	// Should route to A, not to B
	if !server.ShouldRouteTraffic("plugin-a-xyz") {
		t.Error("Expected to route to healthy plugin A")
	}
	if server.ShouldRouteTraffic("plugin-b-123") {
		t.Error("Expected NOT to route to unhealthy plugin B")
	}
}

func TestLifecycleServer_MissingRuntimeID(t *testing.T) {
	server := NewLifecycleServer()

	// Request without runtime ID should fail
	req := connect.NewRequest(&connectpluginv1.ReportHealthRequest{
		State: connectpluginv1.HealthState_HEALTH_STATE_HEALTHY,
	})
	// No X-Plugin-Runtime-ID header

	_, err := server.ReportHealth(context.Background(), req)
	if err == nil {
		t.Error("Expected error when X-Plugin-Runtime-ID header missing")
	}
}
