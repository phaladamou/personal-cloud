package controller

import (
	"context"
	"errors"
	"fmt"

	"github.com/phaladamou/personal-cloud/packages/protocol"
	"github.com/phaladamou/personal-cloud/packages/types"
	"github.com/phaladamou/personal-cloud/storage/snapshots"
	"github.com/phaladamou/personal-cloud/storage/volumes"
)

// Errors returned when the controller has no storage manager configured.
var (
	// ErrVolumesUnavailable is returned by volume operations when the
	// controller was built without WithVolumes.
	ErrVolumesUnavailable = errors.New("controller: volume manager not configured")
	// ErrSnapshotsUnavailable is returned by snapshot operations when
	// the controller was built without WithSnapshots.
	ErrSnapshotsUnavailable = errors.New("controller: snapshot manager not configured")
)

// Default volume provisioned on first use when a snapshot does not
// name an explicit volume.
const (
	defaultVolumeName      = "default"
	defaultVolumeSizeGB    = 10
	defaultVolumeMountPath = "/data"
)

// WithVolumes configures the volume manager used by the volume and
// snapshot operations. Without it, those operations return
// ErrVolumesUnavailable.
func WithVolumes(v volumes.VolumeManager) Option {
	return func(c *Controller) { c.vols = v }
}

// WithSnapshots configures the snapshot manager used by the snapshot
// operations. Without it, those operations return
// ErrSnapshotsUnavailable.
func WithSnapshots(s snapshots.SnapshotManager) Option {
	return func(c *Controller) { c.snaps = s }
}

// CreateVolume provisions a volume in the workspace.
func (c *Controller) CreateVolume(ctx context.Context, workspaceID, name string, sizeGB int64, mountPath string) (types.Volume, error) {
	if _, err := c.store.Get(ctx, workspaceID); err != nil {
		return types.Volume{}, err
	}
	if c.vols == nil {
		return types.Volume{}, ErrVolumesUnavailable
	}
	return c.vols.Create(ctx, workspaceID, name, sizeGB, mountPath)
}

// ListVolumes returns every volume of the workspace, ordered by id.
func (c *Controller) ListVolumes(ctx context.Context, workspaceID string) ([]types.Volume, error) {
	if _, err := c.store.Get(ctx, workspaceID); err != nil {
		return nil, err
	}
	if c.vols == nil {
		return nil, ErrVolumesUnavailable
	}
	return c.vols.List(ctx, workspaceID)
}

// InspectVolume returns one volume by id.
func (c *Controller) InspectVolume(ctx context.Context, workspaceID, volumeID string) (types.Volume, error) {
	if _, err := c.store.Get(ctx, workspaceID); err != nil {
		return types.Volume{}, err
	}
	if c.vols == nil {
		return types.Volume{}, ErrVolumesUnavailable
	}
	return c.vols.Inspect(ctx, workspaceID, volumeID)
}

// DeleteVolume removes a volume and its data.
func (c *Controller) DeleteVolume(ctx context.Context, workspaceID, volumeID string) error {
	if _, err := c.store.Get(ctx, workspaceID); err != nil {
		return err
	}
	if c.vols == nil {
		return ErrVolumesUnavailable
	}
	return c.vols.Delete(ctx, workspaceID, volumeID)
}

// ensureDefaultVolume returns the id of the workspace default volume,
// provisioning it on first use.
func ensureDefaultVolume(ctx context.Context, vols volumes.VolumeManager, workspaceID string) (string, error) {
	list, err := vols.List(ctx, workspaceID)
	if err != nil {
		return "", err
	}
	for _, v := range list {
		if v.Name == defaultVolumeName {
			return v.ID, nil
		}
	}
	vol, err := vols.Create(ctx, workspaceID, defaultVolumeName, defaultVolumeSizeGB, defaultVolumeMountPath)
	if err != nil {
		return "", err
	}
	return vol.ID, nil
}

// defaultVolumeID returns the id of the workspace default volume,
// provisioning it on first use.
func (c *Controller) defaultVolumeID(ctx context.Context, workspaceID string) (string, error) {
	if c.vols == nil {
		return "", fmt.Errorf("controller: snapshot without volume id: %w", ErrVolumesUnavailable)
	}
	return ensureDefaultVolume(ctx, c.vols, workspaceID)
}

// CreateSnapshot captures the volume state and emits snapshot.created
// with the snapshot as payload. An empty volumeID snapshots the
// default volume, provisioning it on first use. Snapshots are allowed
// in any workspace status.
func (c *Controller) CreateSnapshot(ctx context.Context, workspaceID, volumeID, name string) (types.Snapshot, error) {
	ws, err := c.store.Get(ctx, workspaceID)
	if err != nil {
		return types.Snapshot{}, err
	}
	if c.snaps == nil {
		return types.Snapshot{}, ErrSnapshotsUnavailable
	}
	if volumeID == "" {
		volumeID, err = c.defaultVolumeID(ctx, workspaceID)
		if err != nil {
			return types.Snapshot{}, err
		}
	}
	snap, err := c.snaps.Create(ctx, workspaceID, volumeID, name)
	if err != nil {
		return types.Snapshot{}, err
	}
	if ev, err := protocol.NewEvent(protocol.EventSnapshotCreated, ws.ID, snap); err == nil {
		c.bus.Publish(ev)
	}
	return snap, nil
}

// ListSnapshots returns every snapshot of the workspace.
func (c *Controller) ListSnapshots(ctx context.Context, workspaceID string) ([]types.Snapshot, error) {
	if _, err := c.store.Get(ctx, workspaceID); err != nil {
		return nil, err
	}
	if c.snaps == nil {
		return nil, ErrSnapshotsUnavailable
	}
	return c.snaps.List(ctx, workspaceID)
}

// GetSnapshot returns one snapshot by id.
func (c *Controller) GetSnapshot(ctx context.Context, workspaceID, snapshotID string) (types.Snapshot, error) {
	if _, err := c.store.Get(ctx, workspaceID); err != nil {
		return types.Snapshot{}, err
	}
	if c.snaps == nil {
		return types.Snapshot{}, ErrSnapshotsUnavailable
	}
	return c.snaps.Get(ctx, workspaceID, snapshotID)
}

// RestoreSnapshot replaces the snapshot volume content with the
// captured state. Unlike Restore (the lifecycle operation), it never
// changes the workspace status.
func (c *Controller) RestoreSnapshot(ctx context.Context, workspaceID, snapshotID string) error {
	if _, err := c.store.Get(ctx, workspaceID); err != nil {
		return err
	}
	if c.snaps == nil {
		return ErrSnapshotsUnavailable
	}
	return c.snaps.Restore(ctx, workspaceID, snapshotID)
}

// DeleteSnapshot removes a snapshot. Volume data is untouched.
func (c *Controller) DeleteSnapshot(ctx context.Context, workspaceID, snapshotID string) error {
	if _, err := c.store.Get(ctx, workspaceID); err != nil {
		return err
	}
	if c.snaps == nil {
		return ErrSnapshotsUnavailable
	}
	return c.snaps.Delete(ctx, workspaceID, snapshotID)
}

// cleanupStorage removes every volume and snapshot of the workspace.
// It is best-effort: storage leftovers must never fail a workspace
// deletion.
func (c *Controller) cleanupStorage(ctx context.Context, workspaceID string) {
	if c.snaps != nil {
		if list, err := c.snaps.List(ctx, workspaceID); err == nil {
			for _, s := range list {
				_ = c.snaps.Delete(ctx, workspaceID, s.ID)
			}
		}
	}
	if c.vols != nil {
		if list, err := c.vols.List(ctx, workspaceID); err == nil {
			for _, v := range list {
				_ = c.vols.Delete(ctx, workspaceID, v.ID)
			}
		}
	}
}
