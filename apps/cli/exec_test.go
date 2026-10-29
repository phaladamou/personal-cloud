package main

import (
	"context"
	"fmt"
	"io"
	"strings"
	"testing"

	"github.com/phaladamou/personal-cloud/runtime/container"
)

// createAndStart creates a workspace through the CLI and starts it,
// returning its id.
func createAndStart(t *testing.T, env *cliEnv) string {
	t.Helper()
	if code, _, stderr := runCLI([]string{"workspace", "create", "dev"}, "", env.getenv); code != 0 {
		t.Fatalf("create: exit %d, stderr %q", code, stderr)
	}
	code, out, stderr := runCLI([]string{"workspace", "list"}, "", env.getenv)
	if code != 0 {
		t.Fatalf("list: exit %d, stderr %q", code, stderr)
	}
	lines := strings.Split(strings.TrimSpace(out), "\n")
	id := strings.Split(lines[len(lines)-1], "\t")[0]
	if code, _, stderr := runCLI([]string{"workspace", "start", id}, "", env.getenv); code != 0 {
		t.Fatalf("start: exit %d, stderr %q", code, stderr)
	}
	return id
}

func TestExecEcho(t *testing.T) {
	_, backend, env := newCLIEnv(t)
	backend.ExecFunc = func(ctx context.Context, id string, opts container.ExecOptions) error {
		in := make([]byte, 4)
		if _, err := io.ReadFull(opts.Stdin, in); err != nil {
			return err
		}
		_, err := opts.Stdout.Write(append([]byte("got:"), in...))
		return err
	}
	id := createAndStart(t, env)

	code, stdout, stderr := runCLI([]string{"exec", id, "--", "cat"}, "ping", env.getenv)
	if code != 0 {
		t.Fatalf("exec: exit %d, stderr %q", code, stderr)
	}
	if stdout != "got:ping" {
		t.Fatalf("exec stdout: got %q, want %q", stdout, "got:ping")
	}
	if len(backend.Execs) != 1 || len(backend.Execs[0].Cmd) != 1 || backend.Execs[0].Cmd[0] != "cat" {
		t.Fatalf("recorded execs: %+v, want one [cat]", backend.Execs)
	}
}

func TestExecFailure(t *testing.T) {
	_, backend, env := newCLIEnv(t)
	backend.ExecFunc = func(ctx context.Context, id string, opts container.ExecOptions) error {
		return fmt.Errorf("kaboom")
	}
	id := createAndStart(t, env)

	code, _, stderr := runCLI([]string{"exec", id, "--", "false"}, "", env.getenv)
	if code == 0 {
		t.Fatal("exec failing command: exit 0, want nonzero")
	}
	if !strings.Contains(stderr, "error: kaboom") {
		t.Fatalf("exec stderr: got %q, want error: kaboom", stderr)
	}
}

func TestExecArgErrors(t *testing.T) {
	_, _, env := newCLIEnv(t)
	for _, args := range [][]string{
		{"exec"},
		{"exec", "some-id"},           // missing -- and command
		{"exec", "some-id", "extra"},  // missing -- separator
		{"exec", "a", "b", "--", "c"}, // two ids
	} {
		if code, _, stderr := runCLI(args, "", env.getenv); code == 0 {
			t.Fatalf("%v: exit 0, want nonzero", args)
		} else if !strings.Contains(stderr, "pc:") {
			t.Fatalf("%v: stderr %q lacks pc: prefix", args, stderr)
		}
	}
}

func TestExecNotRunning(t *testing.T) {
	_, _, env := newCLIEnv(t)
	code, _, _ := runCLI([]string{"workspace", "create", "dev"}, "", env.getenv)
	if code != 0 {
		t.Fatal("create failed")
	}
	_, out, _ := runCLI([]string{"workspace", "list"}, "", env.getenv)
	lines := strings.Split(strings.TrimSpace(out), "\n")
	id := strings.Split(lines[len(lines)-1], "\t")[0]

	// CREATED workspace: the server rejects the upgrade (409), so the
	// handshake must fail cleanly instead of hanging.
	if code, _, stderr := runCLI([]string{"exec", id, "--", "cat"}, "", env.getenv); code == 0 {
		t.Fatal("exec on CREATED: exit 0, want nonzero")
	} else if stderr == "" {
		t.Fatal("exec on CREATED: empty stderr")
	}
}
