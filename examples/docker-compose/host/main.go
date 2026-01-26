// Package main implements the host platform for the docker-compose example.
// Provides Service Registry services: handshake, lifecycle, registry, router.
package main

import (
	"log"
	"net/http"
	"os"

	connectplugin "github.com/masegraye/connect-plugin-go"
	"github.com/masegraye/connect-plugin-go/gen/plugin/v1/connectpluginv1connect"
)

func main() {
	port := os.Getenv("PORT")
	if port == "" {
		port = "8080"
	}

	handshake := connectplugin.NewHandshakeServer(&connectplugin.ServeConfig{})
	lifecycle := connectplugin.NewLifecycleServer()
	registry := connectplugin.NewServiceRegistry(lifecycle)
	router := connectplugin.NewServiceRouter(handshake, registry, lifecycle)

	mux := http.NewServeMux()

	handshakePath, handshakeHandler := connectpluginv1connect.NewHandshakeServiceHandler(handshake)
	mux.Handle(handshakePath, handshakeHandler)

	lifecyclePath, lifecycleHandler := connectpluginv1connect.NewPluginLifecycleHandler(lifecycle)
	mux.Handle(lifecyclePath, lifecycleHandler)

	registryPath, registryHandler := connectpluginv1connect.NewServiceRegistryHandler(registry)
	mux.Handle(registryPath, registryHandler)

	mux.Handle("/services/", router)

	mux.HandleFunc("/health", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		w.Write([]byte("OK"))
	})

	addr := ":" + port
	log.Printf("Host platform listening on %s", addr)
	log.Println("Services:")
	log.Println("  - Handshake: /connectplugin.v1.HandshakeService/")
	log.Println("  - Lifecycle: /connectplugin.v1.PluginLifecycle/")
	log.Println("  - Registry:  /connectplugin.v1.ServiceRegistry/")
	log.Println("  - Router:    /services/{type}/{provider-id}/{method}")

	if err := http.ListenAndServe(addr, mux); err != nil {
		log.Fatalf("Server error: %v", err)
	}
}
