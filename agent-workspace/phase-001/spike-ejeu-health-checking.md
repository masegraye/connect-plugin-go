# Spike: Health Checking Patterns

**Issue:** KOR-ejeu
**Status:** Complete

## Executive Summary

Health checking for remote plugins requires distinguishing between connectivity health (can we reach the plugin?) and semantic health (is the plugin ready to serve?). The gRPC health checking protocol (grpc.health.v1) provides a well-established pattern with Check, Watch, and List RPCs. For connect-plugin, we should implement a compatible health service that integrates with circuit breakers and Kubernetes probes.

## Core Concepts

### Types of Health

| Type | Question | Example |
|------|----------|---------|
| **Connectivity** | Can we reach the endpoint? | TCP connect, TLS handshake |
| **Liveness** | Is the process alive? | HTTP 200 from any endpoint |
| **Readiness** | Is it ready to serve traffic? | Dependencies initialized, warm caches |
| **Semantic** | Is the specific service healthy? | Per-service status in health response |

### gRPC Health Protocol

From `grpc_health_v1/health.pb.go`:

```protobuf
// grpc/health/v1/health.proto

service Health {
    // Check gets the health of the specified service
    rpc Check(HealthCheckRequest) returns (HealthCheckResponse);

    // List provides a snapshot of all service statuses
    rpc List(HealthListRequest) returns (HealthListResponse);

    // Watch streams health status changes
    rpc Watch(HealthCheckRequest) returns (stream HealthCheckResponse);
}

message HealthCheckRequest {
    string service = 1;  // Empty string = overall health
}

message HealthCheckResponse {
    enum ServingStatus {
        UNKNOWN = 0;
        SERVING = 1;
        NOT_SERVING = 2;
        SERVICE_UNKNOWN = 3;  // Only used by Watch
    }
    ServingStatus status = 1;
}

message HealthListRequest {}

message HealthListResponse {
    map<string, HealthCheckResponse> statuses = 1;
}
```

**ServingStatus semantics:**
- `UNKNOWN`: Status not yet determined
- `SERVING`: Ready to handle requests
- `NOT_SERVING`: Not accepting requests (draining, overloaded)
- `SERVICE_UNKNOWN`: Service not registered (Watch only)

### gRPC Health Server Implementation

From `grpc-go/health/server.go`:

```go
type Server struct {
    mu sync.RWMutex
    shutdown bool
    statusMap map[string]ServingStatus
    updates   map[string]map[WatchServer]chan ServingStatus
}

// Check returns current status
func (s *Server) Check(ctx context.Context, req *HealthCheckRequest) (*HealthCheckResponse, error) {
    s.mu.RLock()
    defer s.mu.RUnlock()
    if status, ok := s.statusMap[req.Service]; ok {
        return &HealthCheckResponse{Status: status}, nil
    }
    return nil, status.Error(codes.NotFound, "unknown service")
}

// Watch streams status changes
func (s *Server) Watch(req *HealthCheckRequest, stream Health_WatchServer) error {
    service := req.Service
    update := make(chan ServingStatus, 1)

    s.mu.Lock()
    // Send initial status
    if status, ok := s.statusMap[service]; ok {
        update <- status
    } else {
        update <- SERVICE_UNKNOWN
    }
    // Register for updates
    s.updates[service][stream] = update
    s.mu.Unlock()

    defer func() {
        s.mu.Lock()
        delete(s.updates[service], stream)
        s.mu.Unlock()
    }()

    var lastSent ServingStatus = -1
    for {
        select {
        case status := <-update:
            if lastSent == status {
                continue
            }
            lastSent = status
            if err := stream.Send(&HealthCheckResponse{Status: status}); err != nil {
                return err
            }
        case <-stream.Context().Done():
            return nil
        }
    }
}

// SetServingStatus updates a service's status
func (s *Server) SetServingStatus(service string, status ServingStatus) {
    s.mu.Lock()
    defer s.mu.Unlock()
    if s.shutdown {
        return
    }
    s.statusMap[service] = status
    // Notify all watchers
    for _, ch := range s.updates[service] {
        select {
        case <-ch: // Clear old value
        default:
        }
        ch <- status
    }
}

// Shutdown sets all services to NOT_SERVING
func (s *Server) Shutdown() {
    s.mu.Lock()
    defer s.mu.Unlock()
    s.shutdown = true
    for service := range s.statusMap {
        s.setServingStatusLocked(service, NOT_SERVING)
    }
}
```

**Key patterns:**
- Per-service status tracking
- Watch uses channel per subscriber
- Deduplication (don't send same status twice)
- Graceful shutdown sets all to NOT_SERVING
- Empty service name = overall health

### gRPC Health Client Integration

From `grpc-go/health/client.go`:

```go
func clientHealthCheck(ctx context.Context, newStream func(string) (any, error),
    setConnectivityState func(connectivity.State, error), service string) error {

    tryCnt := 0
    for {
        // Backoff on retry
        if tryCnt > 0 && !backoff(ctx, tryCnt-1) {
            return nil
        }
        tryCnt++

        setConnectivityState(connectivity.Connecting, nil)

        // Open Watch stream
        stream, err := newStream(healthCheckMethod)
        if err != nil {
            continue // Retry
        }

        // Send health check request
        stream.SendMsg(&HealthCheckRequest{Service: service})
        stream.CloseSend()

        // Receive health updates
        for {
            resp := new(HealthCheckResponse)
            err = stream.RecvMsg(resp)

            // UNIMPLEMENTED = assume healthy
            if status.Code(err) == codes.Unimplemented {
                setConnectivityState(connectivity.Ready, nil)
                return err
            }

            if err != nil {
                setConnectivityState(connectivity.TransientFailure, err)
                continue // Retry connection
            }

            // Reset backoff on successful message
            tryCnt = 0

            if resp.Status == SERVING {
                setConnectivityState(connectivity.Ready, nil)
            } else {
                setConnectivityState(connectivity.TransientFailure,
                    fmt.Errorf("health check failed: %s", resp.Status))
            }
        }
    }
}
```

**Key patterns:**
- Watch-based for continuous updates
- Exponential backoff on errors
- UNIMPLEMENTED treated as healthy (backward compat)
- Maps health to connectivity state

## Kubernetes Probe Patterns

### Probe Types

```yaml
# Pod spec
containers:
  - name: plugin
    livenessProbe:
      httpGet:
        path: /healthz
        port: 8080
      initialDelaySeconds: 5
      periodSeconds: 10
      failureThreshold: 3

    readinessProbe:
      grpc:
        port: 8080
        service: "my-plugin"
      initialDelaySeconds: 5
      periodSeconds: 5
      failureThreshold: 1

    startupProbe:
      httpGet:
        path: /healthz
        port: 8080
      failureThreshold: 30
      periodSeconds: 10
```

**Probe semantics:**
- **Liveness**: Failed = container restarted
- **Readiness**: Failed = removed from service endpoints
- **Startup**: Delays liveness checks during startup

### gRPC Probe Support (Kubernetes 1.24+)

Kubernetes natively supports gRPC health checks:

```yaml
readinessProbe:
  grpc:
    port: 8080
    service: ""  # Empty = overall health
```

## connect-plugin Health Design

### Health Service Proto

```protobuf
// connectplugin/health/v1/health.proto

syntax = "proto3";
package connectplugin.health.v1;

// Health service compatible with grpc.health.v1
service HealthService {
    // Check returns the health of a specific service or overall plugin
    rpc Check(HealthCheckRequest) returns (HealthCheckResponse);

    // List returns health of all registered services
    rpc List(HealthListRequest) returns (HealthListResponse);

    // Watch streams health status changes
    rpc Watch(HealthCheckRequest) returns (stream HealthCheckResponse);
}

message HealthCheckRequest {
    // Service name to check. Empty string = overall plugin health.
    string service = 1;
}

message HealthCheckResponse {
    ServingStatus status = 1;

    // Optional: Additional health metadata
    HealthMetadata metadata = 2;
}

enum ServingStatus {
    SERVING_STATUS_UNSPECIFIED = 0;
    SERVING_STATUS_SERVING = 1;
    SERVING_STATUS_NOT_SERVING = 2;
    SERVING_STATUS_SERVICE_UNKNOWN = 3;
}

message HealthMetadata {
    // Version of the plugin
    string version = 1;

    // Uptime in seconds
    int64 uptime_seconds = 2;

    // Last error message if NOT_SERVING
    string last_error = 3;

    // Custom metadata
    map<string, string> extra = 4;
}

message HealthListRequest {}

message HealthListResponse {
    map<string, HealthCheckResponse> statuses = 1;
}
```

### Health Server Implementation

```go
// health/server.go

type Server struct {
    mu        sync.RWMutex
    shutdown  bool
    statusMap map[string]*serviceStatus
    startTime time.Time
}

type serviceStatus struct {
    status    ServingStatus
    lastError string
    metadata  map[string]string
    watchers  map[*watchConn]struct{}
}

type watchConn struct {
    ch     chan *HealthCheckResponse
    ctx    context.Context
    cancel context.CancelFunc
}

func NewServer() *Server {
    return &Server{
        statusMap: map[string]*serviceStatus{
            "": {status: ServingStatus_SERVING}, // Overall health
        },
        startTime: time.Now(),
    }
}

func (s *Server) Check(ctx context.Context, req *connect.Request[HealthCheckRequest]) (
    *connect.Response[HealthCheckResponse], error) {

    s.mu.RLock()
    defer s.mu.RUnlock()

    svc, ok := s.statusMap[req.Msg.Service]
    if !ok {
        return nil, connect.NewError(connect.CodeNotFound, fmt.Errorf("unknown service: %s", req.Msg.Service))
    }

    return connect.NewResponse(&HealthCheckResponse{
        Status: svc.status,
        Metadata: &HealthMetadata{
            Version:       Version,
            UptimeSeconds: int64(time.Since(s.startTime).Seconds()),
            LastError:     svc.lastError,
            Extra:         svc.metadata,
        },
    }), nil
}

func (s *Server) Watch(ctx context.Context, req *connect.Request[HealthCheckRequest],
    stream *connect.ServerStream[HealthCheckResponse]) error {

    service := req.Msg.Service

    s.mu.Lock()
    svc, ok := s.statusMap[service]
    if !ok {
        svc = &serviceStatus{status: ServingStatus_SERVICE_UNKNOWN}
        s.statusMap[service] = svc
    }

    // Create watcher
    wctx, cancel := context.WithCancel(ctx)
    watcher := &watchConn{
        ch:     make(chan *HealthCheckResponse, 1),
        ctx:    wctx,
        cancel: cancel,
    }
    if svc.watchers == nil {
        svc.watchers = make(map[*watchConn]struct{})
    }
    svc.watchers[watcher] = struct{}{}

    // Send initial status
    watcher.ch <- &HealthCheckResponse{Status: svc.status}
    s.mu.Unlock()

    defer func() {
        s.mu.Lock()
        delete(svc.watchers, watcher)
        s.mu.Unlock()
        cancel()
    }()

    var lastSent ServingStatus = -1
    for {
        select {
        case resp := <-watcher.ch:
            if resp.Status == lastSent {
                continue
            }
            lastSent = resp.Status
            if err := stream.Send(resp); err != nil {
                return err
            }
        case <-ctx.Done():
            return nil
        }
    }
}

// SetServingStatus updates a service's health status
func (s *Server) SetServingStatus(service string, status ServingStatus, opts ...StatusOption) {
    s.mu.Lock()
    defer s.mu.Unlock()

    if s.shutdown {
        return
    }

    svc, ok := s.statusMap[service]
    if !ok {
        svc = &serviceStatus{}
        s.statusMap[service] = svc
    }

    cfg := &statusConfig{}
    for _, opt := range opts {
        opt(cfg)
    }

    svc.status = status
    svc.lastError = cfg.lastError
    if cfg.metadata != nil {
        svc.metadata = cfg.metadata
    }

    // Notify watchers
    resp := &HealthCheckResponse{Status: status}
    for watcher := range svc.watchers {
        select {
        case <-watcher.ch: // Clear old
        default:
        }
        select {
        case watcher.ch <- resp:
        default:
        }
    }
}

type StatusOption func(*statusConfig)

type statusConfig struct {
    lastError string
    metadata  map[string]string
}

func WithError(err error) StatusOption {
    return func(c *statusConfig) {
        if err != nil {
            c.lastError = err.Error()
        }
    }
}

func WithMetadata(m map[string]string) StatusOption {
    return func(c *statusConfig) {
        c.metadata = m
    }
}

// Shutdown sets all services to NOT_SERVING
func (s *Server) Shutdown() {
    s.mu.Lock()
    defer s.mu.Unlock()
    s.shutdown = true
    for service := range s.statusMap {
        s.setStatusLocked(service, ServingStatus_NOT_SERVING)
    }
}
```

### Health Monitor (Client-Side)

```go
// health/monitor.go

type Monitor struct {
    client      healthv1connect.HealthServiceClient
    interval    time.Duration
    failureThreshold int
    successThreshold int

    mu           sync.RWMutex
    status       ServingStatus
    consecutive  int
    callbacks    []func(ServingStatus)
}

type MonitorConfig struct {
    // CheckInterval between health checks
    CheckInterval time.Duration

    // FailureThreshold consecutive failures before unhealthy
    FailureThreshold int

    // SuccessThreshold consecutive successes before healthy
    SuccessThreshold int

    // Timeout for each health check
    Timeout time.Duration

    // Service name to check (empty = overall)
    Service string
}

func NewMonitor(client healthv1connect.HealthServiceClient, cfg MonitorConfig) *Monitor {
    if cfg.CheckInterval == 0 {
        cfg.CheckInterval = 10 * time.Second
    }
    if cfg.FailureThreshold == 0 {
        cfg.FailureThreshold = 3
    }
    if cfg.SuccessThreshold == 0 {
        cfg.SuccessThreshold = 1
    }
    if cfg.Timeout == 0 {
        cfg.Timeout = 5 * time.Second
    }

    return &Monitor{
        client:           client,
        interval:         cfg.CheckInterval,
        failureThreshold: cfg.FailureThreshold,
        successThreshold: cfg.SuccessThreshold,
        status:           ServingStatus_UNKNOWN,
    }
}

func (m *Monitor) Start(ctx context.Context) {
    go m.runLoop(ctx)
}

func (m *Monitor) runLoop(ctx context.Context) {
    ticker := time.NewTicker(m.interval)
    defer ticker.Stop()

    // Initial check
    m.doCheck(ctx)

    for {
        select {
        case <-ctx.Done():
            return
        case <-ticker.C:
            m.doCheck(ctx)
        }
    }
}

func (m *Monitor) doCheck(ctx context.Context) {
    checkCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
    defer cancel()

    resp, err := m.client.Check(checkCtx, connect.NewRequest(&HealthCheckRequest{}))

    m.mu.Lock()
    defer m.mu.Unlock()

    var newStatus ServingStatus
    if err != nil {
        // Check failed
        if m.status == ServingStatus_SERVING {
            m.consecutive++
            if m.consecutive >= m.failureThreshold {
                newStatus = ServingStatus_NOT_SERVING
            } else {
                newStatus = m.status // No change yet
            }
        } else {
            m.consecutive = 0
            newStatus = ServingStatus_NOT_SERVING
        }
    } else {
        newStatus = resp.Msg.Status
        if newStatus == ServingStatus_SERVING {
            if m.status != ServingStatus_SERVING {
                m.consecutive++
                if m.consecutive < m.successThreshold {
                    newStatus = m.status // Not healthy yet
                }
            } else {
                m.consecutive = 0
            }
        }
    }

    if newStatus != m.status {
        oldStatus := m.status
        m.status = newStatus
        // Notify callbacks
        for _, cb := range m.callbacks {
            go cb(newStatus)
        }
    }
}

func (m *Monitor) Status() ServingStatus {
    m.mu.RLock()
    defer m.mu.RUnlock()
    return m.status
}

func (m *Monitor) OnStatusChange(cb func(ServingStatus)) {
    m.mu.Lock()
    defer m.mu.Unlock()
    m.callbacks = append(m.callbacks, cb)
}
```

### Circuit Breaker Integration

```go
// Health status feeds into circuit breaker

type HealthAwareCircuitBreaker struct {
    *CircuitBreaker
    monitor *Monitor
}

func NewHealthAwareCircuitBreaker(cb *CircuitBreaker, monitor *Monitor) *HealthAwareCircuitBreaker {
    hacb := &HealthAwareCircuitBreaker{
        CircuitBreaker: cb,
        monitor:        monitor,
    }

    // Open circuit when health check fails
    monitor.OnStatusChange(func(status ServingStatus) {
        if status == ServingStatus_NOT_SERVING {
            cb.Trip()
        }
    })

    return hacb
}

func (cb *HealthAwareCircuitBreaker) Allow() error {
    // Check health status first
    if cb.monitor.Status() == ServingStatus_NOT_SERVING {
        return ErrCircuitOpen
    }
    return cb.CircuitBreaker.Allow()
}
```

### HTTP Health Endpoint

For Kubernetes liveness/readiness without gRPC:

```go
// health/http.go

func NewHTTPHandler(server *Server) http.Handler {
    mux := http.NewServeMux()

    // Liveness: always OK if process is running
    mux.HandleFunc("/healthz", func(w http.ResponseWriter, r *http.Request) {
        w.WriteHeader(http.StatusOK)
        w.Write([]byte("ok"))
    })

    // Readiness: check actual health status
    mux.HandleFunc("/readyz", func(w http.ResponseWriter, r *http.Request) {
        service := r.URL.Query().Get("service")
        resp, err := server.Check(r.Context(), connect.NewRequest(&HealthCheckRequest{Service: service}))
        if err != nil {
            w.WriteHeader(http.StatusServiceUnavailable)
            w.Write([]byte(err.Error()))
            return
        }
        if resp.Msg.Status != ServingStatus_SERVING {
            w.WriteHeader(http.StatusServiceUnavailable)
            w.Write([]byte(resp.Msg.Status.String()))
            return
        }
        w.WriteHeader(http.StatusOK)
        w.Write([]byte("ok"))
    })

    return mux
}
```

## Health Check Flow

```
┌─────────────────────────────────────────────────────────────────┐
│                         Host Process                             │
│                                                                  │
│  ┌──────────────────────────────────────────────────────────┐   │
│  │                    Health Monitor                          │   │
│  │  ┌─────────────┐    ┌─────────────┐    ┌─────────────┐   │   │
│  │  │   Check     │───▶│  Threshold  │───▶│  Callbacks  │   │   │
│  │  │   Loop      │    │   Logic     │    │  (CB, etc)  │   │   │
│  │  └─────────────┘    └─────────────┘    └─────────────┘   │   │
│  └──────────────────────────────┬───────────────────────────┘   │
│                                 │                                │
│                                 │ Watch or Poll                  │
│                                 ▼                                │
│  ┌──────────────────────────────────────────────────────────┐   │
│  │              Connect Health Client                         │   │
│  │                                                            │   │
│  │   Check(service) → ServingStatus                           │   │
│  │   Watch(service) → stream<ServingStatus>                   │   │
│  │   List() → map[service]ServingStatus                       │   │
│  └──────────────────────────────────────────────────────────┘   │
└─────────────────────────────────┬───────────────────────────────┘
                                  │
                             HTTP/Connect
                                  │
┌─────────────────────────────────▼───────────────────────────────┐
│                         Plugin Process                           │
│                                                                  │
│  ┌──────────────────────────────────────────────────────────┐   │
│  │              Connect Health Server                         │   │
│  │                                                            │   │
│  │   statusMap: map[service]*status                           │   │
│  │   watchers: map[service][]chan                             │   │
│  │                                                            │   │
│  │   SetServingStatus("kv", SERVING)                          │   │
│  │   SetServingStatus("kv", NOT_SERVING, WithError(err))      │   │
│  └──────────────────────────────────────────────────────────┘   │
│                                                                  │
│  ┌──────────────────────────────────────────────────────────┐   │
│  │                    HTTP Endpoints                          │   │
│  │                                                            │   │
│  │   /healthz  → Liveness (always 200 if process alive)       │   │
│  │   /readyz   → Readiness (checks actual service health)     │   │
│  └──────────────────────────────────────────────────────────┘   │
└─────────────────────────────────────────────────────────────────┘
```

## Comparison: Watch vs Poll

| Aspect | Watch | Poll |
|--------|-------|------|
| **Latency** | Immediate updates | Delayed by interval |
| **Resource** | One connection | Per-check connection |
| **Complexity** | Higher (stream mgmt) | Lower |
| **Recovery** | Stream reconnection | Simple retry |
| **Best for** | Real-time needs | Simple monitoring |

## Integration Points

### Plugin Server

```go
func Serve(cfg *ServeConfig) error {
    health := health.NewServer()

    // Register health service
    mux := http.NewServeMux()
    mux.Handle(healthv1connect.NewHealthServiceHandler(health))

    // Register plugin services
    for name, plugin := range cfg.Plugins {
        plugin.RegisterServer(mux)
        health.SetServingStatus(name, health.ServingStatus_SERVING)
    }

    // HTTP health endpoints for K8s
    mux.Handle("/", health.NewHTTPHandler(health))

    // Graceful shutdown
    srv := &http.Server{Addr: cfg.Addr, Handler: mux}

    go func() {
        <-cfg.StopCh
        health.Shutdown()
        srv.Shutdown(context.Background())
    }()

    return srv.ListenAndServe()
}
```

### Plugin Client

```go
func (c *Client) Connect(ctx context.Context) error {
    // Create health client
    c.healthClient = healthv1connect.NewHealthServiceClient(c.httpClient, c.endpoint)

    // Start health monitoring
    c.healthMonitor = health.NewMonitor(c.healthClient, health.MonitorConfig{
        CheckInterval:    c.cfg.HealthCheckInterval,
        FailureThreshold: 3,
        SuccessThreshold: 1,
    })
    c.healthMonitor.Start(ctx)

    // Integrate with circuit breaker
    if c.circuitBreaker != nil {
        c.healthMonitor.OnStatusChange(func(status health.ServingStatus) {
            if status == health.ServingStatus_NOT_SERVING {
                c.circuitBreaker.Trip()
            }
        })
    }

    return nil
}
```

## Conclusions

1. **gRPC health protocol** is the standard - implement compatible Check/Watch/List
2. **Watch-based monitoring** is preferred for real-time health tracking
3. **Threshold logic** (consecutive failures) prevents flapping
4. **HTTP endpoints** needed for Kubernetes probes
5. **Circuit breaker integration** should react to health status changes
6. **Per-service health** enables granular status tracking

## Next Steps

1. Define health.proto compatible with grpc.health.v1
2. Implement Health server for plugin side
3. Implement Health monitor for client side
4. Add HTTP endpoints for Kubernetes
5. Integrate with circuit breaker
6. Add health status to discovery (ready filtering)
