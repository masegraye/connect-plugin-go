package memtransport_test

import (
	"context"
	"errors"
	"net/http"
	"testing"
	"time"

	"connectrpc.com/connect"
	kvv1 "github.com/masegraye/connect-plugin-go/examples/kv/gen"
	"github.com/masegraye/connect-plugin-go/examples/kv/gen/kvv1connect"
	kvimpl "github.com/masegraye/connect-plugin-go/examples/kv/impl"
	"github.com/masegraye/connect-plugin-go/internal/memtransport"
)

// setupKVService creates a KV service running over memtransport.
// Returns the Connect client and a cleanup function.
func setupKVService(t *testing.T) (kvv1connect.KVServiceClient, func()) {
	t.Helper()

	ln := memtransport.New()
	store := kvimpl.NewStore()

	mux := http.NewServeMux()
	path, handler := kvv1connect.NewKVServiceHandler(store)
	mux.Handle(path, handler)

	srv := &http.Server{Handler: mux}
	go srv.Serve(ln)

	client := kvv1connect.NewKVServiceClient(ln.HTTPClient(), "http://mem")

	cleanup := func() {
		srv.Shutdown(context.Background())
		ln.Close()
	}

	return client, cleanup
}

func TestConnectRPC_UnaryPut(t *testing.T) {
	client, cleanup := setupKVService(t)
	defer cleanup()

	ctx := context.Background()

	_, err := client.Put(ctx, connect.NewRequest(&kvv1.PutRequest{
		Key:   "test-key",
		Value: []byte("test-value"),
	}))
	if err != nil {
		t.Fatalf("Put failed: %v", err)
	}
}

func TestConnectRPC_UnaryGetAfterPut(t *testing.T) {
	client, cleanup := setupKVService(t)
	defer cleanup()

	ctx := context.Background()

	// Put
	_, err := client.Put(ctx, connect.NewRequest(&kvv1.PutRequest{
		Key:   "hello",
		Value: []byte("world"),
	}))
	if err != nil {
		t.Fatalf("Put failed: %v", err)
	}

	// Get
	resp, err := client.Get(ctx, connect.NewRequest(&kvv1.GetRequest{
		Key: "hello",
	}))
	if err != nil {
		t.Fatalf("Get failed: %v", err)
	}

	if !resp.Msg.Found {
		t.Fatal("expected Found=true")
	}
	if string(resp.Msg.Value) != "world" {
		t.Errorf("value = %q, want %q", resp.Msg.Value, "world")
	}
}

func TestConnectRPC_UnaryDelete(t *testing.T) {
	client, cleanup := setupKVService(t)
	defer cleanup()

	ctx := context.Background()

	// Put then delete
	client.Put(ctx, connect.NewRequest(&kvv1.PutRequest{
		Key:   "to-delete",
		Value: []byte("temp"),
	}))

	delResp, err := client.Delete(ctx, connect.NewRequest(&kvv1.DeleteRequest{
		Key: "to-delete",
	}))
	if err != nil {
		t.Fatalf("Delete failed: %v", err)
	}
	if !delResp.Msg.Found {
		t.Error("expected Found=true on delete")
	}

	// Verify gone
	getResp, err := client.Get(ctx, connect.NewRequest(&kvv1.GetRequest{
		Key: "to-delete",
	}))
	if err != nil {
		t.Fatalf("Get after delete failed: %v", err)
	}
	if getResp.Msg.Found {
		t.Error("expected Found=false after delete")
	}
}

func TestConnectRPC_UnaryNotFound(t *testing.T) {
	client, cleanup := setupKVService(t)
	defer cleanup()

	ctx := context.Background()

	resp, err := client.Get(ctx, connect.NewRequest(&kvv1.GetRequest{
		Key: "nonexistent",
	}))
	if err != nil {
		t.Fatalf("Get failed: %v", err)
	}
	if resp.Msg.Found {
		t.Error("expected Found=false for nonexistent key")
	}
}

func TestConnectRPC_UnaryValidationError(t *testing.T) {
	client, cleanup := setupKVService(t)
	defer cleanup()

	ctx := context.Background()

	// Put with empty key should return error
	_, err := client.Put(ctx, connect.NewRequest(&kvv1.PutRequest{
		Key:   "",
		Value: []byte("value"),
	}))
	if err == nil {
		t.Fatal("expected error for empty key")
	}

	// Verify it's a Connect error with correct code
	var connectErr *connect.Error
	if ok := connect.IsNotModifiedError(err); ok {
		t.Skip("not modified, skip")
	}
	if errors.As(err, &connectErr) {
		if connectErr.Code() != connect.CodeInvalidArgument {
			t.Errorf("error code = %v, want %v", connectErr.Code(), connect.CodeInvalidArgument)
		}
	}
}

func TestConnectRPC_ServerStreaming(t *testing.T) {
	ln := memtransport.New()
	store := kvimpl.NewStore()

	mux := http.NewServeMux()
	path, handler := kvv1connect.NewKVServiceHandler(store)
	mux.Handle(path, handler)

	srv := &http.Server{Handler: mux}
	go srv.Serve(ln)
	defer srv.Shutdown(context.Background())
	defer ln.Close()

	client := kvv1connect.NewKVServiceClient(ln.HTTPClient(), "http://mem")
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	// Start watching
	stream, err := client.Watch(ctx, connect.NewRequest(&kvv1.WatchRequest{
		Prefix: "user:",
	}))
	if err != nil {
		t.Fatalf("Watch failed: %v", err)
	}

	// Receive the initial "watch started" event
	if !stream.Receive() {
		t.Fatalf("expected initial event, got error: %v", stream.Err())
	}
	initial := stream.Msg()
	if initial.Key != "_watch_started" {
		t.Errorf("initial event key = %q, want %q", initial.Key, "_watch_started")
	}

	// Put a matching key (in background so stream can receive it)
	go func() {
		time.Sleep(50 * time.Millisecond)
		client.Put(ctx, connect.NewRequest(&kvv1.PutRequest{
			Key:   "user:alice",
			Value: []byte("Alice"),
		}))
	}()

	// Should receive the put event
	if !stream.Receive() {
		t.Fatalf("expected put event, got error: %v", stream.Err())
	}
	putEvent := stream.Msg()
	if putEvent.Key != "user:alice" {
		t.Errorf("event key = %q, want %q", putEvent.Key, "user:alice")
	}
	if string(putEvent.Value) != "Alice" {
		t.Errorf("event value = %q, want %q", putEvent.Value, "Alice")
	}
	if putEvent.Type != kvv1.EventType_EVENT_TYPE_PUT {
		t.Errorf("event type = %v, want PUT", putEvent.Type)
	}

	// Cancel to end stream
	cancel()
}

func TestConnectRPC_MultipleOperations(t *testing.T) {
	client, cleanup := setupKVService(t)
	defer cleanup()

	ctx := context.Background()

	// Exercise a sequence of operations to stress the transport.
	keys := []string{"a", "b", "c", "d", "e"}
	for _, k := range keys {
		_, err := client.Put(ctx, connect.NewRequest(&kvv1.PutRequest{
			Key:   k,
			Value: []byte("val-" + k),
		}))
		if err != nil {
			t.Fatalf("Put(%s) failed: %v", k, err)
		}
	}

	for _, k := range keys {
		resp, err := client.Get(ctx, connect.NewRequest(&kvv1.GetRequest{Key: k}))
		if err != nil {
			t.Fatalf("Get(%s) failed: %v", k, err)
		}
		if !resp.Msg.Found {
			t.Errorf("Get(%s): expected Found=true", k)
		}
		expected := "val-" + k
		if string(resp.Msg.Value) != expected {
			t.Errorf("Get(%s) = %q, want %q", k, resp.Msg.Value, expected)
		}
	}

	for _, k := range keys {
		_, err := client.Delete(ctx, connect.NewRequest(&kvv1.DeleteRequest{Key: k}))
		if err != nil {
			t.Fatalf("Delete(%s) failed: %v", k, err)
		}
	}

	for _, k := range keys {
		resp, err := client.Get(ctx, connect.NewRequest(&kvv1.GetRequest{Key: k}))
		if err != nil {
			t.Fatalf("Get(%s) after delete failed: %v", k, err)
		}
		if resp.Msg.Found {
			t.Errorf("Get(%s) after delete: expected Found=false", k)
		}
	}
}

