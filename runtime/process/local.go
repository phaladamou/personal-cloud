package process

import (
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/phaladamou/personal-cloud/packages/types"
)

// LocalManager is a ProcessManager backed by the local OS via os/exec.
//
// The process lifetime is independent of the context passed to Start:
// cancelling that context does not stop the process.
type LocalManager struct {
	mu    sync.Mutex
	seq   uint64
	procs map[string]*localProcess
}

// NewLocalManager builds an empty LocalManager.
func NewLocalManager() *LocalManager {
	return &LocalManager{procs: make(map[string]*localProcess)}
}

var _ ProcessManager = (*LocalManager)(nil)

// localProcess is a single supervised OS process. It doubles as the
// io.Writer receiving the combined stdout/stderr of the command.
type localProcess struct {
	cmd  *exec.Cmd
	info Info

	mu     sync.Mutex
	logs   []byte
	notify chan struct{} // capacity 1, signalled on new output or exit
	done   chan struct{} // closed when the process exits
}

// Write appends command output to the log buffer.
func (p *localProcess) Write(b []byte) (int, error) {
	p.mu.Lock()
	p.logs = append(p.logs, b...)
	p.mu.Unlock()
	select {
	case p.notify <- struct{}{}:
	default:
	}
	return len(b), nil
}

// reap waits for the command, records the exit code and wakes waiters.
func (p *localProcess) reap() {
	err := p.cmd.Wait()
	code := 0
	if err != nil {
		if exitErr, ok := err.(*exec.ExitError); ok {
			code = exitErr.ExitCode()
		} else {
			code = -1
		}
	}
	p.mu.Lock()
	p.info.ExitCode = &code
	if code == -1 {
		p.info.State = types.ProcessKilled
	} else {
		p.info.State = types.ProcessExited
	}
	p.mu.Unlock()
	close(p.done)
	select {
	case p.notify <- struct{}{}:
	default:
	}
}

func (m *LocalManager) lookup(id string) (*localProcess, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	p, ok := m.procs[id]
	if !ok {
		return nil, fmt.Errorf("%w: %s", ErrNotFound, id)
	}
	return p, nil
}

// Start launches a program and returns its process id.
func (m *LocalManager) Start(ctx context.Context, spec StartSpec) (string, error) {
	if err := ctx.Err(); err != nil {
		return "", err
	}
	if strings.TrimSpace(spec.Command) == "" {
		return "", fmt.Errorf("process: empty command")
	}
	p := &localProcess{
		notify: make(chan struct{}, 1),
		done:   make(chan struct{}),
	}
	cmd := exec.Command(spec.Command, spec.Args...)
	if spec.Dir != "" {
		cmd.Dir = spec.Dir
	}
	if spec.Env != nil {
		cmd.Env = append(os.Environ(), spec.Env...)
	}
	if spec.Stdin != nil {
		cmd.Stdin = spec.Stdin
	}
	cmd.Stdout = p
	cmd.Stderr = p
	if err := cmd.Start(); err != nil {
		return "", fmt.Errorf("process: start %q: %w", spec.Command, err)
	}
	p.cmd = cmd
	p.info = Info{
		Command:   spec.Command,
		Args:      spec.Args,
		Dir:       spec.Dir,
		Env:       spec.Env,
		PID:       cmd.Process.Pid,
		State:     types.ProcessRunning,
		StartedAt: time.Now().UTC(),
	}
	id := fmt.Sprintf("proc-%d", atomic.AddUint64(&m.seq, 1))
	p.info.ID = id
	m.mu.Lock()
	m.procs[id] = p
	m.mu.Unlock()
	go p.reap()
	return id, nil
}

// Stop kills a process. Stopping an exited process returns nil.
func (m *LocalManager) Stop(ctx context.Context, id string) error {
	p, err := m.lookup(id)
	if err != nil {
		return err
	}
	select {
	case <-p.done:
		return nil
	default:
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := p.cmd.Process.Kill(); err != nil {
		select {
		case <-p.done:
			return nil
		default:
		}
		return fmt.Errorf("process: stop %s: %w", id, err)
	}
	return nil
}

// Signal sends sig to a running process.
func (m *LocalManager) Signal(ctx context.Context, id string, sig os.Signal) error {
	p, err := m.lookup(id)
	if err != nil {
		return err
	}
	select {
	case <-p.done:
		return fmt.Errorf("%w: %s", ErrExited, id)
	default:
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := p.cmd.Process.Signal(sig); err != nil {
		select {
		case <-p.done:
			return fmt.Errorf("%w: %s", ErrExited, id)
		default:
		}
		return fmt.Errorf("process: signal %s: %w", id, err)
	}
	return nil
}

// Wait blocks until the process exits or ctx is done.
func (m *LocalManager) Wait(ctx context.Context, id string) (int, error) {
	p, err := m.lookup(id)
	if err != nil {
		return 0, err
	}
	select {
	case <-ctx.Done():
		return 0, ctx.Err()
	case <-p.done:
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.info.ExitCode == nil {
		return 0, fmt.Errorf("process: missing exit code for %s", id)
	}
	return *p.info.ExitCode, nil
}

// Logs returns a stream of the combined stdout/stderr output.
func (m *LocalManager) Logs(ctx context.Context, id string) (io.ReadCloser, error) {
	p, err := m.lookup(id)
	if err != nil {
		return nil, err
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	return &logReader{ctx: ctx, p: p}, nil
}

// logReader streams the log buffer from the beginning, blocking for new
// output until the process exits.
type logReader struct {
	ctx context.Context
	p   *localProcess
	off int
}

func (r *logReader) Read(b []byte) (int, error) {
	for {
		r.p.mu.Lock()
		if r.off < len(r.p.logs) {
			n := copy(b, r.p.logs[r.off:])
			r.off += n
			r.p.mu.Unlock()
			return n, nil
		}
		r.p.mu.Unlock()
		select {
		case <-r.p.done:
			r.p.mu.Lock()
			if r.off < len(r.p.logs) {
				n := copy(b, r.p.logs[r.off:])
				r.off += n
				r.p.mu.Unlock()
				return n, nil
			}
			r.p.mu.Unlock()
			return 0, io.EOF
		case <-r.ctx.Done():
			return 0, r.ctx.Err()
		case <-r.p.notify:
		}
	}
}

// Close releases the reader. It is a no-op: readers share the buffer.
func (r *logReader) Close() error { return nil }

// Inspect returns a snapshot of the process metadata.
func (m *LocalManager) Inspect(ctx context.Context, id string) (Info, error) {
	p, err := m.lookup(id)
	if err != nil {
		return Info{}, err
	}
	if err := ctx.Err(); err != nil {
		return Info{}, err
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.info, nil
}
