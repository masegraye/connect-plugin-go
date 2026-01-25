# connect-plugin Documentation

A remote-first plugin system combining HashiCorp go-plugin's interface design with Connect RPC's network-friendly transport.

## Documentation Structure

```
docs/
├── README.md                 # This file
├── phase-001/               # Phase 1: Research & Design
│   ├── 01-go-plugin-analysis.md
│   ├── 02-connect-rpc-analysis.md
│   └── 03-project-thesis.md
├── phase-002/               # Phase 2: Core Implementation (future)
├── phase-003/               # Phase 3: Resilience (future)
└── as-built/                # Completed implementation docs (future)
```

## Phase Documents

### Phase 001: Research & Design

| Document | Description |
|----------|-------------|
| [01-go-plugin-analysis.md](phase-001/01-go-plugin-analysis.md) | Deep dive into HashiCorp go-plugin architecture, strengths, and limitations |
| [02-connect-rpc-analysis.md](phase-001/02-connect-rpc-analysis.md) | Analysis of Connect RPC library, protocol support, and extension points |
| [03-project-thesis.md](phase-001/03-project-thesis.md) | Project vision, architecture design, and phased implementation plan |

## Quick Reference

### Project Goal

Enable plugins running as sidecar containers or remote services while maintaining the ergonomic developer experience of HashiCorp go-plugin.

### Key Insight

go-plugin is explicitly designed for local, reliable networks:

> "While the plugin system is over RPC, it is currently only designed to work over a local [reliable] network. Plugins over a real network are not supported and will lead to unexpected behavior."

Connect RPC provides a better foundation for unreliable network communication due to:
- Standard HTTP semantics
- HTTP/1.1 fallback
- Load balancer friendly
- Built-in protocol flexibility (Connect, gRPC, gRPC-Web)

### Planned Phases

1. **Phase 1: Core Foundation** - Basic client/server, handshake, dispense
2. **Phase 2: Resilience** - Retry, circuit breaker, health monitoring
3. **Phase 3: Discovery** - Kubernetes, DNS, pluggable discovery
4. **Phase 4: Advanced** - Bidirectional broker, versioning, hot reload
5. **Phase 5: Container** - Sidecar patterns, lifecycle helpers

## Source Libraries

The workspace contains the source code for both libraries being analyzed:

- `connect-go/` - Connect RPC library (connectrpc.com/connect)
- `go-plugin/` - HashiCorp go-plugin library (github.com/hashicorp/go-plugin)
