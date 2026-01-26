// Package main demonstrates fx integration with PluginLauncher.
// Shows unmanaged deployment where fx is the orchestrator.
// Demonstrates MIXING strategies: in-memory logger + process-based KV.
// Uses generated delegate interfaces for clean, local-feeling API.
package main

import (
	"context"
	"log"
	"net/http"
	"time"

	connectplugin "github.com/masegraye/connect-plugin-go"
	loggercap "github.com/masegraye/connect-plugin-go/examples/capabilities/logger"
	"github.com/masegraye/connect-plugin-go/examples/kv/gen/kvv1delegate"
	"github.com/masegraye/connect-plugin-go/gen/capability/logger/v1/loggerv1connect"
	"github.com/masegraye/connect-plugin-go/gen/loggerv1delegate"
	"github.com/masegraye/connect-plugin-go/gen/plugin/v1/connectpluginv1connect"
	"go.uber.org/fx"
)

func main() {
	log.Println("=== fx-managed: In-Memory Logger + Process KV ===")
	log.Println()

	app := fx.New(
		// === Infrastructure ===

		fx.Provide(func(lc fx.Lifecycle) (*connectplugin.Platform, *connectplugin.ServiceRegistry) {
			handshake := connectplugin.NewHandshakeServer(&connectplugin.ServeConfig{})
			lifecycle := connectplugin.NewLifecycleServer()
			registry := connectplugin.NewServiceRegistry(lifecycle)
			router := connectplugin.NewServiceRouter(handshake, registry, lifecycle)
			platform := connectplugin.NewPlatform(registry, lifecycle, router)

			mux := http.NewServeMux()
			handshakePath, handshakeHandler := connectpluginv1connect.NewHandshakeServiceHandler(handshake)
			mux.Handle(handshakePath, handshakeHandler)
			lifecyclePath, lifecycleHandler := connectpluginv1connect.NewPluginLifecycleHandler(lifecycle)
			mux.Handle(lifecyclePath, lifecycleHandler)
			registryPath, registryHandler := connectpluginv1connect.NewServiceRegistryHandler(registry)
			mux.Handle(registryPath, registryHandler)
			mux.Handle("/services/", platform.Router())

			server := &http.Server{Addr: ":9080", Handler: mux}
			go server.ListenAndServe()
			time.Sleep(200 * time.Millisecond)
			log.Println("Host platform started on :9080")

			lc.Append(fx.Hook{
				OnStop: func(ctx context.Context) error {
					return server.Shutdown(ctx)
				},
			})

			return platform, registry
		}),

		fx.Provide(func(platform *connectplugin.Platform, registry *connectplugin.ServiceRegistry, lc fx.Lifecycle) *connectplugin.PluginLauncher {
			launcher := connectplugin.NewPluginLauncher(platform, registry)

			launcher.RegisterStrategy(connectplugin.NewProcessStrategy())
			launcher.RegisterStrategy(connectplugin.NewInMemoryStrategy())

			launcher.Configure(map[string]connectplugin.PluginSpec{
				// In-memory logger
				"logger-inmemory": {
					Name:        "logger-inmemory",
					Provides:    []string{"logger"},
					Strategy:    "in-memory",
					Plugin:      &LoggerPlugin{},
					ImplFactory: func() any { return loggercap.NewLoggerCapability() },
					HostURL:     "http://localhost:9080",
					Port:        9081,
				},
				// Process-based KV
				"kv-server": {
					Name:       "kv-server",
					Provides:   []string{"kv"},
					Strategy:   "process",
					BinaryPath: "./dist/kv-server",
					HostURL:    "http://localhost:9080",
					Port:       9082,
				},
			})

			lc.Append(fx.Hook{
				OnStop: func(ctx context.Context) error {
					log.Println("Shutting down plugins...")
					launcher.Shutdown()
					return nil
				},
			})

			return launcher
		}),

		// === Provide Typed Delegates ===
		// These provide clean, domain-focused interfaces that hide the RPC details.

		fx.Provide(func(launcher *connectplugin.PluginLauncher) (loggerv1delegate.Logger, error) {
			log.Println("fx providing Logger delegate (in-memory)...")
			endpoint, err := launcher.GetService("logger-inmemory", "logger")
			if err != nil {
				return nil, err
			}
			log.Printf("Logger endpoint: %s (goroutine)", endpoint)

			// Create typed delegate - application code uses this clean interface
			client := loggerv1connect.NewLoggerClient(http.DefaultClient, endpoint)
			return loggerv1delegate.New(client), nil
		}),

		fx.Provide(func(launcher *connectplugin.PluginLauncher) (kvv1delegate.KV, error) {
			log.Println("fx providing KV delegate (process)...")
			endpoint, err := launcher.GetService("kv-server", "kv")
			if err != nil {
				return nil, err
			}
			log.Printf("KV endpoint: %s (child process)", endpoint)

			// Create typed delegate - application code uses this clean interface
			return kvv1delegate.NewFromURL(endpoint), nil
		}),

		// === Application Code ===
		// Note: Application only knows about Logger and KV interfaces.
		// It has no idea these are remote plugins - the API feels local.

		fx.Invoke(func(lc fx.Lifecycle, shutdowner fx.Shutdowner,
			logger loggerv1delegate.Logger,
			kv kvv1delegate.KV) {

			lc.Append(fx.Hook{
				OnStart: func(ctx context.Context) error {
					time.Sleep(500 * time.Millisecond)

					log.Println()
					log.Println("=== Application Using Typed Delegates ===")
					log.Println()

					// Clean API - no connect.NewRequest, no message types!
					logger.Log(ctx, "INFO", "Application started", nil)

					// Store data - simple method call
					err := kv.Put(ctx, "user:123", []byte("Alice"))
					if err != nil {
						return err
					}
					log.Println("kv.Put(\"user:123\", \"Alice\")")

					logger.Log(ctx, "INFO", "Stored user data", map[string]string{"key": "user:123"})

					// Retrieve data - clean return values
					value, found, err := kv.Get(ctx, "user:123")
					if err != nil {
						return err
					}
					if found {
						log.Printf("kv.Get(\"user:123\") -> %s", value)
					}

					logger.Log(ctx, "INFO", "Retrieved user data", map[string]string{"value": string(value)})

					log.Println()
					log.Println("=== fx-managed demonstration complete! ===")
					log.Println("  - Logger: IN-MEMORY (goroutine)")
					log.Println("  - KV: PROCESS (child process)")
					log.Println("  - MIXED STRATEGIES working together!")
					log.Println("  - Application uses GENERATED DELEGATES")
					log.Println("  - Clean API: kv.Put(ctx, key, value)")
					log.Println("  - No connect.NewRequest boilerplate!")
					log.Println()

					return shutdowner.Shutdown()
				},
			})
		}),
	)

	app.Run()
}
