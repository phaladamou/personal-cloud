package controller

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"sync"

	"github.com/phaladamou/personal-cloud/packages/protocol"
	"github.com/phaladamou/personal-cloud/packages/types"
	"github.com/phaladamou/personal-cloud/runtime/container"
	"github.com/phaladamou/personal-cloud/storage/snapshots"
	"github.com/phaladamou/personal-cloud/storage/volumes"
)

// ErrIllegalTransition is returned when an operation is not allowed in
// the workspace current status (for example starting a running
// workspace, or executing a command outside RUNNING).
var ErrIllegalTransition = errors.New("controller: illegal transition")

// defaultInitCommand keeps a workspace container alive. The local
// backend runs it as a plain OS process, so it must exist on the host.
var defaultInitCommand = []string{"sleep", "infinity"}

// Controller reconciles workspace lifecycle state.
//
// Desired state lives in the Store; actual workload state lives behind
// the container.ContainerBackend. Every method is a guarded transition
// over the types.WorkspaceStatus enum:
//
//	Create:  - -> CREATED (emits workspace.created)
//	Start:   CREATED|STOPPED -> STARTING -> RUNNING (emits workspace.started)
//	Sleep:   RUNNING -> SLEEPING
//	Wakeup:  SLEEPING -> RUNNING (emits workspace.started)
//	Stop:    RUNNING|SLEEPING|STARTING -> STOPPED (emits workspace.stopped)
//	Restore: STOPPED -> RESTORING -> RUNNING (emits workspace.started)
//	Delete:  CREATED|STOPPED|SLEEPING -> -
//
// Start and Restore persist their intermediate state before touching the
// runtime, so observers can see STARTING/RESTORING; on runtime failure
// they fall back to STOPPED. The other operations drive the runtime
// first and only persist the transition on success, leaving state
// untouched on failure.
//
// Only transitions with a matching protocol.EventType emit an event;
// there is deliberately no event for sleep, restore intermediates, or
// delete. The workspace id -> container id mapping is in memory only in
// Phase 1: a controller restart re-creates containers on next Start.
//
// Storage (see storage.go) is optional: volume and snapshot operations
// delegate to the configured managers and CreateSnapshot emits
// snapshot.created. Without managers they return ErrVolumesUnavailable
// / ErrSnapshotsUnavailable.
//
// Files (see files.go) are optional too: file operations delegate to
// the configured FilesystemProvider and emit file.changed on every
// mutation. Without a provider they return ErrFilesystemsUnavailable.
//
// Processes (see processes.go) list the container processes and stream
// their logs. Exec emits process.started, process.stdout and
// process.exited for every command.
//
// Placement (see placement.go) is optional: with a scheduler
// configured (WithScheduler), Start and Restore reserve the workspace
// recorded requirements on a node and persist the assigned NodeID
// before booting; Stop and Delete free the reservation. Placement
// failure leaves the workspace untouched. Without a scheduler,
// workspaces start unplaced on the local backend.
//
// A nil bus disables event publishing. Methods are safe for concurrent
// use.
type Controller struct {
	store   Store
	backend container.ContainerBackend
	bus     *protocol.EventBus
	initCmd []string
	sched   Scheduler
	vols    volumes.VolumeManager
	snaps   snapshots.SnapshotManager
	fs      FilesystemProvider

	mu         sync.Mutex
	containers map[string]string
}

// Option configures a Controller.
type Option func(*Controller)

// WithInitCommand overrides the command used to boot workspace
// containers (default: sleep infinity).
func WithInitCommand(cmd ...string) Option {
	return func(c *Controller) {
		if len(cmd) > 0 {
			c.initCmd = cmd
		}
	}
}

// NewController builds a Controller over store and backend.
func NewController(store Store, backend container.ContainerBackend, bus *protocol.EventBus, opts ...Option) *Controller {
	c := &Controller{
		store:      store,
		backend:    backend,
		bus:        bus,
		initCmd:    defaultInitCommand,
		containers: make(map[string]string),
	}
	for _, opt := range opts {
		opt(c)
	}
	return c
}

// generateID returns a random workspace id.
func generateID() (string, error) {
	var b [8]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", err
	}
	return "ws-" + hex.EncodeToString(b[:]), nil
}

// statusPayload is the event payload published on lifecycle transitions.
type statusPayload struct {
	Status types.WorkspaceStatus `json:"status"`
}

// emit publishes a lifecycle event for ws. Publishing is best-effort:
// event construction cannot fail for known types and a nil bus drops.
func (c *Controller) emit(typ protocol.EventType, ws types.Workspace) {
	ev, err := protocol.NewEvent(typ, ws.ID, statusPayload{Status: ws.Status})
	if err != nil {
		return
	}
	c.bus.Publish(ev)
}

// transition persists a status change, returning the updated workspace.
func (c *Controller) transition(ctx context.Context, ws types.Workspace, next types.WorkspaceStatus) (types.Workspace, error) {
	if err := ws.TransitionTo(next); err != nil {
		return types.Workspace{}, fmt.Errorf("%w: %w", ErrIllegalTransition, err)
	}
	if err := c.store.Update(ctx, ws); err != nil {
		return types.Workspace{}, err
	}
	return ws, nil
}

// containerFor returns the container id bound to the workspace, if any.
func (c *Controller) containerFor(workspaceID string) (string, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	id, ok := c.containers[workspaceID]
	return id, ok
}

// bindContainer records the workspace id -> container id mapping.
func (c *Controller) bindContainer(workspaceID, containerID string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.containers[workspaceID] = containerID
}

// unbindContainer drops the workspace id -> container id mapping.
func (c *Controller) unbindContainer(workspaceID string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	delete(c.containers, workspaceID)
}

// bootContainer creates (when needed) and starts the container backing
// ws, returning its id. A stale mapping (container gone from the
// backend) triggers a single re-create.
func (c *Controller) bootContainer(ctx context.Context, ws types.Workspace) (string, error) {
	if id, ok := c.containerFor(ws.ID); ok {
		if err := c.backend.Start(ctx, id); err == nil {
			return id, nil
		} else if !errors.Is(err, container.ErrNotFound) {
			return "", err
		}
		c.unbindContainer(ws.ID)
	}
	id, err := c.backend.Create(ctx, container.Config{
		Name:    ws.Name,
		Command: c.initCmd[0],
		Args:    c.initCmd[1:],
	})
	if err != nil {
		return "", err
	}
	if err := c.backend.Start(ctx, id); err != nil {
		return "", err
	}
	c.bindContainer(ws.ID, id)
	return id, nil
}

// Create registers a new workspace in CREATED status without placement
// requirements. Use CreateWithResources to record requirements.
func (c *Controller) Create(ctx context.Context, userID, name string) (types.Workspace, error) {
	return c.CreateWithResources(ctx, userID, name, PlacementRequest{})
}

// Get returns the workspace with the given id.
func (c *Controller) Get(ctx context.Context, id string) (types.Workspace, error) {
	return c.store.Get(ctx, id)
}

// List returns every workspace, ordered by id.
func (c *Controller) List(ctx context.Context) ([]types.Workspace, error) {
	return c.store.List(ctx)
}

// Start boots a CREATED or STOPPED workspace into RUNNING. With a
// scheduler configured, it places the workspace first: placement
// failure returns the scheduler error with the workspace untouched;
// a later boot failure releases the reservation and falls back to
// STOPPED.
func (c *Controller) Start(ctx context.Context, id string) (types.Workspace, error) {
	ws, err := c.store.Get(ctx, id)
	if err != nil {
		return types.Workspace{}, err
	}
	if ws.Status != types.WorkspaceCreated && ws.Status != types.WorkspaceStopped {
		return types.Workspace{}, fmt.Errorf("%w: cannot start workspace in %s", ErrIllegalTransition, ws.Status)
	}
	ws, err = c.place(ctx, ws)
	if err != nil {
		return types.Workspace{}, err
	}
	ws, err = c.transition(ctx, ws, types.WorkspaceStarting)
	if err != nil {
		return types.Workspace{}, err
	}
	if _, err := c.bootContainer(ctx, ws); err != nil {
		bootErr := err
		if unplaced, uerr := c.unplace(ctx, ws); uerr == nil {
			ws = unplaced
		}
		ws, _ = c.transition(ctx, ws, types.WorkspaceStopped)
		return types.Workspace{}, fmt.Errorf("controller: start %s: %w", id, bootErr)
	}
	ws, err = c.transition(ctx, ws, types.WorkspaceRunning)
	if err != nil {
		return types.Workspace{}, err
	}
	c.emit(protocol.EventWorkspaceStarted, ws)
	return ws, nil
}

// Sleep suspends a RUNNING workspace. The container is stopped but kept,
// so Wakeup can resume it.
func (c *Controller) Sleep(ctx context.Context, id string) (types.Workspace, error) {
	ws, err := c.store.Get(ctx, id)
	if err != nil {
		return types.Workspace{}, err
	}
	if ws.Status != types.WorkspaceRunning {
		return types.Workspace{}, fmt.Errorf("%w: cannot sleep workspace in %s", ErrIllegalTransition, ws.Status)
	}
	if ctrID, ok := c.containerFor(id); ok {
		if err := c.backend.Stop(ctx, ctrID); err != nil {
			return types.Workspace{}, fmt.Errorf("controller: sleep %s: %w", id, err)
		}
	}
	return c.transition(ctx, ws, types.WorkspaceSleeping)
}

// Wakeup resumes a SLEEPING workspace into RUNNING.
func (c *Controller) Wakeup(ctx context.Context, id string) (types.Workspace, error) {
	ws, err := c.store.Get(ctx, id)
	if err != nil {
		return types.Workspace{}, err
	}
	if ws.Status != types.WorkspaceSleeping {
		return types.Workspace{}, fmt.Errorf("%w: cannot wake workspace in %s", ErrIllegalTransition, ws.Status)
	}
	ctrID, ok := c.containerFor(id)
	if !ok {
		return types.Workspace{}, fmt.Errorf("%w: cannot wake workspace in %s without container", ErrIllegalTransition, ws.Status)
	}
	if err := c.backend.Start(ctx, ctrID); err != nil {
		return types.Workspace{}, fmt.Errorf("controller: wake %s: %w", id, err)
	}
	ws, err = c.transition(ctx, ws, types.WorkspaceRunning)
	if err != nil {
		return types.Workspace{}, err
	}
	c.emit(protocol.EventWorkspaceStarted, ws)
	return ws, nil
}

// Stop halts a RUNNING, SLEEPING, or STARTING workspace. A missing
// container counts as already stopped.
func (c *Controller) Stop(ctx context.Context, id string) (types.Workspace, error) {
	ws, err := c.store.Get(ctx, id)
	if err != nil {
		return types.Workspace{}, err
	}
	if ws.Status != types.WorkspaceRunning && ws.Status != types.WorkspaceSleeping && ws.Status != types.WorkspaceStarting {
		return types.Workspace{}, fmt.Errorf("%w: cannot stop workspace in %s", ErrIllegalTransition, ws.Status)
	}
	if ctrID, ok := c.containerFor(id); ok {
		if err := c.backend.Stop(ctx, ctrID); err != nil && !errors.Is(err, container.ErrNotFound) {
			return types.Workspace{}, fmt.Errorf("controller: stop %s: %w", id, err)
		}
	}
	ws, err = c.transition(ctx, ws, types.WorkspaceStopped)
	if err != nil {
		return types.Workspace{}, err
	}
	// The reservation is freed: stopping the workload is the primary
	// effect, so a persistence hiccup here must not fail the Stop.
	if unplaced, uerr := c.unplace(ctx, ws); uerr == nil {
		ws = unplaced
	}
	c.emit(protocol.EventWorkspaceStopped, ws)
	return ws, nil
}

// Restore brings a STOPPED workspace back to RUNNING through RESTORING.
// Like Start, it places the workspace first when a scheduler is
// configured (Stop freed the previous reservation).
func (c *Controller) Restore(ctx context.Context, id string) (types.Workspace, error) {
	ws, err := c.store.Get(ctx, id)
	if err != nil {
		return types.Workspace{}, err
	}
	if ws.Status != types.WorkspaceStopped {
		return types.Workspace{}, fmt.Errorf("%w: cannot restore workspace in %s", ErrIllegalTransition, ws.Status)
	}
	ws, err = c.place(ctx, ws)
	if err != nil {
		return types.Workspace{}, err
	}
	ws, err = c.transition(ctx, ws, types.WorkspaceRestoring)
	if err != nil {
		return types.Workspace{}, err
	}
	if _, err := c.bootContainer(ctx, ws); err != nil {
		bootErr := err
		if unplaced, uerr := c.unplace(ctx, ws); uerr == nil {
			ws = unplaced
		}
		ws, _ = c.transition(ctx, ws, types.WorkspaceStopped)
		return types.Workspace{}, fmt.Errorf("controller: restore %s: %w", id, bootErr)
	}
	ws, err = c.transition(ctx, ws, types.WorkspaceRunning)
	if err != nil {
		return types.Workspace{}, err
	}
	c.emit(protocol.EventWorkspaceStarted, ws)
	return ws, nil
}

// Delete removes a CREATED, STOPPED, or SLEEPING workspace, its
// container, its scheduler reservation (best-effort), and
// (best-effort) its volumes and snapshots. A missing container is
// ignored.
func (c *Controller) Delete(ctx context.Context, id string) error {
	ws, err := c.store.Get(ctx, id)
	if err != nil {
		return err
	}
	if ws.Status != types.WorkspaceCreated && ws.Status != types.WorkspaceStopped && ws.Status != types.WorkspaceSleeping {
		return fmt.Errorf("%w: cannot delete workspace in %s", ErrIllegalTransition, ws.Status)
	}
	if ctrID, ok := c.containerFor(id); ok {
		if err := c.backend.Remove(ctx, ctrID); err != nil && !errors.Is(err, container.ErrNotFound) {
			return fmt.Errorf("controller: delete %s: %w", id, err)
		}
		c.unbindContainer(id)
	}
	// A placed workspace (SLEEPING still holds its reservation) frees
	// it best-effort; the record goes away with the Delete below.
	if ws.NodeID != "" && c.sched != nil {
		_, _ = c.sched.Release(ctx, ws.NodeID, ws.Resources)
	}
	if err := c.store.Delete(ctx, id); err != nil {
		return err
	}
	c.cleanupStorage(ctx, id)
	return nil
}

// Exec runs a command in the workspace container. The workspace must be
// RUNNING. It emits process.started before the command runs,
// process.stdout per output chunk, and process.exited once it finishes;
// all three share the exec process id (opts.ExecID, generated when
// empty), which is also the id under Processes/ProcessLogs.
func (c *Controller) Exec(ctx context.Context, id string, opts container.ExecOptions) error {
	ws, ctrID, opts, err := c.prepareExec(ctx, id, opts)
	if err != nil {
		return err
	}
	c.emitProcess(ws.ID, protocol.EventProcessStarted,
		processStartedPayload{ProcessID: opts.ExecID, Command: opts.Cmd})
	if opts.Stdout != nil {
		opts.Stdout = eventWriter{bus: c.bus, workspaceID: ws.ID, processID: opts.ExecID, dst: opts.Stdout}
	}
	if opts.Stderr != nil {
		opts.Stderr = eventWriter{bus: c.bus, workspaceID: ws.ID, processID: opts.ExecID, dst: opts.Stderr}
	}
	runErr := c.backend.Exec(ctx, ctrID, opts)
	exited := processExitedPayload{ProcessID: opts.ExecID}
	if runErr != nil {
		exited.ExitCode = -1
		exited.Error = runErr.Error()
	}
	c.emitProcess(ws.ID, protocol.EventProcessExited, exited)
	return runErr
}
