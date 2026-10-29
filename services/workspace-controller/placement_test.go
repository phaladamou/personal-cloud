package controller

import (
	"context"
	"errors"
	"path/filepath"
	"testing"

	"github.com/phaladamou/personal-cloud/compute/node"
	"github.com/phaladamou/personal-cloud/packages/protocol"
	"github.com/phaladamou/personal-cloud/packages/types"
	"github.com/phaladamou/personal-cloud/services/scheduler"
)

// placedSetup wires a controller over a scheduler with two nodes:
// node-a (small) and node-b (big, GPU).
func placedSetup(t *testing.T) (*Controller, *node.Registry) {
	t.Helper()
	ctx := context.Background()
	store, err := NewFileStore(filepath.Join(t.TempDir(), "ws.json"))
	if err != nil {
		t.Fatalf("NewFileStore: %v", err)
	}
	r := node.NewRegistry(nil)
	for _, spec := range []node.Spec{
		{ID: "node-a", Name: "a", CPU: 4, MemoryMB: 8192, DiskGB: 100},
		{ID: "node-b", Name: "b", CPU: 16, MemoryMB: 65536, DiskGB: 500, GPUs: 2,
			Labels: map[string]string{"zone": "east"}},
	} {
		if _, err := r.Register(ctx, spec); err != nil {
			t.Fatalf("Register %s: %v", spec.ID, err)
		}
	}
	c := NewController(store, &FakeBackend{}, protocol.NewEventBus(), WithScheduler(scheduler.New(r)))
	return c, r
}

func smallReq() PlacementRequest {
	return PlacementRequest{Resources: types.Resource{CPU: 2, MemoryMB: 1024, DiskGB: 10}}
}

func TestPlacementRoundTrip(t *testing.T) {
	ctx := context.Background()
	c, r := placedSetup(t)

	ws, err := c.CreateWithResources(ctx, "user-1", "dev", smallReq())
	if err != nil {
		t.Fatalf("CreateWithResources: %v", err)
	}
	if ws.NodeID != "" {
		t.Fatalf("created NodeID: got %q, want empty (placement happens on Start)", ws.NodeID)
	}
	if ws.Resources != smallReq().Resources {
		t.Fatalf("created resources: got %v", ws.Resources)
	}

	got, err := c.Start(ctx, ws.ID)
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	if got.NodeID != "node-a" {
		t.Fatalf("placed NodeID: got %q, want node-a", got.NodeID)
	}
	stored, _ := r.Get(ctx, "node-a")
	if stored.Allocated != smallReq().Resources {
		t.Fatalf("allocated on node-a: got %v", stored.Allocated)
	}

	stopped, err := c.Stop(ctx, ws.ID)
	if err != nil {
		t.Fatalf("Stop: %v", err)
	}
	if stopped.NodeID != "" {
		t.Fatalf("stopped NodeID: got %q, want empty", stopped.NodeID)
	}
	stored, _ = r.Get(ctx, "node-a")
	if stored.Allocated != (types.Resource{}) {
		t.Fatalf("allocated after Stop: got %v, want zero", stored.Allocated)
	}

	// Restart re-places on the freed node.
	again, err := c.Start(ctx, ws.ID)
	if err != nil {
		t.Fatalf("Start again: %v", err)
	}
	if again.NodeID != "node-a" {
		t.Fatalf("re-placed NodeID: got %q, want node-a", again.NodeID)
	}
}

func TestStartWithoutSchedulerLeavesUnplaced(t *testing.T) {
	ctx := context.Background()
	c, _, _ := testSetup(t) // No scheduler.

	ws, err := c.CreateWithResources(ctx, "user-1", "dev", smallReq())
	if err != nil {
		t.Fatalf("CreateWithResources: %v", err)
	}
	got, err := c.Start(ctx, ws.ID)
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	if got.NodeID != "" {
		t.Fatalf("NodeID without scheduler: got %q, want empty", got.NodeID)
	}
}

func TestStartNoFitLeavesUntouched(t *testing.T) {
	ctx := context.Background()
	c, _ := placedSetup(t)

	huge := PlacementRequest{Resources: types.Resource{CPU: 1024, MemoryMB: 1 << 30, DiskGB: 1 << 20}}
	ws, err := c.CreateWithResources(ctx, "user-1", "dev", huge)
	if err != nil {
		t.Fatalf("CreateWithResources: %v", err)
	}
	if _, err := c.Start(ctx, ws.ID); !errors.Is(err, scheduler.ErrNoFit) {
		t.Fatalf("Start oversized: got %v, want ErrNoFit", err)
	}
	got, err := c.Get(ctx, ws.ID)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got.Status != types.WorkspaceCreated || got.NodeID != "" {
		t.Fatalf("workspace after no-fit: got %+v, want CREATED unplaced", got)
	}
}

func TestStartWithResources(t *testing.T) {
	ctx := context.Background()
	c, _ := placedSetup(t)

	ws, err := c.Create(ctx, "user-1", "dev")
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	got, err := c.StartWithResources(ctx, ws.ID, PlacementRequest{
		Resources: types.Resource{CPU: 1, MemoryMB: 64, DiskGB: 1},
		Selector:  map[string]string{"zone": "east"},
	})
	if err != nil {
		t.Fatalf("StartWithResources: %v", err)
	}
	if got.NodeID != "node-b" {
		t.Fatalf("placed NodeID: got %q, want node-b (selector)", got.NodeID)
	}
	// The requirements are recorded for later restarts.
	stored, _ := c.Get(ctx, ws.ID)
	if stored.Selector["zone"] != "east" || stored.Resources.CPU != 1 {
		t.Fatalf("recorded requirements: got %+v", stored)
	}
	// Illegal status persists nothing.
	if _, err := c.StartWithResources(ctx, ws.ID, smallReq()); !errors.Is(err, ErrIllegalTransition) {
		t.Fatalf("StartWithResources while RUNNING: got %v, want ErrIllegalTransition", err)
	}
}

func TestStartBootFailureReleasesReservation(t *testing.T) {
	ctx := context.Background()
	c, r := placedSetup(t)
	ws, err := c.CreateWithResources(ctx, "user-1", "dev", smallReq())
	if err != nil {
		t.Fatalf("CreateWithResources: %v", err)
	}
	c.backend.(*FakeBackend).StartErr = errors.New("boom")
	if _, err := c.Start(ctx, ws.ID); err == nil {
		t.Fatal("Start with backend failure: got nil, want error")
	}
	got, _ := c.Get(ctx, ws.ID)
	if got.Status != types.WorkspaceStopped || got.NodeID != "" {
		t.Fatalf("workspace after failed Start: got %+v, want STOPPED unplaced", got)
	}
	stored, _ := r.Get(ctx, "node-a")
	if stored.Allocated != (types.Resource{}) {
		t.Fatalf("allocated after failed Start: got %v, want zero", stored.Allocated)
	}
}

func TestDeleteSleepingReleasesReservation(t *testing.T) {
	ctx := context.Background()
	c, r := placedSetup(t)
	ws, err := c.CreateWithResources(ctx, "user-1", "dev", smallReq())
	if err != nil {
		t.Fatalf("CreateWithResources: %v", err)
	}
	if _, err := c.Start(ctx, ws.ID); err != nil {
		t.Fatalf("Start: %v", err)
	}
	if _, err := c.Sleep(ctx, ws.ID); err != nil {
		t.Fatalf("Sleep: %v", err)
	}
	// Sleep keeps the reservation; Delete frees it.
	stored, _ := r.Get(ctx, "node-a")
	if stored.Allocated != smallReq().Resources {
		t.Fatalf("allocated while SLEEPING: got %v", stored.Allocated)
	}
	if err := c.Delete(ctx, ws.ID); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	stored, _ = r.Get(ctx, "node-a")
	if stored.Allocated != (types.Resource{}) {
		t.Fatalf("allocated after Delete: got %v, want zero", stored.Allocated)
	}
}

func TestRestoreReplacesAfterStop(t *testing.T) {
	ctx := context.Background()
	c, r := placedSetup(t)
	ws, err := c.CreateWithResources(ctx, "user-1", "dev", smallReq())
	if err != nil {
		t.Fatalf("CreateWithResources: %v", err)
	}
	if _, err := c.Start(ctx, ws.ID); err != nil {
		t.Fatalf("Start: %v", err)
	}
	if _, err := c.Stop(ctx, ws.ID); err != nil {
		t.Fatalf("Stop: %v", err)
	}
	got, err := c.Restore(ctx, ws.ID)
	if err != nil {
		t.Fatalf("Restore: %v", err)
	}
	if got.NodeID != "node-a" {
		t.Fatalf("restored NodeID: got %q, want node-a", got.NodeID)
	}
	stored, _ := r.Get(ctx, "node-a")
	if stored.Allocated != smallReq().Resources {
		t.Fatalf("allocated after Restore: got %v", stored.Allocated)
	}
}

func TestPlacementGPU(t *testing.T) {
	ctx := context.Background()
	c, _ := placedSetup(t)
	ws, err := c.CreateWithResources(ctx, "user-1", "gpu-job",
		PlacementRequest{Resources: types.Resource{CPU: 2, MemoryMB: 4096, DiskGB: 10, GPUs: 1}})
	if err != nil {
		t.Fatalf("CreateWithResources: %v", err)
	}
	got, err := c.Start(ctx, ws.ID)
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	if got.NodeID != "node-b" {
		t.Fatalf("GPU NodeID: got %q, want node-b", got.NodeID)
	}
}

func TestPlacementNegativeRejected(t *testing.T) {
	ctx := context.Background()
	c, _ := placedSetup(t)
	bad := PlacementRequest{Resources: types.Resource{CPU: -1}}
	if _, err := c.CreateWithResources(ctx, "user-1", "dev", bad); err == nil {
		t.Fatal("CreateWithResources negative: got nil, want error")
	}
	ws, err := c.Create(ctx, "user-1", "dev")
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if _, err := c.StartWithResources(ctx, ws.ID, bad); err == nil {
		t.Fatal("StartWithResources negative: got nil, want error")
	}
}
