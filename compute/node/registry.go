// Package node tracks the compute nodes able to run workloads.
//
// The Registry is an in-memory node inventory: Register brings a node
// ONLINE and emits node.connected, Unregister drops it and emits
// node.disconnected. Node events are cluster-scoped: they carry an
// empty workspace id and the node in the payload.
//
// Liveness comes from heartbeats: Heartbeat refreshes a node and brings
// an OFFLINE node back ONLINE (emitting node.connected), while Sweep
// marks ONLINE nodes whose heartbeat is older than the configured
// timeout as OFFLINE (emitting node.disconnected). Allocate and Release
// track the resources placed on a node; Find selects ONLINE nodes with
// room for a request and matching labels.
package node

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"sort"
	"sync"
	"time"

	"github.com/phaladamou/personal-cloud/packages/protocol"
	"github.com/phaladamou/personal-cloud/packages/types"
)

// ErrNodeNotFound is returned when no node matches the given id.
var ErrNodeNotFound = errors.New("node: not found")

// ErrInsufficientCapacity is returned when a placement request exceeds
// the allocatable resources of a node.
var ErrInsufficientCapacity = errors.New("node: insufficient capacity")

// ErrNodeNotReady is returned when resources are placed on a node that
// is not ONLINE.
var ErrNodeNotReady = errors.New("node: not ready")

// DefaultHeartbeatTimeout is the liveness window applied when no
// explicit timeout is configured.
const DefaultHeartbeatTimeout = 30 * time.Second

// Spec describes a node to register.
type Spec struct {
	// ID pins the registration to a stable identity. Empty means a
	// fresh random id; a set ID re-registers the node: capacity and
	// labels are refreshed, Allocated is preserved, and the node is
	// back ONLINE.
	ID string
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

// Option configures a Registry.
type Option func(*Registry)

// WithHeartbeatTimeout sets the liveness window: an ONLINE node whose
// heartbeat is older than d is marked OFFLINE by Sweep. A
// non-positive d keeps the default.
func WithHeartbeatTimeout(d time.Duration) Option {
	return func(r *Registry) {
		if d > 0 {
			r.timeout = d
		}
	}
}

// WithClock injects the time source used for heartbeats and expiry.
// It defaults to time.Now and exists for deterministic tests.
func WithClock(now func() time.Time) Option {
	return func(r *Registry) {
		if now != nil {
			r.now = now
		}
	}
}

// Registry is an in-memory node inventory. A nil bus disables event
// publishing. Methods are safe for concurrent use.
type Registry struct {
	bus *protocol.EventBus

	timeout time.Duration
	now     func() time.Time

	mu    sync.Mutex
	nodes map[string]types.Node
}

// NewRegistry builds an empty registry publishing on bus.
func NewRegistry(bus *protocol.EventBus, opts ...Option) *Registry {
	r := &Registry{
		bus:     bus,
		timeout: DefaultHeartbeatTimeout,
		now:     time.Now,
		nodes:   make(map[string]types.Node),
	}
	for _, opt := range opts {
		opt(r)
	}
	return r
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

// copyLabels duplicates a label map, preserving nil.
func copyLabels(in map[string]string) map[string]string {
	if in == nil {
		return nil
	}
	out := make(map[string]string, len(in))
	for k, v := range in {
		out[k] = v
	}
	return out
}

// Register adds a node in ONLINE status and emits node.connected. With
// Spec.ID set, it re-registers that identity instead: capacity and
// labels are refreshed, Allocated is preserved, the node is back
// ONLINE and node.connected is emitted.
func (r *Registry) Register(ctx context.Context, spec Spec) (types.Node, error) {
	if err := ctx.Err(); err != nil {
		return types.Node{}, err
	}
	id := spec.ID
	if id == "" {
		var err error
		if id, err = generateID(); err != nil {
			return types.Node{}, fmt.Errorf("node: generate id: %w", err)
		}
	}
	n, err := types.NewNode(id, spec.Name, spec.CPU, spec.MemoryMB, spec.DiskGB, spec.GPUs)
	if err != nil {
		return types.Node{}, err
	}
	n.Status = types.NodeOnline
	n.Labels = copyLabels(spec.Labels)
	n.LastHeartbeat = r.now().UTC()

	r.mu.Lock()
	if prev, ok := r.nodes[id]; ok {
		// Re-registration keeps the placement state and identity
		// timestamps of the previous incarnation.
		n.Allocated = prev.Allocated
		n.CreatedAt = prev.CreatedAt
	}
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

// Heartbeat records a liveness signal from the node. An OFFLINE node
// comes back ONLINE and node.connected is emitted; a DRAINED node
// keeps its drained state.
func (r *Registry) Heartbeat(ctx context.Context, id string) (types.Node, error) {
	if err := ctx.Err(); err != nil {
		return types.Node{}, err
	}
	r.mu.Lock()
	n, ok := r.nodes[id]
	if !ok {
		r.mu.Unlock()
		return types.Node{}, fmt.Errorf("%w: %s", ErrNodeNotFound, id)
	}
	n.LastHeartbeat = r.now().UTC()
	reconnected := n.Status == types.NodeOffline
	if reconnected {
		n.Status = types.NodeOnline
	}
	r.nodes[id] = n
	r.mu.Unlock()
	if reconnected {
		r.emit(protocol.EventNodeConnected, n)
	}
	return n, nil
}

// Sweep marks ONLINE nodes whose heartbeat is older than the
// configured timeout as OFFLINE and emits node.disconnected for each.
// It returns the newly offline nodes, ordered by id.
func (r *Registry) Sweep(ctx context.Context) ([]types.Node, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	cutoff := r.now().UTC().Add(-r.timeout)
	r.mu.Lock()
	var stale []types.Node
	for id, n := range r.nodes {
		if n.Status != types.NodeOnline || !n.LastHeartbeat.Before(cutoff) {
			continue
		}
		n.Status = types.NodeOffline
		r.nodes[id] = n
		stale = append(stale, n)
	}
	r.mu.Unlock()
	sort.Slice(stale, func(i, j int) bool { return stale[i].ID < stale[j].ID })
	for _, n := range stale {
		r.emit(protocol.EventNodeDisconnected, n)
	}
	return stale, nil
}

// Allocate places req resources on an ONLINE node with room for them,
// shrinking its allocatable share. Overflowing requests fail with
// ErrInsufficientCapacity and leave the node untouched.
func (r *Registry) Allocate(ctx context.Context, id string, req types.Resource) (types.Node, error) {
	if err := ctx.Err(); err != nil {
		return types.Node{}, err
	}
	if req.Negative() {
		return types.Node{}, fmt.Errorf("node: negative resource request %v", req)
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	n, ok := r.nodes[id]
	if !ok {
		return types.Node{}, fmt.Errorf("%w: %s", ErrNodeNotFound, id)
	}
	if n.Status != types.NodeOnline {
		return types.Node{}, fmt.Errorf("%w: %s is %s", ErrNodeNotReady, id, n.Status)
	}
	if !req.Fits(n.Allocatable()) {
		return types.Node{}, fmt.Errorf("%w: %v exceeds allocatable %v on %s",
			ErrInsufficientCapacity, req, n.Allocatable(), id)
	}
	n.Allocated = n.Allocated.Add(req)
	r.nodes[id] = n
	return n, nil
}

// Release frees req resources previously placed on the node. Releasing
// more than allocated clamps at zero; the node state is untouched.
func (r *Registry) Release(ctx context.Context, id string, req types.Resource) (types.Node, error) {
	if err := ctx.Err(); err != nil {
		return types.Node{}, err
	}
	if req.Negative() {
		return types.Node{}, fmt.Errorf("node: negative resource release %v", req)
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	n, ok := r.nodes[id]
	if !ok {
		return types.Node{}, fmt.Errorf("%w: %s", ErrNodeNotFound, id)
	}
	n.Allocated = n.Allocated.Sub(req)
	r.nodes[id] = n
	return n, nil
}

// Find returns the ONLINE nodes with room for req whose labels satisfy
// the selector, ordered by id. A nil selector matches every node.
func (r *Registry) Find(ctx context.Context, req types.Resource, selector map[string]string) ([]types.Node, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	var out []types.Node
	for _, n := range r.nodes {
		if n.Status != types.NodeOnline || !req.Fits(n.Allocatable()) || !n.Matches(selector) {
			continue
		}
		out = append(out, n)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out, nil
}
