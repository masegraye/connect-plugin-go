package connectplugin

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"connectrpc.com/connect"
	connectpluginv1 "github.com/example/connect-plugin-go/gen/plugin/v1"
	"github.com/example/connect-plugin-go/gen/plugin/v1/connectpluginv1connect"
	loggerv1 "github.com/example/connect-plugin-go/gen/capability/logger/v1"
	"github.com/example/connect-plugin-go/gen/capability/logger/v1/loggerv1connect"
)

// testLoggerCapability is a test logger capability.
type testLoggerCapability struct {
	logs []string
}

func (t *testLoggerCapability) CapabilityType() string {
	return "logger"
}

func (t *testLoggerCapability) Version() string {
	return "1.0.0"
}

func (t *testLoggerCapability) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	_, handler := loggerv1connect.NewLoggerHandler(t)
	handler.ServeHTTP(w, r)
}

func (t *testLoggerCapability) Log(
	ctx context.Context,
	req *connect.Request[loggerv1.LogRequest],
) (*connect.Response[loggerv1.LogResponse], error) {
	t.logs = append(t.logs, req.Msg.Message)
	return connect.NewResponse(&loggerv1.LogResponse{}), nil
}

func TestCapabilityBroker_RequestCapability(t *testing.T) {
	// Create test logger
	logger := &testLoggerCapability{}

	// Create broker (will update baseURL after server starts)
	broker := NewCapabilityBroker("")
	broker.RegisterCapability(logger)

	// Create test server
	server := httptest.NewServer(broker.Handler())
	defer server.Close()

	// Update broker baseURL with actual server URL
	broker.baseURL = server.URL

	// Create broker client
	brokerClient := connectpluginv1connect.NewCapabilityBrokerClient(
		server.Client(),
		server.URL,
	)

	// Request logger capability
	grantResp, err := brokerClient.RequestCapability(
		context.Background(),
		connect.NewRequest(&connectpluginv1.RequestCapabilityRequest{
			CapabilityType: "logger",
			MinVersion:     "1.0.0",
			Reason:         "test",
		}),
	)
	if err != nil {
		t.Fatalf("RequestCapability failed: %v", err)
	}

	grant := grantResp.Msg.Grant
	if grant == nil {
		t.Fatal("grant is nil")
	}
	if grant.CapabilityType != "logger" {
		t.Errorf("grant.CapabilityType = %s, want logger", grant.CapabilityType)
	}
	if grant.BearerToken == "" {
		t.Error("grant.BearerToken is empty")
	}
	if grant.EndpointUrl == "" {
		t.Error("grant.EndpointUrl is empty")
	}

	// Use the capability grant
	// The endpoint URL should be absolute (server.URL + path)
	// But let's handle both cases
	capabilityURL := grant.EndpointUrl
	if !strings.HasPrefix(capabilityURL, "http") {
		capabilityURL = server.URL + grant.EndpointUrl
	}

	httpClient := &http.Client{
		Transport: &bearerTokenTransport{
			base:  http.DefaultTransport,
			token: grant.BearerToken,
		},
	}

	loggerClient := loggerv1connect.NewLoggerClient(httpClient, capabilityURL)

	// Call Log via capability
	_, err = loggerClient.Log(
		context.Background(),
		connect.NewRequest(&loggerv1.LogRequest{
			Level:   "info",
			Message: "test message",
		}),
	)
	if err != nil {
		t.Fatalf("Log failed: %v", err)
	}

	// Verify log was recorded
	if len(logger.logs) != 1 {
		t.Fatalf("expected 1 log, got %d", len(logger.logs))
	}
	if logger.logs[0] != "test message" {
		t.Errorf("log = %s, want 'test message'", logger.logs[0])
	}
}

func TestCapabilityBroker_UnknownCapability(t *testing.T) {
	broker := NewCapabilityBroker("")

	server := httptest.NewServer(broker.Handler())
	defer server.Close()

	broker.baseURL = server.URL

	brokerClient := connectpluginv1connect.NewCapabilityBrokerClient(
		server.Client(),
		server.URL,
	)

	// Request unknown capability
	_, err := brokerClient.RequestCapability(
		context.Background(),
		connect.NewRequest(&connectpluginv1.RequestCapabilityRequest{
			CapabilityType: "unknown",
		}),
	)

	if err == nil {
		t.Fatal("RequestCapability should fail for unknown capability")
	}

	if connect.CodeOf(err) != connect.CodeNotFound {
		t.Errorf("error code = %v, want NotFound", connect.CodeOf(err))
	}
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
