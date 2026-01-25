# Spike: Service Discovery Patterns

**Issue:** KOR-munj
**Status:** Complete

## Executive Summary

Service discovery for connect-plugin needs to support both simple static endpoints and dynamic discovery through Kubernetes, DNS, or external registries. The key insight is that discovery and resolution are separate concerns: discovery finds endpoints, while resolution maintains them over time. For MVP, static discovery with a watch interface is sufficient; Kubernetes and DNS implementations follow the same pattern.

## Core Concepts

### Discovery vs Resolution

| Concern | Description | Example |
|---------|-------------|---------|
| **Discovery** | One-time lookup of endpoints | `Discover("kv-plugin")` → `["10.0.0.1:8080"]` |
| **Resolution** | Continuous tracking of endpoint changes | Watch stream delivering adds/removes |

### gRPC Resolver Pattern

gRPC uses a Builder/Resolver pattern for pluggable service discovery:

```go
// resolver/resolver.go from grpc-go

// Builder creates a resolver for a given target
type Builder interface {
    // Build creates a new resolver for the given target
    Build(target Target, cc ClientConn, opts BuildOptions) (Resolver, error)
    // Scheme returns the scheme supported by this resolver
    Scheme() string
}

// Resolver watches for the updates on the specified target
type Resolver interface {
    // ResolveNow is a hint to resolve immediately
    ResolveNow(ResolveNowOptions)
    // Close closes the resolver
    Close()
}

// ClientConn receives state updates
type ClientConn interface {
    UpdateState(State) error
    ReportError(error)
}

// State contains resolved endpoints
type State struct {
    Addresses []Address
    Endpoints []Endpoint
}
```

**Key characteristics:**
- Scheme-based routing (e.g., `dns:///host`, `k8s:///namespace/service`)
- Asynchronous updates via `UpdateState()` callback
- Resolver runs in background goroutine
- `ResolveNow()` for on-demand refresh

### DNS Resolution Pattern

From `grpc-go/internal/resolver/dns/dns_resolver.go`:

```go
type dnsResolver struct {
    host     string
    port     string
    resolver internal.NetResolver
    ctx      context.Context
    cancel   context.CancelFunc
    cc       resolver.ClientConn
    rn       chan struct{}  // ResolveNow channel
    wg       sync.WaitGroup
}

func (d *dnsResolver) watcher() {
    defer d.wg.Done()
    backoffIndex := 1
    for {
        state, err := d.lookup()
        if err != nil {
            d.cc.ReportError(err)
        } else {
            err = d.cc.UpdateState(*state)
        }

        var nextResolutionTime time.Time
        if err == nil {
            // Success: wait for ResolveNow or minimum interval
            backoffIndex = 1
            nextResolutionTime = time.Now().Add(MinResolutionInterval)
            select {
            case <-d.ctx.Done():
                return
            case <-d.rn:
            }
        } else {
            // Error: backoff and retry
            nextResolutionTime = time.Now().Add(backoff.Exponential.Backoff(backoffIndex))
            backoffIndex++
        }
        select {
        case <-d.ctx.Done():
            return
        case <-time.After(time.Until(nextResolutionTime)):
        }
    }
}
```

**Key characteristics:**
- Polling-based with backoff on errors
- Minimum resolution interval (30s) to prevent thrashing
- ResolveNow channel for immediate refresh
- Graceful shutdown via context cancellation

### Kubernetes Informer Pattern

From `client-go/informers/core/v1/endpoints.go`:

```go
type EndpointsInformer interface {
    Informer() cache.SharedIndexInformer
    Lister() corev1.EndpointsLister
}

// SharedInformer provides watch-based updates
type SharedInformer interface {
    // AddEventHandler registers callbacks for changes
    AddEventHandler(handler ResourceEventHandler) (ResourceEventHandlerRegistration, error)
    // Run starts the informer
    Run(stopCh <-chan struct{})
    // HasSynced returns true after initial list
    HasSynced() bool
}

// ResourceEventHandler receives events
type ResourceEventHandler interface {
    OnAdd(obj interface{})
    OnUpdate(oldObj, newObj interface{})
    OnDelete(obj interface{})
}
```

**Key characteristics:**
- Watch-based (server push) rather than polling
- Initial List followed by Watch for incremental updates
- Local cache with indexing for fast lookups
- Event handlers for change notification
- Shared informers reduce API server load

## connect-plugin Discovery Design

### DiscoveryService Interface

Based on project thesis with enhancements:

```go
// DiscoveryService provides plugin endpoint discovery
type DiscoveryService interface {
    // Discover returns current endpoint(s) for a plugin service
    Discover(ctx context.Context, service string) ([]Endpoint, error)

    // Watch provides endpoint change notifications
    // Returns a channel that receives updates when endpoints change
    // The channel is closed when the context is cancelled
    Watch(ctx context.Context, service string) (<-chan []Endpoint, error)
}

// Endpoint represents a plugin service endpoint
type Endpoint struct {
    // URL is the full endpoint URL (e.g., "http://10.0.0.1:8080")
    URL string

    // Weight for load balancing (0-100, higher = more traffic)
    Weight int

    // Metadata contains endpoint-specific attributes
    Metadata map[string]string

    // Ready indicates if the endpoint is ready to receive traffic
    Ready bool
}

// DiscoveryEvent represents a change in endpoints
type DiscoveryEvent struct {
    // Type is add, update, or delete
    Type EventType

    // Endpoints is the current full list after this event
    Endpoints []Endpoint
}

type EventType string

const (
    EventTypeAdd    EventType = "add"
    EventTypeUpdate EventType = "update"
    EventTypeDelete EventType = "delete"
)
```

### Static Discovery (MVP)

Simple implementation for direct endpoints:

```go
// StaticDiscovery returns fixed endpoint(s)
type StaticDiscovery struct {
    endpoints map[string][]Endpoint
}

func NewStaticDiscovery(endpoints map[string]string) *StaticDiscovery {
    m := make(map[string][]Endpoint)
    for service, url := range endpoints {
        m[service] = []Endpoint{{URL: url, Ready: true, Weight: 100}}
    }
    return &StaticDiscovery{endpoints: m}
}

func (s *StaticDiscovery) Discover(ctx context.Context, service string) ([]Endpoint, error) {
    eps, ok := s.endpoints[service]
    if !ok {
        return nil, fmt.Errorf("service %q not found", service)
    }
    return eps, nil
}

func (s *StaticDiscovery) Watch(ctx context.Context, service string) (<-chan []Endpoint, error) {
    eps, err := s.Discover(ctx, service)
    if err != nil {
        return nil, err
    }

    // Static discovery sends initial state then blocks until context done
    ch := make(chan []Endpoint, 1)
    ch <- eps

    go func() {
        <-ctx.Done()
        close(ch)
    }()

    return ch, nil
}
```

### DNS Discovery

DNS-based discovery with SRV record support:

```go
// DNSDiscovery resolves endpoints via DNS
type DNSDiscovery struct {
    resolver    *net.Resolver
    minInterval time.Duration
    timeout     time.Duration
}

func NewDNSDiscovery(opts ...DNSOption) *DNSDiscovery {
    d := &DNSDiscovery{
        resolver:    net.DefaultResolver,
        minInterval: 30 * time.Second,
        timeout:     10 * time.Second,
    }
    for _, opt := range opts {
        opt(d)
    }
    return d
}

func (d *DNSDiscovery) Discover(ctx context.Context, service string) ([]Endpoint, error) {
    ctx, cancel := context.WithTimeout(ctx, d.timeout)
    defer cancel()

    // Try SRV record first: _grpc._tcp.service
    _, srvs, err := d.resolver.LookupSRV(ctx, "grpc", "tcp", service)
    if err == nil && len(srvs) > 0 {
        return d.srvToEndpoints(ctx, srvs)
    }

    // Fall back to A/AAAA records
    addrs, err := d.resolver.LookupHost(ctx, service)
    if err != nil {
        return nil, fmt.Errorf("dns lookup failed: %w", err)
    }

    endpoints := make([]Endpoint, 0, len(addrs))
    for _, addr := range addrs {
        endpoints = append(endpoints, Endpoint{
            URL:    fmt.Sprintf("http://%s", addr),
            Ready:  true,
            Weight: 100,
        })
    }
    return endpoints, nil
}

func (d *DNSDiscovery) Watch(ctx context.Context, service string) (<-chan []Endpoint, error) {
    ch := make(chan []Endpoint, 1)

    go func() {
        defer close(ch)

        var lastEndpoints []Endpoint
        backoff := d.minInterval

        for {
            endpoints, err := d.Discover(ctx, service)
            if err != nil {
                // Exponential backoff on error
                backoff = min(backoff*2, 5*time.Minute)
            } else {
                backoff = d.minInterval
                if !endpointsEqual(lastEndpoints, endpoints) {
                    select {
                    case ch <- endpoints:
                        lastEndpoints = endpoints
                    case <-ctx.Done():
                        return
                    }
                }
            }

            select {
            case <-ctx.Done():
                return
            case <-time.After(backoff):
            }
        }
    }()

    return ch, nil
}
```

### Kubernetes Discovery

EndpointSlice-based discovery using client-go informers:

```go
// KubernetesDiscovery watches Kubernetes EndpointSlices
type KubernetesDiscovery struct {
    clientset kubernetes.Interface
    namespace string
    informer  cache.SharedIndexInformer
    stopCh    chan struct{}
}

func NewKubernetesDiscovery(clientset kubernetes.Interface, namespace string) *KubernetesDiscovery {
    return &KubernetesDiscovery{
        clientset: clientset,
        namespace: namespace,
        stopCh:    make(chan struct{}),
    }
}

func (k *KubernetesDiscovery) Start(ctx context.Context) error {
    factory := informers.NewSharedInformerFactoryWithOptions(
        k.clientset,
        30*time.Second,
        informers.WithNamespace(k.namespace),
    )

    k.informer = factory.Discovery().V1().EndpointSlices().Informer()

    go factory.Start(k.stopCh)

    // Wait for cache sync
    if !cache.WaitForCacheSync(ctx.Done(), k.informer.HasSynced) {
        return fmt.Errorf("failed to sync endpoint cache")
    }

    return nil
}

func (k *KubernetesDiscovery) Stop() {
    close(k.stopCh)
}

func (k *KubernetesDiscovery) Discover(ctx context.Context, service string) ([]Endpoint, error) {
    slices, err := k.clientset.DiscoveryV1().EndpointSlices(k.namespace).List(ctx, metav1.ListOptions{
        LabelSelector: fmt.Sprintf("kubernetes.io/service-name=%s", service),
    })
    if err != nil {
        return nil, fmt.Errorf("failed to list endpoint slices: %w", err)
    }

    return k.slicesToEndpoints(slices.Items), nil
}

func (k *KubernetesDiscovery) Watch(ctx context.Context, service string) (<-chan []Endpoint, error) {
    ch := make(chan []Endpoint, 1)

    registration, err := k.informer.AddEventHandler(cache.FilteringResourceEventHandler{
        FilterFunc: func(obj interface{}) bool {
            slice, ok := obj.(*discoveryv1.EndpointSlice)
            if !ok {
                return false
            }
            return slice.Labels["kubernetes.io/service-name"] == service
        },
        Handler: cache.ResourceEventHandlerFuncs{
            AddFunc: func(obj interface{}) {
                k.sendUpdate(ctx, ch, service)
            },
            UpdateFunc: func(oldObj, newObj interface{}) {
                k.sendUpdate(ctx, ch, service)
            },
            DeleteFunc: func(obj interface{}) {
                k.sendUpdate(ctx, ch, service)
            },
        },
    })
    if err != nil {
        return nil, fmt.Errorf("failed to add event handler: %w", err)
    }

    go func() {
        <-ctx.Done()
        k.informer.RemoveEventHandler(registration)
        close(ch)
    }()

    // Send initial state
    k.sendUpdate(ctx, ch, service)

    return ch, nil
}

func (k *KubernetesDiscovery) slicesToEndpoints(slices []discoveryv1.EndpointSlice) []Endpoint {
    var endpoints []Endpoint
    for _, slice := range slices {
        for _, ep := range slice.Endpoints {
            ready := ep.Conditions.Ready != nil && *ep.Conditions.Ready
            for _, addr := range ep.Addresses {
                for _, port := range slice.Ports {
                    endpoints = append(endpoints, Endpoint{
                        URL:    fmt.Sprintf("http://%s:%d", addr, *port.Port),
                        Ready:  ready,
                        Weight: 100,
                    })
                }
            }
        }
    }
    return endpoints
}
```

### Scheme-Based Resolution

Following gRPC's pattern, support scheme-based endpoint parsing:

```go
// ParseEndpoint parses an endpoint string into a discovery request
// Supported schemes:
//   - static://host:port        - Direct connection
//   - dns:///hostname           - DNS resolution
//   - k8s:///namespace/service  - Kubernetes service
func ParseEndpoint(endpoint string) (scheme, target string, err error) {
    u, err := url.Parse(endpoint)
    if err != nil {
        // Assume static if no scheme
        return "static", endpoint, nil
    }

    switch u.Scheme {
    case "", "http", "https":
        return "static", endpoint, nil
    case "static":
        return "static", u.Host, nil
    case "dns":
        return "dns", strings.TrimPrefix(u.Path, "/"), nil
    case "k8s", "kubernetes":
        return "kubernetes", strings.TrimPrefix(u.Path, "/"), nil
    default:
        return "", "", fmt.Errorf("unsupported scheme: %s", u.Scheme)
    }
}

// DiscoveryRegistry manages multiple discovery implementations
type DiscoveryRegistry struct {
    mu        sync.RWMutex
    providers map[string]DiscoveryService
}

func NewDiscoveryRegistry() *DiscoveryRegistry {
    return &DiscoveryRegistry{
        providers: map[string]DiscoveryService{
            "static": NewStaticDiscovery(nil),
            "dns":    NewDNSDiscovery(),
        },
    }
}

func (r *DiscoveryRegistry) Register(scheme string, provider DiscoveryService) {
    r.mu.Lock()
    defer r.mu.Unlock()
    r.providers[scheme] = provider
}

func (r *DiscoveryRegistry) Resolve(ctx context.Context, endpoint string) ([]Endpoint, error) {
    scheme, target, err := ParseEndpoint(endpoint)
    if err != nil {
        return nil, err
    }

    r.mu.RLock()
    provider, ok := r.providers[scheme]
    r.mu.RUnlock()

    if !ok {
        return nil, fmt.Errorf("no discovery provider for scheme: %s", scheme)
    }

    return provider.Discover(ctx, target)
}
```

## Watch vs Poll Comparison

| Aspect | Watch (Kubernetes) | Poll (DNS) |
|--------|-------------------|------------|
| **Latency** | Near-instant updates | Delayed by poll interval |
| **Load** | Server pushes changes | Client polls repeatedly |
| **Complexity** | Higher (connection mgmt) | Lower (simple requests) |
| **Reliability** | Reconnection logic needed | Simple retry |
| **Resource Use** | Single connection | Per-poll connection |
| **Best For** | Kubernetes, etcd | DNS, static |

## Implementation Phases

### Phase 1 (MVP): Static Discovery
- Direct URL endpoints
- Simple Watch interface (sends initial, then waits)
- No external dependencies

### Phase 2: DNS Discovery
- A/AAAA record resolution
- SRV record support for port discovery
- Polling with backoff

### Phase 3: Kubernetes Discovery
- EndpointSlice informers
- Watch-based updates
- Ready/NotReady filtering
- Label-based filtering

### Phase 4 (Future): External Registries
- Consul (watch + blocking queries)
- etcd (watch API)
- Custom HTTP-based discovery

## Client Integration

How the client uses discovery:

```go
type Client struct {
    discovery DiscoveryService
    endpoints []Endpoint
    current   int // For round-robin
    mu        sync.RWMutex
}

func NewClient(endpoint string, opts ...Option) (*Client, error) {
    cfg := defaultConfig()
    for _, opt := range opts {
        opt(cfg)
    }

    // Determine discovery provider
    var discovery DiscoveryService
    if cfg.Discovery != nil {
        discovery = cfg.Discovery
    } else {
        scheme, target, err := ParseEndpoint(endpoint)
        if err != nil {
            return nil, err
        }
        discovery = getDiscoveryForScheme(scheme, target)
    }

    return &Client{
        discovery: discovery,
    }, nil
}

func (c *Client) Connect(ctx context.Context) error {
    // Initial discovery
    endpoints, err := c.discovery.Discover(ctx, c.target)
    if err != nil {
        return err
    }
    c.setEndpoints(endpoints)

    // Start watching for changes
    watch, err := c.discovery.Watch(ctx, c.target)
    if err != nil {
        return err
    }

    go c.watchEndpoints(ctx, watch)
    return nil
}

func (c *Client) watchEndpoints(ctx context.Context, watch <-chan []Endpoint) {
    for {
        select {
        case <-ctx.Done():
            return
        case endpoints, ok := <-watch:
            if !ok {
                return
            }
            c.setEndpoints(endpoints)
        }
    }
}

func (c *Client) getEndpoint() (*Endpoint, error) {
    c.mu.RLock()
    defer c.mu.RUnlock()

    ready := filterReady(c.endpoints)
    if len(ready) == 0 {
        return nil, ErrNoReadyEndpoints
    }

    // Simple round-robin
    c.current = (c.current + 1) % len(ready)
    return &ready[c.current], nil
}
```

## Conclusions

1. **DiscoveryService interface** with Discover() and Watch() covers all use cases
2. **Static discovery** is sufficient for MVP - most deployments start simple
3. **DNS discovery** follows gRPC's polling pattern with backoff
4. **Kubernetes discovery** uses informers for efficient watch-based updates
5. **Scheme-based routing** (`k8s:///`, `dns:///`) enables flexible configuration
6. **Watch semantics** should be channel-based with initial state delivery

## Next Steps

1. Implement StaticDiscovery for MVP
2. Add DiscoveryRegistry for scheme-based routing
3. Design integration with Client connection management
4. Implement DNS discovery (Phase 2)
5. Implement Kubernetes discovery (Phase 3)
