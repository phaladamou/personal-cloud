package container

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os/exec"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/phaladamou/personal-cloud/packages/types"
	"github.com/phaladamou/personal-cloud/runtime/process"
)

// sortInfos orders process infos by start time, then id.
func sortInfos(infos []process.Info) {
	sort.Slice(infos, func(i, j int) bool {
		if infos[i].StartedAt.Equal(infos[j].StartedAt) {
			return infos[i].ID < infos[j].ID
		}
		return infos[i].StartedAt.Before(infos[j].StartedAt)
	})
}

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
	cfg     Config
	name    string
	state   State
	procID  string
	execSeq int
	execs   map[string]*execRecord
}

// execRecord is a tracked Exec command with its combined output. It
// doubles as the io.Writer receiving the command output.
type execRecord struct {
	rec process.Info

	mu     sync.Mutex
	logs   []byte
	notify chan struct{} // capacity 1, signalled on new output or exit
	done   chan struct{} // closed when the command exits
}

// Write appends command output to the record buffer.
func (r *execRecord) Write(b []byte) (int, error) {
	r.mu.Lock()
	r.logs = append(r.logs, b...)
	r.mu.Unlock()
	select {
	case r.notify <- struct{}{}:
	default:
	}
	return len(b), nil
}

// finish records the exit code and wakes log waiters.
func (r *execRecord) finish(code int) {
	r.mu.Lock()
	r.rec.ExitCode = &code
	if code == -1 {
		r.rec.State = types.ProcessKilled
	} else {
		r.rec.State = types.ProcessExited
	}
	r.mu.Unlock()
	close(r.done)
	select {
	case r.notify <- struct{}{}:
	default:
	}
}

// snapshot returns a copy of the record metadata.
func (r *execRecord) snapshot() process.Info {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.rec
}

// snapshotLogs returns a copy of the buffered output.
func (r *execRecord) snapshotLogs() []byte {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]byte(nil), r.logs...)
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
	b.containers[id] = &localContainer{cfg: cfg, name: name, state: StateCreated, execs: make(map[string]*execRecord)}
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

// Exec runs a command in a running container, streaming stdio. The
// command is tracked as a process: its combined output is recorded for
// Processes/Logs under opts.ExecID (generated when empty).
func (b *LocalBackend) Exec(ctx context.Context, id string, opts ExecOptions) error {
	c, err := b.lookup(id)
	if err != nil {
		return err
	}
	b.mu.Lock()
	running := c.state == StateRunning
	cfg := c.cfg
	if running && len(opts.Cmd) > 0 {
		execID := strings.TrimSpace(opts.ExecID)
		if execID == "" {
			c.execSeq++
			execID = fmt.Sprintf("exec-%d", c.execSeq)
		}
		c.execs[execID] = &execRecord{
			rec: process.Info{
				ID:        execID,
				Command:   opts.Cmd[0],
				Args:      append([]string(nil), opts.Cmd[1:]...),
				State:     types.ProcessRunning,
				StartedAt: time.Now().UTC(),
			},
			notify: make(chan struct{}, 1),
			done:   make(chan struct{}),
		}
		opts.ExecID = execID
	}
	b.mu.Unlock()
	if !running {
		return fmt.Errorf("container: exec %s: not running", id)
	}
	if len(opts.Cmd) == 0 {
		return fmt.Errorf("container: exec %s: empty command", id)
	}
	rec := b.execRecord(id, opts.ExecID)
	cmd := exec.CommandContext(ctx, opts.Cmd[0], opts.Cmd[1:]...)
	if cfg.Dir != "" {
		cmd.Dir = cfg.Dir
	}
	if cfg.Env != nil {
		cmd.Env = append(cmd.Environ(), cfg.Env...)
	}
	cmd.Stdin = opts.Stdin
	cmd.Stdout = teeWriter{rec: rec, dst: opts.Stdout}
	cmd.Stderr = teeWriter{rec: rec, dst: opts.Stderr}
	runErr := cmd.Run()
	code := 0
	if runErr != nil {
		if exitErr, ok := runErr.(*exec.ExitError); ok {
			code = exitErr.ExitCode()
		} else {
			code = -1
		}
	}
	if rec != nil {
		if cmd.Process != nil {
			rec.mu.Lock()
			rec.rec.PID = cmd.Process.Pid
			rec.mu.Unlock()
		}
		rec.finish(code)
	}
	if runErr != nil {
		return fmt.Errorf("container: exec %s: %w", id, runErr)
	}
	return nil
}

// execRecord returns the tracked exec record, if any.
func (b *LocalBackend) execRecord(id, execID string) *execRecord {
	b.mu.Lock()
	defer b.mu.Unlock()
	if c, ok := b.containers[id]; ok {
		return c.execs[execID]
	}
	return nil
}

// teeWriter records command output while forwarding it.
type teeWriter struct {
	rec *execRecord
	dst io.Writer
}

func (w teeWriter) Write(p []byte) (int, error) {
	if w.rec != nil {
		_, _ = w.rec.Write(p)
	}
	if w.dst == nil {
		return len(p), nil
	}
	return w.dst.Write(p)
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

// Processes lists the container main process and the tracked exec
// commands. Exec entries are ordered by start time.
func (b *LocalBackend) Processes(ctx context.Context, id string) ([]process.Info, error) {
	c, err := b.lookup(id)
	if err != nil {
		return nil, err
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	b.mu.Lock()
	procID := c.procID
	recs := make([]*execRecord, 0, len(c.execs))
	for _, r := range c.execs {
		recs = append(recs, r)
	}
	b.mu.Unlock()
	out := []process.Info{}
	if procID != "" {
		info, err := b.pm.Inspect(ctx, procID)
		if err != nil {
			if !errors.Is(err, process.ErrNotFound) {
				return nil, fmt.Errorf("container: processes %s: %w", id, err)
			}
		} else {
			info.ID = MainProcessID
			out = append(out, info)
		}
	}
	infos := make([]process.Info, 0, len(recs))
	for _, r := range recs {
		infos = append(infos, r.snapshot())
	}
	sortInfos(infos)
	return append(out, infos...), nil
}

// Logs returns the combined output of one container process.
func (b *LocalBackend) Logs(ctx context.Context, id, processID string, follow bool) (io.ReadCloser, error) {
	c, err := b.lookup(id)
	if err != nil {
		return nil, err
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	b.mu.Lock()
	procID := c.procID
	rec := c.execs[processID]
	b.mu.Unlock()
	if processID == MainProcessID {
		if procID == "" {
			return nil, fmt.Errorf("%w: %s in container %s", ErrProcessNotFound, processID, id)
		}
		if follow {
			rc, err := b.pm.Logs(ctx, procID)
			return rc, mapProcessErr(id, processID, err)
		}
		snap, err := b.pm.LogsSnapshot(ctx, procID)
		if err != nil {
			return nil, mapProcessErr(id, processID, err)
		}
		return io.NopCloser(bytes.NewReader(snap)), nil
	}
	if rec == nil {
		return nil, fmt.Errorf("%w: %s in container %s", ErrProcessNotFound, processID, id)
	}
	if follow {
		return &execLogReader{ctx: ctx, rec: rec}, nil
	}
	return io.NopCloser(bytes.NewReader(rec.snapshotLogs())), nil
}

// mapProcessErr rewrites a process-manager lookup miss as
// ErrProcessNotFound and passes other errors through.
func mapProcessErr(id, processID string, err error) error {
	if err == nil {
		return nil
	}
	if errors.Is(err, process.ErrNotFound) {
		return fmt.Errorf("%w: %s in container %s", ErrProcessNotFound, processID, id)
	}
	return err
}

// execLogReader streams an exec record buffer from the beginning,
// blocking for new output until the command exits.
type execLogReader struct {
	ctx context.Context
	rec *execRecord
	off int
}

func (r *execLogReader) Read(b []byte) (int, error) {
	for {
		r.rec.mu.Lock()
		if r.off < len(r.rec.logs) {
			n := copy(b, r.rec.logs[r.off:])
			r.off += n
			r.rec.mu.Unlock()
			return n, nil
		}
		r.rec.mu.Unlock()
		select {
		case <-r.rec.done:
			r.rec.mu.Lock()
			if r.off < len(r.rec.logs) {
				n := copy(b, r.rec.logs[r.off:])
				r.off += n
				r.rec.mu.Unlock()
				return n, nil
			}
			r.rec.mu.Unlock()
			return 0, io.EOF
		case <-r.ctx.Done():
			return 0, r.ctx.Err()
		case <-r.rec.notify:
		}
	}
}

// Close releases the reader. It is a no-op: readers share the buffer.
func (r *execLogReader) Close() error { return nil }
