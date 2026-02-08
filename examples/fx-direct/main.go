// Package main demonstrates fx integration with InMemoryStrategy.
//
// All plugins run in-process via net.Pipe() — zero TCP, zero binaries.
// Application code receives typed interfaces via fx dependency injection
// and has no idea the implementation is a ConnectRPC service.
package main

import (
	"context"
	"log"

	"connectrpc.com/connect"
	connectplugin "github.com/masegraye/connect-plugin-go"
	kvv1 "github.com/masegraye/connect-plugin-go/examples/kv/gen"
	"github.com/masegraye/connect-plugin-go/examples/kv/gen/kvv1connect"
	kvplugin "github.com/masegraye/connect-plugin-go/examples/kv/gen/kvv1plugin"
	kvimpl "github.com/masegraye/connect-plugin-go/examples/kv/impl"
	"go.uber.org/fx"
)

func main() {
	log.Println("=== fx + Direct Dispatch ===")
	log.Println()

	app := fx.New(
		// === Infrastructure ===
		// Minimal setup: registry + launcher. No HTTP server needed.

		fx.Provide(func() (*connectplugin.ServiceRegistry, *connectplugin.PluginLauncher) {
			lifecycle := connectplugin.NewLifecycleServer()
			registry := connectplugin.NewServiceRegistry(lifecycle)
			launcher := connectplugin.NewPluginLauncher(nil, registry)
			launcher.RegisterStrategy(connectplugin.NewInMemoryStrategy(registry))

			launcher.Configure(map[string]connectplugin.PluginSpec{
				"kv": {
					Name:     "kv",
					Provides: []string{"kv"},
					Strategy: "in-memory",
					Plugin:   &kvplugin.KVServicePlugin{},
					ImplFactory: func() any {
						return kvimpl.NewStore()
					},
				},
			})

			return registry, launcher
		}),

		// === Provide typed KV client ===
		// Application code gets kvv1connect.KVServiceClient via DI.

		fx.Provide(func(launcher *connectplugin.PluginLauncher) (kvv1connect.KVServiceClient, error) {
			endpoint, httpClient, err := launcher.GetServiceClient("kv", "kv")
			if err != nil {
				return nil, err
			}
			log.Printf("KV plugin: %s (in-memory)", endpoint)
			return kvv1connect.NewKVServiceClient(httpClient, endpoint), nil
		}),

		// === Application code ===
		// This code has no idea it's talking to an in-memory ConnectRPC service.

		fx.Invoke(func(lc fx.Lifecycle, shutdowner fx.Shutdowner, kv kvv1connect.KVServiceClient) {
			lc.Append(fx.Hook{
				OnStart: func(ctx context.Context) error {
					log.Println()
					log.Println("--- Application logic ---")

					// Store
					_, err := kv.Put(ctx, connect.NewRequest(&kvv1.PutRequest{
						Key:   "user:1",
						Value: []byte("Alice"),
					}))
					if err != nil {
						return err
					}
					log.Println("Put(user:1) = Alice")

					_, err = kv.Put(ctx, connect.NewRequest(&kvv1.PutRequest{
						Key:   "user:2",
						Value: []byte("Bob"),
					}))
					if err != nil {
						return err
					}
					log.Println("Put(user:2) = Bob")

					// Retrieve
					resp, err := kv.Get(ctx, connect.NewRequest(&kvv1.GetRequest{
						Key: "user:1",
					}))
					if err != nil {
						return err
					}
					log.Printf("Get(user:1) = %s", resp.Msg.Value)

					// Delete
					delResp, err := kv.Delete(ctx, connect.NewRequest(&kvv1.DeleteRequest{
						Key: "user:2",
					}))
					if err != nil {
						return err
					}
					log.Printf("Delete(user:2) found=%v", delResp.Msg.Found)

					log.Println()
					log.Println("=== fx + Direct Dispatch complete ===")
					log.Println("  All operations over net.Pipe() — zero TCP")
					log.Println("  Application code is transport-agnostic")
					log.Println()

					return shutdowner.Shutdown()
				},
			})
		}),

		// Cleanup
		fx.Invoke(func(lc fx.Lifecycle, launcher *connectplugin.PluginLauncher) {
			lc.Append(fx.Hook{
				OnStop: func(ctx context.Context) error {
					launcher.Shutdown()
					return nil
				},
			})
		}),
	)

	app.Run()
}
