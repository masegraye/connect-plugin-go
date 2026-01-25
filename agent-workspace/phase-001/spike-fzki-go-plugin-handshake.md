# Spike: go-plugin Deep Dive - Handshake & Lifecycle

**Issue:** KOR-fzki
**Status:** Complete

## Executive Summary

go-plugin uses a stdout-based handshake protocol where the plugin process outputs connection information on a single line. The host parses this line to get the network address, protocol type, and optional TLS certificate. This design is fundamentally coupled to subprocess communication and must be replaced for network-based plugins.

## Handshake Protocol

### Wire Format

The plugin outputs a single line to stdout with pipe-delimited fields:

```
CORE_VERSION|APP_VERSION|NETWORK|ADDRESS|PROTOCOL|SERVER_CERT|MUX_SUPPORTED
```

Example:
```
1|3|unix|/tmp/plugin-1234/plugin.sock|grpc|LS0tLS1CRUdJTi...|true
```

### Field Definitions

| Index | Field | Required | Description |
|-------|-------|----------|-------------|
| 0 | Core Version | Yes | Must match `CoreProtocolVersion` (currently 1) |
| 1 | App Version | Yes | Plugin's protocol version, negotiated with client's `VersionedPlugins` |
| 2 | Network | Yes | `tcp` or `unix` |
| 3 | Address | Yes | Network address (e.g., `127.0.0.1:12345` or `/path/to/socket`) |
| 4 | Protocol | No | `netrpc` or `grpc` (defaults to `netrpc`) |
| 5 | Server Cert | No | Base64-encoded DER x.509 certificate (for AutoMTLS) |
| 6 | Mux Supported | No | `true`/`false` for gRPC broker multiplexing support |

### Parsing Logic (client.go:829-944)

```go
line = strings.TrimSpace(line)
parts := strings.Split(line, "|")
if len(parts) < 4 {
    // Error: unrecognized format
}

// parts[0]: Core protocol version - must match CoreProtocolVersion
// parts[1]: App protocol version - negotiate with VersionedPlugins
// parts[2]: Network type
// parts[3]: Address
// parts[4]: Protocol (optional, defaults to netrpc)
// parts[5]: Server cert (optional)
// parts[6]: Mux support (optional)
```

## AutoMTLS Certificate Exchange

### Flow

```
┌─────────────┐                              ┌─────────────┐
│    Host     │                              │   Plugin    │
└──────┬──────┘                              └──────┬──────┘
       │                                            │
       │  1. Generate ECDSA P-521 key pair         │
       │  2. Create self-signed x509 cert          │
       │                                            │
       │  ─────── PLUGIN_CLIENT_CERT=<PEM> ──────▶ │
       │          (via environment variable)        │
       │                                            │
       │                              3. Parse client cert
       │                              4. Generate server key pair
       │                              5. Create server cert
       │                                            │
       │  ◀─────── Server cert in handshake ────── │
       │           (Base64 DER in field 5)         │
       │                                            │
       │  6. Add server cert to RootCAs/ClientCAs  │
       │                                            │
       │  ════════ mTLS Connection ═══════════════ │
       │                                            │
```

### Certificate Properties (mtls.go)

- **Algorithm:** ECDSA with P-521 curve
- **Validity:** ~30 years (262980 hours)
- **Key Usage:** Digital Signature, Key Encipherment, Key Agreement, Cert Sign
- **Extended Key Usage:** Client Auth, Server Auth
- **Is CA:** true (self-signed)
- **Common Name:** localhost

### Security Properties

1. **One-time use:** New cert generated per plugin launch
2. **Mutual authentication:** Both client and server verify each other
3. **No external CA:** Self-signed, no PKI infrastructure needed
4. **Transport only:** No application-level auth

## Environment Variables

The host sets these environment variables before starting the plugin:

| Variable | Purpose |
|----------|---------|
| `{MagicCookieKey}={MagicCookieValue}` | Validates this is a real plugin (UX, not security) |
| `PLUGIN_MIN_PORT` | Minimum TCP port for listener |
| `PLUGIN_MAX_PORT` | Maximum TCP port for listener |
| `PLUGIN_PROTOCOL_VERSIONS` | Comma-separated list of supported versions |
| `PLUGIN_CLIENT_CERT` | PEM-encoded client certificate (if AutoMTLS) |
| `PLUGIN_MULTIPLEX_GRPC` | Whether host wants broker multiplexing |
| `PLUGIN_UNIX_SOCKET_DIR` | Directory for unix sockets (if using RunnerFunc) |
| `PLUGIN_UNIX_SOCKET_GROUP` | Group ownership for unix sockets |

## Lifecycle State Machine

```
                    ┌─────────────────────────────────────────────────────────┐
                    │                                                         │
                    ▼                                                         │
┌──────────┐   ┌─────────┐   ┌───────────┐   ┌───────────┐   ┌──────────┐   │
│  Created │──▶│ Starting│──▶│ Connected │──▶│  Running  │──▶│  Exited  │───┘
└──────────┘   └─────────┘   └───────────┘   └───────────┘   └──────────┘
     │              │              │               │               ▲
     │              │              │               │               │
     │              ▼              ▼               ▼               │
     │         [Timeout]      [Error]         [Kill()]            │
     │              │              │               │               │
     │              └──────────────┴───────────────┴───────────────┘
     │
     └─────────────▶ [Reattach] ──▶ Connected ──▶ Running ──▶ Exited
```

### State Descriptions

1. **Created:** `NewClient()` called, no subprocess yet
2. **Starting:** `Start()` called, subprocess launched, waiting for handshake
3. **Connected:** Handshake received, address known, but no RPC client yet
4. **Running:** `Client()` called, RPC client created, ready for use
5. **Exited:** Process terminated (graceful or forced)

## Start() Sequence (client.go:580-948)

```go
func (c *Client) Start() (addr net.Addr, err error) {
    // 1. Validate config
    // - Exactly one of: Cmd, Reattach, RunnerFunc
    // - SecureConfig cannot be used with Reattach

    // 2. Handle Reattach (skip to step 8 if reattaching)
    if c.config.Reattach != nil {
        return c.reattach()
    }

    // 3. Build environment variables
    env := []string{
        MagicCookieKey=MagicCookieValue,
        PLUGIN_MIN_PORT, PLUGIN_MAX_PORT,
        PLUGIN_PROTOCOL_VERSIONS,
    }

    // 4. Verify checksum (if SecureConfig)
    if c.config.SecureConfig != nil {
        ok, _ := c.config.SecureConfig.Check(cmd.Path)
    }

    // 5. Generate AutoMTLS certs (if enabled)
    if c.config.AutoMTLS {
        certPEM, keyPEM, _ := generateCert()
        cmd.Env = append(cmd.Env, "PLUGIN_CLIENT_CERT="+certPEM)
        c.config.TLSConfig = &tls.Config{...}
    }

    // 6. Start subprocess via runner
    runner.Start(ctx)

    // 7. Start goroutines for stderr logging and stdout reading
    go c.logStderr(runner.Stderr())
    go readStdoutLines(runner.Stdout()) -> linesCh

    // 8. Wait for handshake line (with timeout)
    select {
    case <-timeout:
        // Error: timeout
    case <-c.doneCtx.Done():
        // Error: plugin exited
    case line := <-linesCh:
        // Parse handshake
        parts := strings.Split(line, "|")

        // 9. Validate core protocol version
        // 10. Negotiate app protocol version
        // 11. Get network address
        // 12. Get protocol type
        // 13. Load server cert (if present)
        // 14. Check mux support (if requested)
    }

    return addr, nil
}
```

## Kill() Sequence (client.go:498-572)

```go
func (c *Client) Kill() {
    // 1. Try graceful shutdown via RPC
    client, err := c.Client()
    if err == nil {
        err = client.Close()  // Sends shutdown RPC
        graceful = (err == nil)
    }

    // 2. Wait for graceful exit (2 second timeout)
    if graceful {
        select {
        case <-c.doneCtx.Done():
            return  // Clean exit
        case <-time.After(2 * time.Second):
            // Timeout, proceed to force kill
        }
    }

    // 3. Force kill
    runner.Kill(ctx)

    // 4. Cleanup
    // - Wait for goroutines
    // - Remove socket directory
}
```

## Reattach Mechanism

Reattach allows connecting to an already-running plugin without launching it.

### ReattachConfig

```go
type ReattachConfig struct {
    Protocol        Protocol    // netrpc or grpc
    ProtocolVersion int         // Negotiated version
    Addr            net.Addr    // Network address
    Pid             int         // Process ID (for default ReattachFunc)
    ReattachFunc    ReattachFunc // Custom attach function
    Test            bool        // Test mode flag
}
```

### Reattach Flow

1. Skip subprocess launch
2. Use stored address directly
3. Set up process wait goroutine
4. Create RPC client

### Use Cases

- Plugin survives host restart
- Plugin daemonization
- Test mode (in-process plugins)

## Key Design Decisions for connect-plugin

### What We Must Replace

| go-plugin | connect-plugin |
|-----------|----------------|
| Stdout handshake | HTTP handshake endpoint |
| Environment variables | Request headers or handshake payload |
| Process ID | Service instance ID |
| Unix socket/TCP | HTTP/HTTPS URL |
| Subprocess kill | Graceful HTTP shutdown + timeout |

### What We Can Reuse Conceptually

1. **Protocol versioning:** Negotiate compatible versions in handshake
2. **Capability advertisement:** Handshake includes available plugins
3. **Graceful shutdown pattern:** Try clean shutdown, then timeout
4. **AutoMTLS concept:** Could adapt for initial connection (but not via env var)

### Network Handshake Design

Proposed replacement for stdout handshake:

```protobuf
// connectplugin.core.v1/handshake.proto

message HandshakeRequest {
    int32 core_protocol_version = 1;
    repeated int32 supported_app_versions = 2;
    repeated string requested_plugins = 3;
    map<string, string> client_capabilities = 4;
}

message HandshakeResponse {
    int32 core_protocol_version = 1;
    int32 negotiated_app_version = 2;
    repeated PluginInfo available_plugins = 3;
    map<string, string> server_capabilities = 4;
}

message PluginInfo {
    string name = 1;
    string version = 2;
    repeated string capabilities = 3;
}
```

### Security Considerations

For network plugins, AutoMTLS via environment variables won't work. Options:

1. **Pre-shared mTLS certs:** Configure TLS out of band
2. **Token exchange in handshake:** Include auth token in HandshakeRequest
3. **OIDC/JWT:** Standard token-based auth
4. **Service mesh:** Delegate to Istio/Linkerd

## Failure Modes

### go-plugin Failure Modes

| Failure | Detection | Handling |
|---------|-----------|----------|
| Plugin won't start | Timeout waiting for handshake | Return error |
| Plugin crashes during startup | EOF on stdout | Return error |
| Plugin crashes during operation | Context cancelled, EOF | Return error on next RPC |
| Version mismatch | Handshake parsing | Return error |
| Network unreachable | Connection error | Return error |

### Additional Failure Modes for connect-plugin

| Failure | Detection | Handling |
|---------|-----------|----------|
| Network partition | Connection timeout | Retry with backoff |
| Plugin temporarily unavailable | 503 status | Retry with backoff |
| Load balancer failover | Request fails | Retry to different instance |
| Plugin restart | Connection reset | Re-handshake |

## Conclusions

1. **Stdout handshake must be replaced** with an HTTP-based handshake endpoint
2. **Environment variables** must be replaced with handshake request/response fields
3. **Process lifecycle** must be replaced with health checking + circuit breaker
4. **AutoMTLS pattern** is elegant but needs network-friendly adaptation
5. **Version negotiation** pattern should be preserved
6. **Graceful shutdown** pattern (try clean, then timeout) should be preserved

## Next Steps

1. Design the `connectplugin.core.v1` handshake proto
2. Decide on network-friendly auth mechanism
3. Design health check integration with lifecycle
4. Prototype basic handshake flow
