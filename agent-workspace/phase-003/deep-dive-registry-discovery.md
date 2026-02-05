# Security Assessment: Service Registry and Discovery System

**Component:** connect-plugin-go Service Registry and Discovery
**Files Reviewed:**
- `/Users/masegraye/workspaces/2026-01-29/connect-plugin-review/connect-plugin-go/registry.go`
- `/Users/masegraye/workspaces/2026-01-29/connect-plugin-review/connect-plugin-go/discovery.go`
- `/Users/masegraye/workspaces/2026-01-29/connect-plugin-review/connect-plugin-go/router.go`
- `/Users/masegraye/workspaces/2026-01-29/connect-plugin-review/connect-plugin-go/broker.go`
- `/Users/masegraye/workspaces/2026-01-29/connect-plugin-review/connect-plugin-go/handshake.go`
- `/Users/masegraye/workspaces/2026-01-29/connect-plugin-review/connect-plugin-go/platform.go`
- `/Users/masegraye/workspaces/2026-01-29/connect-plugin-review/connect-plugin-go/lifecycle.go`

**Date:** 2026-01-29
**Auditor:** Security Assessment Agent

---

## Executive Summary

This security audit examined the service registry and discovery system in connect-plugin-go, which enables plugin-to-plugin communication through a centralized host. The analysis identified **12 distinct security findings** across critical, high, medium, and low severity levels.

The most critical issues involve:
1. **Missing authorization controls** for service registration - any authenticated plugin can register any service type
2. **No rate limiting** on registration operations, enabling DoS attacks
3. **Token handling weaknesses** including lack of expiration and timing-safe comparisons
4. **Unrestricted service discovery** allowing information leakage

---

## Detailed Findings

### 1. Missing Service Registration Authorization

**Severity:** CRITICAL
**Location:** `registry.go:96-136` (RegisterService function)

**Description:**
The `RegisterService` RPC accepts any `service_type` from any authenticated plugin without authorization checks. A malicious or compromised plugin can register services it does not legitimately provide.

```go
// registry.go:96-136
func (r *ServiceRegistry) RegisterService(
    ctx context.Context,
    req *connect.Request[connectpluginv1.RegisterServiceRequest],
) (*connect.Response[connectpluginv1.RegisterServiceResponse], error) {
    // Extract runtime_id from request headers
    runtimeID := req.Header().Get("X-Plugin-Runtime-ID")
    if runtimeID == "" {
        return nil, connect.NewError(...)
    }
    // NO AUTHORIZATION CHECK: accepts any service_type
    provider := &ServiceProvider{
        ServiceType:    req.Msg.ServiceType,  // Attacker-controlled
        ...
    }
    r.providers[req.Msg.ServiceType] = append(...)
```

**Attack Scenario:**
1. Malicious plugin authenticates with valid runtime token
2. Plugin registers as provider for critical service types (e.g., "secrets", "auth")
3. Other plugins discover and route sensitive requests to the malicious plugin

**Remediation:**
- Implement an allowlist of service types each plugin is permitted to register
- Validate `req.Msg.ServiceType` against the plugin's declared `Provides` from handshake
- Consider cryptographic service type verification (plugin must prove it should provide a service)

---

### 2. Service Impersonation via Route Hijacking

**Severity:** CRITICAL
**Location:** `registry.go:125`, `router.go:89-93`

**Description:**
Multiple plugins can register for the same `service_type`, and the selection strategy is controlled by host configuration. A malicious plugin can register as a provider for an existing service type and potentially intercept traffic via `SelectionFirst`, `SelectionRandom`, or `SelectionRoundRobin`.

```go
// registry.go:125 - Multiple providers allowed
r.providers[req.Msg.ServiceType] = append(r.providers[req.Msg.ServiceType], provider)

// registry.go:280-299 - Selection strategies can route to malicious provider
switch strategy {
case SelectionFirst:
    return providers[0]  // Order-dependent
case SelectionRandom:
    return providers[rand.Intn(len(providers))]  // Random chance
```

**Attack Scenario:**
1. Legitimate "cache" plugin registers
2. Malicious plugin also registers as "cache" provider
3. With RoundRobin/Random, some requests route to malicious plugin
4. Malicious plugin intercepts, logs, or modifies data

**Remediation:**
- Implement service type ownership model (first registrant owns the type)
- Add option to require explicit multi-provider approval
- Log and alert on duplicate service type registrations

---

### 3. Unrestricted Service Discovery

**Severity:** HIGH
**Location:** `registry.go:368-395` (DiscoverService function)

**Description:**
Any plugin can discover any service without authorization. This allows information disclosure about the system topology and enables targeted attacks.

```go
// registry.go:368-395
func (r *ServiceRegistry) DiscoverService(
    ctx context.Context,
    req *connect.Request[connectpluginv1.DiscoverServiceRequest],
) (*connect.Response[connectpluginv1.DiscoverServiceResponse], error) {
    // NO AUTHORIZATION: Any plugin can discover any service
    provider, err := r.SelectProvider(req.Msg.ServiceType, req.Msg.MinVersion)
```

**Attack Scenario:**
1. Malicious plugin enumerates all service types
2. Discovers sensitive services (secrets, admin, internal-api)
3. Uses this information for targeted attacks

**Remediation:**
- Implement discovery ACLs based on plugin's declared `Requires`
- Only allow plugins to discover services they declared as dependencies
- Consider namespace isolation for sensitive services

---

### 4. Registration Flooding Denial of Service

**Severity:** HIGH
**Location:** `registry.go:96-136`, `registry.go:397-456`

**Description:**
No rate limiting exists on service registration or watch operations. A malicious plugin can flood the registry with registrations, exhausting memory and CPU.

```go
// registry.go:125 - Unbounded append
r.providers[req.Msg.ServiceType] = append(r.providers[req.Msg.ServiceType], provider)

// registry.go:128 - Unbounded map growth
r.registrations[registrationID] = provider

// registry.go:411 - Watcher channel created per watch request
watcher := &serviceWatcher{
    ch: make(chan *connectpluginv1.WatchServiceEvent, 10),
```

**Attack Scenario:**
1. Malicious plugin repeatedly calls RegisterService with unique service types
2. Memory exhaustion as `providers` and `registrations` maps grow unbounded
3. Or: Plugin opens thousands of WatchService streams
4. Host runs out of memory/goroutines

**Remediation:**
- Implement per-plugin registration limits
- Add rate limiting (e.g., max 10 registrations/minute/plugin)
- Limit total registrations per service type
- Limit concurrent watch streams per plugin

---

### 5. Token Non-Expiration and Revocation Gap

**Severity:** HIGH
**Location:** `handshake.go:91-93`, `broker.go:99-105`

**Description:**
Runtime tokens and capability grant tokens have no expiration mechanism. Once issued, tokens remain valid indefinitely until the process restarts.

```go
// handshake.go:91-93 - Token stored without expiration
h.mu.Lock()
h.tokens[runtimeID] = runtimeToken  // No expiration field
h.mu.Unlock()

// broker.go:99-105 - Grant tokens also never expire
grant := &grantInfo{
    grantID:        grantID,
    capabilityType: req.Msg.CapabilityType,
    token:          token,  // No expiration
    handler:        handler,
}
b.grants[grantID] = grant
```

**Attack Scenario:**
1. Plugin authenticates and receives token
2. Plugin is later removed/revoked
3. Attacker with stolen token continues making requests
4. Token remains valid for lifetime of host process

**Remediation:**
- Add expiration timestamp to tokens (`ExpiresAt time.Time`)
- Implement token refresh mechanism
- Add explicit token revocation when plugins are removed
- Consider short-lived tokens with automatic renewal

---

### 6. Timing-Unsafe Token Comparison

**Severity:** MEDIUM
**Location:** `handshake.go:180-190`, `broker.go:164`

**Description:**
Token validation uses standard string comparison (`==`) instead of constant-time comparison, enabling timing attacks.

```go
// handshake.go:188-189
return expectedToken == token  // Timing attack possible

// broker.go:164
if grant.token != token {  // Timing attack possible
```

**Attack Scenario:**
1. Attacker makes repeated requests with guessed tokens
2. Measures response times to determine character-by-character correctness
3. Eventually reconstructs valid token through statistical analysis

**Remediation:**
- Use `crypto/subtle.ConstantTimeCompare()` for token validation:
```go
import "crypto/subtle"
return subtle.ConstantTimeCompare([]byte(expectedToken), []byte(token)) == 1
```

---

### 7. Insufficient Input Validation on Metadata

**Severity:** MEDIUM
**Location:** `registry.go:119-121`

**Description:**
Service provider metadata is accepted without validation for size or content. This enables data injection and resource exhaustion.

```go
// registry.go:119-121
provider := &ServiceProvider{
    ...
    Metadata:       req.Msg.Metadata,  // No validation
    ...
}
```

**Attack Scenario:**
1. Plugin registers with extremely large metadata (megabytes of data)
2. Metadata propagates to all watchers and discovery responses
3. Memory exhaustion and amplification attacks possible
4. Or: Metadata contains malicious content (XSS if displayed in UI)

**Remediation:**
- Validate metadata key/value lengths (e.g., max 256 chars each)
- Limit total metadata entries (e.g., max 20 keys)
- Sanitize/escape metadata values
- Reject reserved metadata keys

---

### 8. Unregistration Authorization Bypass

**Severity:** MEDIUM
**Location:** `registry.go:139-172`

**Description:**
Any plugin with a valid registration ID can unregister services, not just the plugin that registered them. While registration IDs are random, this violates the principle of least privilege.

```go
// registry.go:139-172
func (r *ServiceRegistry) UnregisterService(
    ctx context.Context,
    req *connect.Request[connectpluginv1.UnregisterServiceRequest],
) (*connect.Response[connectpluginv1.UnregisterServiceResponse], error) {
    r.mu.Lock()
    defer r.mu.Unlock()

    // No check that caller's runtime_id matches provider.RuntimeID
    provider, ok := r.registrations[req.Msg.RegistrationId]
    if !ok {
        return nil, connect.NewError(...)
    }
    // Allows unregistration without ownership verification
```

**Attack Scenario:**
1. Plugin A registers a service, receives registration ID
2. Registration ID leaks (logs, error messages, enumeration)
3. Plugin B uses the ID to unregister Plugin A's service
4. Service disruption/denial of service

**Remediation:**
- Extract caller's `X-Plugin-Runtime-ID` from request
- Verify `provider.RuntimeID == callerRuntimeID` before unregistering
- Or: Sign registration IDs with plugin's token

---

### 9. Information Disclosure via Error Messages

**Severity:** MEDIUM
**Location:** `router.go:91`, `registry.go:149-152`, `registry.go:315`

**Description:**
Error messages expose internal system details including provider IDs, registration IDs, and service topology.

```go
// router.go:91 - Exposes provider ID
http.Error(w, fmt.Sprintf("provider not found: %s", providerID), http.StatusNotFound)

// registry.go:149-152 - Exposes registration ID
return nil, connect.NewError(
    connect.CodeNotFound,
    fmt.Errorf("registration not found: %s", req.Msg.RegistrationId),
)

// registry.go:315 - Confirms provider existence
return nil, fmt.Errorf("provider not found: %s", registrationID)
```

**Attack Scenario:**
1. Attacker enumerates registration/provider IDs through errors
2. Error messages confirm which IDs exist vs. don't exist
3. Information used to plan targeted attacks

**Remediation:**
- Use generic error messages: "resource not found"
- Log detailed errors server-side only
- Return consistent error responses regardless of failure reason

---

### 10. Race Condition in Watch/Notify

**Severity:** MEDIUM
**Location:** `registry.go:406-456`

**Description:**
The WatchService implementation has a subtle race condition between registering the watcher and receiving the initial state.

```go
// registry.go:406-423
func (r *ServiceRegistry) WatchService(...) error {
    r.mu.Lock()
    // Create watcher
    watcher := &serviceWatcher{...}
    // Register watcher
    r.watchers[serviceType] = append(r.watchers[serviceType], watcher)
    // Send initial state
    initialEvent := r.buildServiceEventLocked(serviceType)
    watcher.ch <- initialEvent
    r.mu.Unlock()  // <-- Lock released here

    // RACE WINDOW: events can be missed between unlock and reading from channel
    for {
        select {
        case event, ok := <-watcher.ch:  // May miss events
```

**Attack Scenario:**
1. Watcher registers and receives initial state
2. Lock releases
3. New registration/unregistration happens (notifies new state)
4. Watcher hasn't started reading channel yet
5. State change notification dropped (channel buffer full)

**Remediation:**
- Keep lock held until streaming loop starts, or
- Use a more robust notification mechanism with acknowledgment
- Increase channel buffer or make it unbuffered with guaranteed delivery

---

### 11. Weak Runtime ID Generation

**Severity:** LOW
**Location:** `handshake.go:195-203`

**Description:**
Runtime IDs include only a 4-character hex suffix (16 bits of entropy), making them partially predictable.

```go
// handshake.go:195-203
func generateRuntimeID(selfID string) string {
    // Generate 4-character random suffix
    suffix := generateRandomHex(4)  // Only 16 bits of entropy
    normalized := strings.ToLower(strings.ReplaceAll(selfID, " ", "-"))
    return fmt.Sprintf("%s-%s", normalized, suffix)
}
```

**Attack Scenario:**
1. Attacker knows plugin's self_id (e.g., "cache-plugin")
2. Only 65,536 possible runtime IDs
3. Attacker can enumerate and potentially guess valid runtime IDs

**Remediation:**
- Increase suffix length to at least 8 characters (32 bits)
- Consider UUIDs for runtime IDs
- Or: Use cryptographic hash of self_id + random nonce

---

### 12. Proxy Request Header Injection

**Severity:** LOW
**Location:** `router.go:149-158`

**Description:**
The proxy copies most headers from the original request, potentially allowing header injection attacks against backend services.

```go
// router.go:149-158
for key, values := range req.Header {
    canonicalKey := http.CanonicalHeaderKey(key)
    if canonicalKey == "Authorization" || canonicalKey == "X-Plugin-Runtime-Id" {
        continue  // Only these two are filtered
    }
    for _, value := range values {
        proxyReq.Header.Add(key, value)  // All other headers pass through
    }
}
```

**Attack Scenario:**
1. Caller includes malicious headers (e.g., `X-Forwarded-For`, `Host`)
2. Headers are proxied to backend service
3. Backend may trust these headers for access control or logging

**Remediation:**
- Implement header allowlist instead of blocklist
- Strip hop-by-hop headers (Connection, Keep-Alive, etc.)
- Explicitly control which headers are forwarded

---

## Race Condition Analysis

### Concurrent Registration/Unregistration

**Location:** `registry.go:158-163`

The registry uses a single mutex for all operations, but there is a potential slice corruption issue:

```go
// registry.go:158-163 - Unsafe slice manipulation
for i, p := range providers {
    if p.RegistrationID == req.Msg.RegistrationId {
        r.providers[serviceType] = append(providers[:i], providers[i+1:]...)
        break
    }
}
```

If the slice is shared (e.g., returned by `GetAllProviders`), this could cause data races despite the mutex being held.

**Remediation:**
- Ensure `GetAllProviders` returns a deep copy (it currently does via line 361)
- Add explicit documentation about thread safety guarantees
- Consider using concurrent-safe data structures

---

## Security Recommendations Summary

### Immediate (Critical/High):
1. Implement service registration authorization based on handshake declarations
2. Add rate limiting to registration and watch endpoints
3. Implement token expiration and revocation
4. Add discovery authorization based on declared dependencies

### Short-term (Medium):
5. Switch to constant-time token comparison
6. Validate metadata size and content
7. Add ownership verification to unregistration
8. Sanitize error messages

### Long-term (Low/Hardening):
9. Increase runtime ID entropy
10. Implement proxy header allowlist
11. Add comprehensive audit logging
12. Consider mTLS for plugin-to-host communication

---

## Appendix: Token Security Analysis

### Token Generation Assessment

The system uses cryptographically secure random generation:

```go
// broker.go:189-193
func generateToken() string {
    b := make([]byte, 32)
    rand.Read(b)  // crypto/rand - secure
    return base64.URLEncoding.EncodeToString(b)
}
```

**Positive:** Uses `crypto/rand` for 256 bits of entropy.

**Issues:**
- No error handling (panics on failure)
- Token format is predictable (base64-encoded)
- No token rotation mechanism

### Registration ID Assessment

```go
// registry.go:528-530
func generateRegistrationID() string {
    return "reg-" + generateRandomHex(16)  // 64 bits of entropy
}
```

**Assessment:** 64 bits is sufficient for registration IDs in typical deployments, but could be increased for highly contested environments.

---

## Conclusion

The connect-plugin-go service registry and discovery system implements basic security measures but lacks critical authorization controls. The most significant risks are:

1. **Authorization gap** - Any authenticated plugin can register/discover any service
2. **Token lifecycle** - Tokens never expire and have no revocation mechanism
3. **DoS vectors** - Unbounded resource allocation on registration/watch

These issues should be addressed before production deployment in multi-tenant or security-sensitive environments.
