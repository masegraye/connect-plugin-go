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

// HealthServer implements the health checking protocol.
type HealthServer struct {
	mu        sync.RWMutex
	shutdown  bool
	statusMap map[string]connectpluginv1.ServingStatus
	watchers  map[string][]*healthWatcher
}

type healthWatcher struct {
	ch     chan connectpluginv1.ServingStatus
	ctx    context.Context
	cancel context.CancelFunc
}

// NewHealthServer creates a new health server.
func NewHealthServer() *HealthServer {
	return &HealthServer{
		statusMap: map[string]connectpluginv1.ServingStatus{
			"": connectpluginv1.ServingStatus_SERVING_STATUS_SERVING, // Overall health
		},
		watchers: make(map[string][]*healthWatcher),
	}
}

// Check implements the health check RPC.
func (h *HealthServer) Check(
	ctx context.Context,
	req *connect.Request[connectpluginv1.HealthCheckRequest],
) (*connect.Response[connectpluginv1.HealthCheckResponse], error) {
	h.mu.RLock()
	defer h.mu.RUnlock()

	status, ok := h.statusMap[req.Msg.Service]
	if !ok {
		return nil, connect.NewError(
			connect.CodeNotFound,
			fmt.Errorf("unknown service: %s", req.Msg.Service),
		)
	}

	return connect.NewResponse(&connectpluginv1.HealthCheckResponse{
		Status: status,
	}), nil
}

// Watch implements the health watch streaming RPC.
func (h *HealthServer) Watch(
	ctx context.Context,
	req *connect.Request[connectpluginv1.HealthCheckRequest],
	stream *connect.ServerStream[connectpluginv1.HealthCheckResponse],
) error {
	service := req.Msg.Service

	h.mu.Lock()

	// Get current status
	status, ok := h.statusMap[service]
	if !ok {
		status = connectpluginv1.ServingStatus_SERVING_STATUS_SERVICE_UNKNOWN
	}

	// Create watcher
	wctx, cancel := context.WithCancel(ctx)
	watcher := &healthWatcher{
		ch:     make(chan connectpluginv1.ServingStatus, 1),
		ctx:    wctx,
		cancel: cancel,
	}

	// Send initial status
	watcher.ch <- status

	// Register watcher
	h.watchers[service] = append(h.watchers[service], watcher)
	h.mu.Unlock()

	// Cleanup on exit
	defer func() {
		h.mu.Lock()
		watchers := h.watchers[service]
		for i, w := range watchers {
			if w == watcher {
				h.watchers[service] = append(watchers[:i], watchers[i+1:]...)
				break
			}
		}
		h.mu.Unlock()
		cancel()
	}()

	// Stream status changes
	var lastSent connectpluginv1.ServingStatus = -1
	for {
		select {
		case <-ctx.Done():
			return nil

		case status := <-watcher.ch:
			// Deduplicate - don't send same status twice
			if status == lastSent {
				continue
			}
			lastSent = status

			if err := stream.Send(&connectpluginv1.HealthCheckResponse{
				Status: status,
			}); err != nil {
				return err
			}
		}
	}
}

// SetServingStatus updates a service's health status.
// Use empty string for overall server health.
func (h *HealthServer) SetServingStatus(service string, status connectpluginv1.ServingStatus) {
	h.mu.Lock()
	defer h.mu.Unlock()

	if h.shutdown {
		return
	}

	h.statusMap[service] = status

	// Notify watchers
	for _, watcher := range h.watchers[service] {
		select {
		case <-watcher.ch: // Clear old status
		default:
		}
		select {
		case watcher.ch <- status:
		default:
			// Watcher not reading, skip
		}
	}
}

// Shutdown sets all services to NOT_SERVING and prevents future updates.
func (h *HealthServer) Shutdown() {
	h.mu.Lock()
	defer h.mu.Unlock()

	h.shutdown = true
	for service := range h.statusMap {
		h.setStatusLocked(service, connectpluginv1.ServingStatus_SERVING_STATUS_NOT_SERVING)
	}
}

// setStatusLocked sets status without locking (caller must hold lock).
func (h *HealthServer) setStatusLocked(service string, status connectpluginv1.ServingStatus) {
	h.statusMap[service] = status

	// Notify watchers
	for _, watcher := range h.watchers[service] {
		select {
		case <-watcher.ch:
		default:
		}
		select {
		case watcher.ch <- status:
		default:
		}
	}
}

// HealthServerHandler returns the path and handler for the health service.
func HealthServerHandler(server *HealthServer) (string, http.Handler) {
	return connectpluginv1connect.NewHealthServiceHandler(server)
}

// HTTPHealthHandler creates HTTP handlers for Kubernetes probes.
func HTTPHealthHandler(server *HealthServer) http.Handler {
	mux := http.NewServeMux()

	// Liveness: always OK if process is running
	mux.HandleFunc("/healthz", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		w.Write([]byte("ok"))
	})

	// Readiness: check actual health status
	mux.HandleFunc("/readyz", func(w http.ResponseWriter, r *http.Request) {
		resp, err := server.Check(r.Context(), connect.NewRequest(&connectpluginv1.HealthCheckRequest{
			Service: "", // Overall health
		}))

		if err != nil || resp.Msg.Status != connectpluginv1.ServingStatus_SERVING_STATUS_SERVING {
			w.WriteHeader(http.StatusServiceUnavailable)
			w.Write([]byte("not ready"))
			return
		}

		w.WriteHeader(http.StatusOK)
		w.Write([]byte("ready"))
	})

	return mux
}
