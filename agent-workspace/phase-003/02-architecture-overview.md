# Architecture Overview

## System Architecture

connect-plugin-go implements a remote-first plugin system using Connect RPC (HTTP-based RPC protocol compatible with gRPC). Unlike HashiCorp's go-plugin which uses Unix sockets and subprocess management, this library is designed for network-distributed plugin architectures.

```
┌─────────────────────────────────────────────────────────────────────┐
│                           HOST APPLICATION                          │
├─────────────────────────────────────────────────────────────────────┤
│  ┌───────────┐  ┌───────────┐  ┌───────────┐  ┌──────────────────┐ │
│  │  Platform │  │  Service  │  │ Lifecycle │  │   Capability     │ │
│  │  Manager  │  │  Registry │  │  Server   │  │     Broker       │ │
│  └─────┬─────┘  └─────┬─────┘  └─────┬─────┘  └────────┬─────────┘ │
│        │              │              │                  │           │
│        └──────────────┴──────────────┴──────────────────┘           │
│                              │                                       │
│                    ┌─────────┴─────────┐                            │
│                    │   Service Router  │                            │
│                    │  /services/{type} │                            │
│                    └─────────┬─────────┘                            │
│                              │                                       │
│  ┌───────────────────────────┴───────────────────────────────────┐  │
│  │                     Handshake Server                          │  │
│  │                  (Protocol Negotiation)                       │  │
│  └───────────────────────────────────────────────────────────────┘  │
└──────────────────────────────────┬──────────────────────────────────┘
                                   │ HTTP/Connect RPC
          ┌────────────────────────┼────────────────────────┐
          │                        │                        │
          ▼                        ▼                        ▼
┌─────────────────┐      ┌─────────────────┐      ┌─────────────────┐
│    PLUGIN A     │      │    PLUGIN B     │      │    PLUGIN C     │
│  (Logger v1.0)  │      │  (Cache v2.1)   │      │   (Auth v1.5)   │
├─────────────────┤      ├─────────────────┤      ├─────────────────┤
│ PluginIdentity  │      │ PluginIdentity  │      │ PluginIdentity  │
│ PluginControl   │      │ PluginControl   │      │ PluginControl   │
│ Service Handler │      │ Service Handler │      │ Service Handler │
│ Client (to host)│      │ Client (to host)│      │ Client (to host)│
└─────────────────┘      └─────────────────┘      └─────────────────┘
```

## Core Components

### 1. Plugin Interface (`plugin.go`)

The central abstraction that all plugins implement:

```go
type Plugin interface {
    Metadata() PluginMetadata
    ConnectServer(impl any) (path string, handler http.Handler, error error)
    ConnectClient(baseURL string, httpClient connect.HTTPClient) (any, error)
}
```

**Security Consideration:** The plugin interface accepts `any` type for implementations, requiring runtime type assertions. This is a design trade-off for flexibility.

### 2. Client (`client.go`)

Manages the connection lifecycle from plugin to host:

- Lazy connection (connects on first Dispense)
- Service discovery integration
- Runtime identity management
- Health reporting

**Key Security Functions:**
- `doHandshake()`: Protocol negotiation with magic cookie validation
- `ReportHealth()`: Sends plugin health with authentication headers
- `SetRuntimeIdentity()`: Receives host-assigned identity

### 3. Server (`server.go`)

Serves plugin implementations to clients:

```go
type ServeConfig struct {
    Plugins            PluginSet
    Impls              map[string]any
    HealthService      *HealthServer
    CapabilityBroker   *CapabilityBroker
    LifecycleService   *LifecycleServer
    ServiceRegistry    *ServiceRegistry
    ServiceRouter      *ServiceRouter
}
```

**HTTP Endpoint Registration:**
- `/plugin.v1.HandshakeService/` - Protocol handshake
- `/plugin.v1.HealthService/` - Health checking
- `/healthz`, `/readyz` - Kubernetes probes
- `/broker/`, `/capabilities/` - Capability broker
- `/services/` - Plugin-to-plugin routing
- `/{service.path}/` - Individual plugin services

### 4. Handshake Protocol (`handshake.go`)

Two-phase protocol negotiation:

**Phase 1 (Basic):**
1. Magic cookie validation
2. Protocol version negotiation
3. Plugin availability check
4. Server metadata exchange

**Phase 2 (Extended):**
1. Self-identity declaration
2. Runtime ID assignment
3. Runtime token generation
4. Service capability advertisement

### 5. Service Registry (`registry.go`)

Multi-provider service registry with:
- Service registration/unregistration
- Provider selection strategies (first, round-robin, random, weighted)
- Health-aware provider filtering
- Service watching for real-time updates

### 6. Service Router (`router.go`)

HTTP reverse proxy for plugin-to-plugin communication:

```
Plugin A → /services/{type}/{provider-id}/{method} → Host → Plugin B
```

**Security Functions:**
- Caller identity validation (X-Plugin-Runtime-ID)
- Token validation (Authorization: Bearer)
- Provider health checking
- Request proxying

### 7. Capability Broker (`broker.go`)

Manages host-provided capabilities:

- Capability registration by host
- Grant issuance with bearer tokens
- Request routing to capability handlers

### 8. Authentication System (`auth.go`, `auth_token.go`, `auth_mtls.go`)

Pluggable authentication with two built-in providers:

**Token Auth:**
- Bearer token in Authorization header
- Configurable header name and prefix
- Custom validator function

**mTLS Auth:**
- Client certificate verification
- Identity extraction from certificate
- TLS configuration helpers

### 9. Plugin Launcher (`launcher.go`, `launch_*.go`)

Pluggable plugin instantiation strategies:

**Process Strategy:**
- Launches plugins as child processes
- Environment variable configuration
- Port-based readiness checking

**In-Memory Strategy:**
- Launches plugins as goroutines
- Direct loopback communication
- Shared process memory

### 10. Platform Manager (`platform.go`)

Orchestrates multi-plugin deployments:

- Dependency graph management
- Plugin addition/removal
- Blue-green deployment (ReplacePlugin)
- Impact analysis

## Data Flow

### Handshake Flow (Model B - Self-Registering)

```
Plugin                                Host
  │                                    │
  ├──Handshake(self_id, version)──────►│
  │                                    ├── Validate magic cookie
  │                                    ├── Generate runtime_id
  │                                    ├── Generate runtime_token
  │◄──HandshakeResponse(runtime_id)────┤
  │                                    │
  ├──RegisterService(type, version)───►│ (with X-Plugin-Runtime-ID header)
  │◄──RegisterServiceResponse(reg_id)──┤
  │                                    │
  ├──ReportHealth(HEALTHY)────────────►│
  │                                    │
```

### Service Call Flow (Plugin-to-Plugin)

```
Plugin A                      Host                        Plugin B
  │                            │                            │
  ├─DiscoverService("logger")─►│                            │
  │◄─ServiceEndpoint──────────┤                            │
  │                            │                            │
  ├─/services/logger/B/{method}►├─────Proxy Request────────►│
  │  (with runtime_id + token) │  (strips auth headers)     │
  │                            │                            │
  │◄──────────Response─────────┤◄────────Response───────────┤
```

## Trust Model

### Host → Plugin Trust
- Host initiates plugin processes (if using ProcessStrategy)
- Host assigns runtime identities
- Host can request plugin shutdown
- Host validates all plugin registrations

### Plugin → Host Trust
- Plugin must present valid magic cookie
- Plugin receives runtime token from host
- Plugin must authenticate all host API calls
- Plugin trusts service registry responses

### Plugin → Plugin Trust (Mediated)
- All inter-plugin communication routes through host
- Host validates both caller and provider
- No direct plugin-to-plugin authentication
- Trust is transitive through host

## Deployment Models

### Model A: Managed Deployment
Host controls plugin lifecycle via Platform:
- Host calls `Platform.AddPlugin()`
- Host calls plugin's `PluginIdentity.SetRuntimeIdentity()`
- Plugin registers services after identity assignment

### Model B: Unmanaged Deployment
Plugins self-register with host:
- Plugin initiated independently (sidecar, container)
- Plugin calls `HandshakeService.Handshake()` with self_id
- Host assigns runtime identity in response
- Plugin registers services after handshake

## Key Design Decisions

### 1. HTTP/Connect vs gRPC
**Decision:** Use Connect RPC (HTTP-based)
**Rationale:**
- Browser compatibility
- HTTP/1.1 fallback
- Standard HTTP tooling
- No binary protocol debugging difficulty

### 2. Centralized Routing vs Direct Communication
**Decision:** All plugin-to-plugin calls route through host
**Rationale:**
- Centralized observability
- Host-controlled provider selection
- Simplified authentication model
- Easier circuit breaking

### 3. Bearer Tokens vs Session IDs
**Decision:** Bearer tokens per plugin instance
**Rationale:**
- Stateless validation
- No session lookup overhead
- Compatible with standard HTTP auth

### 4. Pluggable Launch Strategies
**Decision:** Strategy pattern for plugin instantiation
**Rationale:**
- Support process, in-memory, and future strategies
- Test flexibility
- Deployment flexibility

## Comparison with go-plugin

| Aspect | go-plugin | connect-plugin-go |
|--------|-----------|-------------------|
| Communication | Unix socket + mTLS | HTTP/Connect RPC |
| Process Model | Always subprocess | Configurable |
| Language Support | Go only (native) | Any Connect client |
| Discovery | Local only | Network service discovery |
| Trust Model | Host → Plugin | Bidirectional |
| Network Support | Single machine | Distributed |
| Health Checking | Process liveness | Application-level |
| Plugin-to-Plugin | Through host only | Through host router |
