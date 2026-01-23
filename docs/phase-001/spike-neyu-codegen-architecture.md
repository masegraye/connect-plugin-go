# Spike: Code Generation Tool Architecture

**Issue:** KOR-neyu
**Status:** Complete

## Executive Summary

The connect-plugin code generation tool (`connect-plugin-gen`) generates Go adapters that bridge between Go interfaces and Connect RPC services. Unlike protoc plugins that transform .proto files, connect-plugin-gen reads proto schemas AND Go interface definitions to generate bidirectional adapters. The tool follows protoc plugin conventions while adding a second pass for Go interface binding.

## Architecture Overview

```
┌─────────────────────────────────────────────────────────────────────┐
│                     connect-plugin-gen                              │
│                                                                      │
│  ┌─────────────┐    ┌─────────────┐    ┌─────────────────────────┐  │
│  │   Proto     │    │    Go       │    │    Template             │  │
│  │   Parser    │───▶│   Matcher   │───▶│    Generator            │  │
│  │             │    │             │    │                         │  │
│  │ (protogen)  │    │ (go/types)  │    │ Plugin.go               │  │
│  │             │    │             │    │ Client.go               │  │
│  └─────────────┘    └─────────────┘    │ Server.go               │  │
│        ▲                   ▲           └─────────────────────────┘  │
│        │                   │                      │                  │
│        │                   │                      ▼                  │
│  ┌─────┴─────┐    ┌───────┴───────┐    ┌─────────────────────────┐  │
│  │ .proto    │    │ Go interface  │    │ Generated Files         │  │
│  │ files     │    │ source        │    │                         │  │
│  └───────────┘    └───────────────┘    │ *_plugin.go             │  │
│                                        │ *_grpc_client.go        │  │
│                                        │ *_grpc_server.go        │  │
│                                        └─────────────────────────┘  │
└─────────────────────────────────────────────────────────────────────┘
```

## Protoc Plugin Fundamentals

### Plugin Protocol

Protoc plugins communicate via stdin/stdout:

1. **Input**: `CodeGeneratorRequest` protobuf message on stdin
2. **Processing**: Parse descriptors, generate code
3. **Output**: `CodeGeneratorResponse` protobuf message on stdout

```go
func main() {
    protogen.Options{
        ParamFunc: flagSet.Set,
    }.Run(func(plugin *protogen.Plugin) error {
        for _, file := range plugin.Files {
            if file.Generate {
                generate(plugin, file)
            }
        }
        return nil
    })
}
```

### Key protogen Types

```go
// Plugin represents the running code generator
type Plugin struct {
    Files []*File                    // All files in compilation
    SupportedFeatures uint64         // Features this plugin supports
}

// File represents a .proto file
type File struct {
    GoPackageName GoPackageName      // Go package name
    GoImportPath  GoImportPath       // Go import path
    Services      []*Service         // Services in this file
    Messages      []*Message         // Messages in this file
}

// Service represents a protobuf service
type Service struct {
    GoName  string                   // Go identifier
    Methods []*Method                // RPCs in this service
}

// Method represents an RPC
type Method struct {
    GoName string                    // Go identifier
    Input  *Message                  // Request type
    Output *Message                  // Response type
    Desc   protoreflect.MethodDescriptor  // Full descriptor
}

// GeneratedFile is used to write output
type GeneratedFile struct {
    // Methods for writing code
    P(...interface{})                // Print line
    QualifiedGoIdent(GoIdent) string // Import and qualify identifier
}
```

## connect-plugin-gen Design

### Invocation Modes

#### Mode 1: Standalone CLI

```bash
# Generate from proto schema + Go interface source
connect-plugin-gen \
  --proto=kvstore.proto \
  --interface=github.com/example/app.KVStore \
  --output=./gen

# Generates:
# gen/kvstore_plugin.go      - Plugin definition
# gen/kvstore_grpc_client.go - Client adapter (proto -> interface)
# gen/kvstore_grpc_server.go - Server adapter (interface -> proto)
```

#### Mode 2: Protoc Plugin

```bash
# Use as protoc plugin with option specifying Go interface
protoc --go_out=. --connect-plugin_out=. \
  --connect-plugin_opt=interface=github.com/example/app.KVStore \
  kvstore.proto
```

#### Mode 3: Buf Plugin

```yaml
# buf.gen.yaml
version: v2
plugins:
  - local: protoc-gen-go
    out: gen
  - local: protoc-gen-connect-plugin
    out: gen
    opt:
      - interface=github.com/example/app.KVStore
```

### Configuration

```go
type Config struct {
    // Proto source
    ProtoFile    string   // .proto file path
    ProtoPackage string   // Proto package to generate from

    // Go interface mapping
    Interface    string   // Fully qualified Go interface
    InterfacePkg string   // Go package containing interface

    // Output control
    OutputDir    string   // Output directory
    PackageName  string   // Generated package name

    // Options
    GenerateClient bool   // Generate client adapter
    GenerateServer bool   // Generate server adapter
    GenerateMock   bool   // Generate mock implementation
}
```

### Interface Matching

The generator must match proto service methods to Go interface methods:

```go
type Matcher struct {
    protoService *protogen.Service
    goInterface  *types.Interface
}

func (m *Matcher) Match() (*MethodMapping, error) {
    mapping := &MethodMapping{}

    for _, protoMethod := range m.protoService.Methods {
        goMethod := m.findGoMethod(protoMethod.GoName)
        if goMethod == nil {
            return nil, fmt.Errorf("no Go method for %s", protoMethod.GoName)
        }

        // Validate signatures match
        if err := m.validateSignature(protoMethod, goMethod); err != nil {
            return nil, err
        }

        mapping.Methods = append(mapping.Methods, &MethodMatch{
            Proto: protoMethod,
            Go:    goMethod,
        })
    }

    return mapping, nil
}
```

### Signature Validation

```go
func (m *Matcher) validateSignature(proto *protogen.Method, goMethod *types.Func) error {
    sig := goMethod.Type().(*types.Signature)

    // Check parameters
    params := sig.Params()
    if params.Len() < 2 {
        return fmt.Errorf("expected at least 2 params (ctx, req), got %d", params.Len())
    }

    // First param must be context.Context
    if !isContextType(params.At(0).Type()) {
        return fmt.Errorf("first param must be context.Context")
    }

    // Check return values
    results := sig.Results()
    if results.Len() < 1 || results.Len() > 2 {
        return fmt.Errorf("expected 1-2 return values, got %d", results.Len())
    }

    // Last return must be error
    if !isErrorType(results.At(results.Len() - 1).Type()) {
        return fmt.Errorf("last return must be error")
    }

    return nil
}
```

## Generated Code Structure

### Plugin Definition

```go
// gen/kvstore_plugin.go
package kvstoreconnect

import (
    "context"

    connectplugin "github.com/example/connect-plugin-go"
    "google.golang.org/grpc"
)

// KVStorePlugin implements connectplugin.ConnectPlugin for the KVStore interface.
type KVStorePlugin struct{}

var _ connectplugin.ConnectPlugin = (*KVStorePlugin)(nil)

func (p *KVStorePlugin) Name() string {
    return "kvstore"
}

func (p *KVStorePlugin) Client(conn connectplugin.ClientConn) (interface{}, error) {
    return NewKVStoreGRPCClient(conn), nil
}

func (p *KVStorePlugin) Server(impl interface{}) (connectplugin.Handler, error) {
    kvstore, ok := impl.(KVStore)
    if !ok {
        return nil, fmt.Errorf("expected KVStore, got %T", impl)
    }
    return NewKVStoreGRPCServer(kvstore), nil
}
```

### Client Adapter (Proto -> Go Interface)

```go
// gen/kvstore_grpc_client.go
package kvstoreconnect

import (
    "context"

    pb "github.com/example/gen/kvstore/v1"
    kvstore "github.com/example/app"
    connectplugin "github.com/example/connect-plugin-go"
    "connectrpc.com/connect"
)

// KVStoreGRPCClient wraps a Connect client to implement the KVStore interface.
type KVStoreGRPCClient struct {
    client pb.KVStoreServiceClient
}

var _ kvstore.KVStore = (*KVStoreGRPCClient)(nil)

func NewKVStoreGRPCClient(conn connectplugin.ClientConn) *KVStoreGRPCClient {
    return &KVStoreGRPCClient{
        client: pb.NewKVStoreServiceClient(conn.HTTPClient(), conn.BaseURL()),
    }
}

func (c *KVStoreGRPCClient) Get(ctx context.Context, key string) ([]byte, error) {
    resp, err := c.client.Get(ctx, connect.NewRequest(&pb.GetRequest{
        Key: key,
    }))
    if err != nil {
        return nil, err
    }
    return resp.Msg.Value, nil
}

func (c *KVStoreGRPCClient) Put(ctx context.Context, key string, value []byte) error {
    _, err := c.client.Put(ctx, connect.NewRequest(&pb.PutRequest{
        Key:   key,
        Value: value,
    }))
    return err
}
```

### Server Adapter (Go Interface -> Proto)

```go
// gen/kvstore_grpc_server.go
package kvstoreconnect

import (
    "context"

    pb "github.com/example/gen/kvstore/v1"
    kvstore "github.com/example/app"
    "connectrpc.com/connect"
)

// KVStoreGRPCServer wraps a KVStore implementation to serve as a Connect handler.
type KVStoreGRPCServer struct {
    impl kvstore.KVStore
}

func NewKVStoreGRPCServer(impl kvstore.KVStore) *KVStoreGRPCServer {
    return &KVStoreGRPCServer{impl: impl}
}

func (s *KVStoreGRPCServer) Get(
    ctx context.Context,
    req *connect.Request[pb.GetRequest],
) (*connect.Response[pb.GetResponse], error) {
    value, err := s.impl.Get(ctx, req.Msg.Key)
    if err != nil {
        return nil, err
    }
    return connect.NewResponse(&pb.GetResponse{Value: value}), nil
}

func (s *KVStoreGRPCServer) Put(
    ctx context.Context,
    req *connect.Request[pb.PutRequest],
) (*connect.Response[pb.PutResponse], error) {
    err := s.impl.Put(ctx, req.Msg.Key, req.Msg.Value)
    if err != nil {
        return nil, err
    }
    return connect.NewResponse(&pb.PutResponse{}), nil
}
```

## Streaming Method Adaptation

### Go chan -> Proto Stream

```go
// Go interface with channel
type EventStream interface {
    Subscribe(ctx context.Context, filter string) (<-chan Event, error)
}

// Generated server adapter
func (s *EventStreamGRPCServer) Subscribe(
    ctx context.Context,
    req *connect.Request[pb.SubscribeRequest],
    stream *connect.ServerStream[pb.Event],
) error {
    events, err := s.impl.Subscribe(ctx, req.Msg.Filter)
    if err != nil {
        return err
    }

    for event := range events {
        if ctx.Err() != nil {
            return ctx.Err()
        }
        if err := stream.Send(&pb.Event{
            Id:      event.ID,
            Type:    event.Type,
            Payload: event.Payload,
        }); err != nil {
            return err
        }
    }
    return nil
}
```

### Proto Stream -> Go chan

```go
// Generated client adapter
func (c *EventStreamGRPCClient) Subscribe(ctx context.Context, filter string) (<-chan Event, error) {
    stream, err := c.client.Subscribe(ctx, connect.NewRequest(&pb.SubscribeRequest{
        Filter: filter,
    }))
    if err != nil {
        return nil, err
    }

    events := make(chan Event)
    go func() {
        defer close(events)
        for stream.Receive() {
            select {
            case events <- Event{
                ID:      stream.Msg().Id,
                Type:    stream.Msg().Type,
                Payload: stream.Msg().Payload,
            }:
            case <-ctx.Done():
                return
            }
        }
        if err := stream.Err(); err != nil {
            // Log error
        }
    }()

    return events, nil
}
```

## Type Conversion

### Automatic Conversions

| Go Type | Proto Type |
|---------|------------|
| string | string |
| []byte | bytes |
| int32, int64 | int32, int64 |
| uint32, uint64 | uint32, uint64 |
| float32, float64 | float, double |
| bool | bool |
| time.Time | google.protobuf.Timestamp |
| time.Duration | google.protobuf.Duration |
| map[K]V | map<K, V> |
| []T | repeated T |

### Custom Type Mapping

```go
// Configuration for custom type mapping
type TypeMapping struct {
    GoType    string // e.g., "uuid.UUID"
    ProtoType string // e.g., "string"
    ToProto   string // e.g., "uuid.UUID.String"
    FromProto string // e.g., "uuid.Parse"
}

// In generated code
func (c *Client) GetUser(ctx context.Context, id uuid.UUID) (*User, error) {
    resp, err := c.client.GetUser(ctx, connect.NewRequest(&pb.GetUserRequest{
        Id: id.String(),  // Custom conversion
    }))
    if err != nil {
        return nil, err
    }
    return &User{
        ID:   uuid.MustParse(resp.Msg.User.Id),  // Custom conversion
        Name: resp.Msg.User.Name,
    }, nil
}
```

## Template-Based Generation

### Template Structure

```go
var clientTemplate = template.Must(template.New("client").Parse(`
package {{.PackageName}}

import (
{{range .Imports}}
    {{.Alias}} "{{.Path}}"
{{end}}
)

// {{.ClientName}} wraps a Connect client to implement {{.InterfaceName}}.
type {{.ClientName}} struct {
    client {{.ProtoClientType}}
}

var _ {{.InterfacePackage}}.{{.InterfaceName}} = (*{{.ClientName}})(nil)

func New{{.ClientName}}(conn connectplugin.ClientConn) *{{.ClientName}} {
    return &{{.ClientName}}{
        client: {{.ProtoPackage}}.New{{.ServiceName}}Client(conn.HTTPClient(), conn.BaseURL()),
    }
}

{{range .Methods}}
func (c *{{$.ClientName}}) {{.GoName}}({{.Params}}) {{.Returns}} {
    {{.Body}}
}
{{end}}
`))
```

### Generator Implementation

```go
type Generator struct {
    plugin   *protogen.Plugin
    config   *Config
    matcher  *Matcher
}

func (g *Generator) Generate() error {
    // Parse Go interface
    iface, err := g.parseGoInterface()
    if err != nil {
        return err
    }

    // Match methods
    mapping, err := g.matcher.Match(g.protoService, iface)
    if err != nil {
        return err
    }

    // Generate files
    if g.config.GenerateClient {
        if err := g.generateClient(mapping); err != nil {
            return err
        }
    }
    if g.config.GenerateServer {
        if err := g.generateServer(mapping); err != nil {
            return err
        }
    }

    return nil
}

func (g *Generator) generateClient(mapping *MethodMapping) error {
    data := &ClientTemplateData{
        PackageName:   g.config.PackageName,
        ClientName:    mapping.Service.GoName + "GRPCClient",
        InterfaceName: mapping.Interface.Name(),
        // ... populate rest
    }

    f := g.plugin.NewGeneratedFile(
        g.config.OutputDir+"/"+strings.ToLower(mapping.Service.GoName)+"_grpc_client.go",
        protogen.GoImportPath(g.config.PackageName),
    )

    return clientTemplate.Execute(f, data)
}
```

## CLI Implementation

```go
// cmd/connect-plugin-gen/main.go
package main

import (
    "flag"
    "fmt"
    "os"

    "github.com/example/connect-plugin-go/gen"
)

func main() {
    var cfg gen.Config

    flag.StringVar(&cfg.ProtoFile, "proto", "", "Proto file path")
    flag.StringVar(&cfg.Interface, "interface", "", "Go interface (fully qualified)")
    flag.StringVar(&cfg.OutputDir, "output", ".", "Output directory")
    flag.BoolVar(&cfg.GenerateClient, "client", true, "Generate client adapter")
    flag.BoolVar(&cfg.GenerateServer, "server", true, "Generate server adapter")
    flag.Parse()

    // Detect if running as protoc plugin
    if isProtocPlugin() {
        runAsProtocPlugin(&cfg)
        return
    }

    // Standalone mode
    if cfg.ProtoFile == "" || cfg.Interface == "" {
        fmt.Fprintln(os.Stderr, "Usage: connect-plugin-gen --proto=FILE --interface=PKG.TYPE")
        flag.PrintDefaults()
        os.Exit(1)
    }

    if err := gen.Generate(&cfg); err != nil {
        fmt.Fprintln(os.Stderr, "Error:", err)
        os.Exit(1)
    }
}

func isProtocPlugin() bool {
    // Protoc plugins receive input on stdin with no args
    return len(os.Args) == 1 && !isTerminal(os.Stdin)
}

func runAsProtocPlugin(cfg *gen.Config) {
    protogen.Options{
        ParamFunc: func(name, value string) error {
            switch name {
            case "interface":
                cfg.Interface = value
            case "client":
                cfg.GenerateClient = value != "false"
            case "server":
                cfg.GenerateServer = value != "false"
            }
            return nil
        },
    }.Run(func(plugin *protogen.Plugin) error {
        for _, file := range plugin.Files {
            if file.Generate {
                gen.GenerateFile(plugin, file, cfg)
            }
        }
        return nil
    })
}
```

## Key Differences from protoc-gen-connect-go

| Aspect | protoc-gen-connect-go | connect-plugin-gen |
|--------|----------------------|-------------------|
| Input | Proto only | Proto + Go interface |
| Output | Connect client/handler | Plugin adapters |
| Mode | Protoc plugin only | Protoc plugin or standalone |
| Matching | N/A | Interface ↔ Proto method |
| Conversion | Proto types only | Go types ↔ Proto types |

## Conclusions

1. **Dual input required**: Both proto schema and Go interface source
2. **protogen for proto parsing**: Reuse standard protobuf tooling
3. **go/types for interface parsing**: Parse Go interface definitions
4. **Method matching**: Validate proto methods match Go interface
5. **Template-based generation**: Clean, maintainable code generation
6. **Multiple modes**: Standalone CLI, protoc plugin, buf plugin

## Next Steps

1. Implement basic Generator skeleton
2. Add proto parsing with protogen
3. Add Go interface parsing with go/types
4. Implement method matching and validation
5. Add template-based code generation
6. Add streaming method support
7. Add custom type mapping configuration
