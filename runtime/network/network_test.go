package network

import (
	"fmt"
	"net"
	"testing"
)

func TestFreePortUsable(t *testing.T) {
	port, err := FreePort()
	if err != nil {
		t.Fatalf("FreePort: %v", err)
	}
	if port <= 0 || port > 65535 {
		t.Fatalf("port = %d, want a valid TCP port", port)
	}
	l, err := net.Listen("tcp", fmt.Sprintf("127.0.0.1:%d", port))
	if err != nil {
		t.Fatalf("listen on free port %d: %v", port, err)
	}
	l.Close()
}

func TestFreePortsDistinct(t *testing.T) {
	ports, err := FreePorts(5)
	if err != nil {
		t.Fatalf("FreePorts: %v", err)
	}
	if len(ports) != 5 {
		t.Fatalf("len(ports) = %d, want 5", len(ports))
	}
	seen := make(map[int]bool)
	for _, p := range ports {
		if seen[p] {
			t.Fatalf("duplicate port %d in %v", p, ports)
		}
		seen[p] = true
	}
}

func TestFreePortsInvalidCount(t *testing.T) {
	if _, err := FreePorts(0); err == nil {
		t.Fatal("FreePorts(0): want error, got nil")
	}
	if _, err := FreePorts(-1); err == nil {
		t.Fatal("FreePorts(-1): want error, got nil")
	}
}
