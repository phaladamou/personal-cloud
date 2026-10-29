// Package snapshots captures and restores workspace filesystem state.
//
// A snapshot is a point-in-time copy of one volume (see
// packages/types.Snapshot). The SnapshotManager interface (create,
// list, get, delete, restore) has two implementations: a local
// content-addressed manager for real use and an in-memory fake for
// tests.
package snapshots

import (
	"context"
	"errors"

	"github.com/phaladamou/personal-cloud/packages/types"
)

// Errors returned by SnapshotManager implementations.
var (
	// ErrSnapshotNotFound is returned when no snapshot matches the id
	// within the workspace.
	ErrSnapshotNotFound = errors.New("snapshots: snapshot not found")
	// ErrVolumeNotFound is returned when the snapshotted volume has no
	// data directory.
	ErrVolumeNotFound = errors.New("snapshots: volume not found")
)

// SnapshotManager owns the snapshots of every workspace.
//
// workspaceID scopes every operation: snapshot ids are only unique
// within their workspace. Restore targets the volume recorded in the
// snapshot metadata. Implementations must be safe for concurrent use.
type SnapshotManager interface {
	// Create captures the current state of the volume and returns its
	// metadata. Content-addressed managers return the existing
	// snapshot when the volume content is unchanged.
	Create(ctx context.Context, workspaceID, volumeID, name string) (types.Snapshot, error)
	// List returns every snapshot of the workspace, ordered by creation
	// time then id. An unknown workspace lists as empty, not as an
	// error.
	List(ctx context.Context, workspaceID string) ([]types.Snapshot, error)
	// Get returns one snapshot by id.
	Get(ctx context.Context, workspaceID, snapshotID string) (types.Snapshot, error)
	// Delete removes a snapshot. Volume data is untouched.
	Delete(ctx context.Context, workspaceID, snapshotID string) error
	// Restore replaces the snapshot volume content with the captured
	// state.
	Restore(ctx context.Context, workspaceID, snapshotID string) error
}
