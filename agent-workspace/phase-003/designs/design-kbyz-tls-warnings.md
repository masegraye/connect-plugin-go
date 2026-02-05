# Design: TLS Enforcement Warnings

**Finding:** PROTO-CRIT-003
**Severity:** CRITICAL
**Document ID:** design-kbyz-tls-warnings

## Problem Statement

The connect-plugin-go framework allows clients and servers to operate without TLS encryption. All HTTP communication transmits credentials and plugin data in plaintext when using `http://` endpoints. This creates a critical security vulnerability enabling:

- **Man-in-the-Middle (MITM) attacks** on credentials during handshake
- **Interception of runtime tokens** used for authentication
- **Eavesdropping on plugin data** transmitted between host and plugins
- **Plaintext transmission of secrets** if plugins handle sensitive data

The framework currently provides no warnings when operating in this insecure mode, making it easy for operators to unknowingly expose credentials.

## Goal

Implement an operational warning system that:
1. Detects non-TLS connections at runtime
2. Emits clear warnings at appropriate points
3. Allows opt-out for testing/development environments
4. Does NOT enforce TLS (warnings only, for operational awareness)

## Warning Points

### Client-Side Warnings

**Location:** `client.go` - `Connect()` method
**Trigger:** When HTTP client is created with non-TLS endpoint
**Timing:** At explicit `Connect()` call or first `Dispense()` (lazy connection)
**Severity:** WARN

```
Warning emitted when:
- Endpoint starts with "http://" (not "https://")
- Endpoint uses non-standard ports with http:// (not port 443)
```

**Message Template:**
```
WARN: Plugin client connecting to non-TLS endpoint
  endpoint: http://localhost:8080
  risk: Credentials and plugin data will be transmitted in plaintext
  recommendation: Use HTTPS endpoint in production
  suppress: Set CONNECTPLUGIN_DISABLE_TLS_WARNING=1
```

### Server-Side Warnings

**Location:** `server.go` - `Serve()` function
**Trigger:** When HTTP server starts without TLS configuration
**Timing:** At server startup, before `ListenAndServe()` call
**Severity:** WARN

```
Warning emitted when:
- ServeConfig has no TLSConfig field (assumed missing)
- Server is about to listen on non-HTTPS port
- Handshake will transmit runtime tokens in plaintext
```

**Message Template:**
```
WARN: Plugin server starting without TLS encryption
  address: :8080
  risk: Runtime tokens and credentials will be transmitted in plaintext
  recommendation: Configure TLS/HTTPS in production
  suppress: Set CONNECTPLUGIN_DISABLE_TLS_WARNING=1
```

### Discovery Service Warnings

**Location:** `client.go` - `discoverEndpoint()` method
**Trigger:** When discovered endpoint is non-TLS
**Timing:** After endpoint discovery, before first connection
**Severity:** WARN

```
Warning emitted when:
- Service discovery returns http:// endpoint
- Dynamic discovery enables insecure connection silently
```

**Message Template:**
```
WARN: Service discovery returned non-TLS endpoint
  service: plugin-host
  endpoint: http://discovery.example.com:9090
  risk: Plugin connection will be unencrypted
  recommendation: Ensure discovery service returns HTTPS endpoints
  suppress: Set CONNECTPLUGIN_DISABLE_TLS_WARNING=1
```

## Detection Logic

### Client-Side Detection

```go
// Check endpoint URL scheme
func isNonTLSEndpoint(endpoint string) bool {
    parsedURL, err := url.Parse(endpoint)
    if err != nil {
        // Parse error - warn about invalid URL
        return true
    }

    // Warn for http:// or file:// schemes
    return parsedURL.Scheme == "http" || parsedURL.Scheme == "file"
}
```

**Special Cases:**

1. **Localhost Exception:**
   - Still warn on `http://localhost:*` (credentials are at risk on shared systems)
   - Only suppress warning if explicitly configured
   - Do NOT treat localhost as inherently secure

2. **Unix Sockets:**
   - No TLS needed for `unix:///var/run/socket.sock` (kernel-enforced isolation)
   - Detect with scheme `unix://`
   - Skip warning for unix domain sockets
   - Pattern: `unix:///path` or `unix://@abstract-socket`

3. **Testing Framework:**
   - Ports like 9999, 9998, 9997 commonly used for testing
   - Still warn (no special treatment for test ports)
   - Rely on `CONNECTPLUGIN_DISABLE_TLS_WARNING` for test environments

4. **Port Analysis:**
   - Port 443 → likely HTTPS even without explicit scheme
   - Port 8080, 9090, custom ports → likely HTTP
   - Warn unless scheme explicitly states https://

### Server-Side Detection

```go
// Check if TLS is configured
func isNonTLSServer(srv *http.Server) bool {
    // In Go, http.Server without TLS = plain HTTP
    return srv.TLSConfig == nil
}
```

**Current Limitation:**

The connect-plugin-go `Serve()` function currently:
- Creates plain `http.Server{}`
- Does NOT accept `TLSConfig` parameter
- No TLS configuration path exists yet

**Detection Strategy:**

Since `ServeConfig` has no TLS field, assume any call to `Serve()` is unencrypted:
- Always warn when `Serve()` is called
- Future: Add optional `TLSConfig` field to suppress warning when TLS is configured

**Edge Cases:**

1. **Reverse Proxy Scenario:**
   - Server runs on `http://localhost:9000` (internal)
   - Fronted by nginx/Envoy doing TLS termination
   - Server still exposes plaintext internally
   - Warn anyway (internal endpoint can still be intercepted)

2. **Network Isolation:**
   - Server on `http://10.0.0.5:8080` (private network)
   - Still warn (network boundaries are fragile)
   - Network isolation is not cryptographic

3. **Multi-Listener Scenario:**
   - If plugin supports multiple listeners (HTTP + HTTPS)
   - Warn about any HTTP listener
   - Recommend pure HTTPS-only deployment

## Warning Messages

### Message Format

All warnings follow this structure:

```
WARN [connectplugin]: <description>
  key: value
  key: value
  ...
```

### Client Connection Warning

```
WARN [connectplugin]: Non-TLS plugin endpoint
  endpoint: http://192.168.1.100:8080
  impact: credentials/tokens/plugin-data transmitted in plaintext
  risk: Man-in-the-middle attacks, credential theft
  resolution: Use https:// endpoint or configure TLS in client
  environment: CONNECTPLUGIN_DISABLE_TLS_WARNING=1 (for testing only)
```

### Server Startup Warning

```
WARN [connectplugin]: Plugin server running without TLS
  address: 0.0.0.0:8080
  impact: runtime tokens transmitted in plaintext
  risk: Compromised tokens enable unauthorized plugin control
  resolution: Configure HTTPS listener or enable TLS in ServeConfig
  environment: CONNECTPLUGIN_DISABLE_TLS_WARNING=1 (for testing only)
```

### Discovery Warning

```
WARN [connectplugin]: Service discovery returned non-TLS endpoint
  service: plugin-host
  endpoint: http://discovery.local:8080
  impact: plugin communication will be unencrypted
  risk: Man-in-the-middle attacks on plugin registration/service calls
  resolution: Configure discovery service to return https:// endpoints
  environment: CONNECTPLUGIN_DISABLE_TLS_WARNING=1 (for testing only)
```

## Configuration

### Environment Variable: CONNECTPLUGIN_DISABLE_TLS_WARNING

**Default:** Not set (warnings enabled)
**Type:** String boolean (`"1"`, `"true"`, `"yes"` all valid)
**Scope:** Process-wide (checked once at each warning point)

**Usage:**

```bash
# Disable all TLS warnings (testing/development)
export CONNECTPLUGIN_DISABLE_TLS_WARNING=1

# Run with non-TLS
go run examples/kv/host/main.go

# Server with warnings disabled
export CONNECTPLUGIN_DISABLE_TLS_WARNING=1
go run examples/kv/server-with-logger/main.go
```

**Check Implementation:**

```go
func tlsWarningsDisabled() bool {
    val := os.Getenv("CONNECTPLUGIN_DISABLE_TLS_WARNING")
    return val == "1" || strings.ToLower(val) == "true" || strings.ToLower(val) == "yes"
}
```

### Logging Mechanism

**Strategy:** Use Go's standard `log` package for warnings

```go
import "log"

func warnNonTLS(endpoint string) {
    if tlsWarningsDisabled() {
        return
    }

    log.Printf("WARN [connectplugin]: Non-TLS endpoint detected\n"+
        "  endpoint: %s\n"+
        "  risk: Plaintext transmission of credentials\n"+
        "  suppress: CONNECTPLUGIN_DISABLE_TLS_WARNING=1\n",
        endpoint)
}
```

**Why standard library `log`?**
- No external dependencies
- Works with any logger (integrates with Logrus, Zap, slog, etc.)
- Operators can redirect via standard mechanisms
- Visible by default (stderr)

**Integration with Custom Loggers:**

Applications can redirect standard log output:

```go
import (
    "io"
    "log"
    "os"
)

// Capture warnings for custom processing
logger := log.New(customWriter, "WARN [connectplugin]: ", log.Ldate|log.Ltime)
```

## Edge Cases and Justifications

### 1. Localhost is NOT Exception

**Decision:** Warn even for `localhost` and `127.0.0.1`

**Justification:**
- Shared systems (containers, VMs) expose localhost to other processes
- Kubernetes: localhost in pods can access other containers
- Development machines: other local users/processes can intercept
- Credentials are at risk even on localhost

**Example:**
```go
// ALWAYS warn for localhost
if endpoint == "http://localhost:8080" {
    warnNonTLS(endpoint)  // YES, still warn
}
```

### 2. Unix Sockets - Conditional Exceptions

**Decision:** No warning for `unix://` scheme (kernel-enforced isolation)

**Justification:**
- Unix sockets use filesystem permissions (kernel-enforced)
- No network layer = no MITM possible
- Common for local plugin communication

**Implementation:**

```go
func isNonTLSEndpoint(endpoint string) bool {
    u, _ := url.Parse(endpoint)

    // Unix sockets are cryptographically isolated by kernel
    if u.Scheme == "unix" || u.Scheme == "unixgram" {
        return false  // No warning needed
    }

    return u.Scheme == "http"
}
```

### 3. Testing Environments

**Decision:** No special "test port" exceptions - use `CONNECTPLUGIN_DISABLE_TLS_WARNING`

**Justification:**
- Port numbers are unreliable indicators of test vs. production
- Tests may run on 8080 in CI/CD pipelines
- E2E tests should respect security practices
- Unit tests don't use network (in-memory strategy)
- Explicit env var makes test setup intentional and visible

**Example Test Setup:**

```bash
# test-server.sh
#!/bin/bash
export CONNECTPLUGIN_DISABLE_TLS_WARNING=1
export CONNECTPLUGIN_INSECURE=1  # If future enforcement added
go run ./examples/kv/server-with-logger/main.go &
sleep 1

# Run tests
go test ./...

# Cleanup
kill %1
```

### 4. Load Balancer / Reverse Proxy Scenarios

**Decision:** Still warn about internal plaintext connections

**Justification:**
- TLS termination at boundary doesn't protect internal traffic
- Internal networks can still be compromised
- Operator should use TLS end-to-end
- Future: mTLS between components

**Example:**
```
nginx (HTTPS:443)
    ↓ (plaintext :8080)
plugin-server
```

Even with nginx handling TLS, we warn about the internal `http://localhost:8080` channel.

### 5. Multi-Address Binding

**Decision:** Warn separately for each non-TLS address

**Current Implementation:**

`Serve()` accepts single `Addr` string, so single warning.

**If Future Multi-Listener Support:**

```go
// Future: Array of listeners
type ServeConfig struct {
    Listeners []ListenerConfig  // TLS + Addr pairs
}

type ListenerConfig struct {
    Addr      string
    TLSConfig *tls.Config  // nil = plaintext
}

// Warn for each plaintext listener
for _, listener := range cfg.Listeners {
    if listener.TLSConfig == nil {
        warnNonTLS(listener.Addr)
    }
}
```

## Implementation Plan

### Phase 1: Client Warnings (client.go)

**File:** `/Users/masegraye/workspaces/2026-01-29/connect-plugin-review/connect-plugin-go/client.go`

**Location:** In `Connect()` method, after line 143

```go
// After discovering endpoint, before creating HTTP client
if !tlsWarningsDisabled() {
    if isNonTLSEndpoint(c.cfg.Endpoint) {
        warnNonTLSEndpoint(c.cfg.Endpoint)
    }
}

// Create HTTP client for Connect RPCs
c.httpClient = &http.Client{}
```

**New Helper Functions:**

```go
// tlsWarningsDisabled checks if TLS warnings are suppressed
func tlsWarningsDisabled() bool {
    val := os.Getenv("CONNECTPLUGIN_DISABLE_TLS_WARNING")
    return val == "1" || strings.ToLower(val) == "true" || strings.ToLower(val) == "yes"
}

// isNonTLSEndpoint checks if endpoint uses plaintext HTTP
func isNonTLSEndpoint(endpoint string) bool {
    u, err := url.Parse(endpoint)
    if err != nil {
        // Invalid URL - warn cautiously
        return true
    }

    // Unix sockets are kernel-isolated, no TLS needed
    if u.Scheme == "unix" || u.Scheme == "unixgram" {
        return false
    }

    // Warn for plaintext schemes
    return u.Scheme == "http"
}

// warnNonTLSEndpoint emits a warning about plaintext endpoint
func warnNonTLSEndpoint(endpoint string) {
    log.Printf("WARN [connectplugin]: Non-TLS plugin endpoint\n"+
        "  endpoint: %s\n"+
        "  impact: credentials/tokens/plugin-data transmitted in plaintext\n"+
        "  risk: Man-in-the-middle attacks, credential theft\n"+
        "  resolution: Use https:// endpoint or configure TLS in client\n"+
        "  suppress: CONNECTPLUGIN_DISABLE_TLS_WARNING=1 (testing only)\n",
        endpoint)
}
```

### Phase 2: Server Warnings (server.go)

**File:** `/Users/masegraye/workspaces/2026-01-29/connect-plugin-review/connect-plugin-go/server.go`

**Location:** In `Serve()` function, after line 156 (after validation)

```go
// Validate configuration
if err := cfg.Validate(); err != nil {
    return err
}

// Warn if running without TLS
if !tlsWarningsDisabled() {
    warnServerWithoutTLS(cfg.Addr)
}

// Build the HTTP mux
mux := http.NewServeMux()
```

**New Helper Function:**

```go
// warnServerWithoutTLS emits a warning about plaintext server
func warnServerWithoutTLS(addr string) {
    log.Printf("WARN [connectplugin]: Plugin server running without TLS\n"+
        "  address: %s\n"+
        "  impact: runtime tokens transmitted in plaintext\n"+
        "  risk: Compromised tokens enable unauthorized plugin control\n"+
        "  resolution: Configure HTTPS listener or enable TLS in ServeConfig\n"+
        "  suppress: CONNECTPLUGIN_DISABLE_TLS_WARNING=1 (testing only)\n",
        addr)
}
```

### Phase 3: Discovery Warnings (client.go)

**File:** `/Users/masegraye/workspaces/2026-01-29/connect-plugin-review/connect-plugin-go/client.go`

**Location:** In `discoverEndpoint()` method, after line 179

```go
// Use first endpoint (TODO: Add selection strategy)
c.cfg.Endpoint = endpoints[0].URL
c.cfg.HostURL = endpoints[0].URL

// Warn if discovered endpoint is non-TLS
if !tlsWarningsDisabled() {
    if isNonTLSEndpoint(endpoints[0].URL) {
        warnDiscoveredEndpoint(serviceName, endpoints[0].URL)
    }
}

return nil
```

**New Helper Function:**

```go
// warnDiscoveredEndpoint emits a warning about plaintext discovered endpoint
func warnDiscoveredEndpoint(serviceName, endpoint string) {
    log.Printf("WARN [connectplugin]: Service discovery returned non-TLS endpoint\n"+
        "  service: %s\n"+
        "  endpoint: %s\n"+
        "  impact: plugin communication will be unencrypted\n"+
        "  risk: Man-in-the-middle attacks on plugin registration/service calls\n"+
        "  resolution: Configure discovery service to return https:// endpoints\n"+
        "  suppress: CONNECTPLUGIN_DISABLE_TLS_WARNING=1 (testing only)\n",
        serviceName, endpoint)
}
```

## Testing Strategy

### Unit Tests

```go
// Test TLS warning detection
func TestIsNonTLSEndpoint(t *testing.T) {
    tests := []struct {
        endpoint string
        wantWarn bool
    }{
        {"http://localhost:8080", true},
        {"https://example.com", false},
        {"unix:///var/run/socket.sock", false},
        {"file:///etc/config", true},
    }

    for _, tt := range tests {
        got := isNonTLSEndpoint(tt.endpoint)
        if got != tt.wantWarn {
            t.Errorf("isNonTLSEndpoint(%q) = %v, want %v",
                tt.endpoint, got, tt.wantWarn)
        }
    }
}

// Test suppression via environment variable
func TestTLSWarningsSuppressed(t *testing.T) {
    os.Setenv("CONNECTPLUGIN_DISABLE_TLS_WARNING", "1")
    defer os.Unsetenv("CONNECTPLUGIN_DISABLE_TLS_WARNING")

    if !tlsWarningsDisabled() {
        t.Error("tlsWarningsDisabled() = false, want true")
    }
}
```

### Integration Tests

1. **Client Connection Warnings:**
   - Start server on `http://localhost:8080`
   - Create client connecting to it
   - Verify warning appears in stderr

2. **Server Startup Warnings:**
   - Start server with no TLS config
   - Verify warning appears in startup logs

3. **Discovery Warnings:**
   - Mock discovery service returning `http://` endpoint
   - Connect client
   - Verify warning appears

4. **Suppression Test:**
   - Run each scenario with `CONNECTPLUGIN_DISABLE_TLS_WARNING=1`
   - Verify no warnings appear

### Test Harness

```bash
# Test client warnings
export CONNECTPLUGIN_DISABLE_TLS_WARNING=1
go test ./... -v -run TestNonTLSClientWarning

# Test server warnings (temporarily enable to see warnings)
unset CONNECTPLUGIN_DISABLE_TLS_WARNING
go test ./... -v -run TestNonTLSServerWarning 2>&1 | grep "WARN"

# Verify suppression works
export CONNECTPLUGIN_DISABLE_TLS_WARNING=1
go test ./... -v -run TestNonTLSServerWarning 2>&1 | grep -c "WARN"  # Should be 0
```

## Future Considerations

### 1. TLS Configuration Support

Once implemented, extend `ServeConfig`:

```go
type ServeConfig struct {
    // ... existing fields ...

    // TLS configuration for HTTPS
    TLSConfig *tls.Config  // nil = plaintext HTTP

    // Enforce TLS (optional, future)
    // RequireTLS bool  // Return error if TLS not configured
}
```

Suppress warning when `TLSConfig` is set:

```go
if cfg.TLSConfig != nil {
    // TLS configured, no warning
} else if !tlsWarningsDisabled() {
    warnServerWithoutTLS(cfg.Addr)
}
```

### 2. Client-Side TLS Configuration

Support for client-provided TLS:

```go
type ClientConfig struct {
    // ... existing fields ...

    // HTTPClient can be provided with custom TLS
    HTTPClient *http.Client  // Pre-configured client
}

// In Connect():
if c.cfg.HTTPClient != nil {
    c.httpClient = c.cfg.HTTPClient
} else {
    c.httpClient = &http.Client{}
}
```

Suppress warning if custom client provided with TLS:

```go
if c.cfg.HTTPClient != nil {
    // Assume custom client has TLS if provided
} else if isNonTLSEndpoint(c.cfg.Endpoint) {
    warnNonTLSEndpoint(c.cfg.Endpoint)
}
```

### 3. Structured Logging Integration

Support structured logging frameworks:

```go
// Future: slog integration (Go 1.21+)
import "log/slog"

func warnNonTLSEndpoint(endpoint string) {
    slog.Warn("Non-TLS endpoint detected",
        "endpoint", endpoint,
        "impact", "plaintext transmission of credentials",
        "suppress", "CONNECTPLUGIN_DISABLE_TLS_WARNING=1")
}
```

### 4. Metrics/Observability

Count warnings emitted:

```go
var (
    nonTLSWarningsTotal = prometheus.NewCounterVec(
        prometheus.CounterOpts{
            Name: "connectplugin_non_tls_warnings_total",
            Help: "Number of non-TLS warnings emitted",
        },
        []string{"context"},  // "client", "server", "discovery"
    )
)

func warnNonTLSEndpoint(endpoint string) {
    nonTLSWarningsTotal.WithLabelValues("client").Inc()
    // ... emit warning ...
}
```

## Security Notes

### What This Does NOT Do

- **Does not enforce TLS** - operators can still connect insecurely
- **Does not validate certificates** - HTTPS without cert validation can still be MITM'd
- **Does not prevent plaintext transmission** - only warns operators

### What This Does

- **Raises awareness** - operators see plaintext risk clearly
- **Documents risk** - warning explains credential exposure
- **Enables intentional choices** - opt-out via env var is explicit
- **Sets foundation** - prepares for future TLS enforcement

### Recommended Next Steps

1. **Implement TLS support** in `ServeConfig` (certificate loading)
2. **Add client TLS validation** (certificate pinning/CA verification)
3. **Deprecate insecure mode** (version N+1: warn → error)
4. **Enforce HTTPS-only** (version N+2: no more plaintext support)

## References

- **Finding:** PROTO-CRIT-003 - Missing TLS Enforcement
- **OWASP:** CWE-295 (Improper Certificate Validation)
- **OWASP:** CWE-319 (Cleartext Transmission of Sensitive Data)
- **Go:** `net/http` TLS Configuration
- **Go:** `net/url` scheme parsing

## Appendix: Example Warnings

### Client Connecting to Plaintext Server

```bash
$ go run examples/kv/host/main.go
WARN [connectplugin]: Non-TLS plugin endpoint
  endpoint: http://localhost:8080
  impact: credentials/tokens/plugin-data transmitted in plaintext
  risk: Man-in-the-middle attacks, credential theft
  resolution: Use https:// endpoint or configure TLS in client
  suppress: CONNECTPLUGIN_DISABLE_TLS_WARNING=1 (testing only)
Creating plugin client...
Connecting to plugin...
[... rest of client execution ...]
```

### Server Starting Without TLS

```bash
$ go run examples/kv/server-with-logger/main.go
WARN [connectplugin]: Plugin server running without TLS
  address: :8080
  impact: runtime tokens transmitted in plaintext
  risk: Compromised tokens enable unauthorized plugin control
  resolution: Configure HTTPS listener or enable TLS in ServeConfig
  suppress: CONNECTPLUGIN_DISABLE_TLS_WARNING=1 (testing only)
Starting KV plugin server with logger capability on :8080
Registered capability: logger v1.0.0
Server running...
```

### Suppressed Warnings (Testing)

```bash
$ CONNECTPLUGIN_DISABLE_TLS_WARNING=1 go run examples/kv/host/main.go
Creating plugin client...
[No warning emitted]
Connecting to plugin...
[... client execution without warnings ...]
```
