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
	controller "github.com/phaladamou/personal-cloud/services/workspace-controller"
)

// newNodeCLIEnv wires the CLI harness with a node registry behind the
// gateway, like production traffic.
func newNodeCLIEnv(t *testing.T) *cliEnv {
	t.Helper()
	store, err := controller.NewFileStore(filepath.Join(t.TempDir(), "ws.json"))
	if err != nil {
		t.Fatalf("NewFileStore: %v", err)
	}
	bus := protocol.NewEventBus()
	ctrl := controller.NewController(store, &controller.FakeBackend{}, bus)
	authSvc := newCLIAuth(t)
	gw := gateway.New(gateway.NewRegistry(),
		api.NewServer(ctrl, api.WithNodes(node.NewRegistry(bus)), api.WithAuth(authSvc)),
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

func TestNodeRoundTrip(t *testing.T) {
	env := newNodeCLIEnv(t)
	mustLogin(t, env.getenv)

	code, out, stderr := runCLI([]string{"node", "register", "gpu-1",
		"--cpu", "8", "--mem", "32768", "--disk", "500", "--gpus", "1",
		"--label", "zone=eu"}, "", env.getenv)
	if code != 0 {
		t.Fatalf("register: exit %d, stderr %q", code, stderr)
	}
	id := strings.Split(out, "\t")[0]
	if id == "" || !strings.Contains(out, "ONLINE") || !strings.Contains(out, "gpu-1") {
		t.Fatalf("register output: got %q", out)
	}

	code, out, stderr = runCLI([]string{"node", "list"}, "", env.getenv)
	if code != 0 || !strings.Contains(out, id) || !strings.Contains(out, "gpu-1") {
		t.Fatalf("list: exit %d, out %q, stderr %q", code, out, stderr)
	}

	code, out, stderr = runCLI([]string{"node", "get", id}, "", env.getenv)
	if code != 0 || !strings.Contains(out, id) || !strings.Contains(out, "ONLINE") {
		t.Fatalf("get: exit %d, out %q, stderr %q", code, out, stderr)
	}

	code, out, stderr = runCLI([]string{"node", "deregister", id}, "", env.getenv)
	if code != 0 || !strings.Contains(out, "deregistered "+id) {
		t.Fatalf("deregister: exit %d, out %q, stderr %q", code, out, stderr)
	}

	if code, _, _ := runCLI([]string{"node", "get", id}, "", env.getenv); code == 0 {
		t.Fatal("get after deregister: exit 0, want nonzero")
	}
}

func TestNodeRegisterEqualsForm(t *testing.T) {
	env := newNodeCLIEnv(t)
	mustLogin(t, env.getenv)

	code, out, stderr := runCLI([]string{"node", "register", "cpu-1",
		"--cpu=2", "--mem=4096", "--disk=50", "--label=zone=us"}, "", env.getenv)
	if code != 0 {
		t.Fatalf("register: exit %d, stderr %q", code, stderr)
	}
	if !strings.Contains(out, "cpu-1") || !strings.Contains(out, "ONLINE") {
		t.Fatalf("register output: got %q", out)
	}
}

func TestNodeArgErrors(t *testing.T) {
	env := newNodeCLIEnv(t)
	mustLogin(t, env.getenv) // Server-touching cases must fail for their own reason, not 401.

	for _, args := range [][]string{
		{"node"},
		{"node", "frobnicate"},
		{"node", "list", "extra"},
		{"node", "get"},
		{"node", "get", "id", "extra"},
		{"node", "get", "no-such-id"},
		{"node", "deregister"},
		{"node", "deregister", "no-such-id"},
		{"node", "register"},
		{"node", "register", "a", "b", "--cpu", "1", "--mem", "1", "--disk", "1"},
		{"node", "register", "a"}, // Missing capacity flags.
		{"node", "register", "a", "--cpu", "1", "--mem", "1"},
		{"node", "register", "a", "--cpu", "1", "--mem", "1", "--disk", "1", "--bogus"},
		{"node", "register", "a", "--cpu", "NaN", "--mem", "1", "--disk", "1"},
		{"node", "register", "a", "--cpu", "1", "--mem", "1", "--disk", "1", "--label", "noequals"},
		{"node", "register", "a", "--cpu", "0", "--mem", "1", "--disk", "1"}, // Server 400.
	} {
		if code, _, stderr := runCLI(args, "", env.getenv); code == 0 {
			t.Fatalf("%v: exit 0, want nonzero", args)
		} else if stderr == "" {
			t.Fatalf("%v: empty stderr", args)
		}
	}
}
