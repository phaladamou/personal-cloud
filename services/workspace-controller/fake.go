package controller

import (
	"context"
	"fmt"
	"io"
	"strings"
	"sync"

	"github.com/phaladamou/personal-cloud/runtime/container"
	"github.com/phaladamou/personal-cloud/runtime/process"
)

// FakeExec records one Exec call on a FakeBackend.
type FakeExec struct {
	ID     string
	Cmd    []string
	ExecID string
}

// FakeLogCall records one Logs call on a FakeBackend.
type FakeLogCall struct {
	ID        string
	ProcessID string
	Follow    bool
}

// FakeBackend is an in-memory scripted ContainerBackend for tests.
//
// Containers start in CREATED state; Start moves them to RUNNING and
// Stop back to STOPPED. Calls are recorded in Configs and Execs for
// assertions. The zero value is ready to use.
type FakeBackend struct {
	mu            sync.Mutex
	seq           int
	Configs       map[string]container.Config
	States        map[string]container.State
	Execs         []FakeExec
	Starts        int
	Stops         int
	Removes       int
	ExecOutput    string
	ExecFunc      func(ctx context.Context, id string, opts container.ExecOptions) error
	ProcessesList []process.Info
	LogCalls      []FakeLogCall
	LogsOutput    string

	CreateErr    error
	StartErr     error
	StopErr      error
	RemoveErr    error
	ExecErr      error
	InspectErr   error
	ProcessesErr error
	LogsErr      error
}

var _ container.ContainerBackend = (*FakeBackend)(nil)

func (f *FakeBackend) ensureLocked() {
	if f.Configs == nil {
		f.Configs = make(map[string]container.Config)
	}
	if f.States == nil {
		f.States = make(map[string]container.State)
	}
}

// Create registers a container and returns its id.
func (f *FakeBackend) Create(ctx context.Context, cfg container.Config) (string, error) {
	if err := ctx.Err(); err != nil {
		return "", err
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.CreateErr != nil {
		return "", f.CreateErr
	}
	f.ensureLocked()
	f.seq++
	id := fmt.Sprintf("fake-ctr-%d", f.seq)
	f.Configs[id] = cfg
	f.States[id] = container.StateCreated
	return id, nil
}

// Start boots a created or stopped container.
func (f *FakeBackend) Start(ctx context.Context, id string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.StartErr != nil {
		return f.StartErr
	}
	st, ok := f.States[id]
	if !ok {
		return fmt.Errorf("%w: %s", container.ErrNotFound, id)
	}
	if st == container.StateRunning {
		return fmt.Errorf("controller: fake %s already running", id)
	}
	f.States[id] = container.StateRunning
	f.Starts++
	return nil
}

// Stop halts a running container. It is idempotent.
func (f *FakeBackend) Stop(ctx context.Context, id string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.StopErr != nil {
		return f.StopErr
	}
	if _, ok := f.States[id]; !ok {
		return fmt.Errorf("%w: %s", container.ErrNotFound, id)
	}
	f.States[id] = container.StateStopped
	f.Stops++
	return nil
}

// Remove deletes a container, stopping it first when running.
func (f *FakeBackend) Remove(ctx context.Context, id string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.RemoveErr != nil {
		return f.RemoveErr
	}
	if _, ok := f.States[id]; !ok {
		return fmt.Errorf("%w: %s", container.ErrNotFound, id)
	}
	delete(f.Configs, id)
	delete(f.States, id)
	f.Removes++
	return nil
}

// Exec records the call, then runs ExecFunc when set; otherwise it
// writes ExecOutput to Stdout and returns ExecErr.
func (f *FakeBackend) Exec(ctx context.Context, id string, opts container.ExecOptions) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	f.mu.Lock()
	if f.States == nil {
		f.States = make(map[string]container.State)
	}
	_, ok := f.States[id]
	execFunc := f.ExecFunc
	output := f.ExecOutput
	execErr := f.ExecErr
	cmd := append([]string(nil), opts.Cmd...)
	f.Execs = append(f.Execs, FakeExec{ID: id, Cmd: cmd, ExecID: opts.ExecID})
	stdout := opts.Stdout
	f.mu.Unlock()
	if !ok {
		return fmt.Errorf("%w: %s", container.ErrNotFound, id)
	}
	if execFunc != nil {
		return execFunc(ctx, id, opts)
	}
	if execErr != nil {
		return execErr
	}
	if output != "" && stdout != nil {
		if _, err := stdout.Write([]byte(output)); err != nil {
			return err
		}
	}
	return nil
}

// Inspect returns a snapshot of the container metadata.
func (f *FakeBackend) Inspect(ctx context.Context, id string) (container.Info, error) {
	if err := ctx.Err(); err != nil {
		return container.Info{}, err
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.InspectErr != nil {
		return container.Info{}, f.InspectErr
	}
	st, ok := f.States[id]
	if !ok {
		return container.Info{}, fmt.Errorf("%w: %s", container.ErrNotFound, id)
	}
	return container.Info{ID: id, Name: f.Configs[id].Name, State: st}, nil
}

// Processes returns the scripted process list for a known container.
func (f *FakeBackend) Processes(ctx context.Context, id string) ([]process.Info, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.ProcessesErr != nil {
		return nil, f.ProcessesErr
	}
	if _, ok := f.States[id]; !ok {
		return nil, fmt.Errorf("%w: %s", container.ErrNotFound, id)
	}
	out := make([]process.Info, len(f.ProcessesList))
	copy(out, f.ProcessesList)
	return out, nil
}

// Logs records the call and returns the scripted output for a known
// container and a scripted process id.
func (f *FakeBackend) Logs(ctx context.Context, id, processID string, follow bool) (io.ReadCloser, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.LogsErr != nil {
		return nil, f.LogsErr
	}
	if _, ok := f.States[id]; !ok {
		return nil, fmt.Errorf("%w: %s", container.ErrNotFound, id)
	}
	f.LogCalls = append(f.LogCalls, FakeLogCall{ID: id, ProcessID: processID, Follow: follow})
	for _, p := range f.ProcessesList {
		if p.ID == processID {
			return io.NopCloser(strings.NewReader(f.LogsOutput)), nil
		}
	}
	return nil, fmt.Errorf("%w: %s in container %s", container.ErrProcessNotFound, processID, id)
}
