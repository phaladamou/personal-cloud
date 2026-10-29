package gateway

import (
	"context"
	"errors"
	"fmt"
	"net"
	"sort"
	"strconv"
	"strings"
	"sync"
)

// Registry errors. Unknown workspaces are never an error: listing the
// forwards or connections of a workspace the registry never saw yields
// an empty result, mirroring the storage managers.
var (
	// ErrForwardExists marks a port already forwarded for a workspace.
	ErrForwardExists = errors.New("gateway: port already forwarded")
	// ErrForwardNotFound marks a port with no forward for a workspace.
	ErrForwardNotFound = errors.New("gateway: port not forwarded")
	// ErrConnectionNotFound marks an unknown connection id.
	ErrConnectionNotFound = errors.New("gateway: connection not found")
)

// Protocols accepted on a forward. The HTTP proxy serves every
// registered forward over TCP; the protocol stays informational so a
// later UDP forwarder can reuse the registry.
const (
	ProtocolTCP = "tcp"
	ProtocolUDP = "udp"
)

// Forward exposes one workspace HTTP service: clients reach it at
// /v1/workspaces/{id}/ports/{port}/... and the gateway proxies to
// Target (host:port).
type Forward struct {
	WorkspaceID string `json:"workspace_id"`
	Port        int    `json:"port"`
	Target      string `json:"target"`
	Protocol    string `json:"protocol"`
}

// validTarget checks a host:port address: both parts required, the port
// numeric and in range.
func validTarget(addr string) error {
	host, port, err := net.SplitHostPort(addr)
	if err != nil {
		return fmt.Errorf("invalid target %q: %w", addr, err)
	}
	if strings.TrimSpace(host) == "" {
		return fmt.Errorf("invalid target %q: empty host", addr)
	}
	n, err := strconv.Atoi(port)
	if err != nil || n < 1 || n > 65535 {
		return fmt.Errorf("invalid target %q: bad port", addr)
	}
	return nil
}

// normalize validates f and fills its defaults (tcp protocol).
func (f Forward) normalize() (Forward, error) {
	if strings.TrimSpace(f.WorkspaceID) == "" {
		return Forward{}, fmt.Errorf("gateway: empty workspace id")
	}
	if f.Port < 1 || f.Port > 65535 {
		return Forward{}, fmt.Errorf("gateway: invalid port %d", f.Port)
	}
	if err := validTarget(f.Target); err != nil {
		return Forward{}, err
	}
	if f.Protocol == "" {
		f.Protocol = ProtocolTCP
	}
	if f.Protocol != ProtocolTCP && f.Protocol != ProtocolUDP {
		return Forward{}, fmt.Errorf("gateway: invalid protocol %q", f.Protocol)
	}
	return f, nil
}

// Registry tracks workspace network backends: a default target address
// per workspace (workspaceID -> host:port), per-port forwards, and open
// client connections. It is in-memory only and safe for concurrent use.
type Registry struct {
	mu       sync.RWMutex
	targets  map[string]string
	forwards map[string]map[int]Forward
	conns    map[string]map[string]struct{}
	nextConn uint64
}

// NewRegistry builds an empty Registry.
func NewRegistry() *Registry {
	return &Registry{
		targets:  make(map[string]string),
		forwards: make(map[string]map[int]Forward),
		conns:    make(map[string]map[string]struct{}),
	}
}

// SetTarget records the default backend address (host:port) of a
// workspace. The proxy falls back to it when no per-port forward
// matches. It replaces any previous target.
func (r *Registry) SetTarget(ctx context.Context, workspaceID, addr string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if strings.TrimSpace(workspaceID) == "" {
		return fmt.Errorf("gateway: empty workspace id")
	}
	if err := validTarget(addr); err != nil {
		return err
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	r.targets[workspaceID] = addr
	return nil
}

// Target returns the default backend address of a workspace.
func (r *Registry) Target(ctx context.Context, workspaceID string) (string, error) {
	if err := ctx.Err(); err != nil {
		return "", err
	}
	r.mu.RLock()
	defer r.mu.RUnlock()
	addr, ok := r.targets[workspaceID]
	if !ok {
		return "", fmt.Errorf("%w: no target for workspace %s", ErrForwardNotFound, workspaceID)
	}
	return addr, nil
}

// RemoveTarget drops the default backend address of a workspace. A
// workspace without target simply has no proxy fallback.
func (r *Registry) RemoveTarget(ctx context.Context, workspaceID string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	delete(r.targets, workspaceID)
	return nil
}

// Register records a port forward. Registering an already forwarded
// port fails with ErrForwardExists.
func (r *Registry) Register(ctx context.Context, f Forward) (Forward, error) {
	if err := ctx.Err(); err != nil {
		return Forward{}, err
	}
	f, err := f.normalize()
	if err != nil {
		return Forward{}, err
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	ports, ok := r.forwards[f.WorkspaceID]
	if !ok {
		ports = make(map[int]Forward)
		r.forwards[f.WorkspaceID] = ports
	}
	if _, dup := ports[f.Port]; dup {
		return Forward{}, fmt.Errorf("%w: %d for workspace %s", ErrForwardExists, f.Port, f.WorkspaceID)
	}
	ports[f.Port] = f
	return f, nil
}

// Forwards returns every forward of a workspace, ordered by port. An
// unknown workspace yields an empty list.
func (r *Registry) Forwards(ctx context.Context, workspaceID string) ([]Forward, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	r.mu.RLock()
	defer r.mu.RUnlock()
	ports := r.forwards[workspaceID]
	out := make([]Forward, 0, len(ports))
	for _, f := range ports {
		out = append(out, f)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Port < out[j].Port })
	return out, nil
}

// Lookup returns the forward serving port on a workspace.
func (r *Registry) Lookup(ctx context.Context, workspaceID string, port int) (Forward, error) {
	if err := ctx.Err(); err != nil {
		return Forward{}, err
	}
	r.mu.RLock()
	defer r.mu.RUnlock()
	f, ok := r.forwards[workspaceID][port]
	if !ok {
		return Forward{}, fmt.Errorf("%w: %d for workspace %s", ErrForwardNotFound, port, workspaceID)
	}
	return f, nil
}

// Remove drops the forward serving port on a workspace.
func (r *Registry) Remove(ctx context.Context, workspaceID string, port int) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	ports, ok := r.forwards[workspaceID]
	if !ok {
		return fmt.Errorf("%w: %d for workspace %s", ErrForwardNotFound, port, workspaceID)
	}
	if _, ok := ports[port]; !ok {
		return fmt.Errorf("%w: %d for workspace %s", ErrForwardNotFound, port, workspaceID)
	}
	delete(ports, port)
	if len(ports) == 0 {
		delete(r.forwards, workspaceID)
	}
	return nil
}

// Open tracks a new client connection on a workspace and returns its
// id ("conn-N", unique per registry).
func (r *Registry) Open(ctx context.Context, workspaceID string) (string, error) {
	if err := ctx.Err(); err != nil {
		return "", err
	}
	if strings.TrimSpace(workspaceID) == "" {
		return "", fmt.Errorf("gateway: empty workspace id")
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	r.nextConn++
	id := "conn-" + strconv.FormatUint(r.nextConn, 10)
	set, ok := r.conns[workspaceID]
	if !ok {
		set = make(map[string]struct{})
		r.conns[workspaceID] = set
	}
	set[id] = struct{}{}
	return id, nil
}

// Close drops a tracked connection.
func (r *Registry) Close(ctx context.Context, workspaceID, connID string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	set, ok := r.conns[workspaceID]
	if !ok {
		return fmt.Errorf("%w: %s", ErrConnectionNotFound, connID)
	}
	if _, ok := set[connID]; !ok {
		return fmt.Errorf("%w: %s", ErrConnectionNotFound, connID)
	}
	delete(set, connID)
	if len(set) == 0 {
		delete(r.conns, workspaceID)
	}
	return nil
}

// Connections returns the tracked connection ids of a workspace,
// ordered by arrival. An unknown workspace yields an empty list.
func (r *Registry) Connections(ctx context.Context, workspaceID string) ([]string, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	r.mu.RLock()
	defer r.mu.RUnlock()
	set := r.conns[workspaceID]
	out := make([]string, 0, len(set))
	for id := range set {
		out = append(out, id)
	}
	sort.Strings(out)
	return out, nil
}

// Count returns the number of tracked connections on a workspace.
func (r *Registry) Count(ctx context.Context, workspaceID string) (int, error) {
	if err := ctx.Err(); err != nil {
		return 0, err
	}
	r.mu.RLock()
	defer r.mu.RUnlock()
	return len(r.conns[workspaceID]), nil
}

// Total returns the number of tracked connections across workspaces.
func (r *Registry) Total(ctx context.Context) (int, error) {
	if err := ctx.Err(); err != nil {
		return 0, err
	}
	r.mu.RLock()
	defer r.mu.RUnlock()
	total := 0
	for _, set := range r.conns {
		total += len(set)
	}
	return total, nil
}
