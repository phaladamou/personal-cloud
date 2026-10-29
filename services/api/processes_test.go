package api

import (
	"context"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"
	"time"

	"github.com/phaladamou/personal-cloud/packages/protocol"
	"github.com/phaladamou/personal-cloud/packages/types"
	"github.com/phaladamou/personal-cloud/runtime/container"
	"github.com/phaladamou/personal-cloud/runtime/process"
	controller "github.com/phaladamou/personal-cloud/services/workspace-controller"
)

// procsEnv wires the API over a scriptable backend.
type procsEnv struct {
	srv     *httptest.Server
	ctrl    *controller.Controller
	backend *controller.FakeBackend
}

func newProcsEnv(t *testing.T) *procsEnv {
	t.Helper()
	store, err := controller.NewFileStore(filepath.Join(t.TempDir(), "ws.json"))
	if err != nil {
		t.Fatalf("NewFileStore: %v", err)
	}
	backend := &controller.FakeBackend{}
	ctrl := controller.NewController(store, backend, protocol.NewEventBus())
	srv := httptest.NewServer(NewServer(ctrl))
	t.Cleanup(srv.Close)
	return &procsEnv{srv: srv, ctrl: ctrl, backend: backend}
}

func startWorkspace(t *testing.T, base, id string) {
	t.Helper()
	code, body := doJSON(t, http.MethodPost, base+"/v1/workspaces/"+id+"/start", nil)
	if code != http.StatusOK {
		t.Fatalf("start: got %d (%v), want 200", code, body)
	}
}

func TestProcessFlow(t *testing.T) {
	env := newProcsEnv(t)
	code, created := doJSON(t, http.MethodPost, env.srv.URL+"/v1/workspaces",
		map[string]string{"user_id": "user-1", "name": "dev"})
	if code != http.StatusCreated {
		t.Fatalf("create workspace: got %d", code)
	}
	id := created["id"].(string)
	startWorkspace(t, env.srv.URL, id)

	codeZero := 0
	env.backend.ProcessesList = []process.Info{
		{ID: "main", Command: "sleep", Args: []string{"infinity"}, PID: 4242,
			State: types.ProcessRunning, StartedAt: time.Now().UTC()},
		{ID: "exec-1", Command: "echo", Args: []string{"hi"}, PID: 4243,
			State: types.ProcessExited, ExitCode: &codeZero, StartedAt: time.Now().UTC()},
	}
	env.backend.LogsOutput = "hi\n"

	code, list := doJSON(t, http.MethodGet, env.srv.URL+"/v1/workspaces/"+id+"/processes", nil)
	if code != http.StatusOK {
		t.Fatalf("list processes: got %d", code)
	}
	items, _ := list["processes"].([]any)
	if len(items) != 2 {
		t.Fatalf("list processes: got %v, want 2", list)
	}
	first := items[0].(map[string]any)
	if first["id"] != "main" || first["workspace_id"] != id || first["command"] != "sleep" ||
		first["state"] != "RUNNING" || first["pid"] != float64(4242) {
		t.Fatalf("main process: got %v", first)
	}
	second := items[1].(map[string]any)
	if second["id"] != "exec-1" || second["state"] != "EXITED" || second["exit_code"] != float64(0) {
		t.Fatalf("exec process: got %v", second)
	}

	code, raw, hdr := doRaw(t, http.MethodGet, env.srv.URL+"/v1/workspaces/"+id+"/processes/exec-1/logs", nil)
	if code != http.StatusOK || string(raw) != "hi\n" {
		t.Fatalf("logs: got %d %q, want 200 hi", code, raw)
	}
	if ct := hdr.Get("Content-Type"); ct != "text/plain; charset=utf-8" {
		t.Fatalf("content type = %q", ct)
	}
	code, raw, _ = doRaw(t, http.MethodGet, env.srv.URL+"/v1/workspaces/"+id+"/processes/exec-1/logs?follow=true", nil)
	if code != http.StatusOK || string(raw) != "hi\n" {
		t.Fatalf("logs follow: got %d %q, want 200 hi", code, raw)
	}
	if len(env.backend.LogCalls) != 2 || env.backend.LogCalls[0].Follow || !env.backend.LogCalls[1].Follow {
		t.Fatalf("LogCalls = %+v, want follow flags [false true]", env.backend.LogCalls)
	}

	if code, _ := doJSON(t, http.MethodGet, env.srv.URL+"/v1/workspaces/"+id+"/processes/nope/logs", nil); code != http.StatusNotFound {
		t.Fatalf("logs unknown pid: got %d, want 404", code)
	}
	if code, _ := doJSON(t, http.MethodGet, env.srv.URL+"/v1/workspaces/nope/processes", nil); code != http.StatusNotFound {
		t.Fatalf("list on missing workspace: got %d, want 404", code)
	}
	if code, _ := doJSON(t, http.MethodGet, env.srv.URL+"/v1/workspaces/nope/processes/main/logs", nil); code != http.StatusNotFound {
		t.Fatalf("logs on missing workspace: got %d, want 404", code)
	}
	if code, _ := doJSON(t, http.MethodGet, env.srv.URL+"/v1/workspaces/"+id+"/processes/main/logs?follow=bogus", nil); code != http.StatusBadRequest {
		t.Fatalf("logs invalid follow: got %d, want 400", code)
	}
}

func TestProcessEmptyWithoutContainer(t *testing.T) {
	env := newProcsEnv(t)
	code, created := doJSON(t, http.MethodPost, env.srv.URL+"/v1/workspaces",
		map[string]string{"user_id": "user-1", "name": "dev"})
	if code != http.StatusCreated {
		t.Fatalf("create workspace: got %d", code)
	}
	id := created["id"].(string)

	// Never started: empty process list, unknown logs.
	code, list := doJSON(t, http.MethodGet, env.srv.URL+"/v1/workspaces/"+id+"/processes", nil)
	if code != http.StatusOK {
		t.Fatalf("list processes: got %d", code)
	}
	if items, _ := list["processes"].([]any); len(items) != 0 {
		t.Fatalf("list processes: got %v, want empty", list)
	}
	if code, _ := doJSON(t, http.MethodGet, env.srv.URL+"/v1/workspaces/"+id+"/processes/main/logs", nil); code != http.StatusNotFound {
		t.Fatalf("logs without container: got %d, want 404", code)
	}
}

// TestProcessFollowLive exercises log following over HTTP against the
// real local backend.
func TestProcessFollowLive(t *testing.T) {
	store, err := controller.NewFileStore(filepath.Join(t.TempDir(), "ws.json"))
	if err != nil {
		t.Fatalf("NewFileStore: %v", err)
	}
	ctrl := controller.NewController(store, container.DefaultBackend(), protocol.NewEventBus())
	srv := httptest.NewServer(NewServer(ctrl))
	defer srv.Close()
	ctx := context.Background()

	code, created := doJSON(t, http.MethodPost, srv.URL+"/v1/workspaces",
		map[string]string{"user_id": "user-1", "name": "dev"})
	if code != http.StatusCreated {
		t.Fatalf("create workspace: got %d", code)
	}
	id := created["id"].(string)
	startWorkspace(t, srv.URL, id)
	defer ctrl.Stop(ctx, id)

	if err := ctrl.Exec(ctx, id, container.ExecOptions{Cmd: []string{"echo", "live-ok"}}); err != nil {
		t.Fatalf("Exec: %v", err)
	}
	code, list := doJSON(t, http.MethodGet, srv.URL+"/v1/workspaces/"+id+"/processes", nil)
	if code != http.StatusOK {
		t.Fatalf("list processes: got %d", code)
	}
	items, _ := list["processes"].([]any)
	if len(items) != 2 {
		t.Fatalf("list processes: got %v, want [main exec]", list)
	}
	execID, _ := items[1].(map[string]any)["id"].(string)
	if execID == "" {
		t.Fatalf("exec entry: got %v", items[1])
	}
	code, raw, _ := doRaw(t, http.MethodGet, srv.URL+"/v1/workspaces/"+id+"/processes/"+execID+"/logs?follow=true", nil)
	if code != http.StatusOK || string(raw) != "live-ok\n" {
		t.Fatalf("logs follow: got %d %q, want 200 live-ok", code, raw)
	}
}
