# Design: Bidirectional Broker Architecture

**Issue:** KOR-mdxm
**Status:** Phase 1 Complete, Phase 2 Deferred
**Dependencies:** KOR-lfks, KOR-yosi, KOR-adry

## Executive Summary

**Review Decision**: Phase 1 APPROVED, Phase 2 REJECTED (needs redesign)

**Phase 1 (Implemented)**: Host capabilities with robust security
- Token-based access control with JWT binding
- Token validation caching (LRU + bloom filter)
- Fine-grained permission enforcement on every request
- Comprehensive threat model and mitigations
- Observability hooks for monitoring and auditing

**Phase 2 (Deferred)**: Plugin-to-plugin communication requires redesign
- Original design has 7 network hops (excessive complexity)
- Service registry is single-tenant (one provider per service)
- Recommended alternatives:
  - **Option A**: Static wiring (no registry, explicit dependencies)
  - **Option B**: Simplified registry without proxy (direct connections, 4 hops)
- Defer until use cases are validated

## Overview

The broker architecture enables plugins to request security-sensitive services from the host. Unlike go-plugin's GRPCBroker which only supported host→plugin callbacks, connect-plugin's broker provides:

1. **Host Capabilities** (Phase 1): Security-sensitive bootstrap services provided by the host
2. **Token-Based Access Control** (Phase 1): JWT tokens with revocation and caching
3. **Permission Enforcement** (Phase 1): Fine-grained authorization on every request
4. **Plugin Services** (Phase 2 - Deferred): Discoverable services that plugins provide to each other
5. **Service Discovery** (Phase 2 - Deferred): Plugin-to-plugin communication (needs redesign)

## Key Distinction: Capabilities vs Services

### Host Capabilities (Bootstrap, Security-Sensitive)

Capabilities are provided **exclusively by the host** and grant authority to perform privileged operations:

**Examples:**
- Authentication/authorization
- Secrets management
- Host filesystem access
- Resource quotas
- Network policies
- System metrics

**Characteristics:**
- Security-sensitive (grant authority)
- Must be provided by host (cannot be delegated to plugins)
- Requested during handshake (`required_capabilities`, `optional_capabilities`)
- Granted via capability tokens (see KOR-adry)

### Plugin Services (Discoverable, Composable)

Services are functional capabilities that **any plugin can provide** and other plugins can discover:

**Examples:**
- Logging
- Metrics collection
- Caching
- Notifications
- Message queues
- Business logic services

**Characteristics:**
- Lower security impact (functional, not authoritative)
- Any plugin can provide a service
- Plugins discover services via service registry
- Enables plugin composition (Logger plugin + Cache plugin + App plugin)

### Architectural Analogy

This maps to Android's Intent system:

| Android | connect-plugin |
|---------|----------------|
| System Services (LocationManager, NotificationManager) | Host Capabilities |
| App-provided Services (Intent handlers, ContentProviders) | Plugin Services |
| Intent resolution | Service Discovery |
| Binder | Capability Broker |

## Architecture Overview (Full Vision - Service Registry in Phase 2)

> **Note**: This diagram shows the full vision including service registry. Phase 1 implements only host capabilities (left side). Service registry (right side) is deferred to Phase 2.

```
┌─────────────────────────────────────────────────────────────────────┐
│                              Host                                    │
│                                                                      │
│  ┌────────────────────────────────────────────────────────────────┐ │
│  │                    Capability Broker                            │ │
│  │                                                                  │ │
│  │  ┌─────────────────────┐      ┌─────────────────────────────┐  │ │
│  │  │  Host Capabilities  │      │   Service Registry          │  │ │
│  │  │  (Phase 1)          │      │   (Phase 2 - Deferred)      │  │ │
│  │  │                     │      │                             │  │ │
│  │  │  - Auth/AuthZ       │      │  "logger" -> Logger Plugin  │  │ │
│  │  │  - Secrets          │      │  "cache"  -> Cache Plugin   │  │ │
│  │  │  - Filesystem       │      │  "metrics" -> Metrics Plugin│  │ │
│  │  └─────────────────────┘      └─────────────────────────────┘  │ │
│  │                                                                  │ │
│  │  ┌──────────────────────────────────────────────────────────┐  │ │
│  │  │            Capability Grant Manager (Phase 1)             │  │ │
│  │  │  - Issues capability tokens (JWT)                         │  │ │
│  │  │  - Validates tokens (with caching)                        │  │ │
│  │  │  - Enforces permissions                                   │  │ │
│  │  │  - Revokes tokens                                         │  │ │
│  │  └──────────────────────────────────────────────────────────┘  │ │
│  └────────────────────────────────────────────────────────────────┘ │
│                                                                      │
└───────────────┬──────────────────────┬───────────────────┬──────────┘
                │                      │                   │
           HTTP/Connect           HTTP/Connect        HTTP/Connect
                │                      │                   │
┌───────────────▼──────┐  ┌────────────▼───────┐  ┌──────▼────────────┐
│   Logger Plugin      │  │   Cache Plugin     │  │   App Plugin      │
│   (Phase 2)          │  │   (Phase 2)        │  │   (Phase 1)       │
│                      │  │                    │  │                   │
│  Provides:           │  │  Provides:         │  │  Requires:        │
│  - logger.v1         │  │  - cache.v1        │  │  - secrets (host) │
│                      │  │                    │  │  - auth (host)    │
│  Requires:           │  │  Requires:         │  └───────────────────┘
│  - (none)            │  │  - logger.v1       │
└──────────────────────┘  └────────────────────┘
```

## Service Registry Protocol (Phase 2 - Deferred)

> **Note**: The service registry is deferred to Phase 2 and requires redesign to address the 7-hop network path issue. See "Phase 2: Plugin-to-Plugin Communication" section for alternatives.

### Service Registration

Plugins register services they provide during startup:

```protobuf
// connectplugin/v1/broker.proto

service ServiceRegistry {
    // RegisterService registers a service this plugin provides.
    rpc RegisterService(RegisterServiceRequest) returns (RegisterServiceResponse);

    // UnregisterService removes a service registration.
    rpc UnregisterService(UnregisterServiceRequest) returns (UnregisterServiceResponse);

    // DiscoverService finds providers for a service.
    rpc DiscoverService(DiscoverServiceRequest) returns (DiscoverServiceResponse);

    // WatchService streams service availability updates.
    rpc WatchService(WatchServiceRequest) returns (stream WatchServiceResponse);
}

message RegisterServiceRequest {
    // Service type (e.g., "logger", "cache", "metrics")
    string service_type = 1;

    // Service version
    string version = 2;

    // Service endpoint path (relative to plugin's base URL)
    string endpoint_path = 3;

    // Service metadata
    map<string, string> metadata = 4;
}

message RegisterServiceResponse {
    // Registration ID for this service
    string registration_id = 1;
}

message DiscoverServiceRequest {
    // Service type to discover
    string service_type = 1;

    // Minimum version required (semver)
    string min_version = 2;
}

message DiscoverServiceResponse {
    // Available providers for this service
    repeated ServiceProvider providers = 1;
}

message ServiceProvider {
    // Provider plugin name
    string plugin_name = 1;

    // Service version
    string version = 2;

    // Capability grant for accessing this service
    CapabilityGrant grant = 3;
}

message CapabilityGrant {
    // Unique ID for this grant
    string grant_id = 1;

    // Service endpoint URL
    string endpoint_url = 2;

    // Bearer token for authentication
    string bearer_token = 3;

    // Expiration time
    google.protobuf.Timestamp expires_at = 4;
}
```

### Service Registration Flow

```
┌─────────────┐                          ┌─────────────┐
│   Logger    │                          │    Host     │
│   Plugin    │                          │   Broker    │
└──────┬──────┘                          └──────┬──────┘
       │                                        │
       │  1. POST /connectplugin.v1.ServiceRegistry/RegisterService
       │  ──────────────────────────────────────▶│
       │  {                                      │
       │    service_type: "logger",              │
       │    version: "1.0.0",                    │
       │    endpoint_path: "/logger.v1.Logger/"  │
       │  }                                      │
       │                                         │
       │                          2. Store in registry
       │                          {
       │                            "logger": {
       │                              provider: "logger-plugin",
       │                              version: "1.0.0",
       │                              endpoint: "http://logger-plugin:8080/logger.v1.Logger/"
       │                            }
       │                          }
       │                                         │
       │  ◀──────────────────────────────────────│
       │  { registration_id: "reg-123" }         │
       │                                         │
```

### Service Discovery Flow

```
┌─────────────┐                          ┌─────────────┐         ┌─────────────┐
│    App      │                          │    Host     │         │   Logger    │
│   Plugin    │                          │   Broker    │         │   Plugin    │
└──────┬──────┘                          └──────┬──────┘         └──────┬──────┘
       │                                        │                       │
       │  1. POST /connectplugin.v1.ServiceRegistry/DiscoverService
       │  ──────────────────────────────────────▶│                       │
       │  {                                      │                       │
       │    service_type: "logger",              │                       │
       │    min_version: "1.0.0"                 │                       │
       │  }                                      │                       │
       │                                         │                       │
       │                          2. Lookup in registry
       │                          3. Generate capability grant
       │                          4. Create JWT token
       │                                         │                       │
       │  ◀──────────────────────────────────────│                       │
       │  {                                      │                       │
       │    providers: [{                        │                       │
       │      plugin_name: "logger-plugin",      │                       │
       │      version: "1.0.0",                  │                       │
       │      grant: {                           │                       │
       │        grant_id: "grant-456",           │                       │
       │        endpoint_url: "http://host:8080/capabilities/logger/grant-456",
       │        bearer_token: "eyJ...",          │                       │
       │        expires_at: "..."                │                       │
       │      }                                  │                       │
       │    }]                                   │                       │
       │  }                                      │                       │
       │                                         │                       │
       │  5. POST /capabilities/logger/grant-456/Log
       │     Authorization: Bearer eyJ...        │                       │
       │  ────────────────────────────────────────▶                      │
       │                                         │                       │
       │                          6. Validate token                      │
       │                          7. Route to provider                   │
       │                                         │  ──────────────────────▶
       │                                         │  POST /logger.v1.Logger/Log
       │                                         │                       │
       │                                         │  ◀──────────────────────
       │                                         │  { success }           │
       │  ◀──────────────────────────────────────│                       │
       │  { success }                            │                       │
       │                                         │                       │
```

## Capability Routing (Phase 2 - Plugin Services Only)

> **Note**: This routing mechanism is only needed for plugin-to-plugin services (Phase 2). Host capabilities in Phase 1 use direct handler invocation, not proxying.

The host acts as a reverse proxy for capability requests to plugin services:

```go
// Capability handler routes requests to service providers
func (b *Broker) ServeHTTP(w http.ResponseWriter, r *http.Request) {
    // Extract capability ID from path: /capabilities/{service}/{grant_id}/{method}
    parts := strings.Split(r.URL.Path, "/")
    if len(parts) < 5 {
        http.Error(w, "invalid capability path", http.StatusBadRequest)
        return
    }

    grantID := parts[3]

    // Validate bearer token
    token := extractBearerToken(r)
    grant, err := b.validateCapabilityToken(token, grantID)
    if err != nil {
        http.Error(w, "unauthorized", http.StatusUnauthorized)
        return
    }

    // Route to provider
    provider := grant.Provider
    targetURL := provider.BaseURL + r.URL.Path[len("/capabilities/"+grant.ServiceType+"/"+grantID):]

    // Proxy request
    b.proxyRequest(w, r, targetURL)
}
```

## Host Capability Model

Host capabilities work similarly but are provided by the host itself:

```go
type HostCapabilities struct {
    // Map of capability type -> handler
    handlers map[string]http.Handler
}

func (hc *HostCapabilities) RegisterCapability(capType string, handler http.Handler) {
    hc.handlers[capType] = handler
}

// Example: Secrets capability
func (b *Broker) registerHostCapabilities() {
    b.hostCapabilities.RegisterCapability("secrets", &SecretsHandler{
        vault: b.vault,
    })

    b.hostCapabilities.RegisterCapability("filesystem", &FilesystemHandler{
        allowedPaths: b.cfg.AllowedPaths,
    })
}
```

When a plugin requests a host capability:

```go
// Plugin requests "secrets" capability
req := &RequestCapabilityRequest{
    CapabilityType: "secrets",
    Reason:         "need database password",
}

resp := broker.RequestCapability(ctx, req)
// Returns capability grant with token

// Use capability
secretsClient := NewSecretsClient(resp.Grant.EndpointURL, resp.Grant.BearerToken)
dbPassword := secretsClient.GetSecret(ctx, "database/password")
```

## fx Integration

This broker model maps directly to fx dependency injection:

```go
// Host side: Register capabilities and services in fx container
fx.Provide(
    // Host capabilities
    NewSecretsService,
    NewAuthService,

    // Plugin service registry
    NewServiceRegistry,

    // Capability broker
    NewCapabilityBroker,
)

// Plugin side: Request services via broker
func NewAppPlugin(broker ServiceRegistryClient) (*AppPlugin, error) {
    // Discover logger service
    loggerResp, err := broker.DiscoverService(ctx, &DiscoverServiceRequest{
        ServiceType: "logger",
        MinVersion:  "1.0.0",
    })
    if err != nil {
        return nil, err
    }

    // Create client from grant
    loggerClient := NewLoggerClient(
        loggerResp.Providers[0].Grant.EndpointURL,
        loggerResp.Providers[0].Grant.BearerToken,
    )

    return &AppPlugin{
        logger: loggerClient,
    }, nil
}
```

## Security Model (Phase 1)

> **Note**: This section describes Phase 1 (host capabilities only). Plugin service security is deferred to Phase 2.

### Token-Based Access Control

Each capability grant includes a JWT token (see "Token Binding and Validation" section in Phase 1 for full details):

```json
{
    "iss": "connect-plugin-host",
    "sub": "app-plugin",
    "jti": "grant-456",
    "aud": "capability-broker",
    "cap": {
        "grant_id": "grant-456",
        "capability_type": "secrets",
        "permissions": ["secrets:read:database/*", "secrets:list:database"],
        "plugin_id": "app-plugin",
        "plugin_process_id": 12345
    },
    "exp": 1706054400,
    "iat": 1706050800,
    "nbf": 1706050800
}
```

### Permission Model (Phase 1: Host Capabilities)

Host capabilities use fine-grained, hierarchical permissions:

```json
{
    "cap": {
        "type": "secrets",
        "permissions": [
            "secrets:read:database/*",
            "secrets:list:database"
        ]
    }
}
```

**Permission Format**: `<capability>:<action>:<resource_path>`

**Examples**:
- `secrets:read:database/password` - Read specific secret
- `secrets:read:database/*` - Read all secrets under database/
- `secrets:list:database` - List secrets in database namespace
- `filesystem:read:/var/app/data/*` - Read files in /var/app/data/
- `auth:verify:*` - Verify any token

### Revocation (Phase 1)

Tokens can be revoked immediately with cache invalidation:

```go
// Revoke all grants for a plugin (e.g., on plugin shutdown)
broker.RevokePluginGrants(ctx, "app-plugin")

// Revoke specific grant (e.g., on permission change)
broker.RevokeGrant(ctx, "grant-456")
```

Revocation is enforced via:
1. Bloom filter for fast rejection
2. Exact revocation set for confirmation
3. Cache invalidation for previously validated tokens

See "Token Validation Caching Strategy" in Phase 1 for implementation details.

## Service Lifecycle (Phase 2 - Deferred)

> **Note**: Service lifecycle management is deferred to Phase 2. Phase 1 only handles host capability lifecycle (always available).

### Registration on Startup

```go
func (p *LoggerPlugin) OnStart(ctx context.Context, broker ServiceRegistryClient) error {
    // Register services this plugin provides
    _, err := broker.RegisterService(ctx, &RegisterServiceRequest{
        ServiceType:  "logger",
        Version:      "1.0.0",
        EndpointPath: "/logger.v1.Logger/",
    })
    return err
}
```

### Deregistration on Shutdown

```go
func (p *LoggerPlugin) OnStop(ctx context.Context) error {
    // Unregister services
    return p.broker.UnregisterService(ctx, &UnregisterServiceRequest{
        RegistrationId: p.registrationID,
    })
}
```

### Health-Based Deregistration

If a plugin becomes unhealthy, the host automatically deregisters its services:

```go
func (b *Broker) OnPluginUnhealthy(pluginName string) {
    b.mu.Lock()
    defer b.mu.Unlock()

    // Remove all services provided by this plugin
    for serviceType, provider := range b.registry {
        if provider.PluginName == pluginName {
            delete(b.registry, serviceType)
        }
    }

    // Revoke all capability grants issued to this plugin
    b.revokePluginGrants(pluginName)
}
```

## Service Discovery Patterns (Phase 2 - Deferred)

> **Note**: Service discovery is deferred to Phase 2. Phase 1 uses static capability grants requested during plugin handshake.

### Pull-Based (Query)

```go
// Discover logger service
resp, err := broker.DiscoverService(ctx, &DiscoverServiceRequest{
    ServiceType: "logger",
})
loggerClient := NewLoggerClient(resp.Providers[0].Grant)
```

### Push-Based (Watch)

```go
// Watch for logger service availability
stream, err := broker.WatchService(ctx, &WatchServiceRequest{
    ServiceType: "logger",
})

for {
    event, err := stream.Recv()
    if err != nil {
        break
    }

    switch event.Type {
    case EventType_SERVICE_ADDED:
        // New logger became available
        p.logger = NewLoggerClient(event.Providers[0].Grant)
    case EventType_SERVICE_REMOVED:
        // Logger went away
        p.logger = NewNoopLogger()
    }
}
```

## Phase 1: Host Capabilities Only (APPROVED)

### Scope

- ✅ Host capabilities (bootstrap services)
- ✅ Capability grant system (tokens, validation, caching)
- ✅ Token binding and revocation
- ✅ Permission enforcement
- ✅ Observability hooks
- ❌ Plugin service registry (deferred to Phase 2)
- ❌ Service discovery (deferred to Phase 2)
- ❌ Plugin-to-plugin communication (deferred to Phase 2)

**Rationale**: Host capabilities are essential for auth, secrets, and filesystem access. Plugin-to-plugin composition adds significant complexity (7 network hops) and can be reconsidered in Phase 2 with simpler alternatives.

### Architecture (Phase 1)

```
┌─────────────────────────────────────────────────────────────────────┐
│                              Host                                    │
│                                                                      │
│  ┌────────────────────────────────────────────────────────────────┐ │
│  │                    Capability Broker                            │ │
│  │                                                                  │ │
│  │  ┌─────────────────────┐      ┌─────────────────────────────┐  │ │
│  │  │  Host Capabilities  │      │   Capability Grant Manager  │  │ │
│  │  │  (Bootstrap)        │      │                             │  │ │
│  │  │                     │      │  - Issues capability tokens │  │ │
│  │  │  - Auth/AuthZ       │      │  - Validates tokens         │  │ │
│  │  │  - Secrets          │      │  - Caches valid tokens      │  │ │
│  │  │  - Filesystem       │      │  - Enforces permissions     │  │ │
│  │  │  - Metrics          │      │  - Revokes grants           │  │ │
│  │  └─────────────────────┘      └─────────────────────────────┘  │ │
│  │                                                                  │ │
│  │  ┌──────────────────────────────────────────────────────────┐  │ │
│  │  │            Observability Hooks                            │  │ │
│  │  │  - Capability request tracing                             │  │ │
│  │  │  - Token validation metrics                               │  │ │
│  │  │  - Permission denial logging                              │  │ │
│  │  └──────────────────────────────────────────────────────────┘  │ │
│  └────────────────────────────────────────────────────────────────┘ │
│                                                                      │
└───────────────┬──────────────────────┬───────────────────┬──────────┘
                │                      │                   │
           HTTP/Connect           HTTP/Connect        HTTP/Connect
                │                      │                   │
        ┌───────▼──────┐       ┌──────▼───────┐   ┌──────▼────────┐
        │  Plugin A    │       │  Plugin B    │   │  Plugin C     │
        │              │       │              │   │               │
        │  Requires:   │       │  Requires:   │   │  Requires:    │
        │  - secrets   │       │  - auth      │   │  - filesystem │
        └──────────────┘       └──────────────┘   └───────────────┘
```

### Token Binding and Validation

Each capability grant is bound to the requesting plugin and includes cryptographic binding:

```json
{
    "iss": "connect-plugin-host",
    "sub": "app-plugin",
    "jti": "grant-456",
    "aud": "capability-broker",
    "nbf": 1706050800,
    "exp": 1706054400,
    "iat": 1706050800,
    "cap": {
        "grant_id": "grant-456",
        "capability_type": "secrets",
        "permissions": ["secrets:read:database/*", "secrets:list:database"],
        "plugin_id": "app-plugin",
        "plugin_process_id": 12345
    }
}
```

**Token Binding Enforcement**:
- `sub` (subject) must match the requesting plugin's identity
- `jti` (JWT ID) uniquely identifies this grant for revocation
- `aud` (audience) ensures token can only be used with this broker
- `plugin_process_id` binds token to the plugin's OS process

### Token Validation Caching Strategy

**Problem**: Validating JWTs on every request is expensive (signature verification, expiration checks, revocation lookups).

**Solution**: Multi-layer caching with revocation tracking

```go
type TokenValidator struct {
    // Layer 1: In-memory LRU cache of validated tokens
    validTokenCache *lru.Cache[string, *ValidatedToken]

    // Layer 2: Revocation list (bloom filter + exact set)
    revokedTokens   *bloom.BloomFilter
    revokedExact    map[string]time.Time

    // Layer 3: JWT signing key cache
    signingKeyCache *lru.Cache[string, crypto.PublicKey]

    // TTL for cache entries
    cacheTTL        time.Duration
}

type ValidatedToken struct {
    Claims      *CapabilityClaims
    ValidUntil  time.Time
    PluginID    string
    Permissions []string
}

func (tv *TokenValidator) ValidateToken(ctx context.Context, token string, grantID string) (*ValidatedToken, error) {
    // Fast path: Check revocation bloom filter first
    if tv.revokedTokens.Test([]byte(token)) {
        // Possible revocation, check exact set
        if _, revoked := tv.revokedExact[grantID]; revoked {
            return nil, ErrTokenRevoked
        }
    }

    // Check cache for previously validated token
    if cached, ok := tv.validTokenCache.Get(token); ok {
        if time.Now().Before(cached.ValidUntil) {
            return cached, nil
        }
        // Cache expired, remove it
        tv.validTokenCache.Remove(token)
    }

    // Slow path: Parse and validate JWT
    claims, err := tv.parseAndValidateJWT(ctx, token)
    if err != nil {
        return nil, err
    }

    // Verify grant ID matches token
    if claims.CapabilityGrant.GrantID != grantID {
        return nil, ErrGrantMismatch
    }

    // Cache validated token (TTL = min(token expiry, cache TTL))
    validated := &ValidatedToken{
        Claims:      claims,
        ValidUntil:  time.Unix(claims.ExpiresAt, 0),
        PluginID:    claims.Subject,
        Permissions: claims.CapabilityGrant.Permissions,
    }

    cacheDuration := time.Until(validated.ValidUntil)
    if cacheDuration > tv.cacheTTL {
        cacheDuration = tv.cacheTTL
    }

    tv.validTokenCache.Add(token, validated)

    return validated, nil
}
```

**Cache Invalidation on Revocation**:

```go
func (b *Broker) RevokeGrant(ctx context.Context, grantID string) error {
    // Add to bloom filter for fast rejection
    b.validator.revokedTokens.Add([]byte(grantID))

    // Add to exact set with timestamp
    b.validator.revokedExact[grantID] = time.Now()

    // Remove from validation cache if present
    b.validator.validTokenCache.Remove(grantID)

    // Emit observability event
    b.emitEvent(ctx, EventTokenRevoked{
        GrantID:   grantID,
        RevokedAt: time.Now(),
    })

    return nil
}
```

**Cache Sizing**:
- Valid token cache: 10,000 entries (LRU eviction)
- Revocation bloom filter: 100,000 entries (1% false positive rate)
- Revocation exact set: Unbounded, pruned on token expiry

### Permission Enforcement

Every capability request validates permissions before proxying:

```go
func (b *Broker) ServeHTTP(w http.ResponseWriter, r *http.Request) {
    // Extract capability ID from path: /capabilities/{capability_type}/{grant_id}/{method}
    parts := strings.Split(r.URL.Path, "/")
    if len(parts) < 5 {
        b.emitEvent(r.Context(), EventInvalidPath{Path: r.URL.Path})
        http.Error(w, "invalid capability path", http.StatusBadRequest)
        return
    }

    capabilityType := parts[2]
    grantID := parts[3]
    method := parts[4]

    // Validate bearer token with caching
    token := extractBearerToken(r)
    validated, err := b.validator.ValidateToken(r.Context(), token, grantID)
    if err != nil {
        b.emitEvent(r.Context(), EventTokenValidationFailed{
            GrantID: grantID,
            Error:   err,
        })
        http.Error(w, "unauthorized", http.StatusUnauthorized)
        return
    }

    // Enforce permission for this method
    requiredPerm := fmt.Sprintf("%s:%s", capabilityType, method)
    if !hasPermission(validated.Permissions, requiredPerm) {
        b.emitEvent(r.Context(), EventPermissionDenied{
            PluginID:   validated.PluginID,
            GrantID:    grantID,
            Permission: requiredPerm,
        })
        http.Error(w, "forbidden", http.StatusForbidden)
        return
    }

    // Route to capability handler
    handler, ok := b.hostCapabilities.handlers[capabilityType]
    if !ok {
        http.Error(w, "capability not found", http.StatusNotFound)
        return
    }

    // Add validated claims to context
    ctx := withCapabilityClaims(r.Context(), validated)

    // Emit observability event
    b.emitEvent(ctx, EventCapabilityInvoked{
        PluginID:       validated.PluginID,
        CapabilityType: capabilityType,
        Method:         method,
        GrantID:        grantID,
    })

    // Serve request
    handler.ServeHTTP(w, r.WithContext(ctx))
}

func hasPermission(granted []string, required string) bool {
    for _, perm := range granted {
        if matchesPermission(perm, required) {
            return true
        }
    }
    return false
}

// matchesPermission supports wildcards: "secrets:read:*" matches "secrets:read:foo"
func matchesPermission(pattern, required string) bool {
    // Simple glob matching (can use more sophisticated library)
    if pattern == required {
        return true
    }
    if strings.HasSuffix(pattern, "*") {
        prefix := strings.TrimSuffix(pattern, "*")
        return strings.HasPrefix(required, prefix)
    }
    return false
}
```

### Observability Hooks

The broker emits structured events for monitoring, debugging, and auditing:

```go
type ObservabilityHook interface {
    EmitEvent(ctx context.Context, event Event)
}

type Event interface {
    EventType() string
    Timestamp() time.Time
    ToJSON() ([]byte, error)
}

// Token validation events
type EventTokenValidationFailed struct {
    GrantID   string
    PluginID  string
    Error     error
    Timestamp time.Time
}

type EventTokenRevoked struct {
    GrantID   string
    RevokedBy string
    RevokedAt time.Time
}

// Permission enforcement events
type EventPermissionDenied struct {
    PluginID   string
    GrantID    string
    Permission string
    Method     string
    Timestamp  time.Time
}

// Capability usage events
type EventCapabilityInvoked struct {
    PluginID       string
    CapabilityType string
    Method         string
    GrantID        string
    Duration       time.Duration
    StatusCode     int
    Timestamp      time.Time
}

// Grant lifecycle events
type EventGrantIssued struct {
    GrantID        string
    PluginID       string
    CapabilityType string
    Permissions    []string
    ExpiresAt      time.Time
    IssuedAt       time.Time
}

type EventGrantExpired struct {
    GrantID   string
    ExpiredAt time.Time
}
```

**Integration with observability systems**:

```go
// OpenTelemetry integration
type OTelHook struct {
    tracer trace.Tracer
    meter  metric.Meter
}

func (h *OTelHook) EmitEvent(ctx context.Context, event Event) {
    switch e := event.(type) {
    case EventCapabilityInvoked:
        span := trace.SpanFromContext(ctx)
        span.SetAttributes(
            attribute.String("plugin.id", e.PluginID),
            attribute.String("capability.type", e.CapabilityType),
            attribute.String("capability.method", e.Method),
        )

        h.meter.RecordHistogram(ctx, "capability.duration", e.Duration.Milliseconds())
        h.meter.IncrementCounter(ctx, "capability.invocations", 1)

    case EventPermissionDenied:
        h.meter.IncrementCounter(ctx, "capability.permission_denied", 1)
        // Log security event
        log.Warn("permission denied",
            "plugin_id", e.PluginID,
            "permission", e.Permission,
            "method", e.Method,
        )
    }
}

// Structured logging integration
type LogHook struct {
    logger *slog.Logger
}

func (h *LogHook) EmitEvent(ctx context.Context, event Event) {
    json, _ := event.ToJSON()
    h.logger.InfoContext(ctx, "capability event",
        "event_type", event.EventType(),
        "event_data", string(json),
    )
}
```

### Threat Model

**Threat: Token Theft**
- **Attack**: Malicious plugin steals another plugin's capability token
- **Mitigation**: Token binding to plugin process ID, mutual TLS between plugins and host
- **Detection**: Observability hook logs mismatched process IDs

**Threat: Token Replay**
- **Attack**: Attacker captures and replays a valid token
- **Mitigation**: Short token TTL (5 minutes), nonce/JTI enforcement, TLS transport
- **Detection**: Duplicate JTI usage logged via observability

**Threat: Privilege Escalation**
- **Attack**: Plugin requests broad permissions then abuses them
- **Mitigation**: Least-privilege grants, permission enforcement on every request, wildcard limitations
- **Detection**: Permission denied events tracked, anomaly detection on capability usage patterns

**Threat: Revocation Bypass**
- **Attack**: Plugin uses cached token after revocation
- **Mitigation**: Bloom filter + exact revocation set, cache invalidation on revoke
- **Detection**: Revoked token usage logged, monitoring for revocation-to-usage latency

**Threat: Capability Confusion**
- **Attack**: Plugin uses token for different capability than granted
- **Mitigation**: Grant ID verification, capability type embedded in token claims
- **Detection**: Grant mismatch events logged

**Threat: Host Compromise**
- **Attack**: Host process compromised, all tokens exposed
- **Mitigation**: Hardware security module (HSM) for signing keys, token rotation, plugin sandboxing
- **Detection**: Anomalous token issuance patterns, unexpected capability grants

**Threat: Denial of Service**
- **Attack**: Plugin floods host with capability requests
- **Mitigation**: Rate limiting per plugin, request throttling, circuit breakers
- **Detection**: Request rate metrics, automatic plugin isolation on abuse

## Phase 2: Plugin-to-Plugin Communication (DEFERRED - NEEDS REDESIGN)

### Problem with Original Design

The original Phase 2 design introduced **7 network hops** for plugin-to-plugin calls:

```
Plugin A → Host Discovery → Host Registry → Host Proxy → Plugin B
  (1)          (2)              (3)            (4-7)
```

**Network path**:
1. Plugin A → Host: DiscoverService RPC
2. Host → Host: Registry lookup
3. Host → Plugin A: Return proxy endpoint
4. Plugin A → Host: Capability invocation
5. Host → Host: Token validation
6. Host → Host: Routing decision
7. Host → Plugin B: Proxied request

**Issues**:
- Excessive latency (7 hops for every call)
- Host becomes bottleneck and single point of failure
- Complex token management
- Service registry is single-tenant (one provider per service type)

### Recommendation: REJECT Phase 2 as designed

**Alternative A: Static Wiring (No Registry)**

Plugins declare service dependencies in their manifest, host wires them at startup:

```yaml
# plugin-manifest.yaml
name: app-plugin
requires:
  services:
    - type: logger
      version: "^1.0.0"
      provider: logger-plugin  # Explicitly specified
```

**Benefits**:
- No service discovery overhead
- Direct plugin-to-plugin connections (3 hops: A → Host auth → B)
- Deterministic startup (fails fast if dependency missing)
- Simple multi-tenancy (each plugin instance gets its own wiring)

**Drawbacks**:
- No dynamic service discovery
- Requires restart to change wiring
- Less flexible than registry

**Alternative B: Simplified Registry (No Proxy)**

Host registry returns direct plugin addresses, plugins connect directly:

```
Plugin A → Host Registry → Plugin A receives Plugin B's address → Plugin A → Plugin B
  (1)          (2)                  (3)                              (4)
```

**Benefits**:
- 4 hops instead of 7
- Host not in request path after discovery
- Registry can be multi-tenant (multiple providers per service type)

**Drawbacks**:
- Plugins need direct network connectivity
- Token refresh complexity (Plugin A must renew Plugin B tokens)
- Less host control over traffic

### Phase 2 Decision Point

Before implementing Phase 2, evaluate:
1. **Do users actually need plugin-to-plugin composition?** (Validate use case)
2. **Can composition happen at build time instead of runtime?** (Static wiring)
3. **Is 7-hop latency acceptable?** (Benchmark with real workloads)
4. **Can we use Alternative B to reduce hops?** (Direct connections)

**Recommendation**: Start with Alternative A (static wiring) in Phase 2, iterate based on user feedback.

## Comparison with go-plugin

| Feature | go-plugin GRPCBroker | connect-plugin Broker (Phase 1) |
|---------|---------------------|----------------------------------|
| **Direction** | Host → Plugin only | Host → Plugin (Phase 1), Bidirectional (Phase 2) |
| **Discovery** | None (hardcoded) | Static capability grants (Phase 1), Dynamic registry (Phase 2) |
| **Providers** | Host only | Host only (Phase 1), Host + Plugins (Phase 2) |
| **Security** | Broker ID (uint32) | JWT tokens with binding, caching, revocation |
| **Routing** | Direct gRPC | Direct handler invocation (Phase 1), HTTP proxy (Phase 2) |
| **Lifecycle** | Manual cleanup | Automatic on plugin health change |
| **Observability** | None | Structured events, metrics, tracing hooks |
| **Token Validation** | None | Multi-layer cache (LRU + bloom filter) |
| **Permission Model** | None | Fine-grained, hierarchical permissions |

## Implementation Checklist (Phase 1)

- [x] Capability vs service distinction
- [x] Capability grant system with JWT tokens
- [x] Token binding and validation caching
- [x] Permission enforcement
- [x] Host capability model
- [x] Observability hooks
- [x] Threat model
- [x] Phase 1/2 scope decision
- [ ] Service registry protocol (deferred to Phase 2)
- [ ] Service lifecycle management (deferred to Phase 2)
- [ ] Plugin-to-plugin communication (deferred to Phase 2)

## Next Steps

1. Implement broker proto for host capabilities (`proto/connectplugin/v1/broker.proto`)
2. Implement capability grant manager with token validation caching
3. Implement observability hook system
4. Design client configuration (KOR-qjhn)
5. Design server configuration (KOR-koba)
6. Validate Phase 2 use cases before implementing service registry
