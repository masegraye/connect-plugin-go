# Connect RPC (connect-go) Analysis

## Overview

Connect is a slim library for building browser and gRPC-compatible HTTP APIs. It generates code from Protocol Buffer schemas to handle marshaling, routing, compression, and content type negotiation. Handlers and clients support three protocols: gRPC, gRPC-Web, and Connect's own protocol.

**Key Differentiator**: Connect works over standard HTTP/1.1 or HTTP/2, uses the standard library's `net/http`, and doesn't require any custom infrastructure.

## Architecture

### Core Design Philosophy

```
┌─────────────────────────────────────────────────────────────────────┐
│                      PROTOCOL ABSTRACTION                            │
│  ┌──────────────┐  ┌──────────────┐  ┌──────────────────────────┐   │
│  │   Connect    │  │    gRPC      │  │       gRPC-Web           │   │
│  │  Protocol    │  │  Protocol    │  │       Protocol           │   │
│  └──────────────┘  └──────────────┘  └──────────────────────────┘   │
│         │                 │                      │                   │
│         └─────────────────┴──────────────────────┘                   │
│                           │                                          │
│                    ┌──────▼──────┐                                   │
│                    │   net/http   │                                   │
│                    └─────────────┘                                   │
└─────────────────────────────────────────────────────────────────────┘
```

### Protocol Support

| Protocol | Transport | Streaming | Browser | Notes |
|----------|-----------|-----------|---------|-------|
| Connect | HTTP/1.1, HTTP/2 | Yes (HTTP/2) | Yes | Simple, curl-friendly |
| gRPC | HTTP/2 | Yes | No | Full gRPC compatibility |
| gRPC-Web | HTTP/1.1, HTTP/2 | Server only | Yes | Browser-compatible gRPC |

### Key Components

#### 1. Client (`client.go:34-39`)

```go
type Client[Req, Res any] struct {
    config         *clientConfig
    callUnary      func(context.Context, *Request[Req]) (*Response[Res], error)
    protocolClient protocolClient
    err            error
}
```

Generic client supporting:
- `CallUnary` - request/response
- `CallClientStream` - client streaming
- `CallServerStream` - server streaming
- `CallBidiStream` - bidirectional streaming

#### 2. Handler (`handler.go:28-34`)

```go
type Handler struct {
    spec             Spec
    implementation   StreamingHandlerFunc
    protocolHandlers map[string][]protocolHandler
    allowMethod      string
    acceptPost       string
}
```

Implements `http.Handler`, automatically negotiates protocol based on Content-Type.

#### 3. Protocol Interface (`protocol.go:66-69`)

```go
type protocol interface {
    NewHandler(*protocolHandlerParams) protocolHandler
    NewClient(*protocolClientParams) (protocolClient, error)
}
```

Internal abstraction allowing Connect, gRPC, and gRPC-Web to share infrastructure.

#### 4. Interceptors (`interceptor.go` - referenced)

```go
type Interceptor interface {
    WrapUnary(UnaryFunc) UnaryFunc
    WrapStreamingClient(func(context.Context, Spec) StreamingClientConn) func(context.Context, Spec) StreamingClientConn
    WrapStreamingHandler(StreamingHandlerFunc) StreamingHandlerFunc
}
```

Middleware pattern for cross-cutting concerns (logging, auth, validation).

### Code Generation

Connect uses `protoc-gen-connect-go` to generate:

1. **Service Handlers**: Type-safe handler constructors
2. **Service Clients**: Type-safe client constructors
3. **Unimplemented Handlers**: Base implementations returning errors

Example generated code pattern:
```go
// Generated handler constructor
func NewPingServiceHandler(svc PingServiceHandler, opts ...connect.HandlerOption) (string, http.Handler)

// Generated client constructor
func NewPingServiceClient(httpClient connect.HTTPClient, baseURL string, opts ...connect.ClientOption) PingServiceClient
```

### HTTP Semantics

#### Request Flow
```
Client                          Server
  │                               │
  │  HTTP Request                 │
  │  - Content-Type: application/proto (or json)
  │  - Body: Protobuf message     │
  │──────────────────────────────▶│
  │                               │
  │  HTTP Response                │
  │  - Status: 200                │
  │  - Body: Protobuf message     │
  │◀──────────────────────────────│
```

#### Connect Protocol
- Unary: Single POST request/response
- Streaming: Uses envelope framing (5-byte header: flags + length)
- Errors: Returned as JSON in response body or trailers

#### gRPC Protocol
- Always HTTP/2
- Trailers for status/errors
- `grpc-status` header for error codes

### Configuration Options

#### Client Options
- `WithGRPC()` - Use gRPC protocol
- `WithGRPCWeb()` - Use gRPC-Web protocol
- `WithProtoJSON()` - Use JSON encoding
- `WithCompression()` - Request compression
- `WithInterceptors()` - Add interceptors

#### Handler Options
- `WithCompression()` - Response compression
- `WithInterceptors()` - Add interceptors
- `WithReadMaxBytes()` - Limit request size
- `WithRequireConnectProtocolHeader()` - Strict Connect protocol

## Strengths

### 1. Standard HTTP
- Works with any HTTP infrastructure
- Load balancers, proxies, CDNs all work
- No special requirements like HTTP/2-only

### 2. Protocol Flexibility
- Same code serves Connect, gRPC, gRPC-Web
- Client can choose protocol per-request
- Graceful fallback possible

### 3. Minimal Dependencies
```go
require (
    github.com/google/go-cmp v0.5.9
    google.golang.org/protobuf v1.36.9
)
```
Only protobuf dependency, no grpc-go required.

### 4. net/http Native
- `http.Handler` for servers
- `http.Client` for clients
- All net/http middleware works
- Easy testing with httptest

### 5. Type Safety with Generics
```go
type Client[Req, Res any] struct { ... }
```
Full compile-time type checking.

### 6. Browser Compatible
- Connect protocol works with fetch()
- gRPC-Web for existing gRPC services
- No proxy required for Connect protocol

### 7. curl Friendly
```bash
curl --header "Content-Type: application/json" \
     --data '{"sentence": "Hello"}' \
     http://localhost:8080/service/Method
```

### 8. Interceptor Pattern
Clean middleware abstraction for:
- Logging
- Metrics
- Authentication
- Validation (protovalidate integration)

### 9. Streaming Support
- Server streaming (HTTP/1.1 compatible)
- Client streaming (HTTP/2)
- Bidirectional streaming (HTTP/2)

### 10. Error Handling
- Structured errors with codes
- Error details support
- Maps to/from HTTP status codes

## Weaknesses

### 1. No Built-in Service Discovery
- Requires external service discovery
- No client-side load balancing (relies on HTTP client)

### 2. No Built-in Retry Logic
- Must implement retry in interceptors
- No exponential backoff built-in

### 3. No Connection Pooling Management
- Relies on http.Client pooling
- No circuit breaker patterns

### 4. Bidirectional Streaming Requires HTTP/2
- Falls back to half-duplex on HTTP/1.1
- Browser support limited

### 5. Code Generation Required
- Must run protoc + plugin
- Build pipeline complexity

### 6. Limited Health Check Integration
- grpchealth-go is separate package
- No automatic health registration

## Key Files for Reference

| File | Purpose |
|------|---------|
| `client.go` | Generic client implementation |
| `handler.go` | HTTP handler implementation |
| `protocol.go` | Protocol abstraction interface |
| `protocol_connect.go` | Connect protocol implementation |
| `protocol_grpc.go` | gRPC protocol implementation |
| `interceptor.go` | Interceptor interface |
| `error.go` | Error types and codes |
| `codec.go` | Serialization (proto, JSON) |
| `compression.go` | Compression (gzip) |

## Extension Points

### HTTPClient Interface
```go
type HTTPClient interface {
    Do(*http.Request) (*http.Response, error)
}
```
Allows custom HTTP clients with middleware.

### Codec Interface
```go
type Codec interface {
    Name() string
    Marshal(any) ([]byte, error)
    Unmarshal([]byte, any) error
}
```
Custom serialization formats possible.

### Compressor Interface
Custom compression algorithms supported.

## Why Connect for Plugin System?

### Advantages Over gRPC-go

1. **Simpler HTTP model** - easier to reason about network failures
2. **HTTP/1.1 fallback** - works through more proxies/load balancers
3. **Smaller footprint** - fewer dependencies
4. **Browser compatibility** - future extensibility to web-based plugin management
5. **Better testability** - standard net/http testing patterns

### Network Resilience Considerations

Connect's HTTP-based model is better suited for unreliable networks because:

1. **Standard HTTP retry semantics** - well-understood by infrastructure
2. **Connection pooling via http.Client** - automatic reconnection
3. **Load balancer friendly** - no special protocol requirements
4. **Timeout handling** - standard HTTP timeout patterns

### What We Need to Add

For a plugin system over Connect, we need:

1. **Plugin discovery mechanism** - replacing stdout handshake
2. **Health checking** - detecting plugin availability
3. **Retry logic** - handling transient failures
4. **Circuit breaker** - preventing cascade failures
5. **Plugin lifecycle management** - container orchestration integration

## Conclusion

Connect RPC provides an excellent transport layer for a remote plugin system:

- Standard HTTP semantics for network resilience
- Multiple protocol support for flexibility
- Clean abstractions for extensibility
- Minimal dependencies

It lacks the plugin-specific features of go-plugin (lifecycle management, interface dispensing, bidirectional broker) but provides a better foundation for unreliable network communication.
