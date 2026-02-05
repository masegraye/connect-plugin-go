# Design: Fix mTLS Server Interceptor

**Design ID:** CFLZ
**Author:** Claude Opus 4.5
**Date:** 2026-01-29
**Status:** Draft
**Related Finding:** SEC-AUTH-006

---

## Problem Statement

The current mTLS server interceptor implementation at `/Users/masegraye/workspaces/2026-01-29/connect-plugin-review/connect-plugin-go/auth_mtls.go:57-84` is fundamentally broken. It **hardcodes identity for ALL requests** regardless of whether a valid client certificate was presented:

```go
func (m *MTLSAuth) ServerInterceptor() connect.UnaryInterceptorFunc {
    return func(next connect.UnaryFunc) connect.UnaryFunc {
        return func(ctx context.Context, req connect.AnyRequest) (connect.AnyResponse, error) {
            // Placeholder: Assume cert validation happened at TLS layer
            // Store placeholder auth context
            authCtx := &AuthContext{
                Identity: "mtls-client",  // HARDCODED - EVERY request gets this identity!
                Provider: "mtls",
                Claims:   map[string]string{"verified": "true"},
            }
            ctx = WithAuthContext(ctx, authCtx)
            return next(ctx, req)
        }
    }
}
```

### Impact

1. **Complete Authentication Bypass**: Any HTTP request, regardless of TLS state, is treated as mTLS-authenticated
2. **No Identity Differentiation**: All clients appear as the same identity ("mtls-client")
3. **False Security Posture**: Code comments suggest future implementation but the interceptor is exported and usable today
4. **Authorization Failures**: Any authorization logic based on client identity will fail or be bypassed

---

## Goals

1. **Extract actual client certificate** from the TLS connection state
2. **Extract identity from certificate** using configurable extraction (CN, SAN, custom)
3. **Proper error handling** for all failure cases (no cert, invalid cert, extraction failure)
4. **Maintain backwards compatibility** with existing configuration options
5. **Consistent patterns** with existing `TokenAuth` implementation
6. **Support streaming RPCs** in addition to unary RPCs

---

## Non-Goals

1. **Certificate chain validation** - This is handled by `tls.Config.ClientAuth` and `ClientCAs` at the TLS layer
2. **Certificate revocation checking (CRL/OCSP)** - Out of scope, would be a separate feature
3. **Client-side interceptor changes** - The client interceptor is a no-op by design (mTLS is transport-level)
4. **Certificate pinning** - Out of scope for initial fix
5. **Custom CA verification logic** - TLS layer handles this

---

## Background Research

### Connect-go Request Context

Connect-go does **NOT** provide direct access to the underlying `*http.Request` from within interceptors. The `connect.AnyRequest` interface exposes:

- `Spec()` - RPC specification
- `Peer()` - Peer information (but not TLS state)
- `Header()` - HTTP headers

However, there are two approaches to access TLS state:

#### Approach 1: Context Injection via HTTP Middleware

The standard pattern is to inject the `*http.Request` into the context **before** Connect handlers run. This is done via HTTP middleware wrapping the Connect handler:

```go
func InjectHTTPRequest(next http.Handler) http.Handler {
    return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
        ctx := context.WithValue(r.Context(), httpRequestKey{}, r)
        next.ServeHTTP(w, r.WithContext(ctx))
    })
}

// In interceptor:
httpReq := ctx.Value(httpRequestKey{}).(*http.Request)
if httpReq.TLS != nil {
    certs := httpReq.TLS.PeerCertificates
}
```

#### Approach 2: Connect Peer Information

Connect-go's `connect.Peer()` provides some connection information but **does NOT include TLS state**. The `Peer` struct contains:
- `Addr` - Remote address
- `Protocol` - Protocol (e.g., "connect", "grpc")

This is insufficient for mTLS validation.

### TLS Connection State

The `http.Request.TLS` field provides `*tls.ConnectionState` which contains:

```go
type ConnectionState struct {
    // PeerCertificates contains the certificate chain from the peer
    // Certificates are in leaf-first order
    PeerCertificates []*x509.Certificate

    // VerifiedChains contains verified certificate chains
    // Only populated if ClientAuth >= VerifyClientCertIfGiven
    VerifiedChains [][]*x509.Certificate

    // Other fields: Version, CipherSuite, ServerName, etc.
}
```

Key points:
1. `PeerCertificates[0]` is the **client's leaf certificate**
2. `PeerCertificates` is populated even if verification failed (when `ClientAuth = RequestClientCert`)
3. `VerifiedChains` is only populated if TLS layer verified the chain
4. With `RequireAndVerifyClientCert`, both are populated or connection fails

### Existing Pattern: TokenAuth

The `TokenAuth.ServerInterceptor()` provides a good reference pattern:

```go
func (t *TokenAuth) ServerInterceptor() connect.UnaryInterceptorFunc {
    return func(next connect.UnaryFunc) connect.UnaryFunc {
        return func(ctx context.Context, req connect.AnyRequest) (connect.AnyResponse, error) {
            // 1. Extract credentials (from header)
            authHeader := req.Header().Get(t.Header)
            if authHeader == "" {
                return nil, connect.NewError(connect.CodeUnauthenticated,
                    fmt.Errorf("missing %s header", t.Header))
            }

            // 2. Validate credentials
            identity, claims, err := t.ValidateToken(token)
            if err != nil {
                return nil, connect.NewError(connect.CodeUnauthenticated,
                    fmt.Errorf("invalid token: %w", err))
            }

            // 3. Set auth context
            authCtx := &AuthContext{
                Identity: identity,
                Claims:   claims,
                Provider: "token",
            }
            ctx = WithAuthContext(ctx, authCtx)

            return next(ctx, req)
        }
    }
}
```

---

## Proposed Solution

### Overview

The solution has two parts:

1. **HTTP Middleware** that injects `*http.Request` into context (new)
2. **Updated ServerInterceptor** that extracts TLS state from context (fix)

### Part 1: HTTP Request Context Key (New Public API)

Add a context key for HTTP request injection:

```go
// httpRequestKey is the context key for *http.Request.
// This is exported to allow HTTP middleware to inject the request.
type httpRequestKey struct{}

// HTTPRequestFromContext retrieves the HTTP request from context.
// Returns nil, false if no request is present.
// This is used by interceptors that need access to TLS state.
func HTTPRequestFromContext(ctx context.Context) (*http.Request, bool) {
    req, ok := ctx.Value(httpRequestKey{}).(*http.Request)
    return req, ok
}

// WithHTTPRequest stores an HTTP request in the context.
// This should be called by HTTP middleware before Connect handlers.
func WithHTTPRequest(ctx context.Context, req *http.Request) context.Context {
    return context.WithValue(ctx, httpRequestKey{}, req)
}
```

### Part 2: HTTP Request Injection Middleware

Add middleware that wraps Connect handlers to inject the HTTP request:

```go
// WrapWithHTTPContext wraps an http.Handler to inject the HTTP request into context.
// This enables interceptors to access TLS state, client IP, etc.
//
// Usage:
//   path, handler := connectpluginv1connect.NewKVServiceHandler(impl)
//   mux.Handle(path, connectplugin.WrapWithHTTPContext(handler))
func WrapWithHTTPContext(next http.Handler) http.Handler {
    return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
        ctx := WithHTTPRequest(r.Context(), r)
        next.ServeHTTP(w, r.WithContext(ctx))
    })
}
```

### Part 3: Updated MTLSAuth.ServerInterceptor

```go
// ServerInterceptor returns an interceptor that validates client certificates
// and extracts identity from the client's certificate.
//
// Requirements:
// - Server must be configured with TLS (ClientAuth = RequireAndVerifyClientCert)
// - HTTP handler must be wrapped with WrapWithHTTPContext
//
// If TLS validation passes at the transport layer and this interceptor runs,
// we extract identity from the verified client certificate.
func (m *MTLSAuth) ServerInterceptor() connect.UnaryInterceptorFunc {
    return func(next connect.UnaryFunc) connect.UnaryFunc {
        return func(ctx context.Context, req connect.AnyRequest) (connect.AnyResponse, error) {
            // Extract HTTP request from context
            httpReq, ok := HTTPRequestFromContext(ctx)
            if !ok {
                return nil, connect.NewError(connect.CodeInternal,
                    fmt.Errorf("mTLS interceptor requires HTTP context; wrap handler with WrapWithHTTPContext"))
            }

            // Check TLS connection state
            if httpReq.TLS == nil {
                return nil, connect.NewError(connect.CodeUnauthenticated,
                    fmt.Errorf("TLS connection required for mTLS authentication"))
            }

            // Check for client certificate
            if len(httpReq.TLS.PeerCertificates) == 0 {
                return nil, connect.NewError(connect.CodeUnauthenticated,
                    fmt.Errorf("client certificate required"))
            }

            // Get the client's leaf certificate (first in chain)
            clientCert := httpReq.TLS.PeerCertificates[0]

            // Extract identity using configured extractor
            identity, claims := m.ExtractIdentity(clientCert)
            if identity == "" {
                return nil, connect.NewError(connect.CodeUnauthenticated,
                    fmt.Errorf("failed to extract identity from client certificate"))
            }

            // Store auth context
            authCtx := &AuthContext{
                Identity: identity,
                Provider: "mtls",
                Claims:   claims,
            }
            ctx = WithAuthContext(ctx, authCtx)

            return next(ctx, req)
        }
    }
}
```

### Part 4: Fix Default ExtractIdentity

Fix the array bounds issue (SEC-AUTH-007):

```go
func NewMTLSAuth(clientCert *tls.Certificate, rootCAs, clientCAs *x509.CertPool) *MTLSAuth {
    return &MTLSAuth{
        ClientCert: clientCert,
        RootCAs:    rootCAs,
        ClientCAs:  clientCAs,
        ExtractIdentity: func(cert *x509.Certificate) (string, map[string]string) {
            claims := make(map[string]string)

            // Safely extract organization if present
            if len(cert.Subject.Organization) > 0 {
                claims["organization"] = cert.Subject.Organization[0]
            }

            // Extract all SANs for reference
            if len(cert.DNSNames) > 0 {
                claims["dns_names"] = strings.Join(cert.DNSNames, ",")
            }
            if len(cert.EmailAddresses) > 0 {
                claims["email"] = cert.EmailAddresses[0]
            }

            // Add certificate metadata
            claims["serial_number"] = cert.SerialNumber.String()
            claims["not_after"] = cert.NotAfter.Format(time.RFC3339)

            return cert.Subject.CommonName, claims
        },
    }
}
```

### Part 5: Streaming Interceptor Support

Add streaming interceptor support for streaming RPCs:

```go
// StreamServerInterceptor returns an interceptor for streaming RPCs.
// This validates client certificates for bidirectional and server-streaming RPCs.
func (m *MTLSAuth) StreamServerInterceptor() connect.StreamingHandlerInterceptor {
    return func(next connect.StreamingHandlerFunc) connect.StreamingHandlerFunc {
        return func(ctx context.Context, stream connect.StreamingHandlerConn) error {
            // Extract HTTP request from context
            httpReq, ok := HTTPRequestFromContext(ctx)
            if !ok {
                return connect.NewError(connect.CodeInternal,
                    fmt.Errorf("mTLS interceptor requires HTTP context"))
            }

            // Check TLS connection state
            if httpReq.TLS == nil {
                return connect.NewError(connect.CodeUnauthenticated,
                    fmt.Errorf("TLS connection required"))
            }

            // Check for client certificate
            if len(httpReq.TLS.PeerCertificates) == 0 {
                return connect.NewError(connect.CodeUnauthenticated,
                    fmt.Errorf("client certificate required"))
            }

            // Extract identity
            clientCert := httpReq.TLS.PeerCertificates[0]
            identity, claims := m.ExtractIdentity(clientCert)
            if identity == "" {
                return connect.NewError(connect.CodeUnauthenticated,
                    fmt.Errorf("failed to extract identity from certificate"))
            }

            // Store auth context
            authCtx := &AuthContext{
                Identity: identity,
                Provider: "mtls",
                Claims:   claims,
            }
            ctx = WithAuthContext(ctx, authCtx)

            return next(ctx, stream)
        }
    }
}
```

### Part 6: Update Serve Function

Update `Serve()` to automatically wrap handlers with HTTP context:

```go
// In server.go Serve function, wrap the mux:
func Serve(cfg *ServeConfig) error {
    // ... existing code ...

    // Build the HTTP mux
    mux := http.NewServeMux()

    // ... register handlers ...

    // Wrap entire mux with HTTP context injection
    // This ensures all Connect interceptors can access HTTP request
    handler := WrapWithHTTPContext(mux)

    // Create HTTP server
    srv := &http.Server{
        Addr:    cfg.Addr,
        Handler: handler,  // Use wrapped handler
    }

    // ... rest of function ...
}
```

---

## API Changes

### New Public Functions

```go
// auth_context.go (or auth.go)

// HTTPRequestFromContext retrieves the HTTP request from context.
func HTTPRequestFromContext(ctx context.Context) (*http.Request, bool)

// WithHTTPRequest stores an HTTP request in the context.
func WithHTTPRequest(ctx context.Context, req *http.Request) context.Context

// WrapWithHTTPContext wraps an http.Handler to inject the HTTP request into context.
func WrapWithHTTPContext(next http.Handler) http.Handler
```

### New Method on MTLSAuth

```go
// auth_mtls.go

// StreamServerInterceptor returns a streaming interceptor for mTLS authentication.
func (m *MTLSAuth) StreamServerInterceptor() connect.StreamingHandlerInterceptor
```

### Modified Behavior

The `MTLSAuth.ServerInterceptor()` method now:
- **Requires** HTTP context to be injected (via `WrapWithHTTPContext`)
- **Returns** `CodeInternal` if context is not available
- **Returns** `CodeUnauthenticated` if TLS/certificate is missing
- **Extracts** actual identity from client certificate

---

## Error Handling

### Error Cases

| Condition | Error Code | Message |
|-----------|------------|---------|
| HTTP context not injected | `CodeInternal` | "mTLS interceptor requires HTTP context; wrap handler with WrapWithHTTPContext" |
| Non-TLS connection | `CodeUnauthenticated` | "TLS connection required for mTLS authentication" |
| No client certificate | `CodeUnauthenticated` | "client certificate required" |
| Identity extraction fails | `CodeUnauthenticated` | "failed to extract identity from client certificate" |

### Error Code Rationale

- `CodeInternal`: Configuration/setup error on the server side
- `CodeUnauthenticated`: Client did not provide valid credentials

---

## Configuration

### Existing Configuration (No Changes)

```go
type MTLSAuth struct {
    // ClientCert is the client certificate for outgoing requests (client-side)
    ClientCert *tls.Certificate

    // RootCAs is the trusted CA pool for verifying server certificates (client-side)
    RootCAs *x509.CertPool

    // ClientCAs is the trusted CA pool for verifying client certificates (server-side)
    ClientCAs *x509.CertPool

    // ExtractIdentity extracts identity from verified client certificate (server-side)
    ExtractIdentity func(*x509.Certificate) (identity string, claims map[string]string)
}
```

### Example: Custom Identity Extraction

```go
mtlsAuth := &MTLSAuth{
    ClientCAs: caPool,
    ExtractIdentity: func(cert *x509.Certificate) (string, map[string]string) {
        // Use SAN email as identity
        if len(cert.EmailAddresses) > 0 {
            return cert.EmailAddresses[0], map[string]string{
                "cn": cert.Subject.CommonName,
            }
        }
        // Fallback to CN
        return cert.Subject.CommonName, nil
    },
}
```

### Example: SAN DNS Name as Identity

```go
mtlsAuth := &MTLSAuth{
    ClientCAs: caPool,
    ExtractIdentity: func(cert *x509.Certificate) (string, map[string]string) {
        // Use first DNS SAN as service identity
        if len(cert.DNSNames) > 0 {
            return cert.DNSNames[0], map[string]string{
                "all_dns": strings.Join(cert.DNSNames, ","),
            }
        }
        return cert.Subject.CommonName, nil
    },
}
```

---

## Migration/Backwards Compatibility

### Breaking Changes

1. **ServerInterceptor now requires HTTP context**: Existing code using `MTLSAuth.ServerInterceptor()` without `WrapWithHTTPContext` will receive `CodeInternal` errors.

2. **Identity is no longer hardcoded**: Code that relied on the hardcoded "mtls-client" identity will now see actual certificate identities.

### Migration Steps

1. **Update handler registration** to use `WrapWithHTTPContext`:

   Before:
   ```go
   mux.Handle(path, handler)
   ```

   After:
   ```go
   mux.Handle(path, WrapWithHTTPContext(handler))
   ```

   Or if using `Serve()`, this is automatic.

2. **Review authorization logic** that references identity. The identity will now be the certificate's CN (by default) instead of "mtls-client".

3. **Update ExtractIdentity** if custom identity extraction is needed.

### Feature Flag Option

If gradual migration is required, we could add a configuration option:

```go
type MTLSAuth struct {
    // ... existing fields ...

    // AllowMissingHTTPContext allows the interceptor to work without HTTP context.
    // When true and HTTP context is missing, falls back to legacy behavior.
    // DEPRECATED: This is for migration only and will be removed.
    AllowMissingHTTPContext bool
}
```

**Recommendation**: Do not add this flag. The current implementation is a security vulnerability and should be fixed cleanly.

---

## Test Cases

### Unit Tests

1. **TestMTLSServerInterceptor_ValidCertificate**
   - Setup: HTTP context with TLS state and valid peer certificate
   - Verify: AuthContext is set with correct identity from CN

2. **TestMTLSServerInterceptor_CustomExtractor**
   - Setup: Custom ExtractIdentity function using SAN
   - Verify: Identity matches expected SAN value

3. **TestMTLSServerInterceptor_MissingHTTPContext**
   - Setup: Call interceptor without HTTP context injection
   - Verify: Returns `CodeInternal` error

4. **TestMTLSServerInterceptor_NoTLS**
   - Setup: HTTP context with nil TLS state
   - Verify: Returns `CodeUnauthenticated` error

5. **TestMTLSServerInterceptor_NoPeerCertificate**
   - Setup: TLS state with empty PeerCertificates
   - Verify: Returns `CodeUnauthenticated` error

6. **TestMTLSServerInterceptor_EmptyIdentity**
   - Setup: Certificate with empty CN, extractor returns ""
   - Verify: Returns `CodeUnauthenticated` error

7. **TestDefaultExtractIdentity_NoOrganization**
   - Setup: Certificate without Organization field
   - Verify: No panic, claims has no "organization" key

8. **TestMTLSStreamInterceptor_ValidCertificate**
   - Setup: Streaming handler with valid certificate
   - Verify: AuthContext is propagated to stream handler

### Integration Tests

9. **TestMTLS_EndToEnd_ValidClientCert**
   - Setup: Real TLS server with mTLS, valid client cert
   - Verify: Request succeeds, identity matches cert CN

10. **TestMTLS_EndToEnd_NoClientCert**
    - Setup: TLS server with mTLS, client without cert
    - Verify: Request fails with Unauthenticated

11. **TestMTLS_EndToEnd_UntrustedCA**
    - Setup: Client cert signed by untrusted CA
    - Verify: TLS handshake fails (not interceptor)

12. **TestMTLS_WithTokenAuth_Composition**
    - Setup: ComposeAuthServer with mTLS + token providers
    - Verify: mTLS succeeds when cert present, falls back to token

### Mock Helpers

```go
// testutil/mtls.go

// MockHTTPRequest creates a mock HTTP request with TLS state for testing.
func MockHTTPRequest(cert *x509.Certificate) *http.Request {
    return &http.Request{
        TLS: &tls.ConnectionState{
            PeerCertificates: []*x509.Certificate{cert},
        },
    }
}

// MockContext creates a context with mock HTTP request.
func MockContextWithCert(cert *x509.Certificate) context.Context {
    req := MockHTTPRequest(cert)
    return WithHTTPRequest(context.Background(), req)
}

// GenerateTestCert generates a self-signed certificate for testing.
func GenerateTestCert(cn string, org []string) (*x509.Certificate, error) {
    // ... implementation ...
}
```

---

## Alternatives Considered

### Alternative 1: Use Connect Peer Extension

**Idea**: Extend Connect's `Peer` struct to include TLS state.

**Rejected because**:
- Requires changes to Connect-go library
- Breaking change to Connect-go API
- Context injection is the established pattern

### Alternative 2: Require TLS State in MTLSAuth Constructor

**Idea**: Pass `*tls.ConnectionState` directly to `NewMTLSAuth`.

**Rejected because**:
- TLS state is per-request, not per-server
- Would require restructuring to pass state per-request
- Doesn't fit the interceptor pattern

### Alternative 3: Separate HTTP Middleware Instead of Interceptor

**Idea**: Implement mTLS as HTTP middleware instead of Connect interceptor.

**Rejected because**:
- Breaks consistency with `AuthProvider` interface
- Cannot be composed with other auth providers via `ComposeAuthServer`
- Requires different integration pattern

### Alternative 4: Use connect.Request Generic Parameter

**Idea**: Access HTTP request through typed `connect.Request[T]`.

**Rejected because**:
- `connect.AnyRequest` interface in interceptors doesn't expose HTTP request
- Would require Connect-go API changes
- Context injection is more flexible

---

## Open Questions

### Q1: Should we support `VerifyClientCertIfGiven` mode?

**Context**: With `tls.VerifyClientCertIfGiven`, client certs are optional. The interceptor could pass through requests without certs.

**Options**:
- A) Require certificates (current design)
- B) Add `Optional bool` field to allow pass-through
- C) Let composition handle it (mTLS + anonymous provider)

**Recommendation**: Option C - keep mTLS strict, use composition for optional scenarios.

### Q2: Should HTTP context injection be automatic in `Serve()`?

**Context**: Users might forget to wrap handlers.

**Options**:
- A) Automatic in `Serve()` (proposed)
- B) Manual with clear error messages
- C) Both - automatic in `Serve()`, documented for manual setup

**Recommendation**: Option C for flexibility.

### Q3: Should we validate certificate expiration in the interceptor?

**Context**: TLS layer validates expiration, but interceptor could double-check.

**Options**:
- A) Trust TLS layer (current design)
- B) Add explicit expiration check
- C) Make it configurable

**Recommendation**: Option A - TLS layer already handles this.

### Q4: Should identity extraction failure be configurable (error vs empty identity)?

**Context**: Some deployments might want to allow any valid cert regardless of extractable identity.

**Options**:
- A) Always error on empty identity (current design)
- B) Allow empty identity if extractor returns it
- C) Add configuration option

**Recommendation**: Option A - identity is the core purpose of authentication.

---

## Implementation Plan

### Phase 1: Core Fix (P0 - Security)

1. Add `HTTPRequestFromContext`, `WithHTTPRequest`, `WrapWithHTTPContext`
2. Update `MTLSAuth.ServerInterceptor()` with actual certificate extraction
3. Fix `ExtractIdentity` default function array bounds
4. Add unit tests for new functionality

**Estimated effort**: 1-2 days

### Phase 2: Integration (P1)

1. Update `Serve()` to automatically wrap handlers
2. Add streaming interceptor support
3. Add integration tests with real TLS

**Estimated effort**: 1 day

### Phase 3: Documentation (P1)

1. Update godoc comments
2. Add example in `examples/` directory
3. Document migration path

**Estimated effort**: 0.5 days

---

## Appendix A: Reference Implementation

Complete implementation available after review approval.

## Appendix B: Related Work

- **SEC-AUTH-004**: Timing attack in token validation (separate fix)
- **SEC-AUTH-007**: Array bounds in ExtractIdentity (addressed in this design)
- **TokenAuth**: Reference implementation for interceptor pattern
