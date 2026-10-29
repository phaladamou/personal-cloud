// Package volumes manages per-workspace persistent volumes.
//
// A volume is a named filesystem (see packages/types.Volume) owned by
// one workspace. The VolumeManager interface (create, attach, detach,
// delete, inspect, list) has two implementations: a directory-backed
// local manager for real use and an in-memory fake for tests.
package volumes

import (
	"context"
	"errors"

	"github.com/phaladamou/personal-cloud/packages/types"
)

// ErrVolumeNotFound is returned when no volume matches the id within
// the workspace.
var ErrVolumeNotFound = errors.New("volumes: volume not found")

// VolumeManager owns the volumes of every workspace.
//
// workspaceID scopes every operation: volume ids are only unique
// within their workspace. Implementations must be safe for concurrent
// use.
type VolumeManager interface {
	// Create provisions a new detached volume in the workspace and
	// returns it.
	Create(ctx context.Context, workspaceID, name string, sizeGB int64, mountPath string) (types.Volume, error)
	// Attach marks the volume as attached to its workspace. It is
	// idempotent.
	Attach(ctx context.Context, workspaceID, volumeID string) error
	// Detach marks the volume as detached from its workspace. It is
	// idempotent.
	Detach(ctx context.Context, workspaceID, volumeID string) error
	// Attached reports whether the volume is currently attached.
	Attached(ctx context.Context, workspaceID, volumeID string) (bool, error)
	// Delete removes the volume and its data.
	Delete(ctx context.Context, workspaceID, volumeID string) error
	// Inspect returns the volume metadata.
	Inspect(ctx context.Context, workspaceID, volumeID string) (types.Volume, error)
	// List returns every volume of the workspace, ordered by id. An
	// unknown workspace lists as empty, not as an error.
	List(ctx context.Context, workspaceID string) ([]types.Volume, error)
}
