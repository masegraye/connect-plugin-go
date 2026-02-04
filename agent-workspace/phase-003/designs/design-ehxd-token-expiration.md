# Design: Token Expiration

**Status:** Draft
**Author:** Security Review Agent
**Date:** 2026-02-03
**Related Findings:** AUTH-004, AUTH-005, REG-HIGH-002
**Severity:** HIGH

## Problem Statement

Currently, connect-plugin-go has two types of tokens that never expire:

1. **Runtime tokens** - issued during handshake (stored in `handshake.go`)
2. **Capability grant tokens** - issued by broker (stored in `broker.go`)

This creates a critical security vulnerability:
- Compromised tokens provide **permanent access** to the system
- No mechanism to revoke or expire tokens
- Tokens remain valid even after plugin shutdown or restart
- Memory leak potential - tokens accumulate without cleanup

**Impact:**
- **Confidentiality:** Stolen tokens grant indefinite access to host capabilities
- **Integrity:** Attackers can impersonate plugins indefinitely
- **Availability:** Memory exhaustion from unbounded token storage

## Token Types & Lifecycles

### Runtime Tokens (Phase 2)
- **Purpose:** Authenticate plugin's identity when making Phase 2 RPC calls
- **Lifecycle:** Issued during handshake when plugin provides `self_id`
- **Storage:** `HandshakeServer.tokens map[string]string` (runtime_id → token)
- **Usage:** Validated in `ValidateToken()`, used in `X-Plugin-Runtime-ID` + `Authorization` headers
- **Current Issue:** Never expire, never cleaned up

### Capability Grant Tokens (Phase 3)
- **Purpose:** Authorize access to specific host capabilities (logger, secrets, etc.)
- **Lifecycle:** Issued on-demand via `RequestCapability` RPC
- **Storage:** `CapabilityBroker.grants map[string]*grantInfo` (grant_id → grant)
- **Usage:** Validated in `handleCapabilityRequest()` via Bearer token
- **Current Issue:** Never expire, never cleaned up

### Token Lifecycle Comparison

| Token Type | Issued By | Issued When | Used For | Current TTL | Ideal TTL |
|------------|-----------|-------------|----------|-------------|-----------|
| Runtime | `HandshakeServer` | Handshake | Plugin identity auth | ∞ | 24 hours |
| Capability Grant | `CapabilityBroker` | On-demand | Capability access | ∞ | 1 hour |

## Proposed Solution

### 1. tokenInfo Structure

Replace simple `string` tokens with a rich `tokenInfo` structure:

```go
// tokenInfo stores token data with expiration metadata.
type tokenInfo struct {
    token     string
    issuedAt  time.Time
    expiresAt time.Time
}

// IsExpired checks if the token has expired.
func (t *tokenInfo) IsExpired() bool {
    return time.Now().After(t.expiresAt)
}
```

**Why this structure?**
- `token`: The actual token value (unchanged)
- `issuedAt`: Audit trail for token creation (security logs)
- `expiresAt`: Absolute expiration time (easier than TTL calculation)

### 2. Migration Plan

#### HandshakeServer (Runtime Tokens)

**Before:**
```go
type HandshakeServer struct {
    cfg    *ServeConfig
    mu     sync.RWMutex
    tokens map[string]string // runtime_id → token
}
```

**After:**
```go
type HandshakeServer struct {
    cfg    *ServeConfig
    mu     sync.RWMutex
    tokens map[string]*tokenInfo // runtime_id → token info
}
```

**Changes Required:**
- Update `Handshake()` to create `tokenInfo` with expiration
- Update `ValidateToken()` to check expiration
- Add cleanup goroutine to remove expired tokens

#### CapabilityBroker (Grant Tokens)

**Before:**
```go
type grantInfo struct {
    grantID        string
    capabilityType string
    token          string
    handler        CapabilityHandler
}
```

**After:**
```go
type grantInfo struct {
    grantID        string
    capabilityType string
    tokenData      *tokenInfo // changed from string
    handler        CapabilityHandler
}
```

**Changes Required:**
- Update `RequestCapability()` to create `tokenInfo` with expiration
- Update `handleCapabilityRequest()` to check expiration
- Add cleanup goroutine to remove expired grants

### 3. Expiration Checking

#### Runtime Token Validation

**Location:** `handshake.go:ValidateToken()`

**Current:**
```go
func (h *HandshakeServer) ValidateToken(runtimeID, token string) bool {
    h.mu.RLock()
    defer h.mu.RUnlock()

    expectedToken, ok := h.tokens[runtimeID]
    if !ok {
        return false
    }

    if len(expectedToken) != len(token) {
        return false
    }
    return subtle.ConstantTimeCompare([]byte(expectedToken), []byte(token)) == 1
}
```

**Proposed:**
```go
func (h *HandshakeServer) ValidateToken(runtimeID, token string) bool {
    h.mu.RLock()
    tokenInfo, ok := h.tokens[runtimeID]
    h.mu.RUnlock()

    if !ok {
        return false
    }

    // Check expiration BEFORE comparison (fail fast)
    if tokenInfo.IsExpired() {
        // Lazy cleanup - remove expired token
        h.mu.Lock()
        delete(h.tokens, runtimeID)
        h.mu.Unlock()
        return false
    }

    // Use constant-time comparison (security-critical)
    if len(tokenInfo.token) != len(token) {
        return false
    }
    return subtle.ConstantTimeCompare([]byte(tokenInfo.token), []byte(token)) == 1
}
```

**Key Design Decisions:**
1. Check expiration BEFORE constant-time comparison (performance)
2. Lazy cleanup on validation (simpler than background goroutine)
3. Return `false` for expired tokens (same as invalid)
4. Delete expired token immediately (prevent reuse)

#### Capability Grant Validation

**Location:** `broker.go:handleCapabilityRequest()`

**Current:**
```go
grant, ok := b.grants[grantID]
if !ok {
    http.Error(w, "invalid grant ID", http.StatusUnauthorized)
    return
}

// Use constant-time comparison
if len(grant.token) != len(token) {
    http.Error(w, "invalid token", http.StatusUnauthorized)
    return
}
if subtle.ConstantTimeCompare([]byte(grant.token), []byte(token)) != 1 {
    http.Error(w, "invalid token", http.StatusUnauthorized)
    return
}
```

**Proposed:**
```go
grant, ok := b.grants[grantID]
if !ok {
    http.Error(w, "invalid grant ID", http.StatusUnauthorized)
    return
}

// Check expiration BEFORE comparison
if grant.tokenData.IsExpired() {
    // Lazy cleanup
    b.mu.Lock()
    delete(b.grants, grantID)
    b.mu.Unlock()
    http.Error(w, "grant expired", http.StatusUnauthorized)
    return
}

// Use constant-time comparison
if len(grant.tokenData.token) != len(token) {
    http.Error(w, "invalid token", http.StatusUnauthorized)
    return
}
if subtle.ConstantTimeCompare([]byte(grant.tokenData.token), []byte(token)) != 1 {
    http.Error(w, "invalid token", http.StatusUnauthorized)
    return
}
```

**Error Message:** Use `"grant expired"` to distinguish from other auth failures (better debugging).

### 4. Cleanup Strategy

**Two Approaches:**

#### Option A: Lazy Cleanup (RECOMMENDED)

**Pros:**
- Simpler implementation (no goroutines)
- No shutdown coordination needed
- Naturally handles low-traffic scenarios
- Zero overhead when system is idle

**Cons:**
- Expired tokens remain in memory until accessed
- Potential memory leak if tokens are never validated

**When to use:** Default approach for most deployments.

#### Option B: Background Goroutine

**Pros:**
- Proactive memory cleanup
- Guaranteed bounds on memory usage
- Useful for high-traffic scenarios with many short-lived grants

**Cons:**
- More complex implementation
- Requires goroutine lifecycle management
- Requires graceful shutdown coordination
- Overhead even when idle

**When to use:** Opt-in for high-traffic deployments.

#### Recommended: Hybrid Approach

**Use lazy cleanup by default** + **optional background cleanup** for high-traffic scenarios:

```go
type ServeConfig struct {
    // ... existing fields ...

    // TokenCleanupInterval enables background token cleanup.
    // If 0, uses lazy cleanup only (default).
    // If > 0, runs cleanup goroutine at specified interval.
    // Recommended: 5 * time.Minute for high-traffic deployments.
    TokenCleanupInterval time.Duration
}
```

**Background cleanup implementation:**

```go
// startTokenCleanup starts background cleanup if configured.
func (h *HandshakeServer) startTokenCleanup(interval time.Duration, stopCh <-chan struct{}) {
    if interval == 0 {
        return // Lazy cleanup only
    }

    go func() {
        ticker := time.NewTicker(interval)
        defer ticker.Stop()

        for {
            select {
            case <-ticker.C:
                h.cleanupExpiredTokens()
            case <-stopCh:
                return
            }
        }
    }()
}

// cleanupExpiredTokens removes all expired tokens.
func (h *HandshakeServer) cleanupExpiredTokens() {
    h.mu.Lock()
    defer h.mu.Unlock()

    now := time.Now()
    for runtimeID, info := range h.tokens {
        if now.After(info.expiresAt) {
            delete(h.tokens, runtimeID)
        }
    }
}
```

**Similar implementation for CapabilityBroker.**

### 5. Configuration

#### Default TTL Values

**Recommended defaults based on security best practices:**

| Token Type | Default TTL | Rationale |
|------------|-------------|-----------|
| Runtime Token | 24 hours | Plugins typically have long lifecycles |
| Capability Grant | 1 hour | Fine-grained access should be short-lived |

**Industry comparisons:**
- AWS IAM session tokens: 1-12 hours
- Kubernetes service account tokens: 1 hour (rotated)
- OAuth2 access tokens: 1 hour
- TLS session tickets: 2 hours

#### Configuration Structure

**Add to ServeConfig:**

```go
type ServeConfig struct {
    // ... existing fields ...

    // RuntimeTokenTTL is the time-to-live for runtime tokens.
    // Default: 24 hours
    RuntimeTokenTTL time.Duration

    // CapabilityGrantTTL is the time-to-live for capability grants.
    // Default: 1 hour
    CapabilityGrantTTL time.Duration

    // TokenCleanupInterval enables background token cleanup.
    // If 0, uses lazy cleanup only (default).
    // If > 0, runs cleanup goroutine at specified interval.
    // Recommended: 5 * time.Minute for high-traffic deployments.
    TokenCleanupInterval time.Duration
}
```

**Apply defaults in Serve():**

```go
func Serve(cfg *ServeConfig) error {
    // ... existing defaults ...

    if cfg.RuntimeTokenTTL == 0 {
        cfg.RuntimeTokenTTL = 24 * time.Hour
    }
    if cfg.CapabilityGrantTTL == 0 {
        cfg.CapabilityGrantTTL = 1 * time.Hour
    }
    // TokenCleanupInterval defaults to 0 (lazy only)

    // ...
}
```

**No ClientConfig changes needed** - token expiration is server-side only.

### 6. API Changes

#### Breaking Changes

**NONE.** This is a backward-compatible change:

- Token generation signature unchanged
- Token validation signature unchanged
- RPC interfaces unchanged
- Only internal storage structure changes

#### Internal API Changes

**HandshakeServer:**
```go
// No public API changes - all internal
tokens map[string]*tokenInfo // changed from map[string]string
```

**CapabilityBroker:**
```go
// No public API changes - all internal
type grantInfo struct {
    // ...
    tokenData *tokenInfo // changed from token string
}
```

**New Config Fields (backward compatible):**
- `ServeConfig.RuntimeTokenTTL` (default: 24h)
- `ServeConfig.CapabilityGrantTTL` (default: 1h)
- `ServeConfig.TokenCleanupInterval` (default: 0)

### 7. Token Refresh Strategy

#### Do We Need Token Refresh?

**NO, for these reasons:**

1. **Runtime tokens are long-lived (24h)** - sufficient for plugin lifecycle
2. **Capability grants are short-lived (1h)** - should be re-requested if needed
3. **Refresh adds complexity** - new RPC endpoints, rotation logic, grace periods
4. **Re-handshake is simple** - plugin can just reconnect if token expires
5. **Re-request is cheap** - capability grants are lightweight to re-issue

#### Plugin Behavior on Token Expiration

**Runtime Token Expiration (24h):**
```
Plugin → Host: [Runtime RPC with expired token]
Host → Plugin: 401 Unauthorized
Plugin → Plugin: Detect auth failure
Plugin → Host: [Perform new handshake]
Host → Plugin: [New runtime_id + token]
Plugin → Plugin: Update runtime identity
Plugin → Host: [Retry RPC with new token]
```

**Recommendation:** Plugins should handle 401 errors by re-handshaking.

**Capability Grant Expiration (1h):**
```
Plugin → Host: [Capability RPC with expired grant]
Host → Plugin: 401 Unauthorized "grant expired"
Plugin → Plugin: Detect grant expiration
Plugin → Host: RequestCapability [new grant]
Host → Plugin: [New grant + token]
Plugin → Plugin: Update capability client
Plugin → Host: [Retry RPC with new grant]
```

**Recommendation:** Plugins should handle 401 errors by re-requesting capability.

#### Future Enhancement: Proactive Refresh

If telemetry shows frequent re-handshakes/re-requests, consider adding:

```go
// RefreshRuntimeToken refreshes an existing runtime token.
// Returns new token with extended TTL.
// Old token remains valid for 5-minute grace period.
rpc RefreshRuntimeToken(RefreshRuntimeTokenRequest)
    returns (RefreshRuntimeTokenResponse);
```

**Not recommended for v1** - add only if proven necessary by usage patterns.

## Migration

### Step-by-Step Implementation

#### Phase 1: Add tokenInfo Structure
**Files:** `handshake.go`, `broker.go`

```go
// Add to handshake.go
type tokenInfo struct {
    token     string
    issuedAt  time.Time
    expiresAt time.Time
}

func (t *tokenInfo) IsExpired() bool {
    return time.Now().After(t.expiresAt)
}
```

#### Phase 2: Migrate HandshakeServer
**File:** `handshake.go`

1. Change `tokens map[string]string` to `tokens map[string]*tokenInfo`
2. Update `Handshake()` to create `tokenInfo`:
   ```go
   tokenData := &tokenInfo{
       token:     runtimeToken,
       issuedAt:  time.Now(),
       expiresAt: time.Now().Add(h.cfg.RuntimeTokenTTL),
   }
   h.mu.Lock()
   h.tokens[runtimeID] = tokenData
   h.mu.Unlock()
   ```
3. Update `ValidateToken()` to check expiration (see Section 3)

#### Phase 3: Migrate CapabilityBroker
**File:** `broker.go`

1. Change `grantInfo.token string` to `grantInfo.tokenData *tokenInfo`
2. Update `RequestCapability()` to create `tokenInfo`:
   ```go
   tokenData := &tokenInfo{
       token:     token,
       issuedAt:  time.Now(),
       expiresAt: time.Now().Add(b.cfg.CapabilityGrantTTL),
   }
   grant := &grantInfo{
       grantID:        grantID,
       capabilityType: req.Msg.CapabilityType,
       tokenData:      tokenData,
       handler:        handler,
   }
   ```
3. Update `handleCapabilityRequest()` to check expiration (see Section 3)

#### Phase 4: Add Configuration
**File:** `server.go`

1. Add TTL fields to `ServeConfig`
2. Apply defaults in `Serve()`
3. Pass TTLs to `HandshakeServer` and `CapabilityBroker`

#### Phase 5: Add Background Cleanup (Optional)
**Files:** `handshake.go`, `broker.go`, `server.go`

1. Add `TokenCleanupInterval` to `ServeConfig`
2. Implement `startTokenCleanup()` goroutine
3. Implement `cleanupExpiredTokens()` method
4. Wire up cleanup in `Serve()` with proper shutdown

#### Phase 6: Update Tests
**Files:** `security_test.go`, `broker_test.go`, new `token_expiration_test.go`

1. Update existing tests to work with `tokenInfo`
2. Add tests for token expiration:
   - Runtime token expiration
   - Capability grant expiration
   - Lazy cleanup behavior
   - Background cleanup (if implemented)
   - Time boundary conditions

### Test Strategy

**Unit Tests:**
```go
// TestRuntimeToken_Expiration
// - Issue token with 1-second TTL
// - Validate immediately (should pass)
// - Wait 2 seconds
// - Validate again (should fail)
// - Verify token removed from map

// TestCapabilityGrant_Expiration
// - Request grant with 1-second TTL
// - Use immediately (should work)
// - Wait 2 seconds
// - Use again (should 401)
// - Verify grant removed from map

// TestTokenCleanup_Background
// - Enable background cleanup (1-second interval)
// - Issue 10 tokens with 1-second TTL
// - Wait 3 seconds
// - Verify all tokens cleaned up
// - Stop server, verify goroutine stopped
```

**Integration Tests:**
```go
// TestE2E_RuntimeTokenExpiration
// - Start host with short runtime token TTL
// - Plugin performs handshake
// - Plugin makes RPC (should succeed)
// - Wait for token expiration
// - Plugin makes RPC (should fail 401)
// - Plugin re-handshakes
// - Plugin makes RPC (should succeed)

// TestE2E_CapabilityGrantExpiration
// - Start host with short grant TTL
// - Plugin requests capability grant
// - Plugin uses capability (should succeed)
// - Wait for grant expiration
// - Plugin uses capability (should fail 401)
// - Plugin re-requests grant
// - Plugin uses capability (should succeed)
```

### Rollout Plan

**Version 1.x (Current):**
- No token expiration (current behavior)

**Version 2.0 (Breaking):**
- Add token expiration with defaults (24h runtime, 1h grants)
- Add configuration options
- Add lazy cleanup (always enabled)
- Add background cleanup (opt-in)
- Update examples to show token refresh patterns

**Migration Guide for Users:**
1. No code changes required - backward compatible
2. Consider configuring TTLs based on security requirements
3. Consider enabling background cleanup for high-traffic deployments
4. Update plugin code to handle 401 errors gracefully

## Security Considerations

### Threat Mitigation

**Before (vulnerabilities):**
- AUTH-004: Token leakage via logs → permanent access ✗
- AUTH-005: Stolen token → indefinite impersonation ✗
- REG-HIGH-002: Token accumulation → memory exhaustion ✗

**After (mitigations):**
- AUTH-004: Token leakage → time-bounded access (24h max) ✓
- AUTH-005: Stolen token → limited window for abuse (1h-24h) ✓
- REG-HIGH-002: Token cleanup → bounded memory usage ✓

### Attack Surface Analysis

**Reduced Impact:**
- Compromised runtime token: 24 hours vs. ∞
- Compromised grant token: 1 hour vs. ∞
- Memory DoS: Bounded vs. unbounded growth

**New Considerations:**
- Clock skew: Ensure NTP synchronization between host/plugins
- Time manipulation: Token expiration relies on system clock
- Race conditions: Ensure thread-safe cleanup

### Timing Attack Prevention

**Critical:** Maintain constant-time comparison for token validation.

**Current (secure):**
```go
subtle.ConstantTimeCompare([]byte(expectedToken), []byte(token))
```

**After expiration check (still secure):**
```go
// Check expiration first (not timing-sensitive)
if tokenInfo.IsExpired() {
    return false // No comparison needed
}

// Then check token (constant-time)
subtle.ConstantTimeCompare([]byte(tokenInfo.token), []byte(token))
```

**Why this is safe:**
- Expiration check is not secret-dependent (time is public)
- Token comparison remains constant-time
- Early return on expiration is performance optimization, not security risk

### Clock Synchronization

**Requirement:** Host and plugins must have synchronized clocks.

**Best Practices:**
1. Use NTP in production environments
2. Document clock sync requirements
3. Consider clock skew tolerance (5-minute grace period?)
4. Add warning if system clock is not synchronized

**Future Enhancement:**
```go
type ServeConfig struct {
    // ClockSkewTolerance allows tokens to be valid for an additional
    // grace period after expiration to handle clock drift.
    // Default: 0 (no tolerance)
    // Recommended: 5 * time.Minute for distributed systems
    ClockSkewTolerance time.Duration
}
```

## Performance Impact

### Memory Usage

**Before:**
```
RuntimeToken:  64 bytes (string pointer + 32-byte token)
CapabilityGrant: ~128 bytes (grantInfo struct)
Growth: Unbounded (O(n) with n = total tokens ever issued)
```

**After (Lazy Cleanup):**
```
RuntimeToken: 80 bytes (tokenInfo: token + 2x time.Time = 16 bytes overhead)
CapabilityGrant: ~144 bytes (grantInfo with tokenInfo)
Growth: Bounded (O(n) with n = active tokens only)
```

**After (Background Cleanup):**
```
Same as lazy, but with guaranteed cleanup every interval.
Memory peaks at max_concurrent_tokens × token_size.
```

**Calculation for 1000 plugins:**
```
Before: 1000 plugins × 64 bytes = 64 KB (never cleaned)
After:  1000 plugins × 80 bytes = 80 KB (cleaned after 24h)
```

**Negligible overhead - acceptable.**

### CPU Usage

**Token Validation (Hot Path):**
```
Before: Map lookup + constant-time compare = ~100ns
After:  Map lookup + time.After() + constant-time compare = ~120ns
Overhead: 20% (still sub-microsecond)
```

**Background Cleanup:**
```
Interval: 5 minutes
Duration: O(n) map iteration = ~1ms per 1000 tokens
CPU: Negligible (< 0.01% on modern hardware)
```

**Performance impact: NEGLIGIBLE.**

### Benchmarks

**Required benchmarks:**
```go
BenchmarkValidateToken_Expired       // ~120ns (added time check)
BenchmarkValidateToken_Valid         // ~120ns (unchanged)
BenchmarkCleanupExpiredTokens        // ~1ms per 1000 tokens
BenchmarkHandshake_WithTokenInfo     // ~500us (unchanged)
```

## Monitoring & Observability

### Metrics (Future Enhancement)

**Recommended Prometheus metrics:**

```
# Token lifecycle
connectplugin_runtime_tokens_issued_total
connectplugin_runtime_tokens_expired_total
connectplugin_runtime_tokens_active

connectplugin_capability_grants_issued_total
connectplugin_capability_grants_expired_total
connectplugin_capability_grants_active

# Cleanup
connectplugin_token_cleanup_runs_total
connectplugin_token_cleanup_removed_total
connectplugin_token_cleanup_duration_seconds

# Auth failures
connectplugin_token_validation_failures_total{reason="expired"}
connectplugin_token_validation_failures_total{reason="invalid"}
```

### Logging

**Add structured logging for security events:**

```go
// Token expiration in ValidateToken()
log.Printf("WARN [connectplugin]: Runtime token expired: runtime_id=%s", runtimeID)

// Token expiration in handleCapabilityRequest()
log.Printf("WARN [connectplugin]: Capability grant expired: grant_id=%s type=%s",
    grantID, grant.capabilityType)

// Background cleanup
log.Printf("INFO [connectplugin]: Token cleanup: removed=%d runtime=%d grants=%d",
    totalRemoved, runtimeRemoved, grantsRemoved)
```

**Avoid logging token values** - security risk.

## Implementation Plan

### Phase 1: Core Implementation (1-2 days)
- [ ] Add `tokenInfo` structure to `handshake.go` and `broker.go`
- [ ] Migrate `HandshakeServer.tokens` to use `*tokenInfo`
- [ ] Migrate `CapabilityBroker.grants` to use `*tokenInfo`
- [ ] Update `ValidateToken()` with expiration check + lazy cleanup
- [ ] Update `handleCapabilityRequest()` with expiration check + lazy cleanup

### Phase 2: Configuration (0.5 days)
- [ ] Add `RuntimeTokenTTL` to `ServeConfig`
- [ ] Add `CapabilityGrantTTL` to `ServeConfig`
- [ ] Apply defaults in `Serve()` (24h runtime, 1h grants)
- [ ] Pass TTLs to `HandshakeServer` and `CapabilityBroker` constructors

### Phase 3: Testing (1-2 days)
- [ ] Update existing tests to work with `tokenInfo`
- [ ] Add unit tests for token expiration
- [ ] Add unit tests for lazy cleanup
- [ ] Add integration tests for token expiration + re-handshake
- [ ] Add benchmarks for performance validation
- [ ] Add security tests for timing attacks with expiration

### Phase 4: Background Cleanup (Optional, 0.5 days)
- [ ] Add `TokenCleanupInterval` to `ServeConfig`
- [ ] Implement `startTokenCleanup()` goroutine in `HandshakeServer`
- [ ] Implement `startTokenCleanup()` goroutine in `CapabilityBroker`
- [ ] Wire up cleanup in `Serve()` with `StopCh` coordination
- [ ] Add tests for background cleanup + shutdown

### Phase 5: Documentation (0.5 days)
- [ ] Update security documentation
- [ ] Add token expiration section to examples
- [ ] Document plugin error handling for 401
- [ ] Add migration guide for v2.0
- [ ] Document configuration options

**Total Effort: 3-5 days (depending on background cleanup)**

## Alternatives Considered

### Alternative 1: Refresh Tokens
**Description:** Implement OAuth-style refresh tokens with rotation.

**Pros:**
- Shorter access token lifetimes (5 minutes)
- Graceful rotation without service interruption

**Cons:**
- Significant complexity (new RPC endpoints, rotation logic)
- Storage overhead (2x tokens per client)
- Grace periods complicate revocation
- Overkill for plugin architecture

**Decision:** Rejected - re-handshake is simpler and sufficient.

### Alternative 2: JWT Tokens
**Description:** Use JWT tokens with embedded expiration claims.

**Pros:**
- Self-contained (no storage needed)
- Standard format (easier to debug)
- Claims validation built-in

**Cons:**
- Cannot revoke before expiration (no storage)
- Larger tokens (Base64 overhead)
- Signature verification overhead
- Requires key management

**Decision:** Rejected - current random tokens are simpler and more efficient.

### Alternative 3: Time-Based One-Time Passwords (TOTP)
**Description:** Use TOTP for dynamic token generation.

**Pros:**
- Tokens change every 30 seconds
- No storage needed (derived from shared secret)

**Cons:**
- Clock synchronization critical
- Complex secret distribution
- Not standard for API auth
- Overkill for plugin architecture

**Decision:** Rejected - inappropriate for this use case.

### Alternative 4: Capability-Based Security (No Tokens)
**Description:** Replace tokens with object capabilities (unforgeable references).

**Pros:**
- No token management needed
- Excellent security properties
- Natural revocation (drop reference)

**Cons:**
- Fundamental architecture change
- Incompatible with HTTP/gRPC
- Complex marshaling/unmarshaling
- Not standard practice

**Decision:** Rejected - too disruptive for v2.0.

## Open Questions

1. **Should we add metrics/monitoring in v2.0?**
   - Recommendation: Add in v2.1 based on user feedback

2. **Should we implement background cleanup by default?**
   - Recommendation: No, make it opt-in via `TokenCleanupInterval`

3. **Should we add clock skew tolerance?**
   - Recommendation: No, document NTP requirement instead

4. **Should we log token expiration events?**
   - Recommendation: Yes, but at WARN level (security-relevant)

5. **Should we add token refresh in v2.0?**
   - Recommendation: No, wait for user demand

## References

- [AUTH-004] Token leakage findings - Security audit
- [AUTH-005] Token permanence findings - Security audit
- [REG-HIGH-002] Memory leak findings - Security audit
- [RFC 6750] Bearer Token Usage - IETF
- [OWASP] Token Best Practices
- AWS IAM Session Token TTLs (1-12 hours)
- Kubernetes Service Account Token Rotation (1 hour)
- OAuth2 Token Lifetimes (access: 1h, refresh: 90d)

## Appendix A: Code Examples

### Example 1: Plugin Handling Token Expiration

```go
// Example: KV plugin with automatic re-handshake on 401
type kvPlugin struct {
    client *connectplugin.Client
}

func (p *kvPlugin) Get(ctx context.Context, key string) (string, error) {
    // Attempt RPC
    val, err := p.tryGet(ctx, key)
    if err != nil && isAuthError(err) {
        // Token expired - re-handshake
        if err := p.client.Connect(ctx); err != nil {
            return "", fmt.Errorf("re-handshake failed: %w", err)
        }
        // Retry RPC
        return p.tryGet(ctx, key)
    }
    return val, err
}

func isAuthError(err error) bool {
    return connect.CodeOf(err) == connect.CodeUnauthenticated
}
```

### Example 2: Host Configuration

```go
func main() {
    cfg := &connectplugin.ServeConfig{
        Plugins: plugins,
        Impls:   impls,

        // Configure token expiration
        RuntimeTokenTTL:      24 * time.Hour, // Default
        CapabilityGrantTTL:   1 * time.Hour,  // Default
        TokenCleanupInterval: 5 * time.Minute, // Optional
    }

    if err := connectplugin.Serve(cfg); err != nil {
        log.Fatal(err)
    }
}
```

### Example 3: Capability Grant Refresh

```go
// Example: Logger plugin with automatic grant refresh
type loggerClient struct {
    broker   connectpluginv1connect.CapabilityBrokerClient
    endpoint string
    mu       sync.Mutex
    grant    *connectpluginv1.CapabilityGrant
    client   loggerv1connect.LoggerClient
}

func (c *loggerClient) Log(ctx context.Context, msg string) error {
    // Attempt log
    err := c.tryLog(ctx, msg)
    if err != nil && isGrantExpired(err) {
        // Grant expired - request new grant
        if err := c.refreshGrant(ctx); err != nil {
            return fmt.Errorf("grant refresh failed: %w", err)
        }
        // Retry log
        return c.tryLog(ctx, msg)
    }
    return err
}

func isGrantExpired(err error) bool {
    return connect.CodeOf(err) == connect.CodeUnauthenticated
}
```

## Appendix B: Security Checklist

**Pre-Deployment:**
- [ ] NTP synchronization configured on all nodes
- [ ] Clock skew monitoring in place (optional)
- [ ] Log aggregation configured for token expiration events
- [ ] Metrics collection configured (optional)
- [ ] Plugins updated to handle 401 errors gracefully

**Post-Deployment:**
- [ ] Monitor auth failure rates (should not spike)
- [ ] Monitor re-handshake rates (baseline for future)
- [ ] Monitor memory usage (should stabilize)
- [ ] Check logs for token expiration warnings
- [ ] Validate cleanup is working (if background enabled)

## Appendix C: Performance Benchmarks

**Expected Performance (to be validated):**

```
BenchmarkValidateToken_Before         100000000   100 ns/op   0 B/op   0 allocs/op
BenchmarkValidateToken_After          80000000    120 ns/op   0 B/op   0 allocs/op

BenchmarkHandshake_Before             2000        500 µs/op   1024 B/op   10 allocs/op
BenchmarkHandshake_After              2000        520 µs/op   1072 B/op   11 allocs/op

BenchmarkCleanupTokens_1000           10000       100 µs/op   0 B/op   0 allocs/op
BenchmarkCleanupTokens_10000          1000        1000 µs/op  0 B/op   0 allocs/op
```

**Acceptance Criteria:**
- Token validation overhead < 50% (currently 20%)
- Handshake overhead < 10% (currently 4%)
- Cleanup time < 1ms per 1000 tokens

---

**Status:** Ready for implementation
**Next Steps:** Review with security team, implement Phase 1
