package api

import (
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"github.com/phaladamou/personal-cloud/compute/node"
	"github.com/phaladamou/personal-cloud/packages/protocol"
	"github.com/phaladamou/personal-cloud/services/scheduler"
	controller "github.com/phaladamou/personal-cloud/services/workspace-controller"
)

// newPlacedEnv wires the API over a scheduler with one node: node-a
// (4 CPU, 8 GiB, 100 GB disk, no GPU).
func newPlacedEnv(t *testing.T) *testEnv {
	t.Helper()
	store, err := controller.NewFileStore(filepath.Join(t.TempDir(), "ws.json"))
	if err != nil {
		t.Fatalf("NewFileStore: %v", err)
	}
	backend := &controller.FakeBackend{}
	nodes := node.NewRegistry(nil)
	if _, err := nodes.Register(t.Context(), node.Spec{
		ID: "node-a", Name: "a", CPU: 4, MemoryMB: 8192, DiskGB: 100,
	}); err != nil {
		t.Fatalf("Register: %v", err)
	}
	ctrl := controller.NewController(store, backend, protocol.NewEventBus(),
		controller.WithScheduler(scheduler.New(nodes)))
	authSvc, token := newTestAuth(t)
	srv := httptest.NewServer(NewServer(ctrl, WithAuth(authSvc), WithNodes(nodes)))
	t.Cleanup(srv.Close)
	return &testEnv{srv: srv, backend: backend, token: token}
}

func TestCreateStartWithResources(t *testing.T) {
	env := newPlacedEnv(t)
	code, body := doJSON(t, env.token, http.MethodPost, env.srv.URL+"/v1/workspaces",
		map[string]any{"user_id": "user-1", "name": "dev",
			"cpu": 2, "memory_mb": 1024, "disk_gb": 10})
	if code != http.StatusCreated {
		t.Fatalf("create: got %d (%v), want 201", code, body)
	}
	id := body["id"].(string)
	if body["node_id"] != nil && body["node_id"] != "" {
		t.Fatalf("created node_id: got %v, want empty", body["node_id"])
	}
	res, _ := body["resources"].(map[string]any)
	if res["cpu"] != float64(2) || res["memory_mb"] != float64(1024) {
		t.Fatalf("created resources: got %v", body["resources"])
	}

	code, body = action(t, env, id, "start")
	if code != http.StatusOK {
		t.Fatalf("start: got %d (%v), want 200", code, body)
	}
	if body["node_id"] != "node-a" || body["status"] != "RUNNING" {
		t.Fatalf("started: got %v, want RUNNING on node-a", body)
	}

	code, body = action(t, env, id, "stop")
	if code != http.StatusOK || body["node_id"] != nil {
		t.Fatalf("stopped: got %d %v, want 200 without node_id", code, body)
	}
}

func TestStartNoFitIsConflict(t *testing.T) {
	env := newPlacedEnv(t)
	code, body := doJSON(t, env.token, http.MethodPost, env.srv.URL+"/v1/workspaces",
		map[string]any{"user_id": "user-1", "name": "huge",
			"cpu": 64, "memory_mb": 1 << 20, "disk_gb": 1000})
	if code != http.StatusCreated {
		t.Fatalf("create: got %d (%v), want 201", code, body)
	}
	id := body["id"].(string)

	code, body = action(t, env, id, "start")
	if code != http.StatusConflict {
		t.Fatalf("start oversized: got %d (%v), want 409", code, body)
	}
	if msg, _ := body["error"].(string); !strings.Contains(msg, "no suitable node") {
		t.Fatalf("409 message: got %q, want a no-suitable-node explanation", msg)
	}
	code, body = doJSON(t, env.token, http.MethodGet, env.srv.URL+"/v1/workspaces/"+id, nil)
	if code != http.StatusOK || body["status"] != "CREATED" {
		t.Fatalf("after no-fit: got %d %v, want CREATED", code, body)
	}
}

func TestStartGPURequirement(t *testing.T) {
	env := newPlacedEnv(t) // node-a has no GPU.
	id := createWorkspace(t, env, "user-1", "gpu-job")["id"].(string)
	code, body := doJSON(t, env.token, http.MethodPost, env.srv.URL+"/v1/workspaces/"+id+"/start",
		map[string]any{"cpu": 1, "memory_mb": 64, "disk_gb": 1, "gpus": 1})
	if code != http.StatusConflict {
		t.Fatalf("start with GPU need: got %d (%v), want 409", code, body)
	}
}

func TestStartWithBodyPlaces(t *testing.T) {
	env := newPlacedEnv(t)
	id := createWorkspace(t, env, "user-1", "dev")["id"].(string)
	// A plain workspace starts placed when the start body carries
	// requirements, and the requirements stick for later restarts.
	code, body := doJSON(t, env.token, http.MethodPost, env.srv.URL+"/v1/workspaces/"+id+"/start",
		map[string]any{"cpu": 1, "memory_mb": 64, "disk_gb": 1})
	if code != http.StatusOK || body["node_id"] != "node-a" {
		t.Fatalf("start with body: got %d %v, want 200 on node-a", code, body)
	}
	if code, _ := action(t, env, id, "stop"); code != http.StatusOK {
		t.Fatalf("stop: got %d", code)
	}
	code, body = action(t, env, id, "start")
	if code != http.StatusOK || body["node_id"] != "node-a" {
		t.Fatalf("restart: got %d %v, want 200 on node-a", code, body)
	}
}

func TestCreateStartValidation(t *testing.T) {
	env := newPlacedEnv(t)
	// Negative requirements are rejected on both endpoints.
	code, _ := doJSON(t, env.token, http.MethodPost, env.srv.URL+"/v1/workspaces",
		map[string]any{"user_id": "user-1", "name": "dev", "cpu": -1})
	if code != http.StatusBadRequest {
		t.Fatalf("create negative: got %d, want 400", code)
	}
	id := createWorkspace(t, env, "user-1", "dev")["id"].(string)
	code, _ = doJSON(t, env.token, http.MethodPost, env.srv.URL+"/v1/workspaces/"+id+"/start",
		map[string]any{"gpus": -2})
	if code != http.StatusBadRequest {
		t.Fatalf("start negative: got %d, want 400", code)
	}
	// A malformed start body is a 400, not a start.
	code, _ = doJSON(t, env.token, http.MethodPost, env.srv.URL+"/v1/workspaces/"+id+"/start",
		"just a string")
	if code != http.StatusBadRequest {
		t.Fatalf("start malformed: got %d, want 400", code)
	}
}
