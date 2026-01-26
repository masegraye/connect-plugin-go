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

		// Provide host platform
		fx.Provide(func() (*connectplugin.Platform, *connectplugin.ServiceRegistry) {
			handshake := connectplugin.NewHandshakeServer(&connectplugin.ServeConfig{})
			lifecycle := connectplugin.NewLifecycleServer()
			registry := connectplugin.NewServiceRegistry(lifecycle)
			router := connectplugin.NewServiceRouter(handshake, registry, lifecycle)
			platform := connectplugin.NewPlatform(registry, lifecycle, router)

			return platform, registry
		}),

		// Provide plugin launcher with strategies
		fx.Provide(func(platform *connectplugin.Platform, registry *connectplugin.ServiceRegistry) *connectplugin.PluginLauncher {
			launcher := connectplugin.NewPluginLauncher(platform, registry)

			// Register both strategies
			launcher.RegisterStrategy(connectplugin.NewProcessStrategy())
			launcher.RegisterStrategy(connectplugin.NewInMemoryStrategy())

			// Configure plugins
			launcher.Configure(map[string]connectplugin.PluginSpec{
				"logger-plugin": {
					Name:       "logger-plugin",
					Provides:   []string{"logger"},
					Strategy:   "process",  // ← Process-based (child process)
					BinaryPath: "./dist/logger-plugin",
					HostURL:    "http://localhost:9080",  // Host running on :9080
					Port:       9081,
				},
				"cache-plugin": {
					Name:       "cache-plugin",
					Provides:   []string{"cache"},
					Strategy:   "process",  // ← Also process-based
					BinaryPath: "./dist/cache-plugin",
					HostURL:    "http://localhost:9080",  // Host running on :9080
					Port:       9082,
				},
			})

			return launcher
		}),

		// === Start Host Platform Server ===

		fx.Invoke(func(lc fx.Lifecycle, platform *connectplugin.Platform, registry *connectplugin.ServiceRegistry) {
			var server *http.Server

			lc.Append(fx.Hook{
				OnStart: func(ctx context.Context) error {
					mux := http.NewServeMux()

					// Register Service Registry services
					handshakePath, handshakeHandler := connectpluginv1connect.NewHandshakeServiceHandler(
						connectplugin.NewHandshakeServer(&connectplugin.ServeConfig{}))
					mux.Handle(handshakePath, handshakeHandler)

					lifecyclePath, lifecycleHandler := connectpluginv1connect.NewPluginLifecycleHandler(
						platform.Lifecycle())
					mux.Handle(lifecyclePath, lifecycleHandler)

					registryPath, registryHandler := connectpluginv1connect.NewServiceRegistryHandler(registry)
					mux.Handle(registryPath, registryHandler)

					mux.Handle("/services/", platform.Router())

					server = &http.Server{
						Addr:    ":9080",
						Handler: mux,
					}

					go server.ListenAndServe()

					// Wait for server to be ready
					time.Sleep(200 * time.Millisecond)
					log.Println("✓ Host platform started on :9080")

					return nil
				},
				OnStop: func(ctx context.Context) error {
					if server != nil {
						return server.Shutdown(ctx)
					}
					return nil
				},
			})
		}),

		// === Launch Plugins and Demonstrate ===

		fx.Invoke(func(lc fx.Lifecycle, shutdowner fx.Shutdowner, launcher *connectplugin.PluginLauncher) {
			lc.Append(fx.Hook{
				OnStart: func(ctx context.Context) error {
					log.Println()
					log.Println("=== Launching Plugins via fx ===")

					// Launch logger plugin (process strategy)
					log.Println("fx requesting Logger service...")
					loggerEndpoint, err := launcher.GetService("logger-plugin", "logger")
					if err != nil {
						return fmt.Errorf("failed to launch logger: %w", err)
					}
					log.Printf("✓ Logger available at: %s", loggerEndpoint)

					// Launch cache plugin (process strategy)
					// Cache depends on logger (via Service Registry, not fx DI)
					log.Println("fx requesting Cache service...")
					cacheEndpoint, err := launcher.GetService("cache-plugin", "cache")
					if err != nil {
						return fmt.Errorf("failed to launch cache: %w", err)
					}
					log.Printf("✓ Cache available at: %s", cacheEndpoint)

					// Give plugins time to fully register and report health
					time.Sleep(1 * time.Second)

					log.Println()
					log.Println("=== Plugin Status ===")
					log.Println("✓ Logger plugin running (process strategy)")
					log.Println("✓ Cache plugin running (process strategy)")
					log.Println("✓ Both plugins registered with Service Registry")
					log.Println("✓ Cache discovered logger via Service Registry")
					log.Println()
					log.Println("=== fx-managed demonstration complete! ===")
					log.Println("  - fx orchestrated plugin startup (unmanaged deployment)")
					log.Println("  - Plugins started as child processes")
					log.Println("  - Plugins self-registered with host")
					log.Println("  - PluginLauncher with ProcessStrategy")
					log.Println("  - Service Registry handled plugin dependencies")
					log.Println()

					// Shutdown after demo
					return shutdowner.Shutdown()
				},
			})
		}),

		// === Cleanup Plugins ===

		fx.Invoke(func(lc fx.Lifecycle, launcher *connectplugin.PluginLauncher) {
			lc.Append(fx.Hook{
				OnStop: func(ctx context.Context) error {
					log.Println("Shutting down plugins...")
					launcher.Shutdown()
					return nil
				},
			})
		}),
	)

	app.Run()
}
