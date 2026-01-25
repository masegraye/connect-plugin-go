# Design: Server Configuration & Lifecycle

**Issue:** KOR-koba
**Status:** Complete
**Dependencies:** KOR-fzki, KOR-ejeu

## Overview

The server configuration defines how plugins are served over HTTP. Unlike go-plugin which launches subprocesses, connect-plugin servers are long-running HTTP services that expose multiple plugins, handle graceful shutdown, and integrate with health checking and handshake protocols.

## Design Principles (Post-Review)

This design was simplified based on review feedback:

1. **No magic delays**: Removed hard-coded 2s sleep. Rely on Kubernetes `terminationGracePeriodSeconds` for proper shutdown timing.

2. **Single Cleanup function**: Replaced `OnShutdown []func(context.Context) error` with single `Cleanup func(context.Context) error`. Simpler API, errors are logged but don't stop shutdown.

3. **Explicit service registration**: Health and handshake services must be explicitly set in config (not automatic). Makes the API predictable and removes "magic" behavior.

4. **HTTP/2 GOAWAY automatic**: Go's `http.Server.Shutdown()` handles GOAWAY frames automatically. No manual HTTP/2 connection draining needed.

5. **No multi-version support in v1**: Removed `VersionedPlugins` and `VersionedImpls`. v1 supports single protocol version only. Multi-version support deferred to future release.

6. **No plugin shutdown ordering**: v1 has no dependency graph. All plugins shut down simultaneously. If order matters, handle in Cleanup function.

## Design Goals

1. **Simple defaults**: Minimal config for common cases
2. **Multi-plugin**: Multiple plugins on one server
3. **Graceful shutdown**: Kubernetes-friendly lifecycle
4. **Explicit services**: Health and handshake registered explicitly
5. **Observable**: Logging and metrics integration

## ServeConfig Structure

```go
// ServeConfig configures a plugin server.
type ServeConfig struct {
    // ===== Plugins & Implementations =====

    // Plugins defines the plugin types this server provides.
    // Key = plugin name (e.g., "kv", "auth")
    Plugins PluginSet

    // Impls maps plugin names to their implementations.
    // The impl is passed to Plugin.ConnectServer().
    // Key must match a key in Plugins.
    Impls map[string]any

    // ProtocolVersion is the application protocol version this server implements.
    // Used during handshake negotiation.
    // Default: 1
    // Note: v1 only supports single version. Multi-version support deferred.
    ProtocolVersion int

    // ===== Protocol Configuration =====

    // MagicCookieKey and Value for validation (not security).
    // Must match client's expectation.
    // Default: DefaultMagicCookieKey/Value
    MagicCookieKey   string
    MagicCookieValue string

    // ===== Server Configuration =====

    // Addr is the address to listen on.
    // Examples: ":8080", "0.0.0.0:8080", "localhost:8080"
    // Default: ":8080"
    Addr string

    // TLSConfig for HTTPS.
    // If nil, serves over HTTP.
    TLSConfig *tls.Config

    // HTTPServer allows customizing the http.Server.
    // If nil, creates default server.
    HTTPServer *http.Server

    // ===== Lifecycle =====

    // GracefulShutdownTimeout is max time for graceful shutdown.
    // After timeout, forces shutdown.
    // Default: 30 seconds
    // Relies on Kubernetes terminationGracePeriodSeconds, not internal delays.
    GracefulShutdownTimeout time.Duration

    // Cleanup is called during graceful shutdown before server stops.
    // Use for closing resources (DB connections, caches, etc).
    // Context has GracefulShutdownTimeout deadline.
    // If Cleanup returns error, it is logged but shutdown continues.
    Cleanup func(context.Context) error

    // StopCh signals server shutdown.
    // Server listens on this channel and initiates graceful shutdown.
    // If nil, server runs until killed (SIGTERM/SIGINT).
    StopCh <-chan struct{}

    // ===== Capabilities =====

    // HostCapabilities are capabilities the host provides.
    // Advertised during handshake and served via capability broker.
    HostCapabilities map[string]CapabilityHandler

    // ===== Health =====

    // HealthService manages health status for plugins.
    // Must be explicitly set to enable health checking.
    // Call ServeHealthService() in your plugin to register it.
    // Set to nil to disable health service.
    HealthService *health.Server

    // ===== Handshake =====

    // HandshakeService handles protocol negotiation.
    // Must be explicitly set to enable handshake.
    // Call ServeHandshakeService() in your plugin to register it.
    // Set to nil to disable handshake (only for testing/dev).
    HandshakeService HandshakeServer

    // ===== Observability =====

    // Logger for server operations.
    // If nil, uses default logger.
    Logger Logger

    // Interceptors for all served plugins.
    // Applied in order (first = outermost).
    Interceptors []connect.Interceptor

    // ===== Metadata =====

    // ServerName identifies this server instance.
    // Sent in handshake metadata.
    ServerName string

    // ServerMetadata is custom metadata sent in handshake.
    ServerMetadata map[string]string
}
```

## Default Configuration

```go
// DefaultServeConfig returns sensible defaults.
func DefaultServeConfig() *ServeConfig {
    return &ServeConfig{
        Addr:                    ":8080",
        ProtocolVersion:         1,
        MagicCookieKey:          DefaultMagicCookieKey,
        MagicCookieValue:        DefaultMagicCookieValue,
        GracefulShutdownTimeout: 30 * time.Second,
    }
}
```

## Minimal Configuration Example

```go
// Simple single-plugin server
func main() {
    cfg := &connectplugin.ServeConfig{
        Plugins: connectplugin.PluginSet{
            "kv": &kvplugin.KVServicePlugin{},
        },
        Impls: map[string]any{
            "kv": &myKVStore{data: make(map[string][]byte)},
        },
    }

    // Explicitly register handshake service (required for clients)
    cfg.HandshakeService = connectplugin.NewHandshakeServer(cfg)

    // Optionally register health service
    cfg.HealthService = health.NewServer()

    connectplugin.Serve(cfg)
}
```

## Multi-Plugin Server

```go
func main() {
    cfg := &connectplugin.ServeConfig{
        Addr: ":8080",
        Plugins: connectplugin.PluginSet{
            "kv":    &kvplugin.KVServicePlugin{},
            "auth":  &authplugin.AuthServicePlugin{},
            "cache": &cacheplugin.CacheServicePlugin{},
        },
        Impls: map[string]any{
            "kv":    &myKVStore{},
            "auth":  &myAuthService{},
            "cache": &myCacheService{},
        },
    }

    // Explicitly register services
    cfg.HandshakeService = connectplugin.NewHandshakeServer(cfg)
    cfg.HealthService = health.NewServer()

    connectplugin.Serve(cfg)
}
```

## Note: Multi-Version Support Deferred

v1 only supports a single protocol version. Multi-version negotiation is deferred to a future release. If you need to support multiple API versions:

1. Run separate servers on different ports
2. Use routing at the infrastructure level
3. Version your service definitions (e.g., `kv.v1.KVService`, `kv.v2.KVService`)

## Server Lifecycle

```
┌─────────────────────────────────────────────────────────────────┐
│                         Serve()                                  │
│                                                                  │
│  1. Validate configuration                                       │
│     ┌─────────────────────────────────────────────────────┐     │
│     │ - Plugins and Impls match                           │     │
│     │ - No path conflicts                                 │     │
│     │ - Magic cookie is set                               │     │
│     └─────────────────────────────────────────────────────┘     │
│                                                                  │
│  2. Create HTTP mux                                              │
│     ┌─────────────────────────────────────────────────────┐     │
│     │ mux := http.NewServeMux()                           │     │
│     └─────────────────────────────────────────────────────┘     │
│                                                                  │
│  3. Register handshake service (if set)                          │
│     ┌─────────────────────────────────────────────────────┐     │
│     │ if cfg.HandshakeService != nil {                    │     │
│     │   path, handler := cfg.HandshakeService.Handler()   │     │
│     │   mux.Handle(path, handler)                         │     │
│     │   // /connectplugin.v1.HandshakeService/*           │     │
│     │ }                                                    │     │
│     └─────────────────────────────────────────────────────┘     │
│                                                                  │
│  4. Register health service (if set)                             │
│     ┌─────────────────────────────────────────────────────┐     │
│     │ if cfg.HealthService != nil {                       │     │
│     │   cfg.HealthService.SetServingStatus("", SERVING)   │     │
│     │   path, handler := cfg.HealthService.Handler()      │     │
│     │   mux.Handle(path, handler)                         │     │
│     │   // /connectplugin.health.v1.HealthService/*       │     │
│     │   mux.HandleFunc("/healthz", livenessHandler)       │     │
│     │   mux.HandleFunc("/readyz", readinessHandler)       │     │
│     │ }                                                    │     │
│     └─────────────────────────────────────────────────────┘     │
│                                                                  │
│  5. Register capability broker (if host capabilities set)        │
│     ┌─────────────────────────────────────────────────────┐     │
│     │ mux.Handle("/capabilities/", capabilityBroker)      │     │
│     └─────────────────────────────────────────────────────┘     │
│                                                                  │
│  6. Register plugin services                                     │
│     ┌─────────────────────────────────────────────────────┐     │
│     │ for name, plugin := range cfg.Plugins {             │     │
│     │   impl := cfg.Impls[name]                           │     │
│     │   path, handler := plugin.ConnectServer(impl)       │     │
│     │   mux.Handle(path, handler)                         │     │
│     │   if cfg.HealthService != nil {                     │     │
│     │     cfg.HealthService.SetServingStatus(name, SERVING)│    │
│     │   }                                                  │     │
│     │ }                                                    │     │
│     └─────────────────────────────────────────────────────┘     │
│                                                                  │
│  7. Create HTTP server                                           │
│     ┌─────────────────────────────────────────────────────┐     │
│     │ srv := &http.Server{                                │     │
│     │   Addr:    cfg.Addr,                                │     │
│     │   Handler: mux,                                     │     │
│     │ }                                                    │     │
│     └─────────────────────────────────────────────────────┘     │
│                                                                  │
│  8. Start listening                                              │
│     ┌─────────────────────────────────────────────────────┐     │
│     │ go srv.ListenAndServe()                             │     │
│     │ log.Printf("Plugin server listening on %s", addr)   │     │
│     └─────────────────────────────────────────────────────┘     │
│                                                                  │
│  9. Wait for shutdown signal                                     │
│     ┌─────────────────────────────────────────────────────┐     │
│     │ <-cfg.StopCh (or SIGTERM/SIGINT)                    │     │
│     └─────────────────────────────────────────────────────┘     │
│                                                                  │
│  10. Graceful shutdown                                           │
│      ┌────────────────────────────────────────────────────┐     │
│      │ See "Graceful Shutdown Sequence" below            │     │
│      └────────────────────────────────────────────────────┘     │
│                                                                  │
└─────────────────────────────────────────────────────────────────┘
```

## Graceful Shutdown Sequence

The shutdown sequence is designed to work with Kubernetes `terminationGracePeriodSeconds`. No internal sleep delays are used. Kubernetes handles the delay between marking unhealthy and sending SIGTERM.

```
┌─────────────────────────────────────────────────────────────────┐
│                   Graceful Shutdown                              │
│                                                                  │
│  1. Set health status to NOT_SERVING (if health enabled)         │
│     ┌─────────────────────────────────────────────────────┐     │
│     │ if cfg.HealthService != nil {                       │     │
│     │   cfg.HealthService.Shutdown()                      │     │
│     │   // All plugins now report NOT_SERVING             │     │
│     │ }                                                    │     │
│     │ // K8s handles the delay (terminationGracePeriod)   │     │
│     │ // before sending SIGTERM                           │     │
│     └─────────────────────────────────────────────────────┘     │
│                                                                  │
│  2. Send HTTP/2 GOAWAY frames                                    │
│     ┌─────────────────────────────────────────────────────┐     │
│     │ // srv.Shutdown() handles this automatically        │     │
│     │ // Tells clients to stop sending new streams        │     │
│     │ // Clients should reconnect elsewhere               │     │
│     └─────────────────────────────────────────────────────┘     │
│                                                                  │
│  3. Call Cleanup function (if set)                               │
│     ┌─────────────────────────────────────────────────────┐     │
│     │ shutdownCtx, cancel := context.WithTimeout(         │     │
│     │   context.Background(),                             │     │
│     │   cfg.GracefulShutdownTimeout,                      │     │
│     │ )                                                    │     │
│     │ defer cancel()                                       │     │
│     │                                                      │     │
│     │ if cfg.Cleanup != nil {                             │     │
│     │   if err := cfg.Cleanup(shutdownCtx); err != nil {  │     │
│     │     log.Printf("Cleanup error: %v", err)            │     │
│     │   }                                                  │     │
│     │ }                                                    │     │
│     └─────────────────────────────────────────────────────┘     │
│                                                                  │
│  4. Stop accepting new requests and drain connections            │
│     ┌─────────────────────────────────────────────────────┐     │
│     │ srv.Shutdown(shutdownCtx)                           │     │
│     │ // Stops accepting new connections                  │     │
│     │ // Waits for active requests to complete            │     │
│     │ // Times out after GracefulShutdownTimeout (30s)    │     │
│     └─────────────────────────────────────────────────────┘     │
│                                                                  │
│  5. Log shutdown complete or timeout                             │
│     ┌─────────────────────────────────────────────────────┐     │
│     │ if ctx.Err() == context.DeadlineExceeded {          │     │
│     │   log.Printf("Shutdown timeout, forcing close")     │     │
│     │ } else {                                             │     │
│     │   log.Printf("Server shutdown complete")            │     │
│     │ }                                                    │     │
│     └─────────────────────────────────────────────────────┘     │
│                                                                  │
└─────────────────────────────────────────────────────────────────┘

**HTTP/2 Connection Draining:**
- srv.Shutdown() automatically sends GOAWAY frames on HTTP/2 connections
- GOAWAY tells clients: "stop sending new streams, finish active ones"
- Clients receive GOAWAY and should reconnect to other pods
- No manual GOAWAY handling needed; Go's http.Server handles it

**Kubernetes Integration:**
- Configure terminationGracePeriodSeconds (e.g., 30s-60s)
- K8s marks pod unhealthy, waits, then sends SIGTERM
- Server's GracefulShutdownTimeout should be less than K8s period
- Example: K8s 60s, Server 30s gives 30s buffer

**Plugin Shutdown Order:**
- v1 has no dependency graph support
- All plugins shut down simultaneously
- If order matters, handle it in your Cleanup function
```

## Signal Handling

```go
func Serve(cfg *ServeConfig) error {
    // Set up signal handling if StopCh not provided
    if cfg.StopCh == nil {
        sigCh := make(chan os.Signal, 1)
        signal.Notify(sigCh, os.Interrupt, syscall.SIGTERM)
        cfg.StopCh = sigCh
    }

    // ... setup server

    // Wait for shutdown
    <-cfg.StopCh

    // Graceful shutdown
    return gracefulShutdown(srv, cfg)
}
```

## Cleanup Function

Use the `Cleanup` function to close resources during graceful shutdown.

```go
func main() {
    db, _ := sql.Open("postgres", dsn)
    cache := redis.NewClient(&redis.Options{Addr: "localhost:6379"})
    metricsClient := metrics.NewClient()

    cfg := &connectplugin.ServeConfig{
        Plugins: pluginSet,
        Impls:   impls,

        // Single cleanup function for all shutdown tasks
        Cleanup: func(ctx context.Context) error {
            var errs []error

            // Close database connections
            if err := db.Close(); err != nil {
                errs = append(errs, fmt.Errorf("close db: %w", err))
            }

            // Close cache connections
            if err := cache.Close(); err != nil {
                errs = append(errs, fmt.Errorf("close cache: %w", err))
            }

            // Flush metrics (respects context timeout)
            if err := metricsClient.Flush(ctx); err != nil {
                errs = append(errs, fmt.Errorf("flush metrics: %w", err))
            }

            // Return combined errors (logged, doesn't stop shutdown)
            if len(errs) > 0 {
                return errors.Join(errs...)
            }
            return nil
        },
    }

    cfg.HandshakeService = connectplugin.NewHandshakeServer(cfg)
    cfg.HealthService = health.NewServer()

    connectplugin.Serve(cfg)
}
```

## Health Service Integration

The health service must be explicitly created and configured. Serve() registers it if cfg.HealthService is set.

```go
func main() {
    // Create health service explicitly
    healthService := health.NewServer()

    cfg := &connectplugin.ServeConfig{
        Plugins:       pluginSet,
        Impls:         impls,
        HealthService: healthService, // Explicitly set
    }

    cfg.HandshakeService = connectplugin.NewHandshakeServer(cfg)

    connectplugin.Serve(cfg)
}

// During Serve(), if cfg.HealthService != nil:
// 1. Set overall health to SERVING
//    cfg.HealthService.SetServingStatus("", health.ServingStatusServing)
//
// 2. Set per-plugin health to SERVING
//    for name := range cfg.Plugins {
//        cfg.HealthService.SetServingStatus(name, health.ServingStatusServing)
//    }
//
// 3. Register Connect service
//    path, handler := healthv1connect.NewHealthServiceHandler(cfg.HealthService)
//    mux.Handle(path, handler)
//
// 4. Register HTTP endpoints for Kubernetes
//    mux.HandleFunc("/healthz", livenessHandler)  // Always 200
//    mux.HandleFunc("/readyz", readinessHandler)  // Checks health status
```

### Kubernetes Health Endpoints

```go
// Liveness: always OK if process is running
func livenessHandler(w http.ResponseWriter, r *http.Request) {
    w.WriteHeader(http.StatusOK)
    w.Write([]byte("ok"))
}

// Readiness: check actual health status
func readinessHandler(w http.ResponseWriter, r *http.Request) {
    resp, err := cfg.HealthService.Check(r.Context(),
        &healthv1.HealthCheckRequest{Service: ""})

    if err != nil || resp.Status != healthv1.HealthCheckResponse_SERVING {
        w.WriteHeader(http.StatusServiceUnavailable)
        w.Write([]byte("not ready"))
        return
    }

    w.WriteHeader(http.StatusOK)
    w.Write([]byte("ready"))
}
```

### Plugin Health Control

Plugins can update their own health status at runtime:

```go
type myKVStore struct {
    health *health.Server
    name   string
}

func (kv *myKVStore) Put(ctx context.Context, key string, value []byte) error {
    if err := kv.backend.Put(key, value); err != nil {
        // Mark this specific plugin unhealthy
        kv.health.SetServingStatus(kv.name, health.ServingStatusNotServing)
        return err
    }
    return nil
}

func (kv *myKVStore) HealthCheck() error {
    if err := kv.backend.Ping(); err != nil {
        kv.health.SetServingStatus(kv.name, health.ServingStatusNotServing)
        return err
    }
    kv.health.SetServingStatus(kv.name, health.ServingStatusServing)
    return nil
}
```

**Notes:**
- Health service is optional (set cfg.HealthService = nil to disable)
- If disabled, no /healthz or /readyz endpoints are registered
- For production, always enable health checks for proper K8s integration

## Handshake Service Integration

The handshake service must be explicitly created and configured. Serve() registers it if cfg.HandshakeService is set.

```go
func main() {
    cfg := &connectplugin.ServeConfig{
        Plugins: pluginSet,
        Impls:   impls,
    }

    // Explicitly create handshake service
    cfg.HandshakeService = connectplugin.NewHandshakeServer(cfg)

    connectplugin.Serve(cfg)
}

// During Serve(), if cfg.HandshakeService != nil:
//   path, handler := cfg.HandshakeService.Handler()
//   mux.Handle(path, handler)
//   // Serves at: /connectplugin.v1.HandshakeService/Handshake
```

### Handshake Server Implementation

The handshake server validates clients and negotiates protocol versions:

```go
type handshakeServer struct {
    cfg *ServeConfig
}

func (s *handshakeServer) Handshake(
    ctx context.Context,
    req *connect.Request[connectpluginv1.HandshakeRequest],
) (*connect.Response[connectpluginv1.HandshakeResponse], error) {

    // 1. Validate magic cookie
    if req.Msg.MagicCookieKey != s.cfg.MagicCookieKey ||
        req.Msg.MagicCookieValue != s.cfg.MagicCookieValue {
        return nil, connect.NewError(connect.CodeInvalidArgument,
            errors.New("invalid magic cookie"))
    }

    // 2. Negotiate version (v1: only supports single version)
    clientVersion := req.Msg.AppProtocolVersion
    if clientVersion != s.cfg.ProtocolVersion {
        return nil, connect.NewError(connect.CodeFailedPrecondition,
            fmt.Errorf("version mismatch: server=%d, client=%d",
                s.cfg.ProtocolVersion, clientVersion))
    }

    // 3. Build plugin info
    plugins := make(map[string]*connectpluginv1.PluginInfo)
    for name, plugin := range s.cfg.Plugins {
        path, _ := plugin.ConnectServer(nil) // Get path only
        plugins[name] = &connectpluginv1.PluginInfo{
            Name: name,
            Path: path,
        }
    }

    // 4. Return handshake response
    return connect.NewResponse(&connectpluginv1.HandshakeResponse{
        CoreProtocolVersion: 1,
        AppProtocolVersion:  s.cfg.ProtocolVersion,
        Plugins:             plugins,
        ServerMetadata:      s.cfg.ServerMetadata,
    }), nil
}
```

**Notes:**
- Handshake service is required for production (clients need plugin discovery)
- Can be disabled (cfg.HandshakeService = nil) for testing/development
- v1 only supports exact version match (no negotiation)
- Magic cookie is a basic validation, not security

## Capability Broker Integration

Host capabilities are automatically exposed via the broker:

```go
func main() {
    connectplugin.Serve(&connectplugin.ServeConfig{
        Plugins: pluginSet,
        Impls:   impls,

        // Register host capabilities
        HostCapabilities: map[string]connectplugin.CapabilityHandler{
            "secrets": &SecretsCapability{
                vault: vaultClient,
            },
            "filesystem": &FilesystemCapability{
                allowedPaths: []string{"/data", "/tmp"},
            },
        },
    })
}

// Automatic routing
// POST /capabilities/secrets/grant-123/GetSecret
// → validates token → routes to SecretsCapability
```

## Kubernetes Deployment Example

```yaml
apiVersion: apps/v1
kind: Deployment
metadata:
  name: kv-plugin
spec:
  replicas: 3
  selector:
    matchLabels:
      app: kv-plugin
  template:
    metadata:
      labels:
        app: kv-plugin
    spec:
      containers:
      - name: kv-plugin
        image: myorg/kv-plugin:v1.0.0
        ports:
        - containerPort: 8080
          name: http

        # Health probes
        livenessProbe:
          httpGet:
            path: /healthz
            port: 8080
          initialDelaySeconds: 5
          periodSeconds: 10

        readinessProbe:
          httpGet:
            path: /readyz
            port: 8080
          initialDelaySeconds: 5
          periodSeconds: 5

        # Graceful termination
        terminationGracePeriodSeconds: 60

        env:
        - name: PLUGIN_ADDR
          value: ":8080"
        - name: SHUTDOWN_GRACE_PERIOD
          value: "30s"

---
apiVersion: v1
kind: Service
metadata:
  name: kv-plugin
spec:
  selector:
    app: kv-plugin
  ports:
  - port: 8080
    targetPort: 8080
    name: http
```

## Configuration Validation

```go
// Validate checks ServeConfig for errors.
func (cfg *ServeConfig) Validate() error {
    // 1. Check plugins and impls are set
    if cfg.Plugins == nil {
        return errors.New("Plugins must be set")
    }

    if cfg.Impls == nil {
        return errors.New("Impls must be set")
    }

    // 2. Check all plugins have implementations
    for name := range cfg.Plugins {
        if _, ok := cfg.Impls[name]; !ok {
            return fmt.Errorf("no implementation for plugin %q", name)
        }
    }

    // 3. Check all impls have plugins
    for name := range cfg.Impls {
        if _, ok := cfg.Plugins[name]; !ok {
            return fmt.Errorf("no plugin definition for impl %q", name)
        }
    }

    // 4. Check for path conflicts
    paths := make(map[string]string)
    for name, plugin := range cfg.Plugins {
        path, _ := plugin.ConnectServer(nil) // Get path only
        if existing, ok := paths[path]; ok {
            return fmt.Errorf("path conflict: plugins %q and %q both use %q",
                name, existing, path)
        }
        paths[path] = name
    }

    // 5. Validate magic cookie is set
    if cfg.MagicCookieKey == "" || cfg.MagicCookieValue == "" {
        return errors.New("MagicCookieKey and MagicCookieValue must be set")
    }

    // 6. Validate protocol version
    if cfg.ProtocolVersion < 1 {
        return errors.New("ProtocolVersion must be >= 1")
    }

    return nil
}
```

## Interceptor Composition

Server-side interceptors wrap all plugin handlers:

```go
func main() {
    connectplugin.Serve(&connectplugin.ServeConfig{
        Plugins: pluginSet,
        Impls:   impls,

        // Applied to all plugin handlers
        Interceptors: []connect.Interceptor{
            loggingInterceptor,
            authInterceptor,
            metricsInterceptor,
            recoverInterceptor, // Panic recovery
        },
    })
}
```

## Comparison with go-plugin

| Aspect | go-plugin | connect-plugin |
|--------|-----------|----------------|
| **Process Model** | Subprocess per plugin | HTTP server |
| **Multi-plugin** | One plugin per process | Multiple plugins per server |
| **Lifecycle** | Subprocess management | HTTP server lifecycle |
| **Shutdown** | Kill subprocess | Graceful HTTP shutdown |
| **Health** | Implicit (process alive) | Explicit health service |
| **K8s Integration** | Complex | Native (HTTP probes) |
| **Port Allocation** | Dynamic range | Fixed port |
| **Versioning** | Client-side negotiation | Server advertises support |

## Implementation Checklist

- [x] ServeConfig structure (simplified)
- [x] Default configuration
- [x] Lifecycle diagram
- [x] Graceful shutdown sequence (no sleep, K8s-first)
- [x] Health service integration (explicit)
- [x] Handshake service integration (explicit)
- [x] Capability broker integration
- [x] Multi-plugin support
- [x] Single Cleanup function (not OnShutdown array)
- [x] HTTP/2 GOAWAY handling notes
- [x] Kubernetes deployment pattern
- [x] Configuration validation (simplified)
- [x] Remove VersionedPlugins (deferred to future)

## Next Steps

1. Implement ServeConfig in `serve.go`
2. Implement handshake server
3. Implement graceful shutdown logic
4. Integrate health service
5. Integrate capability broker
6. Design streaming adapters (KOR-uxvj)
