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

	"github.com/phaladamou/personal-cloud/compute/node"
	"github.com/phaladamou/personal-cloud/packages/protocol"
	"github.com/phaladamou/personal-cloud/runtime/container"
	"github.com/phaladamou/personal-cloud/services/api"
	controller "github.com/phaladamou/personal-cloud/services/workspace-controller"
	"github.com/phaladamou/personal-cloud/storage/snapshots"
	"github.com/phaladamou/personal-cloud/storage/volumes"
)

func main() {
	addr := flag.String("addr", ":8080", "HTTP listen address")
	data := flag.String("data", "./data/workspaces.json", "path of the JSON workspace store")
	storage := flag.String("storage", "./data/storage", "root directory for volumes and snapshots")
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
	vols, err := volumes.NewLocalManager(filepath.Join(*storage, "volumes"))
	if err != nil {
		log.Fatalf("api: open volumes: %v", err)
	}
	snaps, err := snapshots.NewLocalManager(filepath.Join(*storage, "snapshots"), vols.DataPath)
	if err != nil {
		log.Fatalf("api: open snapshots: %v", err)
	}
	bus := protocol.NewEventBus()
	ctrl := controller.NewController(store, container.DefaultBackend(), bus,
		controller.WithVolumes(vols), controller.WithSnapshots(snaps),
		controller.WithFilesystems(controller.DirFSProvider{Root: filepath.Join(*storage, "workspaces")}))

	log.Printf("api: listening on %s (store %s, storage %s)", *addr, *data, *storage)
	if err := http.ListenAndServe(*addr, api.NewServer(ctrl, api.WithNodes(node.NewRegistry(bus)))); err != nil {
		log.Fatalf("api: serve: %v", err)
	}
}
