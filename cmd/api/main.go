// Command api runs the Personal Cloud control-plane HTTP API.
//
// It wires the workspace controller over a JSON file store and the
// default (local) container backend, then serves the REST + WebSocket
// API until terminated.
package main

import (
	"crypto/rand"
	"flag"
	"log"
	"net/http"
	"os"
	"path/filepath"

	"github.com/phaladamou/personal-cloud/compute/node"
	"github.com/phaladamou/personal-cloud/packages/protocol"
	"github.com/phaladamou/personal-cloud/runtime/container"
	"github.com/phaladamou/personal-cloud/services/api"
	"github.com/phaladamou/personal-cloud/services/auth"
	"github.com/phaladamou/personal-cloud/services/gateway"
	"github.com/phaladamou/personal-cloud/services/scheduler"
	controller "github.com/phaladamou/personal-cloud/services/workspace-controller"
	"github.com/phaladamou/personal-cloud/storage/snapshots"
	"github.com/phaladamou/personal-cloud/storage/volumes"
)

func main() {
	addr := flag.String("addr", ":8080", "HTTP listen address")
	data := flag.String("data", "./data/workspaces.json", "path of the JSON workspace store")
	storage := flag.String("storage", "./data/storage", "root directory for volumes and snapshots")
	authSecret := flag.String("auth-secret", "", "HMAC secret for auth tokens (env PC_AUTH_SECRET, else generated under storage)")
	gatewayMode := flag.Bool("gateway", true, "front the API with the authenticating gateway (ports proxy, guarded terminal)")
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
	secret := []byte(*authSecret)
	if len(secret) == 0 {
		secret = []byte(os.Getenv("PC_AUTH_SECRET"))
	}
	if len(secret) == 0 {
		secret, err = loadOrCreateSecret(filepath.Join(*storage, "auth-secret"))
		if err != nil {
			log.Fatalf("api: auth secret: %v", err)
		}
	}
	tokenStore, err := auth.NewFileStore(filepath.Join(*storage, "auth-tokens.json"))
	if err != nil {
		log.Fatalf("api: open tokens: %v", err)
	}
	authSvc, err := auth.NewService(tokenStore, secret)
	if err != nil {
		log.Fatalf("api: auth service: %v", err)
	}

	bus := protocol.NewEventBus()
	nodes := node.NewRegistry(bus)
	ctrl := controller.NewController(store, container.DefaultBackend(), bus,
		controller.WithVolumes(vols), controller.WithSnapshots(snaps),
		controller.WithFilesystems(controller.VolumeFSProvider{Volumes: vols, DataPath: vols.DataPath}),
		controller.WithScheduler(scheduler.New(nodes)))

	log.Printf("api: listening on %s (store %s, storage %s)", *addr, *data, *storage)
	apiSrv := api.NewServer(ctrl, api.WithNodes(nodes), api.WithAuth(authSvc))
	var handler http.Handler = apiSrv
	if *gatewayMode {
		handler = gateway.New(gateway.NewRegistry(), apiSrv, gateway.WithAuth(authSvc))
		log.Printf("api: gateway in front (ports proxy on; only login and healthz stay open)")
	}
	if err := http.ListenAndServe(*addr, handler); err != nil {
		log.Fatalf("api: serve: %v", err)
	}
}

// loadOrCreateSecret reads the HMAC secret at path, generating and storing
// (owner-only) a fresh 32-byte secret when the file is missing.
func loadOrCreateSecret(path string) ([]byte, error) {
	if raw, err := os.ReadFile(path); err == nil {
		return raw, nil
	} else if !os.IsNotExist(err) {
		return nil, err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return nil, err
	}
	var secret [32]byte
	if _, err := rand.Read(secret[:]); err != nil {
		return nil, err
	}
	if err := os.WriteFile(path, secret[:], 0o600); err != nil {
		return nil, err
	}
	return secret[:], nil
}
