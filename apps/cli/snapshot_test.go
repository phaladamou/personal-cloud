package main

import (
	"context"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/phaladamou/personal-cloud/packages/protocol"
	"github.com/phaladamou/personal-cloud/services/api"
	controller "github.com/phaladamou/personal-cloud/services/workspace-controller"
	"github.com/phaladamou/personal-cloud/storage/snapshots"
	"github.com/phaladamou/personal-cloud/storage/volumes"
)

// snapshotCLIEnv wires the CLI harness over dir-backed storage so the
// round trip exercises real snapshot files.
type snapshotCLIEnv struct {
	ctrl *controller.Controller
	vols *volumes.LocalManager
	env  *cliEnv
}

func newSnapshotCLIEnv(t *testing.T) *snapshotCLIEnv {
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
	srv := httptest.NewServer(api.NewServer(ctrl, api.WithAuth(newCLIAuth(t))))
	t.Cleanup(srv.Close)
	env := &cliEnv{vars: map[string]string{
		"PC_CONFIG_DIR": t.TempDir(),
		"PC_API":        srv.URL,
	}}
	env.getenv = func(k string) string { return env.vars[k] }
	return &snapshotCLIEnv{ctrl: ctrl, vols: vols, env: env}
}

func TestSnapshotRoundTrip(t *testing.T) {
	ctx := context.Background()
	env := newSnapshotCLIEnv(t)
	mustLogin(t, env.env.getenv)

	code, out, stderr := runCLI([]string{"workspace", "create", "dev"}, "", env.env.getenv)
	if code != 0 {
		t.Fatalf("workspace create: exit %d, stderr %q", code, stderr)
	}
	id := strings.Split(out, "\t")[0]
	if id == "" {
		t.Fatalf("workspace create output: got %q", out)
	}

	// Seed volume content, then snapshot it through the CLI.
	vol, err := env.ctrl.CreateVolume(ctx, id, "default", 10, "/data")
	if err != nil {
		t.Fatalf("CreateVolume: %v", err)
	}
	dataPath := filepath.Join(env.vols.DataPath(id, vol.ID), "hello.txt")
	if err := os.WriteFile(dataPath, []byte("hello"), 0o644); err != nil {
		t.Fatalf("write volume data: %v", err)
	}

	code, out, stderr = runCLI([]string{"snapshot", "create", id, "--name", "base"}, "", env.env.getenv)
	if code != 0 {
		t.Fatalf("snapshot create: exit %d, stderr %q", code, stderr)
	}
	sid := strings.Split(out, "\t")[0]
	if sid == "" || !strings.Contains(out, "base") {
		t.Fatalf("snapshot create output: got %q", out)
	}

	code, out, stderr = runCLI([]string{"snapshot", "create", id, "--volume=" + vol.ID, "--name=again"}, "", env.env.getenv)
	if code != 0 {
		t.Fatalf("snapshot create explicit volume: exit %d, stderr %q", code, stderr)
	}
	if got := strings.Split(out, "\t")[0]; got != sid {
		t.Fatalf("snapshot create unchanged: got %q, want dedupe to %q", got, sid)
	}

	code, out, stderr = runCLI([]string{"snapshot", "list", id}, "", env.env.getenv)
	if code != 0 || !strings.Contains(out, sid) || !strings.Contains(out, "base") {
		t.Fatalf("snapshot list: exit %d, out %q, stderr %q", code, out, stderr)
	}

	// Delete the file, restore through the CLI, verify the content.
	if err := os.Remove(dataPath); err != nil {
		t.Fatalf("remove file: %v", err)
	}
	code, out, stderr = runCLI([]string{"snapshot", "restore", id, sid}, "", env.env.getenv)
	if code != 0 || !strings.Contains(out, "restored "+sid) {
		t.Fatalf("snapshot restore: exit %d, out %q, stderr %q", code, out, stderr)
	}
	raw, err := os.ReadFile(dataPath)
	if err != nil || string(raw) != "hello" {
		t.Fatalf("file after restore: got %q, %v, want %q", raw, err, "hello")
	}

	code, out, stderr = runCLI([]string{"snapshot", "delete", id, sid}, "", env.env.getenv)
	if code != 0 || !strings.Contains(out, "deleted "+sid) {
		t.Fatalf("snapshot delete: exit %d, out %q, stderr %q", code, out, stderr)
	}
	code, out, stderr = runCLI([]string{"snapshot", "list", id}, "", env.env.getenv)
	if code != 0 || strings.Contains(out, sid) {
		t.Fatalf("snapshot list after delete: exit %d, out %q, stderr %q", code, out, stderr)
	}
	if code, _, _ := runCLI([]string{"snapshot", "restore", id, sid}, "", env.env.getenv); code == 0 {
		t.Fatal("snapshot restore after delete: got exit 0, want failure")
	}
}

func TestSnapshotUsageErrors(t *testing.T) {
	_, _, env := newCLIEnv(t)

	for _, args := range [][]string{
		{"snapshot"},
		{"snapshot", "bogus"},
		{"snapshot", "create"},
		{"snapshot", "create", "ws-1", "extra", "--name=x"},
		{"snapshot", "create", "ws-1", "--bogus"},
		{"snapshot", "list"},
		{"snapshot", "list", "ws-1", "extra"},
		{"snapshot", "restore", "ws-1"},
		{"snapshot", "restore", "ws-1", "s", "extra"},
		{"snapshot", "delete", "ws-1"},
	} {
		if code, _, _ := runCLI(args, "", env.getenv); code == 0 {
			t.Fatalf("%v: got exit 0, want failure", args)
		}
	}
}

func TestSnapshotMissingWorkspace(t *testing.T) {
	env := newSnapshotCLIEnv(t)
	mustLogin(t, env.env.getenv)

	if code, _, _ := runCLI([]string{"snapshot", "create", "nope"}, "", env.env.getenv); code == 0 {
		t.Fatal("snapshot create on missing workspace: got exit 0, want failure")
	}
	if code, _, _ := runCLI([]string{"snapshot", "list", "nope"}, "", env.env.getenv); code == 0 {
		t.Fatal("snapshot list on missing workspace: got exit 0, want failure")
	}
	if code, _, _ := runCLI([]string{"snapshot", "restore", "nope", "s"}, "", env.env.getenv); code == 0 {
		t.Fatal("snapshot restore on missing workspace: got exit 0, want failure")
	}
	if code, _, _ := runCLI([]string{"snapshot", "delete", "nope", "s"}, "", env.env.getenv); code == 0 {
		t.Fatal("snapshot delete on missing workspace: got exit 0, want failure")
	}
}
