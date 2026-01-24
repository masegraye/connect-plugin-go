// Package main implements a simple logger plugin for integration testing.
package main

import (
	"context"
	"log"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"connectrpc.com/connect"
	connectplugin "github.com/masegraye/connect-plugin-go"
	connectpluginv1 "github.com/masegraye/connect-plugin-go/gen/plugin/v1"
	"github.com/masegraye/connect-plugin-go/gen/plugin/v1/connectpluginv1connect"
)

func main() {
	port := os.Getenv("PORT")
	if port == "" {
		port = "8081"
	}
	hostURL := os.Getenv("HOST_URL")

	// Deployment model detection:
	// - If HOST_URL is set → Model B (self-registering, plugin initiates handshake)
	// - If HOST_URL is empty → Model A (platform-managed, wait for host to call us)
	modelB := hostURL != ""
	if !modelB {
		hostURL = "http://localhost:8080" // Default for when Model A calls SetRuntimeIdentity
	}

	// Create plugin client
	client, err := connectplugin.NewClient(connectplugin.ClientConfig{
		HostURL:     hostURL,
		SelfID:      "logger-plugin",
		SelfVersion: "1.0.0",
		Metadata: connectplugin.PluginMetadata{
			Name:    "Logger Plugin",
			Version: "1.0.0",
			Provides: []connectplugin.ServiceDeclaration{
				{Type: "logger", Version: "1.0.0", Path: "/logger.v1.Logger/"},
			},
		},
	})
	if err != nil {
		log.Fatalf("Failed to create client: %v", err)
	}

	ctx := context.Background()

	// Model B: Connect to host immediately
	if modelB {
		if err := client.Connect(ctx); err != nil {
			log.Fatalf("Failed to connect: %v", err)
		}
		log.Printf("Logger plugin started (Model B) with runtime_id: %s", client.RuntimeID())

		// Register services immediately
		registerServices(ctx, client)
	} else {
		log.Printf("Logger plugin started (Model A) - waiting for host to assign identity")
		// Model A: Wait for host to call SetRuntimeIdentity, then register
		// The identity handler will call registerServices via callback
	}

	// Start HTTP server for plugin services
	mux := http.NewServeMux()

	// Implement PluginControl service
	controlHandler := &pluginControlHandler{client: client}
	path, handler := connectpluginv1connect.NewPluginControlHandler(controlHandler)
	mux.Handle(path, handler)

	// Implement PluginIdentity service (for Model A)
	identityHandler := &pluginIdentityHandler{
		client:   client,
		metadata: client.Config().Metadata,
	}
	identityPath, identityH := connectpluginv1connect.NewPluginIdentityHandler(identityHandler)
	mux.Handle(identityPath, identityH)

	// Simple logger service endpoint (dummy implementation)
	mux.HandleFunc("/logger.v1.Logger/Log", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`{"success": true}`))
	})

	server := &http.Server{
		Addr:    ":" + port,
		Handler: mux,
	}


	// Handle shutdown
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, os.Interrupt, syscall.SIGTERM)
	go func() {
		<-sigCh
		log.Println("Shutting down logger plugin")
		server.Shutdown(context.Background())
	}()

	log.Printf("Logger plugin listening on :%s", port)
	if err := server.ListenAndServe(); err != http.ErrServerClosed {
		log.Fatalf("Server error: %v", err)
	}
}

// pluginControlHandler implements the PluginControl service.
type pluginControlHandler struct {
	client *connectplugin.Client
}

// registerServices registers all services with the host registry.
func registerServices(ctx context.Context, client *connectplugin.Client) {
	regClient := client.RegistryClient()
	if regClient == nil {
		log.Println("Registry client not available yet, skipping registration")
		return
	}

	for _, svc := range client.Config().Metadata.Provides {
		regReq := connect.NewRequest(&connectpluginv1.RegisterServiceRequest{
			ServiceType:  svc.Type,
			Version:      svc.Version,
			EndpointPath: svc.Path,
		})
		regReq.Header().Set("X-Plugin-Runtime-ID", client.RuntimeID())
		regReq.Header().Set("Authorization", "Bearer "+client.RuntimeToken())

		if _, err := regClient.RegisterService(ctx, regReq); err != nil {
			log.Fatalf("Failed to register service %s: %v", svc.Type, err)
		}
		log.Printf("Registered service: %s v%s", svc.Type, svc.Version)
	}

	// Report healthy after registration
	time.Sleep(100 * time.Millisecond)
	if err := client.ReportHealth(ctx, connectpluginv1.HealthState_HEALTH_STATE_HEALTHY, "", nil); err != nil {
		log.Printf("Failed to report health: %v", err)
	}
}

type pluginIdentityHandler struct {
	client   *connectplugin.Client
	metadata connectplugin.PluginMetadata
}

func (h *pluginIdentityHandler) GetPluginInfo(
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

	requires := make([]*connectpluginv1.ServiceDependency, len(cfg.Metadata.Requires))
	for i, dep := range cfg.Metadata.Requires {
		requires[i] = &connectpluginv1.ServiceDependency{
			Type:               dep.Type,
			MinVersion:         dep.MinVersion,
			RequiredForStartup: dep.RequiredForStartup,
			WatchForChanges:    dep.WatchForChanges,
		}
	}

	return connect.NewResponse(&connectpluginv1.GetPluginInfoResponse{
		SelfId:      cfg.SelfID,
		SelfVersion: cfg.SelfVersion,
		Provides:    provides,
		Requires:    requires,
		Metadata: map[string]string{
			"name":    cfg.Metadata.Name,
			"version": cfg.Metadata.Version,
		},
	}), nil
}

func (h *pluginIdentityHandler) SetRuntimeIdentity(
	ctx context.Context,
	req *connect.Request[connectpluginv1.SetRuntimeIdentityRequest],
) (*connect.Response[connectpluginv1.SetRuntimeIdentityResponse], error) {
	log.Printf("Received runtime identity: %s (token: %s...)",
		req.Msg.RuntimeId, req.Msg.RuntimeToken[:8])

	// Store the runtime identity (Model A)
	h.client.SetRuntimeIdentity(req.Msg.RuntimeId, req.Msg.RuntimeToken, req.Msg.HostUrl)

	// Model A: Now that we have runtime identity, register services
	go func() {
		time.Sleep(100 * time.Millisecond)
		registerServices(context.Background(), h.client)
	}()

	return connect.NewResponse(&connectpluginv1.SetRuntimeIdentityResponse{
		Acknowledged: true,
	}), nil
}

func (h *pluginControlHandler) GetHealth(
	ctx context.Context,
	req *connect.Request[connectpluginv1.GetHealthRequest],
) (*connect.Response[connectpluginv1.GetHealthResponse], error) {
	return connect.NewResponse(&connectpluginv1.GetHealthResponse{
		State:  connectpluginv1.HealthState_HEALTH_STATE_HEALTHY,
		Reason: "all systems operational",
	}), nil
}

func (h *pluginControlHandler) Shutdown(
	ctx context.Context,
	req *connect.Request[connectpluginv1.ShutdownRequest],
) (*connect.Response[connectpluginv1.ShutdownResponse], error) {
	log.Printf("Received shutdown request (grace: %ds, reason: %s)",
		req.Msg.GracePeriodSeconds, req.Msg.Reason)

	// Graceful shutdown in goroutine
	go func() {
		time.Sleep(100 * time.Millisecond)
		os.Exit(0)
	}()

	return connect.NewResponse(&connectpluginv1.ShutdownResponse{
		Acknowledged: true,
	}), nil
}
