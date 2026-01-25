# Spike: Connect RPC Deep Dive - Interceptors & Middleware

**Issue:** KOR-bpyd
**Status:** Complete

## Executive Summary

Connect's interceptor system provides a clean, composable middleware pattern that wraps both unary and streaming RPCs. Unlike gRPC's separate unary/stream interceptor types, Connect uses a single `Interceptor` interface with three wrap methods. Interceptors can modify requests, responses, headers, and handle errors. The same interceptor works for both clients and handlers.

## Core Interceptor Interface

### Interceptor (interceptor.go:50-54)

```go
type Interceptor interface {
    WrapUnary(UnaryFunc) UnaryFunc
    WrapStreamingClient(StreamingClientFunc) StreamingClientFunc
    WrapStreamingHandler(StreamingHandlerFunc) StreamingHandlerFunc
}
```

### Function Signatures

```go
// Unary RPC handler
type UnaryFunc func(context.Context, AnyRequest) (AnyResponse, error)

// Streaming client (client-side view of stream)
type StreamingClientFunc func(context.Context, Spec) StreamingClientConn

// Streaming handler (server-side view of stream)
type StreamingHandlerFunc func(context.Context, StreamingHandlerConn) error
```

## Streaming Connection Interfaces

### StreamingClientConn (connect.go:126-143)

Client's view of any streaming RPC:

```go
type StreamingClientConn interface {
    // Safe to call concurrently with all methods
    Spec() Spec
    Peer() Peer

    // May race with each other, safe with other methods
    Send(any) error
    RequestHeader() http.Header
    CloseRequest() error

    // May race with each other, safe with other methods
    Receive(any) error
    ResponseHeader() http.Header
    ResponseTrailer() http.Header
    CloseResponse() error
}
```

### StreamingHandlerConn (connect.go:91-101)

Server's view of any streaming RPC:

```go
type StreamingHandlerConn interface {
    Spec() Spec
    Peer() Peer

    Receive(any) error
    RequestHeader() http.Header

    Send(any) error
    ResponseHeader() http.Header
    ResponseTrailer() http.Header
}
```

## Convenience Types

### UnaryInterceptorFunc (interceptor.go:58-71)

For interceptors that only need to wrap unary RPCs:

```go
type UnaryInterceptorFunc func(UnaryFunc) UnaryFunc

func (f UnaryInterceptorFunc) WrapUnary(next UnaryFunc) UnaryFunc {
    return f(next)
}

// WrapStreamingClient and WrapStreamingHandler are no-ops
```

## Interceptor Chaining

### Chain Mechanics (interceptor.go:74-113)

Interceptors compose in "onion" order - first interceptor is outermost:

```go
func newChain(interceptors []Interceptor) *chain {
    // Reverse order so first interceptor acts first
    var chain chain
    for i := len(interceptors) - 1; i >= 0; i-- {
        if interceptor := interceptors[i]; interceptor != nil {
            chain.interceptors = append(chain.interceptors, interceptor)
        }
    }
    return &chain
}

func (c *chain) WrapUnary(next UnaryFunc) UnaryFunc {
    for _, interceptor := range c.interceptors {
        next = unaryThunk(next)  // Sentinel check
        next = interceptor.WrapUnary(next)
    }
    return next
}
```

### Execution Order

```
WithInterceptors(A, B, C) produces:

Request flow:   A → B → C → handler
Response flow:  A ← B ← C ← handler

For streaming, the connections are wrapped in the same order:
- First interceptor wraps outermost
- Last interceptor wraps closest to the actual transport
```

## Configuration via Options

### WithInterceptors (option.go:350-352)

```go
func WithInterceptors(interceptors ...Interceptor) Option {
    return &interceptorsOption{interceptors}
}

// Can be used on both clients and handlers
type Option interface {
    ClientOption
    HandlerOption
}
```

### Usage Examples

**Client-side:**
```go
client := pingv1connect.NewPingServiceClient(
    httpClient,
    baseURL,
    connect.WithInterceptors(loggingInterceptor, authInterceptor),
)
```

**Handler-side:**
```go
path, handler := pingv1connect.NewPingServiceHandler(
    pingServer,
    connect.WithInterceptors(loggingInterceptor, recoverInterceptor),
)
```

## Built-in Interceptors

### Recover Interceptor (recover.go)

```go
type recoverHandlerInterceptor struct {
    Interceptor
    handle func(context.Context, Spec, http.Header, any) error
}

func (i *recoverHandlerInterceptor) WrapUnary(next UnaryFunc) UnaryFunc {
    return func(ctx context.Context, req AnyRequest) (_ AnyResponse, retErr error) {
        if req.Spec().IsClient {
            return next(ctx, req)  // No-op for clients
        }
        defer func() {
            if r := recover(); r != nil {
                if r == http.ErrAbortHandler {
                    panic(r)  // Re-panic for http.Server compatibility
                }
                retErr = i.handle(ctx, req.Spec(), req.Header(), r)
            }
        }()
        return next(ctx, req)
    }
}

func (i *recoverHandlerInterceptor) WrapStreamingHandler(next StreamingHandlerFunc) StreamingHandlerFunc {
    return func(ctx context.Context, conn StreamingHandlerConn) (retErr error) {
        defer func() {
            if r := recover(); r != nil {
                if r == http.ErrAbortHandler {
                    panic(r)
                }
                retErr = i.handle(ctx, conn.Spec(), conn.RequestHeader(), r)
            }
        }()
        return next(ctx, conn)
    }
}
```

## Request/Response Types

### AnyRequest (connect.go:224-233)

Generic request interface used in interceptors:

```go
type AnyRequest interface {
    Any() any           // Returns underlying message
    Spec() Spec         // RPC specification
    Peer() Peer         // Client/server info
    Header() http.Header
    HTTPMethod() string
}
```

### AnyResponse (connect.go:298-304)

Generic response interface used in interceptors:

```go
type AnyResponse interface {
    Any() any
    Header() http.Header
    Trailer() http.Header
}
```

### Spec (connect.go:316-322)

RPC metadata available in interceptors:

```go
type Spec struct {
    StreamType       StreamType        // Unary, Client, Server, Bidi
    Schema           any               // protoreflect.MethodDescriptor for protobuf
    Procedure        string            // "/service/method"
    IsClient         bool              // true = client, false = handler
    IdempotencyLevel IdempotencyLevel
}
```

## Stream Types

### StreamType (connect.go:49-56)

```go
type StreamType uint8

const (
    StreamTypeUnary  StreamType = 0b00
    StreamTypeClient StreamType = 0b01
    StreamTypeServer StreamType = 0b10
    StreamTypeBidi              = StreamTypeClient | StreamTypeServer
)
```

## Typed Stream Wrappers

Connect provides typed wrappers for different streaming patterns:

### Client Streaming (client sends multiple, server responds once)
```go
type ClientStreamForClient[Req, Res any] struct {
    conn StreamingClientConn
    // ...
}

func (c *ClientStreamForClient[Req, Res]) Send(request *Req) error
func (c *ClientStreamForClient[Req, Res]) CloseAndReceive() (*Response[Res], error)
```

### Server Streaming (client sends once, server responds multiple)
```go
type ServerStreamForClient[Res any] struct {
    conn StreamingClientConn
    // ...
}

func (s *ServerStreamForClient[Res]) Receive() bool
func (s *ServerStreamForClient[Res]) Msg() *Res
func (s *ServerStreamForClient[Res]) Err() error
```

### Bidirectional Streaming
```go
type BidiStreamForClient[Req, Res any] struct {
    conn StreamingClientConn
    // ...
}

func (b *BidiStreamForClient[Req, Res]) Send(msg *Req) error
func (b *BidiStreamForClient[Req, Res]) Receive() (*Res, error)
func (b *BidiStreamForClient[Req, Res]) CloseRequest() error
func (b *BidiStreamForClient[Req, Res]) CloseResponse() error
```

## Context Protection

### Sentinel Check (interceptor.go:133-138)

Connect prevents interceptors from creating new contexts that would break call tracking:

```go
func checkSentinel(ctx context.Context) error {
    if ctx.Value(clientCallInfoContextKey{}) != ctx.Value(sentinelContextKey{}) {
        return errNewClientContextProhibited
    }
    return nil
}
```

This is checked via `unaryThunk` between each interceptor in the chain.

## Practical Interceptor Examples

### Logging Interceptor

```go
loggingInterceptor := connect.UnaryInterceptorFunc(
    func(next connect.UnaryFunc) connect.UnaryFunc {
        return func(ctx context.Context, req connect.AnyRequest) (connect.AnyResponse, error) {
            logger.Println("calling:", req.Spec().Procedure)
            logger.Println("request:", req.Any())

            response, err := next(ctx, req)

            if err != nil {
                logger.Println("error:", err)
            } else {
                logger.Println("response:", response.Any())
            }
            return response, err
        }
    },
)
```

### Full Interceptor (Unary + Streaming)

```go
type myInterceptor struct{}

func (i *myInterceptor) WrapUnary(next connect.UnaryFunc) connect.UnaryFunc {
    return func(ctx context.Context, req connect.AnyRequest) (connect.AnyResponse, error) {
        // Pre-processing
        start := time.Now()

        resp, err := next(ctx, req)

        // Post-processing
        duration := time.Since(start)
        log.Printf("%s took %v", req.Spec().Procedure, duration)

        return resp, err
    }
}

func (i *myInterceptor) WrapStreamingClient(next connect.StreamingClientFunc) connect.StreamingClientFunc {
    return func(ctx context.Context, spec connect.Spec) connect.StreamingClientConn {
        conn := next(ctx, spec)
        return &wrappedClientConn{StreamingClientConn: conn}
    }
}

func (i *myInterceptor) WrapStreamingHandler(next connect.StreamingHandlerFunc) connect.StreamingHandlerFunc {
    return func(ctx context.Context, conn connect.StreamingHandlerConn) error {
        wrapped := &wrappedHandlerConn{StreamingHandlerConn: conn}
        return next(ctx, wrapped)
    }
}
```

### Streaming Connection Wrapper

```go
type wrappedClientConn struct {
    connect.StreamingClientConn
}

func (w *wrappedClientConn) Send(msg any) error {
    log.Printf("sending: %v", msg)
    return w.StreamingClientConn.Send(msg)
}

func (w *wrappedClientConn) Receive(msg any) error {
    err := w.StreamingClientConn.Receive(msg)
    log.Printf("received: %v (err=%v)", msg, err)
    return err
}
```

## Key Design Decisions for connect-plugin

### What We Can Leverage

1. **Same interceptor interface**: connect-plugin interceptors can follow the same pattern
2. **Typed wrappers**: Provide typed stream wrappers for ergonomic API
3. **Option pattern**: Use same `WithInterceptors()` option style
4. **Onion composition**: Same chaining semantics

### Plugin-Specific Interceptors

For connect-plugin, we need interceptors that:

1. **Circuit breaker**: Track failures, open circuit on threshold
2. **Retry**: Retry failed requests with backoff
3. **Auth**: Inject/validate authentication tokens
4. **Metrics**: Track plugin call latency, errors
5. **Tracing**: Propagate trace context to plugins

### Interceptor Chain Architecture

```
Client Application
       │
       ▼
┌─────────────────────────────────────┐
│  connect-plugin Client              │
│  ┌───────────────────────────────┐  │
│  │ Circuit Breaker Interceptor   │  │
│  │ Retry Interceptor             │  │
│  │ Auth Interceptor              │  │
│  │ Metrics Interceptor           │  │
│  └───────────────────────────────┘  │
│              │                      │
│              ▼                      │
│  ┌───────────────────────────────┐  │
│  │ Connect Client                │  │
│  │ (uses Connect's interceptors) │  │
│  └───────────────────────────────┘  │
└──────────────┬──────────────────────┘
               │
          HTTP/2
               │
               ▼
         Plugin Server
```

### Streaming Considerations

For streaming RPCs in connect-plugin:

1. **Circuit breaker** should only count terminal errors, not per-message
2. **Retry** typically not applicable for streams (would need to replay)
3. **Metrics** should track stream duration and message counts
4. **Auth** applied once at stream start, may need refresh for long streams

## Comparison: Connect vs gRPC Interceptors

| Aspect | Connect | gRPC |
|--------|---------|------|
| Interface | Single `Interceptor` | Separate unary/stream |
| Streaming client | `StreamingClientFunc` | `StreamClientInterceptor` |
| Streaming server | `StreamingHandlerFunc` | `StreamServerInterceptor` |
| Context | Wrapped, sentinel-protected | Direct context |
| Request access | `AnyRequest` interface | Direct message |
| Composable | Via `newChain` | Via `ChainUnaryInterceptor` |

## Conclusions

1. **Clean interface**: Single Interceptor interface handles all cases
2. **Type-erased for flexibility**: AnyRequest/AnyResponse allow generic handling
3. **Typed wrappers for ergonomics**: Concrete stream types for user-facing API
4. **Composable**: Chaining works as expected (onion model)
5. **Same code for client/handler**: Interceptor works on both sides

## Next Steps

1. Design connect-plugin interceptor extensions (circuit breaker, retry)
2. Implement metrics interceptor for plugin health tracking
3. Design auth interceptor chain for flexible authentication
4. Consider streaming-specific interceptors for long-running streams
