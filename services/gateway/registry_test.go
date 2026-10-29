package gateway

import (
	"context"
	"errors"
	"testing"
)

func TestRegistryTargets(t *testing.T) {
	ctx := context.Background()
	r := NewRegistry()

	if err := r.SetTarget(ctx, "ws-1", "127.0.0.1:4001"); err != nil {
		t.Fatalf("SetTarget: %v", err)
	}
	addr, err := r.Target(ctx, "ws-1")
	if err != nil || addr != "127.0.0.1:4001" {
		t.Fatalf("Target: got %q, %v", addr, err)
	}
	// Replace.
	if err := r.SetTarget(ctx, "ws-1", "10.0.0.2:8080"); err != nil {
		t.Fatalf("SetTarget replace: %v", err)
	}
	if addr, _ := r.Target(ctx, "ws-1"); addr != "10.0.0.2:8080" {
		t.Fatalf("Target after replace: got %q", addr)
	}
	// Remove.
	if err := r.RemoveTarget(ctx, "ws-1"); err != nil {
		t.Fatalf("RemoveTarget: %v", err)
	}
	if _, err := r.Target(ctx, "ws-1"); !errors.Is(err, ErrForwardNotFound) {
		t.Fatalf("Target after remove: got %v, want ErrForwardNotFound", err)
	}
	// Unknown workspace.
	if _, err := r.Target(ctx, "nope"); !errors.Is(err, ErrForwardNotFound) {
		t.Fatalf("Target unknown: got %v, want ErrForwardNotFound", err)
	}
}

func TestRegistryTargetValidation(t *testing.T) {
	ctx := context.Background()
	r := NewRegistry()
	for name, addr := range map[string]string{
		"empty":      "",
		"no port":    "127.0.0.1",
		"empty host": ":8080",
		"bad port":   "127.0.0.1:http",
		"port zero":  "127.0.0.1:0",
		"port huge":  "127.0.0.1:99999",
	} {
		if err := r.SetTarget(ctx, "ws-1", addr); err == nil {
			t.Fatalf("SetTarget %s (%q): nil error, want failure", name, addr)
		}
	}
	if err := r.SetTarget(ctx, "  ", "127.0.0.1:8080"); err == nil {
		t.Fatal("SetTarget blank workspace: nil error, want failure")
	}
}

func TestRegistryForwards(t *testing.T) {
	ctx := context.Background()
	r := NewRegistry()

	got, err := r.Register(ctx, Forward{WorkspaceID: "ws-1", Port: 3000, Target: "127.0.0.1:4000"})
	if err != nil {
		t.Fatalf("Register: %v", err)
	}
	if got.Protocol != ProtocolTCP {
		t.Fatalf("Register protocol: got %q, want tcp default", got.Protocol)
	}
	if _, err := r.Register(ctx, Forward{WorkspaceID: "ws-1", Port: 3001, Target: "127.0.0.1:4001", Protocol: "udp"}); err != nil {
		t.Fatalf("Register udp: %v", err)
	}

	// Same port on another workspace is independent.
	if _, err := r.Register(ctx, Forward{WorkspaceID: "ws-2", Port: 3000, Target: "127.0.0.1:5000"}); err != nil {
		t.Fatalf("Register other workspace: %v", err)
	}

	// Duplicate.
	if _, err := r.Register(ctx, Forward{WorkspaceID: "ws-1", Port: 3000, Target: "127.0.0.1:4000"}); !errors.Is(err, ErrForwardExists) {
		t.Fatalf("Register duplicate: got %v, want ErrForwardExists", err)
	}

	// List ordered by port.
	list, err := r.Forwards(ctx, "ws-1")
	if err != nil {
		t.Fatalf("Forwards: %v", err)
	}
	if len(list) != 2 || list[0].Port != 3000 || list[1].Port != 3001 {
		t.Fatalf("Forwards: got %+v, want ports 3000,3001", list)
	}

	// Unknown workspace lists empty.
	list, err = r.Forwards(ctx, "nope")
	if err != nil || len(list) != 0 {
		t.Fatalf("Forwards unknown: got %+v, %v; want empty", list, err)
	}

	// Lookup.
	f, err := r.Lookup(ctx, "ws-1", 3000)
	if err != nil || f.Target != "127.0.0.1:4000" {
		t.Fatalf("Lookup: got %+v, %v", f, err)
	}
	if _, err := r.Lookup(ctx, "ws-1", 9999); !errors.Is(err, ErrForwardNotFound) {
		t.Fatalf("Lookup missing: got %v, want ErrForwardNotFound", err)
	}

	// Remove.
	if err := r.Remove(ctx, "ws-1", 3000); err != nil {
		t.Fatalf("Remove: %v", err)
	}
	if err := r.Remove(ctx, "ws-1", 3000); !errors.Is(err, ErrForwardNotFound) {
		t.Fatalf("Remove twice: got %v, want ErrForwardNotFound", err)
	}
	if err := r.Remove(ctx, "nope", 3000); !errors.Is(err, ErrForwardNotFound) {
		t.Fatalf("Remove unknown workspace: got %v, want ErrForwardNotFound", err)
	}
	if list, _ := r.Forwards(ctx, "ws-1"); len(list) != 1 {
		t.Fatalf("Forwards after remove: got %+v, want 1 left", list)
	}
}

func TestRegistryForwardValidation(t *testing.T) {
	ctx := context.Background()
	r := NewRegistry()
	bad := []Forward{
		{WorkspaceID: "", Port: 3000, Target: "127.0.0.1:4000"},
		{WorkspaceID: "ws-1", Port: 0, Target: "127.0.0.1:4000"},
		{WorkspaceID: "ws-1", Port: 70000, Target: "127.0.0.1:4000"},
		{WorkspaceID: "ws-1", Port: 3000, Target: ""},
		{WorkspaceID: "ws-1", Port: 3000, Target: "127.0.0.1"},
		{WorkspaceID: "ws-1", Port: 3000, Target: "127.0.0.1:4000", Protocol: "sctp"},
	}
	for i, f := range bad {
		if _, err := r.Register(ctx, f); err == nil {
			t.Fatalf("Register bad[%d] (%+v): nil error, want failure", i, f)
		}
	}
}

func TestRegistryConnections(t *testing.T) {
	ctx := context.Background()
	r := NewRegistry()

	a, err := r.Open(ctx, "ws-1")
	if err != nil || a == "" {
		t.Fatalf("Open: got %q, %v", a, err)
	}
	b, err := r.Open(ctx, "ws-1")
	if err != nil || b == a {
		t.Fatalf("Open twice: got %q and %q, want distinct ids", a, b)
	}
	c, err := r.Open(ctx, "ws-2")
	if err != nil || c == a || c == b {
		t.Fatalf("Open ws-2: got %q, want a third distinct id", c)
	}

	if n, _ := r.Count(ctx, "ws-1"); n != 2 {
		t.Fatalf("Count ws-1: got %d, want 2", n)
	}
	if n, _ := r.Count(ctx, "nope"); n != 0 {
		t.Fatalf("Count unknown: got %d, want 0", n)
	}
	if n, _ := r.Total(ctx); n != 3 {
		t.Fatalf("Total: got %d, want 3", n)
	}
	ids, err := r.Connections(ctx, "ws-1")
	if err != nil || len(ids) != 2 || ids[0] != a || ids[1] != b {
		t.Fatalf("Connections: got %v, %v; want [%s %s]", ids, err, a, b)
	}

	if err := r.Close(ctx, "ws-1", a); err != nil {
		t.Fatalf("Close: %v", err)
	}
	if err := r.Close(ctx, "ws-1", a); !errors.Is(err, ErrConnectionNotFound) {
		t.Fatalf("Close twice: got %v, want ErrConnectionNotFound", err)
	}
	if err := r.Close(ctx, "ws-1", "conn-999"); !errors.Is(err, ErrConnectionNotFound) {
		t.Fatalf("Close unknown: got %v, want ErrConnectionNotFound", err)
	}
	if n, _ := r.Count(ctx, "ws-1"); n != 1 {
		t.Fatalf("Count after close: got %d, want 1", n)
	}
	if n, _ := r.Total(ctx); n != 2 {
		t.Fatalf("Total after close: got %d, want 2", n)
	}

	if _, err := r.Open(ctx, "  "); err == nil {
		t.Fatal("Open blank workspace: nil error, want failure")
	}
}

func TestRegistryContextCanceled(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	r := NewRegistry()
	if err := r.SetTarget(ctx, "ws-1", "127.0.0.1:1"); !errors.Is(err, context.Canceled) {
		t.Fatalf("SetTarget canceled: got %v", err)
	}
	if _, err := r.Register(ctx, Forward{WorkspaceID: "ws-1", Port: 1, Target: "127.0.0.1:1"}); !errors.Is(err, context.Canceled) {
		t.Fatalf("Register canceled: got %v", err)
	}
	if _, err := r.Open(ctx, "ws-1"); !errors.Is(err, context.Canceled) {
		t.Fatalf("Open canceled: got %v", err)
	}
}
