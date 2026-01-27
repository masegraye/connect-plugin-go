package connectplugin

import (
	"context"
	"fmt"
	"net/http"
	"sync"
	"time"

	"connectrpc.com/connect"
	connectpluginv1 "github.com/masegraye/connect-plugin-go/gen/plugin/v1"
)

// PluginLauncher manages plugin lifecycle using pluggable strategies.
// Integrates with uber/fx for dependency injection.
type PluginLauncher struct {
	platform   *Platform
	registry   *ServiceRegistry
	strategies map[string]LaunchStrategy
	specs      map[string]PluginSpec
	instances  map[string]*LaunchedPlugin
	mu         sync.Mutex
}

// LaunchedPlugin tracks a launched plugin (used by PluginLauncher).
type LaunchedPlugin struct {
	PluginName string
	Endpoint   string
	Cleanup    func()
	Provides   []string
}

// NewPluginLauncher creates a plugin launcher.
func NewPluginLauncher(platform *Platform, registry *ServiceRegistry) *PluginLauncher {
	return &PluginLauncher{
		platform:   platform,
		registry:   registry,
		strategies: make(map[string]LaunchStrategy),
		specs:      make(map[string]PluginSpec),
		instances:  make(map[string]*LaunchedPlugin),
	}
}

// RegisterStrategy registers a launch strategy.
func (l *PluginLauncher) RegisterStrategy(strategy LaunchStrategy) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.strategies[strategy.Name()] = strategy
}

// Configure adds plugin specifications.
func (l *PluginLauncher) Configure(specs map[string]PluginSpec) {
	l.mu.Lock()
	defer l.mu.Unlock()
	for name, spec := range specs {
		l.specs[name] = spec
	}
}

// GetService returns a service endpoint for a specific service type from a plugin.
// If the plugin isn't running, launches it first using the configured strategy.
// If the plugin is already running, discovers the service from the registry.
//
// Returns the service endpoint. Caller creates typed client.
//
// Example:
//   endpoint, _ := launcher.GetService("logger-plugin", "logger")
//   loggerClient := loggerv1connect.NewLoggerClient(httpClient, endpoint)
func (l *PluginLauncher) GetService(pluginName, serviceType string) (string, error) {
	l.mu.Lock()
	defer l.mu.Unlock()

	// 1. Validate plugin is configured
	spec, ok := l.specs[pluginName]
	if !ok {
		return "", fmt.Errorf("plugin %q not configured in launcher", pluginName)
	}

	// 2. Validate plugin provides this service
	if !contains(spec.Provides, serviceType) {
		return "", fmt.Errorf("plugin %q doesn't provide service %q (provides: %v)",
			pluginName, serviceType, spec.Provides)
	}

	// 3. Launch plugin if not already running
	instance, exists := l.instances[pluginName]
	if !exists {
		if err := l.launchPluginLocked(pluginName, spec); err != nil {
			return "", fmt.Errorf("failed to launch plugin %q: %w", pluginName, err)
		}
		instance = l.instances[pluginName]
	}

	// 4. Verify service is registered (optional check)
	// Plugin should have self-registered this service
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	discReq := connect.NewRequest(&connectpluginv1.DiscoverServiceRequest{
		ServiceType: serviceType,
		MinVersion:  "",
	})

	_, err := l.registry.DiscoverService(ctx, discReq)
	if err != nil {
		return "", fmt.Errorf("service %q not found in registry: %w", serviceType, err)
	}

	// 5. Return plugin's base endpoint URL
	// Caller creates typed client that talks directly to plugin
	// For host→plugin calls, use direct endpoint (not routed)
	return instance.Endpoint, nil
}

// launchPluginLocked launches a plugin using its configured strategy.
// Caller must hold lock.
func (l *PluginLauncher) launchPluginLocked(pluginName string, spec PluginSpec) error {
	// Get strategy
	strategy, ok := l.strategies[spec.Strategy]
	if !ok {
		return fmt.Errorf("strategy %q not registered (available: %v)",
			spec.Strategy, l.availableStrategies())
	}

	// Launch plugin
	ctx := context.Background()
	endpoint, cleanup, err := strategy.Launch(ctx, spec)
	if err != nil {
		return err
	}

	// Wait for plugin to self-register
	// Plugin connects to host, registers services, reports health
	time.Sleep(500 * time.Millisecond)

	// Store instance
	l.instances[pluginName] = &LaunchedPlugin{
		PluginName: pluginName,
		Endpoint:   endpoint,
		Cleanup:    cleanup,
		Provides:   spec.Provides,
	}

	return nil
}

// GetDefaultService is a convenience for single-service plugins.
// Returns error if plugin provides multiple services (caller must specify which).
func (l *PluginLauncher) GetDefaultService(pluginName string) (string, error) {
	l.mu.Lock()
	spec, ok := l.specs[pluginName]
	l.mu.Unlock()

	if !ok {
		return "", fmt.Errorf("plugin %q not configured", pluginName)
	}

	if len(spec.Provides) != 1 {
		return "", fmt.Errorf("plugin %q provides %d services (%v), specify which service type",
			pluginName, len(spec.Provides), spec.Provides)
	}

	return l.GetService(pluginName, spec.Provides[0])
}

// Shutdown stops all launched plugins.
// Should be called in fx OnStop hook.
func (l *PluginLauncher) Shutdown() {
	fmt.Println("===== PluginLauncher.Shutdown() called =====")
	l.mu.Lock()
	defer l.mu.Unlock()

	fmt.Printf("===== Shutting down %d plugin instances =====\n", len(l.instances))
	for name, instance := range l.instances {
		fmt.Printf("===== Shutting down plugin: %s (endpoint: %s) =====\n", name, instance.Endpoint)
		// Try calling Shutdown RPC before sending signals
		if instance.Endpoint != "" {
			fmt.Printf("===== Calling Shutdown RPC for %s =====\n", name)
			shutdownCtx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
			controlClient := NewPluginControlClient(instance.Endpoint, http.DefaultClient)
			acknowledged, err := controlClient.Shutdown(shutdownCtx, 10, "daemon shutdown")
			cancel()
			if err != nil {
				fmt.Printf("===== Shutdown RPC for %s failed: %v =====\n", name, err)
			} else if acknowledged {
				fmt.Printf("===== Shutdown RPC for %s acknowledged, waiting 500ms =====\n", name)
				// Wait a bit for plugin to clean up
				time.Sleep(500 * time.Millisecond)
			}
		}

		// Then call cleanup (SIGINT/SIGKILL as fallback)
		if instance.Cleanup != nil {
			fmt.Printf("===== Calling cleanup function for %s =====\n", name)
			instance.Cleanup()
			fmt.Printf("===== Cleanup for %s complete =====\n", name)
		}
		delete(l.instances, name)
	}
	fmt.Println("===== PluginLauncher.Shutdown() complete =====")
}

// availableStrategies returns list of registered strategy names.
func (l *PluginLauncher) availableStrategies() []string {
	names := make([]string, 0, len(l.strategies))
	for name := range l.strategies {
		names = append(names, name)
	}
	return names
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

// Accessor methods for safe external access (thread-safe)

// GetInstance returns a plugin instance if it exists
func (l *PluginLauncher) GetInstance(name string) (*LaunchedPlugin, bool) {
	l.mu.Lock()
	defer l.mu.Unlock()

	instance, ok := l.instances[name]
	return instance, ok
}

// GetStrategy returns a strategy by name
func (l *PluginLauncher) GetStrategy(name string) (LaunchStrategy, bool) {
	l.mu.Lock()
	defer l.mu.Unlock()

	strategy, ok := l.strategies[name]
	return strategy, ok
}

// StoreInstance stores a plugin instance (for daemon use during discovery)
func (l *PluginLauncher) StoreInstance(name string, instance *LaunchedPlugin) {
	l.mu.Lock()
	defer l.mu.Unlock()

	l.instances[name] = instance
}

// RemoveInstance removes a plugin instance
func (l *PluginLauncher) RemoveInstance(name string) {
	l.mu.Lock()
	defer l.mu.Unlock()

	delete(l.instances, name)
}
