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

    // App protocol versions supported by the server.
    // Included in error responses to help client debugging.
    repeated int32 server_supported_versions = 6;
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

    // Absolute endpoint URL for this capability.
    // MUST be a complete URL (e.g., "http://host:8080/capabilities/logger").
    // Empty means use the main plugin endpoint.
    string endpoint = 3;

    // Protocol for accessing this capability (e.g., "connect", "grpc", "http").
    // Defaults to "connect" if not specified.
    string protocol = 4;

    // Additional metadata about the capability.
    map<string, string> metadata = 5;
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
       │    client_capabilities: []  // Empty for MVP     │
       │  }                                               │
       │                                                  │
       │                              3. Validate cookie  │
       │                              4. Negotiate version│
       │                              5. Filter plugins   │
       │                              6. Validate capabilities │
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
       │        required_capabilities: []  // Empty for MVP │
       │      },                                          │
       │      {                                           │
       │        name: "auth",                             │
       │        version: "2.1.0",                         │
       │        service_paths: ["/auth.v1.AuthService/"] │
       │      }                                           │
       │    ],                                            │
       │    server_capabilities: [],  // Empty for MVP    │
       │    server_supported_versions: [1, 2, 3]          │
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

**Security Considerations:**
- Magic cookie is transmitted over the network in handshake requests
- This is primarily for process-based plugins (go-plugin compatibility)
- For network-deployed plugins, consider:
  - Making magic cookie optional (`DisableMagicCookie: true`)
  - Using proper authentication (mTLS, JWT tokens) instead
  - Relying on network isolation and service mesh for security

**Network Deployment Recommendation:**
For plugins deployed as network services (Kubernetes, Cloud Run), the magic cookie provides minimal value since:
1. Service discovery already ensures correct endpoint
2. Handshake version negotiation catches protocol mismatches
3. Network security should rely on proper authentication mechanisms

**Configuration:**
```go
// For network-deployed plugins, disable magic cookie
cfg := &connectplugin.ServeConfig{
    DisableMagicCookie: true,  // Rely on network auth instead
    // ... use mTLS, JWT, or service mesh authentication
}
```

## Capability Advertisement

**Status:** Deferred to post-MVP

Capability advertisement and the capability broker system add significant complexity to the handshake protocol. For the MVP, we will:

1. Keep the proto fields defined but unused
2. Clients will pass empty `client_capabilities` array
3. Servers will return empty `server_capabilities` array
4. Plugin `required_capabilities` and `optional_capabilities` will be empty

**Rationale:**
- Core plugin functionality works without capabilities
- Host capabilities (logging, metrics, etc.) can be provided via other means initially
- This allows us to ship the handshake protocol sooner
- Capability broker can be added in a future version without protocol changes

**Future Design (Post-MVP):**

### Client Capabilities (Host → Plugin)

The host advertises capabilities it can provide to plugins:

```go
clientCapabilities := []Capability{
    {
        Type:     "logger",
        Version:  "1",
        Endpoint: "http://host:8080/capabilities/logger",
        Protocol: "connect",
    },
    {
        Type:     "metrics",
        Version:  "1",
        Endpoint: "http://host:8080/capabilities/metrics",
        Protocol: "connect",
    },
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

**Server-Side Validation:**
The server validates that all required capabilities are available:

```go
func (s *handshakeServer) validateRequiredCapabilities(
    plugins []*connectpluginv1.PluginInfo,
    clientCapabilities []*connectpluginv1.Capability,
) error {
    available := make(map[string]bool)
    for _, cap := range clientCapabilities {
        available[cap.Type] = true
    }

    for _, plugin := range plugins {
        for _, required := range plugin.RequiredCapabilities {
            if !available[required] {
                return fmt.Errorf("plugin %q requires capability %q which is not provided by client",
                    plugin.Name, required)
            }
        }
    }
    return nil
}
```

**Error Handling:**
- If required capability is missing, handshake fails with `FailedPrecondition`
- Optional capabilities are best-effort
- Client receives clear error message indicating which capability is missing

## Handshake Endpoint

The handshake service is served at a well-known path:

```
POST /connectplugin.v1.HandshakeService/Handshake
Content-Type: application/json
Connect-Protocol-Version: 1
```

### Well-Known Discovery Endpoint

For forward compatibility and protocol version negotiation, servers SHOULD expose a discovery endpoint at:

```
GET /.well-known/connectplugin
Content-Type: application/json
```

**Response:**
```json
{
  "protocol_versions": [1],
  "handshake_endpoint": "/connectplugin.v1.HandshakeService/Handshake",
  "health_endpoint": "/connectplugin.health.v1.HealthService/Check",
  "capabilities_supported": true
}
```

**Purpose:**
- Enables clients to discover handshake endpoint location
- Allows protocol version detection before handshake
- Provides forward compatibility for future protocol versions (v2, v3)
- Supports HTTP/REST clients that don't know Connect protocol

**Client Usage:**
```go
// Optionally check well-known endpoint first
discovery, err := client.Get(baseURL + "/.well-known/connectplugin")
if err == nil {
    // Use discovered endpoints
    handshakeURL := discovery.HandshakeEndpoint
} else {
    // Fall back to default
    handshakeURL = "/connectplugin.v1.HandshakeService/Handshake"
}
```

**Registration:**
```go
func Serve(cfg *ServeConfig) error {
    mux := http.NewServeMux()

    // Register well-known discovery endpoint
    mux.HandleFunc("/.well-known/connectplugin", handleWellKnownDiscovery)

    // Register handshake service
    handshakeServer := newHandshakeServer(cfg)
    path, handler := connectpluginv1connect.NewHandshakeServiceHandler(handshakeServer)
    mux.Handle(path, handler)

    // Register plugin services
    for name, plugin := range cfg.Plugins {
        // ...
    }

    // Serve
}

func handleWellKnownDiscovery(w http.ResponseWriter, r *http.Request) {
    w.Header().Set("Content-Type", "application/json")
    json.NewEncoder(w).Encode(map[string]any{
        "protocol_versions":      []int{1},
        "handshake_endpoint":     "/connectplugin.v1.HandshakeService/Handshake",
        "health_endpoint":        "/connectplugin.health.v1.HealthService/Check",
        "capabilities_supported": true,
    })
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
            fmt.Errorf("no compatible app protocol version - server supports: %v", s.cfg.SupportedVersions))
    }

    // Build plugin info
    plugins := s.buildPluginInfo(req.Msg.RequestedPlugins, negotiatedVersion)

    // Validate required capabilities
    if err := s.validateRequiredCapabilities(plugins, req.Msg.ClientCapabilities); err != nil {
        return nil, connect.NewError(connect.CodeFailedPrecondition, err)
    }

    resp := &connectpluginv1.HandshakeResponse{
        CoreProtocolVersion:      1,
        AppProtocolVersion:       negotiatedVersion,
        Plugins:                  plugins,
        ServerCapabilities:       s.cfg.ServerCapabilities,
        ServerSupportedVersions:  int32Slice(s.cfg.SupportedVersions),
        ServerMetadata: map[string]string{
            "server_version": Version,
            "app_name":       s.cfg.AppName,
        },
    }

    return connect.NewResponse(resp), nil
}

func (s *handshakeServer) validateRequiredCapabilities(
    plugins []*connectpluginv1.PluginInfo,
    clientCapabilities []*connectpluginv1.Capability,
) error {
    // Build set of available capability types
    available := make(map[string]bool)
    for _, cap := range clientCapabilities {
        available[cap.Type] = true
    }

    // Check each plugin's required capabilities
    for _, plugin := range plugins {
        for _, required := range plugin.RequiredCapabilities {
            if !available[required] {
                return fmt.Errorf("plugin %q requires capability %q which is not provided by client",
                    plugin.Name, required)
            }
        }
    }

    return nil
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
| Required capability missing | `FailedPrecondition` | Plugin requires unavailable capability | Fail with clear message |
| Requested plugin not found | None (in response) | Plugin not available | Fail or continue without |
| Network error | `Unavailable` | Connection failed | Retry with backoff |

### Error Messages

Error responses should include actionable information:

```go
// Server error response includes supported versions
if negotiatedVersion == 0 {
    resp := &connectpluginv1.HandshakeResponse{
        ServerSupportedVersions: int32Slice(s.cfg.SupportedVersions),
    }
    // Include response in error metadata if Connect supports it
    return nil, connect.NewError(connect.CodeFailedPrecondition,
        fmt.Errorf("no compatible app protocol version - client requested: %v, server supports: %v",
            req.Msg.AppProtocolVersions, s.cfg.SupportedVersions))
}

// Client error handling with detailed messages
switch {
case isMagicCookieError(err):
    return fmt.Errorf("endpoint %s is not a connect-plugin server (magic cookie mismatch)", url)
case isCoreVersionError(err):
    return fmt.Errorf("incompatible plugin protocol version - client: %d, server: %d", clientV, serverV)
case isAppVersionError(err):
    // Parse server versions from error message or response metadata
    return fmt.Errorf("no compatible plugin version - client supports: %v, server supports: %v",
        clientVersions, parseServerVersions(err))
case isCapabilityError(err):
    return fmt.Errorf("capability requirement not met: %w", err)
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

## Forward Compatibility Strategy

### Protocol Version 2 Support

When protocol v2 is introduced, the well-known discovery endpoint enables smooth migration:

```go
// Client checks protocol versions before handshake
discovery, err := http.Get(baseURL + "/.well-known/connectplugin")
if err != nil {
    // Server doesn't support discovery - assume v1
    return performV1Handshake(ctx)
}

var info WellKnownInfo
json.NewDecoder(discovery.Body).Decode(&info)

// Choose highest mutually supported version
if contains(info.ProtocolVersions, 2) && c.supportsV2 {
    return performV2Handshake(ctx)
}

return performV1Handshake(ctx)
```

### Versioning Strategy

**Core Protocol Version:**
- Increments for breaking changes to handshake protocol itself
- Version 1: Current Connect-based handshake
- Version 2: Future (e.g., different transport, message format)

**App Protocol Version:**
- Increments for plugin interface changes
- Negotiated within a core protocol version
- Both client and server support multiple app versions

**Well-Known Discovery:**
- Enables version detection before handshake
- Lists all supported core protocol versions
- Provides endpoint URLs for each version

### Migration Path

**Adding Protocol v2:**
1. Server implements both v1 and v2 handshake endpoints
2. Well-known endpoint lists both versions
3. Clients try v2, fall back to v1
4. Gradually deprecate v1 over time

**Example Multi-Version Server:**
```go
mux.Handle("/connectplugin.v1.HandshakeService/Handshake", v1Handler)
mux.Handle("/connectplugin.v2.HandshakeService/Handshake", v2Handler)
mux.HandleFunc("/.well-known/connectplugin", func(w http.ResponseWriter, r *http.Request) {
    json.NewEncoder(w).Encode(map[string]any{
        "protocol_versions": []int{1, 2},
        "handshake_endpoints": map[string]string{
            "v1": "/connectplugin.v1.HandshakeService/Handshake",
            "v2": "/connectplugin.v2.HandshakeService/Handshake",
        },
    })
})
```

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

### MVP (Phase 1)
- [x] Handshake proto definition
- [x] Version negotiation algorithm
- [x] Magic cookie validation (with disable option)
- [x] Well-known discovery endpoint
- [x] Client handshake logic
- [x] Server handshake logic
- [x] Enhanced error messages with server versions
- [x] Handshake caching strategy
- [x] Optional handshake mode
- [x] Capability validation (server-side)
- [x] Forward compatibility strategy

### Post-MVP (Phase 2)
- [ ] Capability broker implementation
- [ ] Client capability advertisement
- [ ] Server capability advertisement
- [ ] Plugin capability requirements
- [ ] Capability grant system

## Next Steps

1. Implement handshake proto (`proto/connectplugin/v1/handshake.proto`)
2. Generate Connect service stubs
3. Implement client handshake in `client.go`
4. Implement server handshake in `serve.go`
5. Add well-known discovery endpoint handler
6. Add unit tests for version negotiation
7. Add unit tests for capability validation
8. Design client configuration (KOR-qjhn)
9. Design server configuration (KOR-koba)

## Review Feedback Resolution

This document has been updated to address critical review feedback:

### 1. Version Negotiation Error Details
**Issue:** When version negotiation fails, client receives no information about server's supported versions.

**Resolution:**
- Added `server_supported_versions` field to `HandshakeResponse`
- Server always populates this field, even in error cases
- Enhanced error messages to include both client and server version lists
- Example: "no compatible app protocol version - client requested: [3, 4], server supports: [1, 2]"

### 2. Capability Endpoint Ambiguity
**Issue:** `Capability.endpoint` field was ambiguous - unclear if relative or absolute URL.

**Resolution:**
- Clarified that `endpoint` MUST be an absolute URL
- Added `protocol` field to specify access protocol (e.g., "connect", "grpc", "http")
- Updated documentation with examples showing complete URLs
- Empty endpoint means use main plugin endpoint

### 3. Required Capabilities Validation
**Issue:** Required capabilities were only validated client-side, allowing misconfiguration.

**Resolution:**
- Added server-side validation in handshake handler
- `validateRequiredCapabilities()` function checks all plugin requirements
- Returns `FailedPrecondition` error if required capability missing
- Error message identifies specific plugin and missing capability

### 4. Forward Compatibility Strategy
**Issue:** No clear path for introducing protocol v2 in the future.

**Resolution:**
- Added well-known discovery endpoint at `/.well-known/connectplugin`
- Endpoint returns protocol versions, handshake endpoints, capabilities
- Enables clients to detect protocol version before handshake
- Documents migration path for multi-version servers
- Provides clear versioning strategy for core vs app protocol

### 5. Magic Cookie for Network Use
**Issue:** Magic cookie questionable for network-deployed plugins (K8s, Cloud Run).

**Resolution:**
- Added security considerations section
- Documented that magic cookie provides minimal value for network deployments
- Added `DisableMagicCookie` configuration option
- Recommends proper authentication (mTLS, JWT) for network deployments
- Clarifies magic cookie is primarily for process-based plugins

### 6. Capabilities Deferred to Post-MVP
**Decision:** Capability system adds significant complexity without immediate value.

**Resolution:**
- Marked capability advertisement as "Deferred to post-MVP"
- Proto fields remain defined for forward compatibility
- MVP uses empty capability arrays
- Clear documentation of future capability design
- Allows shipping handshake protocol sooner
- Capability broker can be added in future version without breaking changes
