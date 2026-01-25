package connectpluginfx

import (
	"context"

	connectplugin "github.com/masegraye/connect-plugin-go"
	"go.uber.org/fx"
)

// PluginModule creates an fx module that provides a plugin client.
// The client connects on fx.OnStart and closes on fx.OnStop.
func PluginModule(cfg connectplugin.ClientConfig) fx.Option {
	return fx.Module("connect-plugin",
		fx.Provide(func(lc fx.Lifecycle) (*connectplugin.Client, error) {
			client, err := connectplugin.NewClient(cfg)
			if err != nil {
				return nil, err
			}

			lc.Append(fx.Hook{
				OnStart: func(ctx context.Context) error {
					return client.Connect(ctx)
				},
				OnStop: func(ctx context.Context) error {
					return client.Close()
				},
			})

			return client, nil
		}),
	)
}

// ProvideTypedPlugin creates an fx.Option that provides interface I from a plugin.
// Example:
//
//	fx.New(
//	    connectpluginfx.PluginModule(cfg),
//	    connectpluginfx.ProvideTypedPlugin[kv.KVStore]("kv"),
//	    fx.Invoke(func(store kv.KVStore) {
//	        // Use the plugin
//	    }),
//	)
func ProvideTypedPlugin[I any](pluginName string) fx.Option {
	return fx.Provide(func(client *connectplugin.Client) (I, error) {
		return connectplugin.DispenseTyped[I](client, pluginName)
	})
}
