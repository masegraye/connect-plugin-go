package connectplugin

import (
	"context"
	"net/http"
	"testing"

	"connectrpc.com/connect"
)

func TestClientConfig_Validate(t *testing.T) {
	tests := []struct {
		name    string
		cfg     ClientConfig
		wantErr bool
	}{
		{
			name: "valid config",
			cfg: ClientConfig{
				Endpoint: "http://localhost:8080",
				Plugins: PluginSet{
					"test": &testPlugin{},
				},
			},
			wantErr: false,
		},
		{
			name: "missing endpoint",
			cfg: ClientConfig{
				Plugins: PluginSet{
					"test": &testPlugin{},
				},
			},
			wantErr: true,
		},
		{
			name: "missing plugins",
			cfg: ClientConfig{
				Endpoint: "http://localhost:8080",
			},
			wantErr: true,
		},
		{
			name: "empty plugins",
			cfg: ClientConfig{
				Endpoint: "http://localhost:8080",
				Plugins:  PluginSet{},
			},
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := tt.cfg.Validate()
			if (err != nil) != tt.wantErr {
				t.Errorf("Validate() error = %v, wantErr %v", err, tt.wantErr)
			}
		})
	}
}

func TestNewClient(t *testing.T) {
	cfg := ClientConfig{
		Endpoint: "http://localhost:8080",
		Plugins: PluginSet{
			"test": &testPlugin{},
		},
	}

	client, err := NewClient(cfg)
	if err != nil {
		t.Fatalf("NewClient() error = %v", err)
	}

	if client == nil {
		t.Fatal("NewClient() returned nil client")
	}

	// Client should not be connected yet (lazy)
	if client.connected {
		t.Error("NewClient() connected immediately, expected lazy connection")
	}
}

func TestClient_Dispense_UnknownPlugin(t *testing.T) {
	cfg := ClientConfig{
		Endpoint: "http://localhost:8080",
		Plugins: PluginSet{
			"test": &testPlugin{},
		},
	}

	client, err := NewClient(cfg)
	if err != nil {
		t.Fatalf("NewClient() error = %v", err)
	}

	// Try to dispense unknown plugin (won't connect, will fail early)
	_, err = client.Dispense("unknown")
	if err == nil {
		t.Error("Dispense() succeeded for unknown plugin, expected error")
	}
}

func TestClient_Close(t *testing.T) {
	cfg := ClientConfig{
		Endpoint: "http://localhost:8080",
		Plugins: PluginSet{
			"test": &testPlugin{},
		},
	}

	client, err := NewClient(cfg)
	if err != nil {
		t.Fatalf("NewClient() error = %v", err)
	}

	// Close should succeed
	if err := client.Close(); err != nil {
		t.Errorf("Close() error = %v", err)
	}

	// Second close should be no-op
	if err := client.Close(); err != nil {
		t.Errorf("Second Close() error = %v", err)
	}

	// Connect after close should fail
	if err := client.Connect(context.Background()); err == nil {
		t.Error("Connect() after Close() should fail")
	}
}

// testPlugin is a minimal Plugin implementation for testing
type testPlugin struct{}

func (p *testPlugin) Metadata() PluginMetadata {
	return PluginMetadata{
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
	return &testClient{}, nil
}

type testClient struct{}
