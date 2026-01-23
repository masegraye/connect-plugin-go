# Design: Handshake Protocol for Network

**Issue:** KOR-mbgw
**Status:** Complete
**Dependencies:** KOR-fzki, KOR-yosi

## Overview

The handshake protocol establishes the connection between host and plugin, negotiating protocol versions, discovering available services, and exchanging capability information. Unlike go-plugin's stdout-based handshake, this is a network RPC that is idempotent and stateless.

## Design Goals

1. **Version negotiation**: Client and server agree on compatible protocol version
2. **Service discovery**: Client learns what plugins are available
3. **Capability advertisement**: Both sides advertise what they can provide
4. **Idempotent**: Safe to call multiple times
5. **Optional**: Simple deployments can skip handshake
6. **Fast**: Single RPC, minimal overhead

## Handshake Service Proto

```protobuf
// connectplugin/v1/handshake.proto

syntax = "proto3";
package connectplugin.v1;

option go_package = "github.com/yourorg/connect-plugin-go/gen/connectplugin/v1;connectpluginv1";

// HandshakeService handles protocol negotiation.
service HandshakeService {
    // Handshake performs version negotiation and service discovery.
    // This is idempotent - calling multiple times returns the same result.
    rpc Handshake(HandshakeRequest) returns (HandshakeResponse);
}

message HandshakeRequest {
    // Core protocol version the client supports.
    // Currently must be 1.
    int32 core_protocol_version = 1;

    // App protocol versions the client supports, in preference order.
    // The server picks the highest version it also supports.
    repeated int32 app_protocol_versions = 2;

    // Magic cookie for validation (not security).
    // Must match server's expected value.
    string magic_cookie_key = 3;
    string magic_cookie_value = 4;

    // Plugins the client wants to use.
    // Empty means "tell me what's available".
    repeated string requested_plugins = 5;

    // Client capabilities the host can provide to plugins.
    repeated Capability client_capabilities = 6;

    // Client metadata (for debugging/logging).
    map<string, string> client_metadata = 7;
}

message HandshakeResponse {
    // Negotiated core protocol version.
    int32 core_protocol_version = 1;

    // Negotiated app protocol version.
    int32 app_protocol_version = 2;

    // Available plugins on this server.
    repeated PluginInfo plugins = 3;

    // Server capabilities that plugins can request.
    repeated Capability server_capabilities = 4;

    // Server metadata (version, etc).
    map<string, string> server_metadata = 5;
}

message PluginInfo {
    // Plugin name (e.g., "kv", "auth").
    string name = 1;

    // Plugin version (semantic version string).
    string version = 2;

    // Service paths this plugin exposes.
    repeated string service_paths = 3;

    // Capabilities this plugin requires from the host.
    repeated string required_capabilities = 4;

    // Optional capabilities this plugin can use.
    repeated string optional_capabilities = 5;

    // Plugin metadata.
    map<string, string> metadata = 6;
}

message Capability {
    // Capability type (e.g., "logger", "metrics", "storage").
    string type = 1;

    // Capability version.
    string version = 2;

    // Endpoint URL for this capability (if different from main URL).
    string endpoint = 3;

    // Additional metadata about the capability.
    map<string, string> metadata = 4;
}
```

## Handshake Flow

```
┌─────────────┐                                    ┌─────────────┐
│   Client    │                                    │   Plugin    │
│   (Host)    │                                    │   (Server)  │
└──────┬──────┘                                    └──────┬──────┘
       │                                                  │
       │  1. Client discovers plugin endpoint            │
       │     (static, DNS, K8s, etc.)                    │
       │                                                  │
       │  2. POST /connectplugin.v1.HandshakeService/Handshake
       │  ──────────────────────────────────────────────▶│
       │  {                                               │
       │    core_protocol_version: 1,                     │
       │    app_protocol_versions: [2, 3],                │
       │    magic_cookie_key: "CONNECT_PLUGIN",           │
       │    magic_cookie_value: "d3f40b3...",             │
       │    requested_plugins: ["kv", "auth"],            │
       │    client_capabilities: [                        │
       │      {type: "logger", version: "1"},             │
       │      {type: "metrics", version: "1"}             │
       │    ]                                             │
       │  }                                               │
       │                                                  │
       │                              3. Validate cookie  │
       │                              4. Negotiate version│
       │                              5. Filter plugins   │
       │                                                  │
       │  ◀──────────────────────────────────────────────│
       │  {                                               │
       │    core_protocol_version: 1,                     │
       │    app_protocol_version: 2,                      │
       │    plugins: [                                    │
       │      {                                           │
       │        name: "kv",                               │
       │        version: "1.0.0",                         │
       │        service_paths: ["/kv.v1.KVService/"],    │
       │        required_capabilities: ["logger"]         │
       │      },                                          │
       │      {                                           │
       │        name: "auth",                             │
       │        version: "2.1.0",                         │
       │        service_paths: ["/auth.v1.AuthService/"] │
       │      }                                           │
       │    ],                                            │
       │    server_capabilities: [                        │
       │      {type: "callback", version: "1"}            │
       │    ]                                             │
       │  }                                               │
       │                                                  │
       │  6. Client validates response                    │
       │  7. Client ready to use plugins                  │
       │                                                  │
       │  8. Regular plugin RPCs                          │
       │  ══════════════════════════════════════════════▶│
       │                                                  │
```

## Version Negotiation

### Core Protocol Version

The core protocol version defines the handshake protocol itself and basic wire format rules:

- **Version 1**: Initial version using Connect protocol, this handshake format

**Negotiation:**
- Client sends supported core version (currently only 1)
- Server responds with same version or error
- Mismatch = incompatible, connection fails

### App Protocol Version

The app protocol version defines the plugin interfaces and semantics:

- **Version 1**: Initial plugin interfaces
- **Version 2**: Enhanced capabilities, streaming support
- **Version 3+**: Future enhancements

**Negotiation:**
- Client sends list of supported versions in preference order
- Server picks highest version it also supports
- Response includes negotiated version
- Client uses appropriate `VersionedPlugins` set

```go
type ClientConfig struct {
    // Core protocol version (currently always 1)
    CoreProtocolVersion int

    // Supported app protocol versions (in preference order)
    AppProtocolVersions []int

    // Plugin sets for each app version
    VersionedPlugins map[int]PluginSet
}

// Example
client := connectplugin.NewClient(&connectplugin.ClientConfig{
    CoreProtocolVersion: 1,
    AppProtocolVersions: []int{3, 2, 1}, // Prefer v3
    VersionedPlugins: map[int]connectplugin.PluginSet{
        1: {
            "kv": &kvv1plugin.KVServicePlugin{},
        },
        2: {
            "kv": &kvv2plugin.KVServicePlugin{},
        },
        3: {
            "kv": &kvv3plugin.KVServicePlugin{},
        },
    },
})
```

## Magic Cookie

Like go-plugin, we use a "magic cookie" for UX validation (not security):

```go
const (
    DefaultMagicCookieKey   = "CONNECT_PLUGIN"
    DefaultMagicCookieValue = "d3f40b3c2e1a5f8b9c4d7e6a1b2c3d4e"
)

type ServeConfig struct {
    // MagicCookieKey and Value must match client's expectation
    MagicCookieKey   string
    MagicCookieValue string
    // ...
}
```

**Purpose:**
- Prevents accidentally connecting to wrong service
- Improves error messages ("not a plugin server")
- NOT a security mechanism (use mTLS/tokens for security)

**Validation:**
- Server checks cookie in handshake request
- Mismatch returns error with helpful message
- Client interprets as "not a plugin server"

## Capability Advertisement

### Client Capabilities (Host → Plugin)

The host advertises capabilities it can provide to plugins:

```go
clientCapabilities := []Capability{
    {Type: "logger", Version: "1", Endpoint: "/capabilities/logger"},
    {Type: "metrics", Version: "1", Endpoint: "/capabilities/metrics"},
    {Type: "storage", Version: "1", Endpoint: "/capabilities/storage"},
}
```

Plugins can request these via the capability broker (separate from handshake).

### Server Capabilities (Plugin → Host)

The plugin advertises capabilities it provides:

```go
serverCapabilities := []Capability{
    {Type: "callback", Version: "1"},
    {Type: "webhook", Version: "1"},
}
```

### Plugin Requirements

Plugins declare required and optional capabilities:

```go
&PluginInfo{
    Name: "kv",
    RequiredCapabilities: []string{"logger"},
    OptionalCapabilities: []string{"metrics", "tracing"},
}
```

**Validation:**
- If required capability is missing, client can warn or fail
- Optional capabilities are best-effort

## Handshake Endpoint

The handshake service is served at a well-known path:

```
POST /connectplugin.v1.HandshakeService/Handshake
Content-Type: application/json
Connect-Protocol-Version: 1
```

**Registration:**
```go
func Serve(cfg *ServeConfig) error {
    mux := http.NewServeMux()

    // Register handshake service first
    handshakeServer := newHandshakeServer(cfg)
    path, handler := connectpluginv1connect.NewHandshakeServiceHandler(handshakeServer)
    mux.Handle(path, handler)

    // Register plugin services
    for name, plugin := range cfg.Plugins {
        // ...
    }

    // Serve
}
```

## Client Handshake Logic

```go
func (c *Client) Connect(ctx context.Context) error {
    // Discover endpoint
    endpoints, err := c.discovery.Discover(ctx, c.serviceName)
    if err != nil {
        return err
    }
    c.endpoint = endpoints[0].URL

    // Perform handshake
    handshakeClient := connectpluginv1connect.NewHandshakeServiceClient(
        c.httpClient,
        c.endpoint,
    )

    req := &connectpluginv1.HandshakeRequest{
        CoreProtocolVersion:  c.cfg.CoreProtocolVersion,
        AppProtocolVersions:  c.cfg.AppProtocolVersions,
        MagicCookieKey:       c.cfg.MagicCookieKey,
        MagicCookieValue:     c.cfg.MagicCookieValue,
        RequestedPlugins:     c.cfg.Plugins.Keys(),
        ClientCapabilities:   c.buildClientCapabilities(),
        ClientMetadata: map[string]string{
            "client_version": Version,
            "hostname":       os.Hostname(),
        },
    }

    resp, err := handshakeClient.Handshake(ctx, connect.NewRequest(req))
    if err != nil {
        return fmt.Errorf("handshake failed: %w", err)
    }

    // Validate response
    if resp.Msg.CoreProtocolVersion != c.cfg.CoreProtocolVersion {
        return fmt.Errorf("core protocol version mismatch: got %d, want %d",
            resp.Msg.CoreProtocolVersion, c.cfg.CoreProtocolVersion)
    }

    // Store negotiated version
    c.negotiatedVersion = resp.Msg.AppProtocolVersion

    // Validate requested plugins are available
    availablePlugins := make(map[string]*connectpluginv1.PluginInfo)
    for _, p := range resp.Msg.Plugins {
        availablePlugins[p.Name] = p
    }

    for _, requested := range c.cfg.Plugins.Keys() {
        if _, ok := availablePlugins[requested]; !ok {
            return fmt.Errorf("requested plugin %q not available", requested)
        }
    }

    c.handshakeComplete = true
    return nil
}
```

## Server Handshake Logic

```go
type handshakeServer struct {
    cfg *ServeConfig
}

func (s *handshakeServer) Handshake(ctx context.Context,
    req *connect.Request[connectpluginv1.HandshakeRequest]) (
    *connect.Response[connectpluginv1.HandshakeResponse], error) {

    // Validate magic cookie
    if req.Msg.MagicCookieKey != s.cfg.MagicCookieKey ||
        req.Msg.MagicCookieValue != s.cfg.MagicCookieValue {
        return nil, connect.NewError(connect.CodeInvalidArgument,
            fmt.Errorf("invalid magic cookie - this may not be a connect-plugin server"))
    }

    // Validate core protocol version
    if req.Msg.CoreProtocolVersion != 1 {
        return nil, connect.NewError(connect.CodeInvalidArgument,
            fmt.Errorf("unsupported core protocol version: %d", req.Msg.CoreProtocolVersion))
    }

    // Negotiate app protocol version
    negotiatedVersion := negotiateVersion(req.Msg.AppProtocolVersions, s.cfg.SupportedVersions)
    if negotiatedVersion == 0 {
        return nil, connect.NewError(connect.CodeFailedPrecondition,
            fmt.Errorf("no compatible app protocol version"))
    }

    // Build plugin info
    plugins := s.buildPluginInfo(req.Msg.RequestedPlugins, negotiatedVersion)

    resp := &connectpluginv1.HandshakeResponse{
        CoreProtocolVersion: 1,
        AppProtocolVersion:  negotiatedVersion,
        Plugins:             plugins,
        ServerCapabilities:  s.cfg.ServerCapabilities,
        ServerMetadata: map[string]string{
            "server_version": Version,
            "app_name":       s.cfg.AppName,
        },
    }

    return connect.NewResponse(resp), nil
}

func negotiateVersion(clientVersions []int32, serverVersions []int) int32 {
    // Find highest version both support
    serverSet := make(map[int32]struct{})
    for _, v := range serverVersions {
        serverSet[int32(v)] = struct{}{}
    }

    for _, clientVersion := range clientVersions {
        if _, ok := serverSet[clientVersion]; ok {
            return clientVersion
        }
    }
    return 0
}
```

## Error Handling

### Handshake Errors

| Error | Code | Meaning | Client Action |
|-------|------|---------|---------------|
| Invalid magic cookie | `InvalidArgument` | Not a plugin server | Fail with clear message |
| Core version mismatch | `InvalidArgument` | Incompatible core protocol | Fail - no recovery |
| No compatible app version | `FailedPrecondition` | No compatible plugin version | Fail or try fallback endpoint |
| Requested plugin not found | None (in response) | Plugin not available | Fail or continue without |
| Network error | `Unavailable` | Connection failed | Retry with backoff |

### Error Messages

```go
switch {
case isMagicCookieError(err):
    return fmt.Errorf("endpoint %s is not a connect-plugin server (magic cookie mismatch)", url)
case isCoreVersionError(err):
    return fmt.Errorf("incompatible plugin protocol version - client: %d, server: %d", clientV, serverV)
case isAppVersionError(err):
    return fmt.Errorf("no compatible plugin version - client supports: %v, server supports: %v", clientVersions, serverVersions)
default:
    return fmt.Errorf("handshake failed: %w", err)
}
```

## Handshake Caching

For performance, clients can cache handshake results:

```go
type HandshakeCache struct {
    mu      sync.RWMutex
    entries map[string]*cachedHandshake
}

type cachedHandshake struct {
    response  *connectpluginv1.HandshakeResponse
    timestamp time.Time
    ttl       time.Duration
}

func (c *Client) Connect(ctx context.Context) error {
    // Check cache first
    if cached := c.handshakeCache.Get(c.endpoint); cached != nil && !cached.Expired() {
        c.useHandshakeResponse(cached.response)
        return nil
    }

    // Perform handshake
    resp, err := c.doHandshake(ctx)
    if err != nil {
        return err
    }

    // Cache result
    c.handshakeCache.Put(c.endpoint, resp, 5*time.Minute)
    return nil
}
```

**Cache invalidation:**
- TTL expiry (5 minutes default)
- Connection errors (clear cache, retry handshake)
- Explicit `client.RefreshHandshake()`

## Optional Handshake

For simple deployments where version negotiation isn't needed:

```go
type ClientConfig struct {
    // SkipHandshake skips the handshake RPC.
    // Client assumes server is compatible and all requested plugins are available.
    SkipHandshake bool
}

func (c *Client) Connect(ctx context.Context) error {
    if c.cfg.SkipHandshake {
        c.handshakeComplete = true
        return nil
    }
    return c.doHandshake(ctx)
}
```

**Use cases:**
- Single version deployment (no negotiation needed)
- Development/testing
- Trusted internal services

**Trade-offs:**
- Faster connection (no handshake RPC)
- Less robust (errors discovered later)
- No capability discovery

## Comparison with go-plugin

| Aspect | go-plugin | connect-plugin |
|--------|-----------|----------------|
| **Mechanism** | Stdout line parsing | HTTP RPC |
| **Timing** | On subprocess start | On first connect |
| **Retryable** | No (one chance) | Yes (idempotent RPC) |
| **Version info** | Core + app versions | Core + app versions + capabilities |
| **Service discovery** | N/A (single plugin) | List of available plugins |
| **Capabilities** | Not included | Advertised both ways |
| **Optional** | No (required) | Yes (can skip) |

## Implementation Checklist

- [x] Handshake proto definition
- [x] Version negotiation algorithm
- [x] Magic cookie validation
- [x] Capability advertisement format
- [x] Client handshake logic
- [x] Server handshake logic
- [x] Error handling and messages
- [x] Handshake caching strategy
- [x] Optional handshake mode

## Next Steps

1. Implement handshake proto (`proto/connectplugin/v1/handshake.proto`)
2. Generate Connect service stubs
3. Implement client handshake in `client.go`
4. Implement server handshake in `serve.go`
5. Add unit tests for version negotiation
6. Design client configuration (KOR-qjhn)
7. Design server configuration (KOR-koba)
