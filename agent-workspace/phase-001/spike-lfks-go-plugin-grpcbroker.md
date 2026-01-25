# Spike: go-plugin Deep Dive - GRPCBroker Bidirectional

**Issue:** KOR-lfks
**Status:** Complete

## Executive Summary

GRPCBroker enables bidirectional communication in go-plugin by allowing both host and plugin to dynamically establish new gRPC connections at runtime. Each side can act as both client and server for different services. The broker uses a control stream to exchange connection information (addresses or "knocks" for multiplexed connections) identified by unique IDs.

This pattern is essential for:
- Passing complex arguments that include callbacks (host services as plugin arguments)
- Plugin-initiated requests to host capabilities
- Event subscription/callback patterns

## Architecture Overview

```
┌─────────────────────────────────────────────────────────────────────────┐
│                              Host Process                                │
│                                                                          │
│  ┌──────────────────┐     ┌─────────────────┐     ┌─────────────────┐  │
│  │   Application    │────▶│   GRPCClient    │     │   AddHelper     │  │
│  │                  │     │                 │     │   (Server)      │  │
│  └──────────────────┘     │  broker ───────────┐  └────────▲────────┘  │
│                           │  conn             │            │            │
│                           └─────────┬─────────┘            │            │
│                                     │                      │            │
└─────────────────────────────────────┼──────────────────────┼────────────┘
                                      │                      │
                    Main gRPC Connection            Brokered Connection
                    (plugin services)               (id: 42)
                                      │                      │
┌─────────────────────────────────────┼──────────────────────┼────────────┐
│                                     │                      │            │
│  ┌─────────────────┐     ┌──────────▼────────┐     ┌───────▼────────┐  │
│  │   Counter       │◀────│   GRPCServer      │     │   AddHelper    │  │
│  │   (Impl)        │     │                   │     │   (Client)     │  │
│  └─────────────────┘     │  broker ─────────────┐  └────────────────┘  │
│                          │                   │  │                      │
│                          └───────────────────┘  │                      │
│                                                 │                      │
│                             Plugin Process      │                      │
└─────────────────────────────────────────────────┴──────────────────────┘
```

## Core Components

### 1. GRPCBroker (grpc_broker.go:262-280)

The main broker struct manages bidirectional connection establishment:

```go
type GRPCBroker struct {
    nextId   uint32           // Auto-incrementing connection ID
    streamer streamer         // Control stream for connection info
    tls      *tls.Config      // TLS config for brokered connections
    doneCh   chan struct{}    // Shutdown signal

    clientStreams map[uint32]*gRPCBrokerPending  // Waiting for conn info
    serverStreams map[uint32]*gRPCBrokerPending  // Waiting for knocks

    unixSocketCfg  UnixSocketConfig    // Socket configuration
    addrTranslator runner.AddrTranslator // Address translation
    muxer          grpcmux.GRPCMuxer   // Connection multiplexing
}
```

### 2. Control Stream Protocol (grpc_broker.proto)

```protobuf
message ConnInfo {
    uint32 service_id = 1;    // Unique ID for this connection
    string network = 2;       // "tcp" or "unix"
    string address = 3;       // Network address
    message Knock {
        bool knock = 1;       // Is this a knock request?
        bool ack = 2;         // Is this a knock acknowledgement?
        string error = 3;     // Error message if any
    }
    Knock knock = 4;          // Knock data for multiplexed mode
}

service GRPCBroker {
    rpc StartStream(stream ConnInfo) returns (stream ConnInfo);
}
```

### 3. Server-Side Stream Handler (gRPCBrokerServer)

The server maintains a bidirectional stream:

```go
func (s *gRPCBrokerServer) StartStream(stream plugin.GRPCBroker_StartStreamServer) error {
    // Process send stream (outgoing ConnInfo)
    go func() {
        for {
            select {
            case se := <-s.send:
                err := stream.Send(se.i)
                se.ch <- err
            }
        }
    }()

    // Process receive stream (incoming ConnInfo)
    for {
        i, err := stream.Recv()
        if err != nil {
            return err
        }
        s.recv <- i
    }
}
```

### 4. Client-Side Stream Handler (gRPCBrokerClientImpl)

Mirror implementation for the client side with the same Send/Recv pattern.

## Connection Establishment Patterns

### Pattern 1: Accept/Dial (Non-Multiplexed)

Used when creating separate TCP/Unix socket connections for each brokered service.

**Acceptor Side (starts listener):**
```go
func (b *GRPCBroker) Accept(id uint32) (net.Listener, error) {
    // 1. Create a new listener
    listener, err := serverListener(b.unixSocketCfg)

    // 2. Translate address if needed (e.g., for containers)
    advertiseNet, advertiseAddr, _ := b.addrTranslator.HostToPlugin(...)

    // 3. Send connection info to the other side
    err = b.streamer.Send(&plugin.ConnInfo{
        ServiceId: id,
        Network:   advertiseNet,
        Address:   advertiseAddr,
    })

    return listener, nil
}
```

**Dialer Side (connects to listener):**
```go
func (b *GRPCBroker) Dial(id uint32) (*grpc.ClientConn, error) {
    // 1. Wait for connection info from the acceptor
    p := b.getClientStream(id)
    select {
    case c = <-p.ch:
        close(p.doneCh)
    case <-time.After(5 * time.Second):
        return nil, fmt.Errorf("timeout waiting for connection info")
    }

    // 2. Translate address if needed
    network, address, _ := b.addrTranslator.PluginToHost(...)

    // 3. Dial the address
    return dialGRPCConn(b.tls, netAddrDialer(addr))
}
```

### Pattern 2: Knock/Accept (Multiplexed)

Used when all connections share a single multiplexed connection (yamux-style).

**Dialer Side (sends knock):**
```go
func (b *GRPCBroker) knock(id uint32) error {
    // 1. Send a knock request
    err := b.streamer.Send(&plugin.ConnInfo{
        ServiceId: id,
        Knock: &plugin.ConnInfo_Knock{Knock: true},
    })

    // 2. Wait for acknowledgement
    p := b.getClientStream(id)
    select {
    case msg := <-p.ch:
        if msg.Knock.Error != "" {
            return fmt.Errorf("failed to knock: %s", msg.Knock.Error)
        }
    case <-time.After(5 * time.Second):
        return fmt.Errorf("timeout waiting for knock ack")
    }

    return nil
}
```

**Acceptor Side (handles knock):**
```go
func (b *GRPCBroker) listenForKnocks(id uint32) error {
    p := b.getServerStream(id)
    for {
        select {
        case msg := <-p.ch:
            // 1. Accept the knock
            err := b.muxer.AcceptKnock(id)

            // 2. Send acknowledgement
            err = b.streamer.Send(&plugin.ConnInfo{
                ServiceId: id,
                Knock: &plugin.ConnInfo_Knock{
                    Knock: true,
                    Ack:   true,
                    Error: ackError,
                },
            })
        case <-p.doneCh:
            return nil
        }
    }
}
```

## Usage Pattern: Host Service as Plugin Argument

The canonical example from the bidirectional example:

### 1. Define the Callback Interface (Go)

```go
// AddHelper is a service the host provides to the plugin
type AddHelper interface {
    Sum(int64, int64) (int64, error)
}

// Counter is the plugin interface that uses the callback
type Counter interface {
    Put(key string, value int64, a AddHelper) error
    Get(key string) (int64, error)
}
```

### 2. Client Side (Host → Plugin Call with Callback)

```go
func (m *GRPCClient) Put(key string, value int64, a AddHelper) error {
    // 1. Create a gRPC server for the callback
    addHelperServer := &GRPCAddHelperServer{Impl: a}

    var s *grpc.Server
    serverFunc := func(opts []grpc.ServerOption) *grpc.Server {
        s = grpc.NewServer(opts...)
        proto.RegisterAddHelperServer(s, addHelperServer)
        return s
    }

    // 2. Get a unique broker ID
    brokerID := m.broker.NextId()

    // 3. Accept connections on that ID (starts listener, sends ConnInfo)
    go m.broker.AcceptAndServe(brokerID, serverFunc)

    // 4. Call the plugin, passing the broker ID
    _, err := m.client.Put(context.Background(), &proto.PutRequest{
        AddServer: brokerID,  // Plugin will dial this ID
        Key:       key,
        Value:     value,
    })

    s.Stop()
    return err
}
```

### 3. Server Side (Plugin Receives Call, Uses Callback)

```go
func (m *GRPCServer) Put(ctx context.Context, req *proto.PutRequest) (*proto.Empty, error) {
    // 1. Dial the broker ID to connect to host's callback server
    conn, err := m.broker.Dial(req.AddServer)
    if err != nil {
        return nil, err
    }
    defer conn.Close()

    // 2. Create client for the callback service
    a := &GRPCAddHelperClient{proto.NewAddHelperClient(conn)}

    // 3. Call implementation with the callback
    return &proto.Empty{}, m.Impl.Put(req.Key, req.Value, a)
}
```

### 4. Proto Definition

```protobuf
message PutRequest {
    uint32 add_server = 1;  // Broker ID for callback connection
    string key = 2;
    int64 value = 3;
}

service Counter {
    rpc Put(PutRequest) returns (Empty);
    rpc Get(GetRequest) returns (GetResponse);
}

service AddHelper {
    rpc Sum(SumRequest) returns (SumResponse);
}
```

## Broker Initialization

### Server Side (grpc_server.go:87-91)

```go
func (s *GRPCServer) Init() error {
    // 1. Create broker server (handles incoming stream)
    brokerServer := newGRPCBrokerServer()

    // 2. Register on the main gRPC server
    plugin.RegisterGRPCBrokerServer(s.server, brokerServer)

    // 3. Create broker instance
    s.broker = newGRPCBroker(brokerServer, s.TLS, unixSocketConfigFromEnv(), nil, s.muxer)

    // 4. Start the broker's message routing
    go s.broker.Run()

    // 5. Pass broker to each plugin
    for k, raw := range s.Plugins {
        p := raw.(GRPCPlugin)
        p.GRPCServer(s.broker, s.server)  // Broker available to plugins
    }
}
```

### Client Side (grpc_client.go:69-73)

```go
func newGRPCClient(doneCtx context.Context, c *Client) (*GRPCClient, error) {
    conn, _ := dialGRPCConn(c.config.TLSConfig, c.dialer)

    // 1. Create broker client (connects to server's stream)
    brokerGRPCClient := newGRPCBrokerClient(conn)

    // 2. Create broker instance
    broker := newGRPCBroker(brokerGRPCClient, c.config.TLSConfig, ...)

    // 3. Start the broker's message routing
    go broker.Run()

    // 4. Start the control stream
    go brokerGRPCClient.StartStream()

    // 5. Broker available via GRPCClient.broker for plugins
    return &GRPCClient{broker: broker, ...}, nil
}
```

## Message Routing (Run loop)

```go
func (m *GRPCBroker) Run() {
    for {
        msg, err := m.streamer.Recv()
        if err != nil {
            break  // Exit on error
        }

        // Route to appropriate pending channel
        var p *gRPCBrokerPending
        if msg.Knock != nil && msg.Knock.Knock && !msg.Knock.Ack {
            // Knock request → server stream (to handle knock)
            p = m.getServerStream(msg.ServiceId)
        } else {
            // Connection info or knock ack → client stream (waiting to dial)
            p = m.getClientStream(msg.ServiceId)
            go m.timeoutWait(msg.ServiceId, p)  // Cleanup after timeout
        }

        select {
        case p.ch <- msg:
        default:  // Non-blocking
        }
    }
}
```

## Multiplexing (GRPCMuxer)

The muxer allows all brokered connections to share a single TCP/Unix connection:

```go
type GRPCMuxer interface {
    Enabled() bool
    Listener(id uint32, doneCh <-chan struct{}) (net.Listener, error)
    AcceptKnock(id uint32) error
    Dial() (net.Conn, error)
    Close() error
}
```

Benefits:
- Fewer open connections
- Works better with firewalls/proxies
- Lower latency for connection establishment

Trade-offs:
- More complex implementation
- Head-of-line blocking possible

## Key Design Decisions for connect-plugin

### What We Must Adapt

| go-plugin | connect-plugin |
|-----------|----------------|
| gRPC bidirectional stream | Connect bidirectional stream or SSE + unary |
| TCP/Unix per-brokered-connection | HTTP-based endpoints per service ID |
| Address passing via ConnInfo | Capability URLs or service routing |
| Process-local optimization | Network-first design |

### Core Concepts to Preserve

1. **Broker ID pattern**: Unique ID to correlate Accept/Dial pairs
2. **Callback-as-argument**: Pass service capability, not just data
3. **Symmetric broker**: Both sides can Accept or Dial
4. **Automatic cleanup**: Timeout waiting connections

### Network-Friendly Adaptation

#### Option A: Capability URLs
```go
// Host side
brokerID := broker.NextId()
url := broker.AcceptCapability(brokerID, addHelperHandler)
// url = "https://host:8080/capabilities/42"

// Plugin side (receives URL in request)
client := broker.DialCapability(req.AddServerUrl)
```

#### Option B: Reverse HTTP (Plugin calls back to Host)
```go
// Host registers callback endpoint
broker.RegisterCapability("add-helper", addHelperHandler)

// Plugin makes request to host's base URL
// POST https://host:8080/capabilities/add-helper/sum
```

#### Option C: WebSocket/Stream-based broker
```go
// Single persistent stream for all broker messages
// Similar to go-plugin but over HTTP/2 stream
```

### Recommended Approach

For connect-plugin, we should implement **Option A (Capability URLs)** with **streaming fallback**:

1. **Primary**: Each capability gets a unique URL endpoint
2. **Optimization**: Reuse existing HTTP/2 connection via Connect streams
3. **Security**: Capabilities include bearer token for authorization

```protobuf
message CapabilityGrant {
    string capability_id = 1;     // Unique ID
    string endpoint_url = 2;      // Full URL to call
    string bearer_token = 3;      // Authorization token
    google.protobuf.Timestamp expires_at = 4;
}

message CallWithCapability {
    string capability_id = 1;     // Reference in the request
    // ... other fields
}
```

## Failure Modes

| Failure | go-plugin Handling | connect-plugin Adaptation |
|---------|-------------------|---------------------------|
| Dial timeout | 5 second timeout | Configurable timeout + retry |
| Stream error | Break Run() loop, close broker | Reconnect stream, re-register capabilities |
| Address unreachable | Connection error | Circuit breaker on capability URL |
| Knock not acked | 5 second timeout | HTTP timeout + retry with backoff |

## Conclusions

1. **GRPCBroker is essential** for bidirectional communication patterns
2. **ID-based correlation** allows matching Accept/Dial pairs
3. **Both sides symmetric**: Either can be client or server for a brokered service
4. **Multiplexing optional** but beneficial for network efficiency
5. **For connect-plugin**: Capability URLs with bearer tokens provide network-friendly equivalent

## Next Steps

1. Design `connectplugin.broker.v1` proto for capability exchange
2. Implement Connect-native Accept/Dial pattern
3. Design capability URL routing in server
4. Prototype bidirectional example with Connect

## References

- grpc_broker.go (full implementation)
- grpc_broker.proto (wire protocol)
- examples/bidirectional/ (usage pattern)
- internal/grpcmux/ (multiplexing support)
