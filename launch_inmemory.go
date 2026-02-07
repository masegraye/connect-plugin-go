package connectplugin

import (
	"context"
	"fmt"
	"log"
	"net/http"
	"sync"

	"connectrpc.com/connect"
	connectpluginv1 "github.com/masegraye/connect-plugin-go/gen/plugin/v1"
	"github.com/masegraye/connect-plugin-go/gen/plugin/v1/connectpluginv1connect"
	"github.com/masegraye/connect-plugin-go/internal/memtransport"
)

// InMemoryStrategy launches plugins using in-memory transport (net.Pipe).
// No TCP listener, no port allocation, no network I/O.
// Plugins communicate via ConnectRPC over an in-process net.Pipe transport.
//
// This is the recommended strategy for plugins that run in the same process.
// It preserves full ConnectRPC protocol compatibility (Connect, gRPC, gRPC-Web,
// unary, streaming) while eliminating all network overhead.
type InMemoryStrategy struct {
	mu        sync.Mutex
	listeners map[string]*memtransport.Listener

	// registry is the host's service registry for direct registration.
	// When set, plugins register directly via Go function call (no HTTP to host).
	registry *ServiceRegistry
}

// NewInMemoryStrategy creates a new in-memory launch strategy.
// The registry parameter allows plugins to self-register without HTTP.
// Pass nil if registration is handled externally.
func NewInMemoryStrategy(registry *ServiceRegistry) *InMemoryStrategy {
	return &InMemoryStrategy{
		listeners: make(map[string]*memtransport.Listener),
		registry:  registry,
	}
}

// Name returns the strategy name.
func (s *InMemoryStrategy) Name() string {
	return "in-memory"
}

// Launch starts a plugin using in-memory transport.
// The plugin's ConnectRPC handler is served via net.Pipe — no TCP port needed.
func (s *InMemoryStrategy) Launch(ctx context.Context, spec PluginSpec) (LaunchResult, error) {
	if spec.Plugin == nil {
		return LaunchResult{}, fmt.Errorf("Plugin required for in-memory strategy")
	}
	if spec.ImplFactory == nil {
		return LaunchResult{}, fmt.Errorf("ImplFactory required for in-memory strategy")
	}

	// 1. Create implementation from factory
	impl := spec.ImplFactory()

	// 2. Create Connect handler from plugin
	path, handler, err := spec.Plugin.ConnectServer(impl)
	if err != nil {
		return LaunchResult{}, fmt.Errorf("failed to create handler: %w", err)
	}

	// 3. Build HTTP mux with plugin handler + control services
	mux := http.NewServeMux()
	mux.Handle(path, handler)

	// Add PluginControl service (for health checks)
	controlPath, controlH := connectpluginv1connect.NewPluginControlHandler(
		&inMemoryControlHandler{},
	)
	mux.Handle(controlPath, controlH)

	// 4. Create in-memory listener and start server
	ln := memtransport.New()

	server := &http.Server{Handler: mux}
	go func() {
		if err := server.Serve(ln); err != http.ErrServerClosed {
			log.Printf("[InMemory] Plugin %s server error: %v", spec.Name, err)
		}
	}()

	s.mu.Lock()
	s.listeners[spec.Name] = ln
	s.mu.Unlock()

	// 5. Register services directly with registry (no HTTP round-trip)
	if s.registry != nil {
		s.registerService(spec)
	}

	// 6. Build result with in-memory HTTP client
	// Use http:// scheme so ConnectRPC URL parsing works.
	// The actual transport is in-memory — the URL is never dialed over TCP.
	endpoint := fmt.Sprintf("http://in-memory.%s", spec.Name)

	cleanup := func() {
		s.mu.Lock()
		defer s.mu.Unlock()

		server.Shutdown(context.Background())
		ln.Close()
		delete(s.listeners, spec.Name)
	}

	return LaunchResult{
		Endpoint:   endpoint,
		HTTPClient: ln.HTTPClient(),
		Cleanup:    cleanup,
	}, nil
}

// registerService registers plugin services directly with the registry.
func (s *InMemoryStrategy) registerService(spec PluginSpec) {
	metadata := spec.Plugin.Metadata()

	for i, svcType := range spec.Provides {
		servicePath := ""
		if i < len(metadata.Provides) {
			servicePath = metadata.Provides[i].Path
		}

		regID, err := generateRegistrationID()
		if err != nil {
			log.Printf("[InMemory] Failed to generate registration ID: %v", err)
			continue
		}

		provider := &ServiceProvider{
			RegistrationID: regID,
			RuntimeID:      spec.Name,
			ServiceType:    svcType,
			Version:        "1.0.0",
			EndpointPath:   servicePath,
			Metadata:       map[string]string{"transport": "in-memory"},
		}

		s.registry.mu.Lock()
		s.registry.providers[svcType] = append(s.registry.providers[svcType], provider)
		s.registry.registrations[regID] = provider
		s.registry.mu.Unlock()

		log.Printf("[InMemory] Registered service: %s v1.0.0 (in-memory)", svcType)
	}
}

// inMemoryControlHandler is a minimal PluginControl handler for in-memory plugins.
type inMemoryControlHandler struct{}

func (h *inMemoryControlHandler) GetHealth(
	ctx context.Context,
	req *connect.Request[connectpluginv1.GetHealthRequest],
) (*connect.Response[connectpluginv1.GetHealthResponse], error) {
	return connect.NewResponse(&connectpluginv1.GetHealthResponse{
		State:  connectpluginv1.HealthState_HEALTH_STATE_HEALTHY,
		Reason: "in-memory (in-process)",
	}), nil
}

func (h *inMemoryControlHandler) Shutdown(
	ctx context.Context,
	req *connect.Request[connectpluginv1.ShutdownRequest],
) (*connect.Response[connectpluginv1.ShutdownResponse], error) {
	return connect.NewResponse(&connectpluginv1.ShutdownResponse{
		Acknowledged: true,
	}), nil
}
