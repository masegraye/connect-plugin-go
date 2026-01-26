// Package main demonstrates fx integration with PluginLauncher.
// Shows unmanaged deployment where fx is the orchestrator.
// Demonstrates MIXING strategies: in-memory logger + process-based KV.
package main

import (
	"context"
	"log"
	"net/http"
	"time"

	"connectrpc.com/connect"
	connectplugin "github.com/masegraye/connect-plugin-go"
	loggercap "github.com/masegraye/connect-plugin-go/examples/capabilities/logger"
	kvv1 "github.com/masegraye/connect-plugin-go/examples/kv/gen"
	"github.com/masegraye/connect-plugin-go/examples/kv/gen/kvv1connect"
	loggerv1 "github.com/masegraye/connect-plugin-go/gen/capability/logger/v1"
	"github.com/masegraye/connect-plugin-go/gen/capability/logger/v1/loggerv1connect"
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
			log.Println("✓ Host platform started on :9080")

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
					Strategy:    "in-memory",  // ← In-memory
					Plugin:      &LoggerPlugin{},
					ImplFactory: func() any { return loggercap.NewLoggerCapability() },
					HostURL:     "http://localhost:9080",
					Port:        9081,
				},
				// Process-based KV
				"kv-server": {
					Name:       "kv-server",
					Provides:   []string{"kv"},
					Strategy:   "process",  // ← Process
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

		// === Provide Typed Clients ===

		fx.Provide(fx.Annotate(
			func(launcher *connectplugin.PluginLauncher) (string, error) {
				log.Println("fx providing Logger (in-memory)...")
				endpoint, err := launcher.GetService("logger-inmemory", "logger")
				if err != nil {
					return "", err
				}
				log.Printf("✓ Logger: %s (goroutine)", endpoint)
				return endpoint, nil
			},
			fx.ResultTags(`name:"loggerEndpoint"`),
		)),

		fx.Provide(fx.Annotate(
			func(endpoint string) loggerv1connect.LoggerClient {
				return loggerv1connect.NewLoggerClient(&http.Client{}, endpoint)
			},
			fx.ParamTags(`name:"loggerEndpoint"`),
		)),

		fx.Provide(fx.Annotate(
			func(launcher *connectplugin.PluginLauncher) (string, error) {
				log.Println("fx providing KV (process)...")
				endpoint, err := launcher.GetService("kv-server", "kv")
				if err != nil {
					return "", err
				}
				log.Printf("✓ KV: %s (child process)", endpoint)
				return endpoint, nil
			},
			fx.ResultTags(`name:"kvEndpoint"`),
		)),

		fx.Provide(fx.Annotate(
			func(endpoint string) kvv1connect.KVServiceClient {
				return kvv1connect.NewKVServiceClient(&http.Client{}, endpoint)
			},
			fx.ParamTags(`name:"kvEndpoint"`),
		)),

		// === Application Code ===

		fx.Invoke(func(lc fx.Lifecycle, shutdowner fx.Shutdowner,
			logger loggerv1connect.LoggerClient,
			kv kvv1connect.KVServiceClient) {

			lc.Append(fx.Hook{
				OnStart: func(ctx context.Context) error {
					time.Sleep(500 * time.Millisecond)

					log.Println()
					log.Println("=== Application Using Typed Clients ===")
					log.Println()

					// Log start
					logger.Log(ctx, connect.NewRequest(&loggerv1.LogRequest{
						Level:   "INFO",
						Message: "Application started",
					}))

					// Store data
					_, err := kv.Put(ctx, connect.NewRequest(&kvv1.PutRequest{
						Key:   "user:123",
						Value: []byte("Alice"),
					}))
					if err != nil {
						return err
					}
					log.Println("✓ kv.Put(\"user:123\", \"Alice\")")

					logger.Log(ctx, connect.NewRequest(&loggerv1.LogRequest{
						Level: "INFO",
						Message: "Stored user data",
						Fields: map[string]string{"key": "user:123"},
					}))

					// Retrieve data
					resp, err := kv.Get(ctx, connect.NewRequest(&kvv1.GetRequest{
						Key: "user:123",
					}))
					if err != nil {
						return err
					}
					log.Printf("✓ kv.Get(\"user:123\") → %s", resp.Msg.Value)

					logger.Log(ctx, connect.NewRequest(&loggerv1.LogRequest{
						Level: "INFO",
						Message: "Retrieved user data",
						Fields: map[string]string{"value": string(resp.Msg.Value)},
					}))

					log.Println()
					log.Println("=== fx-managed demonstration complete! ===")
					log.Println("  - Logger: IN-MEMORY (goroutine)")
					log.Println("  - KV: PROCESS (child process)")
					log.Println("  - MIXED STRATEGIES working together!")
					log.Println("  - Application uses generated typed clients")
					log.Println("  - Real data flowing through system")
					log.Println()

					return shutdowner.Shutdown()
				},
			})
		}),
	)

	app.Run()
}
