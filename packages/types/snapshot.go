package types

import (
	"fmt"
	"strings"
	"time"
)

// Snapshot is a point-in-time capture of a workspace volume, used for
// backup and restore.
type Snapshot struct {
	ID          string    `json:"id"`
	WorkspaceID string    `json:"workspace_id"`
	VolumeID    string    `json:"volume_id"`
	Name        string    `json:"name,omitempty"`
	SizeBytes   int64     `json:"size_bytes,omitempty"`
	CreatedAt   time.Time `json:"created_at"`
}

// NewSnapshot builds a validated snapshot of a volume.
func NewSnapshot(id, workspaceID, volumeID, name string) (Snapshot, error) {
	id, err := trimAndRequire(id, ErrEmptyID)
	if err != nil {
		return Snapshot{}, err
	}
	workspaceID, err = trimAndRequire(workspaceID, ErrEmptyWorkspaceID)
	if err != nil {
		return Snapshot{}, err
	}
	volumeID, err = trimAndRequire(volumeID, fmt.Errorf("types: empty volume id"))
	if err != nil {
		return Snapshot{}, err
	}
	return Snapshot{
		ID:          id,
		WorkspaceID: workspaceID,
		VolumeID:    volumeID,
		Name:        strings.TrimSpace(name),
		CreatedAt:   nowUTC(),
	}, nil
}

// String returns a short human-readable representation of the snapshot.
func (s Snapshot) String() string {
	if s.Name != "" {
		return fmt.Sprintf("%s (%s)", s.Name, s.ID)
	}
	return fmt.Sprintf("snapshot %s", s.ID)
}
