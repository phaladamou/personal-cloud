package controller

import (
	"bytes"
	"context"
	"errors"
	"io"
	"strings"
	"testing"
	"time"

	"github.com/phaladamou/personal-cloud/packages/protocol"
	"github.com/phaladamou/personal-cloud/packages/types"
	"github.com/phaladamou/personal-cloud/runtime/container"
	"github.com/phaladamou/personal-cloud/runtime/process"
)

// procsSetup wires a controller with a scriptable backend and one
// RUNNING workspace.
func procsSetup(t *testing.T) (*Controller, *FakeBackend, *protocol.EventBus, types.Workspace) {
	t.Helper()
	c, backend, bus := testSetup(t)
	ws := mustCreate(t, c)
	if _, err := c.Start(context.Background(), ws.ID); err != nil {
		t.Fatalf("Start: %v", err)
	}
	return c, backend, bus, ws
}

func TestListProcesses(t *testing.T) {
	ctx := context.Background()
	c, backend, _, ws := procsSetup(t)

	// A fresh container lists as empty.
	list, err := c.ListProcesses(ctx, ws.ID)
	if err != nil {
		t.Fatalf("ListProcesses: %v", err)
	}
	if len(list) != 0 {
		t.Fatalf("ListProcesses = %+v, want empty", list)
	}

	started := time.Now().UTC().Truncate(time.Millisecond)
	code := 0
	backend.ProcessesList = []process.Info{
		{ID: "main", Command: "sleep", Args: []string{"infinity"}, PID: 4242, State: types.ProcessRunning, StartedAt: started},
		{ID: "exec-1", Command: "echo", Args: []string{"hi"}, PID: 4243, State: types.ProcessExited, ExitCode: &code, StartedAt: started},
	}
	list, err = c.ListProcesses(ctx, ws.ID)
	if err != nil {
		t.Fatalf("ListProcesses: %v", err)
	}
	if len(list) != 2 {
		t.Fatalf("ListProcesses = %+v, want 2 processes", list)
	}
	main := list[0]
	if main.ID != "main" || main.WorkspaceID != ws.ID || main.Command != "sleep" ||
		len(main.Args) != 1 || main.PID != 4242 || main.State != types.ProcessRunning ||
		main.ExitCode != nil || !main.StartedAt.Equal(started) {
		t.Fatalf("main = %+v, want mapped fields", main)
	}
	exec := list[1]
	if exec.ID != "exec-1" || exec.State != types.ProcessExited ||
		exec.ExitCode == nil || *exec.ExitCode != 0 {
		t.Fatalf("exec = %+v, want EXITED 0", exec)
	}

	if _, err := c.ListProcesses(ctx, "nope"); !errors.Is(err, ErrWorkspaceNotFound) {
		t.Fatalf("ListProcesses missing = %v, want ErrWorkspaceNotFound", err)
	}
}

func TestListProcessesWithoutContainer(t *testing.T) {
	ctx := context.Background()
	c, backend, _ := testSetup(t)
	ws := mustCreate(t, c)

	// Never started: no container bound, empty list.
	list, err := c.ListProcesses(ctx, ws.ID)
	if err != nil || len(list) != 0 {
		t.Fatalf("ListProcesses unstarted = %+v, %v, want empty", list, err)
	}
	// Stale mapping: container gone from the backend, empty list.
	if _, err := c.Start(ctx, ws.ID); err != nil {
		t.Fatalf("Start: %v", err)
	}
	for id := range backend.States {
		if err := backend.Remove(ctx, id); err != nil {
			t.Fatalf("Remove: %v", err)
		}
	}
	list, err = c.ListProcesses(ctx, ws.ID)
	if err != nil || len(list) != 0 {
		t.Fatalf("ListProcesses stale = %+v, %v, want empty", list, err)
	}
}

func TestProcessLogs(t *testing.T) {
	ctx := context.Background()
	c, backend, _, ws := procsSetup(t)
	backend.ProcessesList = []process.Info{
		{ID: "main", Command: "sleep", State: types.ProcessRunning},
	}
	backend.LogsOutput = "line1\nline2\n"

	rc, err := c.ProcessLogs(ctx, ws.ID, "main", false)
	if err != nil {
		t.Fatalf("ProcessLogs: %v", err)
	}
	out, _ := io.ReadAll(rc)
	rc.Close()
	if string(out) != "line1\nline2\n" {
		t.Fatalf("logs = %q, want scripted output", out)
	}
	rc, err = c.ProcessLogs(ctx, ws.ID, "main", true)
	if err != nil {
		t.Fatalf("ProcessLogs follow: %v", err)
	}
	rc.Close()
	if len(backend.LogCalls) != 2 || backend.LogCalls[0].Follow || !backend.LogCalls[1].Follow {
		t.Fatalf("LogCalls = %+v, want follow flags [false true]", backend.LogCalls)
	}

	if _, err := c.ProcessLogs(ctx, ws.ID, "nope", false); !errors.Is(err, container.ErrProcessNotFound) {
		t.Fatalf("ProcessLogs unknown = %v, want ErrProcessNotFound", err)
	}
	if _, err := c.ProcessLogs(ctx, "nope", "main", false); !errors.Is(err, ErrWorkspaceNotFound) {
		t.Fatalf("ProcessLogs missing ws = %v, want ErrWorkspaceNotFound", err)
	}
}

func TestProcessLogsWithoutContainer(t *testing.T) {
	ctx := context.Background()
	c, _, _ := testSetup(t)
	ws := mustCreate(t, c)

	if _, err := c.ProcessLogs(ctx, ws.ID, "main", false); !errors.Is(err, container.ErrProcessNotFound) {
		t.Fatalf("ProcessLogs unstarted = %v, want ErrProcessNotFound", err)
	}
}

func TestExecEmitsProcessEvents(t *testing.T) {
	ctx := context.Background()
	c, backend, bus, ws := procsSetup(t)
	var started, stdout, exited []protocol.Event
	bus.Subscribe(protocol.EventProcessStarted, func(e protocol.Event) { started = append(started, e) })
	bus.Subscribe(protocol.EventProcessStdout, func(e protocol.Event) { stdout = append(stdout, e) })
	bus.Subscribe(protocol.EventProcessExited, func(e protocol.Event) { exited = append(exited, e) })
	backend.ExecOutput = "hi\n"

	var buf bytes.Buffer
	if err := c.Exec(ctx, ws.ID, container.ExecOptions{
		Cmd: []string{"echo", "hi"}, Stdout: &buf,
	}); err != nil {
		t.Fatalf("Exec: %v", err)
	}
	if buf.String() != "hi\n" {
		t.Fatalf("stdout = %q, want %q", buf.String(), "hi\n")
	}
	if len(started) != 1 || len(stdout) != 1 || len(exited) != 1 {
		t.Fatalf("events: started=%d stdout=%d exited=%d, want 1 each",
			len(started), len(stdout), len(exited))
	}
	var sp processStartedPayload
	if err := started[0].DecodePayload(&sp); err != nil {
		t.Fatalf("started payload: %v", err)
	}
	if started[0].WorkspaceID != ws.ID || sp.ProcessID == "" || !strings.HasPrefix(sp.ProcessID, "exec-") {
		t.Fatalf("started = %+v / %+v, want exec id", started[0], sp)
	}
	if len(sp.Command) != 2 || sp.Command[0] != "echo" {
		t.Fatalf("started command = %v, want [echo hi]", sp.Command)
	}
	var op processStdoutPayload
	if err := stdout[0].DecodePayload(&op); err != nil {
		t.Fatalf("stdout payload: %v", err)
	}
	if op.ProcessID != sp.ProcessID || op.Data != "hi\n" {
		t.Fatalf("stdout = %+v, want exec output", op)
	}
	var ep processExitedPayload
	if err := exited[0].DecodePayload(&ep); err != nil {
		t.Fatalf("exited payload: %v", err)
	}
	if ep.ProcessID != sp.ProcessID || ep.ExitCode != 0 || ep.Error != "" {
		t.Fatalf("exited = %+v, want exit 0", ep)
	}
	// The backend tracked the same exec id.
	if len(backend.Execs) != 1 || backend.Execs[0].ExecID != sp.ProcessID {
		t.Fatalf("recorded execs = %+v, want exec id %s", backend.Execs, sp.ProcessID)
	}
}

func TestExecExplicitIDAndError(t *testing.T) {
	ctx := context.Background()
	c, backend, bus, ws := procsSetup(t)
	var started, stdout, exited []protocol.Event
	bus.Subscribe(protocol.EventProcessStarted, func(e protocol.Event) { started = append(started, e) })
	bus.Subscribe(protocol.EventProcessStdout, func(e protocol.Event) { stdout = append(stdout, e) })
	bus.Subscribe(protocol.EventProcessExited, func(e protocol.Event) { exited = append(exited, e) })
	backend.ExecErr = errors.New("boom")

	if err := c.Exec(ctx, ws.ID, container.ExecOptions{
		Cmd: []string{"false"}, ExecID: "exec-x", Stdout: io.Discard,
	}); err == nil {
		t.Fatal("Exec: want error, got nil")
	}
	if len(started) != 1 || len(exited) != 1 || len(stdout) != 0 {
		t.Fatalf("events: started=%d stdout=%d exited=%d, want 1/0/1",
			len(started), len(stdout), len(exited))
	}
	var sp processStartedPayload
	_ = started[0].DecodePayload(&sp)
	var ep processExitedPayload
	_ = exited[0].DecodePayload(&ep)
	if sp.ProcessID != "exec-x" || ep.ProcessID != "exec-x" {
		t.Fatalf("exec ids = %q/%q, want exec-x", sp.ProcessID, ep.ProcessID)
	}
	if ep.ExitCode != -1 || !strings.Contains(ep.Error, "boom") {
		t.Fatalf("exited = %+v, want -1 with error", ep)
	}
	if backend.Execs[0].ExecID != "exec-x" {
		t.Fatalf("recorded exec id = %q, want exec-x", backend.Execs[0].ExecID)
	}
}
