# Design: Graceful crypto/rand Error Handling

**Finding ID:** AUTH-HIGH-001
**Severity:** HIGH
**Status:** Design Phase

## Problem Statement

The codebase currently has two critical issues with crypto/rand error handling that could lead to security vulnerabilities and operational failures:

1. **handshake.go:212-219** - `generateRandomHex()` panics when `crypto/rand.Read()` fails
2. **broker.go:188-199** - `generateToken()` and `generateGrantID()` silently ignore `crypto/rand.Read()` errors

### Security Impact

**Panic Issue (generateRandomHex):**
- Causes server crash during handshake protocol
- Denial of service vulnerability
- Breaks plugin initialization and runtime ID generation
- Affects service registry ID generation

**Silent Error Issue (generateToken/generateGrantID):**
- Returns empty/zero bytes when crypto/rand fails
- Creates predictable/weak tokens (all zeros)
- Completely breaks authentication system
- Enables unauthorized access to capabilities
- Enables unauthorized access to runtime identities

### Current Implementations

```go
// handshake.go:212-219
func generateRandomHex(length int) string {
	bytes := make([]byte, (length+1)/2)
	if _, err := rand.Read(bytes); err != nil {
		// Fall back to a less secure but still random method
		// This should never happen in practice
		panic(fmt.Sprintf("crypto/rand.Read failed: %v", err))
	}
	return hex.EncodeToString(bytes)[:length]
}

// broker.go:188-199
func generateGrantID() string {
	b := make([]byte, 16)
	rand.Read(b)  // Error silently ignored
	return base64.URLEncoding.EncodeToString(b)
}

func generateToken() string {
	b := make([]byte, 32)
	rand.Read(b)  // Error silently ignored
	return base64.URLEncoding.EncodeToString(b)
}
```

## Affected Functions

### Primary Functions (Direct Changes Required)

1. **generateRandomHex()** - `handshake.go:212-219`
   - Current: Panics on error
   - Change: Return `(string, error)`

2. **generateToken()** - `broker.go:195-199`
   - Current: Ignores error, returns empty/zero bytes
   - Change: Return `(string, error)`

3. **generateGrantID()** - `broker.go:188-191`
   - Current: Ignores error, returns empty/zero bytes
   - Change: Return `(string, error)`

4. **generateRuntimeID()** - `handshake.go:201-209`
   - Current: Calls `generateRandomHex()` without error handling
   - Change: Return `(string, error)` and propagate errors

5. **generateRegistrationID()** - `registry.go:528-530`
   - Current: Calls `generateRandomHex()` without error handling
   - Change: Return `(string, error)` and propagate errors

### Affected Call Sites

**handshake.go:**
- `Handshake()` RPC handler (line 88-89) - generates runtime ID and token
- `generateRuntimeID()` (line 203) - calls `generateRandomHex()`

**broker.go:**
- `RequestCapability()` RPC handler (lines 97-98) - generates grant ID and token

**platform.go:**
- `AddPlugin()` (lines 126-127) - generates runtime ID and token
- `ReplacePlugin()` (lines 236-237) - generates runtime ID and token

**registry.go:**
- `RegisterService()` RPC handler (calls `generateRegistrationID()`)

**router_test.go:**
- Multiple test functions use `generateRuntimeID()` and `generateToken()` directly

## Proposed Solution

### 1. Update Function Signatures

```go
// handshake.go
func generateRandomHex(length int) (string, error) {
	bytes := make([]byte, (length+1)/2)
	if _, err := rand.Read(bytes); err != nil {
		return "", fmt.Errorf("failed to generate random hex: %w", err)
	}
	return hex.EncodeToString(bytes)[:length], nil
}

func generateRuntimeID(selfID string) (string, error) {
	// Generate 4-character random suffix
	suffix, err := generateRandomHex(4)
	if err != nil {
		return "", fmt.Errorf("failed to generate runtime ID: %w", err)
	}

	// Normalize self_id (lowercase, replace spaces with hyphens)
	normalized := strings.ToLower(strings.ReplaceAll(selfID, " ", "-"))

	return fmt.Sprintf("%s-%s", normalized, suffix), nil
}

// broker.go
func generateGrantID() (string, error) {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		return "", fmt.Errorf("failed to generate grant ID: %w", err)
	}
	return base64.URLEncoding.EncodeToString(b), nil
}

func generateToken() (string, error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", fmt.Errorf("failed to generate token: %w", err)
	}
	return base64.URLEncoding.EncodeToString(b), nil
}

// registry.go
func generateRegistrationID() (string, error) {
	hex, err := generateRandomHex(16)
	if err != nil {
		return "", fmt.Errorf("failed to generate registration ID: %w", err)
	}
	return "reg-" + hex, nil
}
```

### 2. Update RPC Handlers

**handshake.go - Handshake() method:**

```go
func (h *HandshakeServer) Handshake(
	ctx context.Context,
	req *connect.Request[connectpluginv1.HandshakeRequest],
) (*connect.Response[connectpluginv1.HandshakeResponse], error) {
	// ... validation code ...

	// Phase 2: Generate runtime identity
	var runtimeID, runtimeToken string
	if req.Msg.SelfId != "" {
		// Plugin provided self_id - generate runtime identity
		var err error
		runtimeID, err = generateRuntimeID(req.Msg.SelfId)
		if err != nil {
			return nil, connect.NewError(
				connect.CodeInternal,
				fmt.Errorf("failed to generate runtime identity: %w", err),
			)
		}

		runtimeToken, err = generateToken()
		if err != nil {
			return nil, connect.NewError(
				connect.CodeInternal,
				fmt.Errorf("failed to generate runtime token: %w", err),
			)
		}

		// Store token for later validation
		h.mu.Lock()
		h.tokens[runtimeID] = runtimeToken
		h.mu.Unlock()
	}

	// ... rest of handler ...
}
```

**broker.go - RequestCapability() method:**

```go
func (b *CapabilityBroker) RequestCapability(
	ctx context.Context,
	req *connect.Request[connectpluginv1.RequestCapabilityRequest],
) (*connect.Response[connectpluginv1.RequestCapabilityResponse], error) {
	b.mu.Lock()
	defer b.mu.Unlock()

	// Find capability handler
	handler, ok := b.capabilities[req.Msg.CapabilityType]
	if !ok {
		return nil, connect.NewError(
			connect.CodeNotFound,
			fmt.Errorf("capability %q not available", req.Msg.CapabilityType),
		)
	}

	// Generate grant
	grantID, err := generateGrantID()
	if err != nil {
		return nil, connect.NewError(
			connect.CodeInternal,
			fmt.Errorf("failed to generate grant ID: %w", err),
		)
	}

	token, err := generateToken()
	if err != nil {
		return nil, connect.NewError(
			connect.CodeInternal,
			fmt.Errorf("failed to generate bearer token: %w", err),
		)
	}

	grant := &grantInfo{
		grantID:        grantID,
		capabilityType: req.Msg.CapabilityType,
		token:          token,
		handler:        handler,
	}
	b.grants[grantID] = grant

	// ... rest of handler ...
}
```

**registry.go - RegisterService() method:**

```go
func (r *ServiceRegistry) RegisterService(
	ctx context.Context,
	req *connect.Request[connectpluginv1.RegisterServiceRequest],
) (*connect.Response[connectpluginv1.RegisterServiceResponse], error) {
	// ... validation code ...

	// Generate registration ID
	regID, err := generateRegistrationID()
	if err != nil {
		return nil, connect.NewError(
			connect.CodeInternal,
			fmt.Errorf("failed to generate registration ID: %w", err),
		)
	}

	// ... rest of handler ...
}
```

### 3. Update Platform Functions

**platform.go - AddPlugin() method:**

```go
func (p *Platform) AddPlugin(ctx context.Context, config PluginConfig) error {
	// ... plugin info retrieval and validation ...

	// 3. Generate runtime identity
	runtimeID, err := generateRuntimeID(selfID)
	if err != nil {
		return fmt.Errorf("failed to generate runtime ID for plugin %q: %w", selfID, err)
	}

	runtimeToken, err := generateToken()
	if err != nil {
		return fmt.Errorf("failed to generate runtime token for plugin %q: %w", selfID, err)
	}

	// 4. Call plugin's SetRuntimeIdentity() to assign identity
	if err := infoClient.SetRuntimeIdentity(ctx, runtimeID, runtimeToken, ""); err != nil {
		return fmt.Errorf("failed to set runtime identity: %w", err)
	}

	// ... rest of function ...
}
```

**platform.go - ReplacePlugin() method:**

```go
func (p *Platform) ReplacePlugin(ctx context.Context, runtimeID string, newConfig PluginConfig) error {
	oldInstance, ok := p.plugins[runtimeID]
	if !ok {
		return fmt.Errorf("plugin not found: %s", runtimeID)
	}

	// 1. Start new version in parallel
	newRuntimeID, err := generateRuntimeID(newConfig.SelfID)
	if err != nil {
		return fmt.Errorf("failed to generate runtime ID for new plugin version: %w", err)
	}

	newToken, err := generateToken()
	if err != nil {
		return fmt.Errorf("failed to generate token for new plugin version: %w", err)
	}

	newInstance := &PluginInstance{
		RuntimeID: newRuntimeID,
		SelfID:    newConfig.SelfID,
		Metadata:  newConfig.Metadata,
		Endpoint:  newConfig.Endpoint,
		Token:     newToken,
		control:   NewPluginControlClient(newConfig.Endpoint, nil),
	}

	// ... rest of function ...
}
```

## Error Propagation Chain

### Complete Call Graph

```
RPC Handlers (Return Connect Errors):
├── handshake.Handshake()
│   ├── generateRuntimeID(selfID)
│   │   └── generateRandomHex(4) → ERROR → connect.CodeInternal
│   └── generateToken() → ERROR → connect.CodeInternal
│
├── broker.RequestCapability()
│   ├── generateGrantID() → ERROR → connect.CodeInternal
│   └── generateToken() → ERROR → connect.CodeInternal
│
└── registry.RegisterService()
    └── generateRegistrationID()
        └── generateRandomHex(16) → ERROR → connect.CodeInternal

Platform Functions (Return Standard Errors):
├── platform.AddPlugin()
│   ├── generateRuntimeID(selfID) → ERROR → fmt.Errorf wrapper
│   └── generateToken() → ERROR → fmt.Errorf wrapper
│
└── platform.ReplacePlugin()
    ├── generateRuntimeID(selfID) → ERROR → fmt.Errorf wrapper
    └── generateToken() → ERROR → fmt.Errorf wrapper

Low-Level Functions (Return Wrapped Errors):
├── generateRandomHex(length) → ERROR → fmt.Errorf("failed to generate random hex: %w", err)
├── generateRuntimeID(selfID) → ERROR → fmt.Errorf("failed to generate runtime ID: %w", err)
├── generateToken() → ERROR → fmt.Errorf("failed to generate token: %w", err)
├── generateGrantID() → ERROR → fmt.Errorf("failed to generate grant ID: %w", err)
└── generateRegistrationID() → ERROR → fmt.Errorf("failed to generate registration ID: %w", err)
```

### Error Flow Examples

**Success Case:**
```
Client → Handshake RPC → generateRuntimeID() → generateRandomHex() → rand.Read() ✓
                                                                      ↓
                                                                   Success
                                                                      ↓
                       ← HandshakeResponse with runtime_id and token
```

**Failure Case:**
```
Client → Handshake RPC → generateRuntimeID() → generateRandomHex() → rand.Read() ✗
                                                                      ↓
                                                         error("crypto/rand failed")
                                                                      ↓
                                                fmt.Errorf("failed to generate random hex: %w")
                                                                      ↓
                                                fmt.Errorf("failed to generate runtime ID: %w")
                                                                      ↓
                                   connect.NewError(CodeInternal, "failed to generate runtime identity")
                                                                      ↓
                       ← Error Response (gRPC/Connect error with Internal status)
```

## Error Handling Strategy

### What Should Happen When crypto/rand Fails?

**No Retry Logic:**
- `crypto/rand.Read()` failures are extremely rare and indicate serious system issues:
  - Out of entropy (very rare on modern systems)
  - `/dev/urandom` unavailable (system misconfiguration)
  - Hardware failure
- Retrying won't help in these scenarios
- Return error immediately to caller

**Error Classification:**
- **RPC Handlers:** Return `connect.CodeInternal` with descriptive message
- **Platform Functions:** Return standard Go error with context
- **Low-Level Functions:** Return wrapped errors for stack trace

### Error Messages

**For RPC Handlers (User-Facing):**
```go
connect.NewError(
	connect.CodeInternal,
	fmt.Errorf("failed to generate runtime identity: %w", err),
)
```

**For Platform Functions (Internal):**
```go
fmt.Errorf("failed to generate runtime ID for plugin %q: %w", selfID, err)
```

**For Low-Level Functions (Wrapped):**
```go
fmt.Errorf("failed to generate random hex: %w", err)
```

### No Fallback Behavior

**Critical Decision:** Do NOT implement fallback to weaker random sources.

**Rationale:**
- Security tokens require cryptographic randomness
- Falling back to `math/rand` or other PRNG creates exploitable weakness
- Better to fail securely than succeed insecurely
- System administrators must fix underlying issue

**Exception:** There is no valid exception for authentication/authorization tokens.

## API Changes

### Breaking Changes

**Function Signature Changes (Internal API):**

1. `generateRandomHex(int) string` → `generateRandomHex(int) (string, error)`
2. `generateRuntimeID(string) string` → `generateRuntimeID(string) (string, error)`
3. `generateToken() string` → `generateToken() (string, error)`
4. `generateGrantID() string` → `generateGrantID() (string, error)`
5. `generateRegistrationID() string` → `generateRegistrationID() (string, error)`

**Impact:** These are all unexported (private) functions - no external API breakage.

### Test Updates Required

All tests that use these functions need updates:

**router_test.go:**
```go
// Before:
runtimeID := generateRuntimeID("test-plugin")
token := generateToken()

// After:
runtimeID, err := generateRuntimeID("test-plugin")
require.NoError(t, err)
token, err := generateToken()
require.NoError(t, err)
```

**Approximately 20+ test call sites need updating.**

### Migration Path

Since all affected functions are internal (unexported), migration is straightforward:

1. Update function signatures
2. Update all call sites in the same commit
3. Update tests in the same commit
4. No versioning or deprecation needed

## Implementation Plan

### Step 1: Update Low-Level Functions
- Update `generateRandomHex()` to return error
- Update `generateToken()` to return error
- Update `generateGrantID()` to return error
- Add unit tests for error conditions

### Step 2: Update Mid-Level Functions
- Update `generateRuntimeID()` to return error and propagate
- Update `generateRegistrationID()` to return error and propagate
- Add unit tests for error propagation

### Step 3: Update RPC Handlers
- Update `Handshake()` in handshake.go
- Update `RequestCapability()` in broker.go
- Update `RegisterService()` in registry.go
- Ensure proper Connect error codes (CodeInternal)

### Step 4: Update Platform Functions
- Update `AddPlugin()` in platform.go
- Update `ReplacePlugin()` in platform.go
- Ensure proper error wrapping with context

### Step 5: Update Tests
- Update all test files that call these functions
- Add error injection tests (mock crypto/rand failure)
- Verify error messages and codes

### Step 6: Integration Testing
- Test handshake failure scenarios
- Test capability grant failure scenarios
- Test plugin registration failure scenarios
- Verify proper error responses to clients

## Test Strategy

### Unit Tests

**Test Error Conditions:**

```go
// Test generateRandomHex error handling
func TestGenerateRandomHex_Error(t *testing.T) {
	// Mock crypto/rand failure (implementation-dependent)
	// Verify error is returned, not panic
	// Verify error message includes context
}

// Test generateToken error handling
func TestGenerateToken_Error(t *testing.T) {
	// Mock crypto/rand failure
	// Verify error is returned
	// Verify no empty/zero token is returned
}
```

**Test Error Propagation:**

```go
// Test generateRuntimeID propagates errors
func TestGenerateRuntimeID_PropagatesError(t *testing.T) {
	// Mock generateRandomHex to return error
	// Verify generateRuntimeID returns error
	// Verify error is wrapped with context
}
```

### Integration Tests

**Test RPC Error Responses:**

```go
// Test handshake returns proper error on crypto failure
func TestHandshake_CryptoFailure(t *testing.T) {
	// Inject crypto/rand failure (if possible via test build tag)
	// Call Handshake RPC
	// Verify connect.CodeInternal error returned
	// Verify error message is descriptive
}

// Test capability request returns proper error
func TestRequestCapability_CryptoFailure(t *testing.T) {
	// Inject crypto/rand failure
	// Call RequestCapability RPC
	// Verify connect.CodeInternal error returned
	// Verify error message is descriptive
}
```

### Error Injection Approaches

1. **Build Tags:** Create `_test.go` files with mock implementations
2. **Interface Wrapper:** Wrap `crypto/rand.Read()` in interface for testing
3. **Manual Testing:** Temporarily modify code to force error for testing

**Note:** `crypto/rand.Read()` is difficult to mock without significant refactoring. Integration tests may require manual verification or build-time test flags.

### Test Coverage Goals

- 100% coverage of new error paths
- All RPC handlers tested with error conditions
- All platform functions tested with error propagation
- Verify no panic conditions remain
- Verify no silent error conditions remain

## Security Considerations

### Security Benefits

1. **Eliminates Panic DoS:** Server won't crash on crypto/rand failure
2. **Prevents Weak Tokens:** System fails securely instead of generating predictable tokens
3. **Audit Trail:** Errors logged and visible to operators
4. **Fail-Secure:** System refuses to operate with weak security rather than degrading

### Operational Impact

**Failure Scenarios:**
- Handshake RPC returns error → Plugin cannot authenticate
- Capability grant RPC returns error → Plugin cannot access capabilities
- Service registration returns error → Plugin services cannot be registered

**Recovery:**
- System administrator must investigate entropy source issue
- Restart affected processes after fixing underlying issue
- No automatic retry to prevent masking serious problems

### Compliance

This change supports security compliance by:
- Using only cryptographic-quality randomness for security tokens
- Logging security-critical failures
- Preventing silent security failures
- Maintaining audit trail of authentication failures

## Documentation Changes

### Code Comments

Update docstrings for affected functions:

```go
// generateRandomHex generates a cryptographically secure random hex string.
// Returns an error if the system's random number generator is unavailable.
// This typically indicates a serious system configuration issue.
func generateRandomHex(length int) (string, error)

// generateToken generates a cryptographically secure bearer token.
// Returns an error if the system's random number generator is unavailable.
func generateToken() (string, error)
```

### Error Handling Guide

Add to package documentation:

```markdown
## Error Handling

The connect-plugin-go library returns errors from security-critical operations
rather than falling back to weaker alternatives. If crypto/rand operations fail,
functions will return errors rather than generating weak or predictable tokens.

In production, crypto/rand failures are extremely rare and indicate serious
system issues that require administrator intervention.
```

## Timeline Estimate

- **Step 1-2:** 2-4 hours (low-level functions + tests)
- **Step 3-4:** 4-6 hours (RPC handlers + platform functions + tests)
- **Step 5:** 4-6 hours (update all tests)
- **Step 6:** 2-4 hours (integration testing + documentation)

**Total:** 12-20 hours

## Risks and Mitigations

### Risk: Test Coverage for crypto/rand Failure

**Mitigation:** Use build tags or interfaces to inject failures during testing.

### Risk: Breaking Internal API

**Mitigation:** All changes in single commit, comprehensive test suite.

### Risk: Missing Call Sites

**Mitigation:** Use `grep` to find all call sites, verify with compile check.

### Risk: Different Error Handling in Different Contexts

**Mitigation:** Document clear error handling patterns for RPC vs platform contexts.

## Future Enhancements

### Monitoring and Alerting

Consider adding:
- Metrics for crypto/rand failures
- Structured logging for security events
- Alert on repeated failures (indicates serious issue)

### Configuration Options

Consider adding (future work):
- Custom error messages for different deployment contexts
- Configurable error response behavior
- Health check that validates crypto/rand availability

## References

- **Finding:** AUTH-HIGH-001 in 03-security-assessment.md
- **Affected Files:** handshake.go, broker.go, registry.go, platform.go
- **Related Security Patterns:** Fail-secure design, secure random generation
- **Go Documentation:** https://pkg.go.dev/crypto/rand

## Approval

This design should be reviewed by:
- Security team (fail-secure approach)
- API maintainers (internal API changes)
- Operations team (failure modes)

## Changelog

- 2026-02-02: Initial design document created
