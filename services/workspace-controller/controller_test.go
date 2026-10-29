package controller

import (
	"context"
	"errors"
	"path/filepath"
	"testing"

	"github.com/phaladamou/personal-cloud/packages/protocol"
	"github.com/phaladamou/personal-cloud/packages/types"
	"github.com/phaladamou/personal-cloud/runtime/container"
)

func testSetup(t *testing.T) (*Controller, *FakeBackend, *protocol.EventBus) {
	t.Helper()
	store, err := NewFileStore(filepath.Join(t.TempDir(), "ws.json"))
	if err != nil {
		t.Fatalf("NewFileStore: %v", err)
	}
	backend := &FakeBackend{}
	bus := protocol.NewEventBus()
	return NewController(store, backend, bus), backend, bus
}

func mustCreate(t *testing.T, c *Controller) types.Workspace {
	t.Helper()
	ws, err := c.Create(context.Background(), "user-1", "dev")
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	return ws
}

func collectEvents(bus *protocol.EventBus) *[]protocol.Event {
	var out []protocol.Event
	for _, typ := range []protocol.EventType{
		protocol.EventWorkspaceCreated,
		protocol.EventWorkspaceStarted,
		protocol.EventWorkspaceStopped,
	} {
		bus.Subscribe(typ, func(e protocol.Event) { out = append(out, e) })
	}
	return &out
}

func eventTypes(events []protocol.Event) []protocol.EventType {
	out := make([]protocol.EventType, 0, len(events))
	for _, e := range events {
		out = append(out, e.Type)
	}
	return out
}

func TestCreate(t *testing.T) {
	c, _, bus := testSetup(t)
	events := collectEvents(bus)

	ws, err := c.Create(context.Background(), "user-1", "dev")
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if ws.Status != types.WorkspaceCreated {
		t.Fatalf("Create status: got %s, want CREATED", ws.Status)
	}
	if ws.ID == "" || ws.UserID != "user-1" || ws.Name != "dev" {
		t.Fatalf("Create fields: got %+v", ws)
	}
	got := eventTypes(*events)
	if len(got) != 1 || got[0] != protocol.EventWorkspaceCreated {
		t.Fatalf("Create events: got %v, want [workspace.created]", got)
	}

	if _, err := c.Create(context.Background(), "", "dev"); err == nil {
		t.Fatal("Create empty user: got nil, want error")
	}
	if _, err := c.Create(context.Background(), "user-1", "  "); err == nil {
		t.Fatal("Create empty name: got nil, want error")
	}
}

func TestStartHappyPath(t *testing.T) {
	ctx := context.Background()
	c, backend, bus := testSetup(t)
	events := collectEvents(bus)
	ws := mustCreate(t, c)

	got, err := c.Start(ctx, ws.ID)
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	if got.Status != types.WorkspaceRunning {
		t.Fatalf("Start status: got %s, want RUNNING", got.Status)
	}
	if len(backend.Configs) != 1 || backend.Starts != 1 {
		t.Fatalf("Start backend calls: configs=%d starts=%d, want 1/1",
			len(backend.Configs), backend.Starts)
	}
	wantEvents := []protocol.EventType{protocol.EventWorkspaceCreated, protocol.EventWorkspaceStarted}
	if got := eventTypes(*events); len(got) != 2 || got[0] != wantEvents[0] || got[1] != wantEvents[1] {
		t.Fatalf("Start events: got %v, want %v", got, wantEvents)
	}
}

func TestStartFailureFallsBackToStopped(t *testing.T) {
	ctx := context.Background()
	c, backend, _ := testSetup(t)
	backend.StartErr = errors.New("boom")
	ws := mustCreate(t, c)

	if _, err := c.Start(ctx, ws.ID); err == nil {
		t.Fatal("Start with backend failure: got nil, want error")
	}
	got, err := c.Get(ctx, ws.ID)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got.Status != types.WorkspaceStopped {
		t.Fatalf("status after failed Start: got %s, want STOPPED", got.Status)
	}
}

func TestFullLifecycle(t *testing.T) {
	ctx := context.Background()
	c, _, _ := testSetup(t)
	ws := mustCreate(t, c)

	steps := []struct {
		name string
		call func() (types.Workspace, error)
		want types.WorkspaceStatus
	}{
		{"start", func() (types.Workspace, error) { return c.Start(ctx, ws.ID) }, types.WorkspaceRunning},
		{"sleep", func() (types.Workspace, error) { return c.Sleep(ctx, ws.ID) }, types.WorkspaceSleeping},
		{"wakeup", func() (types.Workspace, error) { return c.Wakeup(ctx, ws.ID) }, types.WorkspaceRunning},
		{"stop", func() (types.Workspace, error) { return c.Stop(ctx, ws.ID) }, types.WorkspaceStopped},
		{"restore", func() (types.Workspace, error) { return c.Restore(ctx, ws.ID) }, types.WorkspaceRunning},
		{"stop2", func() (types.Workspace, error) { return c.Stop(ctx, ws.ID) }, types.WorkspaceStopped},
	}
	for _, s := range steps {
		got, err := s.call()
		if err != nil {
			t.Fatalf("%s: %v", s.name, err)
		}
		if got.Status != s.want {
			t.Fatalf("%s status: got %s, want %s", s.name, got.Status, s.want)
		}
	}
	if err := c.Delete(ctx, ws.ID); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	if _, err := c.Get(ctx, ws.ID); !errors.Is(err, ErrWorkspaceNotFound) {
		t.Fatalf("Get after Delete: got %v, want ErrWorkspaceNotFound", err)
	}
}

func TestStopFromSleeping(t *testing.T) {
	ctx := context.Background()
	c, backend, bus := testSetup(t)
	events := collectEvents(bus)
	ws := mustCreate(t, c)

	if _, err := c.Start(ctx, ws.ID); err != nil {
		t.Fatalf("Start: %v", err)
	}
	if _, err := c.Sleep(ctx, ws.ID); err != nil {
		t.Fatalf("Sleep: %v", err)
	}
	got, err := c.Stop(ctx, ws.ID)
	if err != nil {
		t.Fatalf("Stop from SLEEPING: %v", err)
	}
	if got.Status != types.WorkspaceStopped {
		t.Fatalf("Stop status: got %s, want STOPPED", got.Status)
	}
	if backend.Stops != 2 {
		t.Fatalf("Stops: got %d, want 2 (sleep + stop)", backend.Stops)
	}
	last := (*events)[len(*events)-1]
	if last.Type != protocol.EventWorkspaceStopped {
		t.Fatalf("last event: got %s, want workspace.stopped", last.Type)
	}
}

func TestRestartFromStoppedReusesContainer(t *testing.T) {
	ctx := context.Background()
	c, backend, _ := testSetup(t)
	ws := mustCreate(t, c)

	if _, err := c.Start(ctx, ws.ID); err != nil {
		t.Fatalf("Start: %v", err)
	}
	if _, err := c.Stop(ctx, ws.ID); err != nil {
		t.Fatalf("Stop: %v", err)
	}
	if _, err := c.Start(ctx, ws.ID); err != nil {
		t.Fatalf("Start again: %v", err)
	}
	if len(backend.Configs) != 1 || backend.Starts != 2 {
		t.Fatalf("restart backend calls: configs=%d starts=%d, want 1/2",
			len(backend.Configs), backend.Starts)
	}
}

func TestRestoreFailureFallsBackToStopped(t *testing.T) {
	ctx := context.Background()
	c, backend, _ := testSetup(t)
	ws := mustCreate(t, c)

	if _, err := c.Start(ctx, ws.ID); err != nil {
		t.Fatalf("Start: %v", err)
	}
	if _, err := c.Stop(ctx, ws.ID); err != nil {
		t.Fatalf("Stop: %v", err)
	}
	backend.StartErr = errors.New("boom")
	if _, err := c.Restore(ctx, ws.ID); err == nil {
		t.Fatal("Restore with backend failure: got nil, want error")
	}
	got, err := c.Get(ctx, ws.ID)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got.Status != types.WorkspaceStopped {
		t.Fatalf("status after failed Restore: got %s, want STOPPED", got.Status)
	}
}

func TestDeleteVariants(t *testing.T) {
	ctx := context.Background()

	t.Run("created", func(t *testing.T) {
		c, backend, _ := testSetup(t)
		ws := mustCreate(t, c)
		if err := c.Delete(ctx, ws.ID); err != nil {
			t.Fatalf("Delete CREATED: %v", err)
		}
		if backend.Removes != 0 {
			t.Fatalf("Removes: got %d, want 0 (no container yet)", backend.Removes)
		}
	})

	t.Run("sleeping", func(t *testing.T) {
		c, backend, _ := testSetup(t)
		ws := mustCreate(t, c)
		if _, err := c.Start(ctx, ws.ID); err != nil {
			t.Fatalf("Start: %v", err)
		}
		if _, err := c.Sleep(ctx, ws.ID); err != nil {
			t.Fatalf("Sleep: %v", err)
		}
		if err := c.Delete(ctx, ws.ID); err != nil {
			t.Fatalf("Delete SLEEPING: %v", err)
		}
		if backend.Removes != 1 {
			t.Fatalf("Removes: got %d, want 1", backend.Removes)
		}
	})

	t.Run("missing", func(t *testing.T) {
		c, _, _ := testSetup(t)
		if err := c.Delete(ctx, "nope"); !errors.Is(err, ErrWorkspaceNotFound) {
			t.Fatalf("Delete missing: got %v, want ErrWorkspaceNotFound", err)
		}
	})
}

func TestExec(t *testing.T) {
	ctx := context.Background()
	c, backend, _ := testSetup(t)
	backend.ExecOutput = "hi\n"
	ws := mustCreate(t, c)

	var out fakeWriter
	if err := c.Exec(ctx, ws.ID, container.ExecOptions{Cmd: []string{"echo", "hi"}, Stdout: &out}); !errors.Is(err, ErrIllegalTransition) {
		t.Fatalf("Exec on CREATED: got %v, want ErrIllegalTransition", err)
	}
	if _, err := c.Start(ctx, ws.ID); err != nil {
		t.Fatalf("Start: %v", err)
	}
	if err := c.Exec(ctx, ws.ID, container.ExecOptions{Cmd: []string{"echo", "hi"}, Stdout: &out}); err != nil {
		t.Fatalf("Exec: %v", err)
	}
	if string(out) != "hi\n" {
		t.Fatalf("Exec output: got %q, want %q", string(out), "hi\n")
	}
	if len(backend.Execs) != 1 || len(backend.Execs[0].Cmd) != 2 {
		t.Fatalf("Exec recorded: %+v", backend.Execs)
	}
	if _, err := c.Stop(ctx, ws.ID); err != nil {
		t.Fatalf("Stop: %v", err)
	}
	if err := c.Exec(ctx, ws.ID, container.ExecOptions{Cmd: []string{"echo"}}); !errors.Is(err, ErrIllegalTransition) {
		t.Fatalf("Exec on STOPPED: got %v, want ErrIllegalTransition", err)
	}
	if err := c.Exec(ctx, "nope", container.ExecOptions{Cmd: []string{"echo"}}); !errors.Is(err, ErrWorkspaceNotFound) {
		t.Fatalf("Exec missing: got %v, want ErrWorkspaceNotFound", err)
	}
}

type fakeWriter []byte

func (w *fakeWriter) Write(p []byte) (int, error) {
	*w = append(*w, p...)
	return len(p), nil
}

func TestIllegalTransitions(t *testing.T) {
	ctx := context.Background()

	// drive moves a fresh workspace to target status, failing the test
	// on any unexpected error.
	drive := func(t *testing.T, c *Controller, target types.WorkspaceStatus) types.Workspace {
		t.Helper()
		ws := mustCreate(t, c)
		switch target {
		case types.WorkspaceCreated:
		case types.WorkspaceRunning:
			if _, err := c.Start(ctx, ws.ID); err != nil {
				t.Fatalf("drive Start: %v", err)
			}
		case types.WorkspaceSleeping:
			if _, err := c.Start(ctx, ws.ID); err != nil {
				t.Fatalf("drive Start: %v", err)
			}
			if _, err := c.Sleep(ctx, ws.ID); err != nil {
				t.Fatalf("drive Sleep: %v", err)
			}
		case types.WorkspaceStopped:
			if _, err := c.Start(ctx, ws.ID); err != nil {
				t.Fatalf("drive Start: %v", err)
			}
			if _, err := c.Stop(ctx, ws.ID); err != nil {
				t.Fatalf("drive Stop: %v", err)
			}
		}
		ws, err := c.Get(ctx, ws.ID)
		if err != nil {
			t.Fatalf("drive Get: %v", err)
		}
		if ws.Status != target {
			t.Fatalf("drive: got %s, want %s", ws.Status, target)
		}
		return ws
	}

	cases := []struct {
		name string
		from types.WorkspaceStatus
		op   string
		call func(c *Controller, id string) error
	}{
		{"start/running", types.WorkspaceRunning, "Start", func(c *Controller, id string) error {
			_, err := c.Start(ctx, id)
			return err
		}},
		{"start/sleeping", types.WorkspaceSleeping, "Start", func(c *Controller, id string) error {
			_, err := c.Start(ctx, id)
			return err
		}},
		{"sleep/created", types.WorkspaceCreated, "Sleep", func(c *Controller, id string) error {
			_, err := c.Sleep(ctx, id)
			return err
		}},
		{"sleep/sleeping", types.WorkspaceSleeping, "Sleep", func(c *Controller, id string) error {
			_, err := c.Sleep(ctx, id)
			return err
		}},
		{"sleep/stopped", types.WorkspaceStopped, "Sleep", func(c *Controller, id string) error {
			_, err := c.Sleep(ctx, id)
			return err
		}},
		{"wakeup/created", types.WorkspaceCreated, "Wakeup", func(c *Controller, id string) error {
			_, err := c.Wakeup(ctx, id)
			return err
		}},
		{"wakeup/running", types.WorkspaceRunning, "Wakeup", func(c *Controller, id string) error {
			_, err := c.Wakeup(ctx, id)
			return err
		}},
		{"wakeup/stopped", types.WorkspaceStopped, "Wakeup", func(c *Controller, id string) error {
			_, err := c.Wakeup(ctx, id)
			return err
		}},
		{"stop/created", types.WorkspaceCreated, "Stop", func(c *Controller, id string) error {
			_, err := c.Stop(ctx, id)
			return err
		}},
		{"stop/stopped", types.WorkspaceStopped, "Stop", func(c *Controller, id string) error {
			_, err := c.Stop(ctx, id)
			return err
		}},
		{"restore/created", types.WorkspaceCreated, "Restore", func(c *Controller, id string) error {
			_, err := c.Restore(ctx, id)
			return err
		}},
		{"restore/running", types.WorkspaceRunning, "Restore", func(c *Controller, id string) error {
			_, err := c.Restore(ctx, id)
			return err
		}},
		{"restore/sleeping", types.WorkspaceSleeping, "Restore", func(c *Controller, id string) error {
			_, err := c.Restore(ctx, id)
			return err
		}},
		{"delete/running", types.WorkspaceRunning, "Delete", func(c *Controller, id string) error {
			return c.Delete(ctx, id)
		}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			c, _, _ := testSetup(t)
			ws := drive(t, c, tc.from)
			if err := tc.call(c, ws.ID); !errors.Is(err, ErrIllegalTransition) {
				t.Fatalf("%s from %s: got %v, want ErrIllegalTransition", tc.op, tc.from, err)
			}
			// Failed transitions leave state untouched.
			got, err := c.Get(ctx, ws.ID)
			if err != nil {
				t.Fatalf("Get: %v", err)
			}
			if got.Status != tc.from {
				t.Fatalf("status after illegal %s: got %s, want %s", tc.op, got.Status, tc.from)
			}
		})
	}
}

func TestOpsOnMissingWorkspace(t *testing.T) {
	ctx := context.Background()
	c, _, _ := testSetup(t)
	if _, err := c.Get(ctx, "nope"); !errors.Is(err, ErrWorkspaceNotFound) {
		t.Fatalf("Get: got %v, want ErrWorkspaceNotFound", err)
	}
	for name, call := range map[string]func() error{
		"Start":   func() error { _, err := c.Start(ctx, "nope"); return err },
		"Sleep":   func() error { _, err := c.Sleep(ctx, "nope"); return err },
		"Wakeup":  func() error { _, err := c.Wakeup(ctx, "nope"); return err },
		"Stop":    func() error { _, err := c.Stop(ctx, "nope"); return err },
		"Restore": func() error { _, err := c.Restore(ctx, "nope"); return err },
	} {
		if err := call(); !errors.Is(err, ErrWorkspaceNotFound) {
			t.Fatalf("%s: got %v, want ErrWorkspaceNotFound", name, err)
		}
	}
}

func TestList(t *testing.T) {
	ctx := context.Background()
	c, _, _ := testSetup(t)
	mustCreate(t, c)
	mustCreate(t, c)
	list, err := c.List(ctx)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(list) != 2 {
		t.Fatalf("List: got %d workspaces, want 2", len(list))
	}
}
