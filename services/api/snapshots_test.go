package api

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/phaladamou/personal-cloud/packages/protocol"
	controller "github.com/phaladamou/personal-cloud/services/workspace-controller"
	"github.com/phaladamou/personal-cloud/storage/snapshots"
	"github.com/phaladamou/personal-cloud/storage/volumes"
)

// storageEnv wires the API over dir-backed storage managers.
type storageEnv struct {
	srv   *httptest.Server
	ctrl  *controller.Controller
	vols  *volumes.LocalManager
	snaps *snapshots.LocalManager
}

func newStorageEnv(t *testing.T) *storageEnv {
	t.Helper()
	store, err := controller.NewFileStore(filepath.Join(t.TempDir(), "ws.json"))
	if err != nil {
		t.Fatalf("NewFileStore: %v", err)
	}
	vols, err := volumes.NewLocalManager(filepath.Join(t.TempDir(), "volumes"))
	if err != nil {
		t.Fatalf("NewLocalManager volumes: %v", err)
	}
	snaps, err := snapshots.NewLocalManager(filepath.Join(t.TempDir(), "snapshots"), vols.DataPath)
	if err != nil {
		t.Fatalf("NewLocalManager snapshots: %v", err)
	}
	ctrl := controller.NewController(store, &controller.FakeBackend{}, protocol.NewEventBus(),
		controller.WithVolumes(vols), controller.WithSnapshots(snaps))
	srv := httptest.NewServer(NewServer(ctrl))
	t.Cleanup(srv.Close)
	return &storageEnv{srv: srv, ctrl: ctrl, vols: vols, snaps: snaps}
}

// createSnapshot posts a snapshot and returns the decoded body.
func createSnapshot(t *testing.T, env *storageEnv, id string, body any) (int, map[string]any) {
	t.Helper()
	return doJSON(t, http.MethodPost, env.srv.URL+"/v1/workspaces/"+id+"/snapshots", body)
}

func TestSnapshotFlow(t *testing.T) {
	ctx := context.Background()
	env := newStorageEnv(t)

	code, body := doJSON(t, http.MethodPost, env.srv.URL+"/v1/workspaces",
		map[string]string{"user_id": "user-1", "name": "dev"})
	if code != http.StatusCreated {
		t.Fatalf("create workspace: got %d (%v), want 201", code, body)
	}
	id := body["id"].(string)

	// Seed volume content through the default volume.
	vol, err := env.ctrl.CreateVolume(ctx, id, "default", 10, "/data")
	if err != nil {
		t.Fatalf("CreateVolume: %v", err)
	}
	dataPath := filepath.Join(env.vols.DataPath(id, vol.ID), "hello.txt")
	if err := os.WriteFile(dataPath, []byte("hello"), 0o644); err != nil {
		t.Fatalf("write volume data: %v", err)
	}

	// Create a snapshot over HTTP (default volume, empty body).
	code, snap := createSnapshot(t, env, id, nil)
	if code != http.StatusCreated {
		t.Fatalf("create snapshot: got %d (%v), want 201", code, snap)
	}
	sid, _ := snap["id"].(string)
	if sid == "" || snap["workspace_id"] != id || snap["volume_id"] != vol.ID {
		t.Fatalf("create snapshot fields: got %v", snap)
	}

	// Named snapshot with an explicit volume id.
	code, snap2 := createSnapshot(t, env, id, map[string]string{"volume_id": vol.ID, "name": "v2"})
	if code != http.StatusCreated {
		t.Fatalf("create named snapshot: got %d (%v), want 201", code, snap2)
	}
	// Unchanged content dedupes to the same snapshot.
	if snap2["id"] != sid {
		t.Fatalf("create unchanged: got %v, want dedupe to %s", snap2["id"], sid)
	}

	// List shows the snapshot; get returns it.
	code, list := doJSON(t, http.MethodGet, env.srv.URL+"/v1/workspaces/"+id+"/snapshots", nil)
	if code != http.StatusOK {
		t.Fatalf("list snapshots: got %d", code)
	}
	items, _ := list["snapshots"].([]any)
	if len(items) != 1 {
		t.Fatalf("list snapshots: got %v, want 1 snapshot", list)
	}
	code, single := doJSON(t, http.MethodGet, env.srv.URL+"/v1/workspaces/"+id+"/snapshots/"+sid, nil)
	if code != http.StatusOK || single["id"] != sid {
		t.Fatalf("get snapshot: got %d %v", code, single)
	}

	// Delete the file, restore over HTTP, verify the content is back.
	if err := os.Remove(dataPath); err != nil {
		t.Fatalf("remove file: %v", err)
	}
	code, restored := doJSON(t, http.MethodPost, env.srv.URL+"/v1/workspaces/"+id+"/snapshots/"+sid+"/restore", nil)
	if code != http.StatusOK || restored["id"] != sid {
		t.Fatalf("restore snapshot: got %d %v", code, restored)
	}
	raw, err := os.ReadFile(dataPath)
	if err != nil || string(raw) != "hello" {
		t.Fatalf("file after restore: got %q, %v, want %q", raw, err, "hello")
	}

	// Delete the snapshot.
	req, _ := http.NewRequest(http.MethodDelete, env.srv.URL+"/v1/workspaces/"+id+"/snapshots/"+sid, nil)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("delete snapshot: %v", err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusNoContent {
		t.Fatalf("delete snapshot: got %d, want 204", resp.StatusCode)
	}
	code, list = doJSON(t, http.MethodGet, env.srv.URL+"/v1/workspaces/"+id+"/snapshots", nil)
	if code != http.StatusOK {
		t.Fatalf("list after delete: got %d", code)
	}
	if items, _ := list["snapshots"].([]any); len(items) != 0 {
		t.Fatalf("list after delete: got %v, want empty", list)
	}
}

func TestSnapshotErrors(t *testing.T) {
	env := newStorageEnv(t)

	// Unknown workspace on every endpoint.
	if code, _ := createSnapshot(t, env, "nope", nil); code != http.StatusNotFound {
		t.Fatalf("create on missing workspace: got %d, want 404", code)
	}
	if code, _ := doJSON(t, http.MethodGet, env.srv.URL+"/v1/workspaces/nope/snapshots", nil); code != http.StatusNotFound {
		t.Fatalf("list on missing workspace: got %d, want 404", code)
	}
	if code, _ := doJSON(t, http.MethodGet, env.srv.URL+"/v1/workspaces/nope/snapshots/s", nil); code != http.StatusNotFound {
		t.Fatalf("get on missing workspace: got %d, want 404", code)
	}
	if code, _ := doJSON(t, http.MethodPost, env.srv.URL+"/v1/workspaces/nope/snapshots/s/restore", nil); code != http.StatusNotFound {
		t.Fatalf("restore on missing workspace: got %d, want 404", code)
	}
	req, _ := http.NewRequest(http.MethodDelete, env.srv.URL+"/v1/workspaces/nope/snapshots/s", nil)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("delete on missing workspace: %v", err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("delete on missing workspace: got %d, want 404", resp.StatusCode)
	}

	// Unknown snapshot on a real workspace.
	code, created := doJSON(t, http.MethodPost, env.srv.URL+"/v1/workspaces",
		map[string]string{"user_id": "user-1", "name": "dev"})
	if code != http.StatusCreated {
		t.Fatalf("create workspace: got %d (%v), want 201", code, created)
	}
	id := created["id"].(string)
	if code, _ := doJSON(t, http.MethodGet, env.srv.URL+"/v1/workspaces/"+id+"/snapshots/nope", nil); code != http.StatusNotFound {
		t.Fatalf("get missing snapshot: got %d, want 404", code)
	}
	if code, _ := doJSON(t, http.MethodPost, env.srv.URL+"/v1/workspaces/"+id+"/snapshots/nope/restore", nil); code != http.StatusNotFound {
		t.Fatalf("restore missing snapshot: got %d, want 404", code)
	}
	req, _ = http.NewRequest(http.MethodDelete, env.srv.URL+"/v1/workspaces/"+id+"/snapshots/nope", nil)
	resp, err = http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("delete missing snapshot: %v", err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("delete missing snapshot: got %d, want 404", resp.StatusCode)
	}

	// Unknown volume id.
	if code, _ := createSnapshot(t, env, id, map[string]string{"volume_id": "nope"}); code != http.StatusNotFound {
		t.Fatalf("create on missing volume: got %d, want 404", code)
	}

	// Invalid JSON body.
	if code, _ := doJSON(t, http.MethodPost, env.srv.URL+"/v1/workspaces/"+id+"/snapshots", "just a string"); code != http.StatusBadRequest {
		t.Fatalf("create invalid body: got %d, want 400", code)
	}
}

func TestSnapshotsUnavailable(t *testing.T) {
	env := newTestEnv(t) // No storage managers.
	id := createWorkspace(t, env, "user-1", "dev")["id"].(string)

	if code, _ := doJSON(t, http.MethodPost, env.srv.URL+"/v1/workspaces/"+id+"/snapshots", nil); code != http.StatusNotImplemented {
		t.Fatalf("create without storage: got %d, want 501", code)
	}
	if code, _ := doJSON(t, http.MethodGet, env.srv.URL+"/v1/workspaces/"+id+"/snapshots", nil); code != http.StatusNotImplemented {
		t.Fatalf("list without storage: got %d, want 501", code)
	}
	if code, _ := doJSON(t, http.MethodGet, env.srv.URL+"/v1/workspaces/"+id+"/snapshots/s", nil); code != http.StatusNotImplemented {
		t.Fatalf("get without storage: got %d, want 501", code)
	}
	if code, _ := doJSON(t, http.MethodPost, env.srv.URL+"/v1/workspaces/"+id+"/snapshots/s/restore", nil); code != http.StatusNotImplemented {
		t.Fatalf("restore without storage: got %d, want 501", code)
	}
	req, _ := http.NewRequest(http.MethodDelete, env.srv.URL+"/v1/workspaces/"+id+"/snapshots/s", nil)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("delete without storage: %v", err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusNotImplemented {
		t.Fatalf("delete without storage: got %d, want 501", resp.StatusCode)
	}
}
