package connectplugin

import (
	"context"
	"fmt"
	"sync"
)

// DiscoveryService abstracts how plugin endpoints are discovered.
// Implementations include static configuration, Kubernetes service discovery, DNS, etc.
type DiscoveryService interface {
	// Discover returns available endpoints for the given service name.
	// Returns error if service is not found or discovery fails.
	Discover(ctx context.Context, serviceName string) ([]Endpoint, error)

	// Watch returns a channel that receives endpoint updates for the service.
	// The channel is closed when the context is cancelled or discovery ends.
	// Returns error if watch cannot be established.
	Watch(ctx context.Context, serviceName string) (<-chan DiscoveryEvent, error)
}

// Endpoint represents a discovered service endpoint.
type Endpoint struct {
	// URL is the full service URL (e.g., "http://localhost:8080", "https://plugin.example.com")
	URL string

	// Metadata contains additional endpoint information (region, version, etc.)
	Metadata map[string]string

	// Weight for load balancing (0-100, higher = more traffic)
	Weight int
}

// DiscoveryEvent represents a change in service endpoints.
type DiscoveryEvent struct {
	// ServiceName is the service that changed
	ServiceName string

	// Endpoints is the current set of endpoints (replaces previous)
	Endpoints []Endpoint

	// Error if discovery failed
	Error error
}

// StaticDiscovery implements DiscoveryService with static endpoint configuration.
// Endpoints are configured at creation time and never change.
type StaticDiscovery struct {
	mu        sync.RWMutex
	endpoints map[string][]Endpoint
}

// NewStaticDiscovery creates a static discovery service with the given endpoint map.
// Example:
//
//	discovery := NewStaticDiscovery(map[string][]Endpoint{
//	    "plugin-host": {
//	        {URL: "http://localhost:8080", Weight: 100},
//	    },
//	})
func NewStaticDiscovery(endpoints map[string][]Endpoint) *StaticDiscovery {
	return &StaticDiscovery{
		endpoints: endpoints,
	}
}

// Discover returns the static endpoints for the service.
func (s *StaticDiscovery) Discover(ctx context.Context, serviceName string) ([]Endpoint, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	endpoints, ok := s.endpoints[serviceName]
	if !ok {
		return nil, fmt.Errorf("service %q not found in static discovery", serviceName)
	}

	if len(endpoints) == 0 {
		return nil, fmt.Errorf("no endpoints configured for service %q", serviceName)
	}

	// Return a copy to prevent external modification
	result := make([]Endpoint, len(endpoints))
	copy(result, endpoints)
	return result, nil
}

// Watch returns a channel for static endpoints (sends once and closes).
// Static endpoints don't change, so this sends the current state and completes.
func (s *StaticDiscovery) Watch(ctx context.Context, serviceName string) (<-chan DiscoveryEvent, error) {
	endpoints, err := s.Discover(ctx, serviceName)

	ch := make(chan DiscoveryEvent, 1)

	if err != nil {
		// Send error event and close
		ch <- DiscoveryEvent{
			ServiceName: serviceName,
			Error:       err,
		}
		close(ch)
		return ch, nil
	}

	// Send current endpoints and close (static discovery doesn't change)
	ch <- DiscoveryEvent{
		ServiceName: serviceName,
		Endpoints:   endpoints,
	}
	close(ch)

	return ch, nil
}

// AddEndpoint adds an endpoint to a service (for testing/dynamic updates).
func (s *StaticDiscovery) AddEndpoint(serviceName string, endpoint Endpoint) {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.endpoints[serviceName] = append(s.endpoints[serviceName], endpoint)
}

// RemoveService removes all endpoints for a service.
func (s *StaticDiscovery) RemoveService(serviceName string) {
	s.mu.Lock()
	defer s.mu.Unlock()

	delete(s.endpoints, serviceName)
}
