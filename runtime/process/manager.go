// Package process manages OS processes on behalf of workspaces.
//
// It defines the ProcessManager interface (start, stop, signal, wait and
// log streaming), a local implementation backed by os/exec and a fake
// implementation for tests.
package process

import (
	"context"
	"errors"
	"io"
	"os"
)

// Errors returned by ProcessManager implementations.
var (
	// ErrNotFound is returned when no process matches the given id.
	ErrNotFound = errors.New("process: not found")
	// ErrExited is returned when signalling a process that already exited.
	ErrExited = errors.New("process: already exited")
)

// StartSpec describes the program to start.
type StartSpec struct {
	// Command is the program to run (required).
	Command string
	// Args are the program arguments.
	Args []string
	// Dir is the working directory. Empty means the current directory.
	Dir string
	// Env holds extra KEY=VALUE variables appended to the OS environment.
	Env []string
	// Stdin is wired to the program standard input. Nil reads from the
	// null device.
	Stdin io.Reader
}

// ProcessManager starts and supervises OS processes.
//
// Implementations must be safe for concurrent use.
type ProcessManager interface {
	// Start launches a program and returns its process id.
	Start(ctx context.Context, spec StartSpec) (string, error)
	// Stop kills a process. It is idempotent: stopping an exited
	// process returns nil.
	Stop(ctx context.Context, id string) error
	// Signal sends sig to a running process. It returns ErrExited
	// (wrapped) when the process already exited.
	Signal(ctx context.Context, id string, sig os.Signal) error
	// Wait blocks until the process exits or ctx is done, and returns
	// the exit code. A process killed by a signal reports -1.
	Wait(ctx context.Context, id string) (int, error)
	// Logs returns a stream of the combined stdout/stderr output from
	// the beginning. Reads block for new output and return io.EOF once
	// the process exited and the buffer is drained.
	Logs(ctx context.Context, id string) (io.ReadCloser, error)
	// Inspect returns a snapshot of the process metadata.
	Inspect(ctx context.Context, id string) (Info, error)
}
