package types

import (
	"fmt"
	"strings"
	"time"
)

// ProcessState is the execution state of a process running in a workspace.
type ProcessState string

// Valid process states.
const (
	ProcessRunning ProcessState = "RUNNING"
	ProcessExited  ProcessState = "EXITED"
	ProcessKilled  ProcessState = "KILLED"
)

// Valid reports whether s is a known process state.
func (s ProcessState) Valid() bool {
	switch s {
	case ProcessRunning, ProcessExited, ProcessKilled:
		return true
	default:
		return false
	}
}

// String returns the state as a plain string.
func (s ProcessState) String() string {
	return string(s)
}

// Process is a program running inside a workspace runtime.
type Process struct {
	ID          string       `json:"id"`
	WorkspaceID string       `json:"workspace_id"`
	Command     string       `json:"command"`
	Args        []string     `json:"args,omitempty"`
	PID         int          `json:"pid,omitempty"`
	State       ProcessState `json:"state"`
	ExitCode    *int         `json:"exit_code,omitempty"`
	StartedAt   time.Time    `json:"started_at"`
}

// NewProcess builds a validated process in RUNNING state.
func NewProcess(id, workspaceID, command string, args []string) (Process, error) {
	id, err := trimAndRequire(id, ErrEmptyID)
	if err != nil {
		return Process{}, err
	}
	workspaceID, err = trimAndRequire(workspaceID, ErrEmptyWorkspaceID)
	if err != nil {
		return Process{}, err
	}
	command = strings.TrimSpace(command)
	if command == "" {
		return Process{}, fmt.Errorf("types: empty command")
	}
	return Process{
		ID:          id,
		WorkspaceID: workspaceID,
		Command:     command,
		Args:        args,
		State:       ProcessRunning,
		StartedAt:   nowUTC(),
	}, nil
}

// String returns a short human-readable representation of the process.
func (p Process) String() string {
	return fmt.Sprintf("%s %s (%s)", p.ID, p.Command, p.State)
}
