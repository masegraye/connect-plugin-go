package connectplugin

import (
	"context"
	"net/http"

	"connectrpc.com/connect"
	connectpluginv1 "github.com/masegraye/connect-plugin-go/gen/plugin/v1"
	"github.com/masegraye/connect-plugin-go/gen/plugin/v1/connectpluginv1connect"
)

// PluginIdentityClient is a client for calling a plugin's PluginIdentity service (Model A).
// The host uses this to discover plugin metadata and assign runtime identity.
type PluginIdentityClient struct {
	client connectpluginv1connect.PluginIdentityClient
}

// NewPluginIdentityClient creates a client for calling a plugin's PluginIdentity service.
func NewPluginIdentityClient(baseURL string, httpClient connect.HTTPClient) *PluginIdentityClient {
	if httpClient == nil {
		httpClient = &http.Client{}
	}

	return &PluginIdentityClient{
		client: connectpluginv1connect.NewPluginIdentityClient(httpClient, baseURL),
	}
}

// GetPluginInfo calls the plugin's GetPluginInfo RPC to retrieve metadata.
func (c *PluginIdentityClient) GetPluginInfo(ctx context.Context) (*connectpluginv1.GetPluginInfoResponse, error) {
	resp, err := c.client.GetPluginInfo(ctx, connect.NewRequest(&connectpluginv1.GetPluginInfoRequest{}))
	if err != nil {
		return nil, err
	}
	return resp.Msg, nil
}

// SetRuntimeIdentity calls the plugin's SetRuntimeIdentity RPC to assign identity.
func (c *PluginIdentityClient) SetRuntimeIdentity(ctx context.Context, runtimeID, runtimeToken, hostURL string) error {
	req := connect.NewRequest(&connectpluginv1.SetRuntimeIdentityRequest{
		RuntimeId:    runtimeID,
		RuntimeToken: runtimeToken,
		HostUrl:      hostURL,
	})

	_, err := c.client.SetRuntimeIdentity(ctx, req)
	return err
}
