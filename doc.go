// Package connectplugin provides a remote-first plugin system using Connect RPC.
//
// # Overview
//
// connect-plugin enables plugins to run as separate services (sidecars,
// containers, or remote hosts) while providing an interface-oriented
// programming model similar to HashiCorp's go-plugin.
//
// Unlike go-plugin which is designed for local subprocess communication,
// connect-plugin is designed for network communication with support for:
//
//   - Multiple protocols (Connect, gRPC, gRPC-Web)
//   - Service discovery
//   - Health checking
//   - Retries and circuit breakers
//   - HTTP/1.1 and HTTP/2
//
// # Basic Usage
//
// Define a plugin interface and implement the ConnectPlugin interface:
//
//	type Greeter interface {
//	    Greet(ctx context.Context, name string) (string, error)
//	}
//
//	type GreeterPlugin struct{}
//
//	func (p *GreeterPlugin) Name() string { return "greeter" }
//
//	func (p *GreeterPlugin) Client(conn connectplugin.ClientConn) (interface{}, error) {
//	    // Return a client that implements Greeter using Connect RPC
//	    return NewGreeterClient(conn), nil
//	}
//
//	func (p *GreeterPlugin) Server(impl interface{}) (connectplugin.Handler, error) {
//	    // Return an HTTP handler that serves the Greeter implementation
//	    return NewGreeterHandler(impl.(Greeter)), nil
//	}
//
// # Plugin Host
//
// The plugin host creates a client and dispenses plugins:
//
//	client := connectplugin.NewClient(&connectplugin.ClientConfig{
//	    Endpoint: "http://localhost:8080",
//	    Plugins: connectplugin.PluginSet{
//	        "greeter": &GreeterPlugin{},
//	    },
//	})
//	defer client.Close()
//
//	protocol, _ := client.Client()
//	raw, _ := protocol.Dispense("greeter")
//	greeter := raw.(Greeter)
//	message, _ := greeter.Greet(ctx, "World")
//
// # Plugin Server
//
// The plugin server serves plugin implementations:
//
//	connectplugin.Serve(&connectplugin.ServeConfig{
//	    Plugins: connectplugin.PluginSet{
//	        "greeter": &GreeterPlugin{Impl: &MyGreeterImpl{}},
//	    },
//	})
package connectplugin
