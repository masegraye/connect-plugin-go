package connectplugin

import (
	"context"
	"fmt"
	"log"
	"net/http"
	"sync"
	"time"
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

	// 1. Create implementation from factory
	impl := spec.ImplFactory()

	// 2. Create Connect handler from plugin
	path, handler, err := spec.Plugin.ConnectServer(impl)
	if err != nil {
		return "", nil, fmt.Errorf("failed to create handler: %w", err)
	}

	// 3. Create HTTP server with plugin handler
	mux := http.NewServeMux()
	mux.Handle(path, handler)

	// TODO: Add PluginControl service for in-memory plugins
	// TODO: Add PluginIdentity service for managed deployment support

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

	// 6. Return endpoint and cleanup function
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
