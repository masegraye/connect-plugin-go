# HashiCorp go-plugin Analysis

## Overview

go-plugin is HashiCorp's battle-tested plugin system used across Terraform, Vault, Nomad, Packer, Boundary, and Waypoint. It enables plugins implemented as separate processes communicating over RPC (net/rpc or gRPC).

**Critical Limitation for Our Use Case**: The library explicitly states it is "currently only designed to work over a local [reliable] network. Plugins over a real network are not supported and will lead to unexpected behavior."

## Architecture

### Core Components

```
┌─────────────────────────────────────────────────────────────────────┐
│                          HOST PROCESS                                │
│  ┌──────────────┐     ┌──────────────┐     ┌──────────────────────┐ │
│  │    Client    │────▶│ClientProtocol│────▶│  Plugin Interface    │ │
│  │  (Lifecycle) │     │ (RPC/gRPC)   │     │  Implementation      │ │
│  └──────────────┘     └──────────────┘     └──────────────────────┘ │
│         │                    │                                       │
│         │ Subprocess         │ Connection                            │
│         │ Management         │ (TCP/Unix Socket)                     │
└─────────┼────────────────────┼──────────────────────────────────────┘
          │                    │
          ▼                    ▼
┌─────────────────────────────────────────────────────────────────────┐
│                         PLUGIN PROCESS                               │
│  ┌──────────────┐     ┌──────────────┐     ┌──────────────────────┐ │
│  │    Server    │────▶│ServerProtocol│────▶│  Plugin Interface    │ │
│  │   (Serve)    │     │ (RPC/gRPC)   │     │  Implementation      │ │
│  └──────────────┘     └──────────────┘     └──────────────────────┘ │
└─────────────────────────────────────────────────────────────────────┘
```

### Key Abstractions

#### 1. Plugin Interface (`plugin.go:23-32`)

```go
type Plugin interface {
    // Server returns the RPC server struct for net/rpc
    Server(*MuxBroker) (interface{}, error)

    // Client returns the client-side interface implementation
    Client(*MuxBroker, *rpc.Client) (interface{}, error)
}
```

#### 2. GRPCPlugin Interface (`plugin.go:34-46`)

```go
type GRPCPlugin interface {
    // GRPCServer registers the plugin with grpc.Server
    GRPCServer(*GRPCBroker, *grpc.Server) error

    // GRPCClient returns the client-side interface implementation
    GRPCClient(context.Context, *GRPCBroker, *grpc.ClientConn) (interface{}, error)
}
```

#### 3. Client Configuration (`client.go:139-277`)

Key fields:
- `Cmd *exec.Cmd` - subprocess to launch
- `Reattach *ReattachConfig` - attach to existing process
- `RunnerFunc` - custom runner implementation (extensibility point)
- `Plugins PluginSet` - map of plugin name to Plugin implementation
- `VersionedPlugins` - protocol version negotiation
- `AutoMTLS bool` - automatic mutual TLS
- `TLSConfig *tls.Config` - manual TLS configuration
- `GRPCBrokerMultiplex` - multiplex brokered connections

#### 4. Handshake Protocol (`docs/internals.md`)

```
CORE-PROTOCOL-VERSION | APP-PROTOCOL-VERSION | NETWORK-TYPE | NETWORK-ADDR | PROTOCOL | CERT | MUX
Example: 1|3|unix|/path/to/socket|grpc|<base64-cert>|true
```

The handshake is transmitted over stdout from the plugin process.

### Process Lifecycle

1. **Client.Start()** (`client.go:580-948`)
   - Validates configuration (exactly one of Cmd, Reattach, or RunnerFunc)
   - Generates AutoMTLS certificates if enabled
   - Starts the subprocess via runner
   - Reads handshake from stdout
   - Validates protocol version compatibility
   - Establishes connection to plugin address

2. **Serve()** (`server.go:224-526`)
   - Validates magic cookie (prevents accidental direct execution)
   - Negotiates protocol version
   - Creates listener (Unix socket on Unix, TCP on Windows)
   - Sets up TLS if configured
   - Outputs handshake to stdout
   - Serves RPC/gRPC

3. **Dispense** (`grpc_client.go:112-124`)
   - Looks up plugin by name
   - Calls GRPCClient to create interface implementation

### Connection Multiplexing

- **net/rpc**: Uses yamux for connection multiplexing
- **gRPC**: Uses HTTP/2 native multiplexing
- **GRPCBroker**: Enables bidirectional communication by brokering additional connections

### GRPCBroker (`grpc_broker.go` - not fully shown but referenced)

Enables complex scenarios:
- Host passes interface to plugin
- Plugin calls back into host
- Multi-hop RPC chains

## Strengths

### 1. Process Isolation
- Plugin crashes don't crash host
- Memory isolation
- Security boundary (plugin only sees what's passed to it)

### 2. Language Agnostic (gRPC mode)
- Any language can implement a plugin
- Only need to implement the handshake protocol
- Generated protobuf stubs work across languages

### 3. Bidirectional Communication
- Host can pass interfaces to plugin
- Plugin can call back into host
- MuxBroker/GRPCBroker handles connection management

### 4. Reattachment
- Plugins can be "daemonized"
- Host can upgrade while plugin runs
- ReattachConfig preserves connection info

### 5. AutoMTLS
- Zero-config mutual TLS
- Certificates generated at runtime
- Only original client can connect to server

### 6. Protocol Versioning
- Negotiate compatible versions
- Graceful degradation for older plugins
- Human-friendly incompatibility messages

### 7. Integrated Logging
- Plugin stdout/stderr captured
- Structured logging with hclog
- Log levels preserved across process boundary

### 8. Checksum Verification
- SecureConfig validates binary integrity
- Prevents tampering (when properly secured)

## Weaknesses

### 1. Local Network Only
**This is the primary limitation for our use case.**
- Designed for subprocess communication
- No retry logic for transient failures
- No connection pooling for remote scenarios
- No service discovery

### 2. Subprocess Coupling
- Default mode requires launching a subprocess
- Even with RunnerFunc, assumes local process-like semantics
- Handshake protocol assumes stdout/stderr pipes

### 3. Single Connection Model
- One connection per plugin
- No load balancing
- No failover to different instances

### 4. No Health Checking Beyond Ping
- gRPC health check service exists but is minimal
- No circuit breaker patterns
- No graceful degradation

### 5. Complex Setup for Non-Go Plugins
- Need to implement handshake protocol
- Need to handle AutoMTLS certificate exchange
- Documentation for non-Go is sparse

### 6. No Streaming in net/rpc Mode
- Only gRPC supports streaming
- net/rpc is request/response only

### 7. Error Handling Assumptions
- Assumes errors are primarily due to process death
- `Killed` global variable for detecting cleanup phase
- Not designed for network partition handling

## Key Files for Reference

| File | Purpose |
|------|---------|
| `client.go` | Client lifecycle, subprocess management |
| `server.go` | Plugin serving, handshake output |
| `plugin.go` | Plugin/GRPCPlugin interfaces |
| `grpc_client.go` | gRPC-specific client implementation |
| `grpc_broker.go` | Bidirectional communication support |
| `mux_broker.go` | net/rpc connection multiplexing |

## Relevant Extension Points

### RunnerFunc
```go
RunnerFunc func(l hclog.Logger, cmd *exec.Cmd, tmpDir string) (runner.Runner, error)
```
This allows custom process/container management. Could potentially be adapted for container orchestration.

### runner.Runner Interface
```go
type Runner interface {
    Start(ctx context.Context) error
    Wait(ctx context.Context) error
    Kill(ctx context.Context) error
    ID() string
    Name() string
    Stdout() io.Reader
    Stderr() io.Reader
    PluginToHost(pluginNet, pluginAddr string) (string, string, error)
    Diagnose(ctx context.Context) string
}
```
This abstraction could be the key to supporting remote containers.

### ReattachFunc
```go
type ReattachFunc func() (AttachedRunner, error)
```
For attaching to already-running processes - relevant for sidecar scenarios.

## Conclusion

go-plugin provides excellent foundations:
- Clean interface abstraction
- Protocol versioning
- Security (TLS, checksums)
- Bidirectional communication

However, it fundamentally assumes local, reliable communication. Adapting it for remote/unreliable networks requires:
1. Replacing the subprocess model with a remote container model
2. Adding retry/resilience logic
3. Replacing the stdout handshake with a network-based discovery mechanism
4. Adding health checking and circuit breakers
