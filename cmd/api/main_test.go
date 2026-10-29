package main

import (
	"context"
	"testing"
	"time"

	"github.com/phaladamou/personal-cloud/compute/node"
	"github.com/phaladamou/personal-cloud/packages/types"
)

// TestRunSweeperExpiresSilentNode checks the production liveness loop: a
// node that stops heartbeating is marked OFFLINE and dropped from
// placement candidates, while a heartbeating node stays ONLINE.
func TestRunSweeperExpiresSilentNode(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	nodes := node.NewRegistry(nil, node.WithHeartbeatTimeout(100*time.Millisecond))
	spec := func(id, name string) node.Spec {
		return node.Spec{ID: id, Name: name, CPU: 4, MemoryMB: 4096, DiskGB: 50}
	}
	if _, err := nodes.Register(ctx, spec("node-loud", "loud")); err != nil {
		t.Fatalf("register loud: %v", err)
	}
	if _, err := nodes.Register(ctx, spec("node-silent", "silent")); err != nil {
		t.Fatalf("register silent: %v", err)
	}
	go runSweeper(ctx, nodes, 10*time.Millisecond)

	deadline := time.Now().Add(5 * time.Second)
	for {
		// Heartbeat every ~10ms against a 100ms window: generous
		// margin, so only the silent node may expire.
		if _, err := nodes.Heartbeat(ctx, "node-loud"); err != nil {
			t.Fatalf("heartbeat loud: %v", err)
		}
		got, err := nodes.Get(ctx, "node-silent")
		if err != nil {
			t.Fatalf("get silent: %v", err)
		}
		if got.Status == types.NodeOffline {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("silent node still ONLINE after 5s, want OFFLINE")
		}
		time.Sleep(10 * time.Millisecond)
	}
	if got, err := nodes.Get(ctx, "node-loud"); err != nil {
		t.Fatalf("get loud: %v", err)
	} else if got.Status != types.NodeOnline {
		t.Fatalf("loud node status = %s, want ONLINE", got.Status)
	}
	found, err := nodes.Find(ctx, types.Resource{}, nil)
	if err != nil {
		t.Fatalf("Find: %v", err)
	}
	if len(found) != 1 || found[0].ID != "node-loud" {
		t.Fatalf("Find = %v, want only node-loud", found)
	}
}
