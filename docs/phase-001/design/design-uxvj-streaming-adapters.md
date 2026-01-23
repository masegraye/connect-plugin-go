# Design: Streaming Adapter Patterns (chan <-> stream)

**Issue:** KOR-uxvj
**Status:** Complete
**Dependencies:** KOR-yosi, KOR-neyu

## Overview

Streaming adapters bridge Connect's method-based streaming with Go's idiomatic channel-based concurrency. This enables plugin authors to write natural Go code using channels while the generated code handles stream lifecycle, backpressure, and error propagation.

## Design Goals

1. **Idiomatic Go**: Plugin implementations use channels, not streaming APIs
2. **Safe lifecycle**: Automatic cleanup on context cancellation
3. **Backpressure**: Honor stream flow control and channel buffer limits
4. **Error propagation**: Clear error paths from implementation to caller
5. **Zero goroutine leaks**: All goroutines cleaned up on completion

## Streaming Patterns

### Server Streaming (server sends multiple, client receives)

**Proto:**
```protobuf
service LogService {
    rpc Tail(TailRequest) returns (stream LogEntry);
}
```

**Generated Interface (idiomatic Go):**
```go
// LogService is the interface plugin authors implement
type LogService interface {
    // Tail returns a channel of log entries.
    // The implementation sends entries and closes the channel when done.
    // Return nil to indicate error (use context for cancellation).
    Tail(ctx context.Context, req *TailRequest) (<-chan *LogEntry, error)
}
```

**Generated Adapter:**
```go
// serverStreamAdapter adapts a Go channel to a Connect server stream
type logServiceHandler struct {
    impl LogService
}

func (h *logServiceHandler) Tail(
    ctx context.Context,
    req *connect.Request[TailRequest],
    stream *connect.ServerStream[LogEntry],
) error {
    // Call implementation to get channel
    ch, err := h.impl.Tail(ctx, req.Msg)
    if err != nil {
        return err
    }

    // Pump channel to stream
    return pumpChannelToStream(ctx, ch, stream)
}

// pumpChannelToStream sends channel values to stream until channel closes
func pumpChannelToStream[T any](
    ctx context.Context,
    ch <-chan T,
    stream *connect.ServerStream[T],
) error {
    for {
        select {
        case <-ctx.Done():
            return ctx.Err()
        case msg, ok := <-ch:
            if !ok {
                // Channel closed, stream complete
                return nil
            }
            if err := stream.Send(&msg); err != nil {
                return err
            }
        }
    }
}
```

### Client Streaming (client sends multiple, server responds once)

**Proto:**
```protobuf
service UploadService {
    rpc Upload(stream Chunk) returns (UploadResult);
}
```

**Generated Interface (idiomatic Go):**
```go
// UploadService is the interface plugin authors implement
type UploadService interface {
    // Upload receives chunks on a channel and returns result.
    // The channel is closed when client finishes sending.
    Upload(ctx context.Context, chunks <-chan *Chunk) (*UploadResult, error)
}
```

**Generated Adapter:**
```go
type uploadServiceHandler struct {
    impl UploadService
}

func (h *uploadServiceHandler) Upload(
    ctx context.Context,
    stream *connect.ClientStream[Chunk],
) (*connect.Response[UploadResult], error) {
    // Create buffered channel
    ch := make(chan *Chunk, 10)

    // Pump stream to channel in background
    errCh := make(chan error, 1)
    go func() {
        defer close(ch)
        errCh <- pumpStreamToChannel(ctx, stream, ch)
    }()

    // Call implementation with channel
    result, err := h.impl.Upload(ctx, ch)

    // Wait for pump goroutine to finish
    pumpErr := <-errCh
    if pumpErr != nil {
        return nil, pumpErr
    }
    if err != nil {
        return nil, err
    }

    return connect.NewResponse(result), nil
}

// pumpStreamToChannel receives from stream and sends to channel
func pumpStreamToChannel[T any](
    ctx context.Context,
    stream *connect.ClientStream[T],
    ch chan<- *T,
) error {
    for stream.Receive() {
        select {
        case <-ctx.Done():
            return ctx.Err()
        case ch <- stream.Msg():
        }
    }
    return stream.Err()
}
```

### Bidirectional Streaming (both send multiple)

**Proto:**
```protobuf
service ChatService {
    rpc Chat(stream ChatMessage) returns (stream ChatMessage);
}
```

**Generated Interface (callback-based for bidirectional):**
```go
// ChatService is the interface plugin authors implement
type ChatService interface {
    // Chat handles bidirectional message stream.
    // incoming: channel of messages from client
    // send: function to send message to client
    // Returns when stream completes or errors
    Chat(ctx context.Context, incoming <-chan *ChatMessage, send func(*ChatMessage) error) error
}
```

**Generated Adapter:**
```go
type chatServiceHandler struct {
    impl ChatService
}

func (h *chatServiceHandler) Chat(
    ctx context.Context,
    stream *connect.BidiStream[ChatMessage, ChatMessage],
) error {
    // Create channel for incoming messages
    incoming := make(chan *ChatMessage, 10)

    // Pump stream to channel in background
    errCh := make(chan error, 1)
    go func() {
        defer close(incoming)
        errCh <- pumpBidiStreamToChannel(ctx, stream, incoming)
    }()

    // Create send function
    send := func(msg *ChatMessage) error {
        return stream.Send(msg)
    }

    // Call implementation
    implErr := h.impl.Chat(ctx, incoming, send)

    // Wait for pump goroutine
    pumpErr := <-errCh

    // Return first error
    if implErr != nil {
        return implErr
    }
    return pumpErr
}

func pumpBidiStreamToChannel[T any](
    ctx context.Context,
    stream *connect.BidiStream[T, any],
    ch chan<- *T,
) error {
    for {
        msg, err := stream.Receive()
        if err != nil {
            if errors.Is(err, io.EOF) {
                return nil
            }
            return err
        }
        select {
        case <-ctx.Done():
            return ctx.Err()
        case ch <- msg:
        }
    }
}
```

## Alternative: Bidirectional with Separate Channels

Some implementations prefer separate channels:

```go
// ChatService alternative interface
type ChatService interface {
    // Chat handles bidirectional message stream.
    // Returns a channel for outgoing messages.
    // incoming: messages from client
    // outgoing: messages to client (implementation sends, adapter receives)
    Chat(ctx context.Context, incoming <-chan *ChatMessage) (<-chan *ChatMessage, error)
}
```

**Adapter:**
```go
func (h *chatServiceHandler) Chat(
    ctx context.Context,
    stream *connect.BidiStream[ChatMessage, ChatMessage],
) error {
    incoming := make(chan *ChatMessage, 10)

    // Pump incoming
    go func() {
        defer close(incoming)
        pumpBidiStreamToChannel(ctx, stream, incoming)
    }()

    // Get outgoing channel from implementation
    outgoing, err := h.impl.Chat(ctx, incoming)
    if err != nil {
        return err
    }

    // Pump outgoing to stream
    return pumpChannelToStream(ctx, outgoing, stream)
}
```

## Backpressure Handling

### Channel Buffer Size

**Configuration:**
```go
type StreamConfig struct {
    // ChannelBufferSize for stream-to-channel adapters.
    // Larger buffers reduce blocking but increase memory.
    // Default: 10
    ChannelBufferSize int
}
```

**Generated with configurable buffer:**
```go
func (h *uploadServiceHandler) Upload(
    ctx context.Context,
    stream *connect.ClientStream[Chunk],
) (*connect.Response[UploadResult], error) {
    bufSize := h.config.ChannelBufferSize
    if bufSize == 0 {
        bufSize = 10 // Default
    }
    ch := make(chan *Chunk, bufSize)

    // ... pump and call impl
}
```

### Blocking Behavior

When channel is full, the pump goroutine blocks:

```go
func pumpStreamToChannel[T any](
    ctx context.Context,
    stream *connect.ClientStream[T],
    ch chan<- *T,
) error {
    for stream.Receive() {
        select {
        case <-ctx.Done():
            return ctx.Err()
        case ch <- stream.Msg(): // Blocks if channel full
            // This creates backpressure - stream read pauses
        }
    }
    return stream.Err()
}
```

This is **correct behavior**:
- If implementation is slow consuming from channel, pump blocks
- Blocking pump means stream reads pause
- Stream flow control kicks in, slowing sender
- Natural backpressure propagation

## Error Propagation

### Implementation Error

```go
func (s *myLogService) Tail(ctx context.Context, req *TailRequest) (<-chan *LogEntry, error) {
    if req.Filename == "" {
        return nil, connect.NewError(connect.CodeInvalidArgument,
            errors.New("filename required"))
    }

    ch := make(chan *LogEntry)
    go func() {
        defer close(ch)
        // ... send entries
    }()
    return ch, nil
}

// Adapter returns error immediately if impl returns error
```

### Error During Streaming

```go
func (s *myLogService) Tail(ctx context.Context, req *TailRequest) (<-chan *LogEntry, error) {
    ch := make(chan *LogEntry)

    go func() {
        defer close(ch)

        file, err := os.Open(req.Filename)
        if err != nil {
            // Can't send error through channel!
            // Options:
            // 1. Close channel (stream ends normally, no error)
            // 2. Use separate error channel
            // 3. Store error in service, check in separate RPC
            return
        }

        // ... read and send entries
    }()

    return ch, nil
}
```

**Solution: Error channel pattern**

```go
// Enhanced interface with error channel
type LogService interface {
    // Tail returns channels for entries and errors.
    // Close entries channel when done.
    // Send at most one error to error channel.
    Tail(ctx context.Context, req *TailRequest) (
        entries <-chan *LogEntry,
        errs <-chan error,
    )
}

// Adapter waits on both channels
func (h *logServiceHandler) Tail(
    ctx context.Context,
    req *connect.Request[TailRequest],
    stream *connect.ServerStream[LogEntry],
) error {
    entries, errs := h.impl.Tail(ctx, req.Msg)

    for {
        select {
        case <-ctx.Done():
            return ctx.Err()

        case err := <-errs:
            if err != nil {
                return err
            }

        case entry, ok := <-entries:
            if !ok {
                return nil // Stream complete
            }
            if err := stream.Send(entry); err != nil {
                return err
            }
        }
    }
}
```

## Lifecycle Management

### Goroutine Ownership

**Rule**: Adapters own pump goroutines, implementations own producer goroutines.

```go
// ADAPTER owns this goroutine
go func() {
    defer close(ch)
    pumpStreamToChannel(ctx, stream, ch)
}()

// IMPLEMENTATION owns this goroutine
func (s *myService) Tail(ctx context.Context, req *TailRequest) (<-chan *LogEntry, error) {
    ch := make(chan *LogEntry)

    go func() { // Implementation owns this
        defer close(ch)
        // ... produce entries
    }()

    return ch, nil
}
```

### Context Cancellation

All goroutines must respect context:

```go
func (s *myService) Tail(ctx context.Context, req *TailRequest) (<-chan *LogEntry, error) {
    ch := make(chan *LogEntry)

    go func() {
        defer close(ch)

        ticker := time.NewTicker(1 * time.Second)
        defer ticker.Stop()

        for {
            select {
            case <-ctx.Done():
                return // Goroutine exits
            case <-ticker.C:
                entry := &LogEntry{...}
                select {
                case ch <- entry:
                case <-ctx.Done():
                    return
                }
            }
        }
    }()

    return ch, nil
}
```

### Cleanup on Error

```go
func (h *uploadServiceHandler) Upload(
    ctx context.Context,
    stream *connect.ClientStream[Chunk],
) (*connect.Response[UploadResult], error) {
    ch := make(chan *Chunk, 10)

    // Ensure pump goroutine completes before returning
    var wg sync.WaitGroup
    wg.Add(1)

    go func() {
        defer wg.Done()
        defer close(ch)
        pumpStreamToChannel(ctx, stream, ch)
    }()

    result, err := h.impl.Upload(ctx, ch)

    wg.Wait() // Ensure goroutine cleaned up

    if err != nil {
        return nil, err
    }
    return connect.NewResponse(result), nil
}
```

## Generated Code Examples

### Server Streaming

**Proto:**
```protobuf
service WatchService {
    rpc Watch(WatchRequest) returns (stream WatchEvent);
}
```

**Generated (go-side interface):**
```go
// WatchService is the interface to implement
type WatchService interface {
    Watch(ctx context.Context, req *WatchRequest) (<-chan *WatchEvent, error)
}
```

**Generated (Connect handler):**
```go
// watchServiceHandler adapts WatchService to Connect
type watchServiceHandler struct {
    svc WatchService
}

func (h *watchServiceHandler) Watch(
    ctx context.Context,
    req *connect.Request[watchv1.WatchRequest],
    stream *connect.ServerStream[watchv1.WatchEvent],
) error {
    ch, err := h.svc.Watch(ctx, req.Msg)
    if err != nil {
        return err
    }
    return streamChannelToResponse(ctx, ch, stream)
}

func NewWatchServiceHandler(svc WatchService, opts ...connect.HandlerOption) (string, http.Handler) {
    handler := &watchServiceHandler{svc: svc}
    return watchv1connect.NewWatchServiceHandler(handler, opts...)
}
```

### Client Streaming

**Proto:**
```protobuf
service AggregateService {
    rpc Aggregate(stream Value) returns (Result);
}
```

**Generated (go-side interface):**
```go
type AggregateService interface {
    Aggregate(ctx context.Context, values <-chan *Value) (*Result, error)
}
```

**Generated (Connect handler):**
```go
type aggregateServiceHandler struct {
    svc AggregateService
}

func (h *aggregateServiceHandler) Aggregate(
    ctx context.Context,
    stream *connect.ClientStream[aggregatev1.Value],
) (*connect.Response[aggregatev1.Result], error) {
    ch := make(chan *aggregatev1.Value, 10)

    done := make(chan error, 1)
    go func() {
        defer close(ch)
        done <- receiveStreamToChannel(ctx, stream, ch)
    }()

    result, err := h.svc.Aggregate(ctx, ch)

    if receiveErr := <-done; receiveErr != nil {
        return nil, receiveErr
    }
    if err != nil {
        return nil, err
    }

    return connect.NewResponse(result), nil
}
```

## Subscription Manager Pattern

For services that manage multiple concurrent subscriptions:

```go
type SubscriptionManager[K comparable, V any] struct {
    mu          sync.RWMutex
    subscribers map[K][]chan<- V
}

func NewSubscriptionManager[K comparable, V any]() *SubscriptionManager[K, V] {
    return &SubscriptionManager[K, V]{
        subscribers: make(map[K][]chan<- V),
    }
}

func (sm *SubscriptionManager[K, V]) Subscribe(ctx context.Context, key K) <-chan V {
    ch := make(chan V, 10)

    sm.mu.Lock()
    sm.subscribers[key] = append(sm.subscribers[key], ch)
    sm.mu.Unlock()

    // Cleanup on context cancel
    go func() {
        <-ctx.Done()
        sm.unsubscribe(key, ch)
    }()

    return ch
}

func (sm *SubscriptionManager[K, V]) Publish(key K, value V) {
    sm.mu.RLock()
    subs := sm.subscribers[key]
    sm.mu.RUnlock()

    for _, ch := range subs {
        select {
        case ch <- value:
        default:
            // Subscriber slow, skip
        }
    }
}

func (sm *SubscriptionManager[K, V]) unsubscribe(key K, ch chan<- V) {
    sm.mu.Lock()
    defer sm.mu.Unlock()

    subs := sm.subscribers[key]
    for i, sub := range subs {
        if sub == ch {
            sm.subscribers[key] = append(subs[:i], subs[i+1:]...)
            close(ch)
            break
        }
    }
}
```

Usage:

```go
type pubSubService struct {
    mgr *SubscriptionManager[string, *Event]
}

func (s *pubSubService) Subscribe(ctx context.Context, req *SubscribeRequest) (<-chan *Event, error) {
    return s.mgr.Subscribe(ctx, req.Topic), nil
}

func (s *pubSubService) Publish(ctx context.Context, req *PublishRequest) error {
    s.mgr.Publish(req.Topic, req.Event)
    return nil
}
```

## Code Generation Strategy

The `protoc-gen-connect-plugin` generator produces:

1. **Go interface** with channels/callbacks (what user implements)
2. **Connect adapter** (converts between interface and Connect streaming)
3. **Handler registration** (convenience function)
4. **Client wrapper** (optional, wraps Connect client with channel-based API)

**Generator template (pseudo-code):**
```go
func generateServerStreaming(method *Method) string {
    return fmt.Sprintf(`
// %s is the interface to implement
type %s interface {
    %s(ctx context.Context, req *%s) (<-chan *%s, error)
}

// Handler adapter
type %sHandler struct {
    svc %s
}

func (h *%sHandler) %s(
    ctx context.Context,
    req *connect.Request[%s],
    stream *connect.ServerStream[%s],
) error {
    ch, err := h.svc.%s(ctx, req.Msg)
    if err != nil {
        return err
    }
    return streamChannelToResponse(ctx, ch, stream)
}
`,
        method.Service.Name,
        method.Service.Name,
        method.Name,
        method.InputType,
        method.OutputType,
        // ... rest of template
    )
}
```

## Comparison with Direct Connect API

| Pattern | With Adapters | Direct Connect |
|---------|---------------|----------------|
| **Server Streaming** | `func() (<-chan T, error)` | `func(stream *ServerStream[T]) error` |
| **Client Streaming** | `func(values <-chan T) (R, error)` | `func(stream *ClientStream[T]) (*Response[R], error)` |
| **Bidirectional** | `func(in <-chan T, send func(R)) error` | `func(stream *BidiStream[T,R]) error` |
| **Simplicity** | Higher (no stream API) | Lower (need stream API knowledge) |
| **Control** | Lower (adapter decisions) | Higher (full control) |
| **Goroutines** | Managed by adapter | Manual management |

## Implementation Checklist

- [x] Server streaming adapter (chan -> stream)
- [x] Client streaming adapter (stream -> chan)
- [x] Bidirectional streaming adapters (callback and dual-channel)
- [x] Backpressure handling strategy
- [x] Error propagation patterns (error channel)
- [x] Lifecycle management rules
- [x] Context cancellation handling
- [x] Subscription manager pattern
- [x] Generated code examples
- [x] Code generation strategy

## Next Steps

1. Implement adapter helper functions in `stream/adapters.go`
2. Update `protoc-gen-connect-plugin` to generate adapters
3. Add stream configuration options
4. Write adapter tests with cancellation scenarios
5. Document adapter patterns in getting started guide
