# Deployment Models

connect-plugin-go supports two distinct deployment models for plugins. Understanding these models is crucial for choosing the right architecture for your use case.

## Overview

| Aspect | Managed Deployment | Unmanaged Deployment |
|--------|---------------------------|---------------------------|
| **Who starts plugins?** | Host platform | External orchestrator (k8s, docker-compose) |
| **Who initiates handshake?** | Host calls plugin | Plugin calls host |
| **Identity assignment** | Host calls SetRuntimeIdentity() | Host responds to Handshake() |
| **Registration** | Plugin registers after identity assigned | Plugin registers immediately |
| **Use case** | Traditional plugins, local dev | Microservices, cloud-native |
| **Plugin role** | Server (plugin exposes RPC) | Client (plugin calls host) |
| **Host role** | Client (calls plugin RPC) | Server (host exposes RPC) |

## Managed Deployment Plugins

### Architecture

The host platform orchestrates the complete plugin lifecycle.

```
┌──────────────┐                    ┌──────────────┐
│     Host     │                    │    Plugin    │
│   Platform   │                    │   Process    │
└──────┬───────┘                    └──────┬───────┘
       │                                   │
       │  1. Start plugin process          │
       ├──────────────────────────────────>│
       │                                   │
       │  2. GetPluginInfo()               │
       ├──────────────────────────────────>│
       │  ← {self_id, provides, requires}  │
       │<──────────────────────────────────┤
       │                                   │
       │  3. Generate runtime_id, token    │
       │                                   │
       │  4. SetRuntimeIdentity()          │
       ├──────────────────────────────────>│
       │  ← acknowledged                   │
       │<──────────────────────────────────┤
       │                                   │
       │  5. RegisterService()             │
       │<──────────────────────────────────┤
       │                                   │
       │  6. ReportHealth()                │
       │<──────────────────────────────────┤
       │                                   │
```

### Host Implementation

```go
// Create platform
platform := connectplugin.NewPlatform(registry, lifecycle, router)

// Add plugin (platform orchestrates everything)
err := platform.AddPlugin(ctx, connectplugin.PluginConfig{
    SelfID:      "cache-plugin",
    SelfVersion: "1.0.0",
    Endpoint:    "http://localhost:8082",
    Metadata: connectplugin.PluginMetadata{
        Name:    "Cache",
        Version: "1.0.0",
        Provides: []connectplugin.ServiceDeclaration{
            {Type: "cache", Version: "1.0.0", Path: "/cache.v1.Cache/"},
        },
        Requires: []connectplugin.ServiceDependency{
            {Type: "logger", MinVersion: "1.0.0", RequiredForStartup: true},
        },
    },
})
```

### Plugin Implementation

Plugin must implement `PluginIdentity` service:

```go
type pluginIdentityHandler struct {
    client   *connectplugin.Client
    metadata connectplugin.PluginMetadata
}

func (h *pluginIdentityHandler) GetPluginInfo(
    ctx context.Context,
    req *connect.Request[connectpluginv1.GetPluginInfoRequest],
) (*connect.Response[connectpluginv1.GetPluginInfoResponse], error) {
    cfg := h.client.Config()

    return connect.NewResponse(&connectpluginv1.GetPluginInfoResponse{
        SelfId:      cfg.SelfID,
        SelfVersion: cfg.SelfVersion,
        Provides:    convertProvides(cfg.Metadata.Provides),
        Requires:    convertRequires(cfg.Metadata.Requires),
    }), nil
}

func (h *pluginIdentityHandler) SetRuntimeIdentity(
    ctx context.Context,
    req *connect.Request[connectpluginv1.SetRuntimeIdentityRequest],
) (*connect.Response[connectpluginv1.SetRuntimeIdentityResponse], error) {
    // Store runtime identity
    h.client.SetRuntimeIdentity(
        req.Msg.RuntimeId,
        req.Msg.RuntimeToken,
        req.Msg.HostUrl,
    )

    // Register services after identity assigned
    go registerServices(h.client)

    return connect.NewResponse(&connectpluginv1.SetRuntimeIdentityResponse{
        Acknowledged: true,
    }), nil
}
```

### Detection Pattern

Plugins can support both models with environment variable detection:

```go
hostURL := os.Getenv("HOST_URL")

if hostURL == "" {
    // Managed: Wait for host to call SetRuntimeIdentity
    log.Println("Running in Managed (platform-managed)")
} else {
    // Unmanaged: Connect to host immediately
    client.Connect(ctx)
    registerServices(client)
}
```

### When to Use Managed

✅ **Good for:**

- Local development and testing
- Trusted, first-party plugins
- Traditional plugin architectures
- When host controls plugin binaries and lifecycle
- Coordinated startup with dependencies

❌ **Not ideal for:**

- Container orchestration (k8s)
- Untrusted third-party plugins
- Distributed deployments
- Plugins that start independently

## Unmanaged Deployment Plugins

### Architecture

Plugins connect to the host independently, like microservices.

```
┌──────────────┐                    ┌──────────────┐
│   External   │                    │    Plugin    │
│ Orchestrator │                    │   Process    │
│  (k8s, etc)  │                    │              │
└──────┬───────┘                    └──────┬───────┘
       │                                   │
       │  1. Start plugin process          │
       ├──────────────────────────────────>│
       │                                   │
                                           │
                            ┌──────────────┴───────────┐
                            │         Host Platform    │
                            └──────────────┬───────────┘
                                           │
       │  2. Handshake(self_id)            │
       │<──────────────────────────────────┤
       │  → {runtime_id, token}            │
       ├──────────────────────────────────>│
       │                                   │
       │  3. RegisterService()             │
       │<──────────────────────────────────┤
       │                                   │
       │  4. ReportHealth()                │
       │<──────────────────────────────────┤
       │                                   │
```

### Host Implementation

Host is a pure server - plugins connect to it:

```go
// Create host server components
handshake := connectplugin.NewHandshakeServer(&connectplugin.ServeConfig{})
lifecycle := connectplugin.NewLifecycleServer()
registry := connectplugin.NewServiceRegistry(lifecycle)
router := connectplugin.NewServiceRouter(handshake, registry, lifecycle)

// Register services
mux := http.NewServeMux()
mux.Handle(connectpluginv1connect.NewHandshakeServiceHandler(handshake))
mux.Handle(connectpluginv1connect.NewPluginLifecycleHandler(lifecycle))
mux.Handle(connectpluginv1connect.NewServiceRegistryHandler(registry))
mux.Handle("/services/", router)

// Start server
http.ListenAndServe(":8080", mux)
```

### Plugin Implementation

Plugin is a pure client - connects to host:

```go
func main() {
    hostURL := os.Getenv("HOST_URL") // e.g., "http://localhost:8080"

    // Create client
    client, _ := connectplugin.NewClient(connectplugin.ClientConfig{
        HostURL:     hostURL,
        SelfID:      "cache-plugin",
        SelfVersion: "1.0.0",
        Metadata: connectplugin.PluginMetadata{
            Name:    "Cache",
            Version: "1.0.0",
            Provides: []connectplugin.ServiceDeclaration{
                {Type: "cache", Version: "1.0.0", Path: "/cache.v1.Cache/"},
            },
        },
    })

    // Connect to host (handshake, get runtime_id)
    client.Connect(context.Background())

    log.Printf("Connected with runtime_id: %s", client.RuntimeID())

    // Register services
    regClient := client.RegistryClient()
    for _, svc := range client.Config().Metadata.Provides {
        regClient.RegisterService(ctx, &connectpluginv1.RegisterServiceRequest{
            ServiceType:  svc.Type,
            Version:      svc.Version,
            EndpointPath: svc.Path,
        })
    }

    // Report healthy
    client.ReportHealth(ctx,
        connectpluginv1.HealthState_HEALTH_STATE_HEALTHY,
        "ready",
        nil,
    )

    // Start serving (for other plugins to call)
    http.ListenAndServe(":8082", pluginHandler)
}
```

### Docker Compose Deployment

**Complete working example in `examples/docker-compose/`:**

```bash
cd examples/docker-compose
./setup.sh && ./run.sh && ./test.sh
```

**docker-compose.yml structure:**

```yaml
services:
  host:
    build: ./Dockerfile.host
    ports: ["8080:8080"]
    healthcheck:
      test: ["CMD", "wget", "-q", "-O-", "http://localhost:8080/health"]

  logger:
    build: ./Dockerfile.logger
    environment:
      HOST_URL: http://host:8080
    depends_on:
      host:
        condition: service_healthy
    # NOT depends_on: []  - logger has no plugin dependencies

  storage:
    build: ./Dockerfile.storage
    environment:
      HOST_URL: http://host:8080
    depends_on:
      host:
        condition: service_healthy
    # NOT depends_on: [logger]  - Compose ignorant of this!

  api:
    build: ./Dockerfile.api
    environment:
      HOST_URL: http://host:8080
    depends_on:
      host:
        condition: service_healthy
    # NOT depends_on: [storage]  - Compose ignorant of this!
```

**See [Docker Compose Guide](../guides/docker-compose.md) for complete details.**

**Kubernetes:**

```yaml
apiVersion: v1
kind: Service
metadata:
  name: plugin-host
spec:
  selector:
    app: plugin-host
  ports:
    - port: 8080
---
apiVersion: apps/v1
kind: Deployment
metadata:
  name: cache-plugin
spec:
  replicas: 2
  template:
    spec:
      containers:
      - name: cache
        image: myapp/cache-plugin
        env:
        - name: HOST_URL
          value: "http://plugin-host:8080"
        - name: PORT
          value: "8082"
```

### When to Use Unmanaged

✅ **Good for:**

- Kubernetes and container orchestration
- Microservices architectures
- Cloud-native deployments
- Third-party or untrusted plugins
- Independent plugin scaling
- Service mesh integration

❌ **Not ideal for:**

- Simple local development (more setup)
- When host needs to control plugin startup order
- Single-binary deployments

## Comparison Table

| Feature | Managed | Unmanaged |
|---------|---------|---------|
| **Startup coordination** | Host controls | External orchestrator |
| **Dependency ordering** | Platform.AddPlugin() validates | Plugins handle degradation |
| **Hot reload** | Platform.ReplacePlugin() | External orchestrator restarts |
| **Impact analysis** | Platform.GetImpact() | External monitoring |
| **Plugin discovery** | Platform knows all plugins | Plugins self-register |
| **Health tracking** | Platform tracks | Both models track |
| **Service discovery** | Both models support | Both models support |
| **Deployment complexity** | Lower (host manages all) | Higher (orchestrator needed) |
| **Scalability** | Limited (single host) | High (independent scaling) |

## Hybrid Approach

You can mix both models in the same deployment:

- **Core plugins**: Platform-managed (Managed) for trusted, critical plugins
- **Extension plugins**: Self-registering (Unmanaged) for third-party or scaled plugins

```go
// Core plugins managed by platform
platform.AddPlugin(ctx, corePluginConfig)

// Extension plugins self-register (host just tracks them)
// Host exposes registry/lifecycle services, plugins connect independently
```

## Migration Path

**Start with Managed** for simplicity:
- Easier to reason about
- Less infrastructure needed
- Better for development

**Migrate to Unmanaged** for production:
- More scalable
- Better fault isolation
- Kubernetes-friendly

The plugin code remains the same - only the startup orchestration changes.

## Next Steps

- [Quick Start](quickstart.md) - Basic setup (works with both models)
- [Service Registry Guide](../guides/service-registry.md) - Plugin-to-plugin communication
- [Configuration Reference](../reference/configuration.md) - Detailed config options
