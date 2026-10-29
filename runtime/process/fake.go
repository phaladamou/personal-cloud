package process

import (
	"context"
	"fmt"
	"io"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/phaladamou/personal-cloud/packages/types"
)

// FakeProcessManager is an in-memory scripted ProcessManager for tests.
//
// Started processes complete immediately: Wait returns ExitCode and Logs
// returns Output. Calls are recorded in Specs, Stopped and Signals for
// assertions. The zero value is ready to use.
type FakeProcessManager struct {
	mu      sync.Mutex
	seq     int
	Specs   map[string]StartSpec
	Stopped map[string]bool
	Signals map[string][]os.Signal

	ExitCode   int
	Output     string
	StartErr   error
	StopErr    error
	SignalErr  error
	WaitErr    error
	LogsErr    error
	InspectErr error
}

var _ ProcessManager = (*FakeProcessManager)(nil)

func (f *FakeProcessManager) ensureLocked() {
	if f.Specs == nil {
		f.Specs = make(map[string]StartSpec)
	}
	if f.Stopped == nil {
		f.Stopped = make(map[string]bool)
	}
	if f.Signals == nil {
		f.Signals = make(map[string][]os.Signal)
	}
}

// Start records the spec and returns a fake process id.
func (f *FakeProcessManager) Start(ctx context.Context, spec StartSpec) (string, error) {
	if err := ctx.Err(); err != nil {
		return "", err
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.StartErr != nil {
		return "", f.StartErr
	}
	f.ensureLocked()
	f.seq++
	id := fmt.Sprintf("fake-%d", f.seq)
	f.Specs[id] = spec
	return id, nil
}

// Stop marks the process as stopped.
func (f *FakeProcessManager) Stop(ctx context.Context, id string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.StopErr != nil {
		return f.StopErr
	}
	if _, ok := f.Specs[id]; !ok {
		return fmt.Errorf("%w: %s", ErrNotFound, id)
	}
	f.Stopped[id] = true
	return nil
}

// Signal records the signal.
func (f *FakeProcessManager) Signal(ctx context.Context, id string, sig os.Signal) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.SignalErr != nil {
		return f.SignalErr
	}
	if _, ok := f.Specs[id]; !ok {
		return fmt.Errorf("%w: %s", ErrNotFound, id)
	}
	f.Signals[id] = append(f.Signals[id], sig)
	return nil
}

// Wait returns the scripted exit code.
func (f *FakeProcessManager) Wait(ctx context.Context, id string) (int, error) {
	if err := ctx.Err(); err != nil {
		return 0, err
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.WaitErr != nil {
		return 0, f.WaitErr
	}
	if _, ok := f.Specs[id]; !ok {
		return 0, fmt.Errorf("%w: %s", ErrNotFound, id)
	}
	return f.ExitCode, nil
}

// Logs returns the scripted output.
func (f *FakeProcessManager) Logs(ctx context.Context, id string) (io.ReadCloser, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.LogsErr != nil {
		return nil, f.LogsErr
	}
	if _, ok := f.Specs[id]; !ok {
		return nil, fmt.Errorf("%w: %s", ErrNotFound, id)
	}
	return io.NopCloser(strings.NewReader(f.Output)), nil
}

// Inspect returns metadata for a recorded process.
func (f *FakeProcessManager) Inspect(ctx context.Context, id string) (Info, error) {
	if err := ctx.Err(); err != nil {
		return Info{}, err
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.InspectErr != nil {
		return Info{}, f.InspectErr
	}
	spec, ok := f.Specs[id]
	if !ok {
		return Info{}, fmt.Errorf("%w: %s", ErrNotFound, id)
	}
	info := Info{
		ID:        id,
		Command:   spec.Command,
		Args:      spec.Args,
		Dir:       spec.Dir,
		Env:       spec.Env,
		State:     types.ProcessRunning,
		StartedAt: time.Now().UTC(),
	}
	if f.Stopped[id] {
		info.State = types.ProcessExited
		code := f.ExitCode
		info.ExitCode = &code
	}
	return info, nil
}
