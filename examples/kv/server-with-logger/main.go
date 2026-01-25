package main

import (
	"log"

	connectplugin "github.com/masegraye/connect-plugin-go"
	loggercap "github.com/masegraye/connect-plugin-go/examples/capabilities/logger"
	kvimpllogger "github.com/masegraye/connect-plugin-go/examples/kv/impl-with-logger"
	kvplugin "github.com/masegraye/connect-plugin-go/examples/kv/gen/kvv1plugin"
)

func main() {
	log.Println("Starting KV plugin server with logger capability on :8080")

	// Create health service
	healthService := connectplugin.NewHealthServer()

	// Create capability broker
	broker := connectplugin.NewCapabilityBroker("http://localhost:8080")

	// Register logger capability
	loggerCap := loggercap.NewLoggerCapability()
	broker.RegisterCapability(loggerCap)
	log.Printf("Registered capability: %s v%s\n", loggerCap.CapabilityType(), loggerCap.Version())

	// Create KV store implementation that uses logger capability
	// NOTE: This requests the capability during NewStoreWithLogger()
	store, err := kvimpllogger.NewStoreWithLogger("http://localhost:8080/broker")
	if err != nil {
		log.Fatal("Failed to create store with logger:", err)
	}

	// Serve the plugin
	if err := connectplugin.Serve(&connectplugin.ServeConfig{
		Addr: ":8080",
		Plugins: connectplugin.PluginSet{
			"kv": &kvplugin.KVServicePlugin{},
		},
		Impls: map[string]any{
			"kv": store,
		},
		HealthService:    healthService,
		CapabilityBroker: broker,
	}); err != nil {
		log.Fatal("Server error:", err)
	}
}

