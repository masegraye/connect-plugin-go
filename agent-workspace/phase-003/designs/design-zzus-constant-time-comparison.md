# Design: Constant-Time Token Comparison

## Problem Statement

**Finding:** AUTH-HIGH-003
**Severity:** MEDIUM

The codebase uses standard string comparison (`==` and `!=`) for security-sensitive token validation, making it vulnerable to timing attacks. An attacker can exploit the timing differences in string comparison to discover valid tokens character-by-character.

### Current Vulnerability

Standard string comparison in Go short-circuits on the first differing character, revealing information about token validity through timing side-channels:

- Comparing `"abcd1234"` vs `"wxyz5678"` fails immediately (fastest)
- Comparing `"abcd1234"` vs `"abcd5678"` fails after 4 matches (slower)
- Comparing `"abcd1234"` vs `"abcd1234"` succeeds (slowest)

By measuring response times across many attempts, an attacker can progressively guess each character of a valid token, significantly reducing the search space from O(256^n) to O(256*n) for n-byte tokens.

### Impact

**Affected Security Boundaries:**
1. **Runtime Identity Validation** - Plugin authentication tokens
2. **Capability Grants** - Bearer tokens for host capabilities
3. **Service Routing** - Authorization tokens for plugin-to-plugin calls

**Attack Scenario:**
1. Attacker measures response times for token validation endpoints
2. Statistical analysis reveals timing differences (even over network)
3. Progressive token discovery reduces 256-bit token space to ~8,192 attempts
4. Attacker gains unauthorized access by forging valid tokens

**Mitigating Factors:**
- 256-bit tokens provide substantial entropy (32 bytes)
- Network jitter makes timing attacks harder (but not impossible)
- TLS is recommended (though not enforced)

**Risk Assessment:** MEDIUM severity
- High impact if exploited (complete authentication bypass)
- Moderate exploitability (requires statistical timing analysis)
- Partially mitigated by network conditions and token entropy

## Token Comparison Sites

Through comprehensive codebase analysis, **3 vulnerable comparison sites** were identified:

### Site 1: Runtime Token Validation (handshake.go)

**Location:** `handshake.go:189`
**Function:** `HandshakeServer.ValidateToken()`

```go
// handshake.go:178-190
func (h *HandshakeServer) ValidateToken(runtimeID, token string) bool {
	h.mu.RLock()
	defer h.mu.RUnlock()

	expectedToken, ok := h.tokens[runtimeID]
	if !ok {
		return false
	}

	return expectedToken == token  // VULNERABLE: Timing attack
}
```

**What tokens are compared:**
- Runtime tokens generated during plugin handshake (256 bits)
- Format: base64-encoded random bytes (e.g., `"Yj8s7K3mN9pQ2rT5vX8z..."`)

**Impact of timing leak:**
- Attacker can impersonate any plugin with known `runtimeID`
- Enables unauthorized service registration and discovery
- Allows access to capability broker and other plugins' services

**String length side-channel:**
- Both tokens are always 44 characters (32 bytes base64-encoded)
- No length variation: length check optimization not needed

### Site 2: Capability Grant Validation (broker.go)

**Location:** `broker.go:164`
**Function:** `CapabilityBroker.handleCapabilityRequest()`

```go
// broker.go:136-167
func (b *CapabilityBroker) handleCapabilityRequest(w http.ResponseWriter, r *http.Request) {
	// ... path parsing ...

	// Extract bearer token
	auth := r.Header.Get("Authorization")
	if !strings.HasPrefix(auth, "Bearer ") {
		http.Error(w, "missing or invalid authorization header", http.StatusUnauthorized)
		return
	}
	token := strings.TrimPrefix(auth, "Bearer ")

	// Validate grant
	b.mu.RLock()
	grant, ok := b.grants[grantID]
	b.mu.RUnlock()

	if !ok {
		http.Error(w, "invalid grant ID", http.StatusUnauthorized)
		return
	}

	if grant.token != token {  // VULNERABLE: Timing attack
		http.Error(w, "invalid token", http.StatusUnauthorized)
		return
	}

	// ... route to capability handler ...
}
```

**What tokens are compared:**
- Capability grant bearer tokens (256 bits)
- Format: base64-encoded random bytes

**Impact of timing leak:**
- Attacker can access host capabilities without authorization
- Scope depends on capabilities (logging, secrets, storage, etc.)
- Could enable privilege escalation or data exfiltration

**String length side-channel:**
- Both tokens are always 44 characters (32 bytes base64-encoded)
- No length variation: length check optimization not needed

### Site 3: Test Code Comparisons (auth_test.go)

**Locations:** `auth_test.go:119`, `auth_test.go:162`, `auth_test.go:320`

```go
// auth_test.go:118-123
validateToken := func(token string) (string, map[string]string, error) {
	if token == "valid-token" {  // Test code - acceptable
		return "user-789", map[string]string{"role": "admin"}, nil
	}
	return "", nil, errors.New("invalid token")
}

// auth_test.go:161-166
validateToken := func(token string) (string, map[string]string, error) {
	if token == "valid-token" {  // Test code - acceptable
		return "user-789", nil, nil
	}
	return "", nil, errors.New("invalid token")
}

// auth_test.go:319-324
provider1 := NewTokenAuth("", func(token string) (string, map[string]string, error) {
	if token == "token-1" {  // Test code - acceptable
		return "user-from-provider1", nil, nil
	}
	return "", nil, errors.New("invalid")
})
```

**Impact:** NONE (test code only)
- These comparisons are in test fixtures, not production code
- Test tokens like `"valid-token"` and `"token-1"` are not security-sensitive
- No timing attack risk in test execution

**Recommendation:** No changes required for test code, but could add a comment explaining why constant-time comparison is NOT needed in tests.

## Summary of Vulnerable Sites

| Location | Function | Token Type | Impact | Priority |
|----------|----------|------------|--------|----------|
| `handshake.go:189` | `ValidateToken()` | Runtime tokens | Plugin impersonation | **HIGH** |
| `broker.go:164` | `handleCapabilityRequest()` | Capability grants | Capability access | **HIGH** |
| `auth_test.go` (3 sites) | Test fixtures | Test tokens | None (test code) | None |

**Total vulnerable sites requiring fixes: 2**

## Proposed Solution

Use `crypto/subtle.ConstantTimeCompare` for all security-sensitive token comparisons. This function performs byte-by-byte comparison without short-circuiting, ensuring constant execution time regardless of token similarity.

### Why crypto/subtle.ConstantTimeCompare?

**Standard Library Function:**
```go
package subtle

// ConstantTimeCompare returns 1 if x and y have equal contents and 0 otherwise.
// The time taken is a function of the length of the slices and is independent of
// the contents. NOTE: This function will panic if the lengths differ.
func ConstantTimeCompare(x, y []byte) int
```

**Security Properties:**
1. **Constant-time execution:** Compares all bytes regardless of differences
2. **No short-circuiting:** Timing independent of token similarity
3. **Side-channel resistant:** Designed specifically for cryptographic use
4. **Standard library:** Well-tested, audited implementation

**Limitations:**
- Requires equal-length byte slices (panics on length mismatch)
- Returns `int` (1 for match, 0 for mismatch) instead of `bool`
- Byte slice conversion overhead for strings (negligible)

### Alternative Approaches Considered

**1. `bytes.Equal()` - REJECTED**
```go
return bytes.Equal([]byte(expectedToken), []byte(token))
```
- **Problem:** Not constant-time, subject to compiler optimizations
- **Risk:** May short-circuit in future Go versions

**2. `strings.Compare()` - REJECTED**
```go
return strings.Compare(expectedToken, token) == 0
```
- **Problem:** Lexicographic comparison, not timing-safe
- **Risk:** Explicitly documented as non-constant-time

**3. Manual byte-by-byte comparison - REJECTED**
```go
if len(a) != len(b) { return false }
result := 0
for i := 0; i < len(a); i++ {
    result |= int(a[i]) ^ int(b[i])
}
return result == 0
```
- **Problem:** Reinvents the wheel, prone to implementation errors
- **Risk:** Compiler optimizations may defeat timing guarantees

**4. `crypto/subtle.ConstantTimeCompare()` - ACCEPTED**
- ✅ Purpose-built for security-sensitive comparisons
- ✅ Guaranteed constant-time by design
- ✅ Standard library with security guarantees
- ✅ Well-tested and audited

## String Length Side-Channel

### Analysis

**Potential vulnerability:** If tokens have variable lengths, an attacker can:
1. Determine expected token length by timing `len(a) != len(b)` check
2. Reduce search space to tokens of correct length
3. Bypass brute-force protections based on entropy alone

**Current state in codebase:**

**Site 1: Runtime Tokens (handshake.go)**
```go
// broker.go:189-193
func generateToken() string {
	b := make([]byte, 32)  // Always 32 bytes
	rand.Read(b)
	return base64.URLEncoding.EncodeToString(b)  // Always 44 chars
}
```
- ✅ **No length side-channel:** All tokens are exactly 32 bytes (44 base64 chars)

**Site 2: Capability Grants (broker.go)**
```go
// broker.go:189-193 (same function)
func generateToken() string {
	b := make([]byte, 32)  // Always 32 bytes
	rand.Read(b)
	return base64.URLEncoding.EncodeToString(b)  // Always 44 chars
}
```
- ✅ **No length side-channel:** All tokens are exactly 32 bytes (44 base64 chars)

### Length Check Handling

Since all tokens are fixed-length, we have two options:

**Option A: Skip length check (rely on ConstantTimeCompare panic)**
```go
// If lengths differ, ConstantTimeCompare will panic
// This is acceptable for internal use where we control token generation
return subtle.ConstantTimeCompare([]byte(expectedToken), []byte(token)) == 1
```

**Pros:** Simpler code, one less branch
**Cons:** Panic on programmer error, not graceful

**Option B: Explicit length check (timing-safe pattern)**
```go
// Check length first (this reveals length, but all our tokens are same length)
if len(expectedToken) != len(token) {
	return false
}
return subtle.ConstantTimeCompare([]byte(expectedToken), []byte(token)) == 1
```

**Pros:** Graceful handling, defensive programming
**Cons:** Extra branch, minimal overhead

### Recommendation: Option B (Explicit Length Check)

**Rationale:**
1. **Defensive programming:** Protects against future changes to token generation
2. **Graceful failure:** Returns `false` instead of panic on unexpected input
3. **No security cost:** Length is already public (all tokens are 44 chars)
4. **Documentation value:** Makes fixed-length assumption explicit

**Note:** If token lengths varied, we would need constant-time length comparison:
```go
// Hypothetical variable-length scenario (NOT our case)
maxLen := max(len(a), len(b))
aPadded := make([]byte, maxLen)
bPadded := make([]byte, maxLen)
copy(aPadded, a)
copy(bPadded, b)
match := subtle.ConstantTimeCompare(aPadded, bPadded) == 1
lengthMatch := subtle.ConstantTimeEq(int32(len(a)), int32(len(b)))
return match && lengthMatch == 1
```

**However, our tokens are fixed-length, so this complexity is unnecessary.**

## Implementation Details

### Site 1: Runtime Token Validation (handshake.go)

**Before (vulnerable):**
```go
// handshake.go:178-190
func (h *HandshakeServer) ValidateToken(runtimeID, token string) bool {
	h.mu.RLock()
	defer h.mu.RUnlock()

	expectedToken, ok := h.tokens[runtimeID]
	if !ok {
		return false
	}

	return expectedToken == token  // VULNERABLE
}
```

**After (secure):**
```go
// handshake.go:178-195
func (h *HandshakeServer) ValidateToken(runtimeID, token string) bool {
	h.mu.RLock()
	defer h.mu.RUnlock()

	expectedToken, ok := h.tokens[runtimeID]
	if !ok {
		return false
	}

	// Use constant-time comparison to prevent timing attacks
	// All runtime tokens are 44 characters (32 bytes base64-encoded)
	if len(expectedToken) != len(token) {
		return false
	}

	return subtle.ConstantTimeCompare([]byte(expectedToken), []byte(token)) == 1
}
```

**Changes:**
1. Add `import "crypto/subtle"` at top of file
2. Add length check before comparison
3. Replace `==` with `subtle.ConstantTimeCompare()`
4. Convert strings to byte slices
5. Compare result to `1` (match indicator)

**Backward compatibility:** Fully compatible, no API changes

### Site 2: Capability Grant Validation (broker.go)

**Before (vulnerable):**
```go
// broker.go:136-167
func (b *CapabilityBroker) handleCapabilityRequest(w http.ResponseWriter, r *http.Request) {
	// ... parsing and grant lookup ...

	if grant.token != token {  // VULNERABLE
		http.Error(w, "invalid token", http.StatusUnauthorized)
		return
	}

	// ... route to handler ...
}
```

**After (secure):**
```go
// broker.go:136-172
func (b *CapabilityBroker) handleCapabilityRequest(w http.ResponseWriter, r *http.Request) {
	// ... parsing and grant lookup ...

	// Use constant-time comparison to prevent timing attacks
	// All capability tokens are 44 characters (32 bytes base64-encoded)
	if len(grant.token) != len(token) {
		http.Error(w, "invalid token", http.StatusUnauthorized)
		return
	}

	if subtle.ConstantTimeCompare([]byte(grant.token), []byte(token)) != 1 {
		http.Error(w, "invalid token", http.StatusUnauthorized)
		return
	}

	// ... route to handler ...
}
```

**Changes:**
1. Add `import "crypto/subtle"` at top of file
2. Add length check before comparison
3. Replace `!=` with `subtle.ConstantTimeCompare() != 1`
4. Convert strings to byte slices
5. Maintain same error response for both length and content mismatch

**Backward compatibility:** Fully compatible, no API changes

### Import Statement

Both files need to add the import:

```go
import (
	"context"
	"crypto/rand"
	"crypto/subtle"  // ADD THIS
	"encoding/base64"
	// ... other imports ...
)
```

### Helper Function (Optional Enhancement)

For consistency and reusability, consider adding a helper function:

```go
// secureCompareTokens performs constant-time comparison of two tokens.
// Returns true if tokens match, false otherwise.
// Safe against timing attacks.
func secureCompareTokens(expected, actual string) bool {
	// Check length first (all our tokens are fixed-length)
	if len(expected) != len(actual) {
		return false
	}

	// Constant-time comparison
	return subtle.ConstantTimeCompare([]byte(expected), []byte(actual)) == 1
}
```

**Usage:**
```go
// handshake.go
return secureCompareTokens(expectedToken, token)

// broker.go
if !secureCompareTokens(grant.token, token) {
	http.Error(w, "invalid token", http.StatusUnauthorized)
	return
}
```

**Recommendation:** Inline the comparison for now (2 sites only), but if more comparison sites are added in future, refactor to helper function.

## Test Strategy

### 1. Correctness Tests

**Objective:** Verify constant-time comparison produces correct results

**Test Cases:**
```go
func TestValidateToken_ConstantTimeComparison(t *testing.T) {
	server := NewHandshakeServer(&ServeConfig{})

	// Store a test token
	server.mu.Lock()
	server.tokens["runtime-abc"] = "YjhzN0szTTlwUTJyVDV2WDh6"  // Valid token
	server.mu.Unlock()

	tests := []struct {
		name      string
		runtimeID string
		token     string
		expected  bool
	}{
		{
			name:      "exact match",
			runtimeID: "runtime-abc",
			token:     "YjhzN0szTTlwUTJyVDV2WDh6",
			expected:  true,
		},
		{
			name:      "different first char",
			runtimeID: "runtime-abc",
			token:     "XjhzN0szTTlwUTJyVDV2WDh6",
			expected:  false,
		},
		{
			name:      "different middle char",
			runtimeID: "runtime-abc",
			token:     "YjhzN0szTTlwUTJyVDVYWDh6",
			expected:  false,
		},
		{
			name:      "different last char",
			runtimeID: "runtime-abc",
			token:     "YjhzN0szTTlwUTJyVDV2WDha",
			expected:  false,
		},
		{
			name:      "wrong length (shorter)",
			runtimeID: "runtime-abc",
			token:     "YjhzN0szTTlwUQ==",
			expected:  false,
		},
		{
			name:      "wrong length (longer)",
			runtimeID: "runtime-abc",
			token:     "YjhzN0szTTlwUTJyVDV2WDh6EXTRA",
			expected:  false,
		},
		{
			name:      "empty token",
			runtimeID: "runtime-abc",
			token:     "",
			expected:  false,
		},
		{
			name:      "unknown runtime ID",
			runtimeID: "runtime-unknown",
			token:     "YjhzN0szTTlwUTJyVDV2WDh6",
			expected:  false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := server.ValidateToken(tt.runtimeID, tt.token)
			if result != tt.expected {
				t.Errorf("ValidateToken() = %v, expected %v", result, tt.expected)
			}
		})
	}
}
```

**Similar test for broker.go capability grant validation**

### 2. Statistical Timing Tests

**Objective:** Verify timing is independent of token similarity

**Challenge:** Timing tests are inherently flaky (system load, GC, etc.)

**Approach:** Statistical analysis with many iterations

```go
func TestValidateToken_TimingIndependence(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping timing test in short mode")
	}

	server := NewHandshakeServer(&ServeConfig{})
	runtimeID := "runtime-test"
	validToken := "YjhzN0szTTlwUTJyVDV2WDh6ABCDEFGH"  // 44 chars

	server.mu.Lock()
	server.tokens[runtimeID] = validToken
	server.mu.Unlock()

	// Test cases with varying similarity to valid token
	testCases := []struct {
		name  string
		token string
		desc  string
	}{
		{
			name:  "all_different",
			token: "ZZZZZZZZZZZZZZZZZZZZZZZZZZZZZZZZZZZZZZZZZZZZ",
			desc:  "No matching characters",
		},
		{
			name:  "half_match",
			token: "YjhzN0szTTlwUTJyVDV2WDh6ZZZZZZZZZZZZ",
			desc:  "First 24 chars match",
		},
		{
			name:  "almost_match",
			token: "YjhzN0szTTlwUTJyVDV2WDh6ABCDEFGZ",
			desc:  "All but last char match",
		},
		{
			name:  "exact_match",
			token: validToken,
			desc:  "Exact match",
		},
	}

	const iterations = 10000
	timings := make(map[string][]time.Duration)

	// Measure timing for each test case
	for _, tc := range testCases {
		timings[tc.name] = make([]time.Duration, iterations)

		for i := 0; i < iterations; i++ {
			start := time.Now()
			server.ValidateToken(runtimeID, tc.token)
			timings[tc.name][i] = time.Since(start)
		}
	}

	// Calculate statistics
	stats := make(map[string]struct{
		mean   time.Duration
		median time.Duration
		stddev time.Duration
	})

	for name, durations := range timings {
		sort.Slice(durations, func(i, j int) bool {
			return durations[i] < durations[j]
		})

		var sum time.Duration
		for _, d := range durations {
			sum += d
		}
		mean := sum / time.Duration(len(durations))
		median := durations[len(durations)/2]

		// Calculate standard deviation
		var variance float64
		for _, d := range durations {
			diff := float64(d - mean)
			variance += diff * diff
		}
		stddev := time.Duration(math.Sqrt(variance / float64(len(durations))))

		stats[name] = struct{
			mean   time.Duration
			median time.Duration
			stddev time.Duration
		}{mean, median, stddev}
	}

	// Print results for manual inspection
	t.Logf("Timing statistics (%d iterations):", iterations)
	for _, tc := range testCases {
		s := stats[tc.name]
		t.Logf("  %s (%s):", tc.name, tc.desc)
		t.Logf("    Mean:   %v", s.mean)
		t.Logf("    Median: %v", s.median)
		t.Logf("    StdDev: %v", s.stddev)
	}

	// Statistical test: All means should be within 10% of each other
	// Note: This is a weak test due to system noise, but catches obvious issues
	var allMeans []time.Duration
	for _, s := range stats {
		allMeans = append(allMeans, s.mean)
	}

	minMean := allMeans[0]
	maxMean := allMeans[0]
	for _, m := range allMeans {
		if m < minMean {
			minMean = m
		}
		if m > maxMean {
			maxMean = m
		}
	}

	variationPct := float64(maxMean-minMean) / float64(minMean) * 100
	t.Logf("Timing variation: %.2f%%", variationPct)

	// Allow up to 20% variation (generous due to system noise)
	if variationPct > 20.0 {
		t.Errorf("Timing variation %.2f%% exceeds 20%% threshold", variationPct)
		t.Error("This may indicate timing attack vulnerability")
	}
}
```

**Note:** This test is best-effort. True timing attack resistance requires:
1. Dedicated timing analysis tools
2. Isolated test environment
3. Statistical significance testing
4. Multiple runs across different systems

**Recommendation:** Run timing tests manually during development, not in CI (too flaky)

### 3. Benchmark Comparison

**Objective:** Measure performance overhead of constant-time comparison

```go
func BenchmarkValidateToken_StandardComparison(b *testing.B) {
	// Baseline: old vulnerable implementation
	expectedToken := "YjhzN0szTTlwUTJyVDV2WDh6ABCDEFGH"
	token := "YjhzN0szTTlwUTJyVDV2WDh6ABCDEFGH"

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = expectedToken == token
	}
}

func BenchmarkValidateToken_ConstantTimeComparison(b *testing.B) {
	// New: secure constant-time implementation
	expectedToken := []byte("YjhzN0szTTlwUTJyVDV2WDh6ABCDEFGH")
	token := []byte("YjhzN0szTTlwUTJyVDV2WDh6ABCDEFGH")

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = subtle.ConstantTimeCompare(expectedToken, token) == 1
	}
}
```

**Expected results:**
- Standard comparison: ~0.5-2 ns/op
- Constant-time comparison: ~5-20 ns/op (depends on token length)
- Overhead: ~10-15ns per comparison (negligible for auth operations)

### 4. Integration Tests

**Objective:** Verify end-to-end token validation in realistic scenarios

```go
func TestHandshake_RuntimeTokenValidation(t *testing.T) {
	// Create server with handshake
	cfg := &ServeConfig{
		MagicCookieKey:   "TEST_PLUGIN",
		MagicCookieValue: "test-value-123",
		ProtocolVersion:  1,
		Plugins:          NewRegistry(),
	}

	handshakeServer := NewHandshakeServer(cfg)

	// Perform handshake to get token
	req := connect.NewRequest(&connectpluginv1.HandshakeRequest{
		MagicCookieKey:      "TEST_PLUGIN",
		MagicCookieValue:    "test-value-123",
		CoreProtocolVersion: 1,
		AppProtocolVersion:  1,
		SelfId:              "test-plugin",
	})

	resp, err := handshakeServer.Handshake(context.Background(), req)
	if err != nil {
		t.Fatalf("Handshake failed: %v", err)
	}

	runtimeID := resp.Msg.RuntimeId
	validToken := resp.Msg.RuntimeToken

	// Test valid token
	if !handshakeServer.ValidateToken(runtimeID, validToken) {
		t.Error("Valid token rejected")
	}

	// Test invalid token (one char different)
	invalidToken := validToken[:len(validToken)-1] + "X"
	if handshakeServer.ValidateToken(runtimeID, invalidToken) {
		t.Error("Invalid token accepted")
	}
}
```

### 5. Regression Tests

**Objective:** Ensure fix doesn't break existing functionality

**Approach:** Run full existing test suite
```bash
task test
```

**Key test files to verify:**
- `auth_test.go` - Auth provider tests
- `handshake_test.go` - Handshake protocol tests (if exists)
- `broker_test.go` - Capability broker tests
- `integration_test.go` - End-to-end tests

## Migration

### Breaking Changes

**None.** This is an internal implementation change with no API impact.

### Backward Compatibility

**Fully compatible:**
- Function signatures unchanged
- Return values unchanged
- Error behavior unchanged
- Performance impact negligible (<20ns overhead per auth)

### Deployment Considerations

**Safe to deploy immediately:**
- No database migrations required
- No configuration changes required
- No client updates required
- No protocol changes

**Recommended deployment strategy:**
1. Merge changes to main branch
2. Run full test suite (including new timing tests manually)
3. Deploy to staging environment
4. Monitor authentication metrics (success rates, latencies)
5. Deploy to production

**Rollback strategy:**
- Simple git revert if issues arise
- No data migration to undo

### Observability

**No monitoring changes needed:**
- Authentication success/failure rates unchanged
- Latency impact unmeasurable in production (sub-microsecond)
- No new error conditions introduced

**Optional: Add security event logging**
```go
// Optional: Log failed auth attempts for security monitoring
if !subtle.ConstantTimeCompare([]byte(expectedToken), []byte(token)) == 1 {
	log.Printf("[SECURITY] Invalid token attempt for runtime ID: %s", runtimeID)
	return false
}
```

## Security Guarantees After Fix

### What This Fix Provides

✅ **Timing attack resistance:** Token comparison time independent of token similarity
✅ **Side-channel mitigation:** No information leakage through execution time
✅ **Standard library security:** Uses audited `crypto/subtle` implementation
✅ **Defense in depth:** Complements existing token entropy and network security

### What This Fix Does NOT Provide

❌ **Token expiration:** Tokens still valid forever (separate issue: REG-HIGH-002)
❌ **Replay protection:** No nonce/timestamp validation (separate issue: PROTO-CRIT-002)
❌ **TLS enforcement:** Tokens still transmitted in plaintext without TLS (PROTO-CRIT-003)
❌ **Rate limiting:** No brute-force protection (separate issue: REG-HIGH-001)

### Residual Risks

**Low Risk: Network-level timing attacks**
- Even with constant-time comparison, network jitter can leak information
- Mitigated by: TLS (hides packet sizes), rate limiting (limits samples)
- Recommendation: Document TLS requirement clearly

**Low Risk: Cache timing attacks**
- `map[string]string` lookup time varies by key length and hash collisions
- Mitigated by: Runtime IDs are controlled by server, not attacker
- Recommendation: Acceptable for current threat model

**Low Risk: String-to-bytes conversion timing**
- `[]byte(token)` allocation time varies slightly by length
- Mitigated by: All tokens are fixed-length (44 characters)
- Recommendation: No action needed

### Defense in Depth Recommendations

This fix should be combined with:

1. **Token Expiration** (REG-HIGH-002)
   - Add TTL to runtime tokens and capability grants
   - Force re-authentication periodically

2. **Rate Limiting** (REG-HIGH-001)
   - Limit authentication attempts per client
   - Implement exponential backoff on failures

3. **TLS Enforcement** (PROTO-CRIT-003)
   - Make TLS mandatory in production
   - Add runtime warning for non-TLS deployments

4. **Audit Logging**
   - Log all authentication failures
   - Monitor for brute-force patterns

## References

### Go Standard Library Documentation
- [`crypto/subtle` package](https://pkg.go.dev/crypto/subtle)
- [`subtle.ConstantTimeCompare` function](https://pkg.go.dev/crypto/subtle#ConstantTimeCompare)

### Security Best Practices
- [CWE-208: Observable Timing Discrepancy](https://cwe.mitre.org/data/definitions/208.html)
- [OWASP: Timing Attack](https://owasp.org/www-community/attacks/Timing_attack)
- [Timing Attacks on RSA](https://crypto.stanford.edu/~dabo/papers/ssl-timing.pdf) (Brumley & Boneh, 2003)

### Related Findings
- **AUTH-HIGH-001:** Token generation error handling
- **AUTH-HIGH-002:** mTLS interceptor implementation
- **REG-HIGH-002:** Token expiration missing
- **PROTO-CRIT-002:** Replay protection missing
- **PROTO-CRIT-003:** TLS not enforced

### Implementation Timeline

**Phase 1: Core Fix (This Design)**
- [x] Identify all vulnerable comparison sites
- [x] Design constant-time comparison approach
- [ ] Implement fix in `handshake.go`
- [ ] Implement fix in `broker.go`
- [ ] Add correctness tests
- [ ] Run benchmark comparison

**Phase 2: Testing & Validation**
- [ ] Add statistical timing tests
- [ ] Run full regression suite
- [ ] Manual timing analysis
- [ ] Code review

**Phase 3: Documentation**
- [ ] Update security documentation
- [ ] Add inline comments explaining timing safety
- [ ] Document residual risks
- [ ] Update threat model

**Estimated effort:** 4-6 hours (development + testing + review)

---

**Document Version:** 1.0
**Author:** Security Audit - Phase 003
**Date:** 2026-01-30
**Status:** Design Complete - Ready for Implementation
