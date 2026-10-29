package node

import (
	"context"
	"errors"
	"testing"

	"github.com/phaladamou/personal-cloud/packages/protocol"
	"github.com/phaladamou/personal-cloud/packages/types"
)

func TestRegisterUnregister(t *testing.T) {
	ctx := context.Background()
	bus := protocol.NewEventBus()
	var connected, disconnected []protocol.Event
	bus.Subscribe(protocol.EventNodeConnected, func(e protocol.Event) { connected = append(connected, e) })
	bus.Subscribe(protocol.EventNodeDisconnected, func(e protocol.Event) { disconnected = append(disconnected, e) })
	r := NewRegistry(bus)

	n, err := r.Register(ctx, Spec{Name: "gpu-1", CPU: 8, MemoryMB: 32768, DiskGB: 500, GPUs: 1,
		Labels: map[string]string{"zone": "eu"}})
	if err != nil {
		t.Fatalf("Register: %v", err)
	}
	if n.ID == "" || n.Name != "gpu-1" || n.Status != types.NodeOnline {
		t.Fatalf("Register fields: got %+v", n)
	}
	if n.CPU != 8 || n.MemoryMB != 32768 || n.DiskGB != 500 || n.GPUs != 1 {
		t.Fatalf("Register resources: got %+v", n)
	}
	if n.Labels["zone"] != "eu" {
		t.Fatalf("Register labels: got %+v", n.Labels)
	}
	if len(connected) != 1 || connected[0].WorkspaceID != "" {
		t.Fatalf("connected events: got %+v, want 1 cluster-scoped", connected)
	}
	var payload types.Node
	if err := connected[0].DecodePayload(&payload); err != nil {
		t.Fatalf("connected payload: %v", err)
	}
	if payload.ID != n.ID || payload.Status != types.NodeOnline {
		t.Fatalf("connected payload: got %+v", payload)
	}

	got, err := r.Get(ctx, n.ID)
	if err != nil || got.ID != n.ID {
		t.Fatalf("Get: got %+v, %v", got, err)
	}
	list, err := r.List(ctx)
	if err != nil || len(list) != 1 || list[0].ID != n.ID {
		t.Fatalf("List: got %+v, %v", list, err)
	}

	if err := r.Unregister(ctx, n.ID); err != nil {
		t.Fatalf("Unregister: %v", err)
	}
	if len(disconnected) != 1 {
		t.Fatalf("disconnected events: got %d, want 1", len(disconnected))
	}
	if err := disconnected[0].DecodePayload(&payload); err != nil {
		t.Fatalf("disconnected payload: %v", err)
	}
	if payload.ID != n.ID {
		t.Fatalf("disconnected payload: got %+v", payload)
	}
	if _, err := r.Get(ctx, n.ID); !errors.Is(err, ErrNodeNotFound) {
		t.Fatalf("Get after Unregister = %v, want ErrNodeNotFound", err)
	}
	if err := r.Unregister(ctx, n.ID); !errors.Is(err, ErrNodeNotFound) {
		t.Fatalf("Unregister twice = %v, want ErrNodeNotFound", err)
	}
	if list, _ := r.List(ctx); len(list) != 0 {
		t.Fatalf("List after Unregister = %+v, want empty", list)
	}
}

func TestRegisterValidation(t *testing.T) {
	ctx := context.Background()
	bus := protocol.NewEventBus()
	var connected int
	bus.Subscribe(protocol.EventNodeConnected, func(protocol.Event) { connected++ })
	r := NewRegistry(bus)

	for name, spec := range map[string]Spec{
		"empty name":  {CPU: 2, MemoryMB: 1024, DiskGB: 10},
		"zero cpu":    {Name: "n", MemoryMB: 1024, DiskGB: 10},
		"zero memory": {Name: "n", CPU: 2, DiskGB: 10},
		"zero disk":   {Name: "n", CPU: 2, MemoryMB: 1024},
		"neg gpus":    {Name: "n", CPU: 2, MemoryMB: 1024, DiskGB: 10, GPUs: -1},
	} {
		if _, err := r.Register(ctx, spec); err == nil {
			t.Fatalf("%s: want error, got nil", name)
		}
	}
	if connected != 0 {
		t.Fatalf("connected events: got %d, want 0", connected)
	}
}

func TestRegistryNilBus(t *testing.T) {
	ctx := context.Background()
	r := NewRegistry(nil)

	n, err := r.Register(ctx, Spec{Name: "n", CPU: 1, MemoryMB: 512, DiskGB: 5})
	if err != nil {
		t.Fatalf("Register: %v", err)
	}
	if err := r.Unregister(ctx, n.ID); err != nil {
		t.Fatalf("Unregister: %v", err)
	}
}
