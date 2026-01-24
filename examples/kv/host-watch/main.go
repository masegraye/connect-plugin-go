package main

import (
	"context"
	"log"
	"sync/atomic"
	"time"

	"connectrpc.com/connect"
	connectplugin "github.com/example/connect-plugin-go"
	kvv1 "github.com/example/connect-plugin-go/examples/kv/gen"
	"github.com/example/connect-plugin-go/examples/kv/gen/kvv1connect"
	kvplugin "github.com/example/connect-plugin-go/examples/kv/plugin"
)

func main() {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	log.Println("Creating plugin client...")

	// Create client
	client, err := connectplugin.NewClient(connectplugin.ClientConfig{
		Endpoint: "http://localhost:8080",
		Plugins: connectplugin.PluginSet{
			"kv": &kvplugin.KVServicePlugin{},
		},
	})
	if err != nil {
		log.Fatal("Failed to create client:", err)
	}
	defer client.Close()

	log.Println("Connecting to plugin...")
	if err := client.Connect(ctx); err != nil {
		log.Fatal("Failed to connect:", err)
	}

	log.Println("Dispensing KV plugin...")
	raw, err := client.Dispense("kv")
	if err != nil {
		log.Fatal("Failed to dispense:", err)
	}

	kvClient := raw.(kvv1connect.KVServiceClient)

	// Start watching in background
	watchCtx, cancelWatch := context.WithCancel(ctx)
	defer cancelWatch()

	log.Println("Starting watch for prefix 'test'...")
	watchStream, err := kvClient.Watch(watchCtx, connect.NewRequest(&kvv1.WatchRequest{
		Prefix: "test",
	}))
	if err != nil {
		log.Fatal("Watch failed:", err)
	}

	// Receive events in background
	var eventCount atomic.Int32
	done := make(chan struct{})

	go func() {
		defer close(done)
		for watchStream.Receive() {
			event := watchStream.Msg()
			count := eventCount.Add(1)
			log.Printf("📡 Watch event #%d: %v key=%s", count, event.Type, event.Key)
		}
		if err := watchStream.Err(); err != nil {
			log.Printf("Watch stream error: %v", err)
		}
	}()

	// Give watch time to start
	time.Sleep(100 * time.Millisecond)

	// Perform operations that trigger watch events
	log.Println("\n=== Performing operations ===")

	log.Println("1. Put test/a...")
	if _, err = kvClient.Put(ctx, connect.NewRequest(&kvv1.PutRequest{
		Key:   "test/a",
		Value: []byte("value-a"),
	})); err != nil {
		log.Fatal("Put failed:", err)
	}
	time.Sleep(100 * time.Millisecond)

	log.Println("2. Put test/b...")
	if _, err = kvClient.Put(ctx, connect.NewRequest(&kvv1.PutRequest{
		Key:   "test/b",
		Value: []byte("value-b"),
	})); err != nil {
		log.Fatal("Put failed:", err)
	}
	time.Sleep(100 * time.Millisecond)

	log.Println("3. Put other/c (should NOT trigger watch)...")
	if _, err = kvClient.Put(ctx, connect.NewRequest(&kvv1.PutRequest{
		Key:   "other/c",
		Value: []byte("value-c"),
	})); err != nil {
		log.Fatal("Put failed:", err)
	}
	time.Sleep(100 * time.Millisecond)

	log.Println("4. Delete test/a...")
	if _, err = kvClient.Delete(ctx, connect.NewRequest(&kvv1.DeleteRequest{
		Key: "test/a",
	})); err != nil {
		log.Fatal("Delete failed:", err)
	}
	time.Sleep(100 * time.Millisecond)

	// Cancel watch and wait for goroutine
	cancelWatch()

	select {
	case <-done:
		// Goroutine finished
	case <-time.After(1 * time.Second):
		log.Fatal("Timeout waiting for watch goroutine to finish")
	}

	finalCount := int(eventCount.Load())
	log.Printf("\n✅ Received %d watch events (expected 3: 2 puts, 1 delete)", finalCount)

	if finalCount != 3 {
		log.Fatalf("❌ Expected 3 events, got %d", finalCount)
	}

	log.Println("✅ Watch test passed!")
}
