// Package main demonstrates fx integration with PluginLauncher.
// Shows unmanaged deployment where fx is the orchestrator.
// Demonstrates MIXING strategies: in-memory logger + process-based cache.
package main

import (
	"context"
	"fmt"
	"log"
	"net/http"
	"time"

	"connectrpc.com/connect"
	connectplugin "github.com/masegraye/connect-plugin-go"
	loggercap "github.com/masegraye/connect-plugin-go/examples/capabilities/logger"
	loggerv1 "github.com/masegraye/connect-plugin-go/gen/capability/logger/v1"
	"github.com/masegraye/connect-plugin-go/gen/capability/logger/v1/loggerv1connect"
	"github.com/masegraye/connect-plugin-go/gen/plugin/v1/connectpluginv1connect"
	"go.uber.org/fx"
)

func main() {
	log.Println("=== fx-managed: Mixing In-Memory and Process Strategies ===")
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

			// Register BOTH strategies
			launcher.RegisterStrategy(connectplugin.NewProcessStrategy())
			launcher.RegisterStrategy(connectplugin.NewInMemoryStrategy())

			launcher.Configure(map[string]connectplugin.PluginSpec{
				// In-memory logger
				"logger-inmemory": {
					Name:        "logger-inmemory",
					Provides:    []string{"logger"},
					Strategy:    "in-memory",  // ← In-memory goroutine
					Plugin:      &LoggerPlugin{},
					ImplFactory: func() any { return loggercap.NewLoggerCapability() },
					HostURL:     "http://localhost:9080",
					Port:        9081,
				},
				// Process-based cache (depends on logger)
				"cache-plugin": {
					Name:       "cache-plugin",
					Provides:   []string{"cache"},
					Strategy:   "process",  // ← Child process
					BinaryPath: "./dist/cache-plugin",
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

		// === Provide Logger (In-Memory) ===

		fx.Provide(fx.Annotate(
			func(launcher *connectplugin.PluginLauncher) (string, error) {
				log.Println("fx providing Logger service (in-memory)...")
				endpoint, err := launcher.GetService("logger-inmemory", "logger")
				if err != nil {
					return "", fmt.Errorf("failed to get logger: %w", err)
				}
				log.Printf("✓ Logger endpoint: %s (in-memory goroutine)", endpoint)
				return endpoint, nil
			},
			fx.ResultTags(`name:"loggerEndpoint"`),
		)),

		fx.Provide(fx.Annotate(
			func(endpoint string) loggerv1connect.LoggerClient {
				log.Println("fx providing typed Logger client...")
				httpClient := &http.Client{}
				loggerClient := loggerv1connect.NewLoggerClient(httpClient, endpoint)
				log.Println("✓ Typed loggerv1connect.LoggerClient created")
				return loggerClient
			},
			fx.ParamTags(`name:"loggerEndpoint"`),
		)),

		// === Provide Cache (Process, depends on Logger) ===

		fx.Provide(fx.Annotate(
			func(launcher *connectplugin.PluginLauncher) (string, error) {
				log.Println("fx providing Cache service (process)...")
				endpoint, err := launcher.GetService("cache-plugin", "cache")
				if err != nil {
					return "", fmt.Errorf("failed to get cache: %w", err)
				}
				log.Printf("✓ Cache endpoint: %s (child process)", endpoint)
				return endpoint, nil
			},
			fx.ResultTags(`name:"cacheEndpoint"`),
		)),

		// === Application Code ===

		fx.Invoke(fx.Annotate(
			func(lc fx.Lifecycle, shutdowner fx.Shutdowner,
				loggerEndpoint, cacheEndpoint string,
				loggerClient loggerv1connect.LoggerClient) {

			lc.Append(fx.Hook{
				OnStart: func(ctx context.Context) error {
					// Give plugins time to register and discover
					time.Sleep(1 * time.Second)

					log.Println()
					log.Println("=== Application received from fx DI ===")
					log.Println()
					log.Println("Logger (in-memory strategy):")
					log.Printf("  loggerEndpoint: %s", loggerEndpoint)
					log.Printf("  loggerClient: %T", loggerClient)
					log.Println()
					log.Println("Cache (process strategy, requires logger):")
					log.Printf("  cacheEndpoint: %s", cacheEndpoint)
					log.Println()

					log.Println("=== Using typed Logger client ===")

					// Application uses logger - doesn't know it's in-memory!
					_, err := loggerClient.Log(ctx, connect.NewRequest(&loggerv1.LogRequest{
						Level:   "INFO",
						Message: "Application started via fx-managed",
					}))
					if err != nil {
						return fmt.Errorf("Log failed: %w", err)
					}
					log.Println("✓ loggerClient.Log(\"Application started\")")

					_, err = loggerClient.Log(ctx, connect.NewRequest(&loggerv1.LogRequest{
						Level:   "INFO",
						Message: "Cache plugin depends on in-memory logger",
						Fields: map[string]string{
							"cache_endpoint": cacheEndpoint,
						},
					}))
					if err != nil {
						return fmt.Errorf("Log failed: %w", err)
					}
					log.Println("✓ loggerClient.Log with structured fields")

					log.Println()
					log.Println("=== fx-managed demonstration complete! ===")
					log.Println("  - fx orchestrated plugin startup (unmanaged)")
					log.Println("  - Logger: IN-MEMORY strategy (goroutine)")
					log.Println("  - Cache: PROCESS strategy (child process)")
					log.Println("  - Cache depends on Logger via Service Registry")
					log.Println("  - Cache discovered in-memory logger successfully")
					log.Println("  - fx provides typed loggerv1connect.LoggerClient")
					log.Println("  - Application doesn't know logger is in-memory!")
					log.Println("  - MIXED STRATEGIES in same application!")
					log.Println()

					return shutdowner.Shutdown()
				},
			})
			},
			fx.ParamTags(``, ``, `name:"loggerEndpoint"`, `name:"cacheEndpoint"`, ``),
		)),
	)

	app.Run()
}
