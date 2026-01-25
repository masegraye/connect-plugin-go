# Design: Streaming Adapter Patterns (chan <-> stream)

**Issue:** KOR-uxvj
**Status:** In Review (Revision 2 - Simplified)
**Dependencies:** KOR-yosi, KOR-neyu

## Revision History

**Revision 1 (Original):** Full adapter suite for all streaming patterns
- Server, client, and bidirectional streaming adapters
- Configurable buffer sizes
- Multiple API styles for bidirectional (callback and dual-channel)

**Revision 2 (This version):** Simplified to minimal viable pattern
- **Server streaming only** - client/bidi use Connect API directly
- **Fixed buffer size** (32) - no configuration
- **Mandatory error channels** - explicit error handling
- **No hidden goroutines** - adapter reads in main RPC goroutine
- Addresses review feedback: complexity, lifecycle, deadlock risks

## Overview

Streaming adapters provide a **minimal, explicit** pattern for server-side streaming using channels. This design intentionally limits scope to the simplest, most valuable pattern while keeping lifecycle and error handling completely explicit.

## Design Principles

1. **Server streaming only**: Client and bidirectional streaming use Connect APIs directly
2. **Explicit lifecycle**: Goroutines and cleanup are visible, not hidden
3. **Mandatory error handling**: All streaming requires error channels
4. **Simple defaults**: Fixed buffer sizes, no configuration
5. **Ship minimal value**: Defer complexity until proven necessary

## What This Design Does NOT Include

These patterns are explicitly out of scope. Use Connect's streaming APIs directly for:

- **Client streaming**: Implementations receive `*connect.ClientStream[T]` directly
- **Bidirectional streaming**: Implementations receive `*connect.BidiStream[T, R]` directly
- **Complex lifecycle**: If you need custom buffering, flow control, or coordination
- **Fire-and-forget**: If you don't want to wait for completion

**Why?** These patterns introduce hidden goroutines, complex error coordination, and potential deadlocks. The marginal convenience is not worth the complexity cost.

## Server Streaming Pattern

This is the **only** adapter pattern we provide. It covers the common case of server-side streaming while keeping complexity minimal.

### The Pattern

**Proto:**
```protobuf
service LogService {
    rpc Tail(TailRequest) returns (stream LogEntry);
}
```

**Generated Interface:**
```go
// LogService is the interface plugin authors implement
type LogService interface {
    // Tail returns channels for log entries and errors.
    //
    // The implementation MUST:
    // 1. Send entries on the entries channel
    // 2. Send at most ONE error on the error channel (if an error occurs)
    // 3. Close the entries channel when done (success or error)
    // 4. Respect ctx cancellation and clean up goroutines
    //
    // The adapter will:
    // 1. Wait for entries, errors, or context cancellation
    // 2. Return the first error received (from err channel or ctx)
    // 3. Return nil when entries channel closes with no error
    Tail(ctx context.Context, req *TailRequest) (entries <-chan *LogEntry, err <-chan error)
}
```

**Generated Adapter:**
```go
type logServiceHandler struct {
    impl LogService
}

func (h *logServiceHandler) Tail(
    ctx context.Context,
    req *connect.Request[TailRequest],
    stream *connect.ServerStream[LogEntry],
) error {
    // Call implementation
    entries, errs := h.impl.Tail(ctx, req.Msg)

    // Pump channels to stream
    return pumpToStream(ctx, entries, errs, stream)
}

// pumpToStream sends channel values to stream until channel closes or error occurs
func pumpToStream[T any](
    ctx context.Context,
    ch <-chan T,
    errs <-chan error,
    stream *connect.ServerStream[T],
) error {
    for {
        select {
        case <-ctx.Done():
            // Context cancelled - clean shutdown
            return ctx.Err()

        case err := <-errs:
            // Error from implementation - stop immediately
            // Implementation MUST close ch after sending error
            return err

        case msg, ok := <-ch:
            if !ok {
                // Channel closed - normal completion
                return nil
            }
            if err := stream.Send(&msg); err != nil {
                // Stream send failed - propagate error
                return err
            }
        }
    }
}
```

## Client Streaming - Use Connect API Directly

**Proto:**
```protobuf
service UploadService {
    rpc Upload(stream Chunk) returns (UploadResult);
}
```

**Generated Interface (Direct Connect API):**
```go
// UploadService is the interface plugin authors implement
type UploadService interface {
    // Upload receives chunks from the client stream.
    // Use stream.Receive() to read chunks.
    // Return the result and any error.
    Upload(ctx context.Context, stream *connect.ClientStream[Chunk]) (*UploadResult, error)
}
```

**Implementation Example:**
```go
func (s *uploadService) Upload(
    ctx context.Context,
    stream *connect.ClientStream[Chunk],
) (*UploadResult, error) {
    var totalBytes int64

    // Read from stream directly - no hidden goroutines
    for stream.Receive() {
        chunk := stream.Msg()
        totalBytes += int64(len(chunk.Data))

        // Check context explicitly
        if ctx.Err() != nil {
            return nil, ctx.Err()
        }
    }

    // Check for stream errors
    if err := stream.Err(); err != nil {
        return nil, err
    }

    return &UploadResult{TotalBytes: totalBytes}, nil
}
```

**Why no adapter?**
- No hidden goroutines to manage
- No complex error coordination needed
- Stream.Receive() is already idiomatic Go
- Implementation has full control over buffering and flow

## Bidirectional Streaming - Use Connect API Directly

**Proto:**
```protobuf
service ChatService {
    rpc Chat(stream ChatMessage) returns (stream ChatMessage);
}
```

**Generated Interface (Direct Connect API):**
```go
// ChatService is the interface plugin authors implement
type ChatService interface {
    // Chat handles bidirectional message streaming.
    // Use stream.Receive() to read incoming messages.
    // Use stream.Send() to write outgoing messages.
    // Return when complete or on error.
    Chat(ctx context.Context, stream *connect.BidiStream[ChatMessage, ChatMessage]) error
}
```

**Implementation Example:**
```go
func (s *chatService) Chat(
    ctx context.Context,
    stream *connect.BidiStream[ChatMessage, ChatMessage],
) error {
    // Create goroutine for receiving - YOU control this
    recvErr := make(chan error, 1)
    go func() {
        for stream.Receive() {
            msg := stream.Msg()

            // Process and respond
            response := &ChatMessage{
                Text: "Echo: " + msg.Text,
            }

            if err := stream.Send(response); err != nil {
                recvErr <- err
                return
            }
        }
        recvErr <- stream.Err()
    }()

    // Wait for completion or cancellation
    select {
    case <-ctx.Done():
        return ctx.Err()
    case err := <-recvErr:
        return err
    }
}
```

**Why no adapter?**
- **Deadlock risk**: Dual-channel pattern can deadlock if send blocks receive
- **Unclear lifecycle**: Which goroutine owns what? When do they finish?
- **Race conditions**: Coordinating two goroutines adds complexity
- **Loss of control**: Implementation can't control send/receive coordination

With direct API access, YOU decide the goroutine structure and coordination strategy.

## Implementation Details

### Error Channel Pattern

The error channel must be buffered to prevent goroutine leaks:

```go
func (s *logService) Tail(
    ctx context.Context,
    req *TailRequest,
) (<-chan *LogEntry, <-chan error) {
    entries := make(chan *LogEntry, 32) // Buffered to prevent blocking
    errs := make(chan error, 1)         // MUST be buffered size 1

    go func() {
        defer close(entries) // ALWAYS close entries channel

        file, err := os.Open(req.Filename)
        if err != nil {
            errs <- connect.NewError(connect.CodeNotFound, err)
            return // Exit after sending error
        }
        defer file.Close()

        scanner := bufio.NewScanner(file)
        for scanner.Scan() {
            entry := &LogEntry{Line: scanner.Text()}

            select {
            case <-ctx.Done():
                // Don't send error on cancellation - adapter handles it
                return
            case entries <- entry:
                // Sent successfully
            }
        }

        if err := scanner.Err(); err != nil {
            errs <- err
        }
    }()

    return entries, errs
}
```

**Critical Rules:**
1. Error channel MUST be buffered (size 1)
2. Send at most ONE error
3. ALWAYS close entries channel (success or error)
4. Don't send errors on context cancellation (adapter detects this)

### Channel Buffer Size

**Fixed at 32** - no configuration needed.

```go
entries := make(chan *LogEntry, 32)
```

**Why 32?**
- Large enough to prevent most blocking
- Small enough to limit memory per stream
- Good balance for typical RPC patterns
- One less thing to configure

**Why not configurable?**
- Configuration adds complexity
- Most users don't need it
- Premature optimization
- Can be added later if proven necessary

### Backpressure

Backpressure happens naturally when the entries channel fills up:

```go
select {
case <-ctx.Done():
    return
case entries <- entry: // Blocks if channel full (32 entries buffered)
    // Implementation pauses until adapter reads from channel
    // Adapter reads from channel and sends to stream
    // If client is slow, stream Send() blocks
    // Natural backpressure chain: client -> stream -> channel -> implementation
}
```

This is correct and safe. No special handling needed.

## Lifecycle Management

### Goroutine Ownership

**Implementation owns the goroutine**. It's created in the implementation and cleaned up when context is cancelled or work completes.

```go
func (s *logService) Tail(
    ctx context.Context,
    req *TailRequest,
) (<-chan *LogEntry, <-chan error) {
    entries := make(chan *LogEntry, 32)
    errs := make(chan error, 1)

    // Implementation creates and owns this goroutine
    go func() {
        defer close(entries) // MUST close channel when done

        // ... do work, respecting ctx.Done()
    }()

    return entries, errs
}
```

**Adapter does NOT create goroutines**. It simply reads from channels in the main RPC goroutine.

```go
func (h *logServiceHandler) Tail(
    ctx context.Context,
    req *connect.Request[TailRequest],
    stream *connect.ServerStream[LogEntry],
) error {
    // NO goroutines created here
    entries, errs := h.impl.Tail(ctx, req.Msg)

    // Read from channels in THIS goroutine
    return pumpToStream(ctx, entries, errs, stream)
}
```

### Context Cancellation

Implementation MUST check context in two places:

```go
func (s *logService) Tail(
    ctx context.Context,
    req *TailRequest,
) (<-chan *LogEntry, <-chan error) {
    entries := make(chan *LogEntry, 32)
    errs := make(chan error, 1)

    go func() {
        defer close(entries)

        ticker := time.NewTicker(1 * time.Second)
        defer ticker.Stop()

        for {
            select {
            case <-ctx.Done():
                // CHECK 1: Between iterations
                return
            case <-ticker.C:
                entry := &LogEntry{Timestamp: time.Now()}

                select {
                case entries <- entry:
                case <-ctx.Done():
                    // CHECK 2: During send (prevents blocking on full channel)
                    return
                }
            }
        }
    }()

    return entries, errs
}
```

**Why two checks?**
1. First check: Exit quickly between iterations
2. Second check: Prevent blocking forever if channel is full and adapter stops reading

### Cleanup Guarantees

**Implementation contract:**
- MUST close entries channel when goroutine exits
- MUST respect context cancellation
- MUST NOT leak goroutines

**Adapter contract:**
- Reads from channels until entries closes or error received
- Propagates context cancellation
- Returns when stream completes (no lingering goroutines)

### Preventing Goroutine Leaks

```go
// BAD: Goroutine might leak if context cancelled
go func() {
    for {
        entry := produce()
        entries <- entry // Blocks forever if adapter stops reading
    }
}()

// GOOD: Always check context during send
go func() {
    defer close(entries)
    for {
        entry := produce()
        select {
        case entries <- entry:
        case <-ctx.Done():
            return // Clean exit
        }
    }
}()
```

## Complete Generated Code Example

**Proto:**
```protobuf
service LogService {
    rpc Tail(TailRequest) returns (stream LogEntry);
}
```

**Generated Interface (what you implement):**
```go
package logv1

// LogService is the interface plugin authors implement.
type LogService interface {
    // Tail streams log entries for the requested file.
    //
    // Return:
    //   - entries: Channel for streaming log entries to client
    //   - errs: Channel for reporting errors (buffered, size 1)
    //
    // Contract:
    //   - MUST close entries channel when done (success or error)
    //   - MAY send at most one error to errs channel
    //   - MUST respect ctx.Done() and clean up goroutines
    //   - SHOULD buffer entries channel (recommended: 32)
    Tail(ctx context.Context, req *TailRequest) (entries <-chan *LogEntry, errs <-chan error)
}
```

**Generated Adapter (Connect handler):**
```go
package logv1

import (
    "context"
    "connectrpc.com/connect"
    logv1 "example.com/proto/log/v1"
)

// logServiceHandler adapts LogService to Connect streaming API
type logServiceHandler struct {
    impl LogService
}

func (h *logServiceHandler) Tail(
    ctx context.Context,
    req *connect.Request[logv1.TailRequest],
    stream *connect.ServerStream[logv1.LogEntry],
) error {
    entries, errs := h.impl.Tail(ctx, req.Msg)
    return pumpToStream(ctx, entries, errs, stream)
}

// pumpToStream is a generated helper function
func pumpToStream[T any](
    ctx context.Context,
    ch <-chan T,
    errs <-chan error,
    stream *connect.ServerStream[T],
) error {
    for {
        select {
        case <-ctx.Done():
            return ctx.Err()
        case err := <-errs:
            return err
        case msg, ok := <-ch:
            if !ok {
                return nil
            }
            if err := stream.Send(&msg); err != nil {
                return err
            }
        }
    }
}

// NewLogServiceHandler creates a Connect handler for LogService
func NewLogServiceHandler(svc LogService, opts ...connect.HandlerOption) (string, http.Handler) {
    handler := &logServiceHandler{impl: svc}
    return logv1connect.NewLogServiceHandler(handler, opts...)
}
```

## Example: Complete Implementation

```go
package myapp

import (
    "bufio"
    "context"
    "os"

    "connectrpc.com/connect"
    logv1 "example.com/proto/log/v1"
)

type logService struct {
    logsDir string
}

func NewLogService(logsDir string) logv1.LogService {
    return &logService{logsDir: logsDir}
}

func (s *logService) Tail(
    ctx context.Context,
    req *logv1.TailRequest,
) (<-chan *logv1.LogEntry, <-chan error) {
    entries := make(chan *logv1.LogEntry, 32)
    errs := make(chan error, 1)

    go func() {
        defer close(entries) // ALWAYS close

        // Validate request
        if req.Filename == "" {
            errs <- connect.NewError(connect.CodeInvalidArgument,
                errors.New("filename required"))
            return
        }

        // Open file
        path := filepath.Join(s.logsDir, req.Filename)
        file, err := os.Open(path)
        if err != nil {
            errs <- connect.NewError(connect.CodeNotFound, err)
            return
        }
        defer file.Close()

        // Stream lines
        scanner := bufio.NewScanner(file)
        lineNum := 0
        for scanner.Scan() {
            lineNum++
            entry := &logv1.LogEntry{
                LineNumber: int64(lineNum),
                Text:       scanner.Text(),
            }

            select {
            case entries <- entry:
                // Sent successfully
            case <-ctx.Done():
                // Client disconnected, exit cleanly
                return
            }
        }

        // Check for scanner errors
        if err := scanner.Err(); err != nil {
            errs <- connect.NewError(connect.CodeInternal, err)
        }
    }()

    return entries, errs
}
```

## When NOT to Use Adapters

Use Connect APIs directly instead of adapters when:

1. **Request validation**: Return early errors before starting goroutines
   ```go
   // Use direct API
   func (s *svc) Tail(ctx context.Context, stream *connect.ServerStream[Entry]) error {
       if req.Filename == "" {
           return connect.NewError(connect.CodeInvalidArgument, ...)
       }
       // Now stream
   }
   ```

2. **Synchronous streaming**: Reading from an iterator or database cursor
   ```go
   // Use direct API - no goroutines needed
   func (s *svc) Query(ctx context.Context, stream *connect.ServerStream[Row]) error {
       rows, err := s.db.Query(...)
       defer rows.Close()
       for rows.Next() {
           stream.Send(...)
       }
   }
   ```

3. **Client/Bidi streaming**: Always use direct API (no adapter provided)

4. **Custom flow control**: Need precise control over buffering or backpressure

5. **Complex coordination**: Multiple streams, conditional sending, etc.

## Design Tradeoffs

**What we gain:**
- Simple, familiar channel-based code for server streaming
- Clear error handling with error channels
- No hidden goroutine lifecycle complexity

**What we give up:**
- Adapters only for server streaming (not client or bidi)
- Must manage goroutines in implementation
- Fixed buffer size (32) - not configurable
- Error channel pattern is less familiar than returning errors directly

**Why this is the right tradeoff:**
- Server streaming is the most common pattern (pub/sub, logs, events)
- Client/bidi streaming have too many edge cases for a safe abstraction
- Explicit goroutines prevent "magic" lifecycle issues
- Fixed buffer size eliminates one configuration dimension
- Better to ship something minimal and proven than overdesign

## Implementation Checklist

Phase 1 (MVP):
- [ ] Implement `pumpToStream` helper function
- [ ] Generate server streaming interface with dual-channel signature
- [ ] Generate adapter for server streaming RPCs
- [ ] Document error channel pattern in generated comments
- [ ] Test: context cancellation cleans up goroutines
- [ ] Test: error propagation through error channel
- [ ] Test: backpressure with slow client

Phase 2 (if needed):
- [ ] Client streaming (direct Connect API in generated interface)
- [ ] Bidirectional streaming (direct Connect API in generated interface)
- [ ] Performance testing with large messages
- [ ] Documentation: when NOT to use adapters

## Next Steps

1. Implement `pumpToStream` in runtime package
2. Update `protoc-gen-connect-plugin` template for server streaming
3. Add generated code comments explaining lifecycle contract
4. Write comprehensive tests for goroutine cleanup
5. Update getting started guide with complete example

## Testing Strategy

These tests must pass to ensure lifecycle guarantees:

### Test 1: Goroutine Cleanup on Success
```go
func TestServerStreaming_GoroutineCleanup_Success(t *testing.T) {
    initial := runtime.NumGoroutine()

    svc := &logService{}
    // Call RPC, stream completes normally
    // ...

    // Wait for cleanup
    time.Sleep(100 * time.Millisecond)

    final := runtime.NumGoroutine()
    if final > initial+1 { // +1 for test goroutine tolerance
        t.Errorf("goroutine leak: initial=%d final=%d", initial, final)
    }
}
```

### Test 2: Goroutine Cleanup on Context Cancel
```go
func TestServerStreaming_GoroutineCleanup_Cancelled(t *testing.T) {
    ctx, cancel := context.WithCancel(context.Background())

    // Start streaming
    entries, _ := svc.Tail(ctx, req)

    // Cancel after first message
    <-entries
    cancel()

    // Verify channel closes
    _, ok := <-entries
    if ok {
        t.Error("entries channel not closed after cancel")
    }

    // Verify no goroutine leak
    // ...
}
```

### Test 3: Error Propagation
```go
func TestServerStreaming_ErrorPropagation(t *testing.T) {
    entries, errs := svc.Tail(ctx, &TailRequest{Filename: "nonexistent"})

    // Should receive error
    select {
    case err := <-errs:
        if err == nil {
            t.Error("expected error, got nil")
        }
    case <-time.After(1 * time.Second):
        t.Error("timeout waiting for error")
    }

    // Entries channel should close
    _, ok := <-entries
    if ok {
        t.Error("entries channel not closed after error")
    }
}
```

### Test 4: Backpressure
```go
func TestServerStreaming_Backpressure(t *testing.T) {
    entries, _ := svc.Tail(ctx, req)

    // Don't read from channel - let it fill up
    time.Sleep(1 * time.Second)

    // Implementation should block, not crash or drop messages
    // Verify buffering behavior
    // ...
}
```

## Open Questions

1. **Should we generate both adapter and direct API versions?**
   - Pro: Users can choose based on needs
   - Con: More generated code, more complexity
   - Decision: Start adapter-only, add escape hatch if requested

2. **Should error channel be required or optional?**
   - Pro (required): Forces explicit error handling
   - Con (required): More boilerplate for simple cases
   - Decision: Required - better to be explicit

3. **Should we provide helper for subscription management?**
   - Pro: Common pattern, saves user code
   - Con: Outside scope of adapter design
   - Decision: Defer to Phase 2 or separate package
