package types

import (
	"fmt"
	"strings"
	"time"
)

// Volume is a persistent filesystem attached to a workspace.
type Volume struct {
	ID          string    `json:"id"`
	WorkspaceID string    `json:"workspace_id"`
	Name        string    `json:"name"`
	SizeGB      int64     `json:"size_gb"`
	MountPath   string    `json:"mount_path"`
	CreatedAt   time.Time `json:"created_at"`
}

// NewVolume builds a validated volume. sizeGB must be positive and
// mountPath must be an absolute path.
func NewVolume(id, workspaceID, name string, sizeGB int64, mountPath string) (Volume, error) {
	id, err := trimAndRequire(id, ErrEmptyID)
	if err != nil {
		return Volume{}, err
	}
	workspaceID, err = trimAndRequire(workspaceID, ErrEmptyWorkspaceID)
	if err != nil {
		return Volume{}, err
	}
	name = strings.TrimSpace(name)
	if name == "" {
		return Volume{}, ErrEmptyName
	}
	if sizeGB <= 0 {
		return Volume{}, fmt.Errorf("types: size_gb must be positive, got %d", sizeGB)
	}
	mountPath = strings.TrimSpace(mountPath)
	if !strings.HasPrefix(mountPath, "/") {
		return Volume{}, fmt.Errorf("types: mount_path must be absolute, got %q", mountPath)
	}
	return Volume{
		ID:          id,
		WorkspaceID: workspaceID,
		Name:        name,
		SizeGB:      sizeGB,
		MountPath:   mountPath,
		CreatedAt:   nowUTC(),
	}, nil
}

// String returns a short human-readable representation of the volume.
func (v Volume) String() string {
	return fmt.Sprintf("%s (%dGB @ %s)", v.Name, v.SizeGB, v.MountPath)
}
