package connectplugin

import (
	"context"
	"fmt"
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
	Strategies map[string]LaunchStrategy   // Exported for daemon access
	Specs      map[string]PluginSpec       // Exported for daemon access
	Instances  map[string]*LaunchedPlugin  // Exported for daemon access
	Mu         sync.Mutex                  // Exported for daemon access
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
		Strategies: make(map[string]LaunchStrategy),
		Specs:      make(map[string]PluginSpec),
		Instances:  make(map[string]*LaunchedPlugin),
	}
}

// RegisterStrategy registers a launch strategy.
func (l *PluginLauncher) RegisterStrategy(strategy LaunchStrategy) {
	l.Mu.Lock()
	defer l.Mu.Unlock()
	l.Strategies[strategy.Name()] = strategy
}

// Configure adds plugin specifications.
func (l *PluginLauncher) Configure(specs map[string]PluginSpec) {
	l.Mu.Lock()
	defer l.Mu.Unlock()
	for name, spec := range specs {
		l.Specs[name] = spec
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
	l.Mu.Lock()
	defer l.Mu.Unlock()

	// 1. Validate plugin is configured
	spec, ok := l.Specs[pluginName]
	if !ok {
		return "", fmt.Errorf("plugin %q not configured in launcher", pluginName)
	}

	// 2. Validate plugin provides this service
	if !contains(spec.Provides, serviceType) {
		return "", fmt.Errorf("plugin %q doesn't provide service %q (provides: %v)",
			pluginName, serviceType, spec.Provides)
	}

	// 3. Launch plugin if not already running
	instance, exists := l.Instances[pluginName]
	if !exists {
		if err := l.launchPluginLocked(pluginName, spec); err != nil {
			return "", fmt.Errorf("failed to launch plugin %q: %w", pluginName, err)
		}
		instance = l.Instances[pluginName]
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
	strategy, ok := l.Strategies[spec.Strategy]
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
	l.Instances[pluginName] = &LaunchedPlugin{
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
	l.Mu.Lock()
	spec, ok := l.Specs[pluginName]
	l.Mu.Unlock()

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
	l.Mu.Lock()
	defer l.Mu.Unlock()

	for name, instance := range l.Instances {
		if instance.Cleanup != nil {
			instance.Cleanup()
		}
		delete(l.Instances, name)
	}
}

// availableStrategies returns list of registered strategy names.
func (l *PluginLauncher) availableStrategies() []string {
	names := make([]string, 0, len(l.Strategies))
	for name := range l.Strategies {
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
