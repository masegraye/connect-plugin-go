// Package main demonstrates direct dispatch — in-memory ConnectRPC with zero TCP.
//
// This example shows the simplest possible use of InMemoryStrategy:
// a KV plugin running in the same process, communicating via net.Pipe()
// with full ConnectRPC protocol (protobuf serialization, headers, streaming).
//
// No TCP ports. No binaries. No network I/O.
package main

import (
	"context"
	"fmt"
	"log"

	"connectrpc.com/connect"
	connectplugin "github.com/masegraye/connect-plugin-go"
	kvv1 "github.com/masegraye/connect-plugin-go/examples/kv/gen"
	"github.com/masegraye/connect-plugin-go/examples/kv/gen/kvv1connect"
	kvplugin "github.com/masegraye/connect-plugin-go/examples/kv/gen/kvv1plugin"
	kvimpl "github.com/masegraye/connect-plugin-go/examples/kv/impl"
)

func main() {
	ctx := context.Background()
	log.Println("=== Direct Dispatch: In-Memory ConnectRPC ===")
	log.Println()

	// 1. Minimal infrastructure — just a registry
	lifecycle := connectplugin.NewLifecycleServer()
	registry := connectplugin.NewServiceRegistry(lifecycle)

	// 2. Create launcher with InMemoryStrategy
	launcher := connectplugin.NewPluginLauncher(nil, registry)
	launcher.RegisterStrategy(connectplugin.NewInMemoryStrategy(registry))

	// 3. Configure KV plugin for direct dispatch
	launcher.Configure(map[string]connectplugin.PluginSpec{
		"kv": {
			Name:     "kv",
			Provides: []string{"kv"},
			Strategy: "in-memory", // No TCP port needed
			Plugin:   &kvplugin.KVServicePlugin{},
			ImplFactory: func() any {
				return kvimpl.NewStore()
			},
		},
	})

	// 4. Get the service — launches plugin in-process
	endpoint, httpClient, err := launcher.GetServiceClient("kv", "kv")
	if err != nil {
		log.Fatalf("Failed to get service: %v", err)
	}
	log.Printf("KV plugin ready at %s (in-memory, zero TCP)", endpoint)

	// 5. Create typed ConnectRPC client
	kvClient := kvv1connect.NewKVServiceClient(httpClient, endpoint)

	// 6. Use it — full ConnectRPC protocol over net.Pipe()
	log.Println()
	log.Println("--- Unary RPCs ---")

	// Put
	_, err = kvClient.Put(ctx, connect.NewRequest(&kvv1.PutRequest{
		Key:   "greeting",
		Value: []byte("hello from direct dispatch"),
	}))
	if err != nil {
		log.Fatalf("Put failed: %v", err)
	}
	log.Println("Put(greeting) = hello from direct dispatch")

	// Get
	getResp, err := kvClient.Get(ctx, connect.NewRequest(&kvv1.GetRequest{
		Key: "greeting",
	}))
	if err != nil {
		log.Fatalf("Get failed: %v", err)
	}
	log.Printf("Get(greeting) = %s (found=%v)", getResp.Msg.Value, getResp.Msg.Found)

	// Store more data
	for i := range 5 {
		key := fmt.Sprintf("item:%d", i)
		_, err := kvClient.Put(ctx, connect.NewRequest(&kvv1.PutRequest{
			Key:   key,
			Value: []byte(fmt.Sprintf("value-%d", i)),
		}))
		if err != nil {
			log.Fatalf("Put(%s) failed: %v", key, err)
		}
	}
	log.Println("Put 5 items (item:0 through item:4)")

	// Delete
	delResp, err := kvClient.Delete(ctx, connect.NewRequest(&kvv1.DeleteRequest{
		Key: "item:2",
	}))
	if err != nil {
		log.Fatalf("Delete failed: %v", err)
	}
	log.Printf("Delete(item:2) found=%v", delResp.Msg.Found)

	// Verify deletion
	getResp2, err := kvClient.Get(ctx, connect.NewRequest(&kvv1.GetRequest{
		Key: "item:2",
	}))
	if err != nil {
		log.Fatalf("Get failed: %v", err)
	}
	log.Printf("Get(item:2) found=%v (expected false)", getResp2.Msg.Found)

	// 7. Server streaming works too
	log.Println()
	log.Println("--- Server Streaming ---")

	streamCtx, cancel := context.WithCancel(ctx)
	stream, err := kvClient.Watch(streamCtx, connect.NewRequest(&kvv1.WatchRequest{
		Prefix: "live:",
	}))
	if err != nil {
		log.Fatalf("Watch failed: %v", err)
	}

	// Receive initial event
	stream.Receive()
	log.Println("Watch stream established (prefix: live:)")

	// Put a key that matches the watch prefix (in background)
	go func() {
		kvClient.Put(ctx, connect.NewRequest(&kvv1.PutRequest{
			Key:   "live:status",
			Value: []byte("streaming works!"),
		}))
	}()

	// Receive the streamed event
	if stream.Receive() {
		event := stream.Msg()
		log.Printf("Watch event: type=%s key=%s value=%s", event.Type, event.Key, event.Value)
	}
	cancel()

	log.Println()
	log.Println("=== Summary ===")
	log.Println("  Transport:  net.Pipe() (in-memory)")
	log.Println("  Protocol:   ConnectRPC over HTTP/1.1")
	log.Println("  TCP ports:  0")
	log.Println("  Binaries:   0")
	log.Println("  Features:   unary RPCs, server streaming, protobuf, headers")
	log.Println()

	launcher.Shutdown()
}
