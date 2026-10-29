package container

import (
	"bytes"
	"context"
	"errors"
	"io"
	"os/exec"
	"strings"
	"testing"
	"time"

	"github.com/phaladamou/personal-cloud/packages/types"
	"github.com/phaladamou/personal-cloud/runtime/process"
)

func TestLocalBackendLifecycle(t *testing.T) {
	b := NewLocalBackend()
	ctx := context.Background()

	if _, err := b.Create(ctx, Config{}); err == nil {
		t.Fatal("Create with empty command: want error, got nil")
	}
	id, err := b.Create(ctx, Config{Name: "web", Command: "sleep", Args: []string{"30"}})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	info, err := b.Inspect(ctx, id)
	if err != nil {
		t.Fatalf("Inspect: %v", err)
	}
	if info.State != StateCreated || info.Name != "web" {
		t.Fatalf("Inspect = %+v, want CREATED web", info)
	}
	if err := b.Start(ctx, id); err != nil {
		t.Fatalf("Start: %v", err)
	}
	if err := b.Start(ctx, id); err == nil {
		t.Fatal("double Start: want error, got nil")
	}
	var stdout bytes.Buffer
	if err := b.Exec(ctx, id, ExecOptions{Cmd: []string{"echo", "exec-ok"}, Stdout: &stdout}); err != nil {
		t.Fatalf("Exec: %v", err)
	}
	if stdout.String() != "exec-ok\n" {
		t.Fatalf("exec stdout = %q, want %q", stdout.String(), "exec-ok\n")
	}
	if err := b.Stop(ctx, id); err != nil {
		t.Fatalf("Stop: %v", err)
	}
	info, err = b.Inspect(ctx, id)
	if err != nil {
		t.Fatalf("Inspect: %v", err)
	}
	if info.State != StateStopped {
		t.Fatalf("State = %s, want STOPPED", info.State)
	}
	if err := b.Stop(ctx, id); err != nil {
		t.Fatalf("second Stop: %v", err)
	}
	if err := b.Remove(ctx, id); err != nil {
		t.Fatalf("Remove: %v", err)
	}
	if _, err := b.Inspect(ctx, id); !errors.Is(err, ErrNotFound) {
		t.Fatalf("Inspect removed = %v, want ErrNotFound", err)
	}
}

func TestLocalBackendExitAndRestart(t *testing.T) {
	b := NewLocalBackend()
	ctx := context.Background()

	id, err := b.Create(ctx, Config{Command: "sh", Args: []string{"-c", "exit 0"}})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if err := b.Start(ctx, id); err != nil {
		t.Fatalf("Start: %v", err)
	}
	waitState(t, b, id, StateStopped)
	// A stopped container restarts.
	if err := b.Start(ctx, id); err != nil {
		t.Fatalf("restart: %v", err)
	}
	waitState(t, b, id, StateStopped)
	if err := b.Remove(ctx, id); err != nil {
		t.Fatalf("Remove: %v", err)
	}
}

func waitState(t *testing.T, b ContainerBackend, id string, want State) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for {
		info, err := b.Inspect(context.Background(), id)
		if err != nil {
			t.Fatalf("Inspect: %v", err)
		}
		if info.State == want {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("State = %s, want %s", info.State, want)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func TestLocalBackendExecValidation(t *testing.T) {
	b := NewLocalBackend()
	ctx := context.Background()

	id, err := b.Create(ctx, Config{Command: "sleep", Args: []string{"30"}})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	defer b.Remove(ctx, id)
	// Exec on a created (not running) container fails.
	if err := b.Exec(ctx, id, ExecOptions{Cmd: []string{"echo", "x"}}); err == nil {
		t.Fatal("Exec on created container: want error, got nil")
	}
	if err := b.Start(ctx, id); err != nil {
		t.Fatalf("Start: %v", err)
	}
	if err := b.Exec(ctx, id, ExecOptions{}); err == nil {
		t.Fatal("Exec with empty command: want error, got nil")
	}
	// Stderr streaming and stdin piping.
	var stderr bytes.Buffer
	if err := b.Exec(ctx, id, ExecOptions{
		Cmd:    []string{"sh", "-c", "cat >&2"},
		Stdin:  strings.NewReader("in-ok\n"),
		Stderr: &stderr,
	}); err != nil {
		t.Fatalf("Exec: %v", err)
	}
	if stderr.String() != "in-ok\n" {
		t.Fatalf("exec stderr = %q, want %q", stderr.String(), "in-ok\n")
	}
	// Failing command reports an error.
	if err := b.Exec(ctx, id, ExecOptions{Cmd: []string{"sh", "-c", "exit 2"}}); err == nil {
		t.Fatal("Exec failing command: want error, got nil")
	}
	if err := b.Exec(ctx, "nope", ExecOptions{Cmd: []string{"echo"}}); !errors.Is(err, ErrNotFound) {
		t.Fatalf("Exec unknown = %v, want ErrNotFound", err)
	}
}

func TestLocalBackendWithFakeManager(t *testing.T) {
	var pm process.FakeProcessManager
	b := NewLocalBackendWithManager(&pm)
	ctx := context.Background()

	id, err := b.Create(ctx, Config{Command: "sleep", Args: []string{"30"}})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if err := b.Start(ctx, id); err != nil {
		t.Fatalf("Start: %v", err)
	}
	if len(pm.Specs) != 1 {
		t.Fatalf("fake recorded %d starts, want 1", len(pm.Specs))
	}
	if err := b.Remove(ctx, id); err != nil {
		t.Fatalf("Remove: %v", err)
	}
	if _, err := b.Inspect(ctx, id); !errors.Is(err, ErrNotFound) {
		t.Fatalf("Inspect removed = %v, want ErrNotFound", err)
	}
}

func TestLocalBackendProcessesAndLogs(t *testing.T) {
	b := NewLocalBackend()
	ctx := context.Background()

	id, err := b.Create(ctx, Config{Command: "sleep", Args: []string{"30"}})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	defer b.Remove(ctx, id)
	// A container that never started lists as empty.
	procs, err := b.Processes(ctx, id)
	if err != nil {
		t.Fatalf("Processes: %v", err)
	}
	if len(procs) != 0 {
		t.Fatalf("Processes = %+v, want empty", procs)
	}
	if _, err := b.Logs(ctx, id, MainProcessID, false); !errors.Is(err, ErrProcessNotFound) {
		t.Fatalf("Logs main before start = %v, want ErrProcessNotFound", err)
	}
	if err := b.Start(ctx, id); err != nil {
		t.Fatalf("Start: %v", err)
	}

	procs, err = b.Processes(ctx, id)
	if err != nil {
		t.Fatalf("Processes: %v", err)
	}
	if len(procs) != 1 || procs[0].ID != MainProcessID {
		t.Fatalf("Processes = %+v, want [main]", procs)
	}
	main := procs[0]
	if main.State != types.ProcessRunning || main.Command != "sleep" || main.PID <= 0 {
		t.Fatalf("main process = %+v, want RUNNING sleep", main)
	}

	var stdout bytes.Buffer
	if err := b.Exec(ctx, id, ExecOptions{Cmd: []string{"echo", "exec-ok"}, Stdout: &stdout}); err != nil {
		t.Fatalf("Exec: %v", err)
	}
	if stdout.String() != "exec-ok\n" {
		t.Fatalf("exec stdout = %q, want %q", stdout.String(), "exec-ok\n")
	}
	procs, err = b.Processes(ctx, id)
	if err != nil {
		t.Fatalf("Processes: %v", err)
	}
	if len(procs) != 2 || procs[0].ID != MainProcessID {
		t.Fatalf("Processes = %+v, want [main exec]", procs)
	}
	execProc := procs[1]
	if execProc.State != types.ProcessExited || execProc.ExitCode == nil || *execProc.ExitCode != 0 {
		t.Fatalf("exec process = %+v, want EXITED 0", execProc)
	}
	if execProc.Command != "echo" || len(execProc.Args) != 1 || execProc.PID <= 0 {
		t.Fatalf("exec process = %+v, want echo with pid", execProc)
	}

	// Snapshot logs return the recorded output without blocking.
	rc, err := b.Logs(ctx, id, execProc.ID, false)
	if err != nil {
		t.Fatalf("Logs exec: %v", err)
	}
	out, _ := io.ReadAll(rc)
	rc.Close()
	if string(out) != "exec-ok\n" {
		t.Fatalf("exec logs = %q, want %q", out, "exec-ok\n")
	}
	// Follow logs on an exited process replay then end with EOF.
	rc, err = b.Logs(ctx, id, execProc.ID, true)
	if err != nil {
		t.Fatalf("Logs exec follow: %v", err)
	}
	out, _ = io.ReadAll(rc)
	rc.Close()
	if string(out) != "exec-ok\n" {
		t.Fatalf("exec follow logs = %q, want %q", out, "exec-ok\n")
	}
	// Failing commands record their exit code.
	if err := b.Exec(ctx, id, ExecOptions{Cmd: []string{"sh", "-c", "exit 2"}}); err == nil {
		t.Fatal("Exec failing command: want error, got nil")
	}
	procs, err = b.Processes(ctx, id)
	if err != nil {
		t.Fatalf("Processes: %v", err)
	}
	last := procs[len(procs)-1]
	if last.ExitCode == nil || *last.ExitCode != 2 {
		t.Fatalf("failed exec = %+v, want exit code 2", last)
	}

	if _, err := b.Logs(ctx, id, "nope", false); !errors.Is(err, ErrProcessNotFound) {
		t.Fatalf("Logs unknown = %v, want ErrProcessNotFound", err)
	}
	if _, err := b.Processes(ctx, "nope"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("Processes unknown = %v, want ErrNotFound", err)
	}
	if _, err := b.Logs(ctx, "nope", MainProcessID, false); !errors.Is(err, ErrNotFound) {
		t.Fatalf("Logs unknown container = %v, want ErrNotFound", err)
	}
}

func TestLocalBackendExecIDTracking(t *testing.T) {
	b := NewLocalBackend()
	ctx := context.Background()

	id, err := b.Create(ctx, Config{Command: "sleep", Args: []string{"30"}})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	defer b.Remove(ctx, id)
	if err := b.Start(ctx, id); err != nil {
		t.Fatalf("Start: %v", err)
	}
	if err := b.Exec(ctx, id, ExecOptions{Cmd: []string{"echo", "x"}, ExecID: "exec-abc"}); err != nil {
		t.Fatalf("Exec: %v", err)
	}
	procs, err := b.Processes(ctx, id)
	if err != nil {
		t.Fatalf("Processes: %v", err)
	}
	if len(procs) != 2 || procs[1].ID != "exec-abc" {
		t.Fatalf("Processes = %+v, want exec-abc tracked", procs)
	}
	rc, err := b.Logs(ctx, id, "exec-abc", false)
	if err != nil {
		t.Fatalf("Logs: %v", err)
	}
	out, _ := io.ReadAll(rc)
	rc.Close()
	if string(out) != "x\n" {
		t.Fatalf("logs = %q, want %q", out, "x\n")
	}
}

func TestDockerStateMapping(t *testing.T) {
	cases := map[string]State{
		"created":    StateCreated,
		"running":    StateRunning,
		"paused":     StateRunning,
		"restarting": StateRunning,
		"exited":     StateStopped,
		"dead":       StateStopped,
		"removing":   StateStopped,
		"bogus":      StateStopped,
	}
	for status, want := range cases {
		if got := dockerState(status); got != want {
			t.Fatalf("dockerState(%q) = %s, want %s", status, got, want)
		}
	}
}

func TestDockerAvailableNoDaemon(t *testing.T) {
	// With an empty PATH there is no docker binary, so no daemon.
	t.Setenv("PATH", t.TempDir())
	if DockerAvailable(context.Background()) {
		t.Fatal("DockerAvailable = true without docker binary, want false")
	}
}

func requireDocker(t *testing.T) *DockerBackend {
	t.Helper()
	d, err := NewDockerBackend()
	if err != nil {
		t.Skipf("docker binary missing: %v", err)
	}
	if !DockerAvailable(context.Background()) {
		t.Skip("no docker daemon, skipping docker backend test")
	}
	// Stay offline-safe: only use a locally cached image.
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	if err := exec.CommandContext(ctx, "docker", "image", "inspect", "hello-world").Run(); err != nil {
		t.Skip("hello-world image not cached, skipping docker backend test")
	}
	return d
}

func TestDockerBackendLifecycle(t *testing.T) {
	d := requireDocker(t)
	ctx := context.Background()

	id, err := d.Create(ctx, Config{Image: "hello-world"})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	defer d.Remove(ctx, id)
	info, err := d.Inspect(ctx, id)
	if err != nil {
		t.Fatalf("Inspect: %v", err)
	}
	if info.State != StateCreated {
		t.Fatalf("State = %s, want CREATED", info.State)
	}
	if err := d.Start(ctx, id); err != nil {
		t.Fatalf("Start: %v", err)
	}
	procs, err := d.Processes(ctx, id)
	if err != nil {
		t.Fatalf("Processes: %v", err)
	}
	if len(procs) != 1 || procs[0].ID != MainProcessID {
		t.Fatalf("Processes = %+v, want [main]", procs)
	}
	deadline := time.Now().Add(30 * time.Second)
	for {
		rc, err := d.Logs(ctx, id, MainProcessID, false)
		if err != nil {
			t.Fatalf("Logs: %v", err)
		}
		out, _ := io.ReadAll(rc)
		rc.Close()
		if strings.Contains(string(out), "Hello from Docker!") {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("logs = %q, want hello-world banner", out)
		}
		time.Sleep(200 * time.Millisecond)
	}
	if _, err := d.Logs(ctx, id, "nope", false); !errors.Is(err, ErrProcessNotFound) {
		t.Fatalf("Logs unknown = %v, want ErrProcessNotFound", err)
	}
	if _, err := d.Create(ctx, Config{}); err == nil {
		t.Fatal("Create without image: want error, got nil")
	}
}
