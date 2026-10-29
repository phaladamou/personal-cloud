package container

import (
	"context"
	"fmt"
	"os/exec"
	"strings"
	"sync"

	"github.com/phaladamou/personal-cloud/runtime/process"
)

// LocalBackend is a ContainerBackend where each container is a group of
// local OS processes rooted at its main process. There is no filesystem
// or network isolation: it is a portable stand-in for real containers.
type LocalBackend struct {
	pm         process.ProcessManager
	mu         sync.Mutex
	seq        int
	containers map[string]*localContainer
}

type localContainer struct {
	cfg    Config
	name   string
	state  State
	procID string
}

var _ ContainerBackend = (*LocalBackend)(nil)

// NewLocalBackend builds a backend supervised by a new local process manager.
func NewLocalBackend() *LocalBackend {
	return NewLocalBackendWithManager(process.NewLocalManager())
}

// NewLocalBackendWithManager builds a backend supervised by pm. It is
// used to inject fakes in tests.
func NewLocalBackendWithManager(pm process.ProcessManager) *LocalBackend {
	return &LocalBackend{pm: pm, containers: make(map[string]*localContainer)}
}

func (b *LocalBackend) lookup(id string) (*localContainer, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	c, ok := b.containers[id]
	if !ok {
		return nil, fmt.Errorf("%w: %s", ErrNotFound, id)
	}
	return c, nil
}

// Create registers a container and returns its id.
func (b *LocalBackend) Create(ctx context.Context, cfg Config) (string, error) {
	if err := ctx.Err(); err != nil {
		return "", err
	}
	if strings.TrimSpace(cfg.Command) == "" {
		return "", fmt.Errorf("container: empty command")
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	b.seq++
	id := fmt.Sprintf("ctr-%d", b.seq)
	name := strings.TrimSpace(cfg.Name)
	if name == "" {
		name = id
	}
	b.containers[id] = &localContainer{cfg: cfg, name: name, state: StateCreated}
	return id, nil
}

// Start boots a created or stopped container.
func (b *LocalBackend) Start(ctx context.Context, id string) error {
	c, err := b.lookup(id)
	if err != nil {
		return err
	}
	b.mu.Lock()
	if c.state == StateRunning {
		b.mu.Unlock()
		return fmt.Errorf("container: %s already running", id)
	}
	b.mu.Unlock()
	procID, err := b.pm.Start(ctx, process.StartSpec{
		Command: c.cfg.Command,
		Args:    c.cfg.Args,
		Dir:     c.cfg.Dir,
		Env:     c.cfg.Env,
	})
	if err != nil {
		return fmt.Errorf("container: start %s: %w", id, err)
	}
	b.mu.Lock()
	c.procID = procID
	c.state = StateRunning
	b.mu.Unlock()
	go b.reap(id, procID)
	return nil
}

// reap marks the container stopped once its main process exits.
func (b *LocalBackend) reap(id, procID string) {
	_, _ = b.pm.Wait(context.Background(), procID)
	b.mu.Lock()
	defer b.mu.Unlock()
	if c, ok := b.containers[id]; ok && c.procID == procID {
		c.state = StateStopped
	}
}

// Stop halts a running container. It is idempotent.
func (b *LocalBackend) Stop(ctx context.Context, id string) error {
	c, err := b.lookup(id)
	if err != nil {
		return err
	}
	b.mu.Lock()
	if c.state != StateRunning {
		b.mu.Unlock()
		return nil
	}
	procID := c.procID
	b.mu.Unlock()
	if err := b.pm.Stop(ctx, procID); err != nil {
		return fmt.Errorf("container: stop %s: %w", id, err)
	}
	_, _ = b.pm.Wait(context.Background(), procID)
	b.mu.Lock()
	c.state = StateStopped
	b.mu.Unlock()
	return nil
}

// Remove deletes a container, stopping it first when running.
func (b *LocalBackend) Remove(ctx context.Context, id string) error {
	if err := b.Stop(ctx, id); err != nil {
		return err
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	if _, ok := b.containers[id]; !ok {
		return fmt.Errorf("%w: %s", ErrNotFound, id)
	}
	delete(b.containers, id)
	return nil
}

// Exec runs a command in a running container, streaming stdio.
func (b *LocalBackend) Exec(ctx context.Context, id string, opts ExecOptions) error {
	c, err := b.lookup(id)
	if err != nil {
		return err
	}
	b.mu.Lock()
	running := c.state == StateRunning
	cfg := c.cfg
	b.mu.Unlock()
	if !running {
		return fmt.Errorf("container: exec %s: not running", id)
	}
	if len(opts.Cmd) == 0 {
		return fmt.Errorf("container: exec %s: empty command", id)
	}
	cmd := exec.CommandContext(ctx, opts.Cmd[0], opts.Cmd[1:]...)
	if cfg.Dir != "" {
		cmd.Dir = cfg.Dir
	}
	if cfg.Env != nil {
		cmd.Env = append(cmd.Environ(), cfg.Env...)
	}
	cmd.Stdin = opts.Stdin
	cmd.Stdout = opts.Stdout
	cmd.Stderr = opts.Stderr
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("container: exec %s: %w", id, err)
	}
	return nil
}

// Inspect returns a snapshot of the container metadata.
func (b *LocalBackend) Inspect(ctx context.Context, id string) (Info, error) {
	c, err := b.lookup(id)
	if err != nil {
		return Info{}, err
	}
	if err := ctx.Err(); err != nil {
		return Info{}, err
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	return Info{ID: id, Name: c.name, State: c.state}, nil
}
