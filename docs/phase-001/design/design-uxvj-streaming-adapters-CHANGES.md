# Streaming Adapters Design - Revision Summary

## What Changed

This document summarizes the changes made to `design-uxvj-streaming-adapters.md` in response to review feedback.

## Review Feedback Addressed

### 1. Hidden Goroutines (Critical)
**Issue:** Original design had adapters creating hidden goroutines for client/bidi streaming.

**Resolution:**
- Adapters now **never create goroutines**
- Implementation creates and owns goroutines (explicit, visible)
- Adapter reads from channels in the main RPC goroutine
- Client and bidirectional streaming removed from adapter scope

### 2. Bidirectional Dual-Channel Deadlock Risk (Critical)
**Issue:** Dual-channel pattern for bidi streaming could deadlock if sends block receives.

**Resolution:**
- **Removed bidirectional adapters entirely**
- Generated interface uses `*connect.BidiStream` directly
- Documentation explains why: deadlock risk, unclear lifecycle, race conditions
- Implementations have full control over goroutine coordination

### 3. Error Channel Pattern Unnatural (Major)
**Issue:** Error channels are less familiar than returning errors directly.

**Resolution:**
- **Made error channels mandatory** - forces explicit error handling
- Added comprehensive documentation on error channel pattern
- Clear rules: buffered size 1, send at most one error
- Alternative noted in "When NOT to Use Adapters" section

### 4. Client Streaming Goroutine Races (Critical)
**Issue:** Hidden pump goroutine had coordination issues with implementation.

**Resolution:**
- **Removed client streaming adapters entirely**
- Generated interface uses `*connect.ClientStream` directly
- Documentation explains: no hidden goroutines, Stream.Receive() is idiomatic
- Simpler and safer than adapter pattern

### 5. "Too Magical" - Abstracts Important Details (Critical)
**Issue:** Adapters hid important lifecycle and control flow details.

**Resolution:**
- **Reduced scope to server streaming only**
- Added "What This Design Does NOT Include" section
- Added "When NOT to Use Adapters" section
- Lifecycle documentation expanded significantly
- Goroutine ownership explicitly documented
- Context cancellation requirements clearly stated

## Key Design Changes

### Before (Revision 1)
- Server, client, and bidirectional streaming adapters
- Configurable buffer sizes
- Multiple API styles for bidirectional (callback, dual-channel)
- Adapters created hidden goroutines
- Error handling mixed (return error vs error channel)

### After (Revision 2)
- **Server streaming ONLY**
- **Fixed buffer size (32)** - no configuration
- **Mandatory error channels** for all streaming
- **No hidden goroutines** - implementation owns lifecycle
- **Explicit documentation** of what NOT to use adapters for

## Benefits of New Design

1. **Simpler**: One pattern instead of three
2. **Explicit**: Goroutine creation visible in implementation
3. **Safer**: No hidden coordination, deadlocks, or races
4. **Focused**: Covers most common case (server streaming)
5. **Flexible**: Direct API available for complex cases

## What We Ship

**Minimal Viable Pattern:**
- Server streaming adapter with dual-channel signature
- `pumpToStream` helper function
- Generated interface comments documenting contracts
- Comprehensive lifecycle documentation

**What We Defer:**
- Client streaming adapters (use Connect API directly)
- Bidirectional adapters (use Connect API directly)
- Configurable buffer sizes (fixed at 32)
- Subscription manager helpers (separate package later)

## Migration Impact

### Users on Revision 1 (None Yet)
No migration needed - design not yet implemented.

### Future Users
Clear guidance on when to use:
- **Use adapter**: Server streaming with async producers
- **Use direct API**: Client/bidi streaming, sync streaming, validation

## Documentation Additions

New sections added:
1. **What This Design Does NOT Include** - Explicit scope limits
2. **When NOT to Use Adapters** - Five concrete scenarios
3. **Lifecycle Management** - Comprehensive goroutine ownership docs
4. **Testing Strategy** - Four critical tests for lifecycle guarantees
5. **Design Tradeoffs** - Honest assessment of gains and costs

## Implementation Guidance

The revised design includes:
- Complete generated code example
- Complete implementation example
- Four critical tests to verify lifecycle guarantees
- Clear rules for error channel usage
- Explicit context cancellation requirements
- Goroutine leak prevention patterns

## Conclusion

This revision significantly simplifies the design by:
- Focusing on the most valuable pattern (server streaming)
- Making lifecycle explicit instead of hidden
- Removing patterns with complexity/safety tradeoffs
- Providing clear guidance on when NOT to use adapters

The result is a minimal, safe, well-documented pattern that can be extended later if needed.
