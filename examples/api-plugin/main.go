// Package main implements an API plugin for URL shortening.
// Provides: api service
// Requires: storage service
package main

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
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

	modelB := hostURL != ""
	if !modelB {
		hostURL = "http://localhost:8080"
	}

	client, err := connectplugin.NewClient(connectplugin.ClientConfig{
		HostURL:     hostURL,
		SelfID:      "api-plugin",
		SelfVersion: "1.0.0",
		Metadata: connectplugin.PluginMetadata{
			Name:    "API Plugin",
			Version: "1.0.0",
			Provides: []connectplugin.ServiceDeclaration{
				{Type: "api", Version: "1.0.0", Path: "/api.v1.API/"},
			},
			Requires: []connectplugin.ServiceDependency{
				{Type: "storage", MinVersion: "1.0.0", RequiredForStartup: true, WatchForChanges: true},
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
		log.Printf("API plugin started (Model B) with runtime_id: %s", client.RuntimeID())
		registerServices(ctx, client)
	} else {
		log.Printf("API plugin started (Model A) - waiting for host")
	}

	api := &apiService{client: client}

	mux := http.NewServeMux()

	controlHandler := &pluginControlHandler{client: client}
	controlPath, controlH := connectpluginv1connect.NewPluginControlHandler(controlHandler)
	mux.Handle(controlPath, controlH)

	identityHandler := &pluginIdentityHandler{client: client}
	identityPath, identityH := connectpluginv1connect.NewPluginIdentityHandler(identityHandler)
	mux.Handle(identityPath, identityH)

	mux.HandleFunc("/api.v1.API/Shorten", api.Shorten)
	mux.HandleFunc("/api.v1.API/Resolve", api.Resolve)

	server := &http.Server{
		Addr:    ":" + port,
		Handler: mux,
	}

	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, os.Interrupt, syscall.SIGTERM)
	go func() {
		<-sigCh
		log.Println("Shutting down API plugin")
		server.Shutdown(context.Background())
	}()

	log.Printf("API plugin listening on :%s", port)
	if err := server.ListenAndServe(); err != http.ErrServerClosed {
		log.Fatalf("Server error: %v", err)
	}
}

type apiService struct {
	client *connectplugin.Client
}

func (a *apiService) Shorten(w http.ResponseWriter, r *http.Request) {
	url := r.URL.Query().Get("url")
	if url == "" {
		http.Error(w, "Missing url parameter", http.StatusBadRequest)
		return
	}

	shortCode := generateShortCode()

	if err := a.storeURL(r.Context(), shortCode, url); err != nil {
		http.Error(w, fmt.Sprintf("Storage failed: %v", err), http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	w.Write([]byte(fmt.Sprintf(`{"short_code": "%s", "url": "%s"}`, shortCode, url)))
}

func (a *apiService) Resolve(w http.ResponseWriter, r *http.Request) {
	shortCode := r.URL.Query().Get("code")
	if shortCode == "" {
		http.Error(w, "Missing code parameter", http.StatusBadRequest)
		return
	}

	url, err := a.getURL(r.Context(), shortCode)
	if err != nil {
		http.Error(w, fmt.Sprintf("Not found: %v", err), http.StatusNotFound)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	w.Write([]byte(fmt.Sprintf(`{"url": "%s"}`, url)))
}

func (a *apiService) storeURL(ctx context.Context, shortCode, url string) error {
	regClient := a.client.RegistryClient()
	if regClient == nil {
		return fmt.Errorf("registry client not available")
	}

	discReq := connect.NewRequest(&connectpluginv1.DiscoverServiceRequest{
		ServiceType: "storage",
		MinVersion:  "1.0.0",
	})
	discReq.Header().Set("X-Plugin-Runtime-ID", a.client.RuntimeID())
	discReq.Header().Set("Authorization", "Bearer "+a.client.RuntimeToken())

	discResp, err := regClient.DiscoverService(ctx, discReq)
	if err != nil {
		return fmt.Errorf("storage service not found: %w", err)
	}

	storeURL := fmt.Sprintf("%s%s/Store?code=%s&url=%s",
		a.client.Config().HostURL,
		discResp.Msg.Endpoint.EndpointUrl,
		shortCode, url)

	req, _ := http.NewRequestWithContext(ctx, "POST", storeURL, nil)
	req.Header.Set("X-Plugin-Runtime-ID", a.client.RuntimeID())
	req.Header.Set("Authorization", "Bearer "+a.client.RuntimeToken())

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("storage returned %d", resp.StatusCode)
	}

	return nil
}

func (a *apiService) getURL(ctx context.Context, shortCode string) (string, error) {
	regClient := a.client.RegistryClient()
	if regClient == nil {
		return "", fmt.Errorf("registry client not available")
	}

	discReq := connect.NewRequest(&connectpluginv1.DiscoverServiceRequest{
		ServiceType: "storage",
		MinVersion:  "1.0.0",
	})
	discReq.Header().Set("X-Plugin-Runtime-ID", a.client.RuntimeID())
	discReq.Header().Set("Authorization", "Bearer "+a.client.RuntimeToken())

	discResp, err := regClient.DiscoverService(ctx, discReq)
	if err != nil {
		return "", fmt.Errorf("storage service not found: %w", err)
	}

	getURL := fmt.Sprintf("%s%s/Get?code=%s",
		a.client.Config().HostURL,
		discResp.Msg.Endpoint.EndpointUrl,
		shortCode)

	req, _ := http.NewRequestWithContext(ctx, "GET", getURL, nil)
	req.Header.Set("X-Plugin-Runtime-ID", a.client.RuntimeID())
	req.Header.Set("Authorization", "Bearer "+a.client.RuntimeToken())

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("not found")
	}

	body, _ := io.ReadAll(resp.Body)
	var result struct {
		URL string `json:"url"`
	}
	if err := json.Unmarshal(body, &result); err != nil {
		return "", err
	}

	return result.URL, nil
}

func generateShortCode() string {
	b := make([]byte, 4)
	rand.Read(b)
	return hex.EncodeToString(b)
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
		ServiceType: "storage",
		MinVersion:  "1.0.0",
	})
	discReq.Header().Set("X-Plugin-Runtime-ID", client.RuntimeID())
	discReq.Header().Set("Authorization", "Bearer "+client.RuntimeToken())

	_, err := regClient.DiscoverService(ctx, discReq)
	if err != nil {
		log.Printf("Storage not available yet, reporting degraded: %v", err)
		client.ReportHealth(ctx, connectpluginv1.HealthState_HEALTH_STATE_DEGRADED,
			"storage dependency not available", []string{"storage"})
	} else {
		log.Println("Storage discovered, reporting healthy")
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
