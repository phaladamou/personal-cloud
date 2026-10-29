package types

import (
	"fmt"
	"strings"
	"time"
)

// JobStatus is the execution state of a background job.
type JobStatus string

// Valid job states.
const (
	JobPending   JobStatus = "PENDING"
	JobRunning   JobStatus = "RUNNING"
	JobSucceeded JobStatus = "SUCCEEDED"
	JobFailed    JobStatus = "FAILED"
)

// Valid reports whether s is a known job status.
func (s JobStatus) Valid() bool {
	switch s {
	case JobPending, JobRunning, JobSucceeded, JobFailed:
		return true
	default:
		return false
	}
}

// String returns the status as a plain string.
func (s JobStatus) String() string {
	return string(s)
}

// Job is a unit of background work scheduled by the control plane
// (snapshot, restore, migration).
type Job struct {
	ID          string    `json:"id"`
	WorkspaceID string    `json:"workspace_id"`
	Kind        string    `json:"kind"`
	Status      JobStatus `json:"status"`
	Error       string    `json:"error,omitempty"`
	CreatedAt   time.Time `json:"created_at"`
	UpdatedAt   time.Time `json:"updated_at"`
}

// NewJob builds a validated job in PENDING status.
func NewJob(id, workspaceID, kind string) (Job, error) {
	id, err := trimAndRequire(id, ErrEmptyID)
	if err != nil {
		return Job{}, err
	}
	workspaceID, err = trimAndRequire(workspaceID, ErrEmptyWorkspaceID)
	if err != nil {
		return Job{}, err
	}
	kind = strings.TrimSpace(kind)
	if kind == "" {
		return Job{}, fmt.Errorf("types: empty job kind")
	}
	now := nowUTC()
	return Job{
		ID:          id,
		WorkspaceID: workspaceID,
		Kind:        kind,
		Status:      JobPending,
		CreatedAt:   now,
		UpdatedAt:   now,
	}, nil
}

// String returns a short human-readable representation of the job.
func (j Job) String() string {
	return fmt.Sprintf("%s %s (%s)", j.ID, j.Kind, j.Status)
}
