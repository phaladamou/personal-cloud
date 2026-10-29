// Package node tracks the compute nodes able to run workloads.
//
// The Registry is an in-memory node inventory: Register brings a node
// ONLINE and emits node.connected, Unregister drops it and emits
// node.disconnected. Node events are cluster-scoped: they carry an
// empty workspace id and the node in the payload.
package node

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"sort"
	"sync"

	"github.com/phaladamou/personal-cloud/packages/protocol"
	"github.com/phaladamou/personal-cloud/packages/types"
)

// ErrNodeNotFound is returned when no node matches the given id.
var ErrNodeNotFound = errors.New("node: not found")

// Spec describes a node to register.
type Spec struct {
	// Name is the human label (required).
	Name string
	// CPU, MemoryMB and DiskGB must be positive; GPUs non-negative.
	CPU      int
	MemoryMB int64
	DiskGB   int64
	GPUs     int
	// Labels holds optional key=value metadata.
	Labels map[string]string
}

// Registry is an in-memory node inventory. A nil bus disables event
// publishing. Methods are safe for concurrent use.
type Registry struct {
	bus *protocol.EventBus

	mu    sync.Mutex
	nodes map[string]types.Node
}

// NewRegistry builds an empty registry publishing on bus.
func NewRegistry(bus *protocol.EventBus) *Registry {
	return &Registry{bus: bus, nodes: make(map[string]types.Node)}
}

// generateID returns a random node id.
func generateID() (string, error) {
	var b [8]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", err
	}
	return "node-" + hex.EncodeToString(b[:]), nil
}

// emit publishes a cluster-scoped node event carrying n. Publishing is
// best-effort: a nil bus drops.
func (r *Registry) emit(typ protocol.EventType, n types.Node) {
	ev, err := protocol.NewEvent(typ, "", n)
	if err != nil {
		return
	}
	r.bus.Publish(ev)
}

// Register adds a node in ONLINE status and emits node.connected.
func (r *Registry) Register(ctx context.Context, spec Spec) (types.Node, error) {
	if err := ctx.Err(); err != nil {
		return types.Node{}, err
	}
	id, err := generateID()
	if err != nil {
		return types.Node{}, fmt.Errorf("node: generate id: %w", err)
	}
	n, err := types.NewNode(id, spec.Name, spec.CPU, spec.MemoryMB, spec.DiskGB, spec.GPUs)
	if err != nil {
		return types.Node{}, err
	}
	n.Status = types.NodeOnline
	if spec.Labels != nil {
		n.Labels = make(map[string]string, len(spec.Labels))
		for k, v := range spec.Labels {
			n.Labels[k] = v
		}
	}
	r.mu.Lock()
	r.nodes[id] = n
	r.mu.Unlock()
	r.emit(protocol.EventNodeConnected, n)
	return n, nil
}

// Unregister drops a node and emits node.disconnected with the node as
// payload.
func (r *Registry) Unregister(ctx context.Context, id string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	r.mu.Lock()
	n, ok := r.nodes[id]
	if !ok {
		r.mu.Unlock()
		return fmt.Errorf("%w: %s", ErrNodeNotFound, id)
	}
	delete(r.nodes, id)
	r.mu.Unlock()
	r.emit(protocol.EventNodeDisconnected, n)
	return nil
}

// Get returns the node with the given id.
func (r *Registry) Get(ctx context.Context, id string) (types.Node, error) {
	if err := ctx.Err(); err != nil {
		return types.Node{}, err
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	n, ok := r.nodes[id]
	if !ok {
		return types.Node{}, fmt.Errorf("%w: %s", ErrNodeNotFound, id)
	}
	return n, nil
}

// List returns every node, ordered by id.
func (r *Registry) List(ctx context.Context) ([]types.Node, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	out := make([]types.Node, 0, len(r.nodes))
	for _, n := range r.nodes {
		out = append(out, n)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out, nil
}
