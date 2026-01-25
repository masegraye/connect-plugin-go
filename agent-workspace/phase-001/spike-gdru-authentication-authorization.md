# Spike: Authentication & Authorization Patterns

**Issue:** KOR-gdru
**Status:** Complete

## Executive Summary

connect-plugin needs flexible authentication supporting multiple mechanisms: mTLS for transport security, tokens (JWT, API keys) for application-level auth, and OIDC for enterprise deployments. The key insight is that these layers can be composed - mTLS secures the transport while tokens provide identity and authorization claims. For bidirectional communication (host↔plugin), both parties may need to authenticate to each other, requiring symmetric credential exchange.

## Authentication Layers

### Transport Layer (TLS/mTLS)

```
┌─────────────────────────────────────────────────────────────────┐
│                    Transport Security                            │
│  ┌─────────────┐                            ┌─────────────┐     │
│  │   Client    │ ◄─────── mTLS ───────────► │   Server    │     │
│  │             │    (mutual authentication)  │             │     │
│  └─────────────┘                            └─────────────┘     │
│                                                                  │
│  Provides: Encryption, Server Identity, Client Identity (mTLS)  │
│  Does NOT provide: Authorization, User Identity, Role Claims    │
└─────────────────────────────────────────────────────────────────┘
```

### Application Layer (Tokens)

```
┌─────────────────────────────────────────────────────────────────┐
│                    Application Security                          │
│  ┌─────────────┐                            ┌─────────────┐     │
│  │   Client    │ ────── Bearer Token ─────► │   Server    │     │
│  │             │    (Authorization header)   │             │     │
│  └─────────────┘                            └─────────────┘     │
│                                                                  │
│  Provides: User Identity, Role Claims, Scopes, Expiration       │
│  Does NOT provide: Encryption (relies on TLS)                   │
└─────────────────────────────────────────────────────────────────┘
```

## go-plugin AutoMTLS Pattern

### How It Works (mtls.go, client.go)

1. **Host generates ephemeral certificate:**
```go
// mtls.go:20-76
func generateCert() (cert []byte, privateKey []byte, err error) {
    key, err := ecdsa.GenerateKey(elliptic.P521(), rand.Reader)
    template := &x509.Certificate{
        Subject:      pkix.Name{CommonName: "localhost"},
        DNSNames:     []string{"localhost"},
        ExtKeyUsage:  []x509.ExtKeyUsage{x509.ExtKeyUsageClientAuth, x509.ExtKeyUsageServerAuth},
        KeyUsage:     x509.KeyUsageDigitalSignature | x509.KeyUsageKeyEncipherment | x509.KeyUsageCertSign,
        IsCA:         true,
        NotBefore:    time.Now().Add(-30 * time.Second),
        NotAfter:     time.Now().Add(262980 * time.Hour), // ~30 years
    }
    // ... create self-signed cert
}
```

2. **Host sends client cert to plugin via environment:**
```go
// client.go:671-691
if c.config.AutoMTLS {
    certPEM, keyPEM, err := generateCert()
    cert, err := tls.X509KeyPair(certPEM, keyPEM)
    cmd.Env = append(cmd.Env, fmt.Sprintf("PLUGIN_CLIENT_CERT=%s", certPEM))
    c.config.TLSConfig = &tls.Config{
        Certificates: []tls.Certificate{cert},
        ClientAuth:   tls.RequireAndVerifyClientCert,
        MinVersion:   tls.VersionTLS12,
        ServerName:   "localhost",
    }
}
```

3. **Plugin receives client cert, generates server cert:**
   - Plugin parses `PLUGIN_CLIENT_CERT` from environment
   - Plugin generates its own ephemeral cert
   - Plugin adds host's cert to trusted CAs
   - Plugin returns server cert in handshake (base64 DER in stdout)

4. **Host receives server cert, completes mTLS setup:**
```go
// client.go:950-968
func (c *Client) loadServerCert(cert string) error {
    certPool := x509.NewCertPool()
    asn1, _ := base64.RawStdEncoding.DecodeString(cert)
    x509Cert, _ := x509.ParseCertificate(asn1)
    certPool.AddCert(x509Cert)
    c.config.TLSConfig.RootCAs = certPool
    c.config.TLSConfig.ClientCAs = certPool
}
```

### Security Properties

- **One-time use**: New certs per plugin launch
- **Mutual auth**: Both sides verify each other
- **No external CA**: Self-signed, no PKI overhead
- **Process-local**: Certs never leave local system

### Limitations for Network Plugins

1. **Environment variable exchange doesn't work** over network
2. **Localhost-only DNS names** won't validate for remote hosts
3. **No key rotation** for long-running plugins
4. **No revocation** mechanism

## connect-plugin Authentication Architecture

### Multi-Layer Design

```
┌────────────────────────────────────────────────────────────────────────────┐
│                         Authentication Stack                                │
│                                                                            │
│  ┌──────────────────────────────────────────────────────────────────────┐  │
│  │  Layer 3: Authorization (Interceptor)                                │  │
│  │  - Checks token claims against required permissions                  │  │
│  │  - Per-RPC authorization decisions                                   │  │
│  └──────────────────────────────────────────────────────────────────────┘  │
│                                 │                                          │
│  ┌──────────────────────────────▼───────────────────────────────────────┐  │
│  │  Layer 2: Token Authentication (Interceptor)                         │  │
│  │  - Validates JWT/API key                                             │  │
│  │  - Extracts identity claims to context                               │  │
│  └──────────────────────────────────────────────────────────────────────┘  │
│                                 │                                          │
│  ┌──────────────────────────────▼───────────────────────────────────────┐  │
│  │  Layer 1: Transport Security (TLS/mTLS)                              │  │
│  │  - Encrypts traffic                                                  │  │
│  │  - Authenticates server (TLS) or both parties (mTLS)                │  │
│  └──────────────────────────────────────────────────────────────────────┘  │
│                                                                            │
│  ┌──────────────────────────────────────────────────────────────────────┐  │
│  │  Layer 0: Network                                                    │  │
│  │  - TCP/HTTP transport                                                │  │
│  └──────────────────────────────────────────────────────────────────────┘  │
└────────────────────────────────────────────────────────────────────────────┘
```

### Supported Auth Mechanisms

#### 1. mTLS (Mutual TLS)

Best for: Service-to-service auth in controlled environments

```go
// Configuration
type MTLSConfig struct {
    CertFile       string        // Client/server certificate
    KeyFile        string        // Private key
    CAFile         string        // CA for validating peer
    VerifyHostname bool          // Verify peer's hostname
    MinVersion     uint16        // Minimum TLS version
}

// Host client setup
tlsConfig := &tls.Config{
    Certificates: []tls.Certificate{cert},
    RootCAs:      caCertPool,       // Verify plugin's cert
    ClientCAs:    caCertPool,       // For client cert validation
    MinVersion:   tls.VersionTLS12,
}
httpClient := &http.Client{
    Transport: &http.Transport{TLSClientConfig: tlsConfig},
}
```

#### 2. Bearer Token (JWT)

Best for: Flexible identity, claims, and short-lived auth

```go
// Token auth interceptor
func NewBearerAuthInterceptor(getToken func() string) connect.UnaryInterceptorFunc {
    return func(next connect.UnaryFunc) connect.UnaryFunc {
        return func(ctx context.Context, req connect.AnyRequest) (connect.AnyResponse, error) {
            req.Header().Set("Authorization", "Bearer "+getToken())
            return next(ctx, req)
        }
    }
}

// Server-side validation interceptor
func NewTokenValidationInterceptor(validator TokenValidator) connect.UnaryInterceptorFunc {
    return func(next connect.UnaryFunc) connect.UnaryFunc {
        return func(ctx context.Context, req connect.AnyRequest) (connect.AnyResponse, error) {
            token := strings.TrimPrefix(req.Header().Get("Authorization"), "Bearer ")
            claims, err := validator.Validate(token)
            if err != nil {
                return nil, connect.NewError(connect.CodeUnauthenticated, err)
            }
            ctx = withClaims(ctx, claims)
            return next(ctx, req)
        }
    }
}
```

#### 3. API Key

Best for: Simple, long-lived service credentials

```go
// API key interceptor
func NewAPIKeyInterceptor(apiKey string) connect.UnaryInterceptorFunc {
    return func(next connect.UnaryFunc) connect.UnaryFunc {
        return func(ctx context.Context, req connect.AnyRequest) (connect.AnyResponse, error) {
            req.Header().Set("X-API-Key", apiKey)
            return next(ctx, req)
        }
    }
}
```

#### 4. OIDC (OpenID Connect)

Best for: Enterprise SSO, delegated auth

```go
// OIDC token source
type OIDCTokenSource struct {
    Provider     *oidc.Provider
    ClientID     string
    ClientSecret string
    Scopes       []string

    mu           sync.Mutex
    token        *oauth2.Token
}

func (s *OIDCTokenSource) Token() (*oauth2.Token, error) {
    s.mu.Lock()
    defer s.mu.Unlock()

    if s.token != nil && s.token.Valid() {
        return s.token, nil
    }

    // Refresh or obtain new token
    config := clientcredentials.Config{
        ClientID:     s.ClientID,
        ClientSecret: s.ClientSecret,
        TokenURL:     s.Provider.Endpoint().TokenURL,
        Scopes:       s.Scopes,
    }
    token, err := config.Token(context.Background())
    if err != nil {
        return nil, err
    }
    s.token = token
    return token, nil
}
```

## Bidirectional Authentication

### Challenge: Host ↔ Plugin

For capabilities (host calls back to plugin), both sides need to authenticate:

```
┌──────────────────┐                           ┌──────────────────┐
│      Host        │                           │     Plugin       │
│                  │                           │                  │
│  1. Call plugin  │ ───── Host Token ───────► │  Verify host     │
│     with host    │                           │  identity        │
│     credentials  │                           │                  │
│                  │                           │                  │
│                  │ ◄── Plugin Token ──────── │  2. Plugin calls │
│  Verify plugin   │                           │     capability   │
│  identity        │                           │     with plugin  │
│                  │                           │     credentials  │
└──────────────────┘                           └──────────────────┘
```

### Solution: Mutual Token Exchange

```go
// During handshake, exchange tokens
type HandshakeRequest struct {
    // ... other fields
    HostToken string `json:"host_token,omitempty"` // Token for plugin to verify host
}

type HandshakeResponse struct {
    // ... other fields
    PluginToken string `json:"plugin_token,omitempty"` // Token for host to verify plugin
}

// Capability grant includes plugin's token for callbacks
type CapabilityGrant struct {
    CapabilityID   string    `json:"capability_id"`
    EndpointURL    string    `json:"endpoint_url"`
    BearerToken    string    `json:"bearer_token"`    // Plugin uses this to call capability
    CallbackToken  string    `json:"callback_token"`  // Host uses this if capability calls back
    ExpiresAt      time.Time `json:"expires_at"`
}
```

### Token Exchange Flow

```
1. Host → Plugin: Handshake with host_token
2. Plugin validates host_token (is this a legitimate host?)
3. Plugin → Host: Handshake response with plugin_token
4. Host validates plugin_token (is this the expected plugin?)

5. Host grants capability with bearer_token
6. Plugin calls capability with bearer_token

7. If capability needs callback:
   - Capability response includes callback_capability
   - Plugin exposes callback endpoint
   - Host calls back with callback_token from capability grant
```

## Token Refresh Strategies

### Challenge: Long-Lived Connections

Plugins may run for hours/days. JWT tokens typically expire in minutes/hours.

### Strategy 1: Proactive Refresh

Refresh token before it expires:

```go
type RefreshingTokenSource struct {
    source        TokenSource
    refreshBefore time.Duration // Refresh this long before expiry

    mu            sync.RWMutex
    current       *Token
    refreshing    bool
}

func (r *RefreshingTokenSource) Token() (string, error) {
    r.mu.RLock()
    if r.current != nil && time.Until(r.current.ExpiresAt) > r.refreshBefore {
        token := r.current.Value
        r.mu.RUnlock()
        return token, nil
    }
    r.mu.RUnlock()

    return r.refreshToken()
}

func (r *RefreshingTokenSource) refreshToken() (string, error) {
    r.mu.Lock()
    defer r.mu.Unlock()

    // Double-check after acquiring write lock
    if r.current != nil && time.Until(r.current.ExpiresAt) > r.refreshBefore {
        return r.current.Value, nil
    }

    token, err := r.source.Refresh()
    if err != nil {
        return "", err
    }
    r.current = token
    return token.Value, nil
}
```

### Strategy 2: On-Demand Refresh

Refresh when request fails with auth error:

```go
func NewRetryAuthInterceptor(tokenSource RefreshableTokenSource) connect.UnaryInterceptorFunc {
    return func(next connect.UnaryFunc) connect.UnaryFunc {
        return func(ctx context.Context, req connect.AnyRequest) (connect.AnyResponse, error) {
            // Try with current token
            token, _ := tokenSource.Token()
            req.Header().Set("Authorization", "Bearer "+token)

            resp, err := next(ctx, req)
            if err == nil {
                return resp, nil
            }

            // If auth error, refresh and retry
            if connect.CodeOf(err) == connect.CodeUnauthenticated {
                newToken, refreshErr := tokenSource.Refresh()
                if refreshErr != nil {
                    return nil, err // Return original error
                }

                // Clone request with new token
                req.Header().Set("Authorization", "Bearer "+newToken)
                return next(ctx, req)
            }

            return resp, err
        }
    }
}
```

### Strategy 3: Streaming Token Updates

For long-running streams, push token updates:

```go
// Capability broker can push new tokens
type CapabilityUpdate struct {
    CapabilityID string    `json:"capability_id"`
    NewToken     string    `json:"new_token,omitempty"`
    ExpiresAt    time.Time `json:"expires_at,omitempty"`
    Revoked      bool      `json:"revoked,omitempty"`
}

// Plugin listens for updates
func (p *Plugin) watchCapabilityUpdates(stream CapabilityUpdateStream) {
    for {
        update, err := stream.Receive()
        if err != nil {
            return
        }

        if update.Revoked {
            p.revokeCapability(update.CapabilityID)
        } else if update.NewToken != "" {
            p.updateCapabilityToken(update.CapabilityID, update.NewToken, update.ExpiresAt)
        }
    }
}
```

## Auth Context Propagation

### Nested Calls

When plugin calls back to host, propagate auth context:

```go
// Context key for auth claims
type authClaimsKey struct{}

func withClaims(ctx context.Context, claims Claims) context.Context {
    return context.WithValue(ctx, authClaimsKey{}, claims)
}

func claimsFromContext(ctx context.Context) (Claims, bool) {
    claims, ok := ctx.Value(authClaimsKey{}).(Claims)
    return claims, ok
}

// Propagating interceptor
func NewContextPropagationInterceptor() connect.UnaryInterceptorFunc {
    return func(next connect.UnaryFunc) connect.UnaryFunc {
        return func(ctx context.Context, req connect.AnyRequest) (connect.AnyResponse, error) {
            // If we have claims in context, include them in outgoing request
            if claims, ok := claimsFromContext(ctx); ok {
                // Encode claims as header for downstream
                req.Header().Set("X-Forwarded-Claims", encodeClaims(claims))
            }
            return next(ctx, req)
        }
    }
}
```

## Interceptor Chain Design

### Recommended Order

```go
// Client-side interceptor chain
client := pluginclient.NewClient(
    connect.WithInterceptors(
        // 1. Logging (outermost - sees all requests/responses)
        loggingInterceptor,

        // 2. Retry (retries on transient failures)
        retryInterceptor,

        // 3. Circuit breaker (fails fast if plugin unhealthy)
        circuitBreakerInterceptor,

        // 4. Auth (adds credentials, innermost before actual call)
        authInterceptor,
    ),
)

// Server-side interceptor chain
handler := pluginserver.NewHandler(
    impl,
    connect.WithInterceptors(
        // 1. Recovery (outermost - catches panics)
        recoveryInterceptor,

        // 2. Logging
        loggingInterceptor,

        // 3. Auth validation (validates credentials)
        authValidationInterceptor,

        // 4. Authorization (checks permissions)
        authorizationInterceptor,
    ),
)
```

### Full Auth Interceptor Example

```go
// Complete auth interceptor for connect-plugin
type AuthInterceptor struct {
    tokenSource   TokenSource
    refreshBefore time.Duration
}

func (a *AuthInterceptor) WrapUnary(next connect.UnaryFunc) connect.UnaryFunc {
    return func(ctx context.Context, req connect.AnyRequest) (connect.AnyResponse, error) {
        // Get token (may refresh if needed)
        token, err := a.tokenSource.Token()
        if err != nil {
            return nil, connect.NewError(connect.CodeUnauthenticated,
                fmt.Errorf("failed to get auth token: %w", err))
        }

        // Set Authorization header
        req.Header().Set("Authorization", "Bearer "+token)

        // Make the call
        resp, err := next(ctx, req)

        // Handle auth errors
        if err != nil && connect.CodeOf(err) == connect.CodeUnauthenticated {
            // Try to refresh and retry once
            if newToken, refreshErr := a.tokenSource.Refresh(); refreshErr == nil {
                req.Header().Set("Authorization", "Bearer "+newToken)
                return next(ctx, req)
            }
        }

        return resp, err
    }
}

func (a *AuthInterceptor) WrapStreamingClient(next connect.StreamingClientFunc) connect.StreamingClientFunc {
    return func(ctx context.Context, spec connect.Spec) connect.StreamingClientConn {
        conn := next(ctx, spec)

        // Set initial token
        token, _ := a.tokenSource.Token()
        conn.RequestHeader().Set("Authorization", "Bearer "+token)

        return conn
    }
}

func (a *AuthInterceptor) WrapStreamingHandler(next connect.StreamingHandlerFunc) connect.StreamingHandlerFunc {
    return next // Server-side doesn't add outgoing auth
}
```

## Service Mesh Integration

### When to Delegate to Service Mesh

| Scenario | Handle in connect-plugin | Delegate to mesh |
|----------|-------------------------|------------------|
| Single cluster, simple auth | ✓ | |
| Multi-cluster, complex routing | | ✓ |
| Compliance requirements (audit) | | ✓ |
| Zero-trust, fine-grained authz | | ✓ |
| Simple mTLS between services | ✓ | |

### Istio Integration

When running in Istio:

```yaml
# PeerAuthentication - require mTLS
apiVersion: security.istio.io/v1beta1
kind: PeerAuthentication
metadata:
  name: plugin-mtls
spec:
  selector:
    matchLabels:
      app: my-plugin
  mtls:
    mode: STRICT

---
# AuthorizationPolicy - allow only host to call plugin
apiVersion: security.istio.io/v1beta1
kind: AuthorizationPolicy
metadata:
  name: plugin-authz
spec:
  selector:
    matchLabels:
      app: my-plugin
  rules:
  - from:
    - source:
        principals: ["cluster.local/ns/default/sa/host-app"]
```

In this case, connect-plugin can skip mTLS and token auth - the mesh handles it.

## Configuration Interface

### Proposed API

```go
// Auth configuration options
type AuthConfig struct {
    // Transport layer
    TLS *TLSConfig

    // Application layer (choose one or combine)
    BearerToken  *BearerTokenConfig
    APIKey       *APIKeyConfig
    OIDC         *OIDCConfig

    // Bidirectional auth
    CallbackAuth *CallbackAuthConfig
}

type TLSConfig struct {
    // For server verification
    RootCAs      *x509.CertPool
    ServerName   string

    // For mTLS (client cert)
    Certificate  *tls.Certificate

    // Skip verification (testing only)
    InsecureSkipVerify bool
}

type BearerTokenConfig struct {
    // Static token
    Token string

    // Or dynamic token source
    TokenSource TokenSource

    // Refresh settings
    RefreshBefore time.Duration
}

type OIDCConfig struct {
    IssuerURL    string
    ClientID     string
    ClientSecret string
    Scopes       []string
}

type CallbackAuthConfig struct {
    // Token plugin should use to call capabilities
    CapabilityTokenSource TokenSource

    // Token host should use for callbacks
    CallbackTokenValidator TokenValidator
}
```

### Usage Example

```go
// Host configuring auth
client, _ := connectplugin.NewClient(pluginURL,
    connectplugin.WithAuth(connectplugin.AuthConfig{
        TLS: &connectplugin.TLSConfig{
            RootCAs: certPool,
        },
        OIDC: &connectplugin.OIDCConfig{
            IssuerURL:    "https://auth.example.com",
            ClientID:     "plugin-client",
            ClientSecret: os.Getenv("CLIENT_SECRET"),
            Scopes:       []string{"plugin:read", "plugin:write"},
        },
    }),
)
```

## Conclusions

1. **Layer auth mechanisms**: mTLS for transport, tokens for application
2. **Support multiple token types**: JWT, API key, OIDC for different deployments
3. **Handle bidirectional auth**: Both host and plugin may need credentials
4. **Implement token refresh**: Proactive refresh for long-lived connections
5. **Design interceptor chain**: Logging → Retry → Circuit breaker → Auth
6. **Enable service mesh delegation**: Don't duplicate what the mesh provides

## Next Steps

1. Design `connectplugin.AuthConfig` interface
2. Implement bearer token interceptor with refresh
3. Implement OIDC token source
4. Design capability token exchange protocol
5. Add service mesh detection (skip auth if mesh handles it)
