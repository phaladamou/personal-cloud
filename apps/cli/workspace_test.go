package main

import (
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"github.com/phaladamou/personal-cloud/compute/node"
	"github.com/phaladamou/personal-cloud/packages/protocol"
	"github.com/phaladamou/personal-cloud/services/api"
	"github.com/phaladamou/personal-cloud/services/gateway"
	"github.com/phaladamou/personal-cloud/services/scheduler"
	controller "github.com/phaladamou/personal-cloud/services/workspace-controller"
)

// newPlacedCLIEnv wires the CLI harness with a node registry and a
// scheduler behind the gateway, like production traffic.
func newPlacedCLIEnv(t *testing.T) *cliEnv {
	t.Helper()
	store, err := controller.NewFileStore(filepath.Join(t.TempDir(), "ws.json"))
	if err != nil {
		t.Fatalf("NewFileStore: %v", err)
	}
	bus := protocol.NewEventBus()
	nodes := node.NewRegistry(bus)
	ctrl := controller.NewController(store, &controller.FakeBackend{}, bus,
		controller.WithScheduler(scheduler.New(nodes)))
	authSvc := newCLIAuth(t)
	gw := gateway.New(gateway.NewRegistry(),
		api.NewServer(ctrl, api.WithNodes(nodes), api.WithAuth(authSvc)),
		gateway.WithAuth(authSvc))
	srv := httptest.NewServer(gw)
	t.Cleanup(srv.Close)
	env := &cliEnv{vars: map[string]string{
		"PC_CONFIG_DIR": t.TempDir(),
		"PC_API":        srv.URL,
	}}
	env.getenv = func(k string) string { return env.vars[k] }
	return env
}

func mustRegisterNode(t *testing.T, getenv func(string) string, args ...string) string {
	t.Helper()
	full := append([]string{"node", "register"}, args...)
	code, out, stderr := runCLI(full, "", getenv)
	if code != 0 {
		t.Fatalf("%v: exit %d, stderr %q", full, code, stderr)
	}
	return strings.Split(out, "\t")[0]
}

func TestWorkspaceCreateStartPlaced(t *testing.T) {
	env := newPlacedCLIEnv(t)
	mustLogin(t, env.getenv)
	nodeID := mustRegisterNode(t, env.getenv, "gpu-1",
		"--cpu", "8", "--mem", "32768", "--disk", "500", "--gpus", "1")

	code, out, stderr := runCLI([]string{"workspace", "create", "dev",
		"--cpu", "2", "--mem", "1024", "--disk", "10", "--gpus", "1"}, "", env.getenv)
	if code != 0 {
		t.Fatalf("create: exit %d, stderr %q", code, stderr)
	}
	wsID := strings.Split(out, "\t")[0]
	if !strings.Contains(out, "CREATED") {
		t.Fatalf("create output: got %q", out)
	}

	code, out, stderr = runCLI([]string{"workspace", "start", wsID}, "", env.getenv)
	if code != 0 {
		t.Fatalf("start: exit %d, stderr %q", code, stderr)
	}
	if !strings.Contains(out, "RUNNING") || !strings.Contains(out, nodeID) {
		t.Fatalf("start output: got %q, want RUNNING on %s", out, nodeID)
	}

	code, out, _ = runCLI([]string{"workspace", "get", wsID}, "", env.getenv)
	if code != 0 || !strings.Contains(out, nodeID) {
		t.Fatalf("get: exit %d, out %q, want node %s", code, out, nodeID)
	}
}

func TestWorkspaceStartWithFlags(t *testing.T) {
	env := newPlacedCLIEnv(t)
	mustLogin(t, env.getenv)
	nodeID := mustRegisterNode(t, env.getenv, "cpu-1",
		"--cpu=4", "--mem=8192", "--disk=100", "--label=zone=east")

	code, out, stderr := runCLI([]string{"workspace", "create", "dev"}, "", env.getenv)
	if code != 0 {
		t.Fatalf("create: exit %d, stderr %q", code, stderr)
	}
	wsID := strings.Split(out, "\t")[0]
	if !strings.HasSuffix(strings.TrimSpace(out), "-") {
		t.Fatalf("unplaced create output: got %q, want trailing - node", out)
	}

	code, out, stderr = runCLI([]string{"workspace", "start", wsID,
		"--cpu=1", "--mem=64", "--disk=1", "--selector=zone=east"}, "", env.getenv)
	if code != 0 {
		t.Fatalf("start: exit %d, stderr %q", code, stderr)
	}
	if !strings.Contains(out, nodeID) {
		t.Fatalf("start output: got %q, want node %s", out, nodeID)
	}
}

func TestWorkspaceNoFitSurfaces(t *testing.T) {
	env := newPlacedCLIEnv(t)
	mustLogin(t, env.getenv)
	mustRegisterNode(t, env.getenv, "tiny", "--cpu", "1", "--mem", "64", "--disk", "1")

	code, out, stderr := runCLI([]string{"workspace", "create", "huge",
		"--cpu", "64", "--mem", "1048576", "--disk", "1000"}, "", env.getenv)
	if code != 0 {
		t.Fatalf("create: exit %d, stderr %q", code, stderr)
	}
	wsID := strings.Split(out, "\t")[0]
	code, _, stderr = runCLI([]string{"workspace", "start", wsID}, "", env.getenv)
	if code == 0 {
		t.Fatal("start oversized: exit 0, want nonzero")
	}
	if !strings.Contains(stderr, "no suitable node") {
		t.Fatalf("stderr: got %q, want a no-suitable-node explanation", stderr)
	}
}

func TestWorkspaceResourceFlagErrors(t *testing.T) {
	env := newPlacedCLIEnv(t)
	mustLogin(t, env.getenv)
	for _, args := range [][]string{
		{"workspace", "create", "dev", "--bogus"},
		{"workspace", "create", "dev", "--cpu"},
		{"workspace", "create", "dev", "--cpu=abc"},
		{"workspace", "create", "dev", "--selector=zone"},
		{"workspace", "start", "ws-1", "--bogus"},
		{"workspace", "start", "ws-1", "--mem"},
		{"workspace", "start", "ws-1", "--gpus=abc"},
		{"workspace", "start", "ws-1", "--selector", "no-equals"},
		{"workspace", "start"},
	} {
		if code, _, stderr := runCLI(args, "", env.getenv); code == 0 {
			t.Fatalf("%v: exit 0, want nonzero", args)
		} else if stderr == "" {
			t.Fatalf("%v: empty stderr", args)
		}
	}
}
