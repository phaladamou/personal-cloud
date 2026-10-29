package types

import (
	"fmt"
	"strings"
	"time"
)

// NodeStatus is the connectivity state of a compute node.
type NodeStatus string

// Valid node states.
const (
	NodeOnline  NodeStatus = "ONLINE"
	NodeOffline NodeStatus = "OFFLINE"
	NodeDrained NodeStatus = "DRAINED"
)

// Valid reports whether s is a known node status.
func (s NodeStatus) Valid() bool {
	switch s {
	case NodeOnline, NodeOffline, NodeDrained:
		return true
	default:
		return false
	}
}

// String returns the status as a plain string.
func (s NodeStatus) String() string {
	return string(s)
}

// Node is a machine able to run workloads: a cloud instance, a server,
// a personal machine or a GPU box.
type Node struct {
	ID        string            `json:"id"`
	Name      string            `json:"name"`
	Status    NodeStatus        `json:"status"`
	CPU       int               `json:"cpu"`
	MemoryMB  int64             `json:"memory_mb"`
	DiskGB    int64             `json:"disk_gb"`
	GPUs      int               `json:"gpus"`
	Labels    map[string]string `json:"labels,omitempty"`
	Allocated Resource          `json:"allocated"`
	// LastHeartbeat is the last liveness signal received from the node.
	LastHeartbeat time.Time `json:"last_heartbeat"`
	CreatedAt     time.Time `json:"created_at"`
}

// Capacity returns the total resources of the node.
func (n Node) Capacity() Resource {
	return Resource{CPU: n.CPU, MemoryMB: n.MemoryMB, DiskGB: n.DiskGB, GPUs: n.GPUs}
}

// Allocatable returns the resources still free for placement: capacity
// minus the workloads already placed on the node.
func (n Node) Allocatable() Resource {
	return n.Capacity().Sub(n.Allocated)
}

// Matches reports whether the node labels satisfy every entry of the
// selector. A nil or empty selector matches every node.
func (n Node) Matches(selector map[string]string) bool {
	for k, v := range selector {
		if n.Labels[k] != v {
			return false
		}
	}
	return true
}

// NewNode builds a validated node in OFFLINE status. cpu, memoryMB and
// diskGB must be positive; gpus must be non-negative.
func NewNode(id, name string, cpu int, memoryMB, diskGB int64, gpus int) (Node, error) {
	id, err := trimAndRequire(id, ErrEmptyID)
	if err != nil {
		return Node{}, err
	}
	name = strings.TrimSpace(name)
	if name == "" {
		return Node{}, ErrEmptyName
	}
	if cpu <= 0 {
		return Node{}, fmt.Errorf("types: cpu must be positive, got %d", cpu)
	}
	if memoryMB <= 0 {
		return Node{}, fmt.Errorf("types: memory_mb must be positive, got %d", memoryMB)
	}
	if diskGB <= 0 {
		return Node{}, fmt.Errorf("types: disk_gb must be positive, got %d", diskGB)
	}
	if gpus < 0 {
		return Node{}, fmt.Errorf("types: gpus must be non-negative, got %d", gpus)
	}
	return Node{
		ID:        id,
		Name:      name,
		Status:    NodeOffline,
		CPU:       cpu,
		MemoryMB:  memoryMB,
		DiskGB:    diskGB,
		GPUs:      gpus,
		CreatedAt: nowUTC(),
	}, nil
}

// String returns a short human-readable representation of the node.
func (n Node) String() string {
	return fmt.Sprintf("%s (%s)", n.Name, n.Status)
}
