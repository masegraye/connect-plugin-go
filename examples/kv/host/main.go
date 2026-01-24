package main

import (
	"context"
	"log"
	"net/http"

	"connectrpc.com/connect"
	connectplugin "github.com/example/connect-plugin-go"
	kvv1 "github.com/example/connect-plugin-go/examples/kv/gen"
	"github.com/example/connect-plugin-go/examples/kv/gen/kvv1connect"
	kvplugin "github.com/example/connect-plugin-go/examples/kv/plugin"
	connectpluginv1 "github.com/example/connect-plugin-go/gen/plugin/v1"
	"github.com/example/connect-plugin-go/gen/plugin/v1/connectpluginv1connect"
)

func main() {
	ctx := context.Background()

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

	// Check server health
	log.Println("Checking server health...")
	healthClient := connectpluginv1connect.NewHealthServiceClient(
		&http.Client{},
		"http://localhost:8080",
	)
	healthResp, err := healthClient.Check(ctx, connect.NewRequest(&connectpluginv1.HealthCheckRequest{
		Service: "", // Overall health
	}))
	if err != nil {
		log.Printf("Health check error: %v (continuing anyway)", err)
	} else {
		log.Printf("✓ Server health: %v", healthResp.Msg.Status)
	}

	// Check KV plugin-specific health
	kvHealthResp, err := healthClient.Check(ctx, connect.NewRequest(&connectpluginv1.HealthCheckRequest{
		Service: "kv",
	}))
	if err != nil {
		log.Printf("KV health check error: %v (continuing anyway)", err)
	} else {
		log.Printf("✓ KV plugin health: %v", kvHealthResp.Msg.Status)
	}

	log.Println("Dispensing KV plugin...")
	raw, err := client.Dispense("kv")
	if err != nil {
		log.Fatal("Failed to dispense:", err)
	}

	kvClient := raw.(kvv1connect.KVServiceClient)

	// Test Put
	log.Println("Testing Put...")
	_, err = kvClient.Put(ctx, connect.NewRequest(&kvv1.PutRequest{
		Key:   "hello",
		Value: []byte("world"),
	}))
	if err != nil {
		log.Fatal("Put failed:", err)
	}
	log.Println("✓ Put succeeded")

	// Test Get
	log.Println("Testing Get...")
	getResp, err := kvClient.Get(ctx, connect.NewRequest(&kvv1.GetRequest{
		Key: "hello",
	}))
	if err != nil {
		log.Fatal("Get failed:", err)
	}
	if !getResp.Msg.Found {
		log.Fatal("Key not found")
	}
	log.Printf("✓ Get succeeded: %s\n", string(getResp.Msg.Value))

	// Test Delete
	log.Println("Testing Delete...")
	delResp, err := kvClient.Delete(ctx, connect.NewRequest(&kvv1.DeleteRequest{
		Key: "hello",
	}))
	if err != nil {
		log.Fatal("Delete failed:", err)
	}
	log.Printf("✓ Delete succeeded (found: %v)\n", delResp.Msg.Found)

	// Verify deleted
	log.Println("Verifying deletion...")
	getResp2, err := kvClient.Get(ctx, connect.NewRequest(&kvv1.GetRequest{
		Key: "hello",
	}))
	if err != nil {
		log.Fatal("Get failed:", err)
	}
	if getResp2.Msg.Found {
		log.Fatal("Key should not be found after delete")
	}
	log.Println("✓ Key successfully deleted")

	log.Println("\n✅ All operations completed successfully!")
}
