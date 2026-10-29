package main

import (
	"strings"
	"testing"

	"github.com/phaladamou/personal-cloud/packages/types"
	"github.com/phaladamou/personal-cloud/runtime/process"
)

func TestPsLogsRoundTrip(t *testing.T) {
	_, backend, env := newCLIEnv(t)
	mustLogin(t, env.getenv)

	code, out, stderr := runCLI([]string{"workspace", "create", "dev"}, "", env.getenv)
	if code != 0 {
		t.Fatalf("workspace create: exit %d, stderr %q", code, stderr)
	}
	id := strings.Split(out, "\t")[0]

	// No container yet: empty process list.
	code, out, stderr = runCLI([]string{"ps", id}, "", env.getenv)
	if code != 0 || !strings.Contains(out, "ID\tCOMMAND") {
		t.Fatalf("ps empty: exit %d, out %q, stderr %q", code, out, stderr)
	}

	code, _, stderr = runCLI([]string{"workspace", "start", id}, "", env.getenv)
	if code != 0 {
		t.Fatalf("workspace start: exit %d, stderr %q", code, stderr)
	}
	backend.ProcessesList = []process.Info{
		{ID: "main", Command: "sleep", Args: []string{"infinity"}, PID: 100, State: types.ProcessRunning},
		{ID: "exec-1", Command: "echo", Args: []string{"hi"}, PID: 101, State: types.ProcessExited},
	}
	backend.LogsOutput = "hi\n"

	code, out, stderr = runCLI([]string{"ps", id}, "", env.getenv)
	if code != 0 || !strings.Contains(out, "main\tsleep infinity\tRUNNING\t100") ||
		!strings.Contains(out, "exec-1\techo hi\tEXITED\t101") {
		t.Fatalf("ps: exit %d, out %q, stderr %q", code, out, stderr)
	}

	code, out, stderr = runCLI([]string{"logs", id, "main"}, "", env.getenv)
	if code != 0 || out != "hi\n" {
		t.Fatalf("logs: exit %d, out %q, stderr %q", code, out, stderr)
	}
	code, out, stderr = runCLI([]string{"logs", id, "main", "--follow"}, "", env.getenv)
	if code != 0 || out != "hi\n" {
		t.Fatalf("logs --follow: exit %d, out %q, stderr %q", code, out, stderr)
	}
	if len(backend.LogCalls) != 2 || backend.LogCalls[0].Follow || !backend.LogCalls[1].Follow {
		t.Fatalf("LogCalls = %+v, want follow flags [false true]", backend.LogCalls)
	}

	if code, _, _ := runCLI([]string{"logs", id, "nope"}, "", env.getenv); code == 0 {
		t.Fatal("logs unknown pid: got exit 0, want failure")
	}
}

func TestPsLogsUsageErrors(t *testing.T) {
	_, _, env := newCLIEnv(t)
	mustLogin(t, env.getenv) // Well-shaped cases must reach the server (404), not 401.

	for _, args := range [][]string{
		{"ps"},
		{"ps", "ws-1", "extra"},
		{"ps", "nope"},
		{"logs"},
		{"logs", "ws-1"},
		{"logs", "ws-1", "main", "extra"},
		{"logs", "ws-1", "main", "--bogus"},
		{"logs", "nope", "main"},
	} {
		if code, _, _ := runCLI(args, "", env.getenv); code == 0 {
			t.Fatalf("%v: got exit 0, want failure", args)
		}
	}
}
