package node

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/phaladamou/personal-cloud/packages/protocol"
	"github.com/phaladamou/personal-cloud/packages/types"
)

// testClock is a manually advanced time source.
type testClock struct{ t time.Time }

func (c *testClock) now() time.Time { return c.t }

func testSpec(name string) Spec {
	return Spec{Name: name, CPU: 8, MemoryMB: 32768, DiskGB: 500, GPUs: 1}
}

func TestHeartbeatKeepsOnline(t *testing.T) {
	ctx := context.Background()
	clock := &testClock{t: time.Now().UTC()}
	bus := protocol.NewEventBus()
	var connected int
	bus.Subscribe(protocol.EventNodeConnected, func(protocol.Event) { connected++ })
	r := NewRegistry(bus, WithClock(clock.now), WithHeartbeatTimeout(time.Minute))

	n, err := r.Register(ctx, testSpec("n1"))
	if err != nil {
		t.Fatalf("Register: %v", err)
	}
	if n.LastHeartbeat.IsZero() {
		t.Fatal("Register left LastHeartbeat unset")
	}
	// A fresh heartbeat refreshes the timestamp without re-emitting.
	clock.t = clock.t.Add(10 * time.Second)
	got, err := r.Heartbeat(ctx, n.ID)
	if err != nil {
		t.Fatalf("Heartbeat: %v", err)
	}
	if !got.LastHeartbeat.Equal(clock.t) || got.Status != types.NodeOnline {
		t.Fatalf("Heartbeat: got %+v", got)
	}
	if connected != 1 {
		t.Fatalf("connected events: got %d, want 1 (no re-emit)", connected)
	}
	if _, err := r.Heartbeat(ctx, "node-missing"); !errors.Is(err, ErrNodeNotFound) {
		t.Fatalf("Heartbeat unknown = %v, want ErrNodeNotFound", err)
	}
}

func TestSweepExpiryAndReconnect(t *testing.T) {
	ctx := context.Background()
	clock := &testClock{t: time.Now().UTC()}
	bus := protocol.NewEventBus()
	var connected, disconnected int
	bus.Subscribe(protocol.EventNodeConnected, func(protocol.Event) { connected++ })
	bus.Subscribe(protocol.EventNodeDisconnected, func(protocol.Event) { disconnected++ })
	r := NewRegistry(bus, WithClock(clock.now), WithHeartbeatTimeout(time.Minute))

	n, err := r.Register(ctx, testSpec("n1"))
	if err != nil {
		t.Fatalf("Register: %v", err)
	}
	// Fresh node survives the sweep.
	if stale, err := r.Sweep(ctx); err != nil || len(stale) != 0 {
		t.Fatalf("Sweep fresh: got %v, %v", stale, err)
	}
	// Past the timeout the node goes OFFLINE with an event.
	clock.t = clock.t.Add(61 * time.Second)
	stale, err := r.Sweep(ctx)
	if err != nil || len(stale) != 1 || stale[0].ID != n.ID {
		t.Fatalf("Sweep stale: got %v, %v", stale, err)
	}
	if stale[0].Status != types.NodeOffline || disconnected != 1 {
		t.Fatalf("Sweep stale: got %+v, disconnected=%d", stale[0], disconnected)
	}
	// A second sweep is quiet: no repeated transition.
	if stale, err := r.Sweep(ctx); err != nil || len(stale) != 0 {
		t.Fatalf("Sweep twice: got %v, %v", stale, err)
	}
	if disconnected != 1 {
		t.Fatalf("disconnected events: got %d, want 1", disconnected)
	}
	// Heartbeat reconnects the node with a fresh event.
	back, err := r.Heartbeat(ctx, n.ID)
	if err != nil || back.Status != types.NodeOnline {
		t.Fatalf("Heartbeat reconnect: got %+v, %v", back, err)
	}
	if connected != 2 {
		t.Fatalf("connected events: got %d, want 2", connected)
	}
	if stale, _ := r.Sweep(ctx); len(stale) != 0 {
		t.Fatalf("Sweep after reconnect: got %v, want none", stale)
	}
}

func TestSweepTimeoutConfigurable(t *testing.T) {
	ctx := context.Background()
	clock := &testClock{t: time.Now().UTC()}
	r := NewRegistry(nil, WithClock(clock.now), WithHeartbeatTimeout(time.Hour))
	n, err := r.Register(ctx, testSpec("n1"))
	if err != nil {
		t.Fatalf("Register: %v", err)
	}
	clock.t = clock.t.Add(30 * time.Minute)
	if stale, _ := r.Sweep(ctx); len(stale) != 0 {
		t.Fatalf("Sweep within long timeout: got %v", stale)
	}
	clock.t = clock.t.Add(31 * time.Minute)
	stale, _ := r.Sweep(ctx)
	if len(stale) != 1 || stale[0].ID != n.ID {
		t.Fatalf("Sweep past long timeout: got %v", stale)
	}
}

func TestReregisterSameID(t *testing.T) {
	ctx := context.Background()
	clock := &testClock{t: time.Now().UTC()}
	bus := protocol.NewEventBus()
	var connected int
	bus.Subscribe(protocol.EventNodeConnected, func(protocol.Event) { connected++ })
	r := NewRegistry(bus, WithClock(clock.now), WithHeartbeatTimeout(time.Minute))

	first, err := r.Register(ctx, Spec{
		ID: "node-fixed", Name: "n1", CPU: 4, MemoryMB: 8192, DiskGB: 100,
		Labels: map[string]string{"zone": "eu"},
	})
	if err != nil {
		t.Fatalf("Register: %v", err)
	}
	if first.ID != "node-fixed" {
		t.Fatalf("Register pinned id: got %q", first.ID)
	}
	if _, err := r.Allocate(ctx, first.ID, types.Resource{CPU: 2, MemoryMB: 1024, DiskGB: 10}); err != nil {
		t.Fatalf("Allocate: %v", err)
	}
	// Let the node expire, then re-register the same identity with
	// bigger capacity: it comes back ONLINE, keeps its allocation,
	// and refreshes metadata.
	clock.t = clock.t.Add(61 * time.Second)
	if stale, _ := r.Sweep(ctx); len(stale) != 1 {
		t.Fatalf("Sweep: got %v, want 1 stale", stale)
	}
	second, err := r.Register(ctx, Spec{
		ID: "node-fixed", Name: "n1-bigger", CPU: 16, MemoryMB: 65536, DiskGB: 1000, GPUs: 2,
		Labels: map[string]string{"zone": "us"},
	})
	if err != nil {
		t.Fatalf("re-register: %v", err)
	}
	if second.ID != first.ID || second.CreatedAt != first.CreatedAt {
		t.Fatalf("re-register identity: got %+v, want %+v kept", second, first)
	}
	if second.Status != types.NodeOnline || second.CPU != 16 || second.Labels["zone"] != "us" {
		t.Fatalf("re-register refresh: got %+v", second)
	}
	if second.Allocated.CPU != 2 || second.Allocated.MemoryMB != 1024 {
		t.Fatalf("re-register kept allocation: got %+v", second.Allocated)
	}
	if !second.LastHeartbeat.Equal(clock.t) {
		t.Fatalf("re-register heartbeat: got %v, want %v", second.LastHeartbeat, clock.t)
	}
	if connected != 2 {
		t.Fatalf("connected events: got %d, want 2", connected)
	}
	if list, _ := r.List(ctx); len(list) != 1 {
		t.Fatalf("List after re-register: got %d nodes, want 1", len(list))
	}
}

func TestAllocateRelease(t *testing.T) {
	ctx := context.Background()
	r := NewRegistry(nil)

	n, err := r.Register(ctx, testSpec("n1"))
	if err != nil {
		t.Fatalf("Register: %v", err)
	}
	if got := n.Allocatable(); got.CPU != 8 || got.MemoryMB != 32768 || got.DiskGB != 500 || got.GPUs != 1 {
		t.Fatalf("initial allocatable: got %v", got)
	}
	placed, err := r.Allocate(ctx, n.ID, types.Resource{CPU: 3, MemoryMB: 4096, DiskGB: 100, GPUs: 1})
	if err != nil {
		t.Fatalf("Allocate: %v", err)
	}
	if got := placed.Allocatable(); got.CPU != 5 || got.MemoryMB != 28672 || got.DiskGB != 400 || got.GPUs != 0 {
		t.Fatalf("allocatable after place: got %v", got)
	}
	// Overflowing the GPU share fails without touching the node.
	if _, err := r.Allocate(ctx, n.ID, types.Resource{GPUs: 1}); !errors.Is(err, ErrInsufficientCapacity) {
		t.Fatalf("Allocate overflow = %v, want ErrInsufficientCapacity", err)
	}
	if _, err := r.Allocate(ctx, n.ID, types.Resource{CPU: -1}); err == nil {
		t.Fatal("Allocate negative: want error, got nil")
	}
	if got, _ := r.Get(ctx, n.ID); got.Allocated.CPU != 3 {
		t.Fatalf("failed Allocate mutated node: got %+v", got.Allocated)
	}
	// Release frees room; over-release clamps at zero.
	freed, err := r.Release(ctx, n.ID, types.Resource{CPU: 1, MemoryMB: 1024, DiskGB: 50, GPUs: 1})
	if err != nil {
		t.Fatalf("Release: %v", err)
	}
	if got := freed.Allocatable(); got.CPU != 6 || got.GPUs != 1 {
		t.Fatalf("allocatable after release: got %v", got)
	}
	freed, err = r.Release(ctx, n.ID, types.Resource{CPU: 99, MemoryMB: 1 << 40, DiskGB: 99999, GPUs: 9})
	if err != nil {
		t.Fatalf("over-release: %v", err)
	}
	if freed.Allocated != (types.Resource{}) {
		t.Fatalf("over-release clamped: got %+v", freed.Allocated)
	}
	if _, err := r.Release(ctx, n.ID, types.Resource{CPU: -1}); err == nil {
		t.Fatal("Release negative: want error, got nil")
	}
	if _, err := r.Allocate(ctx, "node-missing", types.Resource{CPU: 1}); !errors.Is(err, ErrNodeNotFound) {
		t.Fatalf("Allocate unknown = %v, want ErrNodeNotFound", err)
	}
}

func TestAllocateRequiresOnline(t *testing.T) {
	ctx := context.Background()
	clock := &testClock{t: time.Now().UTC()}
	r := NewRegistry(nil, WithClock(clock.now), WithHeartbeatTimeout(time.Minute))
	n, err := r.Register(ctx, testSpec("n1"))
	if err != nil {
		t.Fatalf("Register: %v", err)
	}
	clock.t = clock.t.Add(61 * time.Second)
	if _, err := r.Sweep(ctx); err != nil {
		t.Fatalf("Sweep: %v", err)
	}
	if _, err := r.Allocate(ctx, n.ID, types.Resource{CPU: 1}); !errors.Is(err, ErrNodeNotReady) {
		t.Fatalf("Allocate offline = %v, want ErrNodeNotReady", err)
	}
	// Release still works on an offline node: freeing capacity is safe.
	if _, err := r.Release(ctx, n.ID, types.Resource{CPU: 1}); err != nil {
		t.Fatalf("Release offline: %v", err)
	}
}

func TestFind(t *testing.T) {
	ctx := context.Background()
	clock := &testClock{t: time.Now().UTC()}
	r := NewRegistry(nil, WithClock(clock.now), WithHeartbeatTimeout(time.Minute))

	a, err := r.Register(ctx, Spec{ID: "node-a", Name: "a", CPU: 8, MemoryMB: 16384, DiskGB: 200,
		Labels: map[string]string{"zone": "eu", "gpu": "true"}})
	if err != nil {
		t.Fatalf("Register a: %v", err)
	}
	b, err := r.Register(ctx, Spec{ID: "node-b", Name: "b", CPU: 2, MemoryMB: 4096, DiskGB: 50,
		Labels: map[string]string{"zone": "us"}})
	if err != nil {
		t.Fatalf("Register b: %v", err)
	}
	// Fill node b so only a has room for a big request.
	if _, err := r.Allocate(ctx, b.ID, types.Resource{CPU: 2, MemoryMB: 4096, DiskGB: 50}); err != nil {
		t.Fatalf("Allocate b: %v", err)
	}
	found, err := r.Find(ctx, types.Resource{CPU: 4, MemoryMB: 8192, DiskGB: 100}, nil)
	if err != nil || len(found) != 1 || found[0].ID != a.ID {
		t.Fatalf("Find capacity: got %+v, %v", found, err)
	}
	found, err = r.Find(ctx, types.Resource{CPU: 1}, map[string]string{"zone": "us"})
	if err != nil || len(found) != 0 {
		t.Fatalf("Find us selector on full node: got %+v, %v", found, err)
	}
	found, err = r.Find(ctx, types.Resource{CPU: 1}, map[string]string{"zone": "eu", "gpu": "true"})
	if err != nil || len(found) != 1 || found[0].ID != a.ID {
		t.Fatalf("Find label selector: got %+v, %v", found, err)
	}
	// An expired node drops out of Find until its next heartbeat.
	clock.t = clock.t.Add(61 * time.Second)
	if _, err := r.Heartbeat(ctx, b.ID); err != nil {
		t.Fatalf("Heartbeat b: %v", err)
	}
	if _, err := r.Sweep(ctx); err != nil {
		t.Fatalf("Sweep: %v", err)
	}
	found, err = r.Find(ctx, types.Resource{CPU: 1}, nil)
	if err != nil {
		t.Fatalf("Find: %v", err)
	}
	for _, n := range found {
		if n.ID == a.ID {
			t.Fatalf("Find returned stale node a: %+v", found)
		}
	}
}
