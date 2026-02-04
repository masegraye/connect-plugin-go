# Security Deep Dive: Plugin Launcher and Platform System

**Audit Date:** 2026-01-29
**Auditor:** Claude Code Security Analysis
**Scope:** `launcher.go`, `launch_strategy.go`, `launch_process.go`, `launch_inmemory.go`, `platform.go`
**Related Files:** `handshake.go`, `broker.go`, `registry.go`, `router.go`, `internal/depgraph/depgraph.go`

---

## Executive Summary

This security audit examines the plugin launcher and platform subsystem of connect-plugin-go, focusing on process isolation, authentication, authorization, and resource management. The system enables launching plugins either as child processes or in-memory goroutines, with a centralized platform managing plugin lifecycle, dependency graphs, and service routing.

**Overall Risk Assessment: HIGH**

The system presents several significant security concerns, particularly around:
- Lack of process isolation and sandboxing
- Weak authentication in service registration
- Missing authorization controls for platform operations
- Environment variable injection vulnerabilities
- Insufficient input validation on binary paths

---

## 1. Process Isolation (or Lack Thereof)

### Finding 1.1: No OS-Level Process Isolation
**Severity: HIGH**
**Location:** `launch_process.go:45-55`

```go
cmd := exec.CommandContext(ctx, spec.BinaryPath)
cmd.Env = append(os.Environ(),
    fmt.Sprintf("PORT=%d", spec.Port),
    fmt.Sprintf("HOST_URL=%s", hostURL),
)
cmd.Stdout = os.Stdout
cmd.Stderr = os.Stderr
```

**Issue:** Plugin processes are launched with full access to the host environment:
- Inherits all host environment variables via `os.Environ()`
- No namespace isolation (Linux namespaces not used)
- No seccomp filters or AppArmor profiles
- No resource limits (cgroups)
- No filesystem isolation (chroot/containers)
- Stdout/stderr connected directly to host

**Impact:** A malicious plugin can:
- Access sensitive host environment variables (AWS credentials, API keys, database passwords)
- Consume unlimited CPU/memory resources (DoS)
- Access host filesystem without restrictions
- Monitor other processes
- Make arbitrary network connections

**Recommendation:**
1. Use container runtimes (Docker, containerd) for plugin isolation
2. Apply resource limits via cgroups
3. Filter environment variables to only pass required values
4. Consider seccomp profiles to restrict syscalls
5. Implement network namespace isolation

---

### Finding 1.2: In-Memory Plugin Shares Host Process
**Severity: HIGH**
**Location:** `launch_inmemory.go:36-149`

**Issue:** In-memory plugins run as goroutines within the same process as the host:
- No memory isolation between plugins
- Plugins can access shared memory, global variables
- A crash in one plugin crashes the entire host
- Plugins can interfere with Go runtime (GC, scheduler)

**Impact:**
- Complete compromise of host application
- Data leakage between plugins via shared memory
- Denial of service via panic or resource exhaustion
- Race conditions affecting other plugins

**Recommendation:**
1. Document in-memory strategy as "development/testing only"
2. Never use in-memory for untrusted plugins
3. Consider using process strategy as default for production

---

## 2. Environment Variable Injection Security

### Finding 2.1: Host Environment Leakage
**Severity: HIGH**
**Location:** `launch_process.go:46`

```go
cmd.Env = append(os.Environ(), ...)
```

**Issue:** The entire host environment is passed to plugin processes.

**Impact:** Sensitive environment variables are exposed:
- `AWS_ACCESS_KEY_ID`, `AWS_SECRET_ACCESS_KEY`
- Database connection strings (`DATABASE_URL`)
- API tokens and secrets
- Internal service URLs

**Recommendation:**
```go
// Allow-list approach instead of inherit-all
allowedVars := []string{"PATH", "HOME", "USER", "LANG"}
env := make([]string, 0, len(allowedVars)+2)
for _, key := range allowedVars {
    if val := os.Getenv(key); val != "" {
        env = append(env, fmt.Sprintf("%s=%s", key, val))
    }
}
env = append(env, fmt.Sprintf("PORT=%d", spec.Port))
env = append(env, fmt.Sprintf("HOST_URL=%s", hostURL))
cmd.Env = env
```

---

### Finding 2.2: No Validation of HOST_URL
**Severity: MEDIUM**
**Location:** `launch_process.go:40-43`

```go
hostURL := spec.HostURL
if hostURL == "" {
    hostURL = "http://localhost:8080"  // Default
}
```

**Issue:** `HostURL` is used directly without validation. A malicious configuration could:
- Inject command characters if URL is ever used in shell context
- Point plugins to malicious hosts
- Use file:// or other dangerous URL schemes

**Recommendation:**
1. Validate URL scheme (http/https only)
2. Validate hostname format
3. Consider restricting to localhost or internal networks

---

## 3. Binary Path Validation

### Finding 3.1: No Binary Path Validation
**Severity: CRITICAL**
**Location:** `launch_process.go:34-37, 45`

```go
if spec.BinaryPath == "" {
    return "", nil, fmt.Errorf("BinaryPath required for process strategy")
}
// ...
cmd := exec.CommandContext(ctx, spec.BinaryPath)
```

**Issue:** The binary path is executed without any validation:
- No path traversal prevention
- No check that path points to an allowed binary
- No signature verification
- No check against allow-list of plugin binaries

**Impact:** An attacker who can control `PluginSpec` can execute arbitrary binaries:
```go
spec := PluginSpec{
    BinaryPath: "/bin/rm -rf /", // or any malicious command
    Strategy: "process",
}
```

**Recommendation:**
1. Validate binary path against allow-list
2. Implement path canonicalization to prevent traversal
3. Consider code signing verification
4. Restrict to a designated plugin directory

```go
func validateBinaryPath(path string) error {
    // 1. Canonicalize path
    absPath, err := filepath.Abs(path)
    if err != nil {
        return fmt.Errorf("invalid path: %w", err)
    }

    // 2. Check against allowed plugin directories
    allowedDirs := []string{"/opt/plugins", "/usr/local/plugins"}
    allowed := false
    for _, dir := range allowedDirs {
        if strings.HasPrefix(absPath, dir+"/") {
            allowed = true
            break
        }
    }
    if !allowed {
        return fmt.Errorf("binary path not in allowed directory")
    }

    // 3. Verify file exists and is executable
    info, err := os.Stat(absPath)
    if err != nil {
        return fmt.Errorf("binary not found: %w", err)
    }
    if info.Mode()&0111 == 0 {
        return fmt.Errorf("binary is not executable")
    }

    return nil
}
```

---

## 4. Port Allocation Security

### Finding 4.1: No Port Validation or Reservation
**Severity: MEDIUM**
**Location:** `launch_process.go:62`, `launch_inmemory.go:109`

```go
endpoint := fmt.Sprintf("http://localhost:%d", spec.Port)
```

**Issue:** Port allocation has several problems:
- No validation that port is available before launch
- No protection against port squatting
- No privileged port restrictions (ports < 1024)
- No check for port conflicts between plugins

**Impact:**
- Race condition: another process could bind to the port first
- Denial of service by exhausting available ports
- Port hijacking attacks
- A malicious local process could intercept plugin traffic

**Recommendation:**
1. Implement port reservation before process launch
2. Validate port range (avoid privileged ports)
3. Track allocated ports to prevent conflicts
4. Consider using Unix domain sockets instead of TCP

```go
func reservePort(port int) (net.Listener, error) {
    if port < 1024 || port > 65535 {
        return nil, fmt.Errorf("port must be between 1024-65535")
    }

    listener, err := net.Listen("tcp", fmt.Sprintf("localhost:%d", port))
    if err != nil {
        return nil, fmt.Errorf("port %d unavailable: %w", port, err)
    }
    return listener, nil
}
```

---

### Finding 4.2: Localhost Binding Only
**Severity: LOW**
**Location:** `launch_inmemory.go:109`

```go
server := &http.Server{
    Addr:    fmt.Sprintf("localhost:%d", spec.Port),
    Handler: mux,
}
```

**Positive Note:** In-memory plugins correctly bind to `localhost` only, preventing external network access. However, this should be consistently enforced.

---

## 5. Cleanup Function Reliability

### Finding 5.1: Cleanup May Not Execute
**Severity: MEDIUM**
**Location:** `launcher.go:136-141`

```go
l.instances[pluginName] = &pluginInstance{
    pluginName: pluginName,
    endpoint:   endpoint,
    cleanup:    cleanup,
    provides:   spec.Provides,
}
```

**Issue:** Cleanup functions are stored but only called during `Shutdown()`. If:
- The process crashes before `Shutdown()`
- The launcher is garbage collected without `Shutdown()`
- A panic occurs in the main goroutine

Then cleanup functions won't execute, leaving orphan processes.

**Recommendation:**
1. Register signal handlers for graceful shutdown
2. Use `runtime.SetFinalizer` as last resort
3. Implement process supervision to track child processes
4. Store PID files for recovery on restart

---

### Finding 5.2: Cleanup Race Condition
**Severity: LOW**
**Location:** `launch_process.go:70-83`

```go
cleanup := func() {
    s.mu.Lock()
    defer s.mu.Unlock()

    // Graceful shutdown attempt
    if cmd.Process != nil {
        cmd.Process.Signal(os.Interrupt)
        time.Sleep(100 * time.Millisecond)
        cmd.Process.Kill()
        cmd.Wait()
    }

    delete(s.processes, spec.Name)
}
```

**Issue:** The 100ms sleep before kill is arbitrary and may:
- Kill process before it can complete graceful shutdown
- Wait too long if process is unresponsive

**Recommendation:**
1. Use proper graceful shutdown with configurable timeout
2. Wait for process exit with context timeout
3. Add retry logic for process termination

---

## 6. Graceful Shutdown Security

### Finding 6.1: Insufficient Shutdown Signal Handling
**Severity: MEDIUM**
**Location:** `launch_process.go:75-79`

```go
if cmd.Process != nil {
    cmd.Process.Signal(os.Interrupt)
    time.Sleep(100 * time.Millisecond)
    cmd.Process.Kill()
    cmd.Wait()
}
```

**Issues:**
- SIGINT may be ignored by malicious plugins
- 100ms grace period is too short for proper cleanup
- No escalation strategy (SIGTERM before SIGKILL)
- No verification that process actually terminated

**Recommendation:**
```go
func shutdownProcess(cmd *exec.Cmd, gracePeriod time.Duration) error {
    if cmd.Process == nil {
        return nil
    }

    // 1. Try SIGTERM first
    cmd.Process.Signal(syscall.SIGTERM)

    // 2. Wait with timeout
    done := make(chan error, 1)
    go func() { done <- cmd.Wait() }()

    select {
    case <-time.After(gracePeriod):
        // Grace period expired, force kill
        cmd.Process.Kill()
        <-done
    case <-done:
        // Process exited gracefully
    }

    return nil
}
```

---

### Finding 6.2: In-Memory Shutdown Not Enforced
**Severity: MEDIUM**
**Location:** `launch_inmemory.go:212-221`

```go
func (h *inMemoryControlHandler) Shutdown(
    ctx context.Context,
    req *connect.Request[connectpluginv1.ShutdownRequest],
) (*connect.Response[connectpluginv1.ShutdownResponse], error) {
    log.Printf("[InMemory] Shutdown requested (grace: %ds)", req.Msg.GracePeriodSeconds)
    // In-memory plugins can't really exit, just acknowledge
    return connect.NewResponse(&connectpluginv1.ShutdownResponse{
        Acknowledged: true,
    }), nil
}
```

**Issue:** In-memory plugins acknowledge shutdown but don't actually stop. This is acknowledged in the comment but creates a security concern where plugins cannot be forcefully terminated.

**Impact:** A malicious in-memory plugin cannot be stopped without restarting the entire host.

---

## 7. In-Memory Isolation Concerns

### Finding 7.1: Shared Runtime Token Access
**Severity: HIGH**
**Location:** `launch_inmemory.go:60-77`

```go
client, err := NewClient(ClientConfig{
    HostURL:     hostURL,
    // ...
})
// ...
if err := client.Connect(ctx); err != nil { ... }
```

**Issue:** In-memory plugins have direct access to:
- Their `Client` object and runtime token
- Potentially other plugins' memory through shared address space
- Host internal APIs

**Impact:** A malicious in-memory plugin could:
- Extract and misuse its runtime token
- Attempt to access other plugins' tokens via memory scanning
- Call internal host APIs directly

---

### Finding 7.2: ImplFactory Callback Arbitrary Code Execution
**Severity: HIGH**
**Location:** `launch_inmemory.go:80`

```go
impl := spec.ImplFactory()
```

**Issue:** The `ImplFactory` callback executes arbitrary code during plugin launch with no sandboxing.

**Impact:** A malicious factory function can:
- Execute arbitrary code in the host process
- Access host memory and goroutines
- Spawn background goroutines that persist after "shutdown"

---

## 8. Platform AddPlugin/RemovePlugin Authorization

### Finding 8.1: No Authorization Check in AddPlugin
**Severity: CRITICAL**
**Location:** `platform.go:82-187`

```go
func (p *Platform) AddPlugin(ctx context.Context, config PluginConfig) error {
    // 1. Call plugin's GetPluginInfo() to retrieve metadata
    infoClient := NewPluginIdentityClient(config.Endpoint, nil)
    infoResp, err := infoClient.GetPluginInfo(ctx)
    // ... no authorization check
```

**Issue:** `AddPlugin` has no authorization mechanism:
- Any caller with access to the Platform object can add plugins
- No validation of who is calling the function
- The endpoint in `config.Endpoint` is trusted without verification

**Impact:**
- Unauthorized plugin registration
- Rogue plugins can be added to the platform
- Potential for plugin impersonation attacks

**Recommendation:**
1. Implement authorization layer for platform operations
2. Require signed plugin manifests
3. Add audit logging for plugin lifecycle operations

---

### Finding 8.2: No Authentication for RemovePlugin
**Severity: HIGH**
**Location:** `platform.go:191-225`

```go
func (p *Platform) RemovePlugin(ctx context.Context, runtimeID string) error {
    instance, ok := p.plugins[runtimeID]
    if !ok {
        return fmt.Errorf("plugin not found: %s", runtimeID)
    }
    // ... no authorization check
```

**Issue:** Anyone with access to the Platform can remove any plugin.

**Impact:**
- Denial of service by removing critical plugins
- Disruption of service dependencies
- No audit trail

---

### Finding 8.3: Trust Plugin Self-Declaration
**Severity: HIGH**
**Location:** `platform.go:91-95`

```go
// Use metadata from plugin response (trust the plugin's declarations)
selfID := infoResp.SelfId
if selfID == "" {
    selfID = config.SelfID // Fallback to config
}
```

**Issue:** The platform trusts plugin self-identification without verification. A malicious plugin can claim any identity.

**Impact:**
- Plugin impersonation
- Service hijacking by claiming another plugin's services
- Dependency confusion attacks

**Recommendation:**
1. Verify plugin identity against signed manifest
2. Don't trust plugin-provided metadata for authorization decisions
3. Implement mutual TLS for plugin identity verification

---

## 9. Dependency Graph Manipulation Attacks

### Finding 9.1: No Input Validation in Dependency Graph
**Severity: MEDIUM**
**Location:** `internal/depgraph/depgraph.go:52-68`

```go
func (g *Graph) Add(node *Node) {
    g.nodes[node.RuntimeID] = node
    // ... no validation of node contents
```

**Issue:** The dependency graph accepts any node without validation:
- No limit on number of dependencies
- No limit on depth of dependency chains
- No validation of service type names

**Impact:**
- Denial of service via dependency cycle injection (detected but still problematic)
- Resource exhaustion via large dependency graphs
- Startup order manipulation

---

### Finding 9.2: Dependency Cycle Detection But Not Prevention
**Severity: LOW**
**Location:** `internal/depgraph/depgraph.go:158-163`

```go
// If we didn't process all nodes, there's a cycle
if len(result) != len(g.nodes) {
    return nil, fmt.Errorf("dependency cycle detected (processed %d of %d plugins)",
        len(result), len(g.nodes))
}
```

**Positive Note:** Cycles are detected during `StartupOrder()`. However, they are only detected at runtime, not prevented during `Add()`.

**Recommendation:** Detect cycles during `Add()` to fail fast.

---

### Finding 9.3: Service Type Collision Not Prevented
**Severity: MEDIUM**
**Location:** `internal/depgraph/depgraph.go:64-67`

```go
for _, svc := range node.Provides {
    g.byType[svc.Type] = append(g.byType[svc.Type], node.RuntimeID)
}
```

**Issue:** Multiple plugins can claim to provide the same service type. While this enables multi-provider scenarios, it can be exploited:
- A malicious plugin can register for all service types
- Race condition in which provider handles a request

---

## 10. Resource Cleanup on Failure Paths

### Finding 10.1: Incomplete Cleanup on Launch Failure
**Severity: MEDIUM**
**Location:** `launch_process.go:63-67`

```go
if err := waitForPluginReady(endpoint, 5*time.Second); err != nil {
    cmd.Process.Kill()
    cmd.Wait()
    return "", nil, fmt.Errorf("plugin %s didn't become ready: %w", spec.Name, err)
}
```

**Issue:** On launch failure, the process is killed but:
- The process isn't removed from `s.processes` map
- Any allocated resources (ports, temp files) may not be released

---

### Finding 10.2: Goroutine Leak in In-Memory Strategy
**Severity: MEDIUM**
**Location:** `launch_inmemory.go:132-135`

```go
go func() {
    time.Sleep(100 * time.Millisecond) // Wait for server to be fully ready
    registerInMemoryService(ctx, client, spec, endpoint)
}()
```

**Issue:** The registration goroutine is started but:
- Not tracked for cleanup
- If registration fails, the server is still running
- Context cancellation isn't checked in this goroutine

---

### Finding 10.3: Platform AddPlugin Partial Failure Cleanup
**Severity: MEDIUM**
**Location:** `platform.go:175-178`

```go
if err := p.waitForHealthy(ctx, runtimeID, 30*time.Second); err != nil {
    p.depGraph.Remove(runtimeID)
    return fmt.Errorf("plugin %q did not become healthy: %w", selfID, err)
}
```

**Issue:** When plugin fails to become healthy:
- Dependency graph entry is removed
- But runtime token still exists in handshake server
- Router endpoint may have been partially registered

**Recommendation:** Implement transactional plugin addition with full rollback.

---

## Additional Security Findings

### Finding 11: Token Generation Uses crypto/rand (Positive)
**Severity: INFORMATIONAL**
**Location:** `handshake.go:206-214`, `broker.go:189-193`

```go
func generateRandomHex(length int) string {
    bytes := make([]byte, (length+1)/2)
    if _, err := rand.Read(bytes); err != nil {
        panic(fmt.Sprintf("crypto/rand.Read failed: %v", err))
    }
    return hex.EncodeToString(bytes)[:length]
}
```

**Positive Note:** Token generation correctly uses `crypto/rand` for cryptographically secure random values.

---

### Finding 12: Token Validation Uses Constant-Time Comparison (Negative)
**Severity: LOW**
**Location:** `handshake.go:186-190`

```go
func (h *HandshakeServer) ValidateToken(runtimeID, token string) bool {
    // ...
    return expectedToken == token  // Direct string comparison
}
```

**Issue:** Token comparison uses direct string equality, which is vulnerable to timing attacks.

**Recommendation:** Use `subtle.ConstantTimeCompare()` for token validation.

---

### Finding 13: Service Registration Lacks Token Validation
**Severity: HIGH**
**Location:** `registry.go:96-136`

```go
func (r *ServiceRegistry) RegisterService(
    ctx context.Context,
    req *connect.Request[connectpluginv1.RegisterServiceRequest],
) (*connect.Response[connectpluginv1.RegisterServiceResponse], error) {
    // Extract runtime_id from request headers
    runtimeID := req.Header().Get("X-Plugin-Runtime-ID")
    // Token validation happens elsewhere, but not verified here
```

**Issue:** The registry extracts `X-Plugin-Runtime-ID` but doesn't validate the authorization token. Token validation only happens in the router.

**Impact:** If registry endpoints are exposed directly (not through router), unauthorized service registration is possible.

**Recommendation:** Add token validation to all authenticated endpoints.

---

## Summary of Recommendations

### Critical Priority
1. Implement binary path validation with allow-list
2. Add authorization checks to Platform operations
3. Stop trusting plugin self-declared identity
4. Filter environment variables passed to plugins

### High Priority
5. Add token validation to service registration
6. Implement constant-time token comparison
7. Document in-memory strategy security limitations
8. Add resource limits for plugin processes

### Medium Priority
9. Implement port reservation before launch
10. Improve cleanup on failure paths
11. Add cycle detection during dependency graph insertion
12. Implement audit logging for plugin lifecycle

### Low Priority
13. Improve graceful shutdown handling
14. Track goroutines for proper cleanup
15. Add metrics for security-relevant events

---

## Conclusion

The plugin launcher and platform system has fundamental security gaps that must be addressed before production use with untrusted plugins. The most critical issues relate to lack of process isolation, missing authorization controls, and trusting plugin-provided metadata.

For environments with trusted plugins only, the current implementation may be acceptable with careful configuration. For multi-tenant or untrusted plugin scenarios, significant hardening is required.

**Recommended Next Steps:**
1. Prioritize authorization controls for platform operations
2. Implement binary path validation
3. Add environment variable filtering
4. Consider container-based isolation for process strategy
5. Conduct penetration testing with a focus on plugin impersonation attacks
