// Package network holds workspace networking primitives.
//
// Phase 1 keeps this package tiny: a free-port allocator plus the
// interface definitions the future gateway work will implement.
package network

import (
	"context"
	"fmt"
	"net"
)

// FreePort returns an available TCP port on 127.0.0.1.
//
// The port is free when returned but nothing reserves it: callers must
// bind it immediately and tolerate conflicts.
func FreePort() (int, error) {
	return FreePortOn("127.0.0.1")
}

// FreePortOn returns an available TCP port on the given host.
func FreePortOn(host string) (int, error) {
	l, err := net.Listen("tcp", net.JoinHostPort(host, "0"))
	if err != nil {
		return 0, fmt.Errorf("network: listen: %w", err)
	}
	defer l.Close()
	addr, ok := l.Addr().(*net.TCPAddr)
	if !ok {
		return 0, fmt.Errorf("network: unexpected address type %T", l.Addr())
	}
	return addr.Port, nil
}

// FreePorts returns n distinct free TCP ports on 127.0.0.1.
func FreePorts(n int) ([]int, error) {
	if n <= 0 {
		return nil, fmt.Errorf("network: need a positive count, got %d", n)
	}
	ports := make([]int, 0, n)
	seen := make(map[int]bool, n)
	for len(ports) < n {
		p, err := FreePort()
		if err != nil {
			return nil, err
		}
		if !seen[p] {
			seen[p] = true
			ports = append(ports, p)
		}
	}
	return ports, nil
}

// PortMapping exposes a container port on a host port.
type PortMapping struct {
	HostPort      int
	ContainerPort int
	// Protocol is "tcp" or "udp". Empty means "tcp".
	Protocol string
}

// Gateway forwards container ports to the host. It is a stub interface
// for the later gateway work: routing, reverse proxy and port
// forwarding build on it.
type Gateway interface {
	// Forward exposes a container port and returns the host address.
	Forward(ctx context.Context, containerID string, mapping PortMapping) (string, error)
	// Unforward drops a host port forwarding for a container.
	Unforward(ctx context.Context, containerID string, hostPort int) error
	// Close releases every forwarding held by the gateway.
	Close() error
}
