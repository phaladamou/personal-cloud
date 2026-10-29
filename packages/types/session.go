package types

import (
	"fmt"
	"strings"
	"time"
)

// Session represents a client connection to a workspace (terminal,
// file browser, service view) from a specific device.
type Session struct {
	ID          string    `json:"id"`
	WorkspaceID string    `json:"workspace_id"`
	UserID      string    `json:"user_id"`
	Device      string    `json:"device,omitempty"`
	ConnectedAt time.Time `json:"connected_at"`
}

// NewSession builds a validated session for a user connected to a workspace.
func NewSession(id, workspaceID, userID, device string) (Session, error) {
	id, err := trimAndRequire(id, ErrEmptyID)
	if err != nil {
		return Session{}, err
	}
	workspaceID, err = trimAndRequire(workspaceID, ErrEmptyWorkspaceID)
	if err != nil {
		return Session{}, err
	}
	userID, err = trimAndRequire(userID, ErrEmptyUserID)
	if err != nil {
		return Session{}, err
	}
	return Session{
		ID:          id,
		WorkspaceID: workspaceID,
		UserID:      userID,
		Device:      strings.TrimSpace(device),
		ConnectedAt: nowUTC(),
	}, nil
}

// String returns a short human-readable representation of the session.
func (s Session) String() string {
	if s.Device != "" {
		return fmt.Sprintf("session %s on workspace %s (%s)", s.ID, s.WorkspaceID, s.Device)
	}
	return fmt.Sprintf("session %s on workspace %s", s.ID, s.WorkspaceID)
}
