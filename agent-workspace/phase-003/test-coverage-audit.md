# Test Coverage Audit: Phase 1 & Phase 2 Security Features

## Executive Summary

This audit evaluates test coverage for all security features implemented in Phase 1 and Phase 2 of the connect-plugin-go security enhancement project. The codebase has strong test coverage for core security primitives (constant-time comparison, TLS warnings, authentication) but has **critical gaps** in testing token expiration, rate limiting edge cases, service authorization, and comprehensive input validation scenarios.

**Overall Assessment:**
- **Phase 1 Features**: Good coverage (75-85%)
- **Phase 2 Features**: Moderate coverage (50-70%)
- **Critical Gaps**: 8 high-priority areas requiring immediate attention
- **Test Files Analyzed**: 9 files (security_test.go, ratelimit_test.go, auth_test.go, broker_test.go, registry_test.go, platform_test.go, router_test.go, server_test.go, client_test.go)

---

## Feature-by-Feature Analysis

### R1.2: Constant-Time Token Comparison

**Implementation Locations:**
- `handshake.go:195` - Runtime token validation (uses `subtle.ConstantTimeCompare`)
- `broker.go:196` - Capability grant token validation (uses `subtle.ConstantTimeCompare`)

**Existing Tests:**

✅ **security_test.go:**
- `TestTimingAttack_ValidateToken_ConstantTime` (lines 23-90)
  - Tests runtime token comparison with statistical timing analysis
  - Validates variance ratio across 1000 iterations
  - Tests mismatches at first, middle, and last character positions
  - **Strength**: Statistical validation with Kolmogorov-Smirnov test implementation

- `TestTimingAttack_CapabilityGrant_ConstantTime` (lines 94-148)
  - Tests capability grant token comparison
  - Validates timing distributions across different mismatch positions
  - **Strength**: Comprehensive timing analysis

- `BenchmarkTokenComparison_ValidateToken` (lines 498-508)
- `BenchmarkTokenComparison_ConstantTime` (lines 511-534)
  - Performance benchmarks for different scenarios

**Gaps:**

🔴 **CRITICAL: No Integration Tests**
- Missing: End-to-end timing attack tests through actual RPC calls
- Missing: Concurrent timing attack attempts (race conditions)
- Missing: Timing analysis during high load conditions
- Missing: Tests verifying constant-time behavior survives optimization

🟡 **Medium Priority:**
- No tests for token comparison with different lengths beyond equals check
- No tests for unicode/special characters in tokens
- No tests verifying timing behavior under memory pressure

**Recommendations:**

**HIGH PRIORITY:**
1. Add integration test simulating real-world timing attack through HTTP/RPC
2. Add concurrent timing attack test (multiple goroutines)
3. Add test verifying behavior under compiler optimizations (-gcflags='-l=4')

**MEDIUM PRIORITY:**
4. Add tests for empty token constant-time behavior
5. Add stress test (high QPS) to verify timing guarantees hold

**Example Test Needed:**
```go
// TestTimingAttack_Integration_ConstantTime should:
// 1. Start a real server with handshake endpoint
// 2. Make 10,000 requests with tokens mismatching at different positions
// 3. Analyze timing distribution over network
// 4. Verify variance ratio < 2.0 despite network jitter
```

---

### R1.3: Crypto/rand Error Handling

**Implementation Locations:**
- `handshake.go:95-125` - `generateToken()` and `generateRuntimeID()` error propagation
- `broker.go:102-138` - Grant ID and token generation with error handling
- `registry.go` - Registration ID generation
- `platform.go` - Plugin ID generation

**Existing Tests:**

✅ **security_test.go:**
- `TestCryptoErrors_GenerateToken` (lines 171-193)
  - Verifies function signature returns error type
  - Validates token format and length
  - **Weakness**: Cannot inject crypto/rand failures (by design)

- `TestCryptoErrors_GenerateRuntimeID` (lines 197-212)
  - Verifies runtime ID generation error propagation
  - Validates format

**Gaps:**

🔴 **CRITICAL: No Failure Injection Tests**
- Missing: Mock reader tests to verify actual error handling paths
- Missing: Tests verifying graceful degradation when crypto/rand fails
- Missing: Tests for error propagation through call chains
- Missing: Tests verifying no silent fallback to weak randomness

🟡 **Medium Priority:**
- No tests verifying error messages are informative
- No tests for concurrent token generation error handling
- No tests verifying cleanup on generation failure

**Recommendations:**

**HIGH PRIORITY:**
1. Add internal test package that can inject failures into crypto operations
2. Add tests verifying all crypto/rand.Read() call sites propagate errors
3. Add test verifying no silent fallback occurs on crypto failure

**MEDIUM PRIORITY:**
4. Add tests for partial read scenarios (reader returns N < requested bytes)
5. Add tests verifying error context includes operation details

**Example Test Needed:**
```go
// Requires internal testing or dependency injection
func TestCryptoErrors_PropagationChain(t *testing.T) {
    // 1. Inject crypto/rand failure at low level
    // 2. Call handshake
    // 3. Verify error propagates to top-level
    // 4. Verify no token is issued
    // 5. Verify error message mentions "crypto/rand"
}
```

---

### R1.5: TLS Warnings

**Implementation Locations:**
- `client.go:145` - Client-side TLS warning on non-HTTPS endpoints
- `server.go:160` - Server-side TLS warning on non-HTTPS endpoints
- `isNonTLSEndpoint()` - Detection logic
- `tlsWarningsDisabled()` - Suppression check via env var

**Existing Tests:**

✅ **security_test.go:**
- `TestTLSWarnings_NonTLSEndpoint` (lines 283-303)
  - Tests HTTP/HTTPS/Unix socket detection
  - Validates `isNonTLSEndpoint()` logic
  - **Coverage**: All endpoint types

- `TestTLSWarnings_Suppression` (lines 306-336)
  - Tests environment variable suppression
  - Validates case-insensitive parsing ("1", "true", "TRUE", "yes", "YES")
  - Tests negative cases ("", "0", "false")
  - **Coverage**: Comprehensive

**Gaps:**

🟡 **Medium Priority:**
- No tests verifying actual warning output to stderr/logs
- No tests for warning frequency (should only warn once per endpoint)
- No tests verifying warnings during client/server initialization
- No integration tests confirming warnings appear in real scenarios

🟢 **Low Priority:**
- No tests for exotic URL schemes (e.g., "ws://", "wss://")
- No tests for IPv6 addresses in URLs
- No tests for localhost exception (some orgs disable TLS for localhost)

**Recommendations:**

**MEDIUM PRIORITY:**
1. Add test capturing stderr output and verifying warning appears
2. Add test verifying warning only appears once (not on every request)
3. Add integration test for client/server startup warnings

**LOW PRIORITY:**
4. Add test for additional URL schemes if needed
5. Consider adding localhost TLS warning exemption (with test)

**Example Test Needed:**
```go
func TestTLSWarnings_OutputToStderr(t *testing.T) {
    // 1. Capture stderr
    // 2. Create client with http:// endpoint
    // 3. Verify warning message appears in stderr
    // 4. Verify warning includes endpoint URL
    // 5. Create second client - verify warning doesn't repeat
}
```

---

### R2.2: Token Expiration

**Implementation Locations:**
- `handshake.go:30-35` - `tokenInfo` struct with `expiresAt` field
- `handshake.go:216-229` - Runtime token validation with expiration check and lazy cleanup
- `broker.go:45-52` - `grantInfo` struct with `expiresAt` field
- `broker.go:184-189` - Capability grant validation with expiration check

**Existing Tests:**

❌ **NO TESTS FOUND**

**Gaps:**

🔴 **CRITICAL: Zero Test Coverage**
- Missing: Tests verifying tokens expire after TTL
- Missing: Tests for lazy cleanup on expired token validation
- Missing: Tests for expired grant rejection
- Missing: Tests for configurable TTL values
- Missing: Tests for clock skew handling
- Missing: Tests for concurrent expiration cleanup

🔴 **CRITICAL: Edge Cases Not Tested**
- Missing: Test for token validation exactly at expiration time
- Missing: Test for expired token attempted reuse
- Missing: Test for cleanup of multiple expired tokens
- Missing: Test for expiration during active use

**Recommendations:**

**CRITICAL (Immediate Action Required):**
1. **Add basic expiration test for runtime tokens**
2. **Add basic expiration test for capability grants**
3. **Add test for lazy cleanup mechanism**
4. **Add test for expired token rejection**

**HIGH PRIORITY:**
5. Add test for custom TTL configuration
6. Add test for concurrent access during expiration
7. Add test for time.Now() edge cases (exactly at expiry)

**MEDIUM PRIORITY:**
8. Add test for cleanup of bulk expired tokens
9. Add test for expiration metrics/logging
10. Add test for renewal/refresh behavior (if supported)

**Example Tests Needed:**

```go
func TestTokenExpiration_RuntimeToken(t *testing.T) {
    h := NewHandshakeServer(&ServeConfig{})

    // Register token with 1-second TTL
    runtimeID := "test-runtime"
    token := "test-token"
    h.mu.Lock()
    now := time.Now()
    h.tokens[runtimeID] = &tokenInfo{
        token:     token,
        issuedAt:  now,
        expiresAt: now.Add(1 * time.Second),
    }
    h.mu.Unlock()

    // Should be valid immediately
    if !h.ValidateToken(runtimeID, token) {
        t.Error("Token should be valid immediately after issue")
    }

    // Wait for expiration
    time.Sleep(1100 * time.Millisecond)

    // Should be invalid after expiration
    if h.ValidateToken(runtimeID, token) {
        t.Error("Token should be invalid after expiration")
    }

    // Verify lazy cleanup removed token
    h.mu.Lock()
    _, exists := h.tokens[runtimeID]
    h.mu.Unlock()
    if exists {
        t.Error("Expired token should be cleaned up")
    }
}

func TestTokenExpiration_CapabilityGrant(t *testing.T) {
    broker := NewCapabilityBroker("http://localhost:8080")

    // Create expired grant
    grantID := "test-grant"
    token := "test-token"
    broker.mu.Lock()
    now := time.Now()
    broker.grants[grantID] = &grantInfo{
        grantID:        grantID,
        capabilityType: "logger",
        token:          token,
        handler:        nil,
        issuedAt:       now.Add(-2 * time.Hour),
        expiresAt:      now.Add(-1 * time.Hour), // Expired 1 hour ago
    }
    broker.mu.Unlock()

    // Create request with expired grant
    req := httptest.NewRequest("POST", "/capabilities/logger/"+grantID+"/Log", nil)
    req.Header.Set("Authorization", "Bearer "+token)

    w := httptest.NewRecorder()
    broker.handleCapabilityRequest(w, req)

    // Should return 401 Unauthorized
    if w.Code != http.StatusUnauthorized {
        t.Errorf("Expected 401 for expired grant, got %d", w.Code)
    }

    // Verify grant was cleaned up
    broker.mu.Lock()
    _, exists := broker.grants[grantID]
    broker.mu.Unlock()
    if exists {
        t.Error("Expired grant should be cleaned up")
    }
}

func TestTokenExpiration_ExactlyAtExpiry(t *testing.T) {
    h := NewHandshakeServer(&ServeConfig{})

    // Use a known time for deterministic testing
    now := time.Now()
    expiry := now.Add(1 * time.Second)

    h.mu.Lock()
    h.tokens["test"] = &tokenInfo{
        token:     "token",
        issuedAt:  now,
        expiresAt: expiry,
    }
    h.mu.Unlock()

    // Sleep until exactly at expiry (within margin)
    time.Sleep(time.Until(expiry) + 10*time.Millisecond)

    // After expiry time, should be invalid
    if h.ValidateToken("test", "token") {
        t.Error("Token should be invalid at expiration time")
    }
}

func TestTokenExpiration_ConcurrentAccess(t *testing.T) {
    h := NewHandshakeServer(&ServeConfig{})

    // Register 100 tokens with staggered expiration
    for i := 0; i < 100; i++ {
        h.mu.Lock()
        h.tokens[fmt.Sprintf("runtime-%d", i)] = &tokenInfo{
            token:     fmt.Sprintf("token-%d", i),
            issuedAt:  time.Now(),
            expiresAt: time.Now().Add(time.Duration(i) * 10 * time.Millisecond),
        }
        h.mu.Unlock()
    }

    // Concurrently validate tokens
    var wg sync.WaitGroup
    errors := make(chan error, 100)

    for i := 0; i < 100; i++ {
        wg.Add(1)
        go func(id int) {
            defer wg.Done()
            runtimeID := fmt.Sprintf("runtime-%d", id)
            token := fmt.Sprintf("token-%d", id)

            // Validate multiple times
            for j := 0; j < 10; j++ {
                h.ValidateToken(runtimeID, token)
                time.Sleep(5 * time.Millisecond)
            }
        }(i)
    }

    wg.Wait()
    close(errors)

    // Should complete without panics or race conditions
    for err := range errors {
        if err != nil {
            t.Errorf("Concurrent validation error: %v", err)
        }
    }
}
```

---

### R2.1: Rate Limiting

**Implementation Locations:**
- `ratelimit.go` - `TokenBucketLimiter` implementation
- Token bucket algorithm with burst capacity and refill rate
- HTTP and Connect interceptors

**Existing Tests:**

✅ **ratelimit_test.go:**
- `TestTokenBucketLimiter_Allow` (lines 8-36)
  - Tests burst capacity (5 requests)
  - Tests request denial after burst
  - Tests token refill after wait
  - **Coverage**: Basic happy path

- `TestTokenBucketLimiter_MultipleKeys` (lines 38-58)
  - Tests independent buckets per key
  - **Coverage**: Key isolation

- `TestTokenBucketLimiter_DynamicLimits` (lines 60-88)
  - Tests limit changes at runtime
  - **Coverage**: Configuration updates

- `BenchmarkRateLimiter_Allow` (lines 90-103)
- `BenchmarkRateLimiter_MultipleKeys` (lines 105-121)

**Gaps:**

🔴 **CRITICAL: No Edge Case Tests**
- Missing: Test for zero burst capacity
- Missing: Test for zero refill rate (no refills)
- Missing: Test for very high rates (overflow protection)
- Missing: Test for negative rates (invalid input)
- Missing: Test for concurrent bucket access (race conditions)
- Missing: Test for cleanup goroutine behavior
- Missing: Test for Close() idempotency

🟡 **Medium Priority:**
- No tests for bucket cleanup after 5-minute idle period
- No tests for memory leak prevention (bucket accumulation)
- No tests for partial token consumption scenarios
- No tests for HTTP interceptor integration
- No tests for Connect interceptor integration
- No tests for key extractor functions

🟢 **Low Priority:**
- No tests for rate limiter shutdown behavior
- No tests for cleanup timing accuracy

**Recommendations:**

**HIGH PRIORITY:**
1. Add test for concurrent access to same bucket (race detector)
2. Add test for cleanup goroutine removing idle buckets
3. Add test for invalid rate limits (zero/negative)
4. Add test for Close() method behavior

**MEDIUM PRIORITY:**
5. Add integration test with HTTP handler
6. Add integration test with Connect interceptor
7. Add test for key extractor edge cases (empty runtime ID, malformed headers)
8. Add test for burst exhaustion and recovery timing

**LOW PRIORITY:**
9. Add test for very high QPS scenarios
10. Add test for memory usage under load

**Example Tests Needed:**

```go
func TestTokenBucketLimiter_ConcurrentAccess(t *testing.T) {
    limiter := NewTokenBucketLimiter()
    defer limiter.Close()

    limit := Rate{
        RequestsPerSecond: 100,
        Burst:             10,
    }

    // Hammer same key from 100 goroutines
    var wg sync.WaitGroup
    allowed := make(chan bool, 1000)

    for i := 0; i < 100; i++ {
        wg.Add(1)
        go func() {
            defer wg.Done()
            for j := 0; j < 10; j++ {
                allowed <- limiter.Allow("same-key", limit)
            }
        }()
    }

    wg.Wait()
    close(allowed)

    // Count allowed vs denied
    var allowedCount, deniedCount int
    for a := range allowed {
        if a {
            allowedCount++
        } else {
            deniedCount++
        }
    }

    // Should respect burst limit even under concurrent load
    if allowedCount > limit.Burst+100 { // +100 for refills during test
        t.Errorf("Too many requests allowed: %d (expected ~%d)", allowedCount, limit.Burst)
    }
}

func TestTokenBucketLimiter_Cleanup(t *testing.T) {
    limiter := NewTokenBucketLimiter()
    defer limiter.Close()

    limit := Rate{RequestsPerSecond: 10, Burst: 5}

    // Create 100 buckets
    for i := 0; i < 100; i++ {
        limiter.Allow(fmt.Sprintf("key-%d", i), limit)
    }

    // Verify buckets exist
    limiter.mu.RLock()
    initialCount := len(limiter.buckets)
    limiter.mu.RUnlock()
    if initialCount != 100 {
        t.Fatalf("Expected 100 buckets, got %d", initialCount)
    }

    // Wait for cleanup (runs every 1 minute, removes >5min idle)
    // Fast-forward by modifying lastRefill
    limiter.mu.Lock()
    threshold := time.Now().Add(-6 * time.Minute)
    for _, b := range limiter.buckets {
        b.mu.Lock()
        b.lastRefill = threshold
        b.mu.Unlock()
    }
    limiter.mu.Unlock()

    // Trigger cleanup
    limiter.removeOldBuckets()

    // Verify buckets removed
    limiter.mu.RLock()
    finalCount := len(limiter.buckets)
    limiter.mu.RUnlock()
    if finalCount != 0 {
        t.Errorf("Expected 0 buckets after cleanup, got %d", finalCount)
    }
}

func TestTokenBucketLimiter_InvalidRates(t *testing.T) {
    limiter := NewTokenBucketLimiter()
    defer limiter.Close()

    tests := []struct {
        name  string
        rate  Rate
        valid bool
    }{
        {"zero burst", Rate{RequestsPerSecond: 10, Burst: 0}, false},
        {"negative burst", Rate{RequestsPerSecond: 10, Burst: -5}, false},
        {"zero rate", Rate{RequestsPerSecond: 0, Burst: 10}, true}, // No refills
        {"negative rate", Rate{RequestsPerSecond: -10, Burst: 10}, false},
        {"very high rate", Rate{RequestsPerSecond: 1000000, Burst: 1000}, true},
    }

    for _, tt := range tests {
        t.Run(tt.name, func(t *testing.T) {
            // Should not panic
            allowed := limiter.Allow("test-key", tt.rate)

            if !tt.valid && allowed {
                t.Error("Invalid rate should deny request")
            }
        })
    }
}

func TestRateLimitInterceptor_Integration(t *testing.T) {
    limiter := NewTokenBucketLimiter()
    defer limiter.Close()

    limit := Rate{RequestsPerSecond: 5, Burst: 2}

    // Create interceptor
    interceptor := RateLimitInterceptor(
        limiter,
        DefaultRateLimitKeyExtractor,
        limit,
    )

    // Mock handler
    handler := func(ctx context.Context, req connect.AnyRequest) (connect.AnyResponse, error) {
        return &connect.Response[string]{}, nil
    }

    wrapped := interceptor(handler)

    // First 2 should succeed (burst)
    for i := 0; i < 2; i++ {
        req := &connect.Request[string]{}
        req.Header().Set("X-Plugin-Runtime-ID", "test-runtime")
        _, err := wrapped(context.Background(), req)
        if err != nil {
            t.Errorf("Request %d should succeed (burst), got error: %v", i+1, err)
        }
    }

    // Third should fail
    req := &connect.Request[string]{}
    req.Header().Set("X-Plugin-Runtime-ID", "test-runtime")
    _, err := wrapped(context.Background(), req)
    if err == nil {
        t.Error("Request should be rate limited")
    }
    if connect.CodeOf(err) != connect.CodeResourceExhausted {
        t.Errorf("Expected ResourceExhausted code, got %v", connect.CodeOf(err))
    }
}
```

---

### R2.3: Service Registration Authorization

**Implementation Locations:**
- `registry.go:51` - `allowedServices` map for runtime authorization
- `registry.go:92-97` - `SetAllowedServices()` method
- `registry.go:RegisterService()` - Authorization check during registration

**Existing Tests:**

❌ **NO TESTS FOUND**

**Gaps:**

🔴 **CRITICAL: Zero Test Coverage for Authorization**
- Missing: Test for allowed service registration (should succeed)
- Missing: Test for unauthorized service registration (should fail)
- Missing: Test for empty allowed list (deny all)
- Missing: Test for wildcard/all services allowed
- Missing: Test for SetAllowedServices() functionality
- Missing: Test for authorization check during handshake

🟡 **Medium Priority:**
- No tests for authorization changes at runtime
- No tests for authorization with multiple service types
- No tests for case sensitivity in service type matching
- No tests for partial matches (should not allow)

**Recommendations:**

**CRITICAL (Immediate Action Required):**
1. **Add test for successful authorized registration**
2. **Add test for rejected unauthorized registration**
3. **Add test for SetAllowedServices() updates**
4. **Add test for empty/nil allowed services list**

**HIGH PRIORITY:**
5. Add test for multiple allowed services
6. Add integration test with handshake flow
7. Add test for authorization error messages

**Example Tests Needed:**

```go
func TestServiceRegistry_Authorization_Allowed(t *testing.T) {
    registry := NewServiceRegistry(nil)

    runtimeID := "logger-plugin-xyz"

    // Set allowed services
    registry.SetAllowedServices(runtimeID, []string{"logger", "metrics"})

    // Register allowed service - should succeed
    req := connect.NewRequest(&connectpluginv1.RegisterServiceRequest{
        ServiceType:  "logger",
        Version:      "1.0.0",
        EndpointPath: "/logger.v1.Logger/",
    })
    req.Header().Set("X-Plugin-Runtime-ID", runtimeID)

    _, err := registry.RegisterService(context.Background(), req)
    if err != nil {
        t.Fatalf("Should allow authorized service registration: %v", err)
    }

    // Verify registration succeeded
    if !registry.HasService("logger", "1.0.0") {
        t.Error("Service should be registered")
    }
}

func TestServiceRegistry_Authorization_Denied(t *testing.T) {
    registry := NewServiceRegistry(nil)

    runtimeID := "cache-plugin-xyz"

    // Set allowed services (logger only)
    registry.SetAllowedServices(runtimeID, []string{"logger"})

    // Try to register unauthorized service - should fail
    req := connect.NewRequest(&connectpluginv1.RegisterServiceRequest{
        ServiceType:  "cache", // Not in allowed list
        Version:      "1.0.0",
        EndpointPath: "/cache.v1.Cache/",
    })
    req.Header().Set("X-Plugin-Runtime-ID", runtimeID)

    _, err := registry.RegisterService(context.Background(), req)
    if err == nil {
        t.Fatal("Should reject unauthorized service registration")
    }

    // Verify error code
    if connect.CodeOf(err) != connect.CodePermissionDenied {
        t.Errorf("Expected PermissionDenied code, got %v", connect.CodeOf(err))
    }

    // Verify service not registered
    if registry.HasService("cache", "1.0.0") {
        t.Error("Unauthorized service should not be registered")
    }
}

func TestServiceRegistry_Authorization_EmptyList(t *testing.T) {
    registry := NewServiceRegistry(nil)

    runtimeID := "restricted-plugin"

    // Set empty allowed list (deny all)
    registry.SetAllowedServices(runtimeID, []string{})

    // Try to register any service - should fail
    req := connect.NewRequest(&connectpluginv1.RegisterServiceRequest{
        ServiceType:  "logger",
        Version:      "1.0.0",
        EndpointPath: "/logger.v1.Logger/",
    })
    req.Header().Set("X-Plugin-Runtime-ID", runtimeID)

    _, err := registry.RegisterService(context.Background(), req)
    if err == nil {
        t.Fatal("Should reject service when allowed list is empty")
    }
}

func TestServiceRegistry_Authorization_NotSet(t *testing.T) {
    registry := NewServiceRegistry(nil)

    runtimeID := "new-plugin"

    // Don't call SetAllowedServices() - authorization not configured

    // Try to register service - behavior depends on policy
    req := connect.NewRequest(&connectpluginv1.RegisterServiceRequest{
        ServiceType:  "logger",
        Version:      "1.0.0",
        EndpointPath: "/logger.v1.Logger/",
    })
    req.Header().Set("X-Plugin-Runtime-ID", runtimeID)

    _, err := registry.RegisterService(context.Background(), req)

    // Document current behavior: should deny by default (fail-safe)
    if err == nil {
        t.Error("Should deny registration when authorization not configured (fail-safe)")
    }
}

func TestServiceRegistry_Authorization_MultipleServices(t *testing.T) {
    registry := NewServiceRegistry(nil)

    runtimeID := "multi-plugin"

    // Allow multiple service types
    registry.SetAllowedServices(runtimeID, []string{"logger", "metrics", "cache"})

    // Register each allowed type
    for _, serviceType := range []string{"logger", "metrics", "cache"} {
        req := connect.NewRequest(&connectpluginv1.RegisterServiceRequest{
            ServiceType:  serviceType,
            Version:      "1.0.0",
            EndpointPath: fmt.Sprintf("/%s.v1.%s/", serviceType, strings.Title(serviceType)),
        })
        req.Header().Set("X-Plugin-Runtime-ID", runtimeID)

        _, err := registry.RegisterService(context.Background(), req)
        if err != nil {
            t.Errorf("Should allow %s registration: %v", serviceType, err)
        }
    }

    // Try unauthorized type
    req := connect.NewRequest(&connectpluginv1.RegisterServiceRequest{
        ServiceType:  "database", // Not allowed
        Version:      "1.0.0",
        EndpointPath: "/database.v1.Database/",
    })
    req.Header().Set("X-Plugin-Runtime-ID", runtimeID)

    _, err := registry.RegisterService(context.Background(), req)
    if err == nil {
        t.Error("Should reject unauthorized service type")
    }
}
```

---

### R2.4: Input Validation

**Implementation Locations:**
- `validation.go` - Comprehensive validation functions
  - `ValidateMetadata()` - Validates metadata maps
  - `ValidateServiceType()` - Validates service type names
  - `ValidateSelfID()` - Validates plugin self IDs
  - `ValidateVersion()` - Validates version strings
  - `ValidateEndpointPath()` - Validates endpoint paths

**Existing Tests:**

❌ **NO TESTS FOUND**

**Critical Note:** The `validation.go` file contains extensive validation logic (181 lines) but has **zero test coverage**.

**Gaps:**

🔴 **CRITICAL: Zero Test Coverage for All Validation Functions**

**Missing Tests for ValidateMetadata():**
- Test for valid metadata (should succeed)
- Test for too many entries (>100)
- Test for empty key
- Test for key too long (>256 bytes)
- Test for invalid key characters
- Test for value too long (>4096 bytes)
- Test for null bytes in key
- Test for null bytes in value
- Test for valid special characters in keys

**Missing Tests for ValidateServiceType():**
- Test for valid service types
- Test for empty service type
- Test for too long (>128 bytes)
- Test for invalid characters
- Test for path traversal ("../", "..", "/", "\\")
- Test for null bytes
- Test for uppercase/lowercase/mixed case

**Missing Tests for ValidateSelfID():**
- Test for valid self IDs
- Test for empty self ID
- Test for too long (>128 bytes)
- Test for invalid characters
- Test for null bytes
- Test for special characters

**Missing Tests for ValidateVersion():**
- Test for valid semver ("1.0.0", "2.1.3-beta", "3.0.0-rc.1")
- Test for invalid versions ("1.0", "v1.0.0", "1.0.0.0")
- Test for empty version
- Test for too long (>64 bytes)
- Test for null bytes

**Missing Tests for ValidateEndpointPath():**
- Test for valid paths ("/logger.v1.Logger/")
- Test for empty path
- Test for missing leading slash
- Test for too long (>256 bytes)
- Test for null bytes
- Test for special characters

**Recommendations:**

**CRITICAL (Immediate Action Required):**
1. **Create validation_test.go with comprehensive tests for all functions**
2. **Add at least 50+ test cases covering all validation functions**
3. **Test all documented constraints (length limits, character restrictions)**
4. **Test security-critical checks (null bytes, path traversal)**

**HIGH PRIORITY:**
5. Add fuzzing tests for all validation functions
6. Add table-driven tests for edge cases
7. Add tests for Unicode handling

**Example Tests Needed:**

```go
// validation_test.go - NEW FILE REQUIRED

func TestValidateMetadata_Valid(t *testing.T) {
    tests := []map[string]string{
        {},
        {"key1": "value1"},
        {"provider": "logger-impl", "version": "1.0.0"},
        {"a.b-c_d": "valid-chars"},
    }

    for _, metadata := range tests {
        if err := ValidateMetadata(metadata); err != nil {
            t.Errorf("ValidateMetadata(%v) should succeed: %v", metadata, err)
        }
    }
}

func TestValidateMetadata_TooManyEntries(t *testing.T) {
    metadata := make(map[string]string)
    for i := 0; i < 101; i++ {
        metadata[fmt.Sprintf("key%d", i)] = "value"
    }

    err := ValidateMetadata(metadata)
    if err == nil {
        t.Error("Should reject >100 metadata entries")
    }
    if !strings.Contains(err.Error(), "too many metadata entries") {
        t.Errorf("Error should mention too many entries: %v", err)
    }
}

func TestValidateMetadata_EmptyKey(t *testing.T) {
    metadata := map[string]string{"": "value"}

    err := ValidateMetadata(metadata)
    if err == nil {
        t.Error("Should reject empty key")
    }
}

func TestValidateMetadata_KeyTooLong(t *testing.T) {
    longKey := strings.Repeat("a", 257)
    metadata := map[string]string{longKey: "value"}

    err := ValidateMetadata(metadata)
    if err == nil {
        t.Error("Should reject key >256 bytes")
    }
}

func TestValidateMetadata_InvalidKeyChars(t *testing.T) {
    tests := []string{
        "key with spaces",
        "key@special",
        "key#hash",
        "key$dollar",
        "123startswithnumber",
        "key\x00null",
    }

    for _, key := range tests {
        metadata := map[string]string{key: "value"}
        if err := ValidateMetadata(metadata); err == nil {
            t.Errorf("Should reject invalid key: %q", key)
        }
    }
}

func TestValidateMetadata_ValueTooLong(t *testing.T) {
    longValue := strings.Repeat("a", 4097)
    metadata := map[string]string{"key": longValue}

    err := ValidateMetadata(metadata)
    if err == nil {
        t.Error("Should reject value >4096 bytes")
    }
}

func TestValidateMetadata_NullBytes(t *testing.T) {
    tests := []map[string]string{
        {"key\x00null": "value"},
        {"key": "value\x00null"},
    }

    for _, metadata := range tests {
        if err := ValidateMetadata(metadata); err == nil {
            t.Errorf("Should reject null bytes: %v", metadata)
        }
    }
}

func TestValidateServiceType_Valid(t *testing.T) {
    tests := []string{
        "logger",
        "cache",
        "logger.v1",
        "my-service",
        "my_service",
        "Service123",
    }

    for _, serviceType := range tests {
        if err := ValidateServiceType(serviceType); err != nil {
            t.Errorf("ValidateServiceType(%q) should succeed: %v", serviceType, err)
        }
    }
}

func TestValidateServiceType_PathTraversal(t *testing.T) {
    tests := []string{
        "../logger",
        "logger/../cache",
        "logger/..",
        "logger/cache",
        "logger\\cache",
        "..",
    }

    for _, serviceType := range tests {
        if err := ValidateServiceType(serviceType); err == nil {
            t.Errorf("Should reject path traversal: %q", serviceType)
        }
    }
}

func TestValidateServiceType_TooLong(t *testing.T) {
    longType := strings.Repeat("a", 129)

    err := ValidateServiceType(longType)
    if err == nil {
        t.Error("Should reject service type >128 bytes")
    }
}

func TestValidateSelfID_Valid(t *testing.T) {
    tests := []string{
        "my-plugin",
        "MyPlugin",
        "plugin123",
        "my.plugin-v2",
    }

    for _, selfID := range tests {
        if err := ValidateSelfID(selfID); err != nil {
            t.Errorf("ValidateSelfID(%q) should succeed: %v", selfID, err)
        }
    }
}

func TestValidateVersion_Valid(t *testing.T) {
    tests := []string{
        "1.0.0",
        "2.1.3",
        "10.20.30",
        "1.0.0-beta",
        "2.0.0-rc.1",
        "3.0.0-alpha.1.2",
    }

    for _, version := range tests {
        if err := ValidateVersion(version); err != nil {
            t.Errorf("ValidateVersion(%q) should succeed: %v", version, err)
        }
    }
}

func TestValidateVersion_Invalid(t *testing.T) {
    tests := []string{
        "1.0",        // Missing patch
        "v1.0.0",     // Has 'v' prefix
        "1.0.0.0",    // Four components
        "1",          // Just major
        "1.0.0-",     // Empty prerelease
        "1.0.0--beta", // Double dash
    }

    for _, version := range tests {
        if err := ValidateVersion(version); err == nil {
            t.Errorf("Should reject invalid version: %q", version)
        }
    }
}

func TestValidateEndpointPath_Valid(t *testing.T) {
    tests := []string{
        "/logger.v1.Logger/",
        "/cache.v1.Cache/",
        "/my.service.v2.API/Method",
    }

    for _, path := range tests {
        if err := ValidateEndpointPath(path); err != nil {
            t.Errorf("ValidateEndpointPath(%q) should succeed: %v", path, err)
        }
    }
}

func TestValidateEndpointPath_Invalid(t *testing.T) {
    tests := []struct {
        path string
        desc string
    }{
        {"", "empty path"},
        {"logger.v1.Logger/", "missing leading slash"},
        {strings.Repeat("/a", 129), "too long"},
        {"/path\x00null", "null bytes"},
    }

    for _, tt := range tests {
        if err := ValidateEndpointPath(tt.path); err == nil {
            t.Errorf("Should reject %s: %q", tt.desc, tt.path)
        }
    }
}

// Fuzzing tests
func FuzzValidateMetadata(f *testing.F) {
    f.Add("key", "value")
    f.Fuzz(func(t *testing.T, key, value string) {
        metadata := map[string]string{key: value}
        ValidateMetadata(metadata)
        // Should not panic
    })
}

func FuzzValidateServiceType(f *testing.F) {
    f.Add("logger")
    f.Fuzz(func(t *testing.T, serviceType string) {
        ValidateServiceType(serviceType)
        // Should not panic
    })
}
```

---

### Additional Security Test Gaps

**Missing Integration Tests:**

🔴 **CRITICAL:**
1. No end-to-end security test combining all features
2. No test for complete handshake-to-RPC flow with all security checks
3. No test for attack scenarios (replay, MITM simulation, privilege escalation)
4. No test for security under high concurrency
5. No test for security during error conditions

**Example Integration Test Needed:**

```go
func TestSecurity_EndToEnd_FullFlow(t *testing.T) {
    // 1. Start secure host with all features enabled
    // 2. Plugin handshake with token generation
    // 3. Plugin registers service with authorization
    // 4. Plugin requests capability with grant
    // 5. Another plugin discovers and calls service
    // 6. Verify all security checks: tokens, rate limits, authorization
    // 7. Attempt unauthorized access - should fail
    // 8. Wait for token expiration - should fail
    // 9. Verify all operations logged/audited
}
```

---

## Priority Recommendations

### Critical (Fix Immediately)

1. **Token Expiration Tests** (R2.2)
   - Add 4 basic tests for runtime and grant expiration
   - Estimated effort: 4 hours

2. **Service Authorization Tests** (R2.3)
   - Add 5 tests for authorization logic
   - Estimated effort: 3 hours

3. **Input Validation Tests** (R2.4)
   - Create validation_test.go with 50+ tests
   - Estimated effort: 8 hours

4. **Rate Limiter Edge Cases** (R2.1)
   - Add concurrent access and cleanup tests
   - Estimated effort: 4 hours

**Total Critical Work: ~19 hours**

### High Priority (Complete Within Sprint)

5. **Constant-Time Integration Tests** (R1.2)
   - Add end-to-end timing attack simulation
   - Estimated effort: 6 hours

6. **Crypto Error Injection Tests** (R1.3)
   - Add internal test package for failure injection
   - Estimated effort: 6 hours

7. **Rate Limiter Integration Tests** (R2.1)
   - Add HTTP and Connect interceptor tests
   - Estimated effort: 4 hours

**Total High Priority Work: ~16 hours**

### Medium Priority (Next Sprint)

8. **TLS Warning Output Tests** (R1.5)
   - Capture and verify stderr warnings
   - Estimated effort: 2 hours

9. **Validation Fuzzing** (R2.4)
   - Add fuzzing tests for all validators
   - Estimated effort: 4 hours

10. **End-to-End Security Integration Test**
    - Complete attack simulation test
    - Estimated effort: 8 hours

**Total Medium Priority Work: ~14 hours**

### Low Priority (Backlog)

11. Additional edge cases for all features
12. Performance regression tests
13. Stress tests under extreme load

---

## Test Metrics

### Current Coverage Estimate

**Phase 1 Features:**
- R1.2 (Constant-Time): 80% unit, 0% integration = **75% overall**
- R1.3 (Crypto Errors): 30% unit (no failure injection) = **30% overall**
- R1.5 (TLS Warnings): 90% unit, 0% output verification = **85% overall**

**Phase 2 Features:**
- R2.1 (Rate Limiting): 60% unit, 0% integration = **50% overall**
- R2.2 (Token Expiration): **0% overall** ⚠️
- R2.3 (Authorization): **0% overall** ⚠️
- R2.4 (Input Validation): **0% overall** ⚠️

**Overall Security Feature Coverage: ~46%** (Critical gap!)

### Target Coverage

- **Unit Test Coverage**: 90%+ for all security features
- **Integration Test Coverage**: 80%+ for critical paths
- **Edge Case Coverage**: 95%+ for security-critical functions
- **Fuzzing Coverage**: 100% for all input validation

### Suggested Coverage Milestones

**Week 1:**
- Add critical tests (R2.2, R2.3, R2.4)
- Target: 70% overall coverage

**Week 2:**
- Add high-priority tests (R1.2, R1.3, R2.1)
- Target: 85% overall coverage

**Week 3:**
- Add integration and fuzzing tests
- Target: 95% overall coverage

---

## Conclusion

The connect-plugin-go security implementation has **strong foundations** for Phase 1 features (constant-time comparison, TLS warnings) but has **critical gaps** in Phase 2 feature testing (token expiration, authorization, validation). The most urgent needs are:

1. ✅ **Immediate**: Add token expiration tests (currently 0%)
2. ✅ **Immediate**: Add service authorization tests (currently 0%)
3. ✅ **Immediate**: Add input validation tests (currently 0%)
4. 🔄 **Soon**: Add rate limiter edge case tests
5. 🔄 **Soon**: Add integration tests for all features

**Recommended Next Step:** Create a task list from the "Priority Recommendations" section and allocate 1-2 engineers for 2 weeks to close the critical gaps.
