package main

import (
	"context"
	"fmt"
	"log"
	"os"
	"time"

	"connectrpc.com/connect"
	connectplugin "github.com/masegraye/connect-plugin-go"
	kvimpl "github.com/masegraye/connect-plugin-go/examples/kv/impl"
	"github.com/masegraye/connect-plugin-go/examples/kv/gen/kvv1connect"
	kvplugin "github.com/masegraye/connect-plugin-go/examples/kv/gen/kvv1plugin"
	connectpluginv1 "github.com/masegraye/connect-plugin-go/gen/plugin/v1"
)

func main() {
	port := os.Getenv("PORT")
	if port == "" {
		port = "8080"
	}
	hostURL := os.Getenv("HOST_URL")

	// Deployment model detection:
	// - If HOST_URL set → Unmanaged (self-register with host)
	// - If HOST_URL empty → Standalone (serve with handshake, clients connect directly)
	isUnmanaged := hostURL != ""

	// Create KV store implementation
	store := kvimpl.NewStore()

	if isUnmanaged {
		// Unmanaged mode: Register with host platform
		log.Printf("Starting KV server (Unmanaged) on :%s, registering with %s", port, hostURL)
		runUnmanaged(store, port, hostURL)
	} else {
		// Standalone mode: Serve with full handshake infrastructure
		log.Printf("Starting KV server (Standalone) on :%s", port)
		runStandalone(store, port)
	}
}

func runStandalone(store *kvimpl.Store, port string) {
	// Use connectplugin.Serve() which provides full plugin infrastructure:
	// - Handshake service for client connection
	// - Health service for monitoring
	// - Signal handling for graceful shutdown
	err := connectplugin.Serve(&connectplugin.ServeConfig{
		Addr: ":" + port,
		Plugins: connectplugin.PluginSet{
			"kv": &kvplugin.KVServicePlugin{},
		},
		Impls: map[string]any{
			"kv": store,
		},
		HealthService: connectplugin.NewHealthServer(),
	})
	if err != nil {
		log.Fatalf("Server error: %v", err)
	}
}

func runUnmanaged(store *kvimpl.Store, port, hostURL string) {
	// Create client for host registration
	client, err := connectplugin.NewClient(connectplugin.ClientConfig{
		HostURL:     hostURL,
		SelfID:      "kv-server",
		SelfVersion: "1.0.0",
		Metadata: connectplugin.PluginMetadata{
			Name:    "KV Server",
			Version: "1.0.0",
			Provides: []connectplugin.ServiceDeclaration{
				{Type: "kv", Version: "1.0.0", Path: "/" + kvv1connect.KVServiceName + "/"},
			},
		},
	})
	if err != nil {
		log.Fatalf("Failed to create client: %v", err)
	}

	ctx := context.Background()
	if err := client.Connect(ctx); err != nil {
		log.Fatalf("Failed to connect to host: %v", err)
	}
	log.Printf("KV server connected with runtime_id: %s", client.RuntimeID())

	// Register service after server starts
	go func() {
		time.Sleep(200 * time.Millisecond) // Wait for server to be ready
		registerService(ctx, client, port)
	}()

	// Serve with full handshake infrastructure
	err = connectplugin.Serve(&connectplugin.ServeConfig{
		Addr: ":" + port,
		Plugins: connectplugin.PluginSet{
			"kv": &kvplugin.KVServicePlugin{},
		},
		Impls: map[string]any{
			"kv": store,
		},
		HealthService: connectplugin.NewHealthServer(),
	})
	if err != nil {
		log.Fatalf("Server error: %v", err)
	}
}

func registerService(ctx context.Context, client *connectplugin.Client, port string) {
	regClient := client.RegistryClient()
	if regClient == nil {
		log.Println("Warning: Registry client not available")
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
		EndpointPath: "/" + kvv1connect.KVServiceName + "/",
		Metadata: map[string]string{
			"base_url": myBaseURL,
		},
	})
	regReq.Header().Set("X-Plugin-Runtime-ID", client.RuntimeID())
	regReq.Header().Set("Authorization", "Bearer "+client.RuntimeToken())

	if _, err := regClient.RegisterService(ctx, regReq); err != nil {
		log.Printf("Failed to register service: %v", err)
		return
	}
	log.Printf("Registered service: kv v1.0.0 at %s", myBaseURL)

	// Report healthy
	client.ReportHealth(ctx, connectpluginv1.HealthState_HEALTH_STATE_HEALTHY, "", nil)
}
