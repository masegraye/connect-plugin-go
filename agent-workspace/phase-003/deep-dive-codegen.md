# Security Assessment: Code Generation System (protoc-gen-connect-plugin)

**Assessment Date:** 2026-01-29
**Reviewer:** Claude Code Security Audit
**Component:** `cmd/protoc-gen-connect-plugin/main.go`
**Related Files:** Examples in `examples/kv/gen/`

---

## Executive Summary

The `protoc-gen-connect-plugin` code generator is a protoc plugin that generates Go code for the connect-plugin system. It creates two packages per service:
1. **Plugin package** (`*plugin/plugin.go`): Implements the `connectplugin.Plugin` interface
2. **Delegate package** (`*delegate/delegate.go`): Provides domain-friendly Go interfaces

Overall, the code generation system demonstrates **reasonable security hygiene** by leveraging the protogen library which pre-sanitizes identifiers. However, several areas require attention.

**Risk Summary:**
- **Critical:** 0
- **High:** 1
- **Medium:** 3
- **Low:** 4
- **Informational:** 3

---

## 1. Code Injection Through Proto File Names/Paths

### Finding 1.1: Proto Source Path in Generated Comments (LOW)

**Location:** `cmd/protoc-gen-connect-plugin/main.go:62` and `main.go:150`

**Code:**
```go
g.P("// source: ", file.Desc.Path())
```

**Analysis:**
The proto file path is emitted directly into generated Go source code as a comment. While Go comments cannot execute code, a malicious proto file path could potentially:
- Inject misleading information
- Create confusing error messages during debugging
- In extreme cases, could contain Unicode characters that render differently

**Severity:** LOW

**Risk Assessment:**
- Proto file paths are typically controlled by the build system
- Go comments are inert and cannot execute
- The protogen library normalizes paths before use

**Recommendation:**
Consider sanitizing the path or escaping special characters, though the risk is minimal:
```go
// Sanitize path for comment safety
sanitizedPath := strings.ReplaceAll(file.Desc.Path(), "*/", "* /")
g.P("// source: ", sanitizedPath)
```

---

## 2. Template Escaping in Generated Code

### Finding 2.1: No Template Injection Vectors (INFORMATIONAL)

**Location:** Throughout `cmd/protoc-gen-connect-plugin/main.go`

**Analysis:**
The code generator uses `g.P()` (protogen's print function) rather than text/template or html/template. This is the **correct approach** for code generation because:

1. **No string interpolation vulnerabilities:** All values are concatenated, not interpolated
2. **protogen sanitization:** The protogen library pre-validates and sanitizes:
   - `service.GoName` - Valid Go identifier
   - `file.GoPackageName` - Valid Go package name
   - `method.GoName` - Valid Go identifier
   - `field.GoName` - Valid Go identifier

**Example of Safe Pattern (main.go:83-84):**
```go
pluginName := service.GoName + "Plugin"
handlerName := service.GoName + "Handler"
```

**Severity:** INFORMATIONAL (No vulnerability)

**Positive Finding:** The generator correctly avoids template injection by using direct string concatenation with pre-sanitized identifiers.

---

## 3. Import Path Manipulation

### Finding 3.1: Concatenated Import Paths (MEDIUM)

**Location:** `cmd/protoc-gen-connect-plugin/main.go:53-54`, `main.go:69`

**Code:**
```go
pluginPkgName := string(file.GoPackageName) + "plugin"
pluginPkgPath := protogen.GoImportPath(string(file.GoImportPath) + "/" + pluginPkgName)
connectPkgPath := string(file.GoImportPath) + "/" + string(file.GoPackageName) + "connect"
```

**Analysis:**
Import paths are constructed by concatenating the file's import path with fixed suffixes. Potential attack vectors:

1. **Path Traversal:** A malicious `GoImportPath` containing `..` could theoretically point to unintended packages
2. **Package Name Collision:** If `GoPackageName` is crafted to match system packages
3. **Unicode Homograph:** Using visually similar characters to impersonate legitimate packages

**Severity:** MEDIUM

**Risk Assessment:**
- The protogen library validates `GoImportPath` and `GoPackageName` before the plugin runs
- Go's import system requires explicit registration of import paths
- The `go_package` option in proto files is typically controlled by the developer

**Mitigating Factor:**
The Go toolchain validates import paths during compilation, so invalid paths would cause build failures rather than security issues.

**Recommendation:**
Add explicit validation of import path components:
```go
func validateImportPath(path string) error {
    // Check for suspicious patterns
    if strings.Contains(path, "..") {
        return fmt.Errorf("import path contains path traversal: %s", path)
    }
    // Add additional validation as needed
    return nil
}
```

### Finding 3.2: Package Name Suffix Collision Potential (LOW)

**Location:** `cmd/protoc-gen-connect-plugin/main.go:53`, `main.go:141`

**Code:**
```go
pluginPkgName := string(file.GoPackageName) + "plugin"
delegatePkgName := string(file.GoPackageName) + "delegate"
```

**Analysis:**
If the original package name ends in `plugin` or `delegate`, the generated package could collide:
- `foo` -> `fooplugin` (OK)
- `kvplugin` -> `kvpluginplugin` (Awkward but safe)

**Severity:** LOW

**Recommendation:**
Consider checking for existing suffixes:
```go
if !strings.HasSuffix(string(file.GoPackageName), "plugin") {
    pluginPkgName = string(file.GoPackageName) + "plugin"
} else {
    // Handle collision case
}
```

---

## 4. Generated Code Security Defaults

### Finding 4.1: Hardcoded Version Strings (LOW)

**Location:** `cmd/protoc-gen-connect-plugin/main.go:104`, `main.go:108`

**Code:**
```go
g.P(`		Version: "1.0.0",`)
```

**Analysis:**
The generated plugin code always reports version `"1.0.0"` regardless of actual version. This could:
- Mask version-dependent security updates
- Confuse debugging/tracing
- Make it harder to identify vulnerable plugin versions

**Severity:** LOW

**Recommendation:**
Accept version as a generator option:
```go
var pluginVersion = flag.String("version", "1.0.0", "version for generated plugins")
```

### Finding 4.2: No TLS Configuration in Generated Client (INFORMATIONAL)

**Location:** `cmd/protoc-gen-connect-plugin/main.go:131-132`

**Code:**
```go
g.P("func (p *", pluginName, ") ConnectClient(baseURL string, httpClient connect.HTTPClient) (any, error) {")
g.P("	return ", file.GoPackageName, "connect.New", service.GoName, "Client(httpClient, baseURL), nil")
```

**Analysis:**
The generated `ConnectClient` accepts any `connect.HTTPClient`, relying on the caller to configure TLS. This is the correct design pattern (dependency injection), but:
- No warning if used with plain HTTP
- No validation of baseURL scheme

**Severity:** INFORMATIONAL

**Positive Finding:** This is actually good design - security configuration should be injected, not hardcoded.

### Finding 4.3: Default HTTP Client in Delegate NewFromURL (MEDIUM)

**Location:** `cmd/protoc-gen-connect-plugin/main.go:228-230`

**Code:**
```go
g.P("func NewFromURL(url string, opts ...connect.ClientOption) ", interfaceName, " {")
g.P("	client := ", file.GoPackageName, "connect.New", service.GoName, "Client(http.DefaultClient, url, opts...)")
```

**Analysis:**
The `NewFromURL` function uses `http.DefaultClient` which:
- Has no timeout configured
- Follows redirects by default (potential SSRF)
- Has no connection limits

**Severity:** MEDIUM

**Recommendation:**
Document the security implications or use a more restrictive default client:
```go
// Consider generating with a timeout-configured client
g.P("	httpClient := &http.Client{Timeout: 30 * time.Second}")
g.P("	client := ...(httpClient, url, opts...)")
```

---

## 5. Type Safety of Generated Delegate Pattern

### Finding 5.1: Type Assertion on ConnectServer impl (HIGH)

**Location:** `cmd/protoc-gen-connect-plugin/main.go:118-122`

**Generated Code:**
```go
func (p *KVServicePlugin) ConnectServer(impl any) (string, http.Handler, error) {
    handler, ok := impl.(kvv1connect.KVServiceHandler)
    if !ok {
        return "", nil, fmt.Errorf("impl must implement KVServiceHandler, got %T", impl)
    }
    // ...
}
```

**Analysis:**
This is a runtime type assertion on `any`. While the error message is helpful, passing the wrong type:
- Only fails at runtime, not compile time
- Could cause confusing errors in complex plugin systems
- No static analysis tools can catch the mismatch

**Severity:** HIGH

**Risk Assessment:**
This is an inherent limitation of the Plugin interface design which uses `any` for flexibility. The generated code does the best it can by providing a clear error message.

**Recommendations:**

1. **Document the expected types clearly** (done via comments)
2. **Consider a type-safe alternative API:**
```go
// Type-safe plugin creation
func NewKVServiceServer(impl kvv1connect.KVServiceHandler) (string, http.Handler) {
    return kvv1connect.NewKVServiceHandler(impl)
}
```

3. **Add compile-time checks in examples:**
```go
// In examples, use explicit type:
var _ kvv1connect.KVServiceHandler = (*Store)(nil)
```

The examples do show this pattern (`examples/kv/impl/store.go:17`), which is good.

### Finding 5.2: Interface Method Signature Generation (LOW)

**Location:** `cmd/protoc-gen-connect-plugin/main.go:265-287`

**Analysis:**
The delegate interface generation flattens request/response types:
```go
// Generated: Get(ctx, key string) (value []byte, found bool, err error)
// Instead of: Get(ctx, *GetRequest) (*GetResponse, error)
```

This is a **design choice** that improves ergonomics but:
- Changes the API contract from protobuf messages to Go primitives
- Could cause issues if proto fields are added later (breaking change)
- Loses access to message metadata (headers, trailers)

**Severity:** LOW (design tradeoff, not vulnerability)

**Recommendation:**
Document that the delegate pattern is for simplified use cases and the Connect client should be used directly when full control is needed.

---

## 6. Streaming Method Handling

### Finding 6.1: Streaming Types Exposed in Delegate Interface (MEDIUM)

**Location:** `cmd/protoc-gen-connect-plugin/main.go:291-312`

**Generated Code:**
```go
// For server streaming
Watch(ctx context.Context, prefix string) (*connect.ServerStreamForClient[kvv1.WatchEvent], error)
```

**Analysis:**
Streaming methods return Connect-specific stream types, which:
- Leaks Connect abstraction into the "domain" interface
- Requires consumers to understand Connect streaming semantics
- No resource cleanup enforcement (streams must be closed)

**Severity:** MEDIUM

**Security Concerns:**
1. **Resource Leak:** Unclosed streams can exhaust server resources
2. **No Cancellation Propagation:** Stream lifecycle not tied to context
3. **Memory Pressure:** Unbounded message buffering

**From streaming.go (lines 29-59):**
```go
func PumpToStream[T any](ctx context.Context, ch <-chan T, errs <-chan error, stream *connect.ServerStream[T]) error {
    for {
        select {
        case <-ctx.Done():
            return ctx.Err()  // Good: respects context
        // ...
        }
    }
}
```

**Positive Finding:** The `PumpToStream` helper properly respects context cancellation.

**Recommendations:**
1. Document stream lifecycle requirements
2. Consider wrapping streams in resource-managed types:
```go
type ManagedStream[T any] struct {
    stream *connect.ServerStreamForClient[T]
    cancel context.CancelFunc
}
func (m *ManagedStream[T]) Close() error { m.cancel(); return nil }
```

---

## 7. Error Handling in Generated Code

### Finding 7.1: Error Messages May Leak Type Information (LOW)

**Location:** `cmd/protoc-gen-connect-plugin/main.go:121`

**Generated Code:**
```go
return "", nil, fmt.Errorf("impl must implement KVServiceHandler, got %T", impl)
```

**Analysis:**
Error messages include the actual type passed (`%T`), which could reveal:
- Internal type names
- Package structure
- Implementation details

**Severity:** LOW

**Risk Assessment:**
These errors should only occur during development, not in production. However, if plugin loading is user-controlled, this could leak internal structure.

**Recommendation:**
For production builds, consider generic error messages:
```go
return "", nil, fmt.Errorf("impl has incorrect type for this plugin")
```

### Finding 7.2: Generated Delegate Zero Values on Error (INFORMATIONAL)

**Location:** `cmd/protoc-gen-connect-plugin/main.go:374-383`

**Generated Code:**
```go
if err != nil {
    return nil, 0, false, "", err  // Zero values for all fields
}
```

**Analysis:**
When errors occur, the generated code returns Go zero values for all return parameters. This is the correct Go idiom, but callers must always check errors before using return values.

**Severity:** INFORMATIONAL

**Positive Finding:** This follows standard Go error handling conventions.

---

## 8. Package Naming Collision Attacks

### Finding 8.1: Service Name Collision Potential (LOW)

**Location:** `cmd/protoc-gen-connect-plugin/main.go:96`

**Code:**
```go
pluginDefaultName := strings.ToLower(strings.TrimSuffix(service.GoName, "Service"))
```

**Analysis:**
Plugin names are derived from service names:
- `KVService` -> `kv`
- `AuthService` -> `auth`

Potential collisions:
- Two services: `KVService` and `Kv` both map to `kv`
- Reserved names: `http`, `net`, `os` could collide with standard library

**Severity:** LOW

**Recommendation:**
Add collision detection:
```go
reservedNames := map[string]bool{"http": true, "net": true, ...}
if reservedNames[pluginDefaultName] {
    // Handle collision
}
```

### Finding 8.2: No Validation of Proto Package vs Go Package (LOW)

**Location:** Throughout code generation

**Analysis:**
The generator trusts that `go_package` is properly configured. If `go_package` in the proto file points to an attacker-controlled path, generated code could:
- Import malicious packages
- Overwrite legitimate packages (if output is not controlled)

**Severity:** LOW

**Risk Assessment:**
This would require control over proto file content AND the build system, which is a high barrier.

**Mitigating Factors:**
- Proto files are typically committed to version control
- The build process typically validates outputs
- Go module system provides isolation

---

## 9. Additional Security Observations

### Finding 9.1: No Code Signing/Verification (INFORMATIONAL)

**Analysis:**
Generated code has no mechanism for:
- Verifying it was generated by the legitimate plugin
- Detecting tampering after generation
- Ensuring reproducible builds

**Severity:** INFORMATIONAL

**Recommendation:**
Consider adding a generation hash in comments:
```go
// Generated by protoc-gen-connect-plugin v0.1.0
// Hash: sha256:abc123...
```

### Finding 9.2: Version Pinning Absent (INFORMATIONAL)

**Location:** `cmd/protoc-gen-connect-plugin/main.go:12`

**Code:**
```go
const version = "0.1.0"
```

**Analysis:**
The generator version is hardcoded but not embedded in generated code in a machine-parseable way.

**Severity:** INFORMATIONAL

**Recommendation:**
Add structured version metadata:
```go
g.P("// @generated by protoc-gen-connect-plugin version ", version)
g.P("var _generatorVersion = ", strconv.Quote(version))
```

---

## Summary of Recommendations

### Immediate Actions (High Priority)

1. **Document type assertion requirements** clearly for `ConnectServer(impl any)`
2. **Add validation** for import paths to prevent path traversal patterns
3. **Document stream lifecycle** requirements for streaming methods

### Medium-Term Improvements

1. Consider **generating type-safe wrapper functions** alongside the generic Plugin interface
2. Add **default timeout configuration** for `NewFromURL` generated function
3. Implement **reserved name checking** for generated plugin names

### Long-Term Enhancements

1. Consider **code signing** for generated output
2. Add **structured version metadata** for reproducibility
3. Develop **static analysis tools** to verify plugin implementations at compile time

---

## Appendix: Files Reviewed

| File | Lines | Purpose |
|------|-------|---------|
| `cmd/protoc-gen-connect-plugin/main.go` | 549 | Main code generator |
| `examples/kv/gen/kvv1connect/kv.connect.go` | 201 | Example Connect-generated code |
| `examples/kv/gen/kv.pb.go` | 556 | Example protobuf-generated code |
| `examples/kv/proto/kv.proto` | 67 | Example proto definition |
| `examples/kv/impl/store.go` | 166 | Example implementation |
| `examples/kv/host/main.go` | 121 | Example host/client |
| `examples/kv/server/main.go` | 209 | Example server |
| `plugin.go` | 166 | Plugin interface definition |
| `client.go` | 421 | Client implementation |
| `server.go` | 288 | Server implementation |
| `streaming.go` | 60 | Streaming helpers |
| `errors.go` | 31 | Error definitions |

---

**Assessment Completed:** 2026-01-29
**Next Review Recommended:** After any major changes to code generation logic
