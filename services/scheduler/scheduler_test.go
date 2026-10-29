package scheduler

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/phaladamou/personal-cloud/compute/gpu"
	"github.com/phaladamou/personal-cloud/compute/node"
	"github.com/phaladamou/personal-cloud/packages/types"
)

func mustRegister(t *testing.T, r *node.Registry, spec node.Spec) types.Node {
	t.Helper()
	n, err := r.Register(context.Background(), spec)
	if err != nil {
		t.Fatalf("Register %s: %v", spec.Name, err)
	}
	return n
}

func req(cpu int, memMB, diskGB int64, gpus int) types.Resource {
	return types.Resource{CPU: cpu, MemoryMB: memMB, DiskGB: diskGB, GPUs: gpus}
}

func TestScheduleFit(t *testing.T) {
	ctx := context.Background()
	r := node.NewRegistry(nil)
	mustRegister(t, r, node.Spec{ID: "node-a", Name: "a", CPU: 4, MemoryMB: 8192, DiskGB: 100})
	mustRegister(t, r, node.Spec{ID: "node-b", Name: "b", CPU: 4, MemoryMB: 8192, DiskGB: 100})

	got, err := New(r).Schedule(ctx, req(2, 1024, 10, 0), nil)
	if err != nil {
		t.Fatalf("Schedule: %v", err)
	}
	if got.ID != "node-a" {
		t.Fatalf("Schedule winner: got %s, want node-a (id tie-break)", got.ID)
	}
	// The reservation shrinks the winner allocatable share.
	stored, _ := r.Get(ctx, got.ID)
	if want := req(2, 1024, 10, 0); stored.Allocated != want {
		t.Fatalf("allocated on %s: got %v, want %v", got.ID, stored.Allocated, want)
	}
}

func TestScheduleNoFit(t *testing.T) {
	ctx := context.Background()
	r := node.NewRegistry(nil)
	mustRegister(t, r, node.Spec{ID: "node-a", Name: "a", CPU: 2, MemoryMB: 1024, DiskGB: 10})
	s := New(r)

	// Nothing fits: oversized request.
	if _, err := s.Schedule(ctx, req(64, 1<<20, 1000, 0), nil); !errors.Is(err, ErrNoFit) {
		t.Fatalf("Schedule oversized: got %v, want ErrNoFit", err)
	}
	// Nothing fits: empty registry.
	if _, err := New(node.NewRegistry(nil)).Schedule(ctx, req(1, 64, 1, 0), nil); !errors.Is(err, ErrNoFit) {
		t.Fatalf("Schedule on empty registry: got %v, want ErrNoFit", err)
	}
	// The failed placements reserve nothing.
	stored, _ := r.Get(ctx, "node-a")
	if stored.Allocated != (types.Resource{}) {
		t.Fatalf("allocated after no-fit: got %v, want zero", stored.Allocated)
	}
}

func TestScheduleSkipsOfflineNodes(t *testing.T) {
	ctx := context.Background()
	now := time.Now().UTC()
	current := now
	r := node.NewRegistry(nil,
		node.WithClock(func() time.Time { return current }),
		node.WithHeartbeatTimeout(time.Minute))
	// node-off is bigger but its heartbeat expires before scheduling.
	mustRegister(t, r, node.Spec{ID: "node-off", Name: "off", CPU: 64, MemoryMB: 1 << 20, DiskGB: 1000})
	mustRegister(t, r, node.Spec{ID: "node-on", Name: "on", CPU: 4, MemoryMB: 8192, DiskGB: 100})
	current = now.Add(61 * time.Second)
	if stale, err := r.Sweep(ctx); err != nil || len(stale) != 2 {
		t.Fatalf("Sweep: got %v, %v", stale, err)
	}
	// Keep node-on alive so only node-off is filtered.
	if _, err := r.Heartbeat(ctx, "node-on"); err != nil {
		t.Fatalf("Heartbeat: %v", err)
	}

	got, err := New(r).Schedule(ctx, req(2, 1024, 10, 0), nil)
	if err != nil {
		t.Fatalf("Schedule: %v", err)
	}
	if got.ID != "node-on" {
		t.Fatalf("Schedule winner: got %s, want node-on (offline filtered)", got.ID)
	}
}

func TestScheduleGPU(t *testing.T) {
	ctx := context.Background()
	r := node.NewRegistry(nil)
	mustRegister(t, r, node.Spec{ID: "node-cpu", Name: "cpu", CPU: 16, MemoryMB: 65536, DiskGB: 500})
	mustRegister(t, r, node.Spec{ID: "node-gpu", Name: "gpu", CPU: 8, MemoryMB: 32768, DiskGB: 500, GPUs: 2,
		Labels: gpu.Labels(gpu.Fake(15360, "NVIDIA T4", "NVIDIA T4"))})
	s := New(r)

	got, err := s.Schedule(ctx, req(2, 4096, 10, 1), nil)
	if err != nil {
		t.Fatalf("Schedule GPU: %v", err)
	}
	if got.ID != "node-gpu" {
		t.Fatalf("Schedule GPU winner: got %s, want node-gpu", got.ID)
	}
	// The second GPU still fits on the same node; the third fits nowhere.
	if _, err := s.Schedule(ctx, req(1, 1024, 1, 1), nil); err != nil {
		t.Fatalf("Schedule second GPU: %v", err)
	}
	if _, err := s.Schedule(ctx, req(1, 1024, 1, 1), nil); !errors.Is(err, ErrNoFit) {
		t.Fatalf("Schedule third GPU: got %v, want ErrNoFit", err)
	}
}

func TestScheduleLabels(t *testing.T) {
	ctx := context.Background()
	r := node.NewRegistry(nil)
	mustRegister(t, r, node.Spec{ID: "node-a", Name: "a", CPU: 8, MemoryMB: 32768, DiskGB: 500,
		Labels: map[string]string{"zone": "east", "gpu.model": "NVIDIA T4"}})
	mustRegister(t, r, node.Spec{ID: "node-b", Name: "b", CPU: 8, MemoryMB: 32768, DiskGB: 500,
		Labels: map[string]string{"zone": "west"}})
	s := New(r)

	got, err := s.Schedule(ctx, req(1, 64, 1, 0), map[string]string{"zone": "west"})
	if err != nil {
		t.Fatalf("Schedule: %v", err)
	}
	if got.ID != "node-b" {
		t.Fatalf("Schedule winner: got %s, want node-b (label match)", got.ID)
	}
	// A GPU model pins placement through its label.
	got, err = s.Schedule(ctx, req(1, 64, 1, 0), map[string]string{"gpu.model": "NVIDIA T4"})
	if err != nil {
		t.Fatalf("Schedule gpu.model: %v", err)
	}
	if got.ID != "node-a" {
		t.Fatalf("Schedule gpu.model winner: got %s, want node-a", got.ID)
	}
	// No node carries the selector.
	if _, err := s.Schedule(ctx, req(1, 64, 1, 0), map[string]string{"zone": "north"}); !errors.Is(err, ErrNoFit) {
		t.Fatalf("Schedule unmatched selector: got %v, want ErrNoFit", err)
	}
}

func TestScheduleLeastLoaded(t *testing.T) {
	ctx := context.Background()
	r := node.NewRegistry(nil)
	mustRegister(t, r, node.Spec{ID: "node-a", Name: "a", CPU: 8, MemoryMB: 8192, DiskGB: 100})
	mustRegister(t, r, node.Spec{ID: "node-b", Name: "b", CPU: 8, MemoryMB: 8192, DiskGB: 100})
	s := New(r)

	// Preload node-a so node-b wins despite the id tie-break.
	if _, err := r.Allocate(ctx, "node-a", req(4, 4096, 50, 0)); err != nil {
		t.Fatalf("Allocate: %v", err)
	}
	got, err := s.Schedule(ctx, req(1, 64, 1, 0), nil)
	if err != nil {
		t.Fatalf("Schedule: %v", err)
	}
	if got.ID != "node-b" {
		t.Fatalf("Schedule winner: got %s, want node-b (least-loaded)", got.ID)
	}
}

func TestScheduleTieBreakDeterministic(t *testing.T) {
	ctx := context.Background()
	r := node.NewRegistry(nil)
	// Register in reverse id order: the winner must still be the
	// smallest id on every run.
	mustRegister(t, r, node.Spec{ID: "node-c", Name: "c", CPU: 8, MemoryMB: 8192, DiskGB: 100})
	mustRegister(t, r, node.Spec{ID: "node-a", Name: "a", CPU: 8, MemoryMB: 8192, DiskGB: 100})
	mustRegister(t, r, node.Spec{ID: "node-b", Name: "b", CPU: 8, MemoryMB: 8192, DiskGB: 100})
	s := New(r)

	for i := 0; i < 3; i++ {
		got, err := s.Schedule(ctx, req(1, 64, 1, 0), nil)
		if err != nil {
			t.Fatalf("Schedule %d: %v", i, err)
		}
		// Each winner takes load, so scheduling spreads a -> b -> c.
		want := []string{"node-a", "node-b", "node-c"}[i]
		if got.ID != want {
			t.Fatalf("Schedule %d winner: got %s, want %s", i, got.ID, want)
		}
	}
}

func TestReleaseAndReschedule(t *testing.T) {
	ctx := context.Background()
	r := node.NewRegistry(nil)
	mustRegister(t, r, node.Spec{ID: "node-a", Name: "a", CPU: 2, MemoryMB: 1024, DiskGB: 10})
	s := New(r)

	first, err := s.Schedule(ctx, req(2, 1024, 10, 0), nil)
	if err != nil {
		t.Fatalf("Schedule: %v", err)
	}
	// The node is full: nothing more fits.
	if _, err := s.Schedule(ctx, req(1, 64, 1, 0), nil); !errors.Is(err, ErrNoFit) {
		t.Fatalf("Schedule on full node: got %v, want ErrNoFit", err)
	}
	// Release frees the reservation for a re-schedule.
	if _, err := s.Release(ctx, first.ID, req(2, 1024, 10, 0)); err != nil {
		t.Fatalf("Release: %v", err)
	}
	second, err := s.Schedule(ctx, req(2, 1024, 10, 0), nil)
	if err != nil {
		t.Fatalf("Re-schedule: %v", err)
	}
	if second.ID != "node-a" {
		t.Fatalf("Re-schedule winner: got %s, want node-a", second.ID)
	}
	if _, err := s.Release(ctx, "node-missing", req(1, 64, 1, 0)); !errors.Is(err, node.ErrNodeNotFound) {
		t.Fatalf("Release unknown node: got %v, want ErrNodeNotFound", err)
	}
}

func TestScheduleInvalid(t *testing.T) {
	ctx := context.Background()
	s := New(node.NewRegistry(nil))
	if _, err := s.Schedule(ctx, req(-1, 64, 1, 0), nil); err == nil {
		t.Fatal("Schedule negative: got nil, want error")
	}
	if _, err := New(nil).Schedule(ctx, req(1, 64, 1, 0), nil); err == nil {
		t.Fatal("Schedule without registry: got nil, want error")
	}
	cancelled, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := s.Schedule(cancelled, req(1, 64, 1, 0), nil); err == nil {
		t.Fatal("Schedule cancelled: got nil, want error")
	}
	if _, err := New(nil).Release(ctx, "node-a", req(1, 64, 1, 0)); err == nil {
		t.Fatal("Release without registry: got nil, want error")
	}
}
