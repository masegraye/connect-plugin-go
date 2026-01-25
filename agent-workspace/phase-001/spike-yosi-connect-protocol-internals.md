# Spike: Connect RPC Deep Dive - Protocol Internals

**Issue:** KOR-yosi
**Status:** Complete

## Executive Summary

Connect RPC supports three wire protocols: Connect, gRPC, and gRPC-Web. All three can be served by the same handler and use the same HTTP/2 or HTTP/1.1 transport. The Connect protocol is the simplest - unary RPCs are plain HTTP POST/GET requests with JSON or Protobuf bodies. Streaming RPCs use an envelope format similar to gRPC. Understanding these internals is essential for connect-plugin to properly handle plugin communication.

## Protocol Overview

### Supported Protocols

| Protocol | Transport | Content-Type Pattern | Trailers |
|----------|-----------|---------------------|----------|
| Connect | HTTP/1.1, HTTP/2 | `application/{codec}` (unary), `application/connect+{codec}` (stream) | In-body for streams |
| gRPC | HTTP/2 only | `application/grpc+{codec}` | HTTP/2 trailers |
| gRPC-Web | HTTP/1.1, HTTP/2 | `application/grpc-web+{codec}` | In-body envelope |

### Protocol Selection (Server-Side)

The server determines protocol from Content-Type and HTTP method:

```go
// protocol_connect.go:136
func (h *connectHandler) CanHandlePayload(request *http.Request, contentType string) bool {
    if request.Method == http.MethodGet {
        // Check query parameters for encoding
    }
    _, ok := h.accept[contentType]
    return ok
}
```

## Connect Protocol Wire Format

### Unary RPCs

**Request:**
```http
POST /service.v1.Service/Method HTTP/1.1
Content-Type: application/json
Connect-Protocol-Version: 1
Connect-Timeout-Ms: 10000
Accept-Encoding: gzip

{"field": "value"}
```

**Successful Response:**
```http
HTTP/1.1 200 OK
Content-Type: application/json
Content-Encoding: gzip

{"result": "value"}
```

**Error Response:**
```http
HTTP/1.1 400 Bad Request
Content-Type: application/json

{
  "code": "invalid_argument",
  "message": "field is required",
  "details": [
    {
      "type": "google.rpc.BadRequest",
      "value": "Cg...",
      "debug": {"fieldViolations": [...]}
    }
  ]
}
```

### Unary GET Requests (Idempotent)

For side-effect-free procedures:

```http
GET /service.v1.Service/Method?connect=v1&encoding=json&message=%7B%22id%22%3A1%7D HTTP/1.1
```

Query parameters:
- `connect=v1`: Protocol version
- `encoding`: Codec name (json, proto)
- `message`: URL-encoded or base64-encoded message
- `base64=1`: If message is base64-encoded
- `compression`: If message is compressed

### Streaming RPCs

**Content Types:**
- Unary: `application/{codec}` (e.g., `application/json`, `application/proto`)
- Streaming: `application/connect+{codec}` (e.g., `application/connect+json`)

**Envelope Format (envelope.go:39-49):**

```
┌─────────────────────────────────────────────────────────┐
│  Flags (1 byte)  │  Length (4 bytes, big-endian)  │ Data │
└─────────────────────────────────────────────────────────┘
```

**Flags:**
- `0b00000001`: Compressed (same as gRPC)
- `0b00000010`: End of stream (Connect-specific)

**End-of-Stream Message:**

When a streaming RPC completes, the server sends a special envelope with flag `0b00000010`:

```json
{
  "error": {
    "code": "invalid_argument",
    "message": "validation failed"
  },
  "metadata": {
    "x-custom-trailer": ["value"]
  }
}
```

### Headers

**Request Headers:**
```
Content-Type: application/connect+proto
Connect-Protocol-Version: 1
Connect-Timeout-Ms: 30000
Connect-Accept-Encoding: gzip      (streaming)
Accept-Encoding: gzip              (unary)
```

**Response Headers:**
```
Content-Type: application/connect+proto
Connect-Content-Encoding: gzip     (streaming)
Content-Encoding: gzip             (unary)
Connect-Accept-Encoding: gzip,identity
```

**Unary Trailers (via headers):**
```
Trailer-X-Custom: value
```

### Compression Negotiation

```go
// protocol.go:302-342
func negotiateCompression(
    availableCompressors readOnlyCompressionPools,
    sent, accept string,
) (requestCompression, responseCompression string, clientVisibleErr *Error) {
    // 1. Validate request compression is supported
    // 2. Default response compression = request compression
    // 3. If client accepts different compression, use first mutually supported
}
```

### Timeout Handling

Connect uses milliseconds in header:

```go
// protocol_connect.go:117-134
func (*connectHandler) SetTimeout(request *http.Request) (context.Context, context.CancelFunc, error) {
    timeout := getHeaderCanonical(request.Header, connectHeaderTimeout) // "Connect-Timeout-Ms"
    millis, err := strconv.ParseInt(timeout, 10, 64)
    // Max 10 digits
    ctx, cancel := context.WithTimeout(request.Context(), time.Duration(millis)*time.Millisecond)
    return ctx, cancel, nil
}
```

## gRPC Protocol Wire Format

### Differences from Connect

1. **Trailers**: Uses HTTP/2 trailers for status
2. **Timeout**: `Grpc-Timeout` header with unit suffix (n/u/m/S/M/H)
3. **Status**: `Grpc-Status`, `Grpc-Message`, `Grpc-Status-Details-Bin` trailers
4. **Content-Type**: `application/grpc` or `application/grpc+{codec}`

### gRPC Headers

```
Content-Type: application/grpc+proto
Grpc-Encoding: gzip
Grpc-Accept-Encoding: gzip,identity
Grpc-Timeout: 30S
Te: trailers
```

### gRPC Trailers

```
Grpc-Status: 3
Grpc-Message: invalid%20argument
Grpc-Status-Details-Bin: CAMSFmludmFsaWQgYXJndW1lbnQ...
```

### gRPC Timeout Encoding

```go
// protocol_grpc.go:763-794
func grpcEncodeTimeout(timeout time.Duration) string {
    // Units: n=nanosecond, u=microsecond, m=millisecond, S=second, M=minute, H=hour
    // Max 8 digits
    switch {
    case timeout < time.Nanosecond*1e8:
        return fmt.Sprintf("%dn", timeout/time.Nanosecond)
    // ... etc
    }
}
```

## gRPC-Web Protocol

### Differences

1. **No HTTP trailers**: Trailers embedded in response body
2. **Trailer envelope**: Uses flag `0b10000000` (128)
3. **Content-Type**: `application/grpc-web+{codec}`

### Trailer Envelope Format

```
┌─────────────────────────────────────────────────────────┐
│  Flags=0x80  │  Length (4 bytes)  │ MIME Headers Block │
└─────────────────────────────────────────────────────────┘
```

The trailer data is MIME headers (like HTTP/1 headers):
```
grpc-status: 0
grpc-message:
x-custom-trailer: value
```

## Error Codes

### Connect Code to HTTP Status Mapping

```go
// protocol_connect.go:1269-1308
func connectCodeToHTTP(code Code) int {
    switch code {
    case CodeCanceled:          return 499
    case CodeUnknown:           return 500
    case CodeInvalidArgument:   return 400
    case CodeDeadlineExceeded:  return 504
    case CodeNotFound:          return 404
    case CodeAlreadyExists:     return 409
    case CodePermissionDenied:  return 403
    case CodeResourceExhausted: return 429
    case CodeFailedPrecondition: return 400
    case CodeAborted:           return 409
    case CodeOutOfRange:        return 400
    case CodeUnimplemented:     return 501
    case CodeInternal:          return 500
    case CodeUnavailable:       return 503
    case CodeDataLoss:          return 500
    case CodeUnauthenticated:   return 401
    default:                    return 500
    }
}
```

### HTTP Status to Code Mapping

```go
// protocol.go:401-424
func httpToCode(httpCode int) Code {
    switch httpCode {
    case 400: return CodeInternal
    case 401: return CodeUnauthenticated
    case 403: return CodePermissionDenied
    case 404: return CodeUnimplemented
    case 429: return CodeUnavailable
    case 502, 503, 504: return CodeUnavailable
    default: return CodeUnknown
    }
}
```

## DuplexHTTPCall (Client-Side)

### Overview

`duplexHTTPCall` manages bidirectional communication over HTTP:

```go
// duplex_http_call.go
type duplexHTTPCall struct {
    ctx            context.Context
    httpClient     HTTPClient
    url            *url.URL
    spec           Spec
    header         http.Header

    // Request body is a pipe
    requestBodyReader *io.PipeReader
    requestBodyWriter *io.PipeWriter

    // Response handling
    response          *http.Response
    responseReady     chan struct{}
}
```

### Send/Receive Flow

1. **Send**: Write to `requestBodyWriter` → piped to HTTP request body
2. **Response Ready**: Wait for `responseReady` channel
3. **Receive**: Read from `response.Body`

## Protocol Interface

### Handler Side

```go
// protocol.go:90-113
type protocolHandler interface {
    Methods() map[string]struct{}
    ContentTypes() map[string]struct{}
    SetTimeout(*http.Request) (context.Context, context.CancelFunc, error)
    CanHandlePayload(*http.Request, string) bool
    NewConn(http.ResponseWriter, *http.Request) (handlerConnCloser, bool)
}
```

### Client Side

```go
// protocol.go:139-153
type protocolClient interface {
    Peer() Peer
    WriteRequestHeader(StreamType, http.Header)
    NewConn(context.Context, Spec, http.Header) streamingClientConn
}
```

## Implications for connect-plugin

### Protocol Selection

For connect-plugin, we should default to the Connect protocol because:

1. **HTTP/1.1 compatible**: Works through HTTP proxies, load balancers
2. **Simpler debugging**: JSON-encoded errors are human-readable
3. **GET support**: Enables caching for idempotent operations
4. **Browser-friendly**: No special handling needed

However, we should also support gRPC for:
1. Interop with existing gRPC services
2. Performance-sensitive deployments (binary-only)

### Handshake Protocol Design

For plugin handshake, use Connect's unary pattern:

```http
POST /.connectplugin.v1.Handshake/Negotiate HTTP/1.1
Content-Type: application/json
Connect-Protocol-Version: 1

{
  "core_protocol_version": 1,
  "supported_app_versions": [1, 2, 3],
  "requested_plugins": ["kv", "auth"],
  "client_capabilities": {"streaming": true}
}
```

Response:
```json
{
  "negotiated_app_version": 2,
  "available_plugins": [
    {"name": "kv", "version": "1.0", "capabilities": ["get", "put"]}
  ],
  "server_capabilities": {"bidirectional": true}
}
```

### Timeout Propagation

Use Connect-Timeout-Ms for:
- Plugin call timeouts (per-RPC)
- Circuit breaker trip detection

### Error Handling

Connect errors map naturally to plugin errors:

| Scenario | Connect Code | Action |
|----------|--------------|--------|
| Plugin unavailable | Unavailable (503) | Retry / circuit breaker |
| Plugin rejected request | InvalidArgument (400) | Return to caller |
| Plugin crashed | Internal (500) | Retry / health check |
| Auth failed | Unauthenticated (401) | Re-authenticate |
| Not allowed | PermissionDenied (403) | Return to caller |

### Streaming for Bidirectional

For capabilities (host → plugin callbacks), use Connect streaming:

1. Client (plugin) opens stream to capability endpoint
2. Server (host) sends requests on the stream
3. Client responds on the stream

This avoids the need for the plugin to expose an HTTP server.

## Wire Format Summary

### Unary Message

```
Request:
  Headers: Content-Type, Connect-Protocol-Version, Connect-Timeout-Ms
  Body: Codec-encoded message (JSON/Protobuf)

Response (success):
  Status: 200
  Headers: Content-Type, Trailer-*
  Body: Codec-encoded message

Response (error):
  Status: 4xx/5xx
  Headers: Content-Type: application/json
  Body: {"code": "...", "message": "...", "details": [...]}
```

### Streaming Message

```
Request/Response:
  Headers: Content-Type: application/connect+{codec}
  Body: Sequence of envelopes

Envelope:
  [1 byte flags][4 bytes length][N bytes data]

End-of-stream envelope (flags=0x02):
  [0x02][length][{"error": ..., "metadata": ...}]
```

## Conclusions

1. **Connect protocol is ideal** for network plugins - HTTP/1.1 compatible, JSON debugging
2. **Envelope format** is shared between Connect streaming and gRPC
3. **Error codes** map cleanly to HTTP status for debugging
4. **Timeout propagation** via Connect-Timeout-Ms is straightforward
5. **Streaming enables bidirectional** without plugin needing to be HTTP server
6. **Protocol negotiation** happens via Content-Type header

## Next Steps

1. Implement handshake service using Connect unary
2. Design capability broker using Connect streaming
3. Add protocol detection for gRPC compatibility
4. Implement timeout propagation in client
