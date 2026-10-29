// Package scheduler places workloads on compute nodes.
//
// Schedule applies hard filters over the node registry (ONLINE state,
// Resource.Fits on allocatable capacity including GPU count, label
// selector) then scores the survivors least-loaded-wins with a node
// id tie-break, so placement is deterministic for a given registry
// state. The winner reservation goes through Registry.Allocate, and
// Release frees it through Registry.Release.
//
// GPU requirements flow through Resource.GPUs; a GPU model pins
// placement through a label selector over the labels exposed by
// compute/gpu (gpu.model). Methods are safe for concurrent use:
// scheduling serializes on the Scheduler so two concurrent placements
// observe each other reservations.
package scheduler

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"sync"

	"github.com/phaladamou/personal-cloud/compute/node"
	"github.com/phaladamou/personal-cloud/packages/types"
)

// ErrNoFit is returned when no registered node can take the workload:
// every node is filtered out (offline, drained, short of allocatable
// capacity, label mismatch) or a concurrent placement stole the last
// room.
var ErrNoFit = errors.New("scheduler: no suitable node")

// Scheduler places workloads over a node registry.
type Scheduler struct {
	nodes *node.Registry

	mu sync.Mutex
}

// New builds a Scheduler over the node registry.
func New(nodes *node.Registry) *Scheduler {
	return &Scheduler{nodes: nodes}
}

// Schedule reserves req on the least-loaded ONLINE node whose labels
// satisfy the selector, and returns the node after reservation. A nil
// or empty selector matches every node. It returns ErrNoFit (wrapped,
// with the request) when nothing fits.
func (s *Scheduler) Schedule(ctx context.Context, req types.Resource, selector map[string]string) (types.Node, error) {
	if err := ctx.Err(); err != nil {
		return types.Node{}, err
	}
	if s.nodes == nil {
		return types.Node{}, errors.New("scheduler: node registry not configured")
	}
	if req.Negative() {
		return types.Node{}, fmt.Errorf("scheduler: negative resource request %v", req)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	candidates, err := s.nodes.Find(ctx, req, selector)
	if err != nil {
		return types.Node{}, err
	}
	sortByLoad(candidates)
	for _, c := range candidates {
		if n, err := s.nodes.Allocate(ctx, c.ID, req); err == nil {
			return n, nil
		} else if !errors.Is(err, node.ErrInsufficientCapacity) && !errors.Is(err, node.ErrNodeNotReady) {
			return types.Node{}, err
		}
		// Lost a race against a direct registry user: try the next
		// candidate with its fresh state.
	}
	return types.Node{}, fmt.Errorf("%w: %v (selector %v)", ErrNoFit, req, selector)
}

// Release frees a reservation previously placed by Schedule.
func (s *Scheduler) Release(ctx context.Context, nodeID string, req types.Resource) (types.Node, error) {
	if err := ctx.Err(); err != nil {
		return types.Node{}, err
	}
	if s.nodes == nil {
		return types.Node{}, errors.New("scheduler: node registry not configured")
	}
	return s.nodes.Release(ctx, nodeID, req)
}

// sortByLoad orders nodes least-loaded first, breaking ties by id so
// the winner is deterministic.
func sortByLoad(nodes []types.Node) {
	sort.Slice(nodes, func(i, j int) bool {
		li, lj := load(nodes[i]), load(nodes[j])
		if li != lj {
			return li < lj
		}
		return nodes[i].ID < nodes[j].ID
	})
}

// load is the summed utilization ratio of the node over cpu, memory,
// disk and GPUs. Zero capacity counts as idle on that dimension (it
// can never be allocated from, and its ratio would divide by zero).
func load(n types.Node) float64 {
	cap := n.Capacity()
	used := n.Allocated
	return ratio(int64(used.CPU), int64(cap.CPU)) +
		ratio(used.MemoryMB, cap.MemoryMB) +
		ratio(used.DiskGB, cap.DiskGB) +
		ratio(int64(used.GPUs), int64(cap.GPUs))
}

// ratio returns used/total, or 0 when total is not positive.
func ratio(used, total int64) float64 {
	if total <= 0 {
		return 0
	}
	return float64(used) / float64(total)
}
