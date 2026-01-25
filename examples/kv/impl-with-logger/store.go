package kvimpllogger

import (
	"context"
	"fmt"
	"net/http"

	"connectrpc.com/connect"
	connectpluginv1 "github.com/masegraye/connect-plugin-go/gen/plugin/v1"
	"github.com/masegraye/connect-plugin-go/gen/plugin/v1/connectpluginv1connect"
	loggerv1 "github.com/masegraye/connect-plugin-go/gen/capability/logger/v1"
	"github.com/masegraye/connect-plugin-go/gen/capability/logger/v1/loggerv1connect"
	kvimpl "github.com/masegraye/connect-plugin-go/examples/kv/impl"
	kvv1 "github.com/masegraye/connect-plugin-go/examples/kv/gen"
)

// StoreWithLogger wraps a KV store and logs operations via host logger capability.
type StoreWithLogger struct {
	*kvimpl.Store
	logger loggerv1connect.LoggerClient
}

// NewStoreWithLogger creates a store that logs via host capability.
// It requests the logger capability during initialization.
func NewStoreWithLogger(brokerURL string) (*StoreWithLogger, error) {
	// Create broker client
	brokerClient := connectpluginv1connect.NewCapabilityBrokerClient(
		&http.Client{},
		brokerURL,
	)

	// Request logger capability
	ctx := context.Background()
	grantResp, err := brokerClient.RequestCapability(ctx, connect.NewRequest(&connectpluginv1.RequestCapabilityRequest{
		CapabilityType: "logger",
		MinVersion:     "1.0.0",
		Reason:         "KV plugin needs logging for operations",
	}))
	if err != nil {
		return nil, fmt.Errorf("failed to request logger capability: %w", err)
	}

	grant := grantResp.Msg.Grant
	fmt.Printf("[KV Plugin] Received logger capability grant: %s\n", grant.GrantId)
	fmt.Printf("[KV Plugin] Logger endpoint: %s\n", grant.EndpointUrl)

	// Create logger client with capability grant
	httpClient := &http.Client{}
	httpClient.Transport = &bearerTokenTransport{
		base:  http.DefaultTransport,
		token: grant.BearerToken,
	}

	loggerClient := loggerv1connect.NewLoggerClient(
		httpClient,
		grant.EndpointUrl,
	)

	return &StoreWithLogger{
		Store:  kvimpl.NewStore(),
		logger: loggerClient,
	}, nil
}

// Put overrides Store.Put to add logging.
func (s *StoreWithLogger) Put(ctx context.Context, req *connect.Request[kvv1.PutRequest]) (*connect.Response[kvv1.PutResponse], error) {
	// Log the operation
	s.logger.Log(ctx, connect.NewRequest(&loggerv1.LogRequest{
		Level:   "info",
		Message: "KV Put operation",
		Fields: map[string]string{
			"key": req.Msg.Key,
		},
	}))

	// Call underlying implementation
	return s.Store.Put(ctx, req)
}

// Delete overrides Store.Delete to add logging.
func (s *StoreWithLogger) Delete(ctx context.Context, req *connect.Request[kvv1.DeleteRequest]) (*connect.Response[kvv1.DeleteResponse], error) {
	// Log the operation
	s.logger.Log(ctx, connect.NewRequest(&loggerv1.LogRequest{
		Level:   "info",
		Message: "KV Delete operation",
		Fields: map[string]string{
			"key": req.Msg.Key,
		},
	}))

	// Call underlying implementation
	return s.Store.Delete(ctx, req)
}

// bearerTokenTransport adds Authorization header to all requests.
type bearerTokenTransport struct {
	base  http.RoundTripper
	token string
}

func (t *bearerTokenTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	req.Header.Set("Authorization", "Bearer "+t.token)
	return t.base.RoundTrip(req)
}
