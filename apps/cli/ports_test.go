package main

import (
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/phaladamou/personal-cloud/packages/protocol"
	"github.com/phaladamou/personal-cloud/services/api"
	"github.com/phaladamou/personal-cloud/services/auth"
	"github.com/phaladamou/personal-cloud/services/gateway"
	controller "github.com/phaladamou/personal-cloud/services/workspace-controller"
)

// createWorkspaceID creates a workspace through the CLI and returns its id.
func createWorkspaceID(t *testing.T, env *cliEnv, name string) string {
	t.Helper()
	code, out, stderr := runCLI([]string{"workspace", "create", name}, "", env.getenv)
	if code != 0 {
		t.Fatalf("create: exit %d, stderr %q", code, stderr)
	}
	id := strings.Split(out, "\t")[0]
	if id == "" {
		t.Fatalf("create output: got %q, want an id row", out)
	}
	return id
}

func TestPortsRoundTrip(t *testing.T) {
	_, _, env := newCLIEnv(t)
	mustLogin(t, env.getenv)
	id := createWorkspaceID(t, env, "dev")

	code, out, stderr := runCLI([]string{"ports", "register", id, "3000", "127.0.0.1:4000"}, "", env.getenv)
	if code != 0 {
		t.Fatalf("register: exit %d, stderr %q", code, stderr)
	}
	if !strings.Contains(out, "3000") || !strings.Contains(out, "127.0.0.1:4000") {
		t.Fatalf("register output: got %q", out)
	}

	code, out, stderr = runCLI([]string{"ports", "register", id, "3001", "127.0.0.1:4001", "--protocol", "udp"}, "", env.getenv)
	if code != 0 {
		t.Fatalf("register udp: exit %d, stderr %q", code, stderr)
	}

	code, out, stderr = runCLI([]string{"ports", "list", id}, "", env.getenv)
	if code != 0 {
		t.Fatalf("list: exit %d, stderr %q", code, stderr)
	}
	if !strings.Contains(out, "PORT") || !strings.Contains(out, "3000") || !strings.Contains(out, "3001") {
		t.Fatalf("list output: got %q", out)
	}
	if strings.Index(out, "3000") > strings.Index(out, "3001") {
		t.Fatalf("list order: got %q, want 3000 first", out)
	}

	// Duplicate registration fails.
	if code, _, stderr := runCLI([]string{"ports", "register", id, "3000", "127.0.0.1:4000"}, "", env.getenv); code == 0 {
		t.Fatal("register duplicate: exit 0, want nonzero")
	} else if stderr == "" {
		t.Fatal("register duplicate: empty stderr")
	}

	code, out, stderr = runCLI([]string{"ports", "remove", id, "3000"}, "", env.getenv)
	if code != 0 || !strings.Contains(out, "removed port 3000") {
		t.Fatalf("remove: exit %d, out %q, stderr %q", code, out, stderr)
	}

	code, out, stderr = runCLI([]string{"ports", "list", id}, "", env.getenv)
	if code != 0 || strings.Contains(out, "3000\t") {
		t.Fatalf("list after remove: exit %d, out %q, stderr %q", code, out, stderr)
	}
	if !strings.Contains(out, "3001") {
		t.Fatalf("list after remove: got %q, want 3001 left", out)
	}

	if code, _, stderr := runCLI([]string{"ports", "remove", id, "3000"}, "", env.getenv); code == 0 {
		t.Fatal("remove twice: exit 0, want nonzero")
	} else if stderr == "" {
		t.Fatal("remove twice: empty stderr")
	}
}

func TestPortsArgErrors(t *testing.T) {
	_, _, env := newCLIEnv(t)
	mustLogin(t, env.getenv) // Server-touching cases must fail for their own reason, not 401.
	for _, args := range [][]string{
		{"ports"},
		{"ports", "frobnicate"},
		{"ports", "register"},
		{"ports", "register", "id"},
		{"ports", "register", "id", "3000"},
		{"ports", "register", "id", "3000", "t", "extra"},
		{"ports", "register", "id", "abc", "127.0.0.1:4000"},
		{"ports", "register", "id", "3000", "127.0.0.1:4000", "--bogus"},
		{"ports", "list"},
		{"ports", "list", "id", "extra"},
		{"ports", "remove"},
		{"ports", "remove", "id"},
		{"ports", "remove", "id", "abc"},
		{"ports", "remove", "id", "3000", "extra"},
	} {
		if code, _, stderr := runCLI(args, "", env.getenv); code == 0 {
			t.Fatalf("%v: exit 0, want nonzero", args)
		} else if stderr == "" {
			t.Fatalf("%v: empty stderr", args)
		}
	}
}

func TestPortsRequireLogin(t *testing.T) {
	_, _, env := newCLIEnv(t)
	code, _, stderr := runCLI([]string{"ports", "list", "ws-1"}, "", env.getenv)
	if code == 0 {
		t.Fatal("ports list without login: exit 0, want nonzero")
	}
	if !strings.Contains(stderr, `run "pc login"`) {
		t.Fatalf("stderr: got %q, want a login hint", stderr)
	}
}

// newRemoteServer starts a second, independent gateway+API server (its
// own auth secret and store) standing in for a remote cloud.
func newRemoteServer(t *testing.T) string {
	t.Helper()
	store, err := controller.NewFileStore(filepath.Join(t.TempDir(), "ws.json"))
	if err != nil {
		t.Fatalf("NewFileStore: %v", err)
	}
	ctrl := controller.NewController(store, &controller.FakeBackend{}, protocol.NewEventBus())
	authSvc, err := auth.NewService(auth.NewFakeStore(), []byte("remote-test-secret-0123456789-0123"))
	if err != nil {
		t.Fatalf("NewService remote auth: %v", err)
	}
	gw := gateway.New(gateway.NewRegistry(), api.NewServer(ctrl, api.WithAuth(authSvc)), gateway.WithAuth(authSvc))
	srv := httptest.NewServer(gw)
	t.Cleanup(srv.Close)
	return srv.URL
}

// TestLoginRemoteAPI proves "pc login --api URL" targets a remote
// server instead of PC_API, and names the device in its output.
func TestLoginRemoteAPI(t *testing.T) {
	_, _, env := newCLIEnv(t)
	remote := newRemoteServer(t)

	code, stdout, stderr := runCLI([]string{"--api", remote, "login", "laptop"}, "", env.getenv)
	if code != 0 {
		t.Fatalf("login --api: exit %d, stderr %q", code, stderr)
	}
	if !strings.Contains(stdout, "laptop") || !strings.Contains(stdout, "logged in") {
		t.Fatalf("login --api output: got %q, want the device name", stdout)
	}
	raw, err := os.ReadFile(filepath.Join(env.vars["PC_CONFIG_DIR"], "token"))
	if err != nil {
		t.Fatalf("read token: %v", err)
	}
	if !strings.HasPrefix(strings.TrimSpace(string(raw)), "pc1.") {
		t.Fatalf("token file: got %q, want a server-issued token", strings.TrimSpace(string(raw)))
	}

	// The saved token authenticates the remote: create there, list there.
	if code, _, stderr := runCLI([]string{"--api", remote, "workspace", "create", "remote-dev"}, "", env.getenv); code != 0 {
		t.Fatalf("remote create: exit %d, stderr %q", code, stderr)
	}
	code, out, stderr := runCLI([]string{"--api", remote, "workspace", "list"}, "", env.getenv)
	if code != 0 || !strings.Contains(out, "remote-dev") {
		t.Fatalf("remote list: exit %d, out %q, stderr %q", code, out, stderr)
	}

	// The same token means nothing to the PC_API server (its own
	// secret): --api really picked the remote, not the env default.
	code, _, stderr = runCLI([]string{"workspace", "list"}, "", env.getenv)
	if code == 0 {
		t.Fatal("local list with remote token: exit 0, want 401 hint")
	}
	if !strings.Contains(stderr, `run "pc login"`) {
		t.Fatalf("local list stderr: got %q, want a login hint", stderr)
	}
}
