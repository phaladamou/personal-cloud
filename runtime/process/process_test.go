package process

import (
	"context"
	"errors"
	"io"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/phaladamou/personal-cloud/packages/types"
)

func TestLocalStartWaitExitCode(t *testing.T) {
	pm := NewLocalManager()
	ctx := context.Background()

	id, err := pm.Start(ctx, StartSpec{Command: "sh", Args: []string{"-c", "exit 3"}})
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	code, err := pm.Wait(ctx, id)
	if err != nil {
		t.Fatalf("Wait: %v", err)
	}
	if code != 3 {
		t.Fatalf("Wait code = %d, want 3", code)
	}
	info, err := pm.Inspect(ctx, id)
	if err != nil {
		t.Fatalf("Inspect: %v", err)
	}
	if info.State != types.ProcessExited {
		t.Fatalf("State = %s, want %s", info.State, types.ProcessExited)
	}
	if info.ExitCode == nil || *info.ExitCode != 3 {
		t.Fatalf("ExitCode = %v, want 3", info.ExitCode)
	}
	if info.PID <= 0 {
		t.Fatalf("PID = %d, want positive", info.PID)
	}
}

func TestLocalLogsStreaming(t *testing.T) {
	pm := NewLocalManager()
	ctx := context.Background()

	id, err := pm.Start(ctx, StartSpec{Command: "sh", Args: []string{"-c", "echo out; echo err >&2"}})
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	rc, err := pm.Logs(ctx, id)
	if err != nil {
		t.Fatalf("Logs: %v", err)
	}
	defer rc.Close()
	out, err := io.ReadAll(rc)
	if err != nil {
		t.Fatalf("ReadAll: %v", err)
	}
	if string(out) != "out\nerr\n" {
		t.Fatalf("logs = %q, want %q", out, "out\nerr\n")
	}
}

func TestLocalLogsFollowSlowOutput(t *testing.T) {
	pm := NewLocalManager()
	ctx := context.Background()

	id, err := pm.Start(ctx, StartSpec{Command: "sh", Args: []string{"-c", "echo first; sleep 0.3; echo second"}})
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	rc, err := pm.Logs(ctx, id)
	if err != nil {
		t.Fatalf("Logs: %v", err)
	}
	defer rc.Close()
	out, err := io.ReadAll(rc)
	if err != nil {
		t.Fatalf("ReadAll: %v", err)
	}
	if string(out) != "first\nsecond\n" {
		t.Fatalf("logs = %q, want %q", out, "first\nsecond\n")
	}
}

func TestLocalLogsAfterExit(t *testing.T) {
	pm := NewLocalManager()
	ctx := context.Background()

	id, err := pm.Start(ctx, StartSpec{Command: "echo", Args: []string{"hello"}})
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	if _, err := pm.Wait(ctx, id); err != nil {
		t.Fatalf("Wait: %v", err)
	}
	// Opening the stream after the exit must still return full output.
	rc, err := pm.Logs(ctx, id)
	if err != nil {
		t.Fatalf("Logs: %v", err)
	}
	defer rc.Close()
	out, err := io.ReadAll(rc)
	if err != nil {
		t.Fatalf("ReadAll: %v", err)
	}
	if string(out) != "hello\n" {
		t.Fatalf("logs = %q, want %q", out, "hello\n")
	}
}

func TestLocalLogsSnapshot(t *testing.T) {
	pm := NewLocalManager()
	ctx := context.Background()

	id, err := pm.Start(ctx, StartSpec{Command: "sh", Args: []string{"-c", "echo one; echo two"}})
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	if _, err := pm.Wait(ctx, id); err != nil {
		t.Fatalf("Wait: %v", err)
	}
	snap, err := pm.LogsSnapshot(ctx, id)
	if err != nil {
		t.Fatalf("LogsSnapshot: %v", err)
	}
	if string(snap) != "one\ntwo\n" {
		t.Fatalf("snapshot = %q, want %q", snap, "one\ntwo\n")
	}
	// Snapshots never block on a running process with no output yet.
	slow, err := pm.Start(ctx, StartSpec{Command: "sleep", Args: []string{"30"}})
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	defer pm.Stop(ctx, slow)
	done := make(chan struct{})
	go func() {
		defer close(done)
		if snap, err := pm.LogsSnapshot(ctx, slow); err != nil || len(snap) != 0 {
			t.Errorf("snapshot running = %q, %v, want empty", snap, err)
		}
	}()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("LogsSnapshot blocked on a running process")
	}
	if _, err := pm.LogsSnapshot(ctx, "nope"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("LogsSnapshot unknown = %v, want ErrNotFound", err)
	}
}

func TestLocalStdinEnvDir(t *testing.T) {
	pm := NewLocalManager()
	ctx := context.Background()
	dir := t.TempDir()

	id, err := pm.Start(ctx, StartSpec{
		Command: "sh",
		Args:    []string{"-c", "cat; echo $PC_TEST; pwd"},
		Dir:     dir,
		Env:     []string{"PC_TEST=env-ok"},
		Stdin:   strings.NewReader("stdin-ok\n"),
	})
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	rc, err := pm.Logs(ctx, id)
	if err != nil {
		t.Fatalf("Logs: %v", err)
	}
	defer rc.Close()
	out, err := io.ReadAll(rc)
	if err != nil {
		t.Fatalf("ReadAll: %v", err)
	}
	if !strings.Contains(string(out), "stdin-ok\n") {
		t.Fatalf("logs missing stdin echo: %q", out)
	}
	if !strings.Contains(string(out), "env-ok\n") {
		t.Fatalf("logs missing env value: %q", out)
	}
	if !strings.Contains(string(out), dir+"\n") {
		t.Fatalf("logs missing working dir %q: %q", dir, out)
	}
}

func TestLocalStop(t *testing.T) {
	pm := NewLocalManager()
	ctx := context.Background()

	id, err := pm.Start(ctx, StartSpec{Command: "sleep", Args: []string{"30"}})
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	if err := pm.Stop(ctx, id); err != nil {
		t.Fatalf("Stop: %v", err)
	}
	code, err := pm.Wait(ctx, id)
	if err != nil {
		t.Fatalf("Wait: %v", err)
	}
	if code != -1 {
		t.Fatalf("Wait code = %d, want -1 for killed process", code)
	}
	info, err := pm.Inspect(ctx, id)
	if err != nil {
		t.Fatalf("Inspect: %v", err)
	}
	if info.State != types.ProcessKilled {
		t.Fatalf("State = %s, want %s", info.State, types.ProcessKilled)
	}
	// Stop is idempotent.
	if err := pm.Stop(ctx, id); err != nil {
		t.Fatalf("second Stop: %v", err)
	}
}

func TestLocalSignal(t *testing.T) {
	pm := NewLocalManager()
	ctx := context.Background()

	id, err := pm.Start(ctx, StartSpec{Command: "sleep", Args: []string{"30"}})
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	if err := pm.Signal(ctx, id, os.Interrupt); err != nil {
		t.Fatalf("Signal: %v", err)
	}
	code, err := pm.Wait(ctx, id)
	if err != nil {
		t.Fatalf("Wait: %v", err)
	}
	if code == 0 {
		t.Fatalf("Wait code = 0, want non-zero after interrupt")
	}
	if err := pm.Signal(ctx, id, os.Interrupt); !errors.Is(err, ErrExited) {
		t.Fatalf("Signal after exit = %v, want ErrExited", err)
	}
}

func TestLocalWaitContextCancel(t *testing.T) {
	pm := NewLocalManager()
	ctx := context.Background()

	id, err := pm.Start(ctx, StartSpec{Command: "sleep", Args: []string{"30"}})
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	defer pm.Stop(context.Background(), id)
	waitCtx, cancel := context.WithTimeout(ctx, 100*time.Millisecond)
	defer cancel()
	if _, err := pm.Wait(waitCtx, id); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("Wait = %v, want context.DeadlineExceeded", err)
	}
}

func TestLocalValidation(t *testing.T) {
	pm := NewLocalManager()
	ctx := context.Background()

	if _, err := pm.Start(ctx, StartSpec{}); err == nil {
		t.Fatal("Start with empty command: want error, got nil")
	}
	if _, err := pm.Start(ctx, StartSpec{Command: "does-not-exist-pc-12345"}); err == nil {
		t.Fatal("Start with unknown binary: want error, got nil")
	}
	cancelled, cancel := context.WithCancel(ctx)
	cancel()
	if _, err := pm.Start(cancelled, StartSpec{Command: "echo"}); !errors.Is(err, context.Canceled) {
		t.Fatalf("Start with cancelled ctx = %v, want context.Canceled", err)
	}
}

func TestLocalNotFound(t *testing.T) {
	pm := NewLocalManager()
	ctx := context.Background()

	if err := pm.Stop(ctx, "nope"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("Stop = %v, want ErrNotFound", err)
	}
	if err := pm.Signal(ctx, "nope", os.Interrupt); !errors.Is(err, ErrNotFound) {
		t.Fatalf("Signal = %v, want ErrNotFound", err)
	}
	if _, err := pm.Wait(ctx, "nope"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("Wait = %v, want ErrNotFound", err)
	}
	if _, err := pm.Logs(ctx, "nope"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("Logs = %v, want ErrNotFound", err)
	}
	if _, err := pm.Inspect(ctx, "nope"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("Inspect = %v, want ErrNotFound", err)
	}
}

func TestFakeProcessManager(t *testing.T) {
	var pm FakeProcessManager
	ctx := context.Background()
	pm.ExitCode = 7
	pm.Output = "fake logs\n"

	id, err := pm.Start(ctx, StartSpec{Command: "echo", Args: []string{"hi"}})
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	if got := pm.Specs[id].Command; got != "echo" {
		t.Fatalf("recorded command = %q, want echo", got)
	}
	code, err := pm.Wait(ctx, id)
	if err != nil {
		t.Fatalf("Wait: %v", err)
	}
	if code != 7 {
		t.Fatalf("Wait code = %d, want 7", code)
	}
	rc, err := pm.Logs(ctx, id)
	if err != nil {
		t.Fatalf("Logs: %v", err)
	}
	out, _ := io.ReadAll(rc)
	rc.Close()
	if string(out) != "fake logs\n" {
		t.Fatalf("logs = %q, want %q", out, "fake logs\n")
	}
	if err := pm.Signal(ctx, id, os.Interrupt); err != nil {
		t.Fatalf("Signal: %v", err)
	}
	if len(pm.Signals[id]) != 1 {
		t.Fatalf("recorded signals = %v, want 1 entry", pm.Signals[id])
	}
	if err := pm.Stop(ctx, id); err != nil {
		t.Fatalf("Stop: %v", err)
	}
	if !pm.Stopped[id] {
		t.Fatal("Stopped flag not set")
	}
	info, err := pm.Inspect(ctx, id)
	if err != nil {
		t.Fatalf("Inspect: %v", err)
	}
	if info.State != types.ProcessExited {
		t.Fatalf("State = %s, want %s", info.State, types.ProcessExited)
	}
	if _, err := pm.Wait(ctx, "nope"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("Wait unknown = %v, want ErrNotFound", err)
	}
	snap, err := pm.LogsSnapshot(ctx, id)
	if err != nil {
		t.Fatalf("LogsSnapshot: %v", err)
	}
	if string(snap) != "fake logs\n" {
		t.Fatalf("snapshot = %q, want %q", snap, "fake logs\n")
	}
	if _, err := pm.LogsSnapshot(ctx, "nope"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("LogsSnapshot unknown = %v, want ErrNotFound", err)
	}
}

func TestFakeStartError(t *testing.T) {
	var pm FakeProcessManager
	pm.StartErr = errors.New("boom")
	if _, err := pm.Start(context.Background(), StartSpec{Command: "echo"}); err == nil {
		t.Fatal("Start: want scripted error, got nil")
	}
}
