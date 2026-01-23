package connectplugin

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"os"
	"os/signal"
	"sync"
	"time"

	"connectrpc.com/connect"
)

// ServeConfig configures the plugin server.
type ServeConfig struct {
	// Plugins are the plugins to serve.
	Plugins PluginSet

	// VersionedPlugins is a map of PluginSets for specific protocol versions.
	VersionedPlugins map[int]PluginSet

	// ProtocolVersion is the protocol version being served.
	ProtocolVersion int

	// Addr is the address to listen on (e.g., ":8080").
	// If empty, defaults to ":8080".
	Addr string

	// Listener is an optional pre-created listener.
	// If set, Addr is ignored.
	Listener net.Listener

	// GracefulTimeout is the timeout for graceful shutdown.
	// Defaults to 30 seconds.
	GracefulTimeout time.Duration

	// Interceptors are Connect interceptors applied to all handlers.
	Interceptors []connect.Interceptor

	// Test, if non-nil, puts the server in test mode.
	Test *ServeTestConfig
}

// ServeTestConfig configures plugin serving for test mode.
type ServeTestConfig struct {
	// Context, if set, will cause the server to shut down when cancelled.
	Context context.Context

	// CloseCh, if non-nil, will be closed when serving exits.
	CloseCh chan<- struct{}
}

// Handler is the interface for plugin HTTP handlers.
type Handler interface {
	http.Handler

	// Path returns the base path for this handler.
	Path() string
}

// Serve serves the plugins defined in the configuration.
// This function blocks until the server is shut down.
func Serve(config *ServeConfig) error {
	if config.Addr == "" && config.Listener == nil {
		config.Addr = ":8080"
	}
	if config.GracefulTimeout == 0 {
		config.GracefulTimeout = 30 * time.Second
	}

	// Build the HTTP mux
	mux := http.NewServeMux()

	// Register each plugin
	for name, p := range config.Plugins {
		cp, ok := p.(ConnectPlugin)
		if !ok {
			return fmt.Errorf("plugin %q does not implement ConnectPlugin", name)
		}

		// Get the handler - for now we pass nil as impl
		// In a real implementation, the ServeConfig would include implementations
		handler, err := cp.Server(nil)
		if err != nil {
			return fmt.Errorf("failed to create handler for plugin %q: %w", name, err)
		}

		mux.Handle(handler.Path(), handler)
	}

	// Create the HTTP server
	server := &http.Server{
		Handler: mux,
	}

	// Create listener if not provided
	var listener net.Listener
	var err error
	if config.Listener != nil {
		listener = config.Listener
	} else {
		listener, err = net.Listen("tcp", config.Addr)
		if err != nil {
			return fmt.Errorf("failed to listen on %s: %w", config.Addr, err)
		}
	}

	// Handle shutdown
	var shutdownOnce sync.Once
	shutdown := func() {
		shutdownOnce.Do(func() {
			ctx, cancel := context.WithTimeout(context.Background(), config.GracefulTimeout)
			defer cancel()
			_ = server.Shutdown(ctx)
		})
	}

	// Set up signal handling or test context
	if config.Test != nil && config.Test.Context != nil {
		go func() {
			<-config.Test.Context.Done()
			shutdown()
		}()
	} else {
		sigCh := make(chan os.Signal, 1)
		signal.Notify(sigCh, os.Interrupt)
		go func() {
			<-sigCh
			shutdown()
		}()
	}

	// Serve
	err = server.Serve(listener)
	if err != nil && !errors.Is(err, http.ErrServerClosed) {
		return err
	}

	// Notify test that we're done
	if config.Test != nil && config.Test.CloseCh != nil {
		close(config.Test.CloseCh)
	}

	return nil
}
