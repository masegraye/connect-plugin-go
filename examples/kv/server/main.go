package main

import (
	"context"
	"fmt"
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
	kvimpl "github.com/masegraye/connect-plugin-go/examples/kv/impl"
	"github.com/masegraye/connect-plugin-go/examples/kv/gen/kvv1connect"
	kvv1plugin "github.com/masegraye/connect-plugin-go/examples/kv/gen/kvv1plugin"
)

func main() {
	port := os.Getenv("PORT")
	if port == "" {
		port = "8080"
	}
	hostURL := os.Getenv("HOST_URL")

	// Deployment model detection:
	// - If HOST_URL set → Unmanaged (self-register with host)
	// - If HOST_URL empty → Managed (wait for host to call us)
	isUnmanaged := hostURL != ""
	if !isUnmanaged {
		hostURL = "http://localhost:8080" // Default
	}

	// Create client for Service Registry integration
	client, err := connectplugin.NewClient(connectplugin.ClientConfig{
		HostURL:     hostURL,
		SelfID:      "kv-server",
		SelfVersion: "1.0.0",
		Metadata: connectplugin.PluginMetadata{
			Name:    "KV Server",
			Version: "1.0.0",
			Provides: []connectplugin.ServiceDeclaration{
				{Type: "kv", Version: "1.0.0", Path: kvv1connect.KVServiceName},
			},
		},
	})
	if err != nil {
		log.Fatalf("Failed to create client: %v", err)
	}

	ctx := context.Background()

	// Unmanaged: Connect to host immediately
	if isUnmanaged {
		if err := client.Connect(ctx); err != nil {
			log.Fatalf("Failed to connect: %v", err)
		}
		log.Printf("KV server started (Unmanaged) with runtime_id: %s", client.RuntimeID())
		registerService(ctx, client, port)
	} else {
		log.Printf("KV server started (Managed) - waiting for host")
	}

	// Create KV implementation
	store := kvimpl.NewStore()

	// Create HTTP server
	mux := http.NewServeMux()

	// Add KV service
	kvPath, kvHandler, _ := (&kvv1plugin.KVServicePlugin{}).ConnectServer(store)
	mux.Handle(kvPath, kvHandler)

	// Add PluginControl service
	controlHandler := &pluginControlHandler{client: client}
	controlPath, controlH := connectpluginv1connect.NewPluginControlHandler(controlHandler)
	mux.Handle(controlPath, controlH)

	// Add PluginIdentity service (for Managed)
	identityHandler := &pluginIdentityHandler{client: client}
	identityPath, identityH := connectpluginv1connect.NewPluginIdentityHandler(identityHandler)
	mux.Handle(identityPath, identityH)

	server := &http.Server{
		Addr:    ":" + port,
		Handler: mux,
	}

	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, os.Interrupt, syscall.SIGTERM)
	go func() {
		<-sigCh
		log.Println("Shutting down KV server")
		server.Shutdown(context.Background())
	}()

	log.Printf("KV server listening on :%s", port)
	if err := server.ListenAndServe(); err != http.ErrServerClosed {
		log.Fatalf("Server error: %v", err)
	}
}

func registerService(ctx context.Context, client *connectplugin.Client, port string) {
	regClient := client.RegistryClient()
	if regClient == nil {
		return
	}

	myHost := os.Getenv("HOSTNAME")
	if myHost == "" {
		myHost = "localhost"
	}
	myBaseURL := fmt.Sprintf("http://%s:%s", myHost, port)

	regReq := connect.NewRequest(&connectpluginv1.RegisterServiceRequest{
		ServiceType:  "kv",
		Version:      "1.0.0",
		EndpointPath: kvv1connect.KVServiceName,
		Metadata: map[string]string{
			"base_url": myBaseURL,
		},
	})
	regReq.Header().Set("X-Plugin-Runtime-ID", client.RuntimeID())
	regReq.Header().Set("Authorization", "Bearer "+client.RuntimeToken())

	if _, err := regClient.RegisterService(ctx, regReq); err != nil {
		log.Fatalf("Failed to register service: %v", err)
	}
	log.Printf("Registered service: kv v1.0.0 at %s", myBaseURL)

	// Report healthy
	client.ReportHealth(ctx, connectpluginv1.HealthState_HEALTH_STATE_HEALTHY, "", nil)
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
		Reason: "operational",
	}), nil
}

func (h *pluginControlHandler) Shutdown(
	ctx context.Context,
	req *connect.Request[connectpluginv1.ShutdownRequest],
) (*connect.Response[connectpluginv1.ShutdownResponse], error) {
	log.Printf("Shutdown requested (grace: %ds)", req.Msg.GracePeriodSeconds)
	go func() {
		time.Sleep(100 * time.Millisecond)
		os.Exit(0)
	}()
	return connect.NewResponse(&connectpluginv1.ShutdownResponse{Acknowledged: true}), nil
}

type pluginIdentityHandler struct {
	client *connectplugin.Client
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

func (h *pluginIdentityHandler) SetRuntimeIdentity(
	ctx context.Context,
	req *connect.Request[connectpluginv1.SetRuntimeIdentityRequest],
) (*connect.Response[connectpluginv1.SetRuntimeIdentityResponse], error) {
	h.client.SetRuntimeIdentity(req.Msg.RuntimeId, req.Msg.RuntimeToken, req.Msg.HostUrl)
	go func() {
		time.Sleep(100 * time.Millisecond)
		port := os.Getenv("PORT")
		if port == "" {
			port = "8080"
		}
		registerService(context.Background(), h.client, port)
	}()
	return connect.NewResponse(&connectpluginv1.SetRuntimeIdentityResponse{Acknowledged: true}), nil
}
