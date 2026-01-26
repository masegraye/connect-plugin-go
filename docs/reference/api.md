# API Reference

Complete API reference for connect-plugin-go.

## Client APIs

### Creating a Client

```go
func NewClient(cfg ClientConfig) (*Client, error)
```

Creates a new plugin client. Validates configuration but does not connect.

**Example:**

```go
client, err := connectplugin.NewClient(connectplugin.ClientConfig{
    Endpoint: "http://localhost:8080",
    Plugins: connectplugin.PluginSet{
        "kv": &kvplugin.KVServicePlugin{},
    },
})
if err != nil {
    log.Fatal(err)
}
defer client.Close()
```

### Connecting

```go
func (c *Client) Connect(ctx context.Context) error
```

Establishes connection to the plugin server. Called automatically on first `Dispense()` or explicitly for eager connection.

### Dispensing Plugins

```go
func (c *Client) Dispense(name string) (any, error)
func DispenseTyped[I any](client *Client, name string) (I, error)
func MustDispenseTyped[I any](client *Client, name string) I
```

Returns plugin implementation.

**Examples:**

```go
// Untyped (requires casting)
raw, _ := client.Dispense("kv")
kv := raw.(kvv1connect.KVServiceClient)

// Typed (recommended)
kv, _ := connectplugin.DispenseTyped[kvv1connect.KVServiceClient](client, "kv")

// Must-typed (panics on error)
kv := connectplugin.MustDispenseTyped[kvv1connect.KVServiceClient](client, "kv")
```

### Service Registry Client APIs

```go
func (c *Client) RuntimeID() string
func (c *Client) RuntimeToken() string
func (c *Client) RegistryClient() connectpluginv1connect.ServiceRegistryClient
func (c *Client) Config() ClientConfig
func (c *Client) SetRuntimeIdentity(runtimeID, runtimeToken, hostURL string)
func (c *Client) ReportHealth(ctx context.Context, state HealthState, reason string, unavailableDeps []string) error
```

**Example:**

```go
// Get runtime identity
runtimeID := client.RuntimeID()

// Report health
client.ReportHealth(ctx,
    connectpluginv1.HealthState_HEALTH_STATE_HEALTHY,
    "operational",
    nil,
)

// Discover services
regClient := client.RegistryClient()
endpoint, _ := regClient.DiscoverService(ctx, &DiscoverServiceRequest{
    ServiceType: "logger",
})
```

## Server APIs

### Serving Plugins

```go
func Serve(cfg *ServeConfig) *Server
```

Creates and starts a plugin server.

```go
server := connectplugin.Serve(&connectplugin.ServeConfig{
    Plugins: pluginSet,
    Impls:   impls,
    Addr:    ":8080",
})

server.Wait()  // Block until shutdown
```

### Server Control

```go
func (s *Server) Wait() error
func (s *Server) Shutdown(ctx context.Context) error
```

## Platform APIs (Managed)

### Platform Creation

```go
func NewPlatform(registry *ServiceRegistry, lifecycle *LifecycleServer, router *ServiceRouter) *Platform
```

### Lifecycle Management

```go
func (p *Platform) AddPlugin(ctx context.Context, config PluginConfig) error
func (p *Platform) RemovePlugin(ctx context.Context, runtimeID string) error
func (p *Platform) ReplacePlugin(ctx context.Context, runtimeID string, newConfig PluginConfig) error
```

**AddPlugin flow:**

1. Calls plugin's `GetPluginInfo()`
2. Validates dependencies
3. Generates `runtime_id` and token
4. Calls plugin's `SetRuntimeIdentity()`
5. Waits for service registration
6. Waits for healthy state
7. Adds to dependency graph

**ReplacePlugin flow:**

1. Starts new version in parallel
2. Waits for new version healthy
3. Atomic switch in router
4. Drains old version
5. Shuts down old version

### Dependency Analysis

```go
func (p *Platform) GetStartupOrder() ([]string, error)
func (p *Platform) GetImpact(runtimeID string) *ImpactAnalysis
```

**Example:**

```go
// Get startup order
order, err := platform.GetStartupOrder()
// Returns: ["logger-abc", "cache-def", "app-ghi"]

// Analyze impact
impact := platform.GetImpact("logger-abc")
fmt.Printf("Affected plugins: %v\n", impact.AffectedPlugins)
fmt.Printf("Affected services: %v\n", impact.AffectedServices)
```

## Service Registry APIs

### Registry Creation

```go
func NewServiceRegistry(lifecycle *LifecycleServer) *ServiceRegistry
```

### Service Management

```go
func (r *ServiceRegistry) RegisterService(ctx, req) (*RegisterServiceResponse, error)
func (r *ServiceRegistry) UnregisterService(ctx, req) (*UnregisterServiceResponse, error)
func (r *ServiceRegistry) DiscoverService(ctx, req) (*DiscoverServiceResponse, error)
func (r *ServiceRegistry) WatchService(ctx, req) (*ServerStream[WatchServiceEvent], error)
```

### Provider Selection

```go
func (r *ServiceRegistry) SetSelectionStrategy(serviceType string, strategy SelectionStrategy)
func (r *ServiceRegistry) SelectProvider(serviceType, minVersion string) (*ServiceProvider, error)
func (r *ServiceRegistry) GetAllProviders(serviceType string) []*ServiceProvider
```

## Discovery APIs

### Static Discovery

```go
func NewStaticDiscovery(endpoints map[string][]Endpoint) *StaticDiscovery
func (s *StaticDiscovery) Discover(ctx context.Context, serviceName string) ([]Endpoint, error)
func (s *StaticDiscovery) Watch(ctx context.Context, serviceName string) (<-chan DiscoveryEvent, error)
func (s *StaticDiscovery) AddEndpoint(serviceName string, endpoint Endpoint)
func (s *StaticDiscovery) RemoveService(serviceName string)
```

## Interceptor APIs

### Retry

```go
func DefaultRetryPolicy() RetryPolicy
func RetryInterceptor(policy RetryPolicy) connect.UnaryInterceptorFunc
func (p *RetryPolicy) calculateBackoff(attempt int) time.Duration
```

### Circuit Breaker

```go
func DefaultCircuitBreakerConfig() CircuitBreakerConfig
func NewCircuitBreaker(config CircuitBreakerConfig) *CircuitBreaker
func CircuitBreakerInterceptor(cb *CircuitBreaker) connect.UnaryInterceptorFunc
func (cb *CircuitBreaker) State() CircuitState
func (cb *CircuitBreaker) Call(ctx context.Context, fn func() error) error
```

### Authentication

```go
func NewTokenAuth(token string, validateToken func(string) (string, map[string]string, error)) *TokenAuth
func NewAPIKeyAuth(apiKey string, validateKey func(string) (string, map[string]string, error)) *APIKeyAuth
func NewMTLSAuth(clientCert *tls.Certificate, rootCAs, clientCAs *x509.CertPool) *MTLSAuth

func (a *TokenAuth) ClientInterceptor() connect.UnaryInterceptorFunc
func (a *TokenAuth) ServerInterceptor() connect.UnaryInterceptorFunc

func ComposeAuthClient(providers ...AuthProvider) connect.UnaryInterceptorFunc
func ComposeAuthServer(providers ...AuthProvider) connect.UnaryInterceptorFunc
func RequireAuth() connect.UnaryInterceptorFunc

func WithAuthContext(ctx context.Context, auth *AuthContext) context.Context
func GetAuthContext(ctx context.Context) *AuthContext
```

## Lifecycle APIs

### LifecycleServer

```go
func NewLifecycleServer() *LifecycleServer
func (l *LifecycleServer) ReportHealth(ctx, req) (*ReportHealthResponse, error)
func (l *LifecycleServer) GetHealthState(runtimeID string) *PluginHealthState
func (l *LifecycleServer) ShouldRouteTraffic(runtimeID string) bool
```

### PluginControl

```go
func NewPluginControlClient(endpoint string, httpClient connect.HTTPClient) *PluginControlClient
func (p *PluginControlClient) GetHealth(ctx context.Context) (*GetHealthResponse, error)
func (p *PluginControlClient) Shutdown(ctx context.Context, gracePeriodSeconds int32, reason string) (bool, error)
```

## Dependency Graph APIs

```go
func New() *Graph
func (g *Graph) Add(node *Node)
func (g *Graph) Remove(runtimeID string)
func (g *Graph) StartupOrder() ([]string, error)
func (g *Graph) GetImpact(runtimeID string) *ImpactAnalysis
func (g *Graph) GetNode(runtimeID string) *Node
func (g *Graph) GetProviders(serviceType string) []string
func (g *Graph) HasService(serviceType string) bool
```

## Types

### Core Types

```go
type Client struct { /* ... */ }
type Server struct { /* ... */ }
type Plugin interface { /* ... */ }
type PluginSet map[string]Plugin
type Endpoint struct { /* ... */ }
type AuthContext struct { /* ... */ }
```

### Service Registry Types

```go
type Platform struct { /* ... */ }
type ServiceRegistry struct { /* ... */ }
type ServiceRouter struct { /* ... */ }
type LifecycleServer struct { /* ... */ }
type PluginMetadata struct { /* ... */ }
type ServiceDeclaration struct { /* ... */ }
type ServiceDependency struct { /* ... */ }
```

### State Types

```go
type CircuitState int
const (
    CircuitClosed
    CircuitOpen
    CircuitHalfOpen
)

type HealthState int32
const (
    HEALTH_STATE_HEALTHY
    HEALTH_STATE_DEGRADED
    HEALTH_STATE_UNHEALTHY
)
```

## Error Codes

Connect RPC error codes used:

| Code | Usage | Retryable | Circuit Breaker Failure |
|------|-------|-----------|------------------------|
| `CodeOK` | Success | - | - |
| `CodeCanceled` | Client cancelled | ❌ | ❌ |
| `CodeUnknown` | Unknown error | ✅ | ✅ |
| `CodeInvalidArgument` | Bad request | ❌ | ❌ |
| `CodeDeadlineExceeded` | Timeout | ✅ | ✅ |
| `CodeNotFound` | Resource missing | ❌ | ❌ |
| `CodeAlreadyExists` | Conflict | ❌ | ❌ |
| `CodePermissionDenied` | Forbidden | ❌ | ❌ |
| `CodeResourceExhausted` | Rate limited | ✅ | ✅ |
| `CodeFailedPrecondition` | State error | ❌ | ❌ |
| `CodeAborted` | Aborted | ❌ | ❌ |
| `CodeOutOfRange` | Out of range | ❌ | ❌ |
| `CodeUnimplemented` | Not implemented | ❌ | ❌ |
| `CodeInternal` | Server error | ✅ | ✅ |
| `CodeUnavailable` | Service down | ✅ | ✅ |
| `CodeDataLoss` | Data corruption | ❌ | ❌ |
| `CodeUnauthenticated` | Auth failed | ❌ | ❌ |

## Constants

```go
const (
    DefaultMagicCookieKey   = "CONNECT_PLUGIN"
    DefaultMagicCookieValue = "d3e5f7a9b1c2"
)
```

## Next Steps

- [Proto Reference](proto.md) - Protocol buffer definitions
- [Configuration Reference](configuration.md) - Config options
- [Service Registry Guide](../guides/service-registry.md) - Service Registry features
