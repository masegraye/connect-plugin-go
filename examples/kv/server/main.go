package main

import (
	"log"

	connectplugin "github.com/example/connect-plugin-go"
	kvimpl "github.com/example/connect-plugin-go/examples/kv/impl"
	kvplugin "github.com/example/connect-plugin-go/examples/kv/plugin"
)

func main() {
	log.Println("Starting KV plugin server on :8080")

	// Create KV store implementation
	store := kvimpl.NewStore()

	// Serve the plugin
	if err := connectplugin.Serve(&connectplugin.ServeConfig{
		Addr: ":8080",
		Plugins: connectplugin.PluginSet{
			"kv": &kvplugin.KVServicePlugin{},
		},
		Impls: map[string]any{
			"kv": store,
		},
	}); err != nil {
		log.Fatal("Server error:", err)
	}
}
