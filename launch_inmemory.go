package connectplugin

import (
	"context"
	"fmt"
	"log"
	"net/http"
	"sync"
	"time"

	"connectrpc.com/connect"
	connectpluginv1 "github.com/masegraye/connect-plugin-go/gen/plugin/v1"
	"github.com/masegraye/connect-plugin-go/gen/plugin/v1/connectpluginv1connect"
)

// InMemoryStrategy launches plugins as in-process HTTP servers (goroutines).
// Plugins run in the same process and communicate via localhost loopback.
type InMemoryStrategy struct {
	mu      sync.Mutex
	servers map[string]*http.Server
}

// NewInMemoryStrategy creates a new in-memory launch strategy.
func NewInMemoryStrategy() *InMemoryStrategy {
	return &InMemoryStrategy{
		servers: make(map[string]*http.Server),
	}
}

// Name returns the strategy name.
func (s *InMemoryStrategy) Name() string {
	return "in-memory"
}

// Launch starts a plugin server in-process as a goroutine.
func (s *InMemoryStrategy) Launch(ctx context.Context, spec PluginSpec) (string, func(), error) {
	if spec.Plugin == nil {
		return "", nil, fmt.Errorf("Plugin required for in-memory strategy")
	}
	if spec.ImplFactory == nil {
		return "", nil, fmt.Errorf("ImplFactory required for in-memory strategy")
	}

	// 1. Create client for self-registration
	hostURL := spec.HostURL
	if hostURL == "" {
		hostURL = "http://localhost:8080"
	}

	// Convert spec.Provides to ServiceDeclaration
	provides := make([]ServiceDeclaration, len(spec.Provides))
	for i, svcType := range spec.Provides {
		provides[i] = ServiceDeclaration{
			Type:    svcType,
			Version: "1.0.0",
			Path:    "", // Will be filled from plugin metadata
		}
	}

	client, err := NewClient(ClientConfig{
		HostURL:     hostURL,
		SelfID:      spec.Name,
		SelfVersion: "1.0.0",
		Metadata: PluginMetadata{
			Name:     spec.Name,
			Version:  "1.0.0",
			Provides: provides,
		},
	})
	if err != nil {
		return "", nil, fmt.Errorf("failed to create client: %w", err)
	}

	// Connect to host (unmanaged: self-register)
	if err := client.Connect(ctx); err != nil {
		return "", nil, fmt.Errorf("failed to connect to host: %w", err)
	}

	// 2. Create implementation from factory
	impl := spec.ImplFactory()

	// 3. Create Connect handler from plugin
	path, handler, err := spec.Plugin.ConnectServer(impl)
	if err != nil {
		return "", nil, fmt.Errorf("failed to create handler: %w", err)
	}

	// Update provides with actual path from plugin
	metadata := spec.Plugin.Metadata()
	if len(metadata.Provides) > 0 {
		provides[0].Path = metadata.Provides[0].Path
	}

	// 4. Create HTTP server with all required services
	mux := http.NewServeMux()
	mux.Handle(path, handler)

	// Add PluginControl service (for health checks and shutdown)
	controlHandler := newInMemoryControlHandler(client)
	controlPath, controlH := connectpluginv1connect.NewPluginControlHandler(controlHandler)
	mux.Handle(controlPath, controlH)

	// Add PluginIdentity service (for managed deployment support)
	identityHandler := newInMemoryIdentityHandler(client)
	identityPath, identityH := connectpluginv1connect.NewPluginIdentityHandler(identityHandler)
	mux.Handle(identityPath, identityH)

	server := &http.Server{
		Addr:    fmt.Sprintf("localhost:%d", spec.Port),
		Handler: mux,
	}

	// 4. Start server in goroutine
	go func() {
		if err := server.ListenAndServe(); err != http.ErrServerClosed {
			log.Printf("[InMemory] Plugin %s server error: %v", spec.Name, err)
		}
	}()

	// 5. Wait for server to be ready
	endpoint := fmt.Sprintf("http://localhost:%d", spec.Port)
	if err := waitForPluginReady(endpoint, 2*time.Second); err != nil {
		server.Shutdown(context.Background())
		return "", nil, fmt.Errorf("server didn't become ready: %w", err)
	}

	s.mu.Lock()
	s.servers[spec.Name] = server
	s.mu.Unlock()

	// 6. Self-register with Service Registry
	go func() {
		time.Sleep(100 * time.Millisecond) // Wait for server to be fully ready
		registerInMemoryService(ctx, client, spec, endpoint)
	}()

	// 7. Return endpoint and cleanup function
	cleanup := func() {
		s.mu.Lock()
		defer s.mu.Unlock()

		if server != nil {
			server.Shutdown(context.Background())
		}
		delete(s.servers, spec.Name)
	}

	return endpoint, cleanup, nil
}

// registerInMemoryService registers the in-memory plugin with the Service Registry.
func registerInMemoryService(ctx context.Context, client *Client, spec PluginSpec, baseURL string) {
	regClient := client.RegistryClient()
	if regClient == nil {
		log.Printf("[InMemory] Warning: Registry client not available for %s", spec.Name)
		return
	}

	// Get service path from plugin metadata
	metadata := spec.Plugin.Metadata()
	servicePath := ""
	if len(metadata.Provides) > 0 {
		servicePath = metadata.Provides[0].Path
	}

	// Register each service this plugin provides
	for _, svcType := range spec.Provides {
		regReq := connect.NewRequest(&connectpluginv1.RegisterServiceRequest{
			ServiceType:  svcType,
			Version:      "1.0.0",
			EndpointPath: servicePath,
			Metadata: map[string]string{
				"base_url": baseURL,
			},
		})
		regReq.Header().Set("X-Plugin-Runtime-ID", client.RuntimeID())
		regReq.Header().Set("Authorization", "Bearer "+client.RuntimeToken())

		if _, err := regClient.RegisterService(ctx, regReq); err != nil {
			log.Printf("[InMemory] Failed to register service %s: %v", svcType, err)
			continue
		}
		log.Printf("[InMemory] Registered service: %s v1.0.0 at %s", svcType, baseURL)
	}

	// Report healthy
	if err := client.ReportHealth(ctx, connectpluginv1.HealthState_HEALTH_STATE_HEALTHY, "", nil); err != nil {
		log.Printf("[InMemory] Failed to report health: %v", err)
	}
}

// Helper functions for creating handlers

type inMemoryControlHandler struct {
	client *Client
}

func newInMemoryControlHandler(client *Client) *inMemoryControlHandler {
	return &inMemoryControlHandler{client: client}
}

func (h *inMemoryControlHandler) GetHealth(
	ctx context.Context,
	req *connect.Request[connectpluginv1.GetHealthRequest],
) (*connect.Response[connectpluginv1.GetHealthResponse], error) {
	return connect.NewResponse(&connectpluginv1.GetHealthResponse{
		State:  connectpluginv1.HealthState_HEALTH_STATE_HEALTHY,
		Reason: "operational",
	}), nil
}

func (h *inMemoryControlHandler) Shutdown(
	ctx context.Context,
	req *connect.Request[connectpluginv1.ShutdownRequest],
) (*connect.Response[connectpluginv1.ShutdownResponse], error) {
	log.Printf("[InMemory] Shutdown requested (grace: %ds)", req.Msg.GracePeriodSeconds)
	// In-memory plugins can't really exit, just acknowledge
	return connect.NewResponse(&connectpluginv1.ShutdownResponse{
		Acknowledged: true,
	}), nil
}

type inMemoryIdentityHandler struct {
	client *Client
}

func newInMemoryIdentityHandler(client *Client) *inMemoryIdentityHandler {
	return &inMemoryIdentityHandler{client: client}
}

func (h *inMemoryIdentityHandler) GetPluginInfo(
	ctx context.Context,
	req *connect.Request[connectpluginv1.GetPluginInfoRequest],
) (*connect.Response[connectpluginv1.GetPluginInfoResponse], error) {
	cfg := h.client.Config()

	provides := make([]*connectpluginv1.ServiceDeclaration, len(cfg.Metadata.Provides))
	for i, svc := range cfg.Metadata.Provides {
		provides[i] = &connectpluginv1.ServiceDeclaration{
			Type:    svc.Type,
			Version: svc.Version,
			Path:    svc.Path,
		}
	}

	return connect.NewResponse(&connectpluginv1.GetPluginInfoResponse{
		SelfId:      cfg.SelfID,
		SelfVersion: cfg.SelfVersion,
		Provides:    provides,
		Metadata: map[string]string{
			"name":    cfg.Metadata.Name,
			"version": cfg.Metadata.Version,
		},
	}), nil
}

func (h *inMemoryIdentityHandler) SetRuntimeIdentity(
	ctx context.Context,
	req *connect.Request[connectpluginv1.SetRuntimeIdentityRequest],
) (*connect.Response[connectpluginv1.SetRuntimeIdentityResponse], error) {
	h.client.SetRuntimeIdentity(req.Msg.RuntimeId, req.Msg.RuntimeToken, req.Msg.HostUrl)
	// For in-memory, trigger registration if in managed mode
	// TODO: Call registerInMemoryService here for managed deployment
	return connect.NewResponse(&connectpluginv1.SetRuntimeIdentityResponse{
		Acknowledged: true,
	}), nil
}
