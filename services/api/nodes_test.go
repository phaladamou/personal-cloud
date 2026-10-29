package api

import (
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"

	"github.com/phaladamou/personal-cloud/compute/node"
	"github.com/phaladamou/personal-cloud/packages/protocol"
	"github.com/phaladamou/personal-cloud/packages/types"
	controller "github.com/phaladamou/personal-cloud/services/workspace-controller"
)

// nodesEnv wires the API over a node registry.
type nodesEnv struct {
	srv   *httptest.Server
	bus   *protocol.EventBus
	token string
}

func newNodesEnv(t *testing.T) *nodesEnv {
	t.Helper()
	store, err := controller.NewFileStore(filepath.Join(t.TempDir(), "ws.json"))
	if err != nil {
		t.Fatalf("NewFileStore: %v", err)
	}
	bus := protocol.NewEventBus()
	ctrl := controller.NewController(store, &controller.FakeBackend{}, bus)
	authSvc, token := newTestAuth(t)
	srv := httptest.NewServer(NewServer(ctrl, WithNodes(node.NewRegistry(bus)), WithAuth(authSvc)))
	t.Cleanup(srv.Close)
	return &nodesEnv{srv: srv, bus: bus, token: token}
}

func TestNodeFlow(t *testing.T) {
	env := newNodesEnv(t)
	var connected, disconnected []protocol.Event
	env.bus.Subscribe(protocol.EventNodeConnected, func(e protocol.Event) { connected = append(connected, e) })
	env.bus.Subscribe(protocol.EventNodeDisconnected, func(e protocol.Event) { disconnected = append(disconnected, e) })

	code, created := doJSON(t, env.token, http.MethodPost, env.srv.URL+"/v1/nodes", map[string]any{
		"name": "gpu-1", "cpu": 8, "memory_mb": 32768, "disk_gb": 500, "gpus": 1,
		"labels": map[string]string{"zone": "eu"},
	})
	if code != http.StatusCreated {
		t.Fatalf("register: got %d (%v), want 201", code, created)
	}
	id, _ := created["id"].(string)
	if id == "" || created["status"] != string(types.NodeOnline) || created["name"] != "gpu-1" {
		t.Fatalf("register fields: got %v", created)
	}
	if len(connected) != 1 || connected[0].WorkspaceID != "" {
		t.Fatalf("connected events: got %+v, want 1 cluster-scoped", connected)
	}
	var payload types.Node
	if err := connected[0].DecodePayload(&payload); err != nil || payload.ID != id {
		t.Fatalf("connected payload: got %+v, %v", payload, err)
	}

	code, list := doJSON(t, env.token, http.MethodGet, env.srv.URL+"/v1/nodes", nil)
	if code != http.StatusOK {
		t.Fatalf("list: got %d", code)
	}
	if items, _ := list["nodes"].([]any); len(items) != 1 {
		t.Fatalf("list: got %v, want 1 node", list)
	}
	code, single := doJSON(t, env.token, http.MethodGet, env.srv.URL+"/v1/nodes/"+id, nil)
	if code != http.StatusOK || single["id"] != id {
		t.Fatalf("get: got %d %v", code, single)
	}

	req, _ := http.NewRequest(http.MethodDelete, env.srv.URL+"/v1/nodes/"+id, nil)
	req.Header.Set("Authorization", "Bearer "+env.token)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("unregister: %v", err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusNoContent {
		t.Fatalf("unregister: got %d, want 204", resp.StatusCode)
	}
	if len(disconnected) != 1 {
		t.Fatalf("disconnected events: got %d, want 1", len(disconnected))
	}
	if err := disconnected[0].DecodePayload(&payload); err != nil || payload.ID != id {
		t.Fatalf("disconnected payload: got %+v, %v", payload, err)
	}
	if code, _ := doJSON(t, env.token, http.MethodGet, env.srv.URL+"/v1/nodes/"+id, nil); code != http.StatusNotFound {
		t.Fatalf("get after unregister: got %d, want 404", code)
	}
}

func TestNodeErrors(t *testing.T) {
	env := newNodesEnv(t)

	for name, body := range map[string]any{
		"empty name":  map[string]any{"cpu": 2, "memory_mb": 1024, "disk_gb": 10},
		"zero cpu":    map[string]any{"name": "n", "memory_mb": 1024, "disk_gb": 10},
		"zero memory": map[string]any{"name": "n", "cpu": 2, "disk_gb": 10},
		"zero disk":   map[string]any{"name": "n", "cpu": 2, "memory_mb": 1024},
		"wrong shape": "just a string",
	} {
		if code, _ := doJSON(t, env.token, http.MethodPost, env.srv.URL+"/v1/nodes", body); code != http.StatusBadRequest {
			t.Fatalf("register %s: got %d, want 400", name, code)
		}
	}
	if code, _ := doJSON(t, env.token, http.MethodGet, env.srv.URL+"/v1/nodes/nope", nil); code != http.StatusNotFound {
		t.Fatalf("get unknown: got %d, want 404", code)
	}
	req, _ := http.NewRequest(http.MethodDelete, env.srv.URL+"/v1/nodes/nope", nil)
	req.Header.Set("Authorization", "Bearer "+env.token)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("unregister unknown: %v", err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("unregister unknown: got %d, want 404", resp.StatusCode)
	}
}

func TestNodeHeartbeatAndCapacity(t *testing.T) {
	env := newNodesEnv(t)

	code, created := doJSON(t, env.token, http.MethodPost, env.srv.URL+"/v1/nodes", map[string]any{
		"name": "n1", "cpu": 8, "memory_mb": 16384, "disk_gb": 200, "gpus": 1,
	})
	if code != http.StatusCreated {
		t.Fatalf("register: got %d (%v), want 201", code, created)
	}
	id, _ := created["id"].(string)
	if created["last_heartbeat"] == "" {
		t.Fatalf("register heartbeat: got %v, want a timestamp", created)
	}

	code, hb := doJSON(t, env.token, http.MethodPost, env.srv.URL+"/v1/nodes/"+id+"/heartbeat", nil)
	if code != http.StatusOK || hb["id"] != id || hb["status"] != string(types.NodeOnline) {
		t.Fatalf("heartbeat: got %d %v", code, hb)
	}
	if code, _ := doJSON(t, env.token, http.MethodPost, env.srv.URL+"/v1/nodes/nope/heartbeat", nil); code != http.StatusNotFound {
		t.Fatalf("heartbeat unknown: got %d, want 404", code)
	}

	code, placed := doJSON(t, env.token, http.MethodPost, env.srv.URL+"/v1/nodes/"+id+"/allocate",
		map[string]any{"cpu": 3, "memory_mb": 4096, "disk_gb": 50, "gpus": 1})
	if code != http.StatusOK {
		t.Fatalf("allocate: got %d (%v), want 200", code, placed)
	}
	alloc, _ := placed["allocated"].(map[string]any)
	if alloc["cpu"] != float64(3) || alloc["gpus"] != float64(1) {
		t.Fatalf("allocate tracking: got %v", placed["allocated"])
	}
	if code, _ := doJSON(t, env.token, http.MethodPost, env.srv.URL+"/v1/nodes/"+id+"/allocate",
		map[string]any{"gpus": 1}); code != http.StatusConflict {
		t.Fatalf("allocate overflow: got %d, want 409", code)
	}
	if code, _ := doJSON(t, env.token, http.MethodPost, env.srv.URL+"/v1/nodes/"+id+"/allocate",
		map[string]any{"cpu": -1}); code != http.StatusBadRequest {
		t.Fatalf("allocate negative: got %d, want 400", code)
	}
	if code, _ := doJSON(t, env.token, http.MethodPost, env.srv.URL+"/v1/nodes/nope/allocate",
		map[string]any{"cpu": 1}); code != http.StatusNotFound {
		t.Fatalf("allocate unknown: got %d, want 404", code)
	}

	code, freed := doJSON(t, env.token, http.MethodPost, env.srv.URL+"/v1/nodes/"+id+"/release",
		map[string]any{"cpu": 3, "memory_mb": 4096, "disk_gb": 50, "gpus": 1})
	if code != http.StatusOK {
		t.Fatalf("release: got %d (%v), want 200", code, freed)
	}
	if alloc, _ := freed["allocated"].(map[string]any); alloc["cpu"] != float64(0) {
		t.Fatalf("release tracking: got %v", freed["allocated"])
	}
	if code, _ := doJSON(t, env.token, http.MethodPost, env.srv.URL+"/v1/nodes/"+id+"/release",
		map[string]any{"cpu": -1}); code != http.StatusBadRequest {
		t.Fatalf("release negative: got %d, want 400", code)
	}
	if code, _ := doJSON(t, env.token, http.MethodPost, env.srv.URL+"/v1/nodes/nope/release",
		map[string]any{"cpu": 1}); code != http.StatusNotFound {
		t.Fatalf("release unknown: got %d, want 404", code)
	}
}

func TestNodesUnavailable(t *testing.T) {
	env := newTestEnv(t) // No node registry.

	if code, _ := doJSON(t, env.token, http.MethodPost, env.srv.URL+"/v1/nodes", map[string]any{"name": "n"}); code != http.StatusNotImplemented {
		t.Fatalf("register without registry: got %d, want 501", code)
	}
	if code, _ := doJSON(t, env.token, http.MethodGet, env.srv.URL+"/v1/nodes", nil); code != http.StatusNotImplemented {
		t.Fatalf("list without registry: got %d, want 501", code)
	}
	if code, _ := doJSON(t, env.token, http.MethodGet, env.srv.URL+"/v1/nodes/n", nil); code != http.StatusNotImplemented {
		t.Fatalf("get without registry: got %d, want 501", code)
	}
	for name, path := range map[string]string{
		"heartbeat": "/v1/nodes/n/heartbeat",
		"allocate":  "/v1/nodes/n/allocate",
		"release":   "/v1/nodes/n/release",
	} {
		if code, _ := doJSON(t, env.token, http.MethodPost, env.srv.URL+path, map[string]any{"cpu": 1}); code != http.StatusNotImplemented {
			t.Fatalf("%s without registry: got %d, want 501", name, code)
		}
	}
	req, _ := http.NewRequest(http.MethodDelete, env.srv.URL+"/v1/nodes/n", nil)
	req.Header.Set("Authorization", "Bearer "+env.token)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("unregister without registry: %v", err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusNotImplemented {
		t.Fatalf("unregister without registry: got %d, want 501", resp.StatusCode)
	}
}
