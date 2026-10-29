package main

import (
	"bytes"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/phaladamou/personal-cloud/packages/protocol"
	"github.com/phaladamou/personal-cloud/services/api"
	controller "github.com/phaladamou/personal-cloud/services/workspace-controller"
)

// cliEnv wires the CLI test harness: a real API over a temp store and
// fake runtime, with PC_CONFIG_DIR and PC_API pointed at temp values.
type cliEnv struct {
	getenv func(string) string
	vars   map[string]string
}

func newCLIEnv(t *testing.T) (*httptest.Server, *controller.FakeBackend, *cliEnv) {
	t.Helper()
	store, err := controller.NewFileStore(filepath.Join(t.TempDir(), "ws.json"))
	if err != nil {
		t.Fatalf("NewFileStore: %v", err)
	}
	backend := &controller.FakeBackend{}
	ctrl := controller.NewController(store, backend, protocol.NewEventBus())
	srv := httptest.NewServer(api.NewServer(ctrl))
	t.Cleanup(srv.Close)
	env := &cliEnv{vars: map[string]string{
		"PC_CONFIG_DIR": t.TempDir(),
		"PC_API":        srv.URL,
	}}
	env.getenv = func(k string) string { return env.vars[k] }
	return srv, backend, env
}

// runCLI drives run() with a string stdin and captures both outputs.
func runCLI(args []string, stdin string, getenv func(string) string) (int, string, string) {
	var stdout, stderr bytes.Buffer
	code := run(args, &stdout, &stderr, strings.NewReader(stdin), getenv)
	return code, stdout.String(), stderr.String()
}

func TestLoginDefaultToken(t *testing.T) {
	_, _, env := newCLIEnv(t)
	code, stdout, stderr := runCLI([]string{"login"}, "", env.getenv)
	if code != 0 {
		t.Fatalf("login: exit %d, stderr %q", code, stderr)
	}
	raw, err := os.ReadFile(filepath.Join(env.vars["PC_CONFIG_DIR"], "token"))
	if err != nil {
		t.Fatalf("read token: %v", err)
	}
	if strings.TrimSpace(string(raw)) != defaultToken {
		t.Fatalf("token file: got %q, want %q", strings.TrimSpace(string(raw)), defaultToken)
	}
	if !strings.Contains(stdout, "token saved to") {
		t.Fatalf("login output: got %q", stdout)
	}
}

func TestLoginCustomToken(t *testing.T) {
	_, _, env := newCLIEnv(t)
	for _, args := range [][]string{
		{"login", "--token", "abc123"},
		{"login", "--token=xyz"},
		{"login", "positional-token"},
	} {
		if code, _, stderr := runCLI(args, "", env.getenv); code != 0 {
			t.Fatalf("%v: exit %d, stderr %q", args, code, stderr)
		}
	}
	raw, _ := os.ReadFile(filepath.Join(env.vars["PC_CONFIG_DIR"], "token"))
	if strings.TrimSpace(string(raw)) != "positional-token" {
		t.Fatalf("token file: got %q", strings.TrimSpace(string(raw)))
	}
}

func TestWorkspaceRoundTrip(t *testing.T) {
	_, _, env := newCLIEnv(t)

	code, out, stderr := runCLI([]string{"workspace", "create", "dev"}, "", env.getenv)
	if code != 0 {
		t.Fatalf("create: exit %d, stderr %q", code, stderr)
	}
	id := strings.Split(out, "\t")[0]
	if id == "" || !strings.Contains(out, "CREATED") {
		t.Fatalf("create output: got %q", out)
	}

	code, out, stderr = runCLI([]string{"workspace", "list"}, "", env.getenv)
	if code != 0 || !strings.Contains(out, id) || !strings.Contains(out, "dev") {
		t.Fatalf("list: exit %d, out %q, stderr %q", code, out, stderr)
	}

	code, out, stderr = runCLI([]string{"workspace", "get", id}, "", env.getenv)
	if code != 0 || !strings.Contains(out, id) {
		t.Fatalf("get: exit %d, out %q, stderr %q", code, out, stderr)
	}

	for _, tc := range []struct{ action, want string }{
		{"start", "RUNNING"},
		{"stop", "STOPPED"},
	} {
		code, out, stderr = runCLI([]string{"workspace", tc.action, id}, "", env.getenv)
		if code != 0 || !strings.Contains(out, tc.want) {
			t.Fatalf("%s: exit %d, out %q, stderr %q", tc.action, code, out, stderr)
		}
	}

	code, out, stderr = runCLI([]string{"workspace", "delete", id}, "", env.getenv)
	if code != 0 || !strings.Contains(out, "deleted "+id) {
		t.Fatalf("delete: exit %d, out %q, stderr %q", code, out, stderr)
	}

	if code, _, _ := runCLI([]string{"workspace", "get", id}, "", env.getenv); code == 0 {
		t.Fatal("get after delete: exit 0, want nonzero")
	}
}

func TestWorkspaceCreateCustomUser(t *testing.T) {
	_, _, env := newCLIEnv(t)
	code, _, stderr := runCLI([]string{"workspace", "create", "dev", "--user", "u-7"}, "", env.getenv)
	if code != 0 {
		t.Fatalf("create --user: exit %d, stderr %q", code, stderr)
	}
}

func TestUsageAndArgErrors(t *testing.T) {
	_, _, env := newCLIEnv(t)

	code, out, _ := runCLI([]string{"help"}, "", env.getenv)
	if code != 0 || !strings.Contains(out, "Usage:") {
		t.Fatalf("help: exit %d, out %q", code, out)
	}
	if code, _, _ := runCLI(nil, "", env.getenv); code == 0 {
		t.Fatal("no args: exit 0, want nonzero")
	}
	for _, args := range [][]string{
		{"bogus"},
		{"workspace"},
		{"workspace", "frobnicate"},
		{"workspace", "create"},
		{"workspace", "create", "a", "b"},
		{"workspace", "get"},
		{"workspace", "start", "id", "extra"},
		{"workspace", "list", "extra"},
		{"workspace", "get", "no-such-id"},
		{"workspace", "delete", "no-such-id"},
		{"login", "--bogus"},
		{"login", "a", "b"},
	} {
		if code, _, stderr := runCLI(args, "", env.getenv); code == 0 {
			t.Fatalf("%v: exit 0, want nonzero", args)
		} else if stderr == "" {
			t.Fatalf("%v: empty stderr", args)
		}
	}
}

func TestAPIFlagOverridesEnv(t *testing.T) {
	_, _, env := newCLIEnv(t)
	// Unreachable --api must fail even though PC_API is healthy.
	code, _, stderr := runCLI([]string{"--api", "http://127.0.0.1:1", "workspace", "list"}, "", env.getenv)
	if code == 0 {
		t.Fatal("unreachable --api: exit 0, want nonzero")
	}
	if !strings.Contains(stderr, "pc:") {
		t.Fatalf("stderr: got %q, want pc: prefix", stderr)
	}
}

func TestTokenSentAsBearer(t *testing.T) {
	srv, _, env := newCLIEnv(t)
	_ = srv
	if code, _, stderr := runCLI([]string{"login", "--token", "t-1"}, "", env.getenv); code != 0 {
		t.Fatalf("login: exit %d, stderr %q", code, stderr)
	}
	// A subsequent call must still succeed with the token file present
	// (the Phase-1 API ignores auth, but the header must not break it).
	if code, _, stderr := runCLI([]string{"workspace", "list"}, "", env.getenv); code != 0 {
		t.Fatalf("list with token: exit %d, stderr %q", code, stderr)
	}
}
