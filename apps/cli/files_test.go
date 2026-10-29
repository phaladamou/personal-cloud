package main

import (
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/phaladamou/personal-cloud/packages/protocol"
	"github.com/phaladamou/personal-cloud/services/api"
	controller "github.com/phaladamou/personal-cloud/services/workspace-controller"
)

// filesCLIEnv wires the CLI harness over a dir-backed filesystem
// provider so the round trip exercises real workspace files.
type filesCLIEnv struct {
	env *cliEnv
}

func newFilesCLIEnv(t *testing.T) *filesCLIEnv {
	t.Helper()
	store, err := controller.NewFileStore(filepath.Join(t.TempDir(), "ws.json"))
	if err != nil {
		t.Fatalf("NewFileStore: %v", err)
	}
	ctrl := controller.NewController(store, &controller.FakeBackend{}, protocol.NewEventBus(),
		controller.WithFilesystems(controller.DirFSProvider{Root: t.TempDir()}))
	srv := httptest.NewServer(api.NewServer(ctrl))
	t.Cleanup(srv.Close)
	env := &cliEnv{vars: map[string]string{
		"PC_CONFIG_DIR": t.TempDir(),
		"PC_API":        srv.URL,
	}}
	env.getenv = func(k string) string { return env.vars[k] }
	return &filesCLIEnv{env: env}
}

func TestFilesRoundTrip(t *testing.T) {
	env := newFilesCLIEnv(t)

	code, out, stderr := runCLI([]string{"workspace", "create", "dev"}, "", env.env.getenv)
	if code != 0 {
		t.Fatalf("workspace create: exit %d, stderr %q", code, stderr)
	}
	id := strings.Split(out, "\t")[0]

	// Upload from stdin, then download through cat.
	code, out, stderr = runCLI([]string{"files", "put", id, "a/b.txt"}, "hello-stdin", env.env.getenv)
	if code != 0 || !strings.Contains(out, "wrote a/b.txt") {
		t.Fatalf("files put stdin: exit %d, out %q, stderr %q", code, out, stderr)
	}
	code, out, stderr = runCLI([]string{"files", "cat", id, "a/b.txt"}, "", env.env.getenv)
	if code != 0 || out != "hello-stdin" {
		t.Fatalf("files cat: exit %d, out %q, stderr %q", code, out, stderr)
	}

	// Upload from a local file.
	local := filepath.Join(t.TempDir(), "up.txt")
	if err := os.WriteFile(local, []byte("hello-file"), 0o644); err != nil {
		t.Fatalf("write local file: %v", err)
	}
	code, out, stderr = runCLI([]string{"files", "put", id, "up.txt", local}, "", env.env.getenv)
	if code != 0 {
		t.Fatalf("files put file: exit %d, stderr %q", code, stderr)
	}
	code, out, stderr = runCLI([]string{"files", "cat", id, "up.txt"}, "", env.env.getenv)
	if code != 0 || out != "hello-file" {
		t.Fatalf("files cat: exit %d, out %q, stderr %q", code, out, stderr)
	}

	// List the root and a directory.
	code, out, stderr = runCLI([]string{"files", "ls", id}, "", env.env.getenv)
	if code != 0 || !strings.Contains(out, "a\tdir") || !strings.Contains(out, "up.txt\tfile") {
		t.Fatalf("files ls: exit %d, out %q, stderr %q", code, out, stderr)
	}
	code, out, stderr = runCLI([]string{"files", "ls", id, "a"}, "", env.env.getenv)
	if code != 0 || !strings.Contains(out, "a/b.txt") {
		t.Fatalf("files ls dir: exit %d, out %q, stderr %q", code, out, stderr)
	}

	// Make and remove a directory.
	code, out, stderr = runCLI([]string{"files", "mkdir", id, "newdir"}, "", env.env.getenv)
	if code != 0 || !strings.Contains(out, "created newdir") {
		t.Fatalf("files mkdir: exit %d, out %q, stderr %q", code, out, stderr)
	}
	code, out, stderr = runCLI([]string{"files", "rm", id, "newdir"}, "", env.env.getenv)
	if code != 0 || !strings.Contains(out, "deleted newdir") {
		t.Fatalf("files rm: exit %d, out %q, stderr %q", code, out, stderr)
	}
	code, out, stderr = runCLI([]string{"files", "rm", id, "a/b.txt"}, "", env.env.getenv)
	if code != 0 {
		t.Fatalf("files rm file: exit %d, stderr %q", code, stderr)
	}
	if code, _, _ := runCLI([]string{"files", "cat", id, "a/b.txt"}, "", env.env.getenv); code == 0 {
		t.Fatal("files cat after rm: got exit 0, want failure")
	}
}

func TestFilesUsageErrors(t *testing.T) {
	_, _, env := newCLIEnv(t)

	for _, args := range [][]string{
		{"files"},
		{"files", "bogus"},
		{"files", "ls"},
		{"files", "ls", "ws-1", "a", "extra"},
		{"files", "cat", "ws-1"},
		{"files", "cat", "ws-1", "f", "extra"},
		{"files", "put", "ws-1"},
		{"files", "put", "ws-1", "f", "local", "extra"},
		{"files", "mkdir", "ws-1"},
		{"files", "rm", "ws-1"},
		{"files", "put", "ws-1", "f", filepath.Join("no-such-dir", "f")},
	} {
		if code, _, _ := runCLI(args, "", env.getenv); code == 0 {
			t.Fatalf("%v: got exit 0, want failure", args)
		}
	}
}

func TestFilesMissingWorkspace(t *testing.T) {
	env := newFilesCLIEnv(t)

	if code, _, _ := runCLI([]string{"files", "ls", "nope"}, "", env.env.getenv); code == 0 {
		t.Fatal("files ls on missing workspace: got exit 0, want failure")
	}
	if code, _, _ := runCLI([]string{"files", "cat", "nope", "f"}, "", env.env.getenv); code == 0 {
		t.Fatal("files cat on missing workspace: got exit 0, want failure")
	}
	if code, _, _ := runCLI([]string{"files", "put", "nope", "f"}, "x", env.env.getenv); code == 0 {
		t.Fatal("files put on missing workspace: got exit 0, want failure")
	}
	if code, _, _ := runCLI([]string{"files", "mkdir", "nope", "d"}, "", env.env.getenv); code == 0 {
		t.Fatal("files mkdir on missing workspace: got exit 0, want failure")
	}
	if code, _, _ := runCLI([]string{"files", "rm", "nope", "f"}, "", env.env.getenv); code == 0 {
		t.Fatal("files rm on missing workspace: got exit 0, want failure")
	}
	if code, _, _ := runCLI([]string{"files", "ls", "nope", "../x"}, "", env.env.getenv); code == 0 {
		t.Fatal("files ls traversal: got exit 0, want failure")
	}
}
