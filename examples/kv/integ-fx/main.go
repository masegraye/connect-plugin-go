package main

import (
	"context"
	"log"
	"time"

	"connectrpc.com/connect"
	connectplugin "github.com/masegraye/connect-plugin-go"
	loggercap "github.com/masegraye/connect-plugin-go/examples/capabilities/logger"
	kvimpl "github.com/masegraye/connect-plugin-go/examples/kv/impl"
	kvv1 "github.com/masegraye/connect-plugin-go/examples/kv/gen"
	"github.com/masegraye/connect-plugin-go/examples/kv/gen/kvv1connect"
	kvplugin "github.com/masegraye/connect-plugin-go/examples/kv/gen/kvv1plugin"
	"go.uber.org/fx"
)

func main() {
	log.Println("=== KV Plugin with Capability Broker (fx Integration) ===")

	app := fx.New(
		// Provide broker with logger capability
		fx.Provide(func() *connectplugin.CapabilityBroker {
			broker := connectplugin.NewCapabilityBroker("http://localhost:18080")
			broker.RegisterCapability(loggercap.NewLoggerCapability())
			log.Println("✓ Registered logger capability")
			return broker
		}),

		// Provide KV store
		fx.Provide(func() *kvimpl.Store {
			return kvimpl.NewStore()
		}),

		// Provide plugin client (lazy - doesn't connect until OnStart)
		fx.Provide(func() (*connectplugin.Client, error) {
			return connectplugin.NewClient(connectplugin.ClientConfig{
				Endpoint: "http://localhost:18080",
				Plugins: connectplugin.PluginSet{
					"kv": &kvplugin.KVServicePlugin{},
				},
			})
		}),

		// Start plugin server
		fx.Invoke(func(lc fx.Lifecycle, broker *connectplugin.CapabilityBroker, store *kvimpl.Store) error {
			stopCh := make(chan struct{})

			lc.Append(fx.Hook{
				OnStart: func(ctx context.Context) error {
					go func() {
						if err := connectplugin.Serve(&connectplugin.ServeConfig{
							Addr: "localhost:18080",
							Plugins: connectplugin.PluginSet{
								"kv": &kvplugin.KVServicePlugin{},
							},
							Impls: map[string]any{
								"kv": store,
							},
							HealthService:    connectplugin.NewHealthServer(),
							CapabilityBroker: broker,
							StopCh:           stopCh,
						}); err != nil {
							log.Printf("Serve error: %v", err)
						}
					}()

					// Wait for server to be ready
					time.Sleep(200 * time.Millisecond)
					log.Println("✓ Plugin server started with broker")
					return nil
				},
				OnStop: func(ctx context.Context) error {
					close(stopCh)
					return nil
				},
			})

			return nil
		}),

		// Connect client and use plugin
		fx.Invoke(func(lc fx.Lifecycle, shutdowner fx.Shutdowner, client *connectplugin.Client) {
			lc.Append(fx.Hook{
				OnStart: func(ctx context.Context) error {
					// Connect to plugin
					if err := client.Connect(ctx); err != nil {
						return err
					}
					log.Println("✓ Client connected (handshake complete)")

					// Dispense KV plugin
					raw, err := client.Dispense("kv")
					if err != nil {
						return err
					}
					kvClient := raw.(kvv1connect.KVServiceClient)

					log.Println("\n=== Testing KV operations ===")

					// Put
					log.Println("1. Put test/key...")
					_, err = kvClient.Put(ctx, connect.NewRequest(&kvv1.PutRequest{
						Key:   "test/key",
						Value: []byte("test-value"),
					}))
					if err != nil {
						return err
					}
					log.Println("   ✓ Put succeeded")

					// Get
					log.Println("2. Get test/key...")
					getResp, err := kvClient.Get(ctx, connect.NewRequest(&kvv1.GetRequest{
						Key: "test/key",
					}))
					if err != nil {
						return err
					}
					log.Printf("   ✓ Value: %s\n", getResp.Msg.Value)

					// Delete
					log.Println("3. Delete test/key...")
					_, err = kvClient.Delete(ctx, connect.NewRequest(&kvv1.DeleteRequest{
						Key: "test/key",
					}))
					if err != nil {
						return err
					}
					log.Println("   ✓ Delete succeeded")

					log.Println("\n✅ fx integration demonstration complete!")
					log.Println("   - Broker advertised logger capability in handshake")
					log.Println("   - Server and client in same process via fx")
					log.Println("   - All lifecycle managed by fx hooks")

					return shutdowner.Shutdown()
				},
				OnStop: func(ctx context.Context) error {
					return client.Close()
				},
			})
		}),
	)

	app.Run()
}
