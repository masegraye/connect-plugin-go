// Package main demonstrates fx integration with PluginLauncher.
// Shows unmanaged deployment where fx is the orchestrator.
package main

import (
	"context"
	"fmt"
	"log"
	"net/http"
	"time"

	connectplugin "github.com/masegraye/connect-plugin-go"
	"github.com/masegraye/connect-plugin-go/gen/plugin/v1/connectpluginv1connect"
	"go.uber.org/fx"
)

func main() {
	log.Println("=== fx-managed: Unmanaged Deployment with fx as Orchestrator ===")
	log.Println()

	app := fx.New(
		// === Infrastructure ===

		// Provide host platform (starts eagerly so plugins can connect)
		fx.Provide(func(lc fx.Lifecycle) (*connectplugin.Platform, *connectplugin.ServiceRegistry) {
			handshake := connectplugin.NewHandshakeServer(&connectplugin.ServeConfig{})
			lifecycle := connectplugin.NewLifecycleServer()
			registry := connectplugin.NewServiceRegistry(lifecycle)
			router := connectplugin.NewServiceRouter(handshake, registry, lifecycle)
			platform := connectplugin.NewPlatform(registry, lifecycle, router)

			// Start host server eagerly (so plugins can connect during Provide phase)
			mux := http.NewServeMux()

			handshakePath, handshakeHandler := connectpluginv1connect.NewHandshakeServiceHandler(handshake)
			mux.Handle(handshakePath, handshakeHandler)

			lifecyclePath, lifecycleHandler := connectpluginv1connect.NewPluginLifecycleHandler(lifecycle)
			mux.Handle(lifecyclePath, lifecycleHandler)

			registryPath, registryHandler := connectpluginv1connect.NewServiceRegistryHandler(registry)
			mux.Handle(registryPath, registryHandler)

			mux.Handle("/services/", platform.Router())

			server := &http.Server{
				Addr:    ":9080",
				Handler: mux,
			}

			go server.ListenAndServe()
			time.Sleep(200 * time.Millisecond)  // Wait for ready
			log.Println("✓ Host platform started on :9080")

			// Register shutdown hook
			lc.Append(fx.Hook{
				OnStop: func(ctx context.Context) error {
					return server.Shutdown(ctx)
				},
			})

			return platform, registry
		}),

		// Provide plugin launcher with strategies
		fx.Provide(func(platform *connectplugin.Platform, registry *connectplugin.ServiceRegistry, lc fx.Lifecycle) *connectplugin.PluginLauncher {
			launcher := connectplugin.NewPluginLauncher(platform, registry)

			// Register strategies
			launcher.RegisterStrategy(connectplugin.NewProcessStrategy())
			launcher.RegisterStrategy(connectplugin.NewInMemoryStrategy())

			// Configure plugins
			launcher.Configure(map[string]connectplugin.PluginSpec{
				"logger-plugin": {
					Name:       "logger-plugin",
					Provides:   []string{"logger"},
					Strategy:   "process",
					BinaryPath: "./dist/logger-plugin",
					HostURL:    "http://localhost:9080",
					Port:       9081,
				},
				"cache-plugin": {
					Name:       "cache-plugin",
					Provides:   []string{"cache"},
					Strategy:   "process",
					BinaryPath: "./dist/cache-plugin",
					HostURL:    "http://localhost:9080",
					Port:       9082,
				},
			})

			// Register cleanup
			lc.Append(fx.Hook{
				OnStop: func(ctx context.Context) error {
					log.Println("Shutting down plugins...")
					launcher.Shutdown()
					return nil
				},
			})

			return launcher
		}),

		// === Provide Plugin Services as fx Types ===

		// Provide Logger endpoint (launcher starts logger-plugin)
		fx.Provide(fx.Annotate(
			func(launcher *connectplugin.PluginLauncher) (string, error) {
				log.Println("fx providing Logger service...")
				endpoint, err := launcher.GetService("logger-plugin", "logger")
				if err != nil {
					return "", fmt.Errorf("failed to get logger: %w", err)
				}
				log.Printf("✓ Logger endpoint: %s", endpoint)
				return endpoint, nil
			},
			fx.ResultTags(`name:"loggerEndpoint"`),
		)),

		// Provide Cache endpoint (launcher starts cache-plugin)
		fx.Provide(fx.Annotate(
			func(launcher *connectplugin.PluginLauncher) (string, error) {
				log.Println("fx providing Cache service...")
				endpoint, err := launcher.GetService("cache-plugin", "cache")
				if err != nil {
					return "", fmt.Errorf("failed to get cache: %w", err)
				}
				log.Printf("✓ Cache endpoint: %s", endpoint)
				return endpoint, nil
			},
			fx.ResultTags(`name:"cacheEndpoint"`),
		)),

		// === Application Code ===
		// In real app, you'd provide typed interfaces here:
		//
		// fx.Provide(func(endpoint loggerEndpoint) (Logger, error) {
		//     return loggerv1connect.NewLoggerClient(httpClient, endpoint), nil
		// })
		//
		// Then app code just uses:
		// fx.Invoke(func(logger Logger, cache Cache) {
		//     logger.Log("message")  // Doesn't know it's a plugin!
		// })

		fx.Invoke(fx.Annotate(
			func(shutdowner fx.Shutdowner, loggerEndpoint, cacheEndpoint string) error {
				// Give plugins time to register and report health
				time.Sleep(1 * time.Second)

				log.Println()
				log.Println("=== Plugin Status ===")
				log.Println("✓ Logger plugin running (process strategy)")
				log.Println("✓ Cache plugin running (process strategy)")
				log.Println("✓ Both self-registered with Service Registry")
				log.Println("✓ Cache discovered logger via Service Registry")
				log.Println()
				log.Println("=== Application receives typed services ===")
				log.Printf("  loggerEndpoint: %s", loggerEndpoint)
				log.Printf("  cacheEndpoint: %s", cacheEndpoint)
				log.Println()
				log.Println("In production, wrap as typed interfaces:")
				log.Println("  fx.Provide(func(endpoint loggerEndpoint) (Logger, error) {")
				log.Println("    return loggerv1connect.NewLoggerClient(http, endpoint), nil")
				log.Println("  })")
				log.Println()
				log.Println("Then application code is plugin-agnostic:")
				log.Println("  fx.Invoke(func(logger Logger, cache Cache) {")
				log.Println("    logger.Log(\"msg\")  // Doesn't know it's a plugin!")
				log.Println("    cache.Set(\"k\", \"v\")")
				log.Println("  })")
				log.Println()
				log.Println("=== fx-managed demonstration complete! ===")
				log.Println("  - fx orchestrated plugin startup (unmanaged deployment)")
				log.Println("  - PluginLauncher started child processes")
				log.Println("  - Plugins self-registered with host")
				log.Println("  - ProcessStrategy used")
				log.Println("  - Service Registry handled dependencies")
				log.Println("  - Application receives plugin services via fx DI")
				log.Println()

				return shutdowner.Shutdown()
			},
			fx.ParamTags(``, `name:"loggerEndpoint"`, `name:"cacheEndpoint"`),
		)),
	)

	app.Run()
}
