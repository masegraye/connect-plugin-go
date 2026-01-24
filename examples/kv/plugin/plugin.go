package kvplugin

import (
	"net/http"

	"connectrpc.com/connect"
	connectplugin "github.com/example/connect-plugin-go"
	"github.com/example/connect-plugin-go/examples/kv/gen/kvv1connect"
)

// KVServicePlugin implements connectplugin.Plugin for the KV service.
type KVServicePlugin struct{}

var _ connectplugin.Plugin = (*KVServicePlugin)(nil)

// Metadata returns plugin metadata.
func (p *KVServicePlugin) Metadata() connectplugin.PluginMetadata {
	return connectplugin.PluginMetadata{
		Name:    "kv",
		Path:    kvv1connect.KVServiceName,
		Version: "1.0.0",
	}
}

// ConnectServer creates a server-side handler for the KV plugin.
func (p *KVServicePlugin) ConnectServer(impl any) (string, http.Handler, error) {
	handler, ok := impl.(kvv1connect.KVServiceHandler)
	if !ok {
		return "", nil, connectplugin.ErrInvalidPluginImpl
	}

	path, h := kvv1connect.NewKVServiceHandler(handler)
	return path, h, nil
}

// ConnectClient creates a client-side KV service client.
func (p *KVServicePlugin) ConnectClient(baseURL string, httpClient connect.HTTPClient) (any, error) {
	return kvv1connect.NewKVServiceClient(httpClient, baseURL), nil
}
