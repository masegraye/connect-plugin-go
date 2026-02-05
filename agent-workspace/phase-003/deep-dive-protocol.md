# Protocol Security Deep Dive: connect-plugin-go

**Document Type:** Security Assessment
**Date:** 2026-01-29
**Scope:** RPC and Handshake Protocol Security Analysis
**Files Analyzed:**
- `proto/plugin/v1/handshake.proto`
- `proto/plugin/v1/broker.proto`
- `proto/plugin/v1/lifecycle.proto`
- `proto/plugin/v1/registry.proto`
- `handshake.go`
- `client.go`
- `broker.go`
- `router.go`
- `registry.go`
- `auth_token.go`
- `auth.go`
- `auth_mtls.go`
- `platform.go`
- `streaming.go`
- `lifecycle.go`

---

## Executive Summary

This assessment evaluates the protocol-level security of connect-plugin-go's RPC handshake, authentication, and service routing mechanisms. The analysis identifies **3 Critical**, **5 High**, **7 Medium**, and **4 Low** severity findings across the protocol implementation.

### Key Findings Summary

| Category | Critical | High | Medium | Low |
|----------|----------|------|--------|-----|
| Protocol Negotiation | 1 | 1 | 1 | 0 |
| Message Validation | 0 | 1 | 2 | 1 |
| Authentication/Authorization | 1 | 2 | 1 | 1 |
| Replay/MITM Protection | 1 | 0 | 2 | 1 |
| Streaming Security | 0 | 1 | 1 | 1 |
| **Total** | **3** | **5** | **7** | **4** |

---

## 1. Protocol Version Negotiation Security

### 1.1 No Cryptographic Binding of Protocol Version

**Severity:** CRITICAL
**Location:** `handshake.go:62-81`, `client.go:231-238`

**Finding:**
Protocol version negotiation lacks cryptographic integrity protection. Version fields (`core_protocol_version`, `app_protocol_version`) are transmitted as plain integers without authentication or signing.

```go
// handshake.go:62-68 - Server-side validation
if req.Msg.CoreProtocolVersion != 1 {
    return nil, connect.NewError(
        connect.CodeInvalidArgument,
        fmt.Errorf("unsupported core protocol version: %d (server supports: 1)", req.Msg.CoreProtocolVersion),
    )
}
```

```go
// client.go:231-238 - Client-side validation
if resp.Msg.CoreProtocolVersion != 1 {
    return fmt.Errorf("core protocol version mismatch: got %d, want 1", resp.Msg.CoreProtocolVersion)
}

if resp.Msg.AppProtocolVersion != int32(protocolVersion) {
    return fmt.Errorf("app protocol version mismatch: got %d, want %d",
        resp.Msg.AppProtocolVersion, protocolVersion)
}
```

**Attack Vector:**
A MITM attacker could modify version negotiation to force a downgrade to a hypothetical weaker protocol version (if future versions introduce backwards-compatible weaknesses).

**Remediation:**
1. Include protocol version in HMAC/signature calculation using runtime_token
2. Add nonce-based challenge-response to handshake
3. Consider protocol version pinning for high-security deployments

---

### 1.2 Version Negotiation Only Supports Exact Match

**Severity:** HIGH
**Location:** `handshake.go:70-81`, `proto/plugin/v1/handshake.proto:19-21`

**Finding:**
The current implementation only supports exact version matching with no range negotiation. This creates a fragile upgrade path and could lead to denial of service during rolling updates.

```protobuf
// handshake.proto:19-21
// App protocol version the client supports.
// v1 uses simple exact match - no negotiation of multiple versions.
int32 app_protocol_version = 2;
```

**Impact:**
- Rolling updates become complex when version mismatch causes connection failures
- No graceful degradation when minor version differences exist

**Remediation:**
1. Implement version range negotiation (min_version, max_version)
2. Add semantic versioning comparison logic
3. Support version capability advertisement

---

### 1.3 Protocol Downgrade Attack Surface

**Severity:** MEDIUM
**Location:** `handshake.go:70-81`

**Finding:**
While current implementation only supports version 1, the design does not include mechanisms to prevent future downgrade attacks. There is no "minimum acceptable version" enforcement or version deprecation signaling.

```go
// handshake.go:71-74
serverVersion := h.cfg.ProtocolVersion
if serverVersion == 0 {
    serverVersion = 1  // Default to 1 if not set
}
```

**Remediation:**
1. Add `minimum_supported_version` configuration
2. Implement version deprecation warnings in handshake response
3. Add server capability to reject deprecated versions

---

## 2. Message Field Validation

### 2.1 Unbounded String Fields in Handshake

**Severity:** HIGH
**Location:** `proto/plugin/v1/handshake.proto:23-40`

**Finding:**
Multiple string and map fields lack size limits, creating potential for resource exhaustion attacks:

```protobuf
// handshake.proto:23-40
string magic_cookie_key = 3;     // No size limit
string magic_cookie_value = 4;   // No size limit
repeated string requested_plugins = 5;  // No count limit
map<string, string> client_metadata = 6;  // No size or count limit
string self_id = 10;             // No size limit
string self_version = 11;        // No size limit
```

**Attack Vector:**
1. Send extremely large magic_cookie values (megabytes)
2. Send thousands of requested_plugins entries
3. Send client_metadata with millions of keys

**Impact:** Memory exhaustion, denial of service

**Remediation:**
1. Add protobuf validation rules (e.g., using buf validate)
2. Implement server-side validation with explicit limits:
   - `magic_cookie_*`: max 256 bytes
   - `requested_plugins`: max 100 entries
   - `client_metadata`: max 1000 entries, max 4KB total
   - `self_id`: max 256 bytes

---

### 2.2 Missing Input Sanitization for Runtime ID Generation

**Severity:** MEDIUM
**Location:** `handshake.go:195-203`

**Finding:**
The `generateRuntimeID` function performs minimal normalization but no sanitization of potentially malicious input:

```go
// handshake.go:195-203
func generateRuntimeID(selfID string) string {
    // Generate 4-character random suffix
    suffix := generateRandomHex(4)

    // Normalize self_id (lowercase, replace spaces with hyphens)
    normalized := strings.ToLower(strings.ReplaceAll(selfID, " ", "-"))

    return fmt.Sprintf("%s-%s", normalized, suffix)
}
```

**Attack Vector:**
- Inject special characters (newlines, null bytes, path traversal sequences)
- Create runtime IDs that could be confused with system IDs
- Potential log injection via malformed self_id

**Remediation:**
```go
// Proposed sanitization
func generateRuntimeID(selfID string) string {
    // Validate input
    if len(selfID) > 256 {
        selfID = selfID[:256]
    }

    // Remove all non-alphanumeric characters except hyphens
    reg := regexp.MustCompile(`[^a-zA-Z0-9-]`)
    normalized := reg.ReplaceAllString(strings.ToLower(selfID), "")

    // Ensure non-empty
    if normalized == "" {
        normalized = "plugin"
    }

    suffix := generateRandomHex(8) // Increased from 4
    return fmt.Sprintf("%s-%s", normalized, suffix)
}
```

---

### 2.3 Service Path Validation Insufficient

**Severity:** MEDIUM
**Location:** `registry.go:112-122`

**Finding:**
The `RegisterService` function stores `endpoint_path` without validating it follows expected patterns:

```go
// registry.go:112-122
provider := &ServiceProvider{
    RegistrationID: registrationID,
    RuntimeID:      runtimeID,
    ServiceType:    req.Msg.ServiceType,
    Version:        req.Msg.Version,
    EndpointPath:   req.Msg.EndpointPath,  // No validation
    Metadata:       req.Msg.Metadata,
    RegisteredAt:   time.Now(),
}
```

**Attack Vector:**
- Register malformed paths to confuse routing
- Path traversal attempts (`../../../etc`)
- Null byte injection

**Remediation:**
1. Validate paths match expected pattern: `/[a-zA-Z0-9_]+\.v[0-9]+\.[A-Z][a-zA-Z0-9_]+/`
2. Reject paths with `.`, `..`, null bytes, or non-printable characters

---

### 2.4 Semver Comparison Using String Comparison

**Severity:** LOW
**Location:** `registry.go:241-254`

**Finding:**
Version comparison uses naive string comparison instead of proper semantic versioning:

```go
// registry.go:248-251
// Simple string comparison - TODO: use semver
if p.Version >= minVersion {
    compatible = append(compatible, p)
}
```

**Impact:**
- `1.9.0 >= 1.10.0` would incorrectly evaluate to true (string comparison)
- Could lead to incompatible version selection

**Remediation:**
Use a proper semver library like `github.com/Masterminds/semver/v3`

---

## 3. Replay Attack Resistance

### 3.1 Token Generation Lacks Nonce/Timestamp

**Severity:** CRITICAL
**Location:** `broker.go:188-193`, `handshake.go:88`

**Finding:**
Token generation uses only random bytes without incorporating temporal or request-specific data:

```go
// broker.go:188-193
func generateToken() string {
    b := make([]byte, 32)
    rand.Read(b)
    return base64.URLEncoding.EncodeToString(b)
}
```

**Attack Vector:**
1. Intercept valid token during transmission
2. Replay the token indefinitely (no expiration)
3. Token remains valid even if original session should have terminated

**Current Token Storage (handshake.go:91-93):**
```go
// Store token for later validation
h.mu.Lock()
h.tokens[runtimeID] = runtimeToken
h.mu.Unlock()
```

**Impact:**
- Tokens never expire
- No revocation mechanism
- Compromised token usable indefinitely

**Remediation:**
1. Add timestamp to token generation
2. Implement token expiration (TTL)
3. Add token revocation capability
4. Consider JWT with expiration claims

```go
type TokenData struct {
    Token     string
    IssuedAt  time.Time
    ExpiresAt time.Time
    RuntimeID string
}

func (h *HandshakeServer) ValidateToken(runtimeID, token string) bool {
    h.mu.RLock()
    defer h.mu.RUnlock()

    data, ok := h.tokens[runtimeID]
    if !ok {
        return false
    }

    if time.Now().After(data.ExpiresAt) {
        return false
    }

    return subtle.ConstantTimeCompare([]byte(data.Token), []byte(token)) == 1
}
```

---

### 3.2 Grant IDs Not Time-Bounded

**Severity:** MEDIUM
**Location:** `broker.go:95-117`

**Finding:**
Capability grants are stored indefinitely with no expiration:

```go
// broker.go:99-105
grant := &grantInfo{
    grantID:        grantID,
    capabilityType: req.Msg.CapabilityType,
    token:          token,
    handler:        handler,
}
b.grants[grantID] = grant  // Stored indefinitely
```

**Remediation:**
1. Add `ExpiresAt` field to `grantInfo`
2. Implement periodic cleanup of expired grants
3. Return expiration info in `CapabilityGrant` response

---

### 3.3 No Request Nonce in Protocol

**Severity:** MEDIUM
**Location:** `proto/plugin/v1/handshake.proto:14-41`

**Finding:**
The HandshakeRequest lacks a nonce field for replay prevention:

```protobuf
message HandshakeRequest {
    int32 core_protocol_version = 1;
    int32 app_protocol_version = 2;
    // ... no nonce field
}
```

**Remediation:**
Add request nonce and server challenge:

```protobuf
message HandshakeRequest {
    // ... existing fields
    bytes client_nonce = 12;  // Client-generated random bytes
}

message HandshakeResponse {
    // ... existing fields
    bytes server_nonce = 12;  // Server-generated random bytes
    bytes challenge_response = 13;  // HMAC(client_nonce || server_nonce, shared_secret)
}
```

---

### 3.4 Handshake Idempotency Without Tracking

**Severity:** LOW
**Location:** `proto/plugin/v1/handshake.proto:9-11`

**Finding:**
Handshake is documented as idempotent but generates new tokens each time:

```protobuf
// Handshake performs version negotiation and plugin discovery.
// This is idempotent - calling multiple times returns the same result.
rpc Handshake(HandshakeRequest) returns (HandshakeResponse);
```

But in `handshake.go:83-94`:
```go
// Phase 2: Generate runtime identity
if req.Msg.SelfId != "" {
    runtimeID = generateRuntimeID(req.Msg.SelfId)  // NEW ID each time
    runtimeToken = generateToken()                  // NEW token each time

    h.mu.Lock()
    h.tokens[runtimeID] = runtimeToken  // Overwrites previous
    h.mu.Unlock()
}
```

**Impact:**
- Each handshake creates new runtime identity
- Previous tokens invalidated without notification
- Documentation misleading

**Remediation:**
1. Track existing handshakes by client identity
2. Return existing runtime_id/token if already handshaken
3. Or update documentation to reflect actual behavior

---

## 4. Man-in-the-Middle Attack Surface

### 4.1 No Mutual Authentication in Default Configuration

**Severity:** CRITICAL
**Location:** `client.go:146-147`

**Finding:**
The default HTTP client has no TLS configuration:

```go
// client.go:146-147
// Create HTTP client for Connect RPCs
// TODO: Add TLS, timeouts, interceptors from ClientOptions
c.httpClient = &http.Client{}
```

**Impact:**
- All traffic unencrypted by default
- Full MITM attack capability
- Token theft via network interception

**Remediation:**
1. Enforce TLS by default
2. Provide clear warning when using HTTP
3. Document security implications

```go
func (c *Client) Connect(ctx context.Context) error {
    // ...

    // Default to TLS unless explicitly disabled
    if c.cfg.Endpoint != "" && !strings.HasPrefix(c.cfg.Endpoint, "https://") {
        if !c.cfg.AllowInsecure {
            return fmt.Errorf("HTTPS required (set AllowInsecure=true to override)")
        }
        log.Println("WARNING: Using insecure HTTP connection")
    }
}
```

---

### 4.2 mTLS Implementation Incomplete

**Severity:** HIGH
**Location:** `auth_mtls.go:58-84`

**Finding:**
The mTLS server interceptor does not actually extract client certificates:

```go
// auth_mtls.go:58-84
func (m *MTLSAuth) ServerInterceptor() connect.UnaryInterceptorFunc {
    return func(next connect.UnaryFunc) connect.UnaryFunc {
        return func(ctx context.Context, req connect.AnyRequest) (connect.AnyResponse, error) {
            // For now, this is a simplified implementation
            // In production, you'd extract from the TLS connection state
            // via req.HTTPRequest().TLS.PeerCertificates

            // Placeholder: Assume cert validation happened at TLS layer
            // Store placeholder auth context
            authCtx := &AuthContext{
                Identity: "mtls-client",  // HARDCODED PLACEHOLDER
                Provider: "mtls",
                Claims:   map[string]string{"verified": "true"},
            }
            ctx = WithAuthContext(ctx, authCtx)

            return next(ctx, req)
        }
    }
}
```

**Impact:**
- mTLS identity extraction not functional
- All mTLS clients appear as "mtls-client"
- Authorization decisions cannot distinguish clients

**Remediation:**
Complete the implementation:

```go
func (m *MTLSAuth) ServerInterceptor() connect.UnaryInterceptorFunc {
    return func(next connect.UnaryFunc) connect.UnaryFunc {
        return func(ctx context.Context, req connect.AnyRequest) (connect.AnyResponse, error) {
            // Get HTTP request to access TLS state
            httpReq := req.(*connect.Request[any]).HTTPRequest()
            if httpReq == nil || httpReq.TLS == nil {
                return nil, connect.NewError(connect.CodeUnauthenticated,
                    fmt.Errorf("TLS connection required"))
            }

            if len(httpReq.TLS.PeerCertificates) == 0 {
                return nil, connect.NewError(connect.CodeUnauthenticated,
                    fmt.Errorf("client certificate required"))
            }

            cert := httpReq.TLS.PeerCertificates[0]
            identity, claims := m.ExtractIdentity(cert)

            authCtx := &AuthContext{
                Identity: identity,
                Provider: "mtls",
                Claims:   claims,
            }
            ctx = WithAuthContext(ctx, authCtx)

            return next(ctx, req)
        }
    }
}
```

---

### 4.3 Token Comparison Not Constant-Time

**Severity:** HIGH
**Location:** `handshake.go:180-190`, `broker.go:164-165`

**Finding:**
Token validation uses standard string comparison, which is vulnerable to timing attacks:

```go
// handshake.go:186-189
expectedToken, ok := h.tokens[runtimeID]
if !ok {
    return false
}
return expectedToken == token  // Timing-vulnerable comparison
```

```go
// broker.go:164-165
if grant.token != token {
    http.Error(w, "invalid token", http.StatusUnauthorized)
    return
}
```

**Impact:**
- Timing side-channel allows token recovery
- Attack requires many requests but is feasible

**Remediation:**
Use constant-time comparison:

```go
import "crypto/subtle"

return subtle.ConstantTimeCompare([]byte(expectedToken), []byte(token)) == 1
```

---

## 5. Trust Boundary Analysis (Host vs Plugin)

### 5.1 Plugin-Provided Metadata Trusted Without Validation

**Severity:** MEDIUM
**Location:** `platform.go:82-115`

**Finding:**
The platform trusts plugin-provided metadata from `GetPluginInfo`:

```go
// platform.go:86-89
infoClient := NewPluginIdentityClient(config.Endpoint, nil)
infoResp, err := infoClient.GetPluginInfo(ctx)

// platform.go:92-95
selfID := infoResp.SelfId  // Trusted from plugin
if selfID == "" {
    selfID = config.SelfID
}
```

**Impact:**
- Plugin can claim any identity
- Plugin can declare capabilities it doesn't have
- Host makes routing decisions based on untrusted data

**Remediation:**
1. Host should maintain authoritative plugin registry
2. Validate plugin claims against expected configuration
3. Sign plugin metadata during deployment

---

### 5.2 Router Forwards Provider Metadata Without Sanitization

**Severity:** LOW
**Location:** `router.go:103-111`

**Finding:**
Provider endpoint URLs from metadata are used directly:

```go
// router.go:103-111
baseURL, ok := r.pluginEndpoints[providerID]
if !ok {
    // Fall back to metadata base_url (Model B self-registration)
    if baseURLMeta, exists := provider.Metadata["base_url"]; exists {
        baseURL = baseURLMeta  // Trusted from plugin
    }
}
```

**Impact:**
- Plugin could redirect traffic to attacker-controlled endpoints
- SSRF via plugin metadata injection

**Remediation:**
1. Validate URLs against allowlist
2. Only permit URLs within expected network ranges
3. Host should control endpoint registration

---

### 5.3 Header Forwarding in Router

**Severity:** LOW
**Location:** `router.go:149-159`

**Finding:**
The router strips `Authorization` and `X-Plugin-Runtime-ID` but forwards all other headers:

```go
// router.go:149-159
for key, values := range req.Header {
    canonicalKey := http.CanonicalHeaderKey(key)
    if canonicalKey == "Authorization" || canonicalKey == "X-Plugin-Runtime-Id" {
        continue
    }
    for _, value := range values {
        proxyReq.Header.Add(key, value)
    }
}
```

**Impact:**
- Potential for header injection attacks
- Headers like `X-Forwarded-For`, `X-Real-IP` could be manipulated
- Internal headers could leak information

**Remediation:**
1. Allowlist approach: only forward specific headers
2. Strip all `X-` prefixed internal headers
3. Add caller identity as trusted header

---

## 6. Streaming Message Security (WatchService)

### 6.1 No Rate Limiting on Watch Connections

**Severity:** HIGH
**Location:** `registry.go:399-456`

**Finding:**
The `WatchService` streaming endpoint has no limits on concurrent watchers:

```go
// registry.go:406-417
wctx, cancel := context.WithCancel(ctx)
watcher := &serviceWatcher{
    ch:     make(chan *connectpluginv1.WatchServiceEvent, 10),
    ctx:    wctx,
    cancel: cancel,
}

// Register watcher - no limit checked
r.watchers[serviceType] = append(r.watchers[serviceType], watcher)
```

**Attack Vector:**
1. Malicious plugin opens thousands of watch connections
2. Each connection consumes memory (channel, context, goroutine)
3. Resource exhaustion denial of service

**Remediation:**
1. Limit watchers per runtime_id
2. Limit total watchers per service type
3. Add authentication for watch requests

```go
const maxWatchersPerService = 100
const maxWatchersPerPlugin = 10

func (r *ServiceRegistry) WatchService(...) error {
    runtimeID := req.Header().Get("X-Plugin-Runtime-ID")

    r.mu.Lock()
    if len(r.watchers[serviceType]) >= maxWatchersPerService {
        r.mu.Unlock()
        return connect.NewError(connect.CodeResourceExhausted,
            fmt.Errorf("too many watchers for service %s", serviceType))
    }
    // ... count watchers by runtimeID
    r.mu.Unlock()
}
```

---

### 6.2 Non-Blocking Send in Watcher Notification

**Severity:** MEDIUM
**Location:** `registry.go:463-469`

**Finding:**
Notification uses non-blocking send, silently dropping events:

```go
// registry.go:463-469
for _, watcher := range r.watchers[serviceType] {
    select {
    case watcher.ch <- event:
    default:
        // Watcher not reading, skip
    }
}
```

**Impact:**
- Plugins may miss critical service availability events
- No acknowledgment mechanism
- Security-relevant events could be lost

**Remediation:**
1. Add message sequence numbers
2. Implement acknowledgment protocol
3. Log dropped events for monitoring
4. Consider backpressure mechanism

---

### 6.3 Watch Channel Unbounded Queue

**Severity:** LOW
**Location:** `registry.go:410`

**Finding:**
Watch channel has fixed buffer of 10:

```go
ch:     make(chan *connectpluginv1.WatchServiceEvent, 10),
```

**Impact:**
- Slow consumer causes event drops (see 6.2)
- Buffer size not configurable

**Remediation:**
1. Make buffer size configurable
2. Implement adaptive backpressure
3. Monitor and alert on drops

---

## 7. Header Injection Through Metadata Fields

### 7.1 Service Metadata Allows Arbitrary Key-Value Pairs

**Severity:** MEDIUM
**Location:** `proto/plugin/v1/registry.proto:39-41`, `registry.go:119-120`

**Finding:**
Service metadata can contain any string keys/values:

```protobuf
// registry.proto:39-41
// Service metadata (optional).
map<string, string> metadata = 4;
```

```go
// registry.go:119-120
Metadata:       req.Msg.Metadata,  // Stored as-is
```

Used in router (router.go:106):
```go
if baseURLMeta, exists := provider.Metadata["base_url"]; exists {
    baseURL = baseURLMeta  // Used to construct request URL
}
```

**Attack Vector:**
1. Plugin registers with `base_url: "http://attacker.com"`
2. Traffic routed to attacker-controlled server
3. Or inject newlines to poison logs

**Remediation:**
1. Allowlist valid metadata keys
2. Validate metadata values (no control characters)
3. Never use metadata for security-relevant decisions

---

## 8. Comparison with go-plugin Security Model

### 8.1 Security Feature Comparison

| Feature | go-plugin | connect-plugin-go | Assessment |
|---------|-----------|-------------------|------------|
| **Process Isolation** | Yes (subprocess) | No (network) | connect-plugin requires explicit network security |
| **AutoMTLS** | Yes (env var exchange) | Incomplete | go-plugin has working mTLS; connect-plugin placeholder only |
| **Magic Cookie** | Yes (UX, not security) | Yes (equivalent) | Parity - both use for basic validation |
| **Checksum Verification** | Yes (SecureConfig) | No | Missing - go-plugin verifies plugin binary |
| **Process Termination** | Direct kill | Graceful + timeout | connect-plugin weaker if plugin ignores shutdown |
| **Token Authentication** | N/A (process boundary) | Yes | connect-plugin adds layer, but implementation has issues |
| **Certificate Pinning** | Via AutoMTLS | Not implemented | Missing in connect-plugin |

### 8.2 go-plugin Security Advantages

1. **Process Boundary:** Subprocess model provides OS-level isolation
2. **AutoMTLS Works:** Fully implemented certificate exchange
3. **Binary Verification:** Can verify plugin checksum before execution
4. **Kill Semantics:** Can forcefully terminate malicious plugins

### 8.3 connect-plugin-go Security Advantages

1. **Token-Based Auth:** Can integrate with existing auth systems (OIDC, JWT)
2. **Network Flexibility:** Can use service mesh security
3. **Health Monitoring:** Protocol-level health reporting
4. **Graceful Shutdown:** Structured shutdown with acknowledgment

### 8.4 Security Gap Analysis

| Gap | Severity | Recommendation |
|-----|----------|----------------|
| No AutoMTLS equivalent | HIGH | Implement certificate exchange protocol |
| No binary verification | MEDIUM | Add plugin signature verification |
| No forced termination | MEDIUM | Document timeout behavior, add escalation |
| Incomplete mTLS | HIGH | Complete implementation |
| No replay protection | CRITICAL | Add nonces and token expiration |

---

## 9. Remediation Priority Matrix

### Critical (Immediate Action Required)

| Finding | Section | Effort | Impact |
|---------|---------|--------|--------|
| Token replay vulnerability | 3.1 | Medium | Token theft enables indefinite impersonation |
| No TLS by default | 4.1 | Low | All traffic interceptionable |
| Protocol version tampering | 1.1 | High | Future downgrade attacks |

### High (Within 30 Days)

| Finding | Section | Effort | Impact |
|---------|---------|--------|--------|
| Unbounded message fields | 2.1 | Medium | Denial of service |
| mTLS not functional | 4.2 | Medium | Advertised security feature broken |
| Timing-vulnerable comparison | 4.3 | Low | Token recovery via timing |
| Unbounded watch connections | 6.1 | Medium | Resource exhaustion |
| Exact version match fragility | 1.2 | Medium | Deployment issues |

### Medium (Within 90 Days)

| Finding | Section | Effort | Impact |
|---------|---------|--------|--------|
| RuntimeID generation | 2.2 | Low | Log injection, confusion |
| Service path validation | 2.3 | Low | Routing confusion |
| Grant expiration | 3.2 | Medium | Stale grants accumulate |
| Request nonces | 3.3 | Medium | Replay window |
| Protocol downgrade surface | 1.3 | Medium | Future risk |
| Plugin metadata trust | 5.1 | Medium | Identity spoofing |
| Non-blocking watch send | 6.2 | Medium | Event loss |

### Low (Technical Debt)

| Finding | Section | Effort | Impact |
|---------|---------|--------|--------|
| Semver comparison | 2.4 | Low | Incorrect version matching |
| Handshake idempotency | 3.4 | Low | Documentation/behavior mismatch |
| Router metadata | 5.2 | Low | SSRF potential |
| Header forwarding | 5.3 | Low | Information leakage |

---

## 10. Appendix: Code References

### Key Security-Relevant Files

| File | Lines | Security Function |
|------|-------|-------------------|
| `handshake.go` | 1-215 | Token generation, validation |
| `broker.go` | 1-194 | Capability grant management |
| `router.go` | 1-187 | Service call authentication |
| `registry.go` | 1-531 | Service registration, discovery |
| `auth_token.go` | 1-116 | Token auth interceptors |
| `auth_mtls.go` | 1-120 | mTLS configuration |
| `client.go` | 1-421 | Client-side handshake |
| `platform.go` | 1-379 | Plugin lifecycle management |
| `lifecycle.go` | 1-149 | Health state tracking |

### Security-Critical Functions

1. `generateToken()` - `broker.go:188-193`
2. `ValidateToken()` - `handshake.go:180-190`
3. `generateRuntimeID()` - `handshake.go:195-203`
4. `proxyRequest()` - `router.go:136-186`
5. `RegisterService()` - `registry.go:96-136`
6. `doHandshake()` - `client.go:186-271`

---

## 11. Conclusion

connect-plugin-go implements a reasonable security foundation for network-based plugins, but has several critical gaps compared to go-plugin's process-based model:

1. **Token management needs hardening:** Expiration, replay protection, constant-time comparison
2. **TLS must be enforced:** Default HTTP is unacceptable for production
3. **mTLS implementation incomplete:** Advertised feature doesn't work
4. **Input validation insufficient:** Resource exhaustion attacks possible

The framework shows good security thinking (token-based auth, header stripping, graceful shutdown), but implementation gaps create exploitable vulnerabilities. Prioritize the Critical findings before production deployment.

---

*Report generated as part of connect-plugin-go security review, Phase 003*
