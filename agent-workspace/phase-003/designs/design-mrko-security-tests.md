# Design: Security Test Suite

**Status**: Draft
**Created**: 2026-02-03
**Author**: Claude (Agent)
**Phase**: 3 (Security Testing)

## Problem Statement

Following Phase 1 security fixes (R1.2, R1.3, R1.5), we lack comprehensive automated tests to:

1. **Validate security fixes** - Ensure constant-time comparison, graceful crypto/rand error handling, and TLS warnings work correctly
2. **Prevent regressions** - Detect if future code changes break security properties
3. **Verify attack resistance** - Prove timing attack resistance through statistical testing
4. **Test edge cases** - Cover crypto failures, invalid tokens, and error paths

### Current Test Coverage Gaps

**Existing tests** (auth_test.go, broker_test.go, router_test.go, registry_test.go):
- Test happy paths (valid tokens, successful authentication)
- Test error paths (missing tokens, invalid tokens)
- Do NOT test timing attack resistance
- Do NOT test crypto/rand failure paths
- Do NOT verify constant-time comparison behavior
- Do NOT test TLS warning behavior

**Security properties NOT tested**:
- Constant-time token comparison (R1.2)
- Crypto/rand error handling (R1.3)
- TLS warning behavior (R1.5)
- Timing attack resistance
- Token validation security
- Grant token security

## Test Categories

### 1. Authentication Security Tests

Test authentication mechanisms against bypass attacks and timing leaks.

**Tests**:
- Invalid token formats (empty, malformed, wrong length)
- Token reuse across different runtime IDs
- Token brute force resistance (fail closed)
- Missing authentication headers
- Malformed Authorization headers

**Files**: `security_auth_test.go`

### 2. Timing Attack Tests

Verify constant-time comparison prevents timing attacks.

**Tests**:
- Token comparison timing (valid vs invalid tokens)
- Grant token comparison timing (broker)
- Runtime ID validation timing
- Statistical analysis of timing variance

**Files**: `security_timing_test.go`

### 3. Crypto Error Handling Tests

Verify graceful handling of crypto/rand failures.

**Tests**:
- `generateRuntimeID()` failure handling
- `generateToken()` failure handling
- `generateGrantID()` failure handling
- `generateRegistrationID()` failure handling
- `generateRandomHex()` failure handling
- Error propagation to RPC callers

**Files**: `security_crypto_test.go`

### 4. TLS Warning Tests

Verify TLS warnings are shown appropriately.

**Tests**:
- Client warning on http:// endpoint
- Server warning on plaintext listener
- No warning for https:// endpoints
- No warning for unix:// sockets
- Warning suppression via CONNECTPLUGIN_DISABLE_TLS_WARNING
- Warning output format validation

**Files**: `security_tls_test.go`

### 5. Authorization Tests

Test authorization enforcement and access control.

**Tests**:
- Service routing with invalid caller token
- Service routing with expired/revoked token
- Capability grant validation
- Cross-plugin authorization
- Runtime ID enforcement

**Files**: `security_authz_test.go`

### 6. Input Fuzzing (Future)

Fuzz testing for input validation vulnerabilities.

**Tests**:
- Fuzz token strings
- Fuzz runtime IDs
- Fuzz header values
- Fuzz service paths
- Fuzz protobuf messages

**Files**: `security_fuzz_test.go` (Go 1.18+ fuzzing)

## Timing Attack Tests - Detailed Design

### Challenge

Proving constant-time comparison is difficult because:
- Modern CPUs have variable-latency instructions
- OS scheduling introduces timing noise
- Network jitter adds unpredictability
- Cache effects vary across runs

### Approach: Statistical Variance Analysis

**Hypothesis**:
- Variable-time comparison: timing varies with byte position of mismatch
- Constant-time comparison: timing variance is uniform regardless of input

**Method**:
1. Generate test tokens (valid + invalid with mismatches at different positions)
2. Measure comparison time for each token (many iterations)
3. Calculate timing variance for each position
4. Apply statistical test (Kolmogorov-Smirnov or Anderson-Darling)
5. Reject if variance differs significantly across positions

### Test Design

```go
// security_timing_test.go

// TestConstantTimeTokenComparison verifies token comparison is constant-time
func TestConstantTimeTokenComparison(t *testing.T) {
    if testing.Short() {
        t.Skip("skipping timing test in short mode")
    }

    // Create handshake server with known token
    validToken := "secure-token-abcd1234"
    runtimeID := "test-plugin-x7k2"

    handshake := NewHandshakeServer(&ServeConfig{})
    handshake.mu.Lock()
    handshake.tokens[runtimeID] = validToken
    handshake.mu.Unlock()

    // Test cases: tokens with mismatches at different positions
    testCases := []struct {
        name     string
        token    string
        position int // byte position of first mismatch
    }{
        {"valid", validToken, -1},
        {"mismatch_pos_0", "Xecure-token-abcd1234", 0},
        {"mismatch_pos_7", "secure-Xoken-abcd1234", 7},
        {"mismatch_pos_14", "secure-token-Xbcd1234", 14},
        {"mismatch_pos_20", "secure-token-abcd123X", 20},
    }

    // Warmup: run comparisons to stabilize CPU cache
    for range 1000 {
        handshake.ValidateToken(runtimeID, validToken)
    }

    // Measure timing for each test case
    const iterations = 10000
    timings := make(map[string][]time.Duration)

    for _, tc := range testCases {
        timings[tc.name] = make([]time.Duration, iterations)

        for i := 0; i < iterations; i++ {
            start := time.Now()
            handshake.ValidateToken(runtimeID, tc.token)
            timings[tc.name][i] = time.Since(start)
        }
    }

    // Statistical analysis
    result := analyzeTimingVariance(timings)

    if !result.IsConstantTime {
        t.Errorf("Token comparison is NOT constant-time: %s", result.Reason)
        t.Logf("Timing statistics:\n%s", result.FormatStats())
    }
}

// analyzeTimingVariance performs statistical analysis on timing data
func analyzeTimingVariance(timings map[string][]time.Duration) TimingAnalysisResult {
    // Calculate statistics for each position
    stats := make(map[string]TimingStats)
    for name, durations := range timings {
        stats[name] = calculateStats(durations)
    }

    // Compare distributions using Kolmogorov-Smirnov test
    // H0: distributions are identical (constant-time)
    // H1: distributions differ (timing leak)
    pValue := kolmogorovSmirnovTest(stats)

    // Reject constant-time hypothesis if p < 0.01 (99% confidence)
    const significanceLevel = 0.01
    isConstantTime := pValue >= significanceLevel

    return TimingAnalysisResult{
        IsConstantTime: isConstantTime,
        PValue:        pValue,
        Stats:         stats,
        Reason:        formatReason(pValue, significanceLevel),
    }
}

type TimingStats struct {
    Mean   time.Duration
    Median time.Duration
    StdDev time.Duration
    Min    time.Duration
    Max    time.Duration
}

type TimingAnalysisResult struct {
    IsConstantTime bool
    PValue        float64
    Stats         map[string]TimingStats
    Reason        string
}
```

### Alternative: Benchmark-Based Approach

Use Go benchmarks to detect timing differences:

```go
func BenchmarkValidateToken_Valid(b *testing.B) {
    handshake := setupHandshake()
    b.ResetTimer()
    for i := 0; i < b.N; i++ {
        handshake.ValidateToken("runtime-id", validToken)
    }
}

func BenchmarkValidateToken_Invalid_EarlyMismatch(b *testing.B) {
    handshake := setupHandshake()
    b.ResetTimer()
    for i := 0; i < b.N; i++ {
        handshake.ValidateToken("runtime-id", invalidTokenEarly)
    }
}

func BenchmarkValidateToken_Invalid_LateMismatch(b *testing.B) {
    handshake := setupHandshake()
    b.ResetTimer()
    for i := 0; i < b.N; i++ {
        handshake.ValidateToken("runtime-id", invalidTokenLate)
    }
}
```

**Analysis**: Benchmark results should have similar ns/op regardless of mismatch position.

### Acceptance Criteria

**Pass conditions**:
- Statistical test p-value ≥ 0.01 (99% confidence in constant-time)
- Benchmark variance < 5% across mismatch positions
- No measurable correlation between mismatch position and timing

**Confidence level**: 99% (α = 0.01)

### CI Integration

Timing tests are sensitive to system load:
- Run in dedicated CI job with isolated runner
- Skip timing tests in short mode (`go test -short`)
- Mark as flaky and retry 3x before failing
- Run timing tests less frequently (nightly, not per-commit)

## Error Injection Tests - Detailed Design

### Challenge

Testing crypto/rand failures requires:
- Simulating crypto/rand.Read() failures
- Verifying error propagation through call stack
- Testing without breaking production code

### Approach: Mock rand.Reader

Go's `crypto/rand.Reader` is an `io.Reader` interface, but functions like `rand.Read()` use the global reader. We need test-injectable failure points.

### Implementation Strategy

#### Option 1: Test Helpers with Failure Injection

Add test-only functions that accept custom rand source:

```go
// crypto.go (production code)

// For testing: allow injecting custom reader
var testRandReader io.Reader

func generateTokenWithReader(r io.Reader) (string, error) {
    b := make([]byte, 32)
    if _, err := r.Read(b); err != nil {
        return "", fmt.Errorf("failed to generate secure token: %w", err)
    }
    return base64.URLEncoding.EncodeToString(b), nil
}

func generateToken() (string, error) {
    reader := rand.Reader
    if testRandReader != nil {
        reader = testRandReader
    }
    return generateTokenWithReader(reader)
}
```

#### Option 2: Build Tags for Test Mode

Use build tags to inject test-only failure points:

```go
// crypto.go
//go:build !test

func cryptoRandRead(b []byte) (int, error) {
    return rand.Read(b)
}
```

```go
// crypto_test.go
//go:build test

// Override in test mode
func cryptoRandRead(b []byte) (int, error) {
    if testCryptoFailure {
        return 0, errors.New("crypto/rand failure")
    }
    return rand.Read(b)
}
```

#### Option 3: Simple Mock Reader (RECOMMENDED)

Create a mock reader for tests only:

```go
// security_crypto_test.go

// failingReader always returns an error
type failingReader struct {
    err error
}

func (f *failingReader) Read(p []byte) (n int, err error) {
    return 0, f.err
}

// Test with failing reader
func TestGenerateToken_CryptoFailure(t *testing.T) {
    mockReader := &failingReader{err: errors.New("RNG unavailable")}

    // Call internal function with mock reader
    _, err := generateTokenWithReader(mockReader)
    if err == nil {
        t.Fatal("Expected error when crypto/rand fails")
    }

    if !strings.Contains(err.Error(), "failed to generate secure token") {
        t.Errorf("Expected wrapped error message, got: %v", err)
    }
}
```

### Test Design

```go
// security_crypto_test.go

func TestCryptoErrorHandling_RuntimeID(t *testing.T) {
    // Test generateRuntimeID error handling
    mockReader := &failingReader{err: errors.New("CSPRNG exhausted")}

    _, err := generateRuntimeIDWithReader("test-plugin", mockReader)
    if err == nil {
        t.Fatal("Expected error from generateRuntimeID")
    }

    if !strings.Contains(err.Error(), "failed to generate runtime ID") {
        t.Errorf("Expected wrapped error, got: %v", err)
    }
}

func TestCryptoErrorHandling_Token(t *testing.T) {
    mockReader := &failingReader{err: errors.New("hardware RNG failure")}

    _, err := generateTokenWithReader(mockReader)
    if err == nil {
        t.Fatal("Expected error from generateToken")
    }

    if !strings.Contains(err.Error(), "failed to generate secure token") {
        t.Errorf("Expected wrapped error, got: %v", err)
    }
}

func TestCryptoErrorHandling_GrantID(t *testing.T) {
    mockReader := &failingReader{err: errors.New("entropy pool empty")}

    _, err := generateGrantIDWithReader(mockReader)
    if err == nil {
        t.Fatal("Expected error from generateGrantID")
    }
}

func TestCryptoErrorHandling_RegistrationID(t *testing.T) {
    mockReader := &failingReader{err: errors.New("system entropy unavailable")}

    _, err := generateRegistrationIDWithReader(mockReader)
    if err == nil {
        t.Fatal("Expected error from generateRegistrationID")
    }
}

// Test RPC error propagation
func TestHandshake_CryptoFailurePropagation(t *testing.T) {
    // Mock crypto failure at RPC boundary
    // Verify client receives CodeInternal error with clear message

    server := httptest.NewServer(/* handshake handler with failing crypto */)
    defer server.Close()

    client := connectpluginv1connect.NewHandshakeServiceClient(
        http.DefaultClient,
        server.URL,
    )

    req := &connectpluginv1.HandshakeRequest{
        CoreProtocolVersion: 1,
        SelfId: "test-plugin",
    }

    _, err := client.Handshake(context.Background(), connect.NewRequest(req))
    if err == nil {
        t.Fatal("Expected error from handshake with crypto failure")
    }

    // Verify error code and message
    if connect.CodeOf(err) != connect.CodeInternal {
        t.Errorf("Expected CodeInternal, got %v", connect.CodeOf(err))
    }

    if !strings.Contains(err.Error(), "failed to generate") {
        t.Errorf("Expected clear error message, got: %v", err)
    }
}
```

### Required Code Changes

To enable error injection testing, add `*WithReader()` variants:

```go
// handshake.go
func generateRuntimeIDWithReader(selfID string, r io.Reader) (string, error) {
    suffix, err := generateRandomHexWithReader(4, r)
    if err != nil {
        return "", fmt.Errorf("failed to generate runtime ID: %w", err)
    }
    normalized := strings.ToLower(strings.ReplaceAll(selfID, " ", "-"))
    return fmt.Sprintf("%s-%s", normalized, suffix), nil
}

func generateRandomHexWithReader(length int, r io.Reader) (string, error) {
    bytes := make([]byte, (length+1)/2)
    if _, err := r.Read(bytes); err != nil {
        return "", fmt.Errorf("crypto/rand.Read failed: %w", err)
    }
    return hex.EncodeToString(bytes)[:length], nil
}

// Production functions call *WithReader with rand.Reader
func generateRuntimeID(selfID string) (string, error) {
    return generateRuntimeIDWithReader(selfID, rand.Reader)
}

func generateRandomHex(length int) (string, error) {
    return generateRandomHexWithReader(length, rand.Reader)
}
```

Apply same pattern to:
- `generateToken()` / `generateTokenWithReader()`
- `generateGrantID()` / `generateGrantIDWithReader()`
- `generateRegistrationID()` / `generateRegistrationIDWithReader()`

## TLS Warning Tests - Detailed Design

### Test Strategy

Verify TLS warnings are logged appropriately without breaking functionality.

### Test Design

```go
// security_tls_test.go

func TestTLSWarning_ClientHTTPEndpoint(t *testing.T) {
    // Capture log output
    var logBuf bytes.Buffer
    log.SetOutput(&logBuf)
    defer log.SetOutput(os.Stderr)

    // Create client with http:// endpoint
    cfg := ClientConfig{
        Endpoint: "http://localhost:8080",
        Plugins:  PluginSet{"test": &testPlugin{}},
    }

    client, err := NewClient(cfg)
    if err != nil {
        t.Fatal(err)
    }

    err = client.Connect(context.Background())
    // Connect may fail (no server), but warning should be logged

    // Verify warning was logged
    logOutput := logBuf.String()
    if !strings.Contains(logOutput, "Non-TLS plugin endpoint") {
        t.Error("Expected TLS warning in client log")
    }
    if !strings.Contains(logOutput, "http://localhost:8080") {
        t.Error("Expected endpoint in warning message")
    }
    if !strings.Contains(logOutput, "Man-in-the-middle attacks") {
        t.Error("Expected security risk in warning")
    }
}

func TestTLSWarning_ServerPlaintext(t *testing.T) {
    var logBuf bytes.Buffer
    log.SetOutput(&logBuf)
    defer log.SetOutput(os.Stderr)

    cfg := &ServeConfig{
        Addr: ":8080",
        Plugins: PluginSet{"test": &testPlugin{}},
        Impls: map[string]any{"test": &testImpl{}},
        StopCh: make(chan struct{}),
    }

    // Start server in background (will log warning)
    go Serve(cfg)

    // Give server time to start and log
    time.Sleep(100 * time.Millisecond)

    // Stop server
    close(cfg.StopCh.(chan struct{}))

    // Verify warning was logged
    logOutput := logBuf.String()
    if !strings.Contains(logOutput, "Plugin server starting without TLS") {
        t.Error("Expected TLS warning in server log")
    }
    if !strings.Contains(logOutput, ":8080") {
        t.Error("Expected address in warning")
    }
}

func TestTLSWarning_HTTPSNoWarning(t *testing.T) {
    var logBuf bytes.Buffer
    log.SetOutput(&logBuf)
    defer log.SetOutput(os.Stderr)

    cfg := ClientConfig{
        Endpoint: "https://secure.example.com",
        Plugins:  PluginSet{"test": &testPlugin{}},
    }

    client, err := NewClient(cfg)
    if err != nil {
        t.Fatal(err)
    }

    // Try to connect (will fail - no server)
    _ = client.Connect(context.Background())

    // Verify NO warning logged
    logOutput := logBuf.String()
    if strings.Contains(logOutput, "Non-TLS") {
        t.Error("Unexpected TLS warning for https:// endpoint")
    }
}

func TestTLSWarning_UnixSocketNoWarning(t *testing.T) {
    var logBuf bytes.Buffer
    log.SetOutput(&logBuf)
    defer log.SetOutput(os.Stderr)

    cfg := ClientConfig{
        Endpoint: "unix:///tmp/plugin.sock",
        Plugins:  PluginSet{"test": &testPlugin{}},
    }

    client, err := NewClient(cfg)
    if err != nil {
        t.Fatal(err)
    }

    _ = client.Connect(context.Background())

    // Unix sockets are secure (kernel isolation)
    logOutput := logBuf.String()
    if strings.Contains(logOutput, "Non-TLS") {
        t.Error("Unexpected TLS warning for unix:// socket")
    }
}

func TestTLSWarning_Suppression(t *testing.T) {
    // Set suppression env var
    os.Setenv("CONNECTPLUGIN_DISABLE_TLS_WARNING", "1")
    defer os.Unsetenv("CONNECTPLUGIN_DISABLE_TLS_WARNING")

    var logBuf bytes.Buffer
    log.SetOutput(&logBuf)
    defer log.SetOutput(os.Stderr)

    cfg := ClientConfig{
        Endpoint: "http://localhost:8080",
        Plugins:  PluginSet{"test": &testPlugin{}},
    }

    client, err := NewClient(cfg)
    if err != nil {
        t.Fatal(err)
    }

    _ = client.Connect(context.Background())

    // Verify warning was suppressed
    logOutput := logBuf.String()
    if strings.Contains(logOutput, "Non-TLS") {
        t.Error("TLS warning should be suppressed with env var")
    }
}

func TestTLSWarning_MessageFormat(t *testing.T) {
    var logBuf bytes.Buffer
    log.SetOutput(&logBuf)
    defer log.SetOutput(os.Stderr)

    cfg := ClientConfig{
        Endpoint: "http://localhost:8080",
        Plugins:  PluginSet{"test": &testPlugin{}},
    }

    client, err := NewClient(cfg)
    if err != nil {
        t.Fatal(err)
    }

    _ = client.Connect(context.Background())

    logOutput := logBuf.String()

    // Verify required fields in warning
    requiredFields := []string{
        "endpoint:",
        "impact:",
        "risk:",
        "resolution:",
        "suppress:",
    }

    for _, field := range requiredFields {
        if !strings.Contains(logOutput, field) {
            t.Errorf("Warning message missing field: %s", field)
        }
    }
}
```

## Test Infrastructure

### Helper Functions

```go
// testing_helpers.go

// setupTestHandshake creates a handshake server with test tokens
func setupTestHandshake() (*HandshakeServer, map[string]string) {
    handshake := NewHandshakeServer(&ServeConfig{})
    tokens := map[string]string{
        "runtime-a": "token-a-12345678",
        "runtime-b": "token-b-87654321",
    }

    handshake.mu.Lock()
    for id, token := range tokens {
        handshake.tokens[id] = token
    }
    handshake.mu.Unlock()

    return handshake, tokens
}

// setupTestBroker creates a broker with test capabilities
func setupTestBroker() *CapabilityBroker {
    broker := NewCapabilityBroker("http://localhost:8080")
    broker.RegisterCapability(&testCapability{})
    return broker
}

// setupTestServer creates a test HTTP server with handlers
func setupTestServer(handlers map[string]http.Handler) *httptest.Server {
    mux := http.NewServeMux()
    for path, handler := range handlers {
        mux.Handle(path, handler)
    }
    return httptest.NewServer(mux)
}

// assertConstantTime fails if timing varies significantly
func assertConstantTime(t *testing.T, timings map[string][]time.Duration) {
    result := analyzeTimingVariance(timings)
    if !result.IsConstantTime {
        t.Errorf("Not constant-time: %s\n%s",
            result.Reason, result.FormatStats())
    }
}

// captureLogOutput captures log.Printf output for testing
func captureLogOutput(fn func()) string {
    var buf bytes.Buffer
    log.SetOutput(&buf)
    defer log.SetOutput(os.Stderr)
    fn()
    return buf.String()
}
```

### Mock Implementations

```go
// testing_mocks.go

// mockPlugin for testing
type mockPlugin struct{}

func (m *mockPlugin) Metadata() PluginMetadata {
    return PluginMetadata{Name: "mock", Version: "1.0.0", Path: "/mock"}
}

func (m *mockPlugin) ConnectClient(endpoint string, client connect.HTTPClient) (any, error) {
    return &mockClient{}, nil
}

func (m *mockPlugin) ConnectServer(impl any) (string, http.Handler, error) {
    return "/mock", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
        w.WriteHeader(http.StatusOK)
    }), nil
}

// failingReader for crypto error testing
type failingReader struct {
    err error
}

func (f *failingReader) Read(p []byte) (n int, err error) {
    return 0, f.err
}

// testCapability for broker testing
type testCapability struct{}

func (t *testCapability) CapabilityType() string { return "test" }
func (t *testCapability) Version() string        { return "1.0.0" }
func (t *testCapability) ServeHTTP(w http.ResponseWriter, r *http.Request) {
    w.WriteHeader(http.StatusOK)
}
```

### Statistical Analysis Functions

```go
// timing_analysis.go

// calculateStats computes timing statistics
func calculateStats(durations []time.Duration) TimingStats {
    sort.Slice(durations, func(i, j int) bool {
        return durations[i] < durations[j]
    })

    sum := time.Duration(0)
    for _, d := range durations {
        sum += d
    }
    mean := sum / time.Duration(len(durations))

    median := durations[len(durations)/2]

    variance := float64(0)
    for _, d := range durations {
        diff := float64(d - mean)
        variance += diff * diff
    }
    stdDev := time.Duration(math.Sqrt(variance / float64(len(durations))))

    return TimingStats{
        Mean:   mean,
        Median: median,
        StdDev: stdDev,
        Min:    durations[0],
        Max:    durations[len(durations)-1],
    }
}

// kolmogorovSmirnovTest performs K-S test on timing distributions
func kolmogorovSmirnovTest(stats map[string]TimingStats) float64 {
    // Implementation using gonum/stat or custom K-S test
    // Returns p-value (0-1)
    // High p-value = distributions are similar (constant-time)
    // Low p-value = distributions differ (timing leak)

    // TODO: Implement using statistical library
    return 0.5 // Placeholder
}
```

## CI Integration

### GitHub Actions Workflow

```yaml
# .github/workflows/security-tests.yml
name: Security Tests

on:
  push:
    branches: [main]
  pull_request:
  schedule:
    # Run timing tests nightly (less sensitive to CI load)
    - cron: '0 2 * * *'

jobs:
  security-tests:
    runs-on: ubuntu-latest
    strategy:
      matrix:
        test-suite:
          - auth
          - crypto
          - tls
          - authz
    steps:
      - uses: actions/checkout@v3

      - uses: actions/setup-go@v4
        with:
          go-version: '1.25'

      - name: Run security tests
        run: |
          go test -v -run TestSecurity -tags=security \
            ./security_${{ matrix.test-suite }}_test.go

      - name: Upload coverage
        uses: codecov/codecov-action@v3
        with:
          files: ./coverage.out
          flags: security-${{ matrix.test-suite }}

  timing-tests:
    runs-on: ubuntu-latest
    # Run on dedicated runner with minimal load
    if: github.event_name == 'schedule'
    steps:
      - uses: actions/checkout@v3

      - uses: actions/setup-go@v4
        with:
          go-version: '1.25'

      - name: Run timing tests
        # Retry 3x to handle flakiness
        uses: nick-invision/retry@v2
        with:
          timeout_minutes: 10
          max_attempts: 3
          command: |
            go test -v -run TestConstantTime \
              -timeout=10m ./security_timing_test.go

      - name: Upload timing results
        if: failure()
        uses: actions/upload-artifact@v3
        with:
          name: timing-analysis
          path: ./timing-*.json
```

### Test Organization

```
connect-plugin-go/
├── security_test.go           # Common test helpers
├── security_auth_test.go      # Authentication tests
├── security_timing_test.go    # Timing attack tests
├── security_crypto_test.go    # Crypto error handling tests
├── security_tls_test.go       # TLS warning tests
├── security_authz_test.go     # Authorization tests
├── security_fuzz_test.go      # Fuzzing tests (future)
└── internal/
    └── testutil/
        ├── timing.go          # Timing analysis utilities
        ├── mocks.go           # Mock implementations
        └── helpers.go         # Test helpers
```

### Performance Baselines

Store timing test baselines in repo:

```json
// testdata/timing-baseline.json
{
  "environment": {
    "os": "linux",
    "arch": "amd64",
    "go_version": "1.25"
  },
  "benchmarks": {
    "ValidateToken_Valid": {
      "mean_ns": 245,
      "stddev_ns": 12,
      "p99_ns": 280
    },
    "ValidateToken_Invalid_Early": {
      "mean_ns": 248,
      "stddev_ns": 14,
      "p99_ns": 285
    },
    "ValidateToken_Invalid_Late": {
      "mean_ns": 246,
      "stddev_ns": 13,
      "p99_ns": 282
    }
  }
}
```

Compare CI results against baseline:
- Fail if mean differs by >10%
- Warn if stddev increases significantly
- Update baseline when running on reference hardware

## Implementation Plan

### Phase 1: Core Infrastructure (Week 1)

**Tasks**:
1. Create `security_test.go` with common helpers
2. Implement `failingReader` mock for crypto tests
3. Add `*WithReader()` variants to crypto functions
4. Create timing analysis utilities

**Deliverables**:
- Test helper functions
- Mock implementations
- Timing statistics library

### Phase 2: Authentication & Crypto Tests (Week 1-2)

**Tasks**:
1. Write `security_auth_test.go` (authentication tests)
2. Write `security_crypto_test.go` (error injection tests)
3. Test all crypto error paths
4. Verify error messages and propagation

**Deliverables**:
- 20+ authentication security tests
- 15+ crypto error handling tests
- 100% coverage of crypto failure paths

### Phase 3: Timing Tests (Week 2)

**Tasks**:
1. Write `security_timing_test.go`
2. Implement statistical analysis (K-S test)
3. Create timing benchmarks
4. Calibrate confidence levels

**Deliverables**:
- Timing attack resistance tests
- Statistical analysis library
- Benchmark baseline

### Phase 4: TLS & Authorization Tests (Week 3)

**Tasks**:
1. Write `security_tls_test.go`
2. Write `security_authz_test.go`
3. Test warning suppression
4. Test cross-plugin authorization

**Deliverables**:
- TLS warning tests
- Authorization enforcement tests
- Cross-plugin security tests

### Phase 5: CI Integration (Week 3)

**Tasks**:
1. Create GitHub Actions workflow
2. Set up timing test job (nightly)
3. Configure retry logic for flaky tests
4. Add coverage reporting

**Deliverables**:
- `.github/workflows/security-tests.yml`
- Timing baseline data
- CI documentation

### Phase 6: Fuzzing (Future)

**Tasks**:
1. Write `security_fuzz_test.go` (Go 1.18+ fuzzing)
2. Fuzz token validation
3. Fuzz header parsing
4. Fuzz protobuf messages

**Deliverables**:
- Fuzz tests for critical inputs
- Corpus of test cases
- Fuzzing documentation

## Success Metrics

**Coverage**:
- 100% coverage of security-critical paths
- All crypto error paths tested
- All authentication paths tested
- All authorization enforcement points tested

**Quality**:
- No timing leaks detected (99% confidence)
- All crypto failures handled gracefully
- Clear error messages for security failures
- TLS warnings shown appropriately

**Regression Prevention**:
- Tests fail if constant-time comparison is broken
- Tests fail if crypto errors are not propagated
- Tests fail if TLS warnings are removed
- Tests fail if authentication is bypassed

**CI Integration**:
- Security tests run on every PR
- Timing tests run nightly
- Flaky tests automatically retried
- Coverage tracked over time

## Open Questions

1. **Timing test sensitivity**: What confidence level is acceptable? (Proposed: 99%)
2. **Timing test frequency**: Nightly vs per-commit? (Proposed: nightly for timing, per-commit for others)
3. **Flaky test policy**: How many retries? (Proposed: 3x retry for timing tests)
4. **Baseline updates**: How often to update timing baselines? (Proposed: quarterly)
5. **Fuzzing integration**: When to add fuzzing? (Proposed: Phase 6, after core tests)

## Future Work

1. **Side-channel analysis**: Cache timing, power analysis (out of scope for software)
2. **Penetration testing**: Hire external security auditor
3. **Compliance testing**: FIPS 140-2 validation (if required)
4. **Formal verification**: Prove timing properties formally (research project)
5. **Continuous fuzzing**: OSS-Fuzz integration

## References

- [OWASP: Testing Guide - Authentication](https://owasp.org/www-project-web-security-testing-guide/latest/4-Web_Application_Security_Testing/04-Authentication_Testing/)
- [Go crypto/subtle package](https://pkg.go.dev/crypto/subtle)
- [Timing Attack Prevention](https://codahale.com/a-lesson-in-timing-attacks/)
- [Go Fuzzing Tutorial](https://go.dev/doc/tutorial/fuzz)
- [Kolmogorov-Smirnov Test](https://en.wikipedia.org/wiki/Kolmogorov%E2%80%93Smirnov_test)

## Appendix: Code Locations

**Security-critical code**:
- `handshake.go:189-203` - `ValidateToken()` (constant-time comparison)
- `broker.go:172-179` - Grant token validation (constant-time comparison)
- `handshake.go:209-230` - `generateRuntimeID()`, `generateRandomHex()` (crypto/rand error handling)
- `broker.go:194-211` - `generateGrantID()`, `generateToken()` (crypto/rand error handling)
- `registry.go:530-538` - `generateRegistrationID()` (crypto/rand error handling)
- `client.go:149-151` - TLS warning (client)
- `server.go:160-167` - TLS warning (server)

**Test files to create**:
- `security_auth_test.go` - Authentication security tests
- `security_timing_test.go` - Timing attack tests
- `security_crypto_test.go` - Crypto error handling tests
- `security_tls_test.go` - TLS warning tests
- `security_authz_test.go` - Authorization tests
- `security_fuzz_test.go` - Fuzzing tests (future)

**Test utilities**:
- `internal/testutil/timing.go` - Timing analysis
- `internal/testutil/mocks.go` - Mock implementations
- `internal/testutil/helpers.go` - Test helpers
