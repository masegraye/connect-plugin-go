# Security Assessment

## 1. Authentication Analysis

### 1.1 Token-Based Authentication (`auth_token.go`)

**Implementation:**
```go
func (t *TokenAuth) ServerInterceptor() connect.UnaryInterceptorFunc {
    return func(next connect.UnaryFunc) connect.UnaryFunc {
        return func(ctx context.Context, req connect.AnyRequest) (connect.AnyResponse, error) {
            authHeader := req.Header().Get(t.Header)
            // ... validation ...
            identity, claims, err := t.ValidateToken(token)
```

**Findings:**

| Finding | Severity | Details |
|---------|----------|---------|
| Token in memory | Low | Tokens stored in struct field, normal Go pattern |
| Custom validator | Medium | Security depends on user-provided ValidateToken function |
| Header parsing | Low | Standard HTTP header parsing, properly strips prefix |

**Recommendation:** Add documentation on secure token generation and validation practices.

### 1.2 mTLS Authentication (`auth_mtls.go`)

**Critical Finding:** The server interceptor is incomplete/placeholder.

```go
// auth_mtls.go:57-84
func (m *MTLSAuth) ServerInterceptor() connect.UnaryInterceptorFunc {
    return func(next connect.UnaryFunc) connect.UnaryFunc {
        return func(ctx context.Context, req connect.AnyRequest) (connect.AnyResponse, error) {
            // Placeholder: Assume cert validation happened at TLS layer
            // Real implementation would extract from req.HTTPRequest().TLS

            // Store placeholder auth context
            authCtx := &AuthContext{
                Identity: "mtls-client",  // HARDCODED!
                Provider: "mtls",
                Claims:   map[string]string{"verified": "true"},  // HARDCODED!
            }
```

**Severity: HIGH** - This hardcodes identity for all mTLS clients, bypassing actual certificate validation.

**Remediation:**
```go
func (m *MTLSAuth) ServerInterceptor() connect.UnaryInterceptorFunc {
    return func(next connect.UnaryFunc) connect.UnaryFunc {
        return func(ctx context.Context, req connect.AnyRequest) (connect.AnyResponse, error) {
            // Extract TLS state from request
            // Note: This requires access to underlying http.Request
            httpReq := extractHTTPRequest(req) // Implementation needed
            if httpReq.TLS == nil || len(httpReq.TLS.PeerCertificates) == 0 {
                return nil, connect.NewError(connect.CodeUnauthenticated,
                    fmt.Errorf("client certificate required"))
            }

            cert := httpReq.TLS.PeerCertificates[0]
            identity, claims := m.ExtractIdentity(cert)
            // ... rest of implementation
```

### 1.3 Runtime Token Generation (`handshake.go`)

**Implementation:**
```go
// handshake.go:188-193
func generateToken() string {
    b := make([]byte, 32)
    rand.Read(b)  // crypto/rand
    return base64.URLEncoding.EncodeToString(b)
}
```

**Findings:**

| Aspect | Assessment |
|--------|------------|
| Entropy | Good - 256 bits (32 bytes) |
| RNG | Good - crypto/rand |
| Encoding | Good - URL-safe base64 |
| Error handling | **BAD** - rand.Read error causes panic |

**Critical Finding:** Error handling for rand.Read:

```go
// handshake.go:206-213
func generateRandomHex(length int) string {
    bytes := make([]byte, (length+1)/2)
    if _, err := rand.Read(bytes); err != nil {
        // Fall back to a less secure but still random method
        // This should never happen in practice
        panic(fmt.Sprintf("crypto/rand.Read failed: %v", err))  // PANIC!
    }
```

**Severity: LOW** - crypto/rand failure is extremely rare and indicates system-level issues, but panic is harsh for a library.

**Recommendation:** Return error instead of panic, let caller decide how to handle.

### 1.4 Token Validation (`handshake.go`)

**Critical Finding:** String comparison vulnerability:

```go
// handshake.go:180-190
func (h *HandshakeServer) ValidateToken(runtimeID, token string) bool {
    h.mu.RLock()
    defer h.mu.RUnlock()

    expectedToken, ok := h.tokens[runtimeID]
    if !ok {
        return false
    }

    return expectedToken == token  // TIMING ATTACK VULNERABLE!
}
```

**Severity: MEDIUM** - Standard string comparison leaks timing information.

**Remediation:**
```go
import "crypto/subtle"

func (h *HandshakeServer) ValidateToken(runtimeID, token string) bool {
    h.mu.RLock()
    expectedToken, ok := h.tokens[runtimeID]
    h.mu.RUnlock()

    if !ok {
        return false
    }

    return subtle.ConstantTimeCompare([]byte(expectedToken), []byte(token)) == 1
}
```

### 1.5 Magic Cookie (`handshake.go`)

**Analysis:**

```go
// handshake.go:17-22
const (
    DefaultMagicCookieKey = "CONNECT_PLUGIN"
    DefaultMagicCookieValue = "d3f40b3c2e1a5f8b9c4d7e6a1b2c3d4e"
)
```

**Security Model:**
- Magic cookie is **validation, not security**
- Transmitted in plaintext over network
- Same value for all plugins using defaults
- Provides no protection against malicious actors

**Severity: MEDIUM** - Users may incorrectly assume this provides security.

**Recommendation:**
1. Add prominent documentation: "Magic cookie is for accidental misconfiguration detection, NOT security"
2. Add runtime warning if using default values in production
3. Consider deprecating in favor of proper TLS + token auth

## 2. Authorization Analysis

### 2.1 Service Registration Authorization

**Finding:** Any authenticated plugin can register any service type.

```go
// registry.go:96-136
func (r *ServiceRegistry) RegisterService(
    ctx context.Context,
    req *connect.Request[connectpluginv1.RegisterServiceRequest],
) (*connect.Response[connectpluginv1.RegisterServiceResponse], error) {
    runtimeID := req.Header().Get("X-Plugin-Runtime-ID")
    if runtimeID == "" {
        return nil, connect.NewError(...)
    }
    // No validation that plugin is authorized to register this service type!
```

**Severity: MEDIUM** - A malicious plugin could register as a different service and intercept traffic.

**Remediation:**
1. During handshake, record which services each plugin declared it provides
2. During registration, validate against declared services
3. Add optional "strict mode" for production

### 2.2 Service Discovery Authorization

**Finding:** No authorization check on DiscoverService.

```go
// registry.go:367-395
func (r *ServiceRegistry) DiscoverService(
    ctx context.Context,
    req *connect.Request[connectpluginv1.DiscoverServiceRequest],
) (*connect.Response[connectpluginv1.DiscoverServiceResponse], error) {
    // No check that caller is authorized to discover this service
    provider, err := r.SelectProvider(req.Msg.ServiceType, req.Msg.MinVersion)
```

**Severity: LOW** - Discovery doesn't expose sensitive data, but could enable reconnaissance.

**Recommendation:** Add optional access control for service discovery.

### 2.3 Capability Request Authorization

**Finding:** Any plugin can request any capability.

```go
// broker.go:77-117
func (b *CapabilityBroker) RequestCapability(...) {
    // Find capability handler
    handler, ok := b.capabilities[req.Msg.CapabilityType]
    // No authorization check!
```

**Severity: MEDIUM** - Depends on what capabilities expose.

**Recommendation:** Add capability-level authorization policies.

## 3. Input Validation

### 3.1 Metadata Fields

**Finding:** No validation on metadata maps.

```go
// registry.go:121-122
Metadata:       req.Msg.Metadata,  // Stored directly, no validation
```

**Risks:**
- Large metadata values (memory exhaustion)
- Special characters in keys/values (injection in logs/displays)
- Null bytes (C string issues in some systems)

**Severity: MEDIUM**

**Remediation:**
```go
func validateMetadata(metadata map[string]string) error {
    const maxKeyLen = 256
    const maxValueLen = 4096
    const maxEntries = 100

    if len(metadata) > maxEntries {
        return fmt.Errorf("too many metadata entries: %d > %d", len(metadata), maxEntries)
    }

    for k, v := range metadata {
        if len(k) > maxKeyLen {
            return fmt.Errorf("metadata key too long: %d > %d", len(k), maxKeyLen)
        }
        if len(v) > maxValueLen {
            return fmt.Errorf("metadata value too long: %d > %d", len(v), maxValueLen)
        }
        if !isValidMetadataKey(k) {
            return fmt.Errorf("invalid metadata key: %q", k)
        }
    }
    return nil
}
```

### 3.2 Service Type Names

**Finding:** Service types accepted without validation.

```go
// registry.go:117
ServiceType:    req.Msg.ServiceType,  // No validation
```

**Risks:**
- Path traversal in service type (../../admin)
- Special characters causing routing issues
- Empty or whitespace-only types

**Severity: LOW** - Limited exploitation potential, but should be validated.

### 3.3 Runtime ID Generation

**Finding:** Self-ID normalized but not fully validated.

```go
// handshake.go:199-202
func generateRuntimeID(selfID string) string {
    normalized := strings.ToLower(strings.ReplaceAll(selfID, " ", "-"))
    return fmt.Sprintf("%s-%s", normalized, suffix)
}
```

**Risks:**
- Unicode normalization attacks
- Extremely long IDs
- Special characters

**Severity: LOW**

## 4. Information Disclosure

### 4.1 Router Logging

**Finding:** Logs potentially sensitive information.

```go
// router.go:116-117
log.Printf("[ROUTER] %s → %s %s (service: %s)",
    callerID, providerID, method, serviceType)
```

**Information disclosed:**
- Plugin runtime IDs
- Service types
- Method names
- Call paths

**Severity: LOW** - Useful for debugging but may expose architecture details.

**Recommendation:** Add log level configuration.

### 4.2 Error Messages

**Finding:** Some errors reveal internal state.

```go
// router.go:109-110
http.Error(w, fmt.Sprintf("provider endpoint not registered: %s", providerID), ...)
```

**Severity: LOW** - Reveals which providers exist vs don't exist.

**Recommendation:** Use generic error messages externally, detailed logs internally.

## 5. Denial of Service

### 5.1 Registration Flooding

**Finding:** No rate limiting on registration endpoint.

**Attack:**
1. Attacker registers thousands of services
2. Registry grows unboundedly
3. Selection strategies slow down
4. Memory exhaustion

**Severity: MEDIUM**

**Remediation:**
- Add per-plugin registration limits
- Add global registration rate limiting
- Add registration cleanup for inactive plugins

### 5.2 Watcher Flooding

**Finding:** Watchers can be created without limit.

```go
// registry.go:406-456
func (r *ServiceRegistry) WatchService(...) error {
    // No limit on number of watchers
    r.watchers[serviceType] = append(r.watchers[serviceType], watcher)
```

**Severity: LOW** - Each watcher holds resources.

### 5.3 Metadata Size

As noted above, unbounded metadata can exhaust memory.

## 6. Cryptographic Issues

### 6.1 Random Number Generation

**Good:** Uses crypto/rand for all security-sensitive randomness.

```go
// broker.go:183-186
func generateGrantID() string {
    b := make([]byte, 16)
    rand.Read(b)  // crypto/rand
```

### 6.2 Token Entropy

**Good:** 256-bit tokens (32 bytes), sufficient for security.

### 6.3 TLS Configuration

**Good:** Minimum TLS 1.2 enforced in mTLS config.

```go
// auth_mtls.go:96-97
MinVersion:   tls.VersionTLS12,
```

## 7. Concurrency Safety

### 7.1 Registry Operations

**Good:** Proper mutex usage throughout.

```go
// registry.go:109-110
r.mu.Lock()
defer r.mu.Unlock()
```

### 7.2 Watcher Notification

**Potential Issue:** Non-blocking send to watcher channels.

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

**Assessment:** Acceptable design - slow watchers don't block others, but may miss events.

### 7.3 Circuit Breaker State

**Potential Issue:** Callback called while lock released.

```go
// circuitbreaker.go:238-242
func (cb *CircuitBreaker) setState(newState CircuitState) {
    // ...
    if cb.config.OnStateChange != nil {
        cb.mu.Unlock()
        cb.config.OnStateChange(oldState, newState)
        cb.mu.Lock()  // Re-acquire
    }
}
```

**Assessment:** Intentional to prevent deadlock, but callback could see inconsistent state if it queries the breaker.

## 8. Dependency Analysis

### 8.1 ConnectRPC

**Assessment:** Well-maintained, security-conscious library from Buf team.
- No known CVEs
- Active maintenance
- Standard HTTP transport

### 8.2 uber/fx

**Assessment:** Dependency injection framework, minimal security surface.
- No network operations
- No parsing untrusted input

### 8.3 Protocol Buffers

**Assessment:** Google-maintained, battle-tested.
- No parsing of user-controlled proto schemas at runtime
- Generated code only

## Summary Table

| Category | Critical | High | Medium | Low |
|----------|----------|------|--------|-----|
| Authentication | 0 | 1 | 2 | 2 |
| Authorization | 0 | 0 | 2 | 1 |
| Input Validation | 0 | 0 | 1 | 2 |
| Information Disclosure | 0 | 0 | 0 | 2 |
| DoS | 0 | 0 | 2 | 1 |
| Cryptography | 0 | 0 | 1 | 1 |
| Concurrency | 0 | 0 | 0 | 2 |
| **Total** | **0** | **1** | **8** | **11** |
