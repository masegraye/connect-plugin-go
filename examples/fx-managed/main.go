// Package main demonstrates fx integration with PluginLauncher.
// Shows unmanaged deployment where fx is the orchestrator.
package main

import (
	"context"
	"fmt"
	"log"
	"net/http"
	"time"

	"connectrpc.com/connect"
	connectplugin "github.com/masegraye/connect-plugin-go"
	kvv1 "github.com/masegraye/connect-plugin-go/examples/kv/gen"
	"github.com/masegraye/connect-plugin-go/examples/kv/gen/kvv1connect"
	"github.com/masegraye/connect-plugin-go/gen/plugin/v1/connectpluginv1connect"
	"go.uber.org/fx"
)

func main() {
	log.Println("=== fx-managed: Unmanaged Deployment with fx as Orchestrator ===")
	log.Println()

	app := fx.New(
		// === Infrastructure ===

		// Provide host platform
		fx.Provide(func(lc fx.Lifecycle) (*connectplugin.Platform, *connectplugin.ServiceRegistry) {
			handshake := connectplugin.NewHandshakeServer(&connectplugin.ServeConfig{})
			lifecycle := connectplugin.NewLifecycleServer()
			registry := connectplugin.NewServiceRegistry(lifecycle)
			router := connectplugin.NewServiceRouter(handshake, registry, lifecycle)
			platform := connectplugin.NewPlatform(registry, lifecycle, router)

			// Start host server eagerly
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

		// Provide plugin launcher
		fx.Provide(func(platform *connectplugin.Platform, registry *connectplugin.ServiceRegistry, lc fx.Lifecycle) *connectplugin.PluginLauncher {
			launcher := connectplugin.NewPluginLauncher(platform, registry)
			launcher.RegisterStrategy(connectplugin.NewProcessStrategy())

			launcher.Configure(map[string]connectplugin.PluginSpec{
				"kv-server": {
					Name:       "kv-server",
					Provides:   []string{"kv"},
					Strategy:   "process",
					BinaryPath: "./dist/kv-server",
					HostURL:    "http://localhost:9080",
					Port:       9081,
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

		// === Low-Level: Provide Endpoint String ===

		fx.Provide(fx.Annotate(
			func(launcher *connectplugin.PluginLauncher) (string, error) {
				log.Println("fx providing KV service (low-level endpoint)...")
				endpoint, err := launcher.GetService("kv-server", "kv")
				if err != nil {
					return "", fmt.Errorf("failed to get kv: %w", err)
				}
				log.Printf("✓ KV endpoint: %s", endpoint)
				return endpoint, nil
			},
			fx.ResultTags(`name:"kvEndpoint"`),
		)),

		// === High-Level: Provide Typed Client ===

		fx.Provide(fx.Annotate(
			func(endpoint string) kvv1connect.KVServiceClient {
				log.Println("fx providing typed KV client...")
				// Create real generated Connect client from endpoint
				httpClient := &http.Client{}
				kvClient := kvv1connect.NewKVServiceClient(httpClient, endpoint)
				log.Printf("✓ Typed kvv1connect.KVServiceClient created")
				return kvClient
			},
			fx.ParamTags(`name:"kvEndpoint"`),
		)),

		// === Application Code ===
		// Application receives typed client and uses it - doesn't know it's a plugin!

		fx.Invoke(fx.Annotate(
			func(lc fx.Lifecycle, shutdowner fx.Shutdowner,
				kvEndpoint string,
				kvClient kvv1connect.KVServiceClient) {

			lc.Append(fx.Hook{
				OnStart: func(ctx context.Context) error {
					// Give plugin time to register
					time.Sleep(1 * time.Second)

					log.Println()
					log.Println("=== Application received from fx DI ===")
					log.Println()
					log.Println("Low-level (endpoint string):")
					log.Printf("  kvEndpoint: %s", kvEndpoint)
					log.Println()
					log.Println("High-level (typed client):")
					log.Printf("  kvClient: %T", kvClient)
					log.Println()

					log.Println("=== Using typed client ===")

					// Application code just uses kvClient - doesn't know it's a plugin!
					_, err := kvClient.Put(ctx, connect.NewRequest(&kvv1.PutRequest{
						Key:   "greeting",
						Value: []byte("Hello from fx!"),
					}))
					if err != nil {
						return fmt.Errorf("Put failed: %w", err)
					}
					log.Println("✓ kvClient.Put(\"greeting\", \"Hello from fx!\")")

					getResp, err := kvClient.Get(ctx, connect.NewRequest(&kvv1.GetRequest{
						Key: "greeting",
					}))
					if err != nil {
						return fmt.Errorf("Get failed: %w", err)
					}
					log.Printf("✓ kvClient.Get(\"greeting\") → %s", getResp.Msg.Value)

					log.Println()
					log.Println("=== fx-managed demonstration complete! ===")
					log.Println("  - fx orchestrated plugin startup (unmanaged)")
					log.Println("  - PluginLauncher started kv-server process")
					log.Println("  - Plugin self-registered with Service Registry")
					log.Println("  - fx provides endpoint string (low-level)")
					log.Println("  - fx provides kvv1connect.KVServiceClient (high-level)")
					log.Println("  - Application uses REAL typed client from codegen")
					log.Println("  - Application doesn't know KV is a plugin!")
					log.Println()

					return shutdowner.Shutdown()
				},
			})
			},
			fx.ParamTags(``, ``, `name:"kvEndpoint"`, ``),
		)),
	)

	app.Run()
}
