# Protocol Buffer Reference

Reference for all proto definitions in connect-plugin-go.

## Core Services

### HandshakeService

Handles protocol negotiation and plugin discovery.

**Location:** `proto/plugin/v1/handshake.proto`

```protobuf
service HandshakeService {
  rpc Handshake(HandshakeRequest) returns (HandshakeResponse);
}

message HandshakeRequest {
  int32 core_protocol_version = 1;    // Must be 1
  int32 app_protocol_version = 2;      // Application version
  string magic_cookie_key = 3;         // Validation key
  string magic_cookie_value = 4;       // Validation value
  repeated string requested_plugins = 5;  // Plugins to use
  map<string, string> client_metadata = 6;

  // Service Registry
  string self_id = 10;       // Plugin's self-declared ID
  string self_version = 11;  // Plugin's version
}

message HandshakeResponse {
  int32 core_protocol_version = 1;
  int32 app_protocol_version = 2;
  repeated PluginInfo plugins = 3;
  map<string, string> server_metadata = 4;
  repeated Capability host_capabilities = 5;

  // Service Registry
  string runtime_id = 10;     // Host-assigned runtime ID
  string runtime_token = 11;  // Authentication token
}
```

### PluginIdentity (Managed)

Plugin-side service for platform-managed deployment.

**Location:** `proto/plugin/v1/plugininfo.proto`

```protobuf
service PluginIdentity {
  rpc GetPluginInfo(GetPluginInfoRequest) returns (GetPluginInfoResponse);
  rpc SetRuntimeIdentity(SetRuntimeIdentityRequest) returns (SetRuntimeIdentityResponse);
}

message GetPluginInfoResponse {
  string self_id = 1;
  string self_version = 2;
  repeated ServiceDeclaration provides = 3;
  repeated ServiceDependency requires = 4;
  map<string, string> metadata = 5;
}

message SetRuntimeIdentityRequest {
  string runtime_id = 1;
  string runtime_token = 2;
  string host_url = 3;
}
```

## Service Registry Services

### ServiceRegistry

Service registration and discovery.

**Location:** `proto/plugin/v1/registry.proto`

```protobuf
service ServiceRegistry {
  rpc RegisterService(RegisterServiceRequest) returns (RegisterServiceResponse);
  rpc UnregisterService(UnregisterServiceRequest) returns (UnregisterServiceResponse);
  rpc DiscoverService(DiscoverServiceRequest) returns (DiscoverServiceResponse);
  rpc WatchService(WatchServiceRequest) returns (stream WatchServiceEvent);
}

message RegisterServiceRequest {
  string service_type = 1;   // e.g., "logger", "cache"
  string version = 2;         // Service version
  string endpoint_path = 3;   // Relative path
  map<string, string> metadata = 4;
}

message DiscoverServiceRequest {
  string service_type = 1;
  string min_version = 2;     // Minimum version (semver)
}

message DiscoverServiceResponse {
  ServiceEndpoint endpoint = 1;  // Single endpoint (host selected)
  bool single_provider = 2;      // Only one provider available?
}

message ServiceEndpoint {
  string provider_id = 1;     // Provider runtime ID
  string version = 2;          // Service version
  string endpoint_url = 3;     // Routed URL: /services/{type}/{provider-id}
  map<string, string> metadata = 4;
}
```

### PluginLifecycle

Health reporting (plugin → host).

**Location:** `proto/plugin/v1/lifecycle.proto`

```protobuf
service PluginLifecycle {
  rpc ReportHealth(ReportHealthRequest) returns (ReportHealthResponse);
}

message ReportHealthRequest {
  HealthState state = 1;
  string reason = 2;
  repeated string unavailable_dependencies = 3;
}

enum HealthState {
  HEALTH_STATE_UNSPECIFIED = 0;
  HEALTH_STATE_HEALTHY = 1;      // Fully functional
  HEALTH_STATE_DEGRADED = 2;     // Limited functionality
  HEALTH_STATE_UNHEALTHY = 3;    // Cannot serve
}
```

### PluginControl

Lifecycle control (host → plugin).

**Location:** `proto/plugin/v1/lifecycle.proto`

```protobuf
service PluginControl {
  rpc GetHealth(GetHealthRequest) returns (GetHealthResponse);
  rpc Shutdown(ShutdownRequest) returns (ShutdownResponse);
}

message ShutdownRequest {
  int32 grace_period_seconds = 1;
  string reason = 2;
}
```

## Service Declarations

### ServiceDeclaration

Describes a service a plugin provides:

```protobuf
message ServiceDeclaration {
  string type = 1;     // Service type: "logger", "cache"
  string version = 2;  // Service version (semver)
  string path = 3;     // Endpoint path: "/logger.v1.Logger/"
}
```

### ServiceDependency

Describes a service a plugin requires:

```protobuf
message ServiceDependency {
  string type = 1;                   // Required service type
  string min_version = 2;            // Minimum version
  bool required_for_startup = 3;     // Block startup if unavailable?
  bool watch_for_changes = 4;        // Subscribe to state changes?
}
```

## Capability Broker (Phase 1)

Host-provided services for plugins.

**Location:** `proto/plugin/v1/broker.proto`

```protobuf
service CapabilityBroker {
  rpc RequestCapability(RequestCapabilityRequest) returns (RequestCapabilityResponse);
}

message Capability {
  string type = 1;      // e.g., "logger", "metrics"
  string version = 2;   // Capability version
  string endpoint = 3;  // Host endpoint URL
}
```

## Header Conventions

### Service Registry Headers

All Service Registry plugin→host calls must include:

```
X-Plugin-Runtime-ID: <runtime_id>
Authorization: Bearer <runtime_token>
```

**Example:**

```go
req := connect.NewRequest(&RegisterServiceRequest{...})
req.Header().Set("X-Plugin-Runtime-ID", client.RuntimeID())
req.Header().Set("Authorization", "Bearer "+client.RuntimeToken())
```

### Service Router

Plugin-to-plugin calls route through host:

```
Path: /services/{type}/{provider-id}/{method}
Headers:
  X-Plugin-Runtime-ID: <caller-runtime-id>
  Authorization: Bearer <caller-token>
```

**Example:**

```
GET /services/logger/logger-plugin-abc123/Log
X-Plugin-Runtime-ID: cache-plugin-def456
Authorization: Bearer xyz789
Content-Type: application/json
```

## Version Negotiation

### Core Protocol Version

Currently: `1`

Used for protocol-level changes (breaking changes to handshake, etc.).

### App Protocol Version

Application-defined, defaults to `1`.

Used for application-level versioning.

**Handshake validation:**

```
Client sends: core=1, app=1
Server checks: core must match exactly
Server checks: app must match (v1 uses exact match, future may negotiate)
```

## Magic Cookie

Basic validation mechanism (not security):

```
Key: CONNECT_PLUGIN
Value: d3e5f7a9b1c2
```

**Purpose:**

- Prevent accidental connections to wrong services
- Not a security mechanism
- Can be customized per deployment

## Service Routing

### Path Structure

```
/services/{service-type}/{provider-runtime-id}/{method}
```

**Examples:**

```
/services/logger/logger-plugin-abc123/Log
/services/cache/cache-plugin-def456/Get
/services/metrics/metrics-plugin-ghi789/Record
```

### Host Routing Flow

1. Extract caller from `X-Plugin-Runtime-ID`
2. Validate token from `Authorization`
3. Look up provider by `provider-runtime-id`
4. Check provider health
5. Proxy to provider's internal endpoint
6. Log: caller → provider → method → status

## Next Steps

- [Configuration Reference](configuration.md) - All config options
- [Service Registry Guide](../guides/service-registry.md) - Using Service Registry features
- [Interceptors Guide](../guides/interceptors.md) - Retry, circuit breaker, auth
