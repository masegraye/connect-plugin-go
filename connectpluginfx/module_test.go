package connectpluginfx

import (
	"context"
	"net"
	"net/http"
	"testing"

	"connectrpc.com/connect"
	connectplugin "github.com/masegraye/connect-plugin-go"
	v1 "github.com/masegraye/connect-plugin-go/gen/plugin/v1"
	"github.com/masegraye/connect-plugin-go/gen/plugin/v1/connectpluginv1connect"
	"go.uber.org/fx"
	"go.uber.org/fx/fxtest"
)

// testPlugin for testing
type testPlugin struct{}

func (p *testPlugin) Metadata() connectplugin.PluginMetadata {
	return connectplugin.PluginMetadata{
		Name:    "test",
		Path:    "/test.v1.TestService/",
		Version: "1.0.0",
	}
}

func (p *testPlugin) ConnectServer(impl any) (string, http.Handler, error) {
	return "/test.v1.TestService/", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}), nil
}

func (p *testPlugin) ConnectClient(baseURL string, httpClient connect.HTTPClient) (any, error) {
	return &testClient{endpoint: baseURL}, nil
}

type testClient struct {
	endpoint string
}

type TestInterface interface {
	Endpoint() string
}

func (c *testClient) Endpoint() string {
	return c.endpoint
}

// mockHandshakeServer returns proper handshake responses
type mockHandshakeServer struct{}

func (m *mockHandshakeServer) Handshake(
	ctx context.Context,
	req *connect.Request[v1.HandshakeRequest],
) (*connect.Response[v1.HandshakeResponse], error) {
	return connect.NewResponse(&v1.HandshakeResponse{
		CoreProtocolVersion: 1,
		AppProtocolVersion:  1,
		Plugins: []*v1.PluginInfo{
			{
				Name:        "test",
				Version:     "1.0.0",
				ServicePath: "/test.v1.TestService/",
			},
		},
		ServerMetadata: map[string]string{
			"server_version": "test",
		},
	}), nil
}

func TestPluginModule(t *testing.T) {
	// Create test server with proper handshake
	mux := http.NewServeMux()
	handshakePath, handshakeHandler := connectpluginv1connect.NewHandshakeServiceHandler(&mockHandshakeServer{})
	mux.Handle(handshakePath, handshakeHandler)

	server := &http.Server{Handler: mux}
	listener, err := newLocalListener()
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()

	serverAddr := "http://" + listener.Addr().String()

	go server.Serve(listener)
	defer server.Close()

	var client *connectplugin.Client

	app := fxtest.New(t,
		PluginModule(connectplugin.ClientConfig{
			Endpoint: serverAddr,
			Plugins: connectplugin.PluginSet{
				"test": &testPlugin{},
			},
		}),
		fx.Populate(&client),
	)

	app.RequireStart()
	defer app.RequireStop()

	if client == nil {
		t.Fatal("client is nil")
	}
}

func TestProvideTypedPlugin(t *testing.T) {
	// Create test server with proper handshake
	mux := http.NewServeMux()
	handshakePath, handshakeHandler := connectpluginv1connect.NewHandshakeServiceHandler(&mockHandshakeServer{})
	mux.Handle(handshakePath, handshakeHandler)

	server := &http.Server{Handler: mux}
	listener, err := newLocalListener()
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()

	serverAddr := "http://" + listener.Addr().String()

	go server.Serve(listener)
	defer server.Close()

	var testIface TestInterface

	app := fxtest.New(t,
		PluginModule(connectplugin.ClientConfig{
			Endpoint: serverAddr,
			Plugins: connectplugin.PluginSet{
				"test": &testPlugin{},
			},
		}),
		ProvideTypedPlugin[TestInterface]("test"),
		fx.Populate(&testIface),
	)

	app.RequireStart()
	defer app.RequireStop()

	if testIface == nil {
		t.Fatal("testIface is nil")
	}

	if testIface.Endpoint() != serverAddr {
		t.Errorf("endpoint = %s, want %s", testIface.Endpoint(), serverAddr)
	}
}

func newLocalListener() (net.Listener, error) {
	return net.Listen("tcp", "127.0.0.1:0")
}
