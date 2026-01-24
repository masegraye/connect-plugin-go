package connectplugin

import (
	"context"
	"fmt"
	"time"

	"github.com/masegraye/connect-plugin-go/internal/depgraph"
)

// Platform manages the lifecycle of plugins in a multi-plugin environment.
// It coordinates the dependency graph, service registry, and health tracking.
type Platform struct {
	depGraph        *depgraph.Graph
	registry        *ServiceRegistry
	lifecycleServer *LifecycleServer
	router          *ServiceRouter

	// Plugin instances
	plugins map[string]*PluginInstance
}

// PluginInstance represents a running plugin.
type PluginInstance struct {
	RuntimeID string
	SelfID    string
	Metadata  PluginMetadata
	Endpoint  string // Internal endpoint (e.g., "http://localhost:8081")
	Token     string // Runtime token for this plugin

	// Control client for calling plugin's PluginControl service
	control *PluginControlClient
}

// PluginConfig is the configuration for adding a plugin to the platform.
type PluginConfig struct {
	// SelfID is the plugin's self-declared identity
	SelfID string

	// SelfVersion is the plugin's version
	SelfVersion string

	// Endpoint is the plugin's internal HTTP endpoint
	Endpoint string

	// Metadata includes service declarations
	Metadata PluginMetadata
}

// NewPlatform creates a new platform instance.
func NewPlatform(
	registry *ServiceRegistry,
	lifecycle *LifecycleServer,
	router *ServiceRouter,
) *Platform {
	return &Platform{
		depGraph:        depgraph.New(),
		registry:        registry,
		lifecycleServer: lifecycle,
		router:          router,
		plugins:         make(map[string]*PluginInstance),
	}
}

// AddPlugin adds a plugin to the platform at runtime.
func (p *Platform) AddPlugin(ctx context.Context, config PluginConfig) error {
	// 1. Validate dependencies are available
	for _, dep := range config.Metadata.Requires {
		if dep.RequiredForStartup && !p.depGraph.HasService(dep.Type) {
			return fmt.Errorf("required service %q not available for plugin %q",
				dep.Type, config.SelfID)
		}
	}

	// 2. Generate runtime identity
	runtimeID := generateRuntimeID(config.SelfID)
	runtimeToken := generateToken()

	// 3. Create plugin instance
	instance := &PluginInstance{
		RuntimeID: runtimeID,
		SelfID:    config.SelfID,
		Metadata:  config.Metadata,
		Endpoint:  config.Endpoint,
		Token:     runtimeToken,
		control:   NewPluginControlClient(config.Endpoint, nil), // TODO: Use authenticated client
	}

	// 4. Add to dependency graph
	depNode := &depgraph.Node{
		RuntimeID: runtimeID,
		SelfID:    config.SelfID,
	}

	for _, svc := range config.Metadata.Provides {
		depNode.Provides = append(depNode.Provides, depgraph.ServiceDeclaration{
			Type:    svc.Type,
			Version: svc.Version,
		})
	}

	for _, dep := range config.Metadata.Requires {
		depNode.Requires = append(depNode.Requires, depgraph.ServiceDependency{
			Type:               dep.Type,
			MinVersion:         dep.MinVersion,
			RequiredForStartup: dep.RequiredForStartup,
			WatchForChanges:    dep.WatchForChanges,
		})
	}

	p.depGraph.Add(depNode)

	// 5. Wait for plugin to become healthy
	if err := p.waitForHealthy(ctx, runtimeID, 30*time.Second); err != nil {
		p.depGraph.Remove(runtimeID)
		return fmt.Errorf("plugin %q did not become healthy: %w", config.SelfID, err)
	}

	// 6. Register plugin endpoint in router
	p.router.RegisterPluginEndpoint(runtimeID, config.Endpoint)

	// 7. Store plugin instance
	p.plugins[runtimeID] = instance

	return nil
}

// RemovePlugin removes a plugin from the platform.
// Analyzes impact and notifies dependent plugins before removal.
func (p *Platform) RemovePlugin(ctx context.Context, runtimeID string) error {
	instance, ok := p.plugins[runtimeID]
	if !ok {
		return fmt.Errorf("plugin not found: %s", runtimeID)
	}

	// 1. Compute impact
	impact := p.depGraph.GetImpact(runtimeID)

	// 2. Notify watchers that services are going away
	// Plugins watching these services will receive UNAVAILABLE events when we unregister
	_ = impact.AffectedServices // Services will be unregistered below

	// 3. Grace period for plugins to adapt (5 seconds)
	time.Sleep(5 * time.Second)

	// 4. Unregister all services from this plugin
	p.registry.UnregisterPluginServices(runtimeID)

	// 5. Request graceful shutdown
	if instance.control != nil {
		_, err := instance.control.Shutdown(ctx, 30, "plugin removed")
		if err != nil {
			// Log error but continue with removal
		}
	}

	// 6. Remove from dependency graph
	p.depGraph.Remove(runtimeID)

	// 7. Remove from plugins map
	delete(p.plugins, runtimeID)

	return nil
}

// ReplacePlugin replaces a plugin with a new version (hot reload).
// Uses blue-green deployment: start new, switch routes, stop old.
func (p *Platform) ReplacePlugin(ctx context.Context, runtimeID string, newConfig PluginConfig) error {
	oldInstance, ok := p.plugins[runtimeID]
	if !ok {
		return fmt.Errorf("plugin not found: %s", runtimeID)
	}

	// 1. Start new version in parallel
	newRuntimeID := generateRuntimeID(newConfig.SelfID)
	newToken := generateToken()

	newInstance := &PluginInstance{
		RuntimeID: newRuntimeID,
		SelfID:    newConfig.SelfID,
		Metadata:  newConfig.Metadata,
		Endpoint:  newConfig.Endpoint,
		Token:     newToken,
		control:   NewPluginControlClient(newConfig.Endpoint, nil),
	}

	// 2. Add to dependency graph
	newNode := &depgraph.Node{
		RuntimeID: newRuntimeID,
		SelfID:    newConfig.SelfID,
	}

	for _, svc := range newConfig.Metadata.Provides {
		newNode.Provides = append(newNode.Provides, depgraph.ServiceDeclaration{
			Type:    svc.Type,
			Version: svc.Version,
		})
	}

	for _, dep := range newConfig.Metadata.Requires {
		newNode.Requires = append(newNode.Requires, depgraph.ServiceDependency{
			Type:               dep.Type,
			MinVersion:         dep.MinVersion,
			RequiredForStartup: dep.RequiredForStartup,
			WatchForChanges:    dep.WatchForChanges,
		})
	}

	p.depGraph.Add(newNode)

	// 3. Wait for new version to become healthy
	if err := p.waitForHealthy(ctx, newRuntimeID, 30*time.Second); err != nil {
		p.depGraph.Remove(newRuntimeID)
		return fmt.Errorf("new version did not become healthy: %w", err)
	}

	// 4. Register new plugin endpoint in router
	p.router.RegisterPluginEndpoint(newRuntimeID, newConfig.Endpoint)

	// 5. Atomic switch in registry
	// TODO: Implement SwitchProvider in registry
	// For now, services are already registered by the new plugin

	// 6. Drain old version (finish in-flight requests)
	p.drainPlugin(oldInstance, 30*time.Second)

	// 7. Request shutdown of old version
	if oldInstance.control != nil {
		oldInstance.control.Shutdown(ctx, 10, "replaced with new version")
	}

	// 8. Remove old version from graph and registry
	p.registry.UnregisterPluginServices(runtimeID)
	p.depGraph.Remove(runtimeID)

	// 9. Update plugins map
	delete(p.plugins, runtimeID)
	p.plugins[newRuntimeID] = newInstance

	return nil
}

// GetImpact returns the impact analysis for removing a plugin.
func (p *Platform) GetImpact(runtimeID string) *depgraph.ImpactAnalysis {
	return p.depGraph.GetImpact(runtimeID)
}

// GetStartupOrder returns the dependency-ordered plugin startup sequence.
func (p *Platform) GetStartupOrder() ([]string, error) {
	return p.depGraph.StartupOrder()
}

// AddToDependencyGraph manually adds a plugin to the dependency graph.
// This is useful for testing or when plugins self-register without going through AddPlugin().
func (p *Platform) AddToDependencyGraph(
	runtimeID string,
	selfID string,
	provides []ServiceDeclaration,
	requires []ServiceDependency,
) {
	node := &depgraph.Node{
		RuntimeID: runtimeID,
		SelfID:    selfID,
		Provides:  make([]depgraph.ServiceDeclaration, 0, len(provides)),
		Requires:  make([]depgraph.ServiceDependency, 0, len(requires)),
	}

	for _, svc := range provides {
		node.Provides = append(node.Provides, depgraph.ServiceDeclaration{
			Type:    svc.Type,
			Version: svc.Version,
		})
	}

	for _, dep := range requires {
		node.Requires = append(node.Requires, depgraph.ServiceDependency{
			Type:               dep.Type,
			MinVersion:         dep.MinVersion,
			RequiredForStartup: dep.RequiredForStartup,
			WatchForChanges:    dep.WatchForChanges,
		})
	}

	p.depGraph.Add(node)
}

// waitForHealthy waits for a plugin to report healthy state.
func (p *Platform) waitForHealthy(ctx context.Context, runtimeID string, timeout time.Duration) error {
	deadline := time.Now().Add(timeout)
	ticker := time.NewTicker(500 * time.Millisecond)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return ctx.Err()

		case <-ticker.C:
			if time.Now().After(deadline) {
				return fmt.Errorf("timeout waiting for plugin to become healthy")
			}

			// Check health state
			state := p.lifecycleServer.GetHealthState(runtimeID)
			if state != nil && state.State == 1 { // HEALTH_STATE_HEALTHY
				return nil
			}
		}
	}
}

// drainPlugin waits for in-flight requests to complete.
func (p *Platform) drainPlugin(instance *PluginInstance, timeout time.Duration) {
	// Simple implementation: just wait
	// TODO: Track in-flight requests and wait for them to complete
	time.Sleep(timeout)
}
