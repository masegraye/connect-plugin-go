package connectplugin

import (
	"context"
	"fmt"
	"math/rand"
	"net/http"
	"sync"
	"time"

	"connectrpc.com/connect"
	connectpluginv1 "github.com/masegraye/connect-plugin-go/gen/plugin/v1"
	"github.com/masegraye/connect-plugin-go/gen/plugin/v1/connectpluginv1connect"
)

// SelectionStrategy determines how the host selects a provider when multiple exist.
type SelectionStrategy int

const (
	// SelectionFirst always returns the first available provider.
	SelectionFirst SelectionStrategy = iota

	// SelectionRoundRobin rotates through providers.
	SelectionRoundRobin

	// SelectionRandom picks a random provider.
	SelectionRandom

	// SelectionWeighted uses weighted selection (future: based on load/health).
	SelectionWeighted
)

// ServiceRegistry manages plugin-to-plugin service discovery.
// Tracks service providers and handles registration/discovery.
type ServiceRegistry struct {
	mu sync.RWMutex

	// providers maps service type to list of providers
	providers map[string][]*ServiceProvider

	// registrations maps registration_id to provider (for unregister)
	registrations map[string]*ServiceProvider

	// selection maps service type to selection strategy (host config)
	selection map[string]SelectionStrategy

	// roundRobinIndex tracks position for round-robin selection
	roundRobinIndex map[string]int

	// lifecycleServer for checking provider health
	lifecycleServer *LifecycleServer

	// watchers tracks clients watching service types
	watchers map[string][]*serviceWatcher
}

// serviceWatcher represents a client watching a service type.
type serviceWatcher struct {
	ch     chan *connectpluginv1.WatchServiceEvent
	ctx    context.Context
	cancel context.CancelFunc
}

// ServiceProvider represents a registered service provider.
type ServiceProvider struct {
	RegistrationID string
	RuntimeID      string
	ServiceType    string
	Version        string
	EndpointPath   string
	Metadata       map[string]string
	RegisteredAt   time.Time
}

// NewServiceRegistry creates a new service registry.
func NewServiceRegistry(lifecycle *LifecycleServer) *ServiceRegistry {
	return &ServiceRegistry{
		providers:       make(map[string][]*ServiceProvider),
		registrations:   make(map[string]*ServiceProvider),
		selection:       make(map[string]SelectionStrategy),
		roundRobinIndex: make(map[string]int),
		lifecycleServer: lifecycle,
		watchers:        make(map[string][]*serviceWatcher),
	}
}

// SetSelectionStrategy configures the selection strategy for a service type.
// This is called by the host during configuration.
func (r *ServiceRegistry) SetSelectionStrategy(serviceType string, strategy SelectionStrategy) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.selection[serviceType] = strategy
}

// RegisterService handles service registration from plugins.
func (r *ServiceRegistry) RegisterService(
	ctx context.Context,
	req *connect.Request[connectpluginv1.RegisterServiceRequest],
) (*connect.Response[connectpluginv1.RegisterServiceResponse], error) {
	// Extract runtime_id from request headers
	runtimeID := req.Header().Get("X-Plugin-Runtime-ID")
	if runtimeID == "" {
		return nil, connect.NewError(
			connect.CodeInvalidArgument,
			fmt.Errorf("X-Plugin-Runtime-ID header required"),
		)
	}

	r.mu.Lock()
	defer r.mu.Unlock()

	// Create service provider
	registrationID := generateRegistrationID()
	provider := &ServiceProvider{
		RegistrationID: registrationID,
		RuntimeID:      runtimeID,
		ServiceType:    req.Msg.ServiceType,
		Version:        req.Msg.Version,
		EndpointPath:   req.Msg.EndpointPath,
		Metadata:       req.Msg.Metadata,
		RegisteredAt:   time.Now(),
	}

	// Add to providers list (multi-provider support)
	r.providers[req.Msg.ServiceType] = append(r.providers[req.Msg.ServiceType], provider)

	// Store registration for unregister lookup
	r.registrations[registrationID] = provider

	// Notify watchers that service is now available
	r.notifyWatchersLocked(req.Msg.ServiceType)

	return connect.NewResponse(&connectpluginv1.RegisterServiceResponse{
		RegistrationId: registrationID,
	}), nil
}

// UnregisterService handles service unregistration.
func (r *ServiceRegistry) UnregisterService(
	ctx context.Context,
	req *connect.Request[connectpluginv1.UnregisterServiceRequest],
) (*connect.Response[connectpluginv1.UnregisterServiceResponse], error) {
	r.mu.Lock()
	defer r.mu.Unlock()

	// Look up provider by registration ID
	provider, ok := r.registrations[req.Msg.RegistrationId]
	if !ok {
		return nil, connect.NewError(
			connect.CodeNotFound,
			fmt.Errorf("registration not found: %s", req.Msg.RegistrationId),
		)
	}

	// Remove from providers list
	serviceType := provider.ServiceType
	providers := r.providers[serviceType]
	for i, p := range providers {
		if p.RegistrationID == req.Msg.RegistrationId {
			r.providers[serviceType] = append(providers[:i], providers[i+1:]...)
			break
		}
	}

	// Remove from registrations map
	delete(r.registrations, req.Msg.RegistrationId)

	// Notify watchers about service state change
	r.notifyWatchersLocked(serviceType)

	return connect.NewResponse(&connectpluginv1.UnregisterServiceResponse{}), nil
}

// UnregisterPluginServices removes all services registered by a plugin.
// Called by the host when a plugin shuts down.
func (r *ServiceRegistry) UnregisterPluginServices(runtimeID string) {
	r.mu.Lock()
	defer r.mu.Unlock()

	// Find all registrations for this plugin
	toRemove := make([]string, 0)
	for regID, provider := range r.registrations {
		if provider.RuntimeID == runtimeID {
			toRemove = append(toRemove, regID)
		}
	}

	// Remove each registration
	for _, regID := range toRemove {
		provider := r.registrations[regID]
		serviceType := provider.ServiceType

		// Remove from providers list
		providers := r.providers[serviceType]
		for i, p := range providers {
			if p.RegistrationID == regID {
				r.providers[serviceType] = append(providers[:i], providers[i+1:]...)
				break
			}
		}

		// Remove from registrations map
		delete(r.registrations, regID)
	}
}

// SelectProvider selects a single provider for the given service type.
// This is where the host-controlled selection happens.
func (r *ServiceRegistry) SelectProvider(serviceType string, minVersion string) (*ServiceProvider, error) {
	r.mu.Lock()
	defer r.mu.Unlock()

	// Get all providers for this service type
	allProviders := r.providers[serviceType]
	if len(allProviders) == 0 {
		return nil, fmt.Errorf("no providers for service %q", serviceType)
	}

	// Filter by version
	compatible := r.filterCompatibleVersions(allProviders, minVersion)
	if len(compatible) == 0 {
		return nil, fmt.Errorf("no compatible providers for service %q (min version: %s)",
			serviceType, minVersion)
	}

	// Filter by health (only Healthy or Degraded)
	available := r.filterAvailable(compatible)
	if len(available) == 0 {
		return nil, fmt.Errorf("no available providers for service %q (all unhealthy)",
			serviceType)
	}

	// Apply selection strategy
	strategy := r.selection[serviceType] // Defaults to 0 (SelectionFirst)
	return r.applyStrategy(serviceType, available, strategy), nil
}

// filterCompatibleVersions filters providers by minimum version.
// For now, we do simple string comparison (assumes semver format).
// TODO: Use proper semver library for version comparison.
func (r *ServiceRegistry) filterCompatibleVersions(providers []*ServiceProvider, minVersion string) []*ServiceProvider {
	if minVersion == "" {
		return providers
	}

	compatible := make([]*ServiceProvider, 0, len(providers))
	for _, p := range providers {
		// Simple string comparison - TODO: use semver
		if p.Version >= minVersion {
			compatible = append(compatible, p)
		}
	}
	return compatible
}

// filterAvailable filters providers by health state.
// Only returns providers that are Healthy or Degraded (not Unhealthy).
func (r *ServiceRegistry) filterAvailable(providers []*ServiceProvider) []*ServiceProvider {
	if r.lifecycleServer == nil {
		// No health tracking - all providers available
		return providers
	}

	available := make([]*ServiceProvider, 0, len(providers))
	for _, p := range providers {
		if r.lifecycleServer.ShouldRouteTraffic(p.RuntimeID) {
			available = append(available, p)
		}
	}
	return available
}

// applyStrategy applies the selection strategy to choose a provider.
// Caller must hold lock.
func (r *ServiceRegistry) applyStrategy(serviceType string, providers []*ServiceProvider, strategy SelectionStrategy) *ServiceProvider {
	if len(providers) == 0 {
		return nil
	}

	switch strategy {
	case SelectionFirst:
		return providers[0]

	case SelectionRoundRobin:
		idx := r.roundRobinIndex[serviceType]
		provider := providers[idx%len(providers)]
		r.roundRobinIndex[serviceType] = (idx + 1) % len(providers)
		return provider

	case SelectionRandom:
		return providers[rand.Intn(len(providers))]

	case SelectionWeighted:
		// TODO: Implement weighted selection based on load/health metrics
		return providers[0]

	default:
		return providers[0]
	}
}

// HasService checks if a service type is available with the given minimum version.
func (r *ServiceRegistry) HasService(serviceType string, minVersion string) bool {
	_, err := r.SelectProvider(serviceType, minVersion)
	return err == nil
}

// GetProvider returns a provider by registration ID.
func (r *ServiceRegistry) GetProvider(registrationID string) (*ServiceProvider, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()

	provider, ok := r.registrations[registrationID]
	if !ok {
		return nil, fmt.Errorf("provider not found: %s", registrationID)
	}

	return provider, nil
}

// GetProviderByRuntimeID returns a provider by runtime ID.
// Returns the first provider if multiple services from same plugin.
func (r *ServiceRegistry) GetProviderByRuntimeID(runtimeID string) (*ServiceProvider, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()

	for _, provider := range r.registrations {
		if provider.RuntimeID == runtimeID {
			return provider, nil
		}
	}

	return nil, fmt.Errorf("no provider found for runtime ID: %s", runtimeID)
}

// GetServicesBy returns all services provided by a plugin.
func (r *ServiceRegistry) GetServicesBy(runtimeID string) []*ServiceProvider {
	r.mu.RLock()
	defer r.mu.RUnlock()

	services := make([]*ServiceProvider, 0)
	for _, provider := range r.registrations {
		if provider.RuntimeID == runtimeID {
			services = append(services, provider)
		}
	}
	return services
}

// GetAllProviders returns all providers for a given service type.
// Does not filter by version or health.
func (r *ServiceRegistry) GetAllProviders(serviceType string) []*ServiceProvider {
	r.mu.RLock()
	defer r.mu.RUnlock()

	providers, ok := r.providers[serviceType]
	if !ok {
		return nil
	}

	result := make([]*ServiceProvider, len(providers))
	copy(result, providers)
	return result
}

// DiscoverService implements the discovery RPC (part of KOR-sbgi).
// Returns a single host-selected endpoint.
func (r *ServiceRegistry) DiscoverService(
	ctx context.Context,
	req *connect.Request[connectpluginv1.DiscoverServiceRequest],
) (*connect.Response[connectpluginv1.DiscoverServiceResponse], error) {
	// Select provider using host strategy
	provider, err := r.SelectProvider(req.Msg.ServiceType, req.Msg.MinVersion)
	if err != nil {
		return nil, connect.NewError(connect.CodeNotFound, err)
	}

	// Build endpoint
	endpoint := &connectpluginv1.ServiceEndpoint{
		ProviderId:  provider.RuntimeID,
		Version:     provider.Version,
		EndpointUrl: fmt.Sprintf("/services/%s/%s", provider.ServiceType, provider.RuntimeID),
		Metadata:    provider.Metadata,
	}

	// Check if this is the only provider
	r.mu.RLock()
	singleProvider := len(r.providers[req.Msg.ServiceType]) == 1
	r.mu.RUnlock()

	return connect.NewResponse(&connectpluginv1.DiscoverServiceResponse{
		Endpoint:       endpoint,
		SingleProvider: singleProvider,
	}), nil
}

// WatchService implements the watch RPC.
// Streams service availability updates when providers register/unregister.
func (r *ServiceRegistry) WatchService(
	ctx context.Context,
	req *connect.Request[connectpluginv1.WatchServiceRequest],
	stream *connect.ServerStream[connectpluginv1.WatchServiceEvent],
) error {
	serviceType := req.Msg.ServiceType

	r.mu.Lock()

	// Create watcher
	wctx, cancel := context.WithCancel(ctx)
	watcher := &serviceWatcher{
		ch:     make(chan *connectpluginv1.WatchServiceEvent, 10),
		ctx:    wctx,
		cancel: cancel,
	}

	// Register watcher
	r.watchers[serviceType] = append(r.watchers[serviceType], watcher)

	// Send initial state
	initialEvent := r.buildServiceEventLocked(serviceType)
	watcher.ch <- initialEvent

	r.mu.Unlock()

	// Cleanup on exit
	defer func() {
		r.mu.Lock()
		watchers := r.watchers[serviceType]
		for i, w := range watchers {
			if w == watcher {
				r.watchers[serviceType] = append(watchers[:i], watchers[i+1:]...)
				break
			}
		}
		r.mu.Unlock()
		cancel()
		close(watcher.ch)
	}()

	// Stream events
	for {
		select {
		case <-ctx.Done():
			return nil

		case event, ok := <-watcher.ch:
			if !ok {
				return nil
			}

			if err := stream.Send(event); err != nil {
				return err
			}
		}
	}
}

// notifyWatchersLocked notifies all watchers of a service type about state changes.
// Caller must hold lock.
func (r *ServiceRegistry) notifyWatchersLocked(serviceType string) {
	event := r.buildServiceEventLocked(serviceType)

	for _, watcher := range r.watchers[serviceType] {
		select {
		case watcher.ch <- event:
		default:
			// Watcher not reading, skip
		}
	}
}

// buildServiceEventLocked builds a WatchServiceEvent for the current state of a service.
// Caller must hold lock.
func (r *ServiceRegistry) buildServiceEventLocked(serviceType string) *connectpluginv1.WatchServiceEvent {
	// Try to select a provider
	providers := r.providers[serviceType]
	if len(providers) == 0 {
		// No providers - service unavailable
		return &connectpluginv1.WatchServiceEvent{
			ServiceType: serviceType,
			State:       connectpluginv1.ServiceState_SERVICE_STATE_UNAVAILABLE,
		}
	}

	// Filter by health
	available := r.filterAvailable(providers)
	if len(available) == 0 {
		// All providers unhealthy
		return &connectpluginv1.WatchServiceEvent{
			ServiceType: serviceType,
			State:       connectpluginv1.ServiceState_SERVICE_STATE_UNAVAILABLE,
		}
	}

	// Get first available provider
	provider := available[0]

	// Check if provider is degraded
	state := connectpluginv1.ServiceState_SERVICE_STATE_AVAILABLE
	if r.lifecycleServer != nil {
		healthState := r.lifecycleServer.GetHealthState(provider.RuntimeID)
		if healthState != nil && healthState.State == connectpluginv1.HealthState_HEALTH_STATE_DEGRADED {
			state = connectpluginv1.ServiceState_SERVICE_STATE_DEGRADED
		}
	}

	// Build endpoint
	endpoint := &connectpluginv1.ServiceEndpoint{
		ProviderId:  provider.RuntimeID,
		Version:     provider.Version,
		EndpointUrl: fmt.Sprintf("/services/%s/%s", provider.ServiceType, provider.RuntimeID),
		Metadata:    provider.Metadata,
	}

	return &connectpluginv1.WatchServiceEvent{
		ServiceType: serviceType,
		State:       state,
		Endpoint:    endpoint,
	}
}

// ServiceRegistryHandler returns the path and handler for the registry service.
func ServiceRegistryHandler(server *ServiceRegistry) (string, http.Handler) {
	return connectpluginv1connect.NewServiceRegistryHandler(server)
}

// generateRegistrationID generates a unique registration ID.
func generateRegistrationID() string {
	return "reg-" + generateRandomHex(16)
}
