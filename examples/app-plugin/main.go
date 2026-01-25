// Package main implements an app plugin that depends on the cache service.
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
		port = "8083"
	}
	hostURL := os.Getenv("HOST_URL")
	if hostURL == "" {
		hostURL = "http://localhost:8080"
	}

	// Create plugin client
	client, err := connectplugin.NewClient(connectplugin.ClientConfig{
		HostURL:     hostURL,
		SelfID:      "app-plugin",
		SelfVersion: "1.0.0",
		Metadata: connectplugin.PluginMetadata{
			Name:    "App Plugin",
			Version: "1.0.0",
			Requires: []connectplugin.ServiceDependency{
				{
					Type:               "cache",
					MinVersion:         "1.0.0",
					RequiredForStartup: true,
					WatchForChanges:    true,
				},
			},
		},
	})
	if err != nil {
		log.Fatalf("Failed to create client: %v", err)
	}

	// Connect to host
	ctx := context.Background()
	if err := client.Connect(ctx); err != nil {
		log.Fatalf("Failed to connect: %v", err)
	}

	log.Printf("App plugin started with runtime_id: %s", client.RuntimeID())

	// Start HTTP server
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

	// Simple app endpoint
	mux.HandleFunc("/app.v1.App/Process", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`{"result": "processed"}`))
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
		log.Println("Shutting down app plugin")
		server.Shutdown(context.Background())
	}()

	log.Printf("App plugin listening on :%s", port)
	if err := server.ListenAndServe(); err != http.ErrServerClosed {
		log.Fatalf("Server error: %v", err)
	}
}

// registerServices checks cache dependency and reports health.
func registerServices(ctx context.Context, client *connectplugin.Client) {
	regClient := client.RegistryClient()
	if regClient == nil {
		log.Println("Registry client not available yet, skipping registration")
		return
	}

	// App plugin doesn't provide services, only checks dependencies
	time.Sleep(300 * time.Millisecond)
	discReq := connect.NewRequest(&connectpluginv1.DiscoverServiceRequest{
		ServiceType: "cache",
		MinVersion:  "1.0.0",
	})
	discReq.Header().Set("X-Plugin-Runtime-ID", client.RuntimeID())
	discReq.Header().Set("Authorization", "Bearer "+client.RuntimeToken())

	_, err := regClient.DiscoverService(ctx, discReq)
	if err != nil {
		log.Printf("Cache not available yet, reporting degraded: %v", err)
		client.ReportHealth(ctx, connectpluginv1.HealthState_HEALTH_STATE_DEGRADED,
			"cache dependency not available", []string{"cache"})
	} else {
		log.Println("Cache discovered, reporting healthy")
		client.ReportHealth(ctx, connectpluginv1.HealthState_HEALTH_STATE_HEALTHY, "", nil)
	}
}

type pluginControlHandler struct {
	client *connectplugin.Client
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

	// Store runtime identity (Model A)
	h.client.SetRuntimeIdentity(req.Msg.RuntimeId, req.Msg.RuntimeToken, req.Msg.HostUrl)

	// Model A: Register services after receiving identity
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
		Reason: "all dependencies available",
	}), nil
}

func (h *pluginControlHandler) Shutdown(
	ctx context.Context,
	req *connect.Request[connectpluginv1.ShutdownRequest],
) (*connect.Response[connectpluginv1.ShutdownResponse], error) {
	log.Printf("Received shutdown request (grace: %ds, reason: %s)",
		req.Msg.GracePeriodSeconds, req.Msg.Reason)

	go func() {
		time.Sleep(100 * time.Millisecond)
		os.Exit(0)
	}()

	return connect.NewResponse(&connectpluginv1.ShutdownResponse{
		Acknowledged: true,
	}), nil
}
