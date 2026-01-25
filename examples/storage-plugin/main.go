// Package main implements a storage plugin for URL shortening.
// Provides: storage service
// Requires: logger service
package main

import (
	"context"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/signal"
	"sync"
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

	modelB := hostURL != ""
	if !modelB {
		hostURL = "http://localhost:8080"
	}

	client, err := connectplugin.NewClient(connectplugin.ClientConfig{
		HostURL:     hostURL,
		SelfID:      "storage-plugin",
		SelfVersion: "1.0.0",
		Metadata: connectplugin.PluginMetadata{
			Name:    "Storage Plugin",
			Version: "1.0.0",
			Provides: []connectplugin.ServiceDeclaration{
				{Type: "storage", Version: "1.0.0", Path: "/storage.v1.Storage/"},
			},
			Requires: []connectplugin.ServiceDependency{
				{Type: "logger", MinVersion: "1.0.0", RequiredForStartup: true, WatchForChanges: true},
			},
		},
	})
	if err != nil {
		log.Fatalf("Failed to create client: %v", err)
	}

	ctx := context.Background()

	if modelB {
		if err := client.Connect(ctx); err != nil {
			log.Fatalf("Failed to connect: %v", err)
		}
		log.Printf("Storage plugin started (Model B) with runtime_id: %s", client.RuntimeID())
		registerServices(ctx, client)
	} else {
		log.Printf("Storage plugin started (Model A) - waiting for host")
	}

	storage := &storageService{
		data:   make(map[string]string),
		client: client,
	}

	mux := http.NewServeMux()

	controlHandler := &pluginControlHandler{client: client}
	controlPath, controlH := connectpluginv1connect.NewPluginControlHandler(controlHandler)
	mux.Handle(controlPath, controlH)

	identityHandler := &pluginIdentityHandler{client: client}
	identityPath, identityH := connectpluginv1connect.NewPluginIdentityHandler(identityHandler)
	mux.Handle(identityPath, identityH)

	mux.HandleFunc("/storage.v1.Storage/Store", storage.Store)
	mux.HandleFunc("/storage.v1.Storage/Get", storage.Get)

	server := &http.Server{
		Addr:    ":" + port,
		Handler: mux,
	}

	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, os.Interrupt, syscall.SIGTERM)
	go func() {
		<-sigCh
		log.Println("Shutting down storage plugin")
		server.Shutdown(context.Background())
	}()

	log.Printf("Storage plugin listening on :%s", port)
	if err := server.ListenAndServe(); err != http.ErrServerClosed {
		log.Fatalf("Server error: %v", err)
	}
}

type storageService struct {
	mu     sync.RWMutex
	data   map[string]string
	client *connectplugin.Client
}

func (s *storageService) Store(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	shortCode := r.URL.Query().Get("code")
	url := r.URL.Query().Get("url")

	if shortCode == "" || url == "" {
		http.Error(w, "Missing code or url", http.StatusBadRequest)
		return
	}

	s.mu.Lock()
	s.data[shortCode] = url
	s.mu.Unlock()

	s.logOperation(r.Context(), fmt.Sprintf("STORE %s → %s", shortCode, url))

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	w.Write([]byte(`{"success": true}`))
}

func (s *storageService) Get(w http.ResponseWriter, r *http.Request) {
	shortCode := r.URL.Query().Get("code")
	if shortCode == "" {
		http.Error(w, "Missing code", http.StatusBadRequest)
		return
	}

	s.mu.RLock()
	url, found := s.data[shortCode]
	s.mu.RUnlock()

	if !found {
		http.Error(w, "Not found", http.StatusNotFound)
		return
	}

	s.logOperation(r.Context(), fmt.Sprintf("GET %s → %s", shortCode, url))

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	w.Write([]byte(fmt.Sprintf(`{"url": "%s"}`, url)))
}

func (s *storageService) logOperation(ctx context.Context, message string) {
	regClient := s.client.RegistryClient()
	if regClient == nil {
		return
	}

	discReq := connect.NewRequest(&connectpluginv1.DiscoverServiceRequest{
		ServiceType: "logger",
		MinVersion:  "1.0.0",
	})
	discReq.Header().Set("X-Plugin-Runtime-ID", s.client.RuntimeID())
	discReq.Header().Set("Authorization", "Bearer "+s.client.RuntimeToken())

	discResp, err := regClient.DiscoverService(ctx, discReq)
	if err != nil {
		log.Printf("Logger not available: %v", err)
		return
	}

	logURL := fmt.Sprintf("%s%s/Log?message=%s",
		s.client.Config().HostURL,
		discResp.Msg.Endpoint.EndpointUrl,
		message)

	req, _ := http.NewRequestWithContext(ctx, "POST", logURL, nil)
	req.Header.Set("X-Plugin-Runtime-ID", s.client.RuntimeID())
	req.Header.Set("Authorization", "Bearer "+s.client.RuntimeToken())

	http.DefaultClient.Do(req)
}

func registerServices(ctx context.Context, client *connectplugin.Client) {
	regClient := client.RegistryClient()
	if regClient == nil {
		log.Println("Registry client not available")
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

	time.Sleep(200 * time.Millisecond)
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
	h.client.SetRuntimeIdentity(req.Msg.RuntimeId, req.Msg.RuntimeToken, req.Msg.HostUrl)
	go func() {
		time.Sleep(100 * time.Millisecond)
		registerServices(context.Background(), h.client)
	}()
	return connect.NewResponse(&connectpluginv1.SetRuntimeIdentityResponse{Acknowledged: true}), nil
}
