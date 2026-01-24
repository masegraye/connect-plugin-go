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
	if hostURL == "" {
		hostURL = "http://localhost:8080"
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

	// Connect to host
	ctx := context.Background()
	if err := client.Connect(ctx); err != nil {
		log.Fatalf("Failed to connect: %v", err)
	}

	log.Printf("Logger plugin started with runtime_id: %s", client.RuntimeID())

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

	// Start HTTP server for plugin services
	mux := http.NewServeMux()

	// Implement PluginControl service
	controlHandler := &pluginControlHandler{client: client}
	path, handler := connectpluginv1connect.NewPluginControlHandler(controlHandler)
	mux.Handle(path, handler)

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

	// Report healthy
	go func() {
		time.Sleep(100 * time.Millisecond)
		if err := client.ReportHealth(ctx, connectpluginv1.HealthState_HEALTH_STATE_HEALTHY, "", nil); err != nil {
			log.Printf("Failed to report health: %v", err)
		}
	}()

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
