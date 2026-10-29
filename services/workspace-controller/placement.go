package controller

import (
	"context"
	"fmt"
	"time"

	"github.com/phaladamou/personal-cloud/packages/protocol"
	"github.com/phaladamou/personal-cloud/packages/types"
)

// Scheduler reserves and frees node capacity for workspaces. The
// scheduler package implements it; the interface keeps the controller
// decoupled from placement.
type Scheduler interface {
	Schedule(ctx context.Context, req types.Resource, selector map[string]string) (types.Node, error)
	Release(ctx context.Context, nodeID string, req types.Resource) (types.Node, error)
}

// WithScheduler configures the placer used by Start and Restore. Without
// it, workspaces start unplaced (NodeID empty) on the local backend,
// whatever their recorded requirements.
func WithScheduler(s Scheduler) Option {
	return func(c *Controller) { c.sched = s }
}

// PlacementRequest carries the compute requirements recorded on a
// workspace: resources to reserve plus a node label selector.
type PlacementRequest struct {
	Resources types.Resource
	Selector  map[string]string
}

// Empty reports whether the request carries no requirements.
func (r PlacementRequest) Empty() bool {
	return r.Resources == (types.Resource{}) && len(r.Selector) == 0
}

// copySelector duplicates a label selector, preserving nil.
func copySelector(in map[string]string) map[string]string {
	if in == nil {
		return nil
	}
	out := make(map[string]string, len(in))
	for k, v := range in {
		out[k] = v
	}
	return out
}

// CreateWithResources registers a new workspace in CREATED status with
// the given placement requirements recorded on it. Placement itself
// happens on Start; a negative request is rejected.
func (c *Controller) CreateWithResources(ctx context.Context, userID, name string, req PlacementRequest) (types.Workspace, error) {
	if req.Resources.Negative() {
		return types.Workspace{}, fmt.Errorf("controller: negative resource request %v", req.Resources)
	}
	id, err := generateID()
	if err != nil {
		return types.Workspace{}, fmt.Errorf("controller: generate id: %w", err)
	}
	ws, err := types.NewWorkspace(id, userID, name)
	if err != nil {
		return types.Workspace{}, err
	}
	ws.Resources = req.Resources
	ws.Selector = copySelector(req.Selector)
	if err := c.store.Create(ctx, ws); err != nil {
		return types.Workspace{}, err
	}
	c.emit(protocol.EventWorkspaceCreated, ws)
	return ws, nil
}

// StartWithResources records req on the workspace, then starts it.
// The status must allow Start; otherwise nothing is persisted.
func (c *Controller) StartWithResources(ctx context.Context, id string, req PlacementRequest) (types.Workspace, error) {
	if req.Resources.Negative() {
		return types.Workspace{}, fmt.Errorf("controller: negative resource request %v", req.Resources)
	}
	ws, err := c.store.Get(ctx, id)
	if err != nil {
		return types.Workspace{}, err
	}
	if ws.Status != types.WorkspaceCreated && ws.Status != types.WorkspaceStopped {
		return types.Workspace{}, fmt.Errorf("%w: cannot start workspace in %s", ErrIllegalTransition, ws.Status)
	}
	ws.Resources = req.Resources
	ws.Selector = copySelector(req.Selector)
	ws.UpdatedAt = time.Now().UTC()
	if err := c.store.Update(ctx, ws); err != nil {
		return types.Workspace{}, err
	}
	return c.Start(ctx, id)
}

// place reserves capacity for ws when a scheduler is configured and the
// workspace wants placement, persisting the assigned NodeID. Without a
// scheduler or requirements it is a no-op. A stale NodeID is released
// first so a re-placement never leaks. On scheduling failure the
// workspace is left untouched (status and placement).
func (c *Controller) place(ctx context.Context, ws types.Workspace) (types.Workspace, error) {
	if c.sched == nil || !ws.WantsPlacement() {
		return ws, nil
	}
	if ws.NodeID != "" {
		_, _ = c.sched.Release(ctx, ws.NodeID, ws.Resources)
		ws.NodeID = ""
	}
	n, err := c.sched.Schedule(ctx, ws.Resources, ws.Selector)
	if err != nil {
		return types.Workspace{}, err
	}
	ws.NodeID = n.ID
	ws.UpdatedAt = time.Now().UTC()
	if err := c.store.Update(ctx, ws); err != nil {
		_, _ = c.sched.Release(ctx, n.ID, ws.Resources)
		ws.NodeID = ""
		return types.Workspace{}, err
	}
	return ws, nil
}

// unplace frees the scheduler reservation of ws and clears its NodeID,
// persisting the result. Without a placement it is a no-op. The release
// is best-effort (the node may be gone) but the record always converges
// to unplaced.
func (c *Controller) unplace(ctx context.Context, ws types.Workspace) (types.Workspace, error) {
	if ws.NodeID == "" {
		return ws, nil
	}
	if c.sched != nil {
		_, _ = c.sched.Release(ctx, ws.NodeID, ws.Resources)
	}
	ws.NodeID = ""
	ws.UpdatedAt = time.Now().UTC()
	if err := c.store.Update(ctx, ws); err != nil {
		return types.Workspace{}, err
	}
	return ws, nil
}
