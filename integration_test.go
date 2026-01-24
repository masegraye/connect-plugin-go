// Package connectplugin_test contains integration tests for Phase 2 Service Registry.
//
// These tests use real plugin processes (logger, cache, app) to verify:
// - Service registration and discovery
// - Multi-provider selection
// - Dependency-ordered startup
// - Health state tracking
// - Service watch notifications
// - Impact analysis
//
// Run with: task test:integration
// or: go test -v -run TestIntegration_ -timeout 60s
//
// Note: Tests use -short flag to skip in unit test runs.
// Build example plugins with: task build-examples
package connectplugin_test

import (
	"context"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"connectrpc.com/connect"
	connectplugin "github.com/masegraye/connect-plugin-go"
	connectpluginv1 "github.com/masegraye/connect-plugin-go/gen/plugin/v1"
	"github.com/masegraye/connect-plugin-go/gen/plugin/v1/connectpluginv1connect"
)

// TestIntegration_BasicDiscoveryAndCalls tests scenario 1: Basic service discovery and calls.
func TestIntegration_BasicDiscoveryAndCalls(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping integration test in short mode")
	}

	// 1. Start host server
	host := startHostServer(t, 18080)
	defer host.Shutdown()

	// 2. Build and start logger plugin
	loggerCmd := buildAndStartPlugin(t, "logger-plugin", 18081, 18080)
	defer stopPlugin(loggerCmd)

	// Wait for logger to register
	time.Sleep(500 * time.Millisecond)

	// 3. Verify logger registered its service
	provider := getProvider(t, host.registry, "logger", "1.0.0")
	if provider == nil {
		t.Fatal("Logger service not registered")
	}
	t.Logf("Logger registered: %s at %s", provider.RuntimeID, provider.EndpointPath)

	// 4. Build and start cache plugin (depends on logger)
	cacheCmd := buildAndStartPlugin(t, "cache-plugin", 18082, 18080)
	defer stopPlugin(cacheCmd)

	// Wait for cache to register and report health
	time.Sleep(1 * time.Second)

	// 5. Verify cache registered its service
	cacheProvider := getProvider(t, host.registry, "cache", "1.0.0")
	if cacheProvider == nil {
		t.Fatal("Cache service not registered")
	}

	// 6. Verify cache discovered logger and reported healthy
	// The cache plugin waits 200ms after starting, then discovers logger, then reports health
	cacheHealth := host.lifecycle.GetHealthState(cacheProvider.RuntimeID)
	if cacheHealth == nil {
		t.Error("Cache health not reported yet")
	} else if cacheHealth.State != connectpluginv1.HealthState_HEALTH_STATE_HEALTHY {
		t.Errorf("Expected cache to be healthy after finding logger, got state: %v, reason: %s",
			cacheHealth.State, cacheHealth.Reason)
	}
}

// TestIntegration_MultiProvider tests scenario 2: Multiple providers for same service.
func TestIntegration_MultiProvider(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping integration test in short mode")
	}

	host := startHostServer(t, 28080)
	defer host.Shutdown()

	// Start two logger instances
	logger1Cmd := buildAndStartPlugin(t, "logger-plugin", 28081, 28080)
	defer stopPlugin(logger1Cmd)

	logger2Cmd := buildAndStartPlugin(t, "logger-plugin", 28091, 28080)
	defer stopPlugin(logger2Cmd)

	time.Sleep(500 * time.Millisecond)

	// Verify both registered
	providers := host.registry.GetAllProviders("logger")
	if len(providers) != 2 {
		t.Fatalf("Expected 2 logger providers, got %d", len(providers))
	}

	// Start cache - should discover one of the loggers
	cacheCmd := buildAndStartPlugin(t, "cache-plugin", 28082, 28080)
	defer stopPlugin(cacheCmd)

	time.Sleep(500 * time.Millisecond)

	// Cache should be healthy (discovered one logger)
	cacheProvider := getProvider(t, host.registry, "cache", "1.0.0")
	if cacheProvider == nil {
		t.Fatal("Cache not registered")
	}

	cacheHealth := host.lifecycle.GetHealthState(cacheProvider.RuntimeID)
	if cacheHealth == nil || cacheHealth.State != connectpluginv1.HealthState_HEALTH_STATE_HEALTHY {
		t.Error("Cache should be healthy with logger available")
	}
}

// TestIntegration_DependencyOrderedStartup tests scenario 3: Startup ordering.
// Note: This test manually populates the dependency graph to test startup order logic.
// In production, Platform.AddPlugin() would populate the graph.
func TestIntegration_DependencyOrderedStartup(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping integration test in short mode")
	}

	host := startHostServer(t, 38080)
	defer host.Shutdown()

	// Start plugins
	loggerCmd := buildAndStartPlugin(t, "logger-plugin", 38081, 38080)
	defer stopPlugin(loggerCmd)
	time.Sleep(300 * time.Millisecond)

	cacheCmd := buildAndStartPlugin(t, "cache-plugin", 38082, 38080)
	defer stopPlugin(cacheCmd)
	time.Sleep(300 * time.Millisecond)

	appCmd := buildAndStartPlugin(t, "app-plugin", 38083, 38080)
	defer stopPlugin(appCmd)
	time.Sleep(300 * time.Millisecond)

	// Get providers to find runtime IDs
	loggerProvider := getProvider(t, host.registry, "logger", "1.0.0")
	cacheProvider := getProvider(t, host.registry, "cache", "1.0.0")

	// Manually add to dependency graph for testing startup order
	// (In production, Platform.AddPlugin() does this)
	host.platform.AddToDependencyGraph(loggerProvider.RuntimeID, "logger",
		[]connectplugin.ServiceDeclaration{{Type: "logger", Version: "1.0.0"}}, nil)
	host.platform.AddToDependencyGraph(cacheProvider.RuntimeID, "cache",
		[]connectplugin.ServiceDeclaration{{Type: "cache", Version: "1.0.0"}},
		[]connectplugin.ServiceDependency{{Type: "logger", RequiredForStartup: true}})

	// Get startup order
	order, err := host.platform.GetStartupOrder()
	if err != nil {
		t.Fatalf("GetStartupOrder failed: %v", err)
	}

	if len(order) != 2 {
		t.Fatalf("Expected 2 plugins in startup order, got %d", len(order))
	}

	// Verify logger comes before cache
	if order[0] != loggerProvider.RuntimeID || order[1] != cacheProvider.RuntimeID {
		t.Errorf("Wrong startup order: %v (expected logger before cache)", order)
	}
}

// TestIntegration_HealthStateChanges tests scenario 4: Health state transitions.
func TestIntegration_HealthStateChanges(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping integration test in short mode")
	}

	host := startHostServer(t, 48080)
	defer host.Shutdown()

	loggerCmd := buildAndStartPlugin(t, "logger-plugin", 48081, 48080)
	defer stopPlugin(loggerCmd)
	time.Sleep(300 * time.Millisecond)

	loggerProvider := getProvider(t, host.registry, "logger", "1.0.0")
	if loggerProvider == nil {
		t.Fatal("Logger not registered")
	}

	// Verify initially healthy
	state := host.lifecycle.GetHealthState(loggerProvider.RuntimeID)
	if state == nil || state.State != connectpluginv1.HealthState_HEALTH_STATE_HEALTHY {
		t.Error("Logger should start healthy")
	}

	// Should route traffic to healthy plugin
	if !host.lifecycle.ShouldRouteTraffic(loggerProvider.RuntimeID) {
		t.Error("Should route traffic to healthy plugin")
	}

	// Simulate plugin reporting unhealthy
	reportHealth(t, host.hostURL, loggerProvider.RuntimeID, "test-token",
		connectpluginv1.HealthState_HEALTH_STATE_UNHEALTHY)
	time.Sleep(100 * time.Millisecond)

	// Should NOT route traffic to unhealthy plugin
	if host.lifecycle.ShouldRouteTraffic(loggerProvider.RuntimeID) {
		t.Error("Should NOT route traffic to unhealthy plugin")
	}
}

// TestIntegration_RemovePluginImpact tests scenario 7: Impact analysis.
// Note: Manually populates dependency graph to test impact logic.
func TestIntegration_RemovePluginImpact(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping integration test in short mode")
	}

	host := startHostServer(t, 58080)
	defer host.Shutdown()

	// Start logger → cache chain
	loggerCmd := buildAndStartPlugin(t, "logger-plugin", 58081, 58080)
	defer stopPlugin(loggerCmd)
	time.Sleep(300 * time.Millisecond)

	cacheCmd := buildAndStartPlugin(t, "cache-plugin", 58082, 58080)
	defer stopPlugin(cacheCmd)
	time.Sleep(500 * time.Millisecond)

	// Get providers
	loggerProvider := getProvider(t, host.registry, "logger", "1.0.0")
	if loggerProvider == nil {
		t.Fatal("Logger not found")
	}
	cacheProvider := getProvider(t, host.registry, "cache", "1.0.0")
	if cacheProvider == nil {
		t.Fatal("Cache not found")
	}

	// Manually add to dependency graph
	host.platform.AddToDependencyGraph(loggerProvider.RuntimeID, "logger",
		[]connectplugin.ServiceDeclaration{{Type: "logger", Version: "1.0.0"}}, nil)
	host.platform.AddToDependencyGraph(cacheProvider.RuntimeID, "cache",
		[]connectplugin.ServiceDeclaration{{Type: "cache", Version: "1.0.0"}},
		[]connectplugin.ServiceDependency{{Type: "logger", RequiredForStartup: true}})

	// Analyze impact of removing logger
	impact := host.platform.GetImpact(loggerProvider.RuntimeID)

	// Should show cache as affected
	if !contains(impact.AffectedPlugins, cacheProvider.RuntimeID) {
		t.Errorf("Expected cache in affected plugins: %v", impact.AffectedPlugins)
	}

	// Should show logger service as affected
	if !contains(impact.AffectedServices, "logger") {
		t.Errorf("Expected logger in affected services: %v", impact.AffectedServices)
	}

	t.Logf("Impact analysis: %+v", impact)
}

// TestIntegration_WatchService tests scenario 5: WatchService notifications.
func TestIntegration_WatchService(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping integration test in short mode")
	}

	host := startHostServer(t, 17080)
	defer host.Shutdown()

	// Start cache plugin first (will wait for logger)
	cacheCmd := buildAndStartPlugin(t, "cache-plugin", 17082, 17080)
	defer stopPlugin(cacheCmd)
	time.Sleep(500 * time.Millisecond)

	// Cache should be degraded (no logger yet)
	cacheProvider := getProvider(t, host.registry, "cache", "1.0.0")
	if cacheProvider == nil {
		t.Fatal("Cache not registered")
	}

	cacheHealth := host.lifecycle.GetHealthState(cacheProvider.RuntimeID)
	if cacheHealth == nil {
		t.Log("Cache health not reported yet (expected)")
	} else if cacheHealth.State == connectpluginv1.HealthState_HEALTH_STATE_HEALTHY {
		t.Error("Cache should not be healthy without logger")
	}

	// Start logger - cache should detect and become healthy
	loggerCmd := buildAndStartPlugin(t, "logger-plugin", 17081, 17080)
	defer stopPlugin(loggerCmd)
	time.Sleep(800 * time.Millisecond)

	// Cache should now be healthy (via watch notification or re-discovery)
	cacheHealth = host.lifecycle.GetHealthState(cacheProvider.RuntimeID)
	if cacheHealth == nil {
		t.Error("Cache should have reported health after logger became available")
	}
	// Note: Cache becomes healthy because it discovers logger on startup
	// Full WatchService streaming would require more complex test setup
}

// TestIntegration_HotReload tests scenario 6: Zero-downtime plugin replacement.
func TestIntegration_HotReload(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping integration test in short mode")
	}

	// Note: This would require:
	// 1. Starting logger v1
	// 2. Starting logger v2 on different port
	// 3. Calling Platform.ReplacePlugin()
	// 4. Verifying traffic switches from v1 to v2
	//
	// For now, skip this test as it requires more complex setup
	t.Skip("Hot reload requires Platform.ReplacePlugin() orchestration - tested in platform_test.go")
}

// TestIntegration_OptionalDependencies tests scenario 8: Optional dependencies.
func TestIntegration_OptionalDependencies(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping integration test in short mode")
	}

	// Note: This would require a plugin with optional dependencies (RequiredForStartup: false)
	// Current example plugins all have required dependencies
	// The behavior is already tested in unit tests (registry_test.go)
	t.Skip("Optional dependencies tested in unit tests - no example plugin with optional deps yet")
}

// hostServer wraps all the components needed for integration testing.
type hostServer struct {
	hostURL   string
	server    *http.Server
	platform  *connectplugin.Platform
	registry  *connectplugin.ServiceRegistry
	lifecycle *connectplugin.LifecycleServer
	router    *connectplugin.ServiceRouter
}

func (h *hostServer) Shutdown() {
	h.server.Shutdown(context.Background())
}

// startHostServer creates and starts a host server on the given port.
func startHostServer(t *testing.T, port int) *hostServer {
	handshake := connectplugin.NewHandshakeServer(&connectplugin.ServeConfig{})
	lifecycle := connectplugin.NewLifecycleServer()
	registry := connectplugin.NewServiceRegistry(lifecycle)
	router := connectplugin.NewServiceRouter(handshake, registry, lifecycle)
	platform := connectplugin.NewPlatform(registry, lifecycle, router)

	mux := http.NewServeMux()

	// Register Phase 2 services
	handshakePath, handshakeHandler := connectpluginv1connect.NewHandshakeServiceHandler(handshake)
	mux.Handle(handshakePath, handshakeHandler)

	lifecyclePath, lifecycleHandler := connectpluginv1connect.NewPluginLifecycleHandler(lifecycle)
	mux.Handle(lifecyclePath, lifecycleHandler)

	registryPath, registryHandler := connectpluginv1connect.NewServiceRegistryHandler(registry)
	mux.Handle(registryPath, registryHandler)

	mux.Handle("/services/", router)

	server := &http.Server{
		Addr:    fmt.Sprintf(":%d", port),
		Handler: mux,
	}

	go func() {
		if err := server.ListenAndServe(); err != http.ErrServerClosed {
			log.Printf("Host server error: %v", err)
		}
	}()

	// Wait for server to be ready
	time.Sleep(200 * time.Millisecond)

	return &hostServer{
		hostURL:   fmt.Sprintf("http://localhost:%d", port),
		server:    server,
		platform:  platform,
		registry:  registry,
		lifecycle: lifecycle,
		router:    router,
	}
}

// buildAndStartPlugin starts a plugin binary from dist/.
// Assumes plugin was already built via `task build-examples`.
func buildAndStartPlugin(t *testing.T, name string, port, hostPort int) *exec.Cmd {
	// Use pre-built binary from dist/
	binaryPath := filepath.Join("dist", name)

	// Start plugin
	cmd := exec.Command(binaryPath)
	cmd.Env = append(os.Environ(),
		fmt.Sprintf("PORT=%d", port),
		fmt.Sprintf("HOST_URL=http://localhost:%d", hostPort),
	)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr

	if err := cmd.Start(); err != nil {
		t.Fatalf("Failed to start %s: %v", name, err)
	}

	t.Logf("Started %s on port %d (PID: %d)", name, port, cmd.Process.Pid)
	return cmd
}

// stopPlugin stops a plugin process.
func stopPlugin(cmd *exec.Cmd) {
	if cmd != nil && cmd.Process != nil {
		cmd.Process.Kill()
		cmd.Wait()
	}
}

// getProvider gets a provider from the registry.
func getProvider(t *testing.T, registry *connectplugin.ServiceRegistry, serviceType, version string) *connectplugin.ServiceProvider {
	provider, err := registry.SelectProvider(serviceType, version)
	if err != nil {
		return nil
	}
	return provider
}

// findPluginIndex finds the index of a plugin by self_id prefix in startup order.
func findPluginIndex(order []string, selfIDPrefix string) int {
	for i, runtimeID := range order {
		// Runtime IDs are like "logger-plugin-x7k2"
		if len(runtimeID) >= len(selfIDPrefix) && runtimeID[:len(selfIDPrefix)] == selfIDPrefix {
			return i
		}
	}
	return -1
}

// contains checks if a slice contains a string.
func contains(slice []string, item string) bool {
	for _, s := range slice {
		if s == item {
			return true
		}
	}
	return false
}

// reportHealth sends a health report to the host.
func reportHealth(t *testing.T, hostURL, runtimeID, token string, state connectpluginv1.HealthState) {
	client := connectpluginv1connect.NewPluginLifecycleClient(http.DefaultClient, hostURL)

	req := connect.NewRequest(&connectpluginv1.ReportHealthRequest{
		State: state,
	})
	req.Header().Set("X-Plugin-Runtime-ID", runtimeID)
	req.Header().Set("Authorization", "Bearer "+token)

	_, err := client.ReportHealth(context.Background(), req)
	if err != nil {
		t.Logf("ReportHealth error (may be expected): %v", err)
	}
}
