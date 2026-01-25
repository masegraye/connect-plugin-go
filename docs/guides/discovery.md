# Discovery Guide

Endpoint discovery allows dynamic plugin location without hardcoded URLs.

## Why Discovery?

Instead of:

```go
client, _ := connectplugin.NewClient(connectplugin.ClientConfig{
    Endpoint: "http://localhost:8080",  // Hardcoded!
    Plugins:  pluginSet,
})
```

Use discovery:

```go
discovery := connectplugin.NewStaticDiscovery(endpoints)

client, _ := connectplugin.NewClient(connectplugin.ClientConfig{
    Discovery:            discovery,
    DiscoveryServiceName: "plugin-host",
    Plugins:              pluginSet,
})
```

**Benefits:**

- No hardcoded URLs in code
- Environment-specific configuration
- Load balancing across multiple endpoints
- Kubernetes service discovery (future)

## DiscoveryService Interface

All discovery implementations satisfy this interface:

```go
type DiscoveryService interface {
    // Discover returns available endpoints for a service
    Discover(ctx context.Context, serviceName string) ([]Endpoint, error)

    // Watch streams endpoint updates (for dynamic discovery)
    Watch(ctx context.Context, serviceName string) (<-chan DiscoveryEvent, error)
}

type Endpoint struct {
    URL      string            // Service URL
    Metadata map[string]string // Region, zone, version, etc.
    Weight   int               // Load balancing weight (0-100)
}
```

## Static Discovery

Hardcode endpoint lists (but externalize from application code):

### Basic Usage

```go
discovery := connectplugin.NewStaticDiscovery(map[string][]connectplugin.Endpoint{
    "plugin-host": {
        {URL: "http://localhost:8080", Weight: 100},
    },
})

client, _ := connectplugin.NewClient(connectplugin.ClientConfig{
    Discovery:            discovery,
    DiscoveryServiceName: "plugin-host",
    Plugins:              pluginSet,
})

// Client discovers http://localhost:8080 on Connect()
```

### Multiple Endpoints (Load Balancing)

```go
discovery := connectplugin.NewStaticDiscovery(map[string][]connectplugin.Endpoint{
    "plugin-cluster": {
        {
            URL:    "http://plugin-1:8080",
            Weight: 50,
            Metadata: map[string]string{
                "region": "us-west-2",
                "zone":   "us-west-2a",
            },
        },
        {
            URL:    "http://plugin-2:8080",
            Weight: 30,
            Metadata: map[string]string{
                "region": "us-west-2",
                "zone":   "us-west-2b",
            },
        },
        {
            URL:    "http://plugin-3:8080",
            Weight: 20,
            Metadata: map[string]string{
                "region": "us-east-1",
                "zone":   "us-east-1a",
            },
        },
    },
})
```

Client selects first endpoint by default (TODO: add selection strategy).

### Configuration from Environment

```go
func loadDiscoveryFromEnv() *connectplugin.StaticDiscovery {
    urls := strings.Split(os.Getenv("PLUGIN_HOST_URLS"), ",")

    endpoints := make([]connectplugin.Endpoint, len(urls))
    for i, url := range urls {
        endpoints[i] = connectplugin.Endpoint{
            URL:    strings.TrimSpace(url),
            Weight: 100 / len(urls),  // Equal weight
        }
    }

    return connectplugin.NewStaticDiscovery(map[string][]connectplugin.Endpoint{
        "plugin-host": endpoints,
    })
}

// Usage:
// PLUGIN_HOST_URLS="http://host1:8080,http://host2:8080,http://host3:8080"
```

### Configuration from File

```go
// Load from JSON config file
type Config struct {
    Services map[string][]struct {
        URL      string            `json:"url"`
        Weight   int               `json:"weight"`
        Metadata map[string]string `json:"metadata"`
    } `json:"services"`
}

func loadDiscoveryFromFile(path string) (*connectplugin.StaticDiscovery, error) {
    data, _ := os.ReadFile(path)

    var config Config
    json.Unmarshal(data, &config)

    endpoints := make(map[string][]connectplugin.Endpoint)
    for svcName, svcEndpoints := range config.Services {
        eps := make([]connectplugin.Endpoint, len(svcEndpoints))
        for i, e := range svcEndpoints {
            eps[i] = connectplugin.Endpoint{
                URL:      e.URL,
                Weight:   e.Weight,
                Metadata: e.Metadata,
            }
        }
        endpoints[svcName] = eps
    }

    return connectplugin.NewStaticDiscovery(endpoints), nil
}
```

**config.json:**

```json
{
  "services": {
    "plugin-host": [
      {
        "url": "http://localhost:8080",
        "weight": 100,
        "metadata": {
          "env": "development"
        }
      }
    ]
  }
}
```

## Watch for Endpoint Changes

Subscribe to endpoint updates:

```go
ctx := context.Background()
ch, err := discovery.Watch(ctx, "plugin-host")

for event := range ch {
    if event.Error != nil {
        log.Printf("Discovery error: %v", event.Error)
        continue
    }

    log.Printf("Endpoints updated for %s:", event.ServiceName)
    for _, ep := range event.Endpoints {
        log.Printf("  - %s (weight: %d)", ep.URL, ep.Weight)
    }

    // Reconnect to new endpoints
    // ...
}
```

**Note:** StaticDiscovery sends one event and closes (static endpoints don't change). Future implementations (Kubernetes discovery) would send updates.

## Future: Kubernetes Discovery

(Not yet implemented - see KOR-zcwl)

```go
// Discover via Kubernetes service
discovery := connectplugin.NewKubernetesDiscovery(connectplugin.K8sConfig{
    Namespace: "default",
    LabelSelector: "app=plugin-host",
})

// Endpoints discovered from k8s service
// Watch() streams updates when pods added/removed
```

## Endpoint Selection Strategies

When multiple endpoints exist, selection strategy determines which to use:

### First (Default)

Always use first endpoint:

```go
// No configuration needed - default behavior
// Consistent but no load balancing
```

### Round Robin

Rotate through endpoints:

```go
// TODO: Add to ClientConfig
SelectionStrategy: SelectionRoundRobin
```

### Random

Random selection:

```go
// TODO: Add to ClientConfig
SelectionStrategy: SelectionRandom
```

### Weighted

Based on endpoint weights:

```go
endpoints := []connectplugin.Endpoint{
    {URL: "http://a:8080", Weight: 70},  // 70% traffic
    {URL: "http://b:8080", Weight: 20},  // 20% traffic
    {URL: "http://c:8080", Weight: 10},  // 10% traffic
}
```

## Best Practices

### Use Discovery for Production

```go
// Development: Hardcoded endpoint
if os.Getenv("ENV") == "development" {
    client, _ := connectplugin.NewClient(connectplugin.ClientConfig{
        Endpoint: "http://localhost:8080",
        Plugins:  pluginSet,
    })
}

// Production: Discovery from environment/config
if os.Getenv("ENV") == "production" {
    discovery := loadDiscoveryFromEnv()
    client, _ := connectplugin.NewClient(connectplugin.ClientConfig{
        Discovery: discovery,
        Plugins:   pluginSet,
    })
}
```

### Endpoint Metadata for Routing

Use metadata for intelligent routing:

```go
// Prefer local region
func selectEndpoint(endpoints []connectplugin.Endpoint, preferredRegion string) connectplugin.Endpoint {
    // Try preferred region first
    for _, ep := range endpoints {
        if ep.Metadata["region"] == preferredRegion {
            return ep
        }
    }
    // Fallback to first endpoint
    return endpoints[0]
}
```

### Health-Based Selection

```go
// Filter to healthy endpoints only
func healthyEndpoints(endpoints []connectplugin.Endpoint) []connectplugin.Endpoint {
    healthy := make([]connectplugin.Endpoint, 0)
    for _, ep := range endpoints {
        if ep.Metadata["health"] == "healthy" {
            healthy = append(healthy, ep)
        }
    }
    return healthy
}
```

## Fallback Pattern

Discovery with fallback to hardcoded endpoint:

```go
client, err := connectplugin.NewClient(connectplugin.ClientConfig{
    Endpoint:  "http://localhost:8080",  // Fallback
    Discovery: discovery,                 // Try discovery first
    Plugins:   pluginSet,
})

// If Discovery configured, tries discovery first
// If discovery fails, falls back to Endpoint
```

## Next Steps

- [Service Registry](service-registry.md) - Plugin-to-plugin service discovery
- [Configuration Reference](../reference/configuration.md) - All config options
- [Kubernetes Discovery](../reference/api.md#kubernetes-discovery) - Coming soon
