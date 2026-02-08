package connectplugin_test

import (
	"context"
	"net/http"
	"testing"

	"connectrpc.com/connect"
	connectplugin "github.com/masegraye/connect-plugin-go"
	kvv1 "github.com/masegraye/connect-plugin-go/examples/kv/gen"
	"github.com/masegraye/connect-plugin-go/examples/kv/gen/kvv1connect"
	kvplugin "github.com/masegraye/connect-plugin-go/examples/kv/gen/kvv1plugin"
	kvimpl "github.com/masegraye/connect-plugin-go/examples/kv/impl"
	connectpluginv1 "github.com/masegraye/connect-plugin-go/gen/plugin/v1"
	"github.com/masegraye/connect-plugin-go/gen/plugin/v1/connectpluginv1connect"
)

func TestInMemoryStrategy_KV_EndToEnd(t *testing.T) {
	ctx := context.Background()

	lifecycle := connectplugin.NewLifecycleServer()
	registry := connectplugin.NewServiceRegistry(lifecycle)

	launcher := connectplugin.NewPluginLauncher(nil, registry)
	launcher.RegisterStrategy(connectplugin.NewInMemoryStrategy(registry))

	launcher.Configure(map[string]connectplugin.PluginSpec{
		"kv-mem": {
			Name:     "kv-mem",
			Provides: []string{"kv"},
			Strategy: "in-memory",
			Plugin:   &kvplugin.KVServicePlugin{},
			ImplFactory: func() any {
				return kvimpl.NewStore()
			},
		},
	})

	endpoint, httpClient, err := launcher.GetServiceClient("kv-mem", "kv")
	if err != nil {
		t.Fatalf("GetServiceClient failed: %v", err)
	}

	if endpoint != "http://in-memory.kv-mem" {
		t.Errorf("endpoint = %q, want %q", endpoint, "http://in-memory.kv-mem")
	}
	if httpClient == nil {
		t.Fatal("httpClient is nil — expected in-memory transport")
	}

	kvClient := kvv1connect.NewKVServiceClient(httpClient, endpoint)

	// Put
	_, err = kvClient.Put(ctx, connect.NewRequest(&kvv1.PutRequest{
		Key:   "mem-key",
		Value: []byte("mem-value"),
	}))
	if err != nil {
		t.Fatalf("Put failed: %v", err)
	}

	// Get
	getResp, err := kvClient.Get(ctx, connect.NewRequest(&kvv1.GetRequest{
		Key: "mem-key",
	}))
	if err != nil {
		t.Fatalf("Get failed: %v", err)
	}
	if !getResp.Msg.Found {
		t.Fatal("expected Found=true")
	}
	if string(getResp.Msg.Value) != "mem-value" {
		t.Errorf("value = %q, want %q", getResp.Msg.Value, "mem-value")
	}

	// Delete
	delResp, err := kvClient.Delete(ctx, connect.NewRequest(&kvv1.DeleteRequest{
		Key: "mem-key",
	}))
	if err != nil {
		t.Fatalf("Delete failed: %v", err)
	}
	if !delResp.Msg.Found {
		t.Error("expected Found=true on delete")
	}

	// Verify deleted
	getResp2, err := kvClient.Get(ctx, connect.NewRequest(&kvv1.GetRequest{
		Key: "mem-key",
	}))
	if err != nil {
		t.Fatalf("Get after delete failed: %v", err)
	}
	if getResp2.Msg.Found {
		t.Error("expected Found=false after delete")
	}

	launcher.Shutdown()
}

func TestInMemoryStrategy_NoPortNeeded(t *testing.T) {
	lifecycle := connectplugin.NewLifecycleServer()
	registry := connectplugin.NewServiceRegistry(lifecycle)

	launcher := connectplugin.NewPluginLauncher(nil, registry)
	launcher.RegisterStrategy(connectplugin.NewInMemoryStrategy(registry))

	launcher.Configure(map[string]connectplugin.PluginSpec{
		"kv-noport": {
			Name:        "kv-noport",
			Provides:    []string{"kv"},
			Strategy:    "in-memory",
			Plugin:      &kvplugin.KVServicePlugin{},
			ImplFactory: func() any { return kvimpl.NewStore() },
		},
	})

	endpoint, httpClient, err := launcher.GetServiceClient("kv-noport", "kv")
	if err != nil {
		t.Fatalf("GetServiceClient failed: %v", err)
	}
	if httpClient == nil {
		t.Fatal("expected in-memory HTTPClient")
	}

	kvClient := kvv1connect.NewKVServiceClient(httpClient, endpoint)
	_, err = kvClient.Put(context.Background(), connect.NewRequest(&kvv1.PutRequest{
		Key:   "test",
		Value: []byte("value"),
	}))
	if err != nil {
		t.Fatalf("Put failed: %v", err)
	}

	launcher.Shutdown()
}

func TestInMemoryStrategy_GetServiceCompat(t *testing.T) {
	lifecycle := connectplugin.NewLifecycleServer()
	registry := connectplugin.NewServiceRegistry(lifecycle)

	launcher := connectplugin.NewPluginLauncher(nil, registry)
	launcher.RegisterStrategy(connectplugin.NewInMemoryStrategy(registry))

	launcher.Configure(map[string]connectplugin.PluginSpec{
		"kv-compat": {
			Name:        "kv-compat",
			Provides:    []string{"kv"},
			Strategy:    "in-memory",
			Plugin:      &kvplugin.KVServicePlugin{},
			ImplFactory: func() any { return kvimpl.NewStore() },
		},
	})

	// GetService (old API) still returns an endpoint string
	endpoint, err := launcher.GetService("kv-compat", "kv")
	if err != nil {
		t.Fatalf("GetService failed: %v", err)
	}
	if endpoint == "" {
		t.Fatal("expected non-empty endpoint")
	}

	launcher.Shutdown()
}

func TestInMemoryStrategy_MultiplePlugins(t *testing.T) {
	ctx := context.Background()
	lifecycle := connectplugin.NewLifecycleServer()
	registry := connectplugin.NewServiceRegistry(lifecycle)

	launcher := connectplugin.NewPluginLauncher(nil, registry)
	launcher.RegisterStrategy(connectplugin.NewInMemoryStrategy(registry))

	launcher.Configure(map[string]connectplugin.PluginSpec{
		"kv-a": {
			Name:        "kv-a",
			Provides:    []string{"kv-a"},
			Strategy:    "in-memory",
			Plugin:      &kvplugin.KVServicePlugin{},
			ImplFactory: func() any { return kvimpl.NewStore() },
		},
		"kv-b": {
			Name:        "kv-b",
			Provides:    []string{"kv-b"},
			Strategy:    "in-memory",
			Plugin:      &kvplugin.KVServicePlugin{},
			ImplFactory: func() any { return kvimpl.NewStore() },
		},
	})

	endpointA, clientA, err := launcher.GetServiceClient("kv-a", "kv-a")
	if err != nil {
		t.Fatalf("GetServiceClient(kv-a) failed: %v", err)
	}
	endpointB, clientB, err := launcher.GetServiceClient("kv-b", "kv-b")
	if err != nil {
		t.Fatalf("GetServiceClient(kv-b) failed: %v", err)
	}

	if endpointA == endpointB {
		t.Error("plugins should have different endpoints")
	}

	kvA := kvv1connect.NewKVServiceClient(clientA, endpointA)
	kvB := kvv1connect.NewKVServiceClient(clientB, endpointB)

	if _, err := kvA.Put(ctx, connect.NewRequest(&kvv1.PutRequest{Key: "x", Value: []byte("from-a")})); err != nil {
		t.Fatalf("kv-a Put failed: %v", err)
	}
	if _, err := kvB.Put(ctx, connect.NewRequest(&kvv1.PutRequest{Key: "x", Value: []byte("from-b")})); err != nil {
		t.Fatalf("kv-b Put failed: %v", err)
	}

	respA, err := kvA.Get(ctx, connect.NewRequest(&kvv1.GetRequest{Key: "x"}))
	if err != nil {
		t.Fatalf("kv-a Get failed: %v", err)
	}
	respB, err := kvB.Get(ctx, connect.NewRequest(&kvv1.GetRequest{Key: "x"}))
	if err != nil {
		t.Fatalf("kv-b Get failed: %v", err)
	}

	if string(respA.Msg.Value) != "from-a" {
		t.Errorf("kv-a value = %q, want %q", respA.Msg.Value, "from-a")
	}
	if string(respB.Msg.Value) != "from-b" {
		t.Errorf("kv-b value = %q, want %q", respB.Msg.Value, "from-b")
	}

	launcher.Shutdown()
}

func TestInMemoryStrategy_MixedWithProcess(t *testing.T) {
	lifecycle := connectplugin.NewLifecycleServer()
	registry := connectplugin.NewServiceRegistry(lifecycle)

	launcher := connectplugin.NewPluginLauncher(nil, registry)
	launcher.RegisterStrategy(connectplugin.NewInMemoryStrategy(registry))
	launcher.RegisterStrategy(connectplugin.NewProcessStrategy())

	launcher.Configure(map[string]connectplugin.PluginSpec{
		"kv-mem": {
			Name:        "kv-mem",
			Provides:    []string{"kv"},
			Strategy:    "in-memory",
			Plugin:      &kvplugin.KVServicePlugin{},
			ImplFactory: func() any { return kvimpl.NewStore() },
		},
	})

	endpoint, httpClient, err := launcher.GetServiceClient("kv-mem", "kv")
	if err != nil {
		t.Fatalf("GetServiceClient failed: %v", err)
	}
	if httpClient == nil {
		t.Fatal("expected in-memory HTTPClient")
	}

	kvClient := kvv1connect.NewKVServiceClient(httpClient, endpoint)
	_, err = kvClient.Put(context.Background(), connect.NewRequest(&kvv1.PutRequest{
		Key:   "mixed",
		Value: []byte("test"),
	}))
	if err != nil {
		t.Fatalf("Put failed: %v", err)
	}

	launcher.Shutdown()
}

// Ensure InMemoryStrategy implements LaunchStrategy at compile time.
var _ connectplugin.LaunchStrategy = (*connectplugin.InMemoryStrategy)(nil)

func TestInMemoryStrategy_HTTPClientInterface(t *testing.T) {
	lifecycle := connectplugin.NewLifecycleServer()
	registry := connectplugin.NewServiceRegistry(lifecycle)
	strategy := connectplugin.NewInMemoryStrategy(registry)

	result, err := strategy.Launch(context.Background(), connectplugin.PluginSpec{
		Name:        "test-plugin",
		Provides:    []string{"test"},
		Plugin:      &kvplugin.KVServicePlugin{},
		ImplFactory: func() any { return kvimpl.NewStore() },
	})
	if err != nil {
		t.Fatalf("Launch failed: %v", err)
	}
	defer result.Cleanup()

	var _ connect.HTTPClient = result.HTTPClient

	_, ok := result.HTTPClient.(*http.Client)
	if !ok {
		t.Error("HTTPClient should be *http.Client")
	}
}

func TestInMemoryStrategy_ControlHealth(t *testing.T) {
	lifecycle := connectplugin.NewLifecycleServer()
	registry := connectplugin.NewServiceRegistry(lifecycle)
	strategy := connectplugin.NewInMemoryStrategy(registry)

	result, err := strategy.Launch(context.Background(), connectplugin.PluginSpec{
		Name:        "health-test",
		Provides:    []string{"test"},
		Plugin:      &kvplugin.KVServicePlugin{},
		ImplFactory: func() any { return kvimpl.NewStore() },
	})
	if err != nil {
		t.Fatalf("Launch failed: %v", err)
	}
	defer result.Cleanup()

	// Call the PluginControl.GetHealth endpoint via ConnectRPC
	controlClient := connectpluginv1connect.NewPluginControlClient(result.HTTPClient.(*http.Client), "http://health-test")
	resp, err := controlClient.GetHealth(context.Background(), connect.NewRequest(&connectpluginv1.GetHealthRequest{}))
	if err != nil {
		t.Fatalf("GetHealth failed: %v", err)
	}
	if resp.Msg.State != connectpluginv1.HealthState_HEALTH_STATE_HEALTHY {
		t.Errorf("state = %v, want HEALTHY", resp.Msg.State)
	}

	// Call Shutdown
	shutResp, err := controlClient.Shutdown(context.Background(), connect.NewRequest(&connectpluginv1.ShutdownRequest{GracePeriodSeconds: 5}))
	if err != nil {
		t.Fatalf("Shutdown failed: %v", err)
	}
	if !shutResp.Msg.Acknowledged {
		t.Error("expected Acknowledged=true")
	}
}

func TestGetDefaultService(t *testing.T) {
	lifecycle := connectplugin.NewLifecycleServer()
	registry := connectplugin.NewServiceRegistry(lifecycle)

	launcher := connectplugin.NewPluginLauncher(nil, registry)
	launcher.RegisterStrategy(connectplugin.NewInMemoryStrategy(registry))

	launcher.Configure(map[string]connectplugin.PluginSpec{
		"single": {
			Name:        "single",
			Provides:    []string{"kv"},
			Strategy:    "in-memory",
			Plugin:      &kvplugin.KVServicePlugin{},
			ImplFactory: func() any { return kvimpl.NewStore() },
		},
		"multi": {
			Name:        "multi",
			Provides:    []string{"kv", "cache"},
			Strategy:    "in-memory",
			Plugin:      &kvplugin.KVServicePlugin{},
			ImplFactory: func() any { return kvimpl.NewStore() },
		},
	})

	// Single-service plugin: GetDefaultService should work
	endpoint, err := launcher.GetDefaultService("single")
	if err != nil {
		t.Fatalf("GetDefaultService(single) failed: %v", err)
	}
	if endpoint == "" {
		t.Error("expected non-empty endpoint")
	}

	// Multi-service plugin: GetDefaultService should error
	_, err = launcher.GetDefaultService("multi")
	if err == nil {
		t.Error("expected error for multi-service plugin")
	}

	// Unknown plugin: should error
	_, err = launcher.GetDefaultService("nonexistent")
	if err == nil {
		t.Error("expected error for unknown plugin")
	}

	launcher.Shutdown()
}

func TestLaunchPluginLocked_UnknownStrategy(t *testing.T) {
	lifecycle := connectplugin.NewLifecycleServer()
	registry := connectplugin.NewServiceRegistry(lifecycle)

	launcher := connectplugin.NewPluginLauncher(nil, registry)
	// Don't register any strategies

	launcher.Configure(map[string]connectplugin.PluginSpec{
		"bad": {
			Name:     "bad",
			Provides: []string{"x"},
			Strategy: "nonexistent",
		},
	})

	_, err := launcher.GetService("bad", "x")
	if err == nil {
		t.Error("expected error for unknown strategy")
	}
}
