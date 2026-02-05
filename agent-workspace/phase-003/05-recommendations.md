# Recommendations

## Priority 1: Must Fix Before Open Source Release

These issues could cause significant security problems or user confusion.

### R1.1: Fix mTLS Server Interceptor

**Issue:** `auth_mtls.go:57-84` hardcodes identity for all mTLS clients.

**Current Code:**
```go
authCtx := &AuthContext{
    Identity: "mtls-client",  // HARDCODED
    Provider: "mtls",
    Claims:   map[string]string{"verified": "true"},
}
```

**Recommended Fix:**
```go
func (m *MTLSAuth) ServerInterceptor() connect.UnaryInterceptorFunc {
    return func(next connect.UnaryFunc) connect.UnaryFunc {
        return func(ctx context.Context, req connect.AnyRequest) (connect.AnyResponse, error) {
            // Note: This requires access to the underlying HTTP request.
            // Connect-go provides this through type assertion.
            httpReq, ok := extractHTTPRequest(req)
            if !ok {
                return nil, connect.NewError(connect.CodeInternal,
                    fmt.Errorf("cannot extract HTTP request for mTLS validation"))
            }

            if httpReq.TLS == nil {
                return nil, connect.NewError(connect.CodeUnauthenticated,
                    fmt.Errorf("TLS connection required for mTLS authentication"))
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

// Helper to extract HTTP request from Connect request
func extractHTTPRequest(req connect.AnyRequest) (*http.Request, bool) {
    // Connect-go doesn't directly expose http.Request
    // This may require a custom solution or interceptor pattern
    // Document this limitation if extraction isn't possible
    return nil, false
}
```

**Alternative:** If HTTP request extraction isn't feasible, document that mTLS authentication happens at the transport layer (http.Server TLS config) and the interceptor is for context propagation only.

**Effort:** Medium
**Files:** `auth_mtls.go`

---

### R1.2: Use Constant-Time Token Comparison

**Issue:** `handshake.go:189` uses `==` for token comparison.

**Current Code:**
```go
return expectedToken == token
```

**Recommended Fix:**
```go
import "crypto/subtle"

func (h *HandshakeServer) ValidateToken(runtimeID, token string) bool {
    h.mu.RLock()
    expectedToken, ok := h.tokens[runtimeID]
    h.mu.RUnlock()

    if !ok {
        return false
    }

    // Use constant-time comparison to prevent timing attacks
    return subtle.ConstantTimeCompare([]byte(expectedToken), []byte(token)) == 1
}
```

**Also fix in:** `router.go:82` (if applicable), `broker.go:164`

**Effort:** Low
**Files:** `handshake.go`, `router.go`, `broker.go`

---

### R1.3: Handle rand.Read Errors Gracefully

**Issue:** `handshake.go:208-213` panics on crypto/rand failure.

**Current Code:**
```go
if _, err := rand.Read(bytes); err != nil {
    panic(fmt.Sprintf("crypto/rand.Read failed: %v", err))
}
```

**Recommended Fix:**
```go
func generateRandomHex(length int) (string, error) {
    bytes := make([]byte, (length+1)/2)
    if _, err := rand.Read(bytes); err != nil {
        return "", fmt.Errorf("crypto/rand.Read failed: %w", err)
    }
    return hex.EncodeToString(bytes)[:length], nil
}

func generateRuntimeID(selfID string) (string, error) {
    suffix, err := generateRandomHex(4)
    if err != nil {
        return "", err
    }
    normalized := strings.ToLower(strings.ReplaceAll(selfID, " ", "-"))
    return fmt.Sprintf("%s-%s", normalized, suffix), nil
}
```

**Propagate errors through:**
- `Handshake()` RPC handler
- `generateToken()`
- `generateGrantID()`

**Effort:** Low
**Files:** `handshake.go`, `broker.go`

---

### R1.4: Add Security Documentation

**Issue:** Users need clear guidance on secure deployment.

**Create:** `docs/security.md`

**Contents:**
1. **Security Model**
   - Trust boundaries (host, plugin, network)
   - What magic cookie does and doesn't protect
   - Authentication options and recommendations

2. **Production Deployment**
   - TLS requirement (MUST for production)
   - Certificate management
   - Token rotation

3. **Common Misconfigurations**
   - Running without TLS
   - Using default magic cookie
   - Trusting plugin-provided metadata

4. **Threat Model**
   - In-scope threats
   - Out-of-scope threats
   - Comparison with go-plugin

**Effort:** Medium
**Files:** New `docs/security.md`

---

### R1.5: Add TLS Warning for Non-TLS Connections

**Issue:** No warning when running without TLS.

**Recommended Fix in `client.go:147`:**
```go
func (c *Client) Connect(ctx context.Context) error {
    // ... existing code ...

    // Warn if not using TLS
    if c.cfg.Endpoint != "" && !strings.HasPrefix(c.cfg.Endpoint, "https://") {
        log.Printf("[WARN] connect-plugin: connecting to %s without TLS. "+
            "This is insecure and should not be used in production.", c.cfg.Endpoint)
    }

    // Create HTTP client
    c.httpClient = &http.Client{}
    // ...
}
```

**Also add to `server.go`:**
```go
func Serve(cfg *ServeConfig) error {
    // ... existing code ...

    // Warn about non-TLS in server
    if !strings.HasPrefix(cfg.Addr, "https") {
        log.Printf("[WARN] connect-plugin: serving on %s without TLS. "+
            "Configure TLS for production use.", cfg.Addr)
    }
```

**Effort:** Low
**Files:** `client.go`, `server.go`

---

## Priority 2: Should Fix Before Production Use

These issues pose risks in production environments.

### R2.1: Add Rate Limiting

**Issue:** No rate limiting on public endpoints.

**Recommended Approach:**
1. Add middleware interface for rate limiting
2. Provide default implementation
3. Make it pluggable for custom implementations

```go
// ratelimit.go (new file)
type RateLimiter interface {
    Allow(ctx context.Context, key string) bool
}

type TokenBucketLimiter struct {
    rate       float64
    bucketSize int
    buckets    map[string]*bucket
    mu         sync.Mutex
}

func NewTokenBucketLimiter(rate float64, bucketSize int) *TokenBucketLimiter {
    return &TokenBucketLimiter{
        rate:       rate,
        bucketSize: bucketSize,
        buckets:    make(map[string]*bucket),
    }
}

func (l *TokenBucketLimiter) Allow(ctx context.Context, key string) bool {
    // Implementation
}

// Middleware
func RateLimitInterceptor(limiter RateLimiter) connect.UnaryInterceptorFunc {
    return func(next connect.UnaryFunc) connect.UnaryFunc {
        return func(ctx context.Context, req connect.AnyRequest) (connect.AnyResponse, error) {
            key := extractRateLimitKey(req) // IP, runtime_id, etc.
            if !limiter.Allow(ctx, key) {
                return nil, connect.NewError(connect.CodeResourceExhausted,
                    fmt.Errorf("rate limit exceeded"))
            }
            return next(ctx, req)
        }
    }
}
```

**Effort:** Medium
**Files:** New `ratelimit.go`, modifications to `server.go`

---

### R2.2: Add Token Expiration

**Issue:** Runtime tokens and grant tokens never expire.

**Recommended Fix:**
```go
// handshake.go
type tokenInfo struct {
    token     string
    issuedAt  time.Time
    expiresAt time.Time
}

type HandshakeServer struct {
    // ...
    tokens map[string]*tokenInfo
}

func (h *HandshakeServer) ValidateToken(runtimeID, token string) bool {
    h.mu.RLock()
    info, ok := h.tokens[runtimeID]
    h.mu.RUnlock()

    if !ok {
        return false
    }

    if time.Now().After(info.expiresAt) {
        return false
    }

    return subtle.ConstantTimeCompare([]byte(info.token), []byte(token)) == 1
}
```

**Also add:**
- Token refresh endpoint
- Configuration for expiration duration
- Cleanup goroutine for expired tokens

**Effort:** Medium
**Files:** `handshake.go`, `broker.go`

---

### R2.3: Add Service Registration Authorization

**Issue:** Any plugin can register any service type.

**Recommended Fix:**
```go
// registry.go
type ServiceRegistry struct {
    // ... existing fields ...

    // Map of runtime_id → allowed service types
    allowedServices map[string][]string
}

func (r *ServiceRegistry) RegisterService(...) {
    runtimeID := req.Header().Get("X-Plugin-Runtime-ID")

    // Check authorization
    allowed, ok := r.allowedServices[runtimeID]
    if ok && !contains(allowed, req.Msg.ServiceType) {
        return nil, connect.NewError(connect.CodePermissionDenied,
            fmt.Errorf("plugin %s not authorized to register service type %s",
                runtimeID, req.Msg.ServiceType))
    }

    // ... rest of registration
}

// Called during handshake to record allowed services
func (r *ServiceRegistry) SetAllowedServices(runtimeID string, services []string) {
    r.mu.Lock()
    defer r.mu.Unlock()
    r.allowedServices[runtimeID] = services
}
```

**Effort:** Medium
**Files:** `registry.go`, `handshake.go`

---

### R2.4: Add Input Validation

**Issue:** Metadata fields not validated.

**Recommended Fix:**
```go
// validation.go (new file)
const (
    MaxMetadataEntries  = 100
    MaxMetadataKeyLen   = 256
    MaxMetadataValueLen = 4096
    MaxServiceTypeLen   = 128
    MaxVersionLen       = 64
)

var validKeyPattern = regexp.MustCompile(`^[a-zA-Z][a-zA-Z0-9_-]*$`)

func ValidateMetadata(metadata map[string]string) error {
    if len(metadata) > MaxMetadataEntries {
        return fmt.Errorf("too many metadata entries: %d > %d",
            len(metadata), MaxMetadataEntries)
    }

    for k, v := range metadata {
        if len(k) > MaxMetadataKeyLen {
            return fmt.Errorf("metadata key too long: %d > %d", len(k), MaxMetadataKeyLen)
        }
        if len(v) > MaxMetadataValueLen {
            return fmt.Errorf("metadata value too long: %d > %d", len(v), MaxMetadataValueLen)
        }
        if !validKeyPattern.MatchString(k) {
            return fmt.Errorf("invalid metadata key format: %q", k)
        }
    }
    return nil
}

func ValidateServiceType(serviceType string) error {
    if serviceType == "" {
        return fmt.Errorf("service type cannot be empty")
    }
    if len(serviceType) > MaxServiceTypeLen {
        return fmt.Errorf("service type too long: %d > %d",
            len(serviceType), MaxServiceTypeLen)
    }
    if !validKeyPattern.MatchString(serviceType) {
        return fmt.Errorf("invalid service type format: %q", serviceType)
    }
    return nil
}
```

**Effort:** Low
**Files:** New `validation.go`, modifications to `registry.go`, `broker.go`

---

### R2.5: Remove Sensitive Data from Logs

**Issue:** Router logs may contain sensitive information.

**Recommended Fix:**
```go
// Add log levels
type LogLevel int

const (
    LogLevelError LogLevel = iota
    LogLevelWarn
    LogLevelInfo
    LogLevelDebug
)

var currentLogLevel = LogLevelInfo

func SetLogLevel(level LogLevel) {
    currentLogLevel = level
}

// In router.go
if currentLogLevel >= LogLevelDebug {
    log.Printf("[ROUTER] %s → %s %s (service: %s)",
        callerID, providerID, method, serviceType)
}

// For errors, use generic messages externally
if currentLogLevel >= LogLevelDebug {
    log.Printf("[ROUTER] Provider lookup failed: %v", err)
}
http.Error(w, "service unavailable", http.StatusServiceUnavailable)
```

**Effort:** Low
**Files:** `router.go`, new logging infrastructure

---

## Priority 3: Future Enhancements

These improvements would strengthen security but aren't critical.

### R3.1: Add Request Signing

Add HMAC signing for request integrity:

```go
type SignedRequest struct {
    Timestamp int64
    Nonce     string
    Signature string  // HMAC-SHA256(timestamp + nonce + body)
}
```

### R3.2: Add Mutual Plugin Authentication

Allow plugins to authenticate each other directly:

```go
type PluginAuthProvider interface {
    GetPluginCertificate() *x509.Certificate
    ValidatePluginCertificate(cert *x509.Certificate) error
}
```

### R3.3: Add Audit Logging

Log security-relevant events:

```go
type AuditEvent struct {
    Timestamp time.Time
    EventType string  // "registration", "discovery", "authentication", etc.
    Actor     string  // runtime_id
    Action    string
    Resource  string
    Outcome   string  // "success", "denied", "error"
    Details   map[string]string
}
```

### R3.4: Add Network Policy Integration

For Kubernetes deployments:

```go
type NetworkPolicyProvider interface {
    AllowConnection(from, to string) bool
    GetPolicy(runtimeID string) *NetworkPolicy
}
```

### R3.5: Add Plugin Sandboxing Options

For process-based plugins:

```go
type SandboxConfig struct {
    EnableSeccomp    bool
    EnableNamespaces bool  // Linux namespaces
    EnableCgroups    bool
    ReadOnlyRootFS   bool
    AllowedSyscalls  []string
}
```

## Implementation Roadmap

### Phase 1: Pre-Release (1-2 weeks)

| Task | Priority | Effort | Status |
|------|----------|--------|--------|
| R1.1 Fix mTLS interceptor | Critical | Medium | TODO |
| R1.2 Constant-time comparison | Critical | Low | TODO |
| R1.3 Handle rand errors | Critical | Low | TODO |
| R1.4 Security documentation | Critical | Medium | TODO |
| R1.5 TLS warnings | Critical | Low | TODO |

### Phase 2: Production Readiness (2-4 weeks)

| Task | Priority | Effort | Status |
|------|----------|--------|--------|
| R2.1 Rate limiting | High | Medium | TODO |
| R2.2 Token expiration | High | Medium | TODO |
| R2.3 Registration authorization | High | Medium | TODO |
| R2.4 Input validation | High | Low | TODO |
| R2.5 Log sanitization | Medium | Low | TODO |

### Phase 3: Hardening (Ongoing)

| Task | Priority | Effort | Status |
|------|----------|--------|--------|
| R3.1 Request signing | Low | High | FUTURE |
| R3.2 Mutual auth | Low | High | FUTURE |
| R3.3 Audit logging | Low | Medium | FUTURE |
| R3.4 Network policies | Low | Medium | FUTURE |
| R3.5 Sandboxing | Low | High | FUTURE |

## Testing Recommendations

### Security Tests to Add

1. **Authentication bypass tests**
   - Empty token
   - Invalid token format
   - Expired token (after implementing expiration)
   - Wrong runtime ID

2. **Authorization tests**
   - Register unauthorized service
   - Discover without authentication
   - Cross-plugin impersonation

3. **Input validation tests**
   - Large metadata values
   - Special characters in service types
   - Unicode edge cases

4. **DoS tests**
   - Registration flooding
   - Watcher flooding
   - Large request payloads

5. **Timing tests**
   - Token comparison timing variance
   - Authentication timing variance

### Fuzzing Recommendations

Consider fuzzing these inputs:
- Metadata maps
- Service type names
- Version strings
- Protocol messages (protobuf)
