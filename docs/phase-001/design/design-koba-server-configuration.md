# Design: Server Configuration & Lifecycle

**Issue:** KOR-koba
**Status:** Complete
**Dependencies:** KOR-fzki, KOR-ejeu

## Overview

The server configuration defines how plugins are served over HTTP. Unlike go-plugin which launches subprocesses, connect-plugin servers are long-running HTTP services that expose multiple plugins, handle graceful shutdown, and integrate with health checking and handshake protocols.

## Design Goals

1. **Simple defaults**: Minimal config for common cases
2. **Multi-plugin**: Multiple plugins on one server
3. **Graceful shutdown**: Kubernetes-friendly lifecycle
4. **Automatic services**: Health and handshake built-in
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

    // VersionedPlugins maps protocol versions to plugin sets.
    // Used for supporting multiple plugin API versions.
    // Overrides Plugins if set.
    VersionedPlugins map[int]PluginSet

    // VersionedImpls maps version -> plugin name -> implementation.
    // Used with VersionedPlugins.
    VersionedImpls map[int]map[string]any

    // SupportedVersions are the app protocol versions this server supports.
    // Negotiated during handshake.
    // Default: []int{1}
    SupportedVersions []int

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
    GracefulShutdownTimeout time.Duration

    // OnShutdown callbacks are called during graceful shutdown.
    // Called in order before server stops accepting requests.
    OnShutdown []func(context.Context) error

    // StopCh signals server shutdown.
    // Server listens on this channel and initiates graceful shutdown.
    // If nil, server runs until killed.
    StopCh <-chan struct{}

    // ===== Capabilities =====

    // HostCapabilities are capabilities the host provides.
    // Advertised during handshake and served via capability broker.
    HostCapabilities map[string]CapabilityHandler

    // ===== Health =====

    // HealthService manages health status for plugins.
    // If nil, creates default health service.
    HealthService *HealthService

    // DisableHealthService prevents automatic health endpoint.
    // Default: false (health service enabled)
    DisableHealthService bool

    // ===== Handshake =====

    // DisableHandshakeService prevents automatic handshake endpoint.
    // Only disable if you know clients will skip handshake.
    // Default: false (handshake service enabled)
    DisableHandshakeService bool

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
        SupportedVersions:       []int{1},
        MagicCookieKey:          DefaultMagicCookieKey,
        MagicCookieValue:        DefaultMagicCookieValue,
        GracefulShutdownTimeout: 30 * time.Second,
        DisableHealthService:    false,
        DisableHandshakeService: false,
    }
}
```

## Minimal Configuration Example

```go
// Simple single-plugin server
func main() {
    connectplugin.Serve(&connectplugin.ServeConfig{
        Plugins: connectplugin.PluginSet{
            "kv": &kvplugin.KVServicePlugin{},
        },
        Impls: map[string]any{
            "kv": &myKVStore{data: make(map[string][]byte)},
        },
    })
}
```

## Multi-Plugin Server

```go
func main() {
    connectplugin.Serve(&connectplugin.ServeConfig{
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
    })
}
```

## Versioned Plugins

```go
func main() {
    connectplugin.Serve(&connectplugin.ServeConfig{
        SupportedVersions: []int{1, 2},
        VersionedPlugins: map[int]connectplugin.PluginSet{
            1: {
                "kv": &kvv1plugin.KVServicePlugin{},
            },
            2: {
                "kv": &kvv2plugin.KVServicePlugin{},
            },
        },
        VersionedImpls: map[int]map[string]any{
            1: {
                "kv": &kvV1Impl{},
            },
            2: {
                "kv": &kvV2Impl{}, // Enhanced implementation
            },
        },
    })
}
```

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
│  3. Register handshake service (unless disabled)                 │
│     ┌─────────────────────────────────────────────────────┐     │
│     │ path, handler := handshakeService.Handler()         │     │
│     │ mux.Handle(path, handler)                           │     │
│     │ // /connectplugin.v1.HandshakeService/*             │     │
│     └─────────────────────────────────────────────────────┘     │
│                                                                  │
│  4. Register health service (unless disabled)                    │
│     ┌─────────────────────────────────────────────────────┐     │
│     │ healthService.SetServingStatus("", SERVING)         │     │
│     │ path, handler := healthService.Handler()            │     │
│     │ mux.Handle(path, handler)                           │     │
│     │ // /connectplugin.health.v1.HealthService/*         │     │
│     │ mux.HandleFunc("/healthz", livenessHandler)         │     │
│     │ mux.HandleFunc("/readyz", readinessHandler)         │     │
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
│     │   healthService.SetServingStatus(name, SERVING)     │     │
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

```
┌─────────────────────────────────────────────────────────────────┐
│                   Graceful Shutdown                              │
│                                                                  │
│  1. Set health status to NOT_SERVING                             │
│     ┌─────────────────────────────────────────────────────┐     │
│     │ healthService.Shutdown()                            │     │
│     │ // All plugins now report NOT_SERVING               │     │
│     │ // K8s removes pod from service endpoints           │     │
│     └─────────────────────────────────────────────────────┘     │
│                                                                  │
│  2. Wait for in-flight requests (grace period)                   │
│     ┌─────────────────────────────────────────────────────┐     │
│     │ time.Sleep(2 * time.Second)                         │     │
│     │ // Allow load balancers to detect unhealthy state   │     │
│     └─────────────────────────────────────────────────────┘     │
│                                                                  │
│  3. Call OnShutdown callbacks                                    │
│     ┌─────────────────────────────────────────────────────┐     │
│     │ for _, cb := range cfg.OnShutdown {                 │     │
│     │   cb(shutdownCtx)                                   │     │
│     │ }                                                    │     │
│     └─────────────────────────────────────────────────────┘     │
│                                                                  │
│  4. Stop accepting new requests                                  │
│     ┌─────────────────────────────────────────────────────┐     │
│     │ shutdownCtx, cancel := context.WithTimeout(         │     │
│     │   context.Background(),                             │     │
│     │   cfg.GracefulShutdownTimeout,                      │     │
│     │ )                                                    │     │
│     │ defer cancel()                                       │     │
│     │ srv.Shutdown(shutdownCtx)                           │     │
│     └─────────────────────────────────────────────────────┘     │
│                                                                  │
│  5. Wait for active connections to drain (or timeout)            │
│     ┌─────────────────────────────────────────────────────┐     │
│     │ // Server waits for active requests to complete     │     │
│     │ // Up to GracefulShutdownTimeout (default 30s)      │     │
│     └─────────────────────────────────────────────────────┘     │
│                                                                  │
│  6. Force close if timeout exceeded                              │
│     ┌─────────────────────────────────────────────────────┐     │
│     │ srv.Close() // Force close remaining connections    │     │
│     └─────────────────────────────────────────────────────┘     │
│                                                                  │
│  7. Log shutdown complete                                        │
│     ┌─────────────────────────────────────────────────────┐     │
│     │ log.Printf("Server shutdown complete")              │     │
│     └─────────────────────────────────────────────────────┘     │
│                                                                  │
└─────────────────────────────────────────────────────────────────┘
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

## OnShutdown Callbacks

```go
func main() {
    db, _ := sql.Open("postgres", dsn)

    connectplugin.Serve(&connectplugin.ServeConfig{
        Plugins: pluginSet,
        Impls:   impls,

        OnShutdown: []func(context.Context) error{
            // Close database connections
            func(ctx context.Context) error {
                return db.Close()
            },

            // Flush metrics
            func(ctx context.Context) error {
                return metricsClient.Flush(ctx)
            },

            // Unregister from service registry
            func(ctx context.Context) error {
                return serviceRegistry.Unregister(ctx, "kv-plugin")
            },
        },
    })
}
```

## Health Service Integration

The health service is automatically registered and manages plugin health:

```go
// Automatic registration during Serve()
healthService := health.NewServer()

// Set overall health to SERVING
healthService.SetServingStatus("", health.ServingStatusServing)

// Set per-plugin health
for name := range cfg.Plugins {
    healthService.SetServingStatus(name, health.ServingStatusServing)
}

// Register Connect service
path, handler := healthv1connect.NewHealthServiceHandler(healthService)
mux.Handle(path, handler)

// Register HTTP endpoints for Kubernetes
mux.HandleFunc("/healthz", func(w http.ResponseWriter, r *http.Request) {
    // Liveness: always OK if process is running
    w.WriteHeader(http.StatusOK)
    w.Write([]byte("ok"))
})

mux.HandleFunc("/readyz", func(w http.ResponseWriter, r *http.Request) {
    // Readiness: check actual health
    resp, err := healthService.Check(r.Context(), &health.HealthCheckRequest{})
    if err != nil || resp.Status != health.ServingStatusServing {
        w.WriteHeader(http.StatusServiceUnavailable)
        return
    }
    w.WriteHeader(http.StatusOK)
    w.Write([]byte("ok"))
})
```

### Manual Health Control

Plugins can update their own health status:

```go
type myKVStore struct {
    health *health.Server
}

func (kv *myKVStore) Put(ctx context.Context, key string, value []byte) error {
    if err := kv.backend.Put(key, value); err != nil {
        // Mark unhealthy if backend fails
        kv.health.SetServingStatus("kv", health.ServingStatusNotServing)
        return err
    }
    return nil
}

func (kv *myKVStore) HealthCheck() error {
    if err := kv.backend.Ping(); err != nil {
        kv.health.SetServingStatus("kv", health.ServingStatusNotServing)
        return err
    }
    kv.health.SetServingStatus("kv", health.ServingStatusServing)
    return nil
}
```

## Handshake Service Integration

The handshake service is automatically registered:

```go
// Automatic registration during Serve()
handshakeService := newHandshakeServer(cfg)

path, handler := connectpluginv1connect.NewHandshakeServiceHandler(handshakeService)
mux.Handle(path, handler)
// Serves at: /connectplugin.v1.HandshakeService/Handshake
```

The handshake server implementation:

```go
type handshakeServer struct {
    cfg *ServeConfig
}

func (s *handshakeServer) Handshake(
    ctx context.Context,
    req *connect.Request[connectpluginv1.HandshakeRequest],
) (*connect.Response[connectpluginv1.HandshakeResponse], error) {

    // Validate magic cookie
    if req.Msg.MagicCookieKey != s.cfg.MagicCookieKey ||
        req.Msg.MagicCookieValue != s.cfg.MagicCookieValue {
        return nil, connect.NewError(connect.CodeInvalidArgument,
            errors.New("invalid magic cookie"))
    }

    // Negotiate version
    negotiatedVersion := negotiateVersion(
        req.Msg.AppProtocolVersions,
        s.cfg.SupportedVersions,
    )
    if negotiatedVersion == 0 {
        return nil, connect.NewError(connect.CodeFailedPrecondition,
            errors.New("no compatible version"))
    }

    // Build plugin info
    plugins := s.buildPluginInfo(negotiatedVersion)

    return connect.NewResponse(&connectpluginv1.HandshakeResponse{
        CoreProtocolVersion: 1,
        AppProtocolVersion:  negotiatedVersion,
        Plugins:             plugins,
        ServerMetadata:      s.cfg.ServerMetadata,
    }), nil
}
```

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

        # Graceful shutdown
        lifecycle:
          preStop:
            exec:
              command: ["/bin/sh", "-c", "sleep 15"]

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
    if cfg.Plugins == nil && cfg.VersionedPlugins == nil {
        return errors.New("either Plugins or VersionedPlugins must be set")
    }

    if cfg.Impls == nil && cfg.VersionedImpls == nil {
        return errors.New("either Impls or VersionedImpls must be set")
    }

    // Check all plugins have implementations
    plugins := cfg.Plugins
    if cfg.VersionedPlugins != nil {
        // Validate all versions
        for version, ps := range cfg.VersionedPlugins {
            impls := cfg.VersionedImpls[version]
            for name := range ps {
                if _, ok := impls[name]; !ok {
                    return fmt.Errorf("version %d: no implementation for plugin %q", version, name)
                }
            }
        }
    } else {
        for name := range plugins {
            if _, ok := cfg.Impls[name]; !ok {
                return fmt.Errorf("no implementation for plugin %q", name)
            }
        }
    }

    // Check for path conflicts
    paths := make(map[string]string)
    for name, plugin := range plugins {
        path, _, err := plugin.ConnectServer(nil)
        if err != nil {
            return fmt.Errorf("plugin %q: %w", name, err)
        }
        if existing, ok := paths[path]; ok {
            return fmt.Errorf("path conflict: plugins %q and %q both use %q",
                name, existing, path)
        }
        paths[path] = name
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

- [x] ServeConfig structure
- [x] Default configuration
- [x] Lifecycle diagram
- [x] Graceful shutdown sequence
- [x] Health service integration
- [x] Handshake service integration
- [x] Capability broker integration
- [x] Multi-plugin support
- [x] Versioned plugin support
- [x] Kubernetes deployment pattern
- [x] Configuration validation

## Next Steps

1. Implement ServeConfig in `serve.go`
2. Implement handshake server
3. Implement graceful shutdown logic
4. Integrate health service
5. Integrate capability broker
6. Design streaming adapters (KOR-uxvj)
