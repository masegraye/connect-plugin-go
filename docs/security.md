# Security Guide

This guide covers the security model, deployment practices, and threat considerations for connect-plugin-go.

## Table of Contents

- [Security Model](#security-model)
- [Production Deployment](#production-deployment)
- [Authentication Options](#authentication-options)
- [Common Misconfigurations](#common-misconfigurations)
- [Threat Model](#threat-model)
- [Security Checklist](#security-checklist)
- [Recent Security Fixes](#recent-security-fixes)
- [Future Enhancements](#future-enhancements)

## Security Model

### Trust Boundaries

Connect-plugin-go operates with three primary trust boundaries:

```
┌─────────────────────────────────────────────────┐
│           Operator/Administrator                │
│              (Trusted Entity)                   │
└──────────────────┬──────────────────────────────┘
                   │
         ┌─────────┴─────────┐
         │                   │
    ┌────▼────┐         ┌────▼────┐
    │  Host   │◄───────►│ Plugin  │
    │Platform │   RPC   │ Service │
    └─────────┘         └─────────┘
         │                   │
    Trust Boundary      Trust Boundary
```

**Host Platform:**
- Orchestrates plugin lifecycle
- Manages service registry
- Provides capability broker
- Issues runtime identities and tokens

**Plugin Services:**
- Provide business logic implementations
- Consume other plugin services
- Request host capabilities
- Report health status

**Network:**
- HTTP/2 over TCP (Connect RPC)
- Plaintext by default (WARNING: configure TLS)
- TLS recommended for production
- mTLS support planned (Phase 3)

### Security Guarantees

**What connect-plugin-go provides:**

1. **Authentication Framework**: Token-based authentication for runtime identities
2. **Capability Grants**: Scoped access tokens for host capabilities
3. **Cryptographic Tokens**: 256-bit tokens using crypto/rand
4. **Constant-Time Comparison**: Timing attack resistance in token validation
5. **Health Tracking**: Plugin health monitoring and isolation
6. **Service Registry**: Plugin-to-plugin service discovery

**What connect-plugin-go does NOT provide:**

1. **TLS Enforcement**: TLS must be configured externally (warnings provided)
2. **mTLS Support**: Mutual TLS authentication not yet implemented
3. **Token Expiration**: Runtime tokens never expire (planned enhancement)
4. **Rate Limiting**: No built-in protection against request flooding
5. **Authorization Policies**: Service registration authorization is basic
6. **Network Isolation**: Physical/network security is operator responsibility

### Magic Cookie: Validation, Not Security

The magic cookie is used for **accidental misconfiguration detection**, not security:

```go
const (
    DefaultMagicCookieKey   = "CONNECT_PLUGIN"
    DefaultMagicCookieValue = "d3f40b3c2e1a5f8b9c4d7e6a1b2c3d4e"
)
```

**What the magic cookie is:**
- A static identifier to verify compatible protocol versions
- Transmitted in plaintext during handshake
- Same value for all plugins using defaults
- Helps catch version mismatches and configuration errors

**What the magic cookie is NOT:**
- Not a secret or authentication mechanism
- Not encrypted or hashed
- Does not prevent malicious actors
- Does not protect against intentional attacks

**Security Implication:** Do NOT rely on the magic cookie for security. Use proper authentication (tokens) and encryption (TLS).

## Production Deployment

### TLS is REQUIRED for Production

**Critical:** Connect-plugin-go transmits credentials in plaintext without TLS. Production deployments MUST use TLS to protect:

- Runtime tokens (256-bit authentication tokens)
- Capability bearer tokens
- Plugin service data
- Service registration metadata

**Warning Detection:**

The framework warns when operating without TLS:

```
WARN [connectplugin]: Non-TLS plugin endpoint
  endpoint: http://localhost:8080
  impact: credentials/tokens/plugin-data transmitted in plaintext
  risk: Man-in-the-middle attacks, credential theft
  resolution: Use https:// endpoint or configure TLS
  suppress: CONNECTPLUGIN_DISABLE_TLS_WARNING=1 (testing only)
```

### Deployment Architectures

#### Option 1: End-to-End TLS (Recommended)

```
┌─────────┐  HTTPS  ┌─────────┐  HTTPS  ┌─────────┐
│  Host   │────────►│ Plugin  │────────►│ Plugin  │
│Platform │  :443   │Service A│  :443   │Service B│
└─────────┘         └─────────┘         └─────────┘
```

**Configuration:**
- Each component serves HTTPS with valid certificates
- Host connects to plugins via https:// endpoints
- Plugins connect to each other via https:// (routed through host)

**Benefits:**
- Strong end-to-end security
- No plaintext transmission at any layer
- Compatible with zero-trust network architectures

**Future Support:**
TLS configuration in `ServeConfig` is planned. Current workaround: Use a reverse proxy.

#### Option 2: TLS Termination at Load Balancer

```
┌────────┐ HTTPS ┌─────────┐ HTTP ┌─────────┐ HTTP ┌─────────┐
│Internet│──────►│ LB/Proxy│─────►│  Host   │─────►│ Plugin  │
└────────┘       └─────────┘      │Platform │      │ Service │
   :443              :443          └─────────┘      └─────────┘
                                     :8080             :8081
```

**Configuration:**
- Public-facing load balancer handles TLS
- Internal communication over plaintext HTTP
- Plugins run on internal network only

**Risks:**
- Internal network traffic is unencrypted
- Compromised internal network exposes credentials
- Does not protect against lateral movement attacks

**Recommendation:** Use only if internal network is strongly isolated (dedicated VPC, network policies).

#### Option 3: Service Mesh (Kubernetes)

```
┌─────────┐ mTLS ┌────────┐ mTLS ┌─────────┐
│  Host   │◄────►│ Istio/ │◄────►│ Plugin  │
│ + Proxy │      │ Linkerd│      │ + Proxy │
└─────────┘      └────────┘      └─────────┘
```

**Configuration:**
- Service mesh (Istio, Linkerd, Consul) handles TLS
- Sidecar proxies inject mTLS automatically
- Connect-plugin-go operates over HTTP (proxied)

**Benefits:**
- Transparent mTLS without code changes
- Certificate rotation handled by mesh
- Observability and traffic management

**Deployment Example (Istio):**

```yaml
apiVersion: security.istio.io/v1beta1
kind: PeerAuthentication
metadata:
  name: plugin-mesh
  namespace: plugins
spec:
  mtls:
    mode: STRICT  # Enforce mTLS
```

### Certificate Management

**For Option 1 (End-to-End TLS):**

Use industry-standard certificate authorities:
- Public CA (Let's Encrypt, DigiCert) for internet-facing endpoints
- Private CA (Vault, cert-manager) for internal communication
- Kubernetes cert-manager for automatic certificate lifecycle

**Best Practices:**
- Use separate certificates per component (not wildcard)
- Rotate certificates regularly (90 days or less)
- Monitor certificate expiration
- Use short-lived certificates (1-7 days with auto-renewal)

**Example with cert-manager (Kubernetes):**

```yaml
apiVersion: cert-manager.io/v1
kind: Certificate
metadata:
  name: plugin-server-tls
  namespace: plugins
spec:
  secretName: plugin-server-tls-secret
  duration: 2160h  # 90 days
  renewBefore: 720h  # Renew 30 days before expiry
  issuerRef:
    name: ca-issuer
    kind: ClusterIssuer
  dnsNames:
    - plugin-server.plugins.svc.cluster.local
```

### Network Security

**Kubernetes Network Policies:**

Restrict plugin-to-plugin communication:

```yaml
apiVersion: networking.k8s.io/v1
kind: NetworkPolicy
metadata:
  name: plugin-isolation
  namespace: plugins
spec:
  podSelector:
    matchLabels:
      role: plugin
  policyTypes:
    - Ingress
    - Egress
  ingress:
    - from:
        - podSelector:
            matchLabels:
              role: host-platform
  egress:
    - to:
        - podSelector:
            matchLabels:
              role: host-platform
```

**Firewall Rules (Docker Compose / VM deployments):**

```bash
# Allow only host → plugin communication
iptables -A INPUT -p tcp --dport 8080 -s 10.0.0.5 -j ACCEPT
iptables -A INPUT -p tcp --dport 8080 -j DROP

# Plugin cannot initiate connections to external networks
iptables -A OUTPUT -p tcp -d 10.0.0.0/24 -j ACCEPT
iptables -A OUTPUT -j DROP
```

## Authentication Options

### Token-Based Authentication (Built-In)

**Runtime Identity Tokens:**

Issued automatically during handshake when plugin provides `self_id`:

```go
// Server-side (automatic)
runtimeID, runtimeToken := handshake.Handshake(req)
// Returns: "cache-plugin-x7k2", "Yj8s7K3mN9pQ2rT5vX8z..."

// Client-side usage
client.ReportHealth(ctx, state, reason, unavailableDeps)
// Token sent automatically in Authorization header
```

**Properties:**
- 256-bit cryptographic random tokens
- Base64-URL encoding (44 characters)
- Constant-time comparison (timing attack resistant)
- No automatic expiration (planned enhancement)

**Capability Bearer Tokens:**

Issued when plugin requests host capability:

```go
// Request capability
grant := broker.RequestCapability(ctx, "logger")
// Returns: grant_id="xyz", token="abc...", endpoint="/capabilities/logger/xyz"

// Use capability
loggerClient := logger.NewLoggerClient(httpClient, hostURL+grant.endpoint)
loggerClient.Log(ctx, &LogRequest{Message: "test"},
    connect.WithHeader("Authorization", "Bearer "+grant.token))
```

**Properties:**
- 256-bit cryptographic random tokens
- Scoped to specific capability type
- Validated on every capability access
- No automatic expiration

### Custom Authentication (Token Validation)

Implement custom token validation:

```go
validateToken := func(token string) (identity string, claims map[string]string, error) {
    // Example: JWT validation
    parsedToken, err := jwt.Parse(token, keyFunc)
    if err != nil {
        return "", nil, err
    }

    claims := parsedToken.Claims.(jwt.MapClaims)
    identity := claims["sub"].(string)

    return identity, map[string]string{
        "role": claims["role"].(string),
        "tenant": claims["tenant"].(string),
    }, nil
}

auth := connectplugin.NewTokenAuth("", validateToken)
server := connectplugin.Serve(&connectplugin.ServeConfig{
    Plugins: plugins,
    Impls:   impls,
    // Auth interceptor configuration planned
})
```

### API Key Authentication

Simplified authentication for plugins:

```go
validateAPIKey := func(apiKey string) (identity string, claims map[string]string, error) {
    // Look up API key in database/cache
    plugin, ok := apiKeyStore.Lookup(apiKey)
    if !ok {
        return "", nil, errors.New("invalid API key")
    }

    return plugin.ID, map[string]string{
        "tier": plugin.Tier,
    }, nil
}

auth := connectplugin.NewAPIKeyAuth("", validateAPIKey)
```

**Header Format:**
```
X-API-Key: abc123def456
```

### mTLS Authentication (Planned)

Mutual TLS authentication is planned for Phase 3:

```go
// Future implementation
mtlsAuth := connectplugin.NewMTLSAuth(clientCert, rootCAs, clientCAs)

// Extract identity from certificate
identity := cert.Subject.CommonName
claims := map[string]string{
    "organization": cert.Subject.Organization[0],
}
```

**Status:** The mTLS interceptor in `auth_mtls.go` is currently a placeholder and should NOT be used in production.

**Tracking:** See design document `design-cflz-mtls-interceptor.md` for implementation plan.

## Common Misconfigurations

### 1. Running Without TLS

**Misconfiguration:**
```go
// INSECURE - Do not use in production
client := connectplugin.NewClient(connectplugin.ClientConfig{
    Endpoint: "http://plugin.example.com:8080",  // Plaintext HTTP
    Plugins:  plugins,
})
```

**Impact:**
- Runtime tokens transmitted in plaintext
- Capability bearer tokens exposed
- Plugin data visible to network observers
- Vulnerable to man-in-the-middle attacks

**Correct Configuration:**
```go
// Secure - Use HTTPS in production
client := connectplugin.NewClient(connectplugin.ClientConfig{
    Endpoint: "https://plugin.example.com",  // TLS encryption
    Plugins:  plugins,
})
```

### 2. Using Default Magic Cookie

**Misconfiguration:**
```go
// Weak - All plugins share same magic cookie
connectplugin.Serve(&connectplugin.ServeConfig{
    // Uses DefaultMagicCookieKey and DefaultMagicCookieValue
    Plugins: plugins,
})
```

**Impact:**
- No security impact (magic cookie is not security)
- BUT: Indicates lack of deployment customization
- May confuse operators about security properties

**Best Practice:**
```go
// Custom magic cookie per deployment
connectplugin.Serve(&connectplugin.ServeConfig{
    MagicCookieKey:   "MYAPP_PLUGIN",
    MagicCookieValue: generateUniqueValue(),  // Different per environment
    Plugins:          plugins,
})
```

**Note:** Even with custom magic cookie, use proper authentication and TLS.

### 3. Trusting Plugin Metadata

**Misconfiguration:**
```go
// UNSAFE - Trusting plugin-provided metadata
plugin := registry.DiscoverService(ctx, "cache")
if plugin.Metadata["trusted"] == "true" {
    allowPrivilegedOperation()  // DANGEROUS
}
```

**Impact:**
- Malicious plugin can set arbitrary metadata
- Authorization bypass
- Privilege escalation

**Correct Approach:**
```go
// Validate against host-controlled data
plugin := registry.DiscoverService(ctx, "cache")
authInfo := GetAuthContext(ctx)  // From authentication interceptor
if authorizer.IsAllowed(authInfo.Identity, "privileged") {
    allowPrivilegedOperation()
}
```

### 4. Missing Token Validation

**Misconfiguration:**
```go
// UNSAFE - Not validating runtime tokens
func (h *MyHandler) Handle(ctx context.Context, req *Request) error {
    // Directly trust X-Plugin-Runtime-ID header
    runtimeID := req.Header.Get("X-Plugin-Runtime-ID")
    plugin := h.plugins[runtimeID]  // DANGEROUS
}
```

**Impact:**
- Any caller can impersonate any plugin
- No authentication
- Complete authorization bypass

**Correct Approach:**
```go
// Validate token before trusting identity
func (h *MyHandler) Handle(ctx context.Context, req *Request) error {
    runtimeID := req.Header.Get("X-Plugin-Runtime-ID")
    token := req.Header.Get("Authorization")

    if !h.handshakeServer.ValidateToken(runtimeID, token) {
        return connect.NewError(connect.CodeUnauthenticated,
            fmt.Errorf("invalid runtime token"))
    }

    plugin := h.plugins[runtimeID]
    // Now safe to trust plugin identity
}
```

### 5. Exposing Plugin Endpoints Publicly

**Misconfiguration:**
```yaml
# UNSAFE - Plugin exposed to internet
services:
  cache-plugin:
    image: myapp/cache-plugin
    ports:
      - "8080:8080"  # Exposed to 0.0.0.0
```

**Impact:**
- Direct access to plugin bypasses host security
- Unvalidated runtime tokens
- No service registry authorization
- Exposed internal APIs

**Correct Configuration:**
```yaml
# Secure - Plugin only accessible to host
services:
  cache-plugin:
    image: myapp/cache-plugin
    networks:
      - plugin-network  # Internal network only

  host-platform:
    image: myapp/host
    ports:
      - "443:443"  # Only host exposed externally
    networks:
      - plugin-network

networks:
  plugin-network:
    internal: true  # No external access
```

## Threat Model

### In-Scope Threats

Connect-plugin-go provides protection against:

1. **Timing Attacks on Token Comparison** (MITIGATED)
   - Constant-time comparison for runtime tokens
   - Constant-time comparison for capability grants
   - Fixed in security update (see Recent Security Fixes)

2. **Accidental Misconfiguration** (MITIGATED)
   - Magic cookie detects version mismatches
   - TLS warnings alert operators
   - Health tracking isolates unhealthy plugins

3. **Crypto Random Failures** (MITIGATED)
   - Graceful error handling for crypto/rand
   - No silent generation of weak tokens
   - Fixed in security update (see Recent Security Fixes)

4. **Service Registry Pollution** (PARTIALLY MITIGATED)
   - Runtime identity required for registration
   - Token validation on all operations
   - Limited by basic authorization (enhancement planned)

5. **Capability Grant Theft** (PARTIALLY MITIGATED)
   - Capability tokens scoped to specific types
   - Constant-time token validation
   - No expiration (enhancement planned)

### Out-of-Scope Threats

Connect-plugin-go does NOT protect against:

1. **Malicious Plugins** (Host Responsibility)
   - Framework assumes plugins are trusted
   - Code execution sandbox not provided
   - Operators must vet plugin binaries

2. **Network-Level Attacks** (Requires TLS)
   - Man-in-the-middle attacks if using HTTP
   - Packet sniffing if no encryption
   - TLS must be configured externally

3. **Compromised Host Platform** (Game Over)
   - Host has full access to all plugins
   - Can forge any runtime token
   - Physical/container security is critical

4. **Resource Exhaustion** (Enhancement Planned)
   - No rate limiting on RPC endpoints
   - No request size limits
   - No concurrent connection limits

5. **Authorization Bypass** (Enhancement Planned)
   - Service registration authorization is basic
   - No fine-grained capability access control
   - No audit logging

### Attack Scenarios

#### Scenario 1: Malicious Plugin Impersonation

**Attack:**
1. Attacker captures runtime token via network sniffing (if no TLS)
2. Attacker sends service registration with stolen token
3. Attacker intercepts calls to legitimate service

**Mitigations:**
- Use TLS to prevent token capture (operator responsibility)
- Constant-time token validation prevents timing-based extraction
- Runtime token required for all registry operations

**Residual Risk:** HIGH if TLS not used, LOW if TLS configured

#### Scenario 2: Capability Grant Replay

**Attack:**
1. Attacker captures capability bearer token
2. Attacker reuses token to access capability
3. Attacker accesses logger, secrets, or other capabilities

**Mitigations:**
- TLS prevents token capture in transit
- Tokens validated on every capability access
- Constant-time comparison prevents timing attacks

**Residual Risk:** MEDIUM (tokens don't expire, planned enhancement)

#### Scenario 3: Service Discovery Reconnaissance

**Attack:**
1. Attacker calls DiscoverService without authentication
2. Attacker enumerates available services
3. Attacker targets specific high-value services

**Mitigations:**
- Discovery requires authenticated connection
- Service types are not secret (by design)

**Residual Risk:** LOW (service discovery authorization planned)

#### Scenario 4: Registration Flooding DoS

**Attack:**
1. Attacker registers thousands of fake services
2. Registry grows unbounded
3. Service selection becomes slow
4. Memory exhaustion

**Mitigations:**
- Requires valid runtime token per registration
- Registration tied to authenticated plugin

**Residual Risk:** MEDIUM (rate limiting planned)

## Security Checklist

### Pre-Deployment Verification

Use this checklist before deploying to production:

- [ ] **TLS Configuration**
  - [ ] HTTPS endpoints configured for all components
  - [ ] Valid TLS certificates installed
  - [ ] Certificate expiration monitoring enabled
  - [ ] TLS warnings suppressed only in non-production environments

- [ ] **Authentication**
  - [ ] Runtime tokens validated on all protected operations
  - [ ] Custom token validation implemented if using JWT/OAuth
  - [ ] Token transmission only over HTTPS
  - [ ] Magic cookie customized (optional but recommended)

- [ ] **Network Security**
  - [ ] Plugin endpoints not exposed to public internet
  - [ ] Network policies restrict plugin-to-plugin communication
  - [ ] Firewall rules limit inbound connections
  - [ ] Service mesh configured for mTLS (if using Kubernetes)

- [ ] **Service Registry**
  - [ ] Only trusted plugins have valid runtime tokens
  - [ ] Service registration requires authentication
  - [ ] Discovery endpoints not publicly accessible

- [ ] **Capability Broker**
  - [ ] Capability handlers validate grants
  - [ ] Sensitive capabilities have additional authorization
  - [ ] Capability tokens not logged or exposed

- [ ] **Monitoring**
  - [ ] Authentication failures monitored
  - [ ] TLS warnings logged and alerted
  - [ ] Health check failures trigger alerts
  - [ ] Token generation errors monitored

- [ ] **Operational Security**
  - [ ] Plugin binaries from trusted sources
  - [ ] Container images scanned for vulnerabilities
  - [ ] Secrets not hardcoded in configuration
  - [ ] Environment-specific configurations (dev/staging/prod)

### Runtime Security Monitoring

Monitor these indicators during operation:

**Authentication Metrics:**
- Failed token validations (potential attack indicator)
- Token generation errors (system health issue)
- Unusual authentication patterns

**Network Metrics:**
- TLS handshake failures
- Non-TLS connections (unexpected in production)
- Connection rate per plugin

**Service Registry Metrics:**
- Registration rate spikes (potential DoS)
- Discovery failure rate
- Service health transitions

**Capability Broker Metrics:**
- Invalid grant attempts
- Capability access denials
- Grant request rate per plugin

## Recent Security Fixes

Connect-plugin-go has undergone security review and implemented critical fixes:

### R1.2: Constant-Time Token Comparison (IMPLEMENTED)

**Issue:** Token validation used standard string comparison (`==`), vulnerable to timing attacks.

**Fix:** Implemented `crypto/subtle.ConstantTimeCompare` for all security-sensitive comparisons.

**Affected Components:**
- Runtime token validation (`handshake.go:189`)
- Capability grant validation (`broker.go:164`)

**Code Change:**
```go
// Before (vulnerable)
return expectedToken == token

// After (secure)
if len(expectedToken) != len(token) {
    return false
}
return subtle.ConstantTimeCompare([]byte(expectedToken), []byte(token)) == 1
```

**Impact:** Prevents timing-based token discovery attacks. Attacker can no longer extract tokens character-by-character through timing analysis.

**References:** `design-zzus-constant-time-comparison.md`

### R1.3: Graceful crypto/rand Error Handling (IMPLEMENTED)

**Issue:** Random number generation failures caused panics or generated weak tokens.

**Fix:** All token generation functions now return errors instead of panicking or failing silently.

**Affected Functions:**
- `generateRandomHex()` - returns `(string, error)`
- `generateToken()` - returns `(string, error)`
- `generateGrantID()` - returns `(string, error)`
- `generateRuntimeID()` - returns `(string, error)`

**Code Change:**
```go
// Before (panic on error)
func generateToken() string {
    b := make([]byte, 32)
    rand.Read(b)  // Error ignored or panic
    return base64.URLEncoding.EncodeToString(b)
}

// After (graceful error handling)
func generateToken() (string, error) {
    b := make([]byte, 32)
    if _, err := rand.Read(b); err != nil {
        return "", fmt.Errorf("failed to generate token: %w", err)
    }
    return base64.URLEncoding.EncodeToString(b), nil
}
```

**Impact:** System fails securely when crypto/rand unavailable. No weak or predictable tokens generated.

**References:** `design-tcfi-rand-error-handling.md`

### R1.5: TLS Warnings (IMPLEMENTED)

**Issue:** No indication when operating without TLS encryption.

**Fix:** Implemented runtime warnings when connecting or serving over plaintext HTTP.

**Warning Locations:**
- Client connection to non-TLS endpoint
- Server startup without TLS configuration
- Service discovery returning non-TLS endpoint

**Example Warning:**
```
WARN [connectplugin]: Non-TLS plugin endpoint
  endpoint: http://localhost:8080
  impact: credentials/tokens/plugin-data transmitted in plaintext
  risk: Man-in-the-middle attacks, credential theft
  resolution: Use https:// endpoint or configure TLS
  suppress: CONNECTPLUGIN_DISABLE_TLS_WARNING=1 (testing only)
```

**Suppression:**
```bash
export CONNECTPLUGIN_DISABLE_TLS_WARNING=1  # For testing only
```

**Impact:** Operators are alerted to insecure configurations. Reduces risk of accidental plaintext deployment.

**References:** `design-kbyz-tls-warnings.md`

## Future Enhancements

### Phase 3: mTLS Support

**Goal:** Implement mutual TLS authentication for plugin-to-plugin communication.

**Design:**
- Complete mTLS server interceptor implementation
- Certificate-based identity extraction
- Integration with service mesh (Istio, Linkerd)
- Certificate rotation support

**Status:** Design in progress, tracked in `design-cflz-mtls-interceptor.md`

### Token Expiration

**Goal:** Add time-limited tokens with automatic renewal.

**Design:**
- Runtime tokens expire after configurable duration (default: 1 hour)
- Capability grants expire after shorter duration (default: 5 minutes)
- Token refresh endpoint for renewal
- Automatic cleanup of expired tokens

**Status:** Planned for next major version

### Rate Limiting

**Goal:** Protect against DoS attacks via request flooding.

**Design:**
- Token bucket rate limiter per runtime identity
- Configurable limits for registration, discovery, capability requests
- Circuit breaker integration for failing plugins
- Pluggable rate limiter interface

**Status:** Planned for production readiness phase

### Authorization Policies

**Goal:** Fine-grained access control for service operations.

**Design:**
- Service registration authorization (restrict which plugins can register which services)
- Capability request authorization (restrict which plugins can access which capabilities)
- Audit logging for security events
- Policy configuration via YAML or API

**Status:** Design phase

### Request Signing

**Goal:** Ensure request integrity and prevent replay attacks.

**Design:**
- HMAC-SHA256 signing of all RPC requests
- Timestamp and nonce to prevent replay
- Signature verification on server side
- Configurable signing keys

**Status:** Future consideration

## Additional Resources

- [Getting Started](getting-started/quickstart.md) - Initial setup and examples
- [Deployment Models](getting-started/deployment-models.md) - Architecture patterns
- [Interceptors Guide](guides/interceptors.md) - Authentication and reliability
- [Service Registry](guides/service-registry.md) - Plugin-to-plugin communication
- [Kubernetes Deployment](guides/kubernetes.md) - Production Kubernetes setup

## Reporting Security Issues

If you discover a security vulnerability in connect-plugin-go:

1. **DO NOT** open a public GitHub issue
2. Email security reports to: [security contact - to be added]
3. Include:
   - Description of the vulnerability
   - Steps to reproduce
   - Potential impact assessment
   - Suggested fix (if available)

We will respond within 48 hours with:
- Acknowledgment of the report
- Assessment of severity
- Timeline for fix
- Coordinated disclosure plan

## Security Acknowledgments

We thank the following for their security contributions:

- Internal security review (Phase 3 audit) - January 2026
  - Timing attack identification and mitigation
  - Crypto error handling improvements
  - TLS warning implementation

[Additional acknowledgments to be added]
