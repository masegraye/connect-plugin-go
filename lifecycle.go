package connectplugin

import (
	"context"
	"fmt"
	"net/http"
	"sync"

	"connectrpc.com/connect"
	connectpluginv1 "github.com/masegraye/connect-plugin-go/gen/plugin/v1"
	"github.com/masegraye/connect-plugin-go/gen/plugin/v1/connectpluginv1connect"
)

// LifecycleServer implements the PluginLifecycle service (plugin → host).
// Plugins call this to report their health state changes.
type LifecycleServer struct {
	mu     sync.RWMutex
	states map[string]*PluginHealthState // runtime_id → health state
}

// PluginHealthState tracks a plugin's health state and metadata.
type PluginHealthState struct {
	State                   connectpluginv1.HealthState
	Reason                  string
	UnavailableDependencies []string
}

// NewLifecycleServer creates a new lifecycle server.
func NewLifecycleServer() *LifecycleServer {
	return &LifecycleServer{
		states: make(map[string]*PluginHealthState),
	}
}

// ReportHealth handles plugin health state reports.
func (l *LifecycleServer) ReportHealth(
	ctx context.Context,
	req *connect.Request[connectpluginv1.ReportHealthRequest],
) (*connect.Response[connectpluginv1.ReportHealthResponse], error) {
	// Extract runtime_id from request headers
	// For now, we'll need to pass this through context or headers
	// TODO: Extract from X-Plugin-Runtime-ID header when router is implemented
	runtimeID := req.Header().Get("X-Plugin-Runtime-ID")
	if runtimeID == "" {
		return nil, connect.NewError(
			connect.CodeInvalidArgument,
			fmt.Errorf("X-Plugin-Runtime-ID header required"),
		)
	}

	l.mu.Lock()
	defer l.mu.Unlock()

	// Store or update health state
	l.states[runtimeID] = &PluginHealthState{
		State:                   req.Msg.State,
		Reason:                  req.Msg.Reason,
		UnavailableDependencies: req.Msg.UnavailableDependencies,
	}

	return connect.NewResponse(&connectpluginv1.ReportHealthResponse{}), nil
}

// GetHealthState returns the current health state for a plugin.
// Returns nil if the plugin has not reported health.
func (l *LifecycleServer) GetHealthState(runtimeID string) *PluginHealthState {
	l.mu.RLock()
	defer l.mu.RUnlock()

	state, ok := l.states[runtimeID]
	if !ok {
		return nil
	}

	// Return a copy to avoid races
	return &PluginHealthState{
		State:                   state.State,
		Reason:                  state.Reason,
		UnavailableDependencies: append([]string{}, state.UnavailableDependencies...),
	}
}

// ShouldRouteTraffic determines if traffic should be routed to a plugin
// based on its health state.
//
// Routing behavior:
//   - HEALTHY: route traffic (full functionality)
//   - DEGRADED: route traffic (plugin decides what to return)
//   - UNHEALTHY: DO NOT route traffic
//   - Unknown/nil state: assume HEALTHY (backward compat)
func (l *LifecycleServer) ShouldRouteTraffic(runtimeID string) bool {
	state := l.GetHealthState(runtimeID)
	if state == nil {
		// No health reported yet - assume healthy (backward compat)
		return true
	}

	switch state.State {
	case connectpluginv1.HealthState_HEALTH_STATE_HEALTHY:
		return true
	case connectpluginv1.HealthState_HEALTH_STATE_DEGRADED:
		return true
	case connectpluginv1.HealthState_HEALTH_STATE_UNHEALTHY:
		return false
	default:
		// Unknown state - assume healthy
		return true
	}
}

// LifecycleServerHandler returns the path and handler for the lifecycle service.
func LifecycleServerHandler(server *LifecycleServer) (string, http.Handler) {
	return connectpluginv1connect.NewPluginLifecycleHandler(server)
}

// PluginControlClient is a helper for calling PluginControl RPCs on a plugin.
// This is used by the host to query plugin health and request shutdown.
type PluginControlClient struct {
	client connectpluginv1connect.PluginControlClient
}

// NewPluginControlClient creates a client for calling PluginControl RPCs.
func NewPluginControlClient(endpoint string, httpClient connect.HTTPClient) *PluginControlClient {
	return &PluginControlClient{
		client: connectpluginv1connect.NewPluginControlClient(httpClient, endpoint),
	}
}

// GetHealth queries the plugin's current health state.
func (p *PluginControlClient) GetHealth(ctx context.Context) (*connectpluginv1.GetHealthResponse, error) {
	resp, err := p.client.GetHealth(ctx, connect.NewRequest(&connectpluginv1.GetHealthRequest{}))
	if err != nil {
		return nil, err
	}
	return resp.Msg, nil
}

// Shutdown requests graceful shutdown with the given grace period.
func (p *PluginControlClient) Shutdown(ctx context.Context, gracePeriodSeconds int32, reason string) (bool, error) {
	resp, err := p.client.Shutdown(ctx, connect.NewRequest(&connectpluginv1.ShutdownRequest{
		GracePeriodSeconds: gracePeriodSeconds,
		Reason:             reason,
	}))
	if err != nil {
		return false, err
	}
	return resp.Msg.Acknowledged, nil
}
