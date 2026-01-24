package connectplugin

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"
)

// ServeConfig configures a plugin server.
type ServeConfig struct {
	// ===== Plugins & Implementations =====

	// Plugins defines the plugin types this server provides.
	// Key = plugin name (e.g., "kv", "auth")
	Plugins PluginSet

	// Impls maps plugin names to their implementations.
	// The impl is passed to Plugin.ConnectServer().
	// Key must match a key in Plugins.
	Impls map[string]any

	// ProtocolVersion is the application protocol version this server implements.
	// Used during handshake negotiation.
	// Default: 1
	ProtocolVersion int

	// MagicCookieKey and Value for validation (not security).
	// Must match client's expectation.
	// Default: DefaultMagicCookieKey/Value
	MagicCookieKey   string
	MagicCookieValue string

	// ServerMetadata is custom metadata sent in handshake.
	ServerMetadata map[string]string

	// ===== Server Configuration =====

	// Addr is the address to listen on.
	// Examples: ":8080", "0.0.0.0:8080", "localhost:8080"
	// Default: ":8080"
	Addr string

	// ===== Lifecycle =====

	// GracefulShutdownTimeout is max time for graceful shutdown.
	// After timeout, forces shutdown.
	// Default: 30 seconds
	// Relies on Kubernetes terminationGracePeriodSeconds, not internal delays.
	GracefulShutdownTimeout time.Duration

	// Cleanup is called during graceful shutdown before server stops.
	// Use for closing resources (DB connections, caches, etc).
	// Context has GracefulShutdownTimeout deadline.
	// If Cleanup returns error, it is logged but shutdown continues.
	Cleanup func(context.Context) error

	// StopCh signals server shutdown.
	// Server listens on this channel and initiates graceful shutdown.
	// If nil, server runs until killed (SIGTERM/SIGINT).
	StopCh <-chan struct{}
}

// Validate checks ServeConfig for errors.
func (cfg *ServeConfig) Validate() error {
	// Check plugins and impls are set
	if cfg.Plugins == nil {
		return fmt.Errorf("%w: Plugins must be set", ErrInvalidConfig)
	}

	if cfg.Impls == nil {
		return fmt.Errorf("%w: Impls must be set", ErrInvalidConfig)
	}

	// Check all plugins have implementations
	for name := range cfg.Plugins {
		if _, ok := cfg.Impls[name]; !ok {
			return fmt.Errorf("%w: no implementation for plugin %q", ErrInvalidConfig, name)
		}
	}

	// Check all impls have plugins
	for name := range cfg.Impls {
		if _, ok := cfg.Plugins[name]; !ok {
			return fmt.Errorf("%w: no plugin definition for impl %q", ErrInvalidConfig, name)
		}
	}

	// Validate the plugin set (checks path conflicts)
	if err := cfg.Plugins.Validate(); err != nil {
		return fmt.Errorf("%w: %v", ErrInvalidConfig, err)
	}

	// Validate protocol version
	if cfg.ProtocolVersion < 1 {
		return fmt.Errorf("%w: ProtocolVersion must be >= 1", ErrInvalidConfig)
	}

	return nil
}

// Serve serves the plugins defined in the configuration.
// This function blocks until the server is shut down via StopCh or signal.
func Serve(cfg *ServeConfig) error {
	// Apply defaults
	if cfg.Addr == "" {
		cfg.Addr = ":8080"
	}
	if cfg.GracefulShutdownTimeout == 0 {
		cfg.GracefulShutdownTimeout = 30 * time.Second
	}
	if cfg.ProtocolVersion == 0 {
		cfg.ProtocolVersion = 1
	}

	// Validate configuration
	if err := cfg.Validate(); err != nil {
		return err
	}

	// Build the HTTP mux
	mux := http.NewServeMux()

	// Register handshake service (always enabled for v1)
	handshakeServer := NewHandshakeServer(cfg)
	handshakePath, handshakeHandler := HandshakeServerHandler(handshakeServer)
	mux.Handle(handshakePath, handshakeHandler)

	// Register plugin services
	for name, plugin := range cfg.Plugins {
		impl, ok := cfg.Impls[name]
		if !ok {
			return fmt.Errorf("no implementation for plugin %q", name)
		}

		path, handler, err := plugin.ConnectServer(impl)
		if err != nil {
			return fmt.Errorf("plugin %q: %w", name, err)
		}

		mux.Handle(path, handler)
	}

	// TODO: Register health service (if cfg.HealthService != nil)
	// TODO: Register capability broker (if len(cfg.HostCapabilities) > 0)

	// Create HTTP server
	srv := &http.Server{
		Addr:    cfg.Addr,
		Handler: mux,
	}

	// Set up shutdown handling
	stopCh := cfg.StopCh
	if stopCh == nil {
		sigCh := make(chan os.Signal, 1)
		signal.Notify(sigCh, os.Interrupt, syscall.SIGTERM)
		// Convert signal channel to struct{} channel
		shutdownCh := make(chan struct{})
		go func() {
			<-sigCh
			close(shutdownCh)
		}()
		stopCh = shutdownCh
	}

	// Start server in background
	errCh := make(chan error, 1)
	go func() {
		if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			errCh <- err
		}
	}()

	// Wait for shutdown signal or server error
	select {
	case err := <-errCh:
		return err
	case <-stopCh:
		// Graceful shutdown
		return gracefulShutdown(srv, cfg)
	}
}

// gracefulShutdown performs graceful shutdown of the server.
func gracefulShutdown(srv *http.Server, cfg *ServeConfig) error {
	shutdownCtx, cancel := context.WithTimeout(context.Background(), cfg.GracefulShutdownTimeout)
	defer cancel()

	// Call cleanup function if provided
	if cfg.Cleanup != nil {
		if err := cfg.Cleanup(shutdownCtx); err != nil {
			// Log error but continue shutdown
			fmt.Fprintf(os.Stderr, "Cleanup error: %v\n", err)
		}
	}

	// Shutdown HTTP server (sends GOAWAY for HTTP/2, drains connections)
	if err := srv.Shutdown(shutdownCtx); err != nil {
		return fmt.Errorf("server shutdown: %w", err)
	}

	return nil
}
