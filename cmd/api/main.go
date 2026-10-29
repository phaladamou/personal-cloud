// Command api runs the Personal Cloud control-plane HTTP API.
//
// It wires the workspace controller over a JSON file store and the
// default (local) container backend, then serves the REST + WebSocket
// API until terminated.
package main

import (
	"flag"
	"log"
	"net/http"
	"os"
	"path/filepath"

	"github.com/phaladamou/personal-cloud/packages/protocol"
	"github.com/phaladamou/personal-cloud/runtime/container"
	"github.com/phaladamou/personal-cloud/services/api"
	controller "github.com/phaladamou/personal-cloud/services/workspace-controller"
)

func main() {
	addr := flag.String("addr", ":8080", "HTTP listen address")
	data := flag.String("data", "./data/workspaces.json", "path of the JSON workspace store")
	flag.Parse()

	if dir := filepath.Dir(*data); dir != "" {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			log.Fatalf("api: create data dir: %v", err)
		}
	}
	store, err := controller.NewFileStore(*data)
	if err != nil {
		log.Fatalf("api: open store: %v", err)
	}
	ctrl := controller.NewController(store, container.DefaultBackend(), protocol.NewEventBus())

	log.Printf("api: listening on %s (store %s)", *addr, *data)
	if err := http.ListenAndServe(*addr, api.NewServer(ctrl)); err != nil {
		log.Fatalf("api: serve: %v", err)
	}
}
