// Package container isolates workspace workloads.
//
// It defines the ContainerBackend interface (create, start, stop, remove
// and exec with streaming stdio) with two implementations: a local
// process-based backend that works everywhere and is the default for
// Phase 1, and a docker backend wrapping the docker CLI, used only when
// a daemon is present.
package container

import (
	"context"
	"errors"
	"io"
)

// ErrNotFound is returned when no container matches the given id.
var ErrNotFound = errors.New("container: not found")

// State is the lifecycle state of a container.
type State string

// Valid container states.
const (
	StateCreated State = "CREATED"
	StateRunning State = "RUNNING"
	StateStopped State = "STOPPED"
)

// Config describes the container to create.
type Config struct {
	// Name is a human label. Empty lets the backend pick one.
	Name string
	// Image is the container image. It is required by the docker
	// backend and ignored by the local backend.
	Image string
	// Command is the program to run (required).
	Command string
	// Args are the program arguments.
	Args []string
	// Env holds extra KEY=VALUE variables.
	Env []string
	// Dir is the working directory. Empty means the backend default.
	Dir string
}

// ExecOptions describes a command to run inside a running container.
type ExecOptions struct {
	// Cmd is the program and arguments to run (required).
	Cmd []string
	// Stdin is wired to the command standard input. Nil reads from the
	// null device.
	Stdin io.Reader
	// Stdout and Stderr receive the command output. Nil discards it.
	Stdout io.Writer
	Stderr io.Writer
}

// Info is a point-in-time snapshot of a container.
type Info struct {
	ID    string
	Name  string
	State State
}

// ContainerBackend manages containers. Implementations must be safe for
// concurrent use.
type ContainerBackend interface {
	// Create registers a container and returns its id.
	Create(ctx context.Context, cfg Config) (string, error)
	// Start boots a created or stopped container.
	Start(ctx context.Context, id string) error
	// Stop halts a running container. It is idempotent.
	Stop(ctx context.Context, id string) error
	// Remove deletes a container, stopping it first when running.
	Remove(ctx context.Context, id string) error
	// Exec runs a command in a running container, streaming stdio.
	Exec(ctx context.Context, id string, opts ExecOptions) error
	// Inspect returns a snapshot of the container metadata.
	Inspect(ctx context.Context, id string) (Info, error)
}

// DefaultBackend returns the Phase 1 default backend: local processes.
func DefaultBackend() ContainerBackend {
	return NewLocalBackend()
}
