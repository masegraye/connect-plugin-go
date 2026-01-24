// Package main implements a cache plugin that depends on the logger service.
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
		port = "8082"
	}
	hostURL := os.Getenv("HOST_URL")
	if hostURL == "" {
		hostURL = "http://localhost:8080"
	}

	// Create plugin client
	client, err := connectplugin.NewClient(connectplugin.ClientConfig{
		HostURL:     hostURL,
		SelfID:      "cache-plugin",
		SelfVersion: "1.0.0",
		Metadata: connectplugin.PluginMetadata{
			Name:    "Cache Plugin",
			Version: "1.0.0",
			Provides: []connectplugin.ServiceDeclaration{
				{Type: "cache", Version: "1.0.0", Path: "/cache.v1.Cache/"},
			},
			Requires: []connectplugin.ServiceDependency{
				{
					Type:               "logger",
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

	log.Printf("Cache plugin started with runtime_id: %s", client.RuntimeID())

	// Register services with host
	regClient := client.RegistryClient()
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

	// Start HTTP server
	mux := http.NewServeMux()

	// Implement PluginControl service
	controlHandler := &pluginControlHandler{client: client}
	path, handler := connectpluginv1connect.NewPluginControlHandler(controlHandler)
	mux.Handle(path, handler)

	// Simple cache service endpoint (dummy implementation)
	mux.HandleFunc("/cache.v1.Cache/Get", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`{"value": "cached-data"}`))
	})

	server := &http.Server{
		Addr:    ":" + port,
		Handler: mux,
	}

	// Report healthy after discovering logger
	go func() {
		time.Sleep(200 * time.Millisecond)

		// Try to discover logger service
		regClient := client.RegistryClient()
		discReq := connect.NewRequest(&connectpluginv1.DiscoverServiceRequest{
			ServiceType: "logger",
			MinVersion:  "1.0.0",
		})
		discReq.Header().Set("X-Plugin-Runtime-ID", client.RuntimeID())
		discReq.Header().Set("Authorization", "Bearer "+client.RuntimeToken())

		_, err := regClient.DiscoverService(ctx, discReq)
		if err != nil {
			log.Printf("Logger not available yet, reporting degraded: %v", err)
			client.ReportHealth(ctx, connectpluginv1.HealthState_HEALTH_STATE_DEGRADED,
				"logger dependency not available", []string{"logger"})
		} else {
			log.Println("Logger discovered, reporting healthy")
			client.ReportHealth(ctx, connectpluginv1.HealthState_HEALTH_STATE_HEALTHY, "", nil)
		}
	}()

	// Handle shutdown
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, os.Interrupt, syscall.SIGTERM)
	go func() {
		<-sigCh
		log.Println("Shutting down cache plugin")
		server.Shutdown(context.Background())
	}()

	log.Printf("Cache plugin listening on :%s", port)
	if err := server.ListenAndServe(); err != http.ErrServerClosed {
		log.Fatalf("Server error: %v", err)
	}
}

type pluginControlHandler struct {
	client *connectplugin.Client
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
