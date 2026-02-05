# Security Audit: Authentication System Deep Dive

**Date:** 2026-01-29
**Auditor:** Claude Opus 4.5
**Scope:** connect-plugin-go authentication subsystem
**Files Reviewed:**
- `/Users/masegraye/workspaces/2026-01-29/connect-plugin-review/connect-plugin-go/auth.go`
- `/Users/masegraye/workspaces/2026-01-29/connect-plugin-review/connect-plugin-go/auth_mtls.go`
- `/Users/masegraye/workspaces/2026-01-29/connect-plugin-review/connect-plugin-go/auth_token.go`
- `/Users/masegraye/workspaces/2026-01-29/connect-plugin-review/connect-plugin-go/handshake.go`
- `/Users/masegraye/workspaces/2026-01-29/connect-plugin-review/connect-plugin-go/broker.go`
- `/Users/masegraye/workspaces/2026-01-29/connect-plugin-review/connect-plugin-go/router.go`
- `/Users/masegraye/workspaces/2026-01-29/connect-plugin-review/connect-plugin-go/lifecycle.go`
- `/Users/masegraye/workspaces/2026-01-29/connect-plugin-review/connect-plugin-go/platform.go`

---

## Executive Summary

The connect-plugin-go authentication system implements a multi-layered security model for plugin communication. While the overall architecture is sound, this audit identified **3 HIGH severity**, **4 MEDIUM severity**, and **3 LOW severity** security findings that should be addressed before production deployment.

### Severity Distribution

| Severity | Count | Immediate Action Required |
|----------|-------|--------------------------|
| CRITICAL | 0     | -                        |
| HIGH     | 3     | Yes                      |
| MEDIUM   | 4     | Before production        |
| LOW      | 3     | As resources permit      |

---

## 1. Token Generation Security

### 1.1 Runtime Token Generation

**File:** `broker.go:188-193`

```go
func generateToken() string {
    b := make([]byte, 32)
    rand.Read(b)
    return base64.URLEncoding.EncodeToString(b)
}
```

**Analysis:**

| Criterion | Assessment | Status |
|-----------|------------|--------|
| Entropy Source | `crypto/rand` | PASS |
| Token Length | 32 bytes (256 bits) | PASS |
| Encoding | Base64URL (43 chars) | PASS |
| Error Handling | Ignores `rand.Read` error | FAIL |

**Finding SEC-AUTH-001: Ignored Error from crypto/rand.Read**
- **Severity:** HIGH
- **Location:** `broker.go:191`
- **Description:** The return value from `rand.Read()` is discarded. While `crypto/rand.Read` on modern systems rarely fails, ignoring this error violates secure coding practices. On systems with depleted entropy pools or in degraded states, this could result in partially filled or zero-filled token buffers.
- **Impact:** Potential for weak or predictable tokens in edge cases
- **Remediation:**
```go
func generateToken() string {
    b := make([]byte, 32)
    if _, err := rand.Read(b); err != nil {
        panic(fmt.Sprintf("crypto/rand.Read failed: %v", err))
    }
    return base64.URLEncoding.EncodeToString(b)
}
```

### 1.2 Runtime ID Generation

**File:** `handshake.go:195-214`

```go
func generateRuntimeID(selfID string) string {
    suffix := generateRandomHex(4)
    normalized := strings.ToLower(strings.ReplaceAll(selfID, " ", "-"))
    return fmt.Sprintf("%s-%s", normalized, suffix)
}

func generateRandomHex(length int) string {
    bytes := make([]byte, (length+1)/2)
    if _, err := rand.Read(bytes); err != nil {
        panic(fmt.Sprintf("crypto/rand.Read failed: %v", err))
    }
    return hex.EncodeToString(bytes)[:length]
}
```

**Analysis:**

| Criterion | Assessment | Status |
|-----------|------------|--------|
| Error Handling | Properly panics on failure | PASS |
| Entropy | 4 hex chars = 16 bits | CONCERN |
| Collision Resistance | 65,536 possible values per selfID | CONCERN |

**Finding SEC-AUTH-002: Insufficient Runtime ID Entropy**
- **Severity:** MEDIUM
- **Location:** `handshake.go:197`
- **Description:** The runtime ID suffix is only 4 hex characters (16 bits of entropy). For a given `selfID`, there are only 65,536 possible runtime IDs. In environments with many plugin instances of the same type, collisions become likely.
- **Impact:**
  - Birthday paradox: 50% collision probability at ~256 instances
  - Potential for runtime ID confusion in high-density deployments
- **Remediation:** Increase suffix length to at least 8 characters (32 bits):
```go
suffix := generateRandomHex(8)  // 32 bits, ~4 billion possibilities
```

### 1.3 Grant ID Generation

**File:** `broker.go:182-186`

```go
func generateGrantID() string {
    b := make([]byte, 16)
    rand.Read(b)
    return base64.URLEncoding.EncodeToString(b)
}
```

**Finding SEC-AUTH-003: Ignored Error in Grant ID Generation**
- **Severity:** HIGH
- **Location:** `broker.go:184`
- **Description:** Same issue as SEC-AUTH-001 - `rand.Read` error is ignored
- **Remediation:** Same pattern - check error and panic on failure

---

## 2. Token Validation Security

### 2.1 Timing Attack Vulnerability

**File:** `handshake.go:178-190`

```go
func (h *HandshakeServer) ValidateToken(runtimeID, token string) bool {
    h.mu.RLock()
    defer h.mu.RUnlock()

    expectedToken, ok := h.tokens[runtimeID]
    if !ok {
        return false
    }

    return expectedToken == token  // <-- Timing vulnerability
}
```

**Finding SEC-AUTH-004: Timing Attack Vulnerability in Token Comparison**
- **Severity:** HIGH
- **Location:** `handshake.go:189`
- **Description:** String comparison using `==` is not constant-time. An attacker can measure response times to progressively guess valid tokens character by character.
- **Attack Complexity:** Moderate - requires network timing measurements
- **Impact:** Token bypass through statistical timing analysis
- **Remediation:**
```go
import "crypto/subtle"

func (h *HandshakeServer) ValidateToken(runtimeID, token string) bool {
    h.mu.RLock()
    defer h.mu.RUnlock()

    expectedToken, ok := h.tokens[runtimeID]
    if !ok {
        return false
    }

    return subtle.ConstantTimeCompare([]byte(expectedToken), []byte(token)) == 1
}
```

### 2.2 Additional Timing Vulnerability in Broker

**File:** `broker.go:164`

```go
if grant.token != token {
    http.Error(w, "invalid token", http.StatusUnauthorized)
    return
}
```

**Finding SEC-AUTH-005: Timing Attack in Capability Broker Token Validation**
- **Severity:** MEDIUM
- **Location:** `broker.go:164`
- **Description:** Same timing vulnerability pattern in capability grant validation
- **Remediation:** Use `subtle.ConstantTimeCompare`

---

## 3. mTLS Implementation Analysis

### 3.1 Server Interceptor Incomplete Implementation

**File:** `auth_mtls.go:57-84`

```go
func (m *MTLSAuth) ServerInterceptor() connect.UnaryInterceptorFunc {
    return func(next connect.UnaryFunc) connect.UnaryFunc {
        return func(ctx context.Context, req connect.AnyRequest) (connect.AnyResponse, error) {
            // Placeholder: Assume cert validation happened at TLS layer
            // Just check if there was a client cert presented
            // Real implementation would extract from req.HTTPRequest().TLS

            // Store placeholder auth context
            authCtx := &AuthContext{
                Identity: "mtls-client",  // <-- Hardcoded!
                Provider: "mtls",
                Claims:   map[string]string{"verified": "true"},
            }
            ctx = WithAuthContext(ctx, authCtx)
            return next(ctx, req)
        }
    }
}
```

**Finding SEC-AUTH-006: mTLS ServerInterceptor is Placeholder Only**
- **Severity:** HIGH (if mTLS is relied upon for security)
- **Location:** `auth_mtls.go:57-84`
- **Description:** The mTLS server interceptor does NOT actually validate client certificates. It unconditionally sets a hardcoded identity of "mtls-client" for ALL requests, regardless of whether mTLS was actually used.
- **Impact:**
  - Any request is treated as mTLS-authenticated
  - Identity is not extracted from actual certificates
  - Complete bypass of mTLS authentication intent
- **Remediation:** Implement actual certificate extraction:
```go
func (m *MTLSAuth) ServerInterceptor() connect.UnaryInterceptorFunc {
    return func(next connect.UnaryFunc) connect.UnaryFunc {
        return func(ctx context.Context, req connect.AnyRequest) (connect.AnyResponse, error) {
            // Get underlying HTTP request
            httpReq, ok := connect.HTTPRequestFromContext(ctx)
            if !ok || httpReq.TLS == nil || len(httpReq.TLS.PeerCertificates) == 0 {
                return nil, connect.NewError(connect.CodeUnauthenticated,
                    fmt.Errorf("client certificate required"))
            }

            clientCert := httpReq.TLS.PeerCertificates[0]
            identity, claims := m.ExtractIdentity(clientCert)

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

### 3.2 Default Identity Extractor Array Bounds

**File:** `auth_mtls.go:36-39`

```go
ExtractIdentity: func(cert *x509.Certificate) (string, map[string]string) {
    return cert.Subject.CommonName, map[string]string{
        "organization": cert.Subject.Organization[0],  // <-- Potential panic
    }
}
```

**Finding SEC-AUTH-007: Potential Panic in Default Identity Extractor**
- **Severity:** MEDIUM
- **Location:** `auth_mtls.go:38`
- **Description:** The default `ExtractIdentity` function accesses `Organization[0]` without checking slice bounds. Certificates without an Organization field will cause a panic.
- **Impact:** Denial of service through specially crafted certificates
- **Remediation:**
```go
ExtractIdentity: func(cert *x509.Certificate) (string, map[string]string) {
    claims := make(map[string]string)
    if len(cert.Subject.Organization) > 0 {
        claims["organization"] = cert.Subject.Organization[0]
    }
    return cert.Subject.CommonName, claims
}
```

### 3.3 TLS Version Configuration

**File:** `auth_mtls.go:93-97` and `auth_mtls.go:114-117`

```go
tlsConfig := &tls.Config{
    // ...
    MinVersion:   tls.VersionTLS12,
}
```

**Analysis:**
- TLS 1.2 minimum is acceptable
- TLS 1.3 is not enforced but available
- No explicit cipher suite restrictions

**Finding SEC-AUTH-008: No Explicit Cipher Suite Configuration**
- **Severity:** LOW
- **Location:** `auth_mtls.go:93-97`
- **Description:** The TLS configuration does not explicitly restrict cipher suites. While Go's defaults are reasonably secure, production deployments may require explicit control over cipher selection.
- **Remediation:** Consider adding explicit cipher suite configuration for compliance requirements.

---

## 4. Authentication Context Propagation

### 4.1 Context Key Implementation

**File:** `auth.go:35-47`

```go
type authContextKey struct{}

func WithAuthContext(ctx context.Context, auth *AuthContext) context.Context {
    return context.WithValue(ctx, authContextKey{}, auth)
}

func GetAuthContext(ctx context.Context) *AuthContext {
    auth, _ := ctx.Value(authContextKey{}).(*AuthContext)
    return auth
}
```

**Analysis:**

| Criterion | Assessment | Status |
|-----------|------------|--------|
| Key Uniqueness | Private struct type | PASS |
| Collision Prevention | Package-scoped, unexported | PASS |
| Nil Safety | Returns nil for missing context | PASS |
| Type Assertion | Uses comma-ok pattern | PASS |

**No findings** - Context propagation is implemented securely.

### 4.2 AuthContext Mutability

**File:** `auth.go:22-33`

```go
type AuthContext struct {
    Identity string
    Claims   map[string]string
    Provider string
}
```

**Finding SEC-AUTH-009: AuthContext Claims Map is Mutable**
- **Severity:** LOW
- **Location:** `auth.go:28`
- **Description:** The `Claims` map can be modified after authentication, potentially allowing handlers to modify authenticated claims.
- **Impact:** Low - requires malicious code within the same process
- **Remediation:** Consider making AuthContext immutable or documenting the expectation that it should not be modified.

---

## 5. Multi-Provider Authentication Chain

### 5.1 Client Composition

**File:** `auth.go:64-80`

```go
func ComposeAuthClient(providers ...AuthProvider) connect.UnaryInterceptorFunc {
    if len(providers) == 0 {
        return func(next connect.UnaryFunc) connect.UnaryFunc {
            return next
        }
    }

    return func(next connect.UnaryFunc) connect.UnaryFunc {
        for i := len(providers) - 1; i >= 0; i-- {
            next = providers[i].ClientInterceptor()(next)
        }
        return next
    }
}
```

**Analysis:**
- Providers applied in reverse order (LIFO semantics)
- All providers contribute headers (additive)
- Clean no-op for empty provider list

**No findings** - Client composition is well-implemented.

### 5.2 Server Composition

**File:** `auth.go:82-121`

```go
func ComposeAuthServer(providers ...AuthProvider) connect.UnaryInterceptorFunc {
    // ...
    return func(next connect.UnaryFunc) connect.UnaryFunc {
        return func(ctx context.Context, req connect.AnyRequest) (connect.AnyResponse, error) {
            var lastErr error

            for _, provider := range providers {
                wrapped := provider.ServerInterceptor()(func(ctx context.Context, req connect.AnyRequest) (connect.AnyResponse, error) {
                    if GetAuthContext(ctx) != nil {
                        return next(ctx, req)
                    }
                    return nil, connect.NewError(connect.CodeUnauthenticated, fmt.Errorf("no auth context"))
                })

                resp, err := wrapped(ctx, req)
                if err == nil {
                    return resp, nil
                }
                lastErr = err
            }
            // ...
        }
    }
}
```

**Finding SEC-AUTH-010: First-Success Authentication May Leak Information**
- **Severity:** LOW
- **Location:** `auth.go:107-111`
- **Description:** The composed server authenticator tries providers sequentially and returns on first success. The error from failed providers (`lastErr`) reveals which provider was attempted last, potentially leaking information about the authentication chain.
- **Impact:** Minor information disclosure about authentication configuration
- **Remediation:** Consider using a generic error message that doesn't reveal internal provider order.

---

## 6. Magic Cookie Security Model

### 6.1 Magic Cookie Implementation

**File:** `handshake.go:17-23`

```go
const (
    DefaultMagicCookieKey   = "CONNECT_PLUGIN"
    DefaultMagicCookieValue = "d3f40b3c2e1a5f8b9c4d7e6a1b2c3d4e"
)
```

**File:** `handshake.go:47-59`

```go
if req.Msg.MagicCookieKey != expectedKey || req.Msg.MagicCookieValue != expectedValue {
    return nil, connect.NewError(
        connect.CodeInvalidArgument,
        fmt.Errorf("invalid magic cookie - this may not be a connect-plugin server"),
    )
}
```

**Analysis:**

The magic cookie serves as a "plugin identity verification" mechanism:

1. **Purpose:** Ensures client is actually a connect-plugin client (not arbitrary HTTP client)
2. **Threat Model:** Protects against accidental connections, not malicious actors
3. **Security Level:** Defense-in-depth, not primary authentication

| Criterion | Assessment | Status |
|-----------|------------|--------|
| Default Value | Hardcoded hex string | CONCERN |
| Comparison | Direct string equality | WARNING |
| Configurability | Via ServeConfig | PASS |

**Finding SEC-AUTH-011: Magic Cookie Uses Non-Constant-Time Comparison**
- **Severity:** MEDIUM
- **Location:** `handshake.go:55`
- **Description:** Magic cookie comparison uses `!=` operator, vulnerable to timing attacks
- **Context:** Magic cookie is defense-in-depth; primary authentication is via runtime tokens
- **Remediation:** Use constant-time comparison for both key and value:
```go
if subtle.ConstantTimeCompare([]byte(req.Msg.MagicCookieKey), []byte(expectedKey)) != 1 ||
   subtle.ConstantTimeCompare([]byte(req.Msg.MagicCookieValue), []byte(expectedValue)) != 1 {
    // ...
}
```

### 6.2 Magic Cookie Threat Model Assessment

The magic cookie is **appropriate for its intended purpose**:

| Threat | Protected | Notes |
|--------|-----------|-------|
| Accidental connections | Yes | Wrong client type rejected |
| Port scanning | Partial | Different error message |
| Malicious plugins | No | Magic cookie is not secret |
| Man-in-the-middle | No | Requires TLS separately |

**Recommendation:** Document that magic cookie is NOT a security boundary and should not be treated as a secret.

---

## 7. Runtime Token Lifecycle

### 7.1 Token Storage

**File:** `handshake.go:29-32`

```go
type HandshakeServer struct {
    cfg *ServeConfig
    mu     sync.RWMutex
    tokens map[string]string // runtime_id -> token
}
```

**Analysis:**

| Criterion | Assessment | Status |
|-----------|------------|--------|
| Thread Safety | RWMutex protected | PASS |
| Storage Type | In-memory map | CONCERN |
| Token Revocation | Not implemented | CONCERN |
| Token Expiration | Not implemented | CONCERN |

**Finding SEC-AUTH-012: No Token Expiration or Revocation**
- **Severity:** MEDIUM
- **Location:** `handshake.go:29-32`
- **Description:** Tokens are stored indefinitely in memory with no expiration or explicit revocation mechanism. If a plugin is compromised, its token remains valid until server restart.
- **Impact:**
  - Compromised tokens cannot be invalidated
  - Memory grows unboundedly with plugin registrations
  - Server restart required for security remediation
- **Remediation:** Implement token lifecycle management:
  1. Add expiration timestamps to tokens
  2. Implement explicit token revocation API
  3. Add periodic token cleanup for removed plugins

### 7.2 Token Issuance Flow

**File:** `handshake.go:83-94`

```go
if req.Msg.SelfId != "" {
    runtimeID = generateRuntimeID(req.Msg.SelfId)
    runtimeToken = generateToken()

    h.mu.Lock()
    h.tokens[runtimeID] = runtimeToken
    h.mu.Unlock()
}
```

**Analysis:**
- Token only generated if `SelfId` provided
- Atomic storage under mutex
- No rate limiting on token issuance

**Finding SEC-AUTH-013: No Rate Limiting on Token Issuance**
- **Severity:** LOW
- **Location:** `handshake.go:83-94`
- **Description:** Handshake endpoint has no rate limiting. An attacker could repeatedly request handshakes to exhaust server memory with token storage.
- **Impact:** Potential denial of service
- **Remediation:** Implement rate limiting or connection limits

### 7.3 Platform Token Management

**File:** `platform.go:125-132`

```go
runtimeID := generateRuntimeID(selfID)
runtimeToken := generateToken()

if err := infoClient.SetRuntimeIdentity(ctx, runtimeID, runtimeToken, ""); err != nil {
    return fmt.Errorf("failed to set runtime identity: %w", err)
}
```

**Analysis:**
- Tokens generated per-plugin during `AddPlugin`
- Sent to plugin via `SetRuntimeIdentity` RPC
- Stored in `PluginInstance.Token`

**Observation:** Token is transmitted to plugin - ensure channel is encrypted (TLS).

---

## 8. Summary of Findings

### High Severity (Immediate Action Required)

| ID | Title | Location | Fix Effort |
|----|-------|----------|------------|
| SEC-AUTH-001 | Ignored error from crypto/rand.Read (token) | broker.go:191 | Low |
| SEC-AUTH-003 | Ignored error from crypto/rand.Read (grant) | broker.go:184 | Low |
| SEC-AUTH-004 | Timing attack in token validation | handshake.go:189 | Low |
| SEC-AUTH-006 | mTLS interceptor is placeholder | auth_mtls.go:57-84 | Medium |

### Medium Severity (Before Production)

| ID | Title | Location | Fix Effort |
|----|-------|----------|------------|
| SEC-AUTH-002 | Insufficient runtime ID entropy | handshake.go:197 | Low |
| SEC-AUTH-005 | Timing attack in broker validation | broker.go:164 | Low |
| SEC-AUTH-007 | Potential panic in identity extractor | auth_mtls.go:38 | Low |
| SEC-AUTH-011 | Magic cookie timing vulnerability | handshake.go:55 | Low |
| SEC-AUTH-012 | No token expiration/revocation | handshake.go:29-32 | Medium |

### Low Severity (As Resources Permit)

| ID | Title | Location | Fix Effort |
|----|-------|----------|------------|
| SEC-AUTH-008 | No explicit cipher suite config | auth_mtls.go:93-97 | Low |
| SEC-AUTH-009 | AuthContext claims map mutable | auth.go:28 | Low |
| SEC-AUTH-010 | Auth chain info leak | auth.go:107-111 | Low |
| SEC-AUTH-013 | No rate limiting on handshake | handshake.go:83-94 | Medium |

---

## 9. Remediation Priority

### Immediate (Before Any Deployment)

1. **Fix crypto/rand error handling** (SEC-AUTH-001, SEC-AUTH-003)
2. **Implement constant-time token comparison** (SEC-AUTH-004, SEC-AUTH-005)
3. **Complete mTLS implementation OR remove/document as non-functional** (SEC-AUTH-006)

### Pre-Production

4. **Increase runtime ID entropy** (SEC-AUTH-002)
5. **Fix Organization array bounds check** (SEC-AUTH-007)
6. **Add constant-time magic cookie comparison** (SEC-AUTH-011)
7. **Design token lifecycle management** (SEC-AUTH-012)

### Production Hardening

8. **Configure explicit TLS cipher suites** (SEC-AUTH-008)
9. **Consider AuthContext immutability** (SEC-AUTH-009)
10. **Standardize auth error messages** (SEC-AUTH-010)
11. **Implement handshake rate limiting** (SEC-AUTH-013)

---

## 10. Positive Security Observations

The codebase demonstrates several security best practices:

1. **Proper use of crypto/rand** - Cryptographically secure random source (when errors are checked)
2. **Adequate token entropy** - 256 bits for runtime tokens
3. **Proper context key typing** - Prevents accidental collision
4. **TLS 1.2 minimum** - Rejects outdated protocol versions
5. **Clean separation of concerns** - Auth providers are composable
6. **Proper mutex usage** - Thread-safe token storage
7. **Defense in depth** - Multiple authentication layers available

---

## Appendix A: Code Reference Index

| File | Lines | Component |
|------|-------|-----------|
| auth.go | 1-122 | AuthProvider interface, context, composition |
| auth_mtls.go | 1-119 | mTLS authentication provider |
| auth_token.go | 1-115 | Token/API key authentication provider |
| handshake.go | 1-214 | Handshake server, token generation |
| broker.go | 1-193 | Capability broker, grant management |
| router.go | 1-186 | Service router, token validation |
| lifecycle.go | 1-148 | Health reporting, plugin control |
| platform.go | 1-378 | Plugin lifecycle, token distribution |

## Appendix B: Related Documentation

- RFC 6749 - OAuth 2.0 Bearer Token Usage
- RFC 8446 - TLS 1.3 Specification
- OWASP Authentication Cheat Sheet
- Go crypto/subtle package documentation
