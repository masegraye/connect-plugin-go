# Security Best Practices

Practical guide to securing your connect-plugin-go deployment with implemented security features.

## Production-Ready Configuration

### Secure Platform Host

Complete example with all security features enabled:

```go
package main

import (
    "context"
    "log"
    "net/http"
    "time"

    connectplugin "github.com/masegraye/connect-plugin-go"
    "github.com/masegraye/connect-plugin-go/gen/plugin/v1/connectpluginv1connect"
)

func main() {
    // === Security Components ===

    // 1. Rate Limiter (protects against DoS)
    limiter := connectplugin.NewTokenBucketLimiter()
    defer limiter.Close()

    // 2. Capability Broker (if providing host capabilities)
    broker := connectplugin.NewCapabilityBroker("https://platform.example.com")
    broker.RegisterCapability(loggerCapability)
    broker.RegisterCapability(secretsCapability)

    // 3. Platform Infrastructure
    handshake := connectplugin.NewHandshakeServer(&connectplugin.ServeConfig{
        RuntimeTokenTTL:    24 * time.Hour,   // Tokens expire after 24h
        CapabilityGrantTTL: 30 * time.Minute, // Grants expire after 30min
    })
    lifecycle := connectplugin.NewLifecycleServer()
    registry := connectplugin.NewServiceRegistry(lifecycle)
    router := connectplugin.NewServiceRouter(handshake, registry, lifecycle)

    // === HTTP Server Setup ===

    mux := http.NewServeMux()

    // Register platform services
    handshakePath, handshakeHandler := connectpluginv1connect.NewHandshakeServiceHandler(handshake)
    mux.Handle(handshakePath, handshakeHandler)

    lifecyclePath, lifecycleHandler := connectpluginv1connect.NewPluginLifecycleHandler(lifecycle)
    mux.Handle(lifecyclePath, lifecycleHandler)

    registryPath, registryHandler := connectpluginv1connect.NewServiceRegistryHandler(registry)
    mux.Handle(registryPath, registryHandler)

    mux.Handle("/broker/", broker.Handler())
    mux.Handle("/capabilities/", broker.Handler())
    mux.Handle("/services/", router)

    // === Start Server ===

    server := &http.Server{
        Addr:    ":8080",
        Handler: mux,
        // TLS configuration (recommended)
        // TLSConfig: &tls.Config{...},
    }

    log.Println("Secure platform starting on :8080")
    log.Println("  - Runtime token TTL: 24h")
    log.Println("  - Capability grant TTL: 30m")
    log.Println("  - Rate limiting: enabled")

    if err := server.ListenAndServe(); err != nil {
        log.Fatalf("Server error: %v", err)
    }
}
```

### Production Checklist

Before deploying to production:

- ✅ Configure TLS (HTTPS endpoints)
- ✅ Enable rate limiting with appropriate limits
- ✅ Set token TTLs based on security requirements
- ✅ Configure service authorization for sensitive services
- ✅ Validate all plugin inputs
- ✅ Monitor security metrics
- ✅ Set up alerting for security events

## Token Expiration

### Configuring Token Lifetimes

Choose TTLs based on security vs usability tradeoff:

```go
// High security (short-lived tokens)
ServeConfig{
    RuntimeTokenTTL:    1 * time.Hour,    // Re-handshake every hour
    CapabilityGrantTTL: 5 * time.Minute,  // Re-grant every 5 minutes
}

// Balanced (recommended for production)
ServeConfig{
    RuntimeTokenTTL:    24 * time.Hour,   // Daily re-handshake
    CapabilityGrantTTL: 1 * time.Hour,    // Hourly re-grant
}

// Development (longer-lived for convenience)
ServeConfig{
    RuntimeTokenTTL:    7 * 24 * time.Hour,  // Weekly
    CapabilityGrantTTL: 24 * time.Hour,      // Daily
}
```

### Handling Token Expiration

**Plugin best practices:**

```go
// Retry handshake on Unauthenticated error
func connectWithRetry(client *connectplugin.Client) error {
    err := client.Connect(ctx)
    if err != nil {
        // Token may have expired, retry handshake
        if connect.CodeOf(err) == connect.CodeUnauthenticated {
            log.Println("Runtime token expired, re-authenticating...")
            return client.Connect(ctx)
        }
        return err
    }
    return nil
}
```

**Capability grant renewal:**

```go
// Track grant expiration and renew proactively
type CapabilityClient struct {
    grant     *connectplugin.CapabilityGrant
    expiresAt time.Time
    client    *connectplugin.Client
}

func (c *CapabilityClient) ensureGrant(ctx context.Context) error {
    // Renew if expiring within 5 minutes
    if time.Until(c.expiresAt) < 5*time.Minute {
        grant, err := c.client.RequestCapability(ctx, "logger")
        if err != nil {
            return err
        }
        c.grant = grant
        c.expiresAt = time.Now().Add(30 * time.Minute)  // Assumes 30min TTL
    }
    return nil
}
```

## Service Registration Authorization

### Restricting Service Types

Control which plugins can register which services:

```go
registry := connectplugin.NewServiceRegistry(lifecycle)

// During handshake, set allowed services
registry.SetAllowedServices("cache-plugin-x7k2", []string{"cache", "metrics"})
registry.SetAllowedServices("logger-plugin-a3f1", []string{"logger"})
registry.SetAllowedServices("storage-plugin-b9d2", []string{"storage", "backup"})

// If plugin tries to register unauthorized service:
// Error: permission_denied: plugin cache-plugin-x7k2 not authorized to register service type "logger"
```

### Authorization Patterns

**Pattern 1: Explicit Whitelist (Recommended)**

```go
// Only allow specific service types per plugin
allowedServices := map[string][]string{
    "verified-cache-v1": {"cache"},
    "trusted-logger-v2": {"logger", "metrics"},
    "admin-plugin":      {"*"},  // Special case: allow all (use carefully)
}

for runtimeID, services := range allowedServices {
    registry.SetAllowedServices(runtimeID, services)
}
```

**Pattern 2: No Restrictions (Development)**

```go
// Don't call SetAllowedServices() for any runtime ID
// Result: All plugins can register any service type
// Use in development only, not production
```

**Pattern 3: Deny All (Testing)**

```go
// Set empty list to deny all registrations
registry.SetAllowedServices("untrusted-plugin", []string{})

// Plugin cannot register ANY service
// Useful for testing plugin without granting service permissions
```

### Dynamic Authorization

Update authorizations at runtime:

```go
// Grant temporary access
registry.SetAllowedServices("temp-plugin-abc", []string{"cache"})

// Later: revoke access
registry.SetAllowedServices("temp-plugin-abc", []string{})

// Plugin's existing registrations remain (until unregistered)
// But plugin cannot register new services
```

## Input Validation

### Validation Rules

All plugin-provided data is validated:

**Metadata:**
- Max 100 entries
- Keys: max 256 bytes, format: `^[a-zA-Z][a-zA-Z0-9_-]*$`
- Values: max 4096 bytes, no null bytes
- Enforced during: service registration, handshake

**Service Types:**
- Max 128 bytes
- Format: alphanumeric + dash/underscore
- No path traversal: `../`, `/`, `\`
- No null bytes

**Self IDs:**
- Max 128 bytes
- Format: `^[a-zA-Z][a-zA-Z0-9_.-]*$`
- No special characters

**Versions:**
- Max 64 bytes
- Valid semver format: `1.0.0`, `2.1.3-beta`
- No `v` prefix

**Endpoint Paths:**
- Max 256 bytes
- Must start with `/`
- No null bytes

### Validation Errors

When validation fails:

```
Code: InvalidArgument
Message: "metadata key too long: 300 > 256"
```

**Plugin best practices:**
- Validate inputs before sending to platform
- Handle InvalidArgument errors gracefully
- Log validation failures for debugging
- Use constants for service types/versions

### Example: Pre-Validation

```go
// Validate before registering
metadata := map[string]string{
    "provider": "my-cache-impl",
    "version":  "1.0.0",
}

if err := connectplugin.ValidateMetadata(metadata); err != nil {
    return fmt.Errorf("invalid metadata: %w", err)
}

// Now safe to register
req := connect.NewRequest(&connectpluginv1.RegisterServiceRequest{
    ServiceType:  "cache",
    Version:      "1.0.0",
    EndpointPath: "/cache.v1.Cache/",
    Metadata:     metadata,
})
```

## Capability Security

### Securing Sensitive Capabilities

Add additional authorization for sensitive capabilities:

```go
type SecureSecretsCapability struct {
    impl      *SecretsStore
    allowlist map[string]bool  // Runtime IDs allowed to access secrets
}

func (c *SecureSecretsCapability) GetSecret(
    ctx context.Context,
    req *connect.Request[secretsv1.GetSecretRequest],
) (*connect.Response[secretsv1.GetSecretResponse], error) {
    // 1. Get authenticated identity (grant already validated by broker)
    grantID := extractGrantIDFromPath(req.HTTPRequest().URL.Path)

    // 2. Additional authorization check
    runtimeID := c.getRuntimeIDForGrant(grantID)
    if !c.allowlist[runtimeID] {
        return nil, connect.NewError(connect.CodePermissionDenied,
            fmt.Errorf("plugin %s not authorized for secrets", runtimeID))
    }

    // 3. Check secret-level permissions
    if !c.impl.CanAccess(runtimeID, req.Msg.Key) {
        return nil, connect.NewError(connect.CodePermissionDenied,
            fmt.Errorf("access denied to secret %s", req.Msg.Key))
    }

    // 4. Retrieve secret
    value, err := c.impl.GetSecret(req.Msg.Key)
    if err != nil {
        return nil, err
    }

    return connect.NewResponse(&secretsv1.GetSecretResponse{
        Value: value,
    }), nil
}
```

### Capability Access Logging

Log all capability access for audit trail:

```go
type AuditedLoggerCapability struct {
    impl *LoggerCapability
}

func (c *AuditedLoggerCapability) Log(
    ctx context.Context,
    req *connect.Request[loggerv1.LogRequest],
) (*connect.Response[loggerv1.LogResponse], error) {
    // Extract grant ID from request path
    grantID := extractGrantIDFromPath(req.HTTPRequest().URL.Path)

    // Audit log
    auditLog.Record(AuditEvent{
        Timestamp:    time.Now(),
        GrantID:      grantID,
        CapabilityType: "logger",
        Action:       "Log",
        Level:        req.Msg.Level,
        Allowed:      true,
    })

    // Forward to actual implementation
    return c.impl.Log(ctx, req)
}
```

## Defense in Depth

Layer multiple security controls:

```go
// Layer 1: Network (external to connect-plugin-go)
// - TLS encryption (HTTPS)
// - Network policies (Kubernetes)
// - Firewall rules

// Layer 2: Platform Authentication
server := connectplugin.Serve(&connectplugin.ServeConfig{
    Plugins:            pluginSet,
    Impls:              impls,
    RuntimeTokenTTL:    24 * time.Hour,   // Automatic token expiration
    CapabilityGrantTTL: 30 * time.Minute, // Short-lived capability grants
    RateLimiter:        limiter,          // DoS protection
})

// Layer 3: Service Authorization
registry.SetAllowedServices("plugin-a", []string{"cache"})
registry.SetAllowedServices("plugin-b", []string{"logger"})

// Layer 4: Input Validation (automatic)
// - All metadata validated
// - Service types validated
// - Versions validated

// Layer 5: Capability-Level Authorization (custom)
type SecureCapability struct {
    allowlist map[string][]string  // runtime_id → allowed operations
}

func (c *SecureCapability) HandleRequest(...) {
    // Check runtime ID has permission for this operation
    if !c.isAllowed(runtimeID, operation) {
        return connect.NewError(connect.CodePermissionDenied, ...)
    }
    // ...
}
```

## Security Monitoring

### Metrics to Track

Implement monitoring for security events:

```go
type SecurityMetrics struct {
    HandshakeAttempts       prometheus.Counter
    HandshakeFailures       prometheus.Counter
    TokenExpirations        prometheus.Counter
    RateLimitRejections     *prometheus.CounterVec  // Labels: runtime_id
    UnauthorizedRegistrations prometheus.Counter
    InvalidInputs           prometheus.Counter
    CapabilityGrantRequests *prometheus.CounterVec  // Labels: capability_type
    CapabilityDenials       prometheus.Counter
}

// Track in interceptors/handlers
func (h *HandshakeServer) Handshake(...) {
    metrics.HandshakeAttempts.Inc()

    result, err := h.doHandshake(req)
    if err != nil {
        metrics.HandshakeFailures.Inc()
        return nil, err
    }

    return result, nil
}
```

### Alerting Rules

Set up alerts for security events:

```yaml
# Prometheus alerts
groups:
  - name: plugin_security
    rules:
      - alert: HighHandshakeFailureRate
        expr: rate(handshake_failures[5m]) > 10
        annotations:
          summary: "High handshake failure rate detected"

      - alert: RateLimitingActive
        expr: rate(rate_limit_rejections[5m]) > 100
        annotations:
          summary: "Plugin being rate limited"

      - alert: UnauthorizedRegistrationAttempt
        expr: increase(unauthorized_registrations[1h]) > 0
        annotations:
          summary: "Plugin attempted unauthorized service registration"

      - alert: TokenExpirationSpike
        expr: rate(token_expirations[5m]) > 5
        annotations:
          summary: "Multiple plugins losing tokens (possible DoS or clock skew)"
```

### Audit Logging

Log security-relevant events:

```go
type AuditLogger struct {
    writer io.Writer
}

func (a *AuditLogger) LogSecurityEvent(event SecurityEvent) {
    entry := map[string]interface{}{
        "timestamp":  time.Now().UTC(),
        "event_type": event.Type,
        "runtime_id": event.RuntimeID,
        "action":     event.Action,
        "result":     event.Result,  // "allowed" or "denied"
        "reason":     event.Reason,
    }

    json.NewEncoder(a.writer).Encode(entry)
}

// Usage in handlers
func (r *ServiceRegistry) RegisterService(...) {
    runtimeID := req.Header().Get("X-Plugin-Runtime-ID")

    // Check authorization
    if !r.isAuthorized(runtimeID, serviceType) {
        auditLogger.LogSecurityEvent(SecurityEvent{
            Type:      "service_registration",
            RuntimeID: runtimeID,
            Action:    fmt.Sprintf("register %s", serviceType),
            Result:    "denied",
            Reason:    "not in allowed services list",
        })
        return nil, connect.NewError(connect.CodePermissionDenied, ...)
    }

    auditLogger.LogSecurityEvent(SecurityEvent{
        Type:      "service_registration",
        RuntimeID: runtimeID,
        Action:    fmt.Sprintf("register %s", serviceType),
        Result:    "allowed",
    })

    // Proceed with registration...
}
```

## Common Security Patterns

### Pattern 1: Tiered Plugin Trust

Different trust levels for different plugins:

```go
// Tier 1: Fully trusted (internal plugins)
registry.SetAllowedServices("internal-auth", []string{"*"})  // All services
broker.SetGrantTTL("internal-auth", 24 * time.Hour)          // Long-lived grants

// Tier 2: Trusted third-party
registry.SetAllowedServices("partner-cache", []string{"cache"})
broker.SetGrantTTL("partner-cache", 1 * time.Hour)

// Tier 3: Untrusted/sandboxed
registry.SetAllowedServices("untrusted-plugin", []string{})  // No registrations
// Don't provide capability access
```

### Pattern 2: Graduated Trust

Start plugins with limited access, grant more over time:

```go
// On initial handshake: minimal permissions
registry.SetAllowedServices(runtimeID, []string{})

// After health checks pass: grant read-only services
if pluginHealthy(runtimeID) {
    registry.SetAllowedServices(runtimeID, []string{"config-reader"})
}

// After validation period: grant full permissions
if pluginValidated(runtimeID) {
    registry.SetAllowedServices(runtimeID, []string{"cache", "logger", "metrics"})
}
```

### Pattern 3: Capability Scoping

Limit capability access to specific operations:

```go
type ScopedSecretsCapability struct {
    impl *SecretsStore
}

func (c *ScopedSecretsCapability) GetSecret(ctx context.Context, req *Request) (*Response, error) {
    runtimeID := getRuntimeIDFromGrant(req)

    // Scope 1: Plugin can only access its own secrets
    if !strings.HasPrefix(req.Msg.Key, runtimeID+"/") {
        return nil, connect.NewError(connect.CodePermissionDenied,
            fmt.Errorf("can only access secrets under %s/", runtimeID))
    }

    // Scope 2: Prevent access to system secrets
    if strings.HasPrefix(req.Msg.Key, "system/") {
        return nil, connect.NewError(connect.CodePermissionDenied,
            fmt.Errorf("system secrets not accessible"))
    }

    return c.impl.GetSecret(req.Msg.Key)
}
```

## Incident Response

### Detecting Compromise

Signs a plugin may be compromised:

- Sudden spike in rate limit rejections
- Unauthorized registration attempts
- Unusual capability grant requests
- Authentication failures
- Connection from unexpected IPs
- Service registration for unexpected types

### Response Procedure

**Step 1: Isolate**
```go
// Immediately revoke plugin's service authorizations
registry.SetAllowedServices(suspiciousRuntimeID, []string{})

// Revoke capability grants (future enhancement)
broker.RevokeGrants(suspiciousRuntimeID)

// Stop routing traffic to plugin
lifecycle.SetHealthState(suspiciousRuntimeID,
    connectpluginv1.HealthState_HEALTH_STATE_UNHEALTHY,
    "security incident - plugin isolated")
```

**Step 2: Investigate**
- Review audit logs for suspicious activity
- Check which services the plugin accessed
- Identify any data exfiltration
- Determine compromise vector

**Step 3: Remediate**
- Rotate all affected tokens
- Update plugin binary if compromised
- Patch vulnerability that allowed compromise
- Update security policies

**Step 4: Restore**
```go
// After verification, restore access
registry.SetAllowedServices(runtimeID, originalPermissions)
lifecycle.SetHealthState(runtimeID,
    connectpluginv1.HealthState_HEALTH_STATE_HEALTHY, "")
```

## Testing Security

### Security Test Suite

Run security tests before deployment:

```bash
# Run all security-specific tests
task test:security

# Tests include:
# - Timing attack resistance
# - Token expiration enforcement
# - Service authorization
# - Input validation
# - Rate limiting behavior
```

### Penetration Testing

Test security in realistic scenarios:

```go
// Test 1: Attempt token replay
func TestSecurity_TokenReplay(t *testing.T) {
    // 1. Capture valid token
    // 2. Wait for token to expire
    // 3. Attempt reuse
    // 4. Verify rejection
}

// Test 2: Attempt unauthorized registration
func TestSecurity_UnauthorizedRegistration(t *testing.T) {
    // 1. Get runtime token
    // 2. Attempt to register unauthorized service
    // 3. Verify PermissionDenied error
}

// Test 3: Rate limit bypass attempt
func TestSecurity_RateLimitBypass(t *testing.T) {
    // 1. Make requests at sustained high rate
    // 2. Verify ResourceExhausted after burst
    // 3. Verify cannot bypass by changing headers
}
```

## Migration from Insecure Setup

### Phase 1: Add Monitoring (No Breaking Changes)

```go
// Enable TLS warnings (already default)
// Add security metrics
// Monitor for issues
```

### Phase 2: Enable Rate Limiting

```go
// Add rate limiter with permissive limits
limiter := connectplugin.NewTokenBucketLimiter()
ServeConfig{
    RateLimiter: limiter,
    // Start with high limits
}

// Monitor rate limit rejections
// Tune limits based on actual traffic
```

### Phase 3: Enable Service Authorization

```go
// Set allowed services for all known plugins
for _, plugin := range knownPlugins {
    registry.SetAllowedServices(plugin.RuntimeID, plugin.AllowedServices)
}

// New plugins will be denied by default (fail-safe)
```

### Phase 4: Reduce Token TTLs

```go
// Gradually reduce from long-lived to production values
// Week 1: 7 days
RuntimeTokenTTL: 7 * 24 * time.Hour

// Week 2: 3 days
RuntimeTokenTTL: 3 * 24 * time.Hour

// Week 3: 1 day (production target)
RuntimeTokenTTL: 24 * time.Hour
```

### Phase 5: Enable TLS

```go
// Configure TLS
server := &http.Server{
    Addr:    ":443",
    Handler: mux,
    TLSConfig: &tls.Config{
        Certificates: []tls.Certificate{cert},
        MinVersion:   tls.VersionTLS13,
    },
}

server.ListenAndServeTLS("", "")
```

## Additional Resources

- [Security Guide](../security.md) - Complete security overview
- [Rate Limiting Guide](rate-limiting.md) - Rate limiter details
- [Configuration Reference](../reference/configuration.md) - All options
- [Interceptors Guide](interceptors.md) - Authentication patterns

## Security Contact

Report security vulnerabilities to: [to be configured]

Include:
- Vulnerability description
- Reproduction steps
- Impact assessment
- Suggested fix (optional)

Response SLA: 48 hours
