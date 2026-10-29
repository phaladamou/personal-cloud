package types

import (
	"fmt"
	"strings"
	"time"
)

// WorkspaceStatus is the lifecycle state of a workspace.
type WorkspaceStatus string

// Valid workspace lifecycle states.
const (
	WorkspaceCreated   WorkspaceStatus = "CREATED"
	WorkspaceStarting  WorkspaceStatus = "STARTING"
	WorkspaceRunning   WorkspaceStatus = "RUNNING"
	WorkspaceSleeping  WorkspaceStatus = "SLEEPING"
	WorkspaceStopped   WorkspaceStatus = "STOPPED"
	WorkspaceRestoring WorkspaceStatus = "RESTORING"
)

// ValidWorkspaceTransitions maps each workspace status to the set of
// statuses it may legally move to.
var ValidWorkspaceTransitions = map[WorkspaceStatus]map[WorkspaceStatus]bool{
	WorkspaceCreated: {
		WorkspaceStarting: true,
	},
	WorkspaceStarting: {
		WorkspaceRunning: true,
		WorkspaceStopped: true,
	},
	WorkspaceRunning: {
		WorkspaceSleeping: true,
		WorkspaceStopped:  true,
	},
	WorkspaceSleeping: {
		WorkspaceRunning: true,
		WorkspaceStopped: true,
	},
	WorkspaceStopped: {
		WorkspaceStarting:  true,
		WorkspaceRestoring: true,
	},
	WorkspaceRestoring: {
		WorkspaceRunning: true,
		WorkspaceStopped: true,
	},
}

// Valid reports whether s is a known workspace status.
func (s WorkspaceStatus) Valid() bool {
	_, ok := ValidWorkspaceTransitions[s]
	return ok
}

// CanTransitionTo reports whether a workspace in status s may move to next.
func (s WorkspaceStatus) CanTransitionTo(next WorkspaceStatus) bool {
	return ValidWorkspaceTransitions[s][next]
}

// String returns the status as a plain string.
func (s WorkspaceStatus) String() string {
	return string(s)
}

// Workspace is the fundamental unit of Personal Cloud: a persistent
// computing environment owned by a user.
//
// Resources holds the compute requirements recorded at creation (zero
// means "no requirement": the workspace runs wherever the local
// backend is). Selector constrains placement to the nodes whose
// labels satisfy it. NodeID is the scheduler placement, set on Start
// and cleared on Stop; empty means unplaced.
type Workspace struct {
	ID        string            `json:"id"`
	UserID    string            `json:"user_id"`
	Name      string            `json:"name"`
	Status    WorkspaceStatus   `json:"status"`
	NodeID    string            `json:"node_id,omitempty"`
	Resources Resource          `json:"resources"`
	Selector  map[string]string `json:"selector,omitempty"`
	CreatedAt time.Time         `json:"created_at"`
	UpdatedAt time.Time         `json:"updated_at"`
}

// NewWorkspace builds a validated workspace in CREATED status.
func NewWorkspace(id, userID, name string) (Workspace, error) {
	id, err := trimAndRequire(id, ErrEmptyID)
	if err != nil {
		return Workspace{}, err
	}
	userID, err = trimAndRequire(userID, ErrEmptyUserID)
	if err != nil {
		return Workspace{}, err
	}
	name = strings.TrimSpace(name)
	if name == "" {
		return Workspace{}, ErrEmptyName
	}
	now := nowUTC()
	return Workspace{
		ID:        id,
		UserID:    userID,
		Name:      name,
		Status:    WorkspaceCreated,
		CreatedAt: now,
		UpdatedAt: now,
	}, nil
}

// TransitionTo moves the workspace to the next status if the transition
// is valid, and refreshes UpdatedAt.
func (w *Workspace) TransitionTo(next WorkspaceStatus) error {
	if !next.Valid() {
		return fmt.Errorf("types: unknown workspace status %q", next)
	}
	if !w.Status.CanTransitionTo(next) {
		return fmt.Errorf("types: invalid workspace transition %s -> %s", w.Status, next)
	}
	w.Status = next
	w.UpdatedAt = nowUTC()
	return nil
}

// WantsPlacement reports whether the workspace carries placement
// requirements: non-zero resources or a non-empty node selector.
func (w Workspace) WantsPlacement() bool {
	return w.Resources != (Resource{}) || len(w.Selector) > 0
}

// String returns a short human-readable representation of the workspace.
func (w Workspace) String() string {
	return fmt.Sprintf("%s (%s)", w.Name, w.Status)
}
