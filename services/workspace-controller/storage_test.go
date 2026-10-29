package controller

import (
	"context"
	"errors"
	"path/filepath"
	"testing"

	"github.com/phaladamou/personal-cloud/packages/protocol"
	"github.com/phaladamou/personal-cloud/packages/types"
	"github.com/phaladamou/personal-cloud/storage/snapshots"
	"github.com/phaladamou/personal-cloud/storage/volumes"
)

// storageSetup wires a controller with fake storage managers.
func storageSetup(t *testing.T) (*Controller, *volumes.FakeManager, *snapshots.FakeManager, *protocol.EventBus) {
	t.Helper()
	store, err := NewFileStore(filepath.Join(t.TempDir(), "ws.json"))
	if err != nil {
		t.Fatalf("NewFileStore: %v", err)
	}
	vols := &volumes.FakeManager{}
	snaps := &snapshots.FakeManager{}
	bus := protocol.NewEventBus()
	ctrl := NewController(store, &FakeBackend{}, bus, WithVolumes(vols), WithSnapshots(snaps))
	return ctrl, vols, snaps, bus
}

func TestVolumeRoundTrip(t *testing.T) {
	ctx := context.Background()
	c, _, _, _ := storageSetup(t)
	ws := mustCreate(t, c)

	vol, err := c.CreateVolume(ctx, ws.ID, "data", 10, "/data")
	if err != nil {
		t.Fatalf("CreateVolume: %v", err)
	}
	if vol.WorkspaceID != ws.ID || vol.Name != "data" {
		t.Fatalf("CreateVolume fields: got %+v", vol)
	}

	list, err := c.ListVolumes(ctx, ws.ID)
	if err != nil || len(list) != 1 || list[0].ID != vol.ID {
		t.Fatalf("ListVolumes: got %+v, %v", list, err)
	}
	got, err := c.InspectVolume(ctx, ws.ID, vol.ID)
	if err != nil || got.ID != vol.ID {
		t.Fatalf("InspectVolume: got %+v, %v", got, err)
	}
	if err := c.DeleteVolume(ctx, ws.ID, vol.ID); err != nil {
		t.Fatalf("DeleteVolume: %v", err)
	}
	list, err = c.ListVolumes(ctx, ws.ID)
	if err != nil || len(list) != 0 {
		t.Fatalf("ListVolumes after Delete: got %+v, %v", list, err)
	}
	if err := c.DeleteVolume(ctx, ws.ID, vol.ID); !errors.Is(err, volumes.ErrVolumeNotFound) {
		t.Fatalf("DeleteVolume twice: got %v, want ErrVolumeNotFound", err)
	}
}

func TestCreateSnapshotDefaultVolume(t *testing.T) {
	ctx := context.Background()
	c, _, _, bus := storageSetup(t)
	var events []protocol.Event
	bus.Subscribe(protocol.EventSnapshotCreated, func(e protocol.Event) { events = append(events, e) })
	ws := mustCreate(t, c)

	snap, err := c.CreateSnapshot(ctx, ws.ID, "", "first")
	if err != nil {
		t.Fatalf("CreateSnapshot: %v", err)
	}
	if snap.WorkspaceID != ws.ID || snap.Name != "first" || snap.VolumeID == "" {
		t.Fatalf("CreateSnapshot fields: got %+v", snap)
	}

	// The default volume was provisioned on first use.
	list, err := c.ListVolumes(ctx, ws.ID)
	if err != nil {
		t.Fatalf("ListVolumes: %v", err)
	}
	if len(list) != 1 || list[0].Name != "default" || list[0].ID != snap.VolumeID {
		t.Fatalf("default volume: got %+v", list)
	}

	// A second snapshot reuses the same default volume.
	snap2, err := c.CreateSnapshot(ctx, ws.ID, "", "second")
	if err != nil {
		t.Fatalf("CreateSnapshot again: %v", err)
	}
	if snap2.VolumeID != snap.VolumeID {
		t.Fatalf("second snapshot volume: got %s, want %s", snap2.VolumeID, snap.VolumeID)
	}
	list, err = c.ListVolumes(ctx, ws.ID)
	if err != nil || len(list) != 1 {
		t.Fatalf("ListVolumes: got %+v, %v, want 1 volume", list, err)
	}

	// Each snapshot emitted snapshot.created with the snapshot payload.
	if len(events) != 2 {
		t.Fatalf("snapshot.created events: got %d, want 2", len(events))
	}
	for i, e := range events {
		if e.Type != protocol.EventSnapshotCreated || e.WorkspaceID != ws.ID {
			t.Fatalf("event %d: got %+v", i, e)
		}
		var payload types.Snapshot
		if err := e.DecodePayload(&payload); err != nil {
			t.Fatalf("event %d payload: %v", i, err)
		}
		if payload.ID == "" || payload.WorkspaceID != ws.ID {
			t.Fatalf("event %d payload: got %+v", i, payload)
		}
	}
}

func TestCreateSnapshotExplicitVolume(t *testing.T) {
	ctx := context.Background()
	c, _, _, _ := storageSetup(t)
	ws := mustCreate(t, c)

	vol, err := c.CreateVolume(ctx, ws.ID, "data", 10, "/data")
	if err != nil {
		t.Fatalf("CreateVolume: %v", err)
	}
	snap, err := c.CreateSnapshot(ctx, ws.ID, vol.ID, "")
	if err != nil {
		t.Fatalf("CreateSnapshot: %v", err)
	}
	if snap.VolumeID != vol.ID {
		t.Fatalf("snapshot volume: got %s, want %s", snap.VolumeID, vol.ID)
	}
}

func TestSnapshotRoundTrip(t *testing.T) {
	ctx := context.Background()
	c, _, _, _ := storageSetup(t)
	ws := mustCreate(t, c)

	snap, err := c.CreateSnapshot(ctx, ws.ID, "", "base")
	if err != nil {
		t.Fatalf("CreateSnapshot: %v", err)
	}
	got, err := c.GetSnapshot(ctx, ws.ID, snap.ID)
	if err != nil || got.ID != snap.ID {
		t.Fatalf("GetSnapshot: got %+v, %v", got, err)
	}
	list, err := c.ListSnapshots(ctx, ws.ID)
	if err != nil || len(list) != 1 || list[0].ID != snap.ID {
		t.Fatalf("ListSnapshots: got %+v, %v", list, err)
	}
	if err := c.RestoreSnapshot(ctx, ws.ID, snap.ID); err != nil {
		t.Fatalf("RestoreSnapshot: %v", err)
	}
	if err := c.DeleteSnapshot(ctx, ws.ID, snap.ID); err != nil {
		t.Fatalf("DeleteSnapshot: %v", err)
	}
	if _, err := c.GetSnapshot(ctx, ws.ID, snap.ID); !errors.Is(err, snapshots.ErrSnapshotNotFound) {
		t.Fatalf("GetSnapshot after Delete: got %v, want ErrSnapshotNotFound", err)
	}
	if err := c.RestoreSnapshot(ctx, ws.ID, snap.ID); !errors.Is(err, snapshots.ErrSnapshotNotFound) {
		t.Fatalf("RestoreSnapshot after Delete: got %v, want ErrSnapshotNotFound", err)
	}
	if err := c.DeleteSnapshot(ctx, ws.ID, snap.ID); !errors.Is(err, snapshots.ErrSnapshotNotFound) {
		t.Fatalf("DeleteSnapshot twice: got %v, want ErrSnapshotNotFound", err)
	}
}

func TestSnapshotInAnyStatus(t *testing.T) {
	ctx := context.Background()
	c, _, _, _ := storageSetup(t)
	ws := mustCreate(t, c)

	if _, err := c.CreateSnapshot(ctx, ws.ID, "", "created"); err != nil {
		t.Fatalf("CreateSnapshot in CREATED: %v", err)
	}
	if _, err := c.Start(ctx, ws.ID); err != nil {
		t.Fatalf("Start: %v", err)
	}
	if _, err := c.CreateSnapshot(ctx, ws.ID, "", "running"); err != nil {
		t.Fatalf("CreateSnapshot in RUNNING: %v", err)
	}
	if _, err := c.Stop(ctx, ws.ID); err != nil {
		t.Fatalf("Stop: %v", err)
	}
	if _, err := c.CreateSnapshot(ctx, ws.ID, "", "stopped"); err != nil {
		t.Fatalf("CreateSnapshot in STOPPED: %v", err)
	}
	list, err := c.ListSnapshots(ctx, ws.ID)
	if err != nil || len(list) != 3 {
		t.Fatalf("ListSnapshots: got %+v, %v, want 3", list, err)
	}
}

func TestStorageOnMissingWorkspace(t *testing.T) {
	ctx := context.Background()
	c, _, _, _ := storageSetup(t)

	if _, err := c.CreateVolume(ctx, "nope", "d", 10, "/data"); !errors.Is(err, ErrWorkspaceNotFound) {
		t.Fatalf("CreateVolume: got %v, want ErrWorkspaceNotFound", err)
	}
	if _, err := c.ListVolumes(ctx, "nope"); !errors.Is(err, ErrWorkspaceNotFound) {
		t.Fatalf("ListVolumes: got %v, want ErrWorkspaceNotFound", err)
	}
	if _, err := c.InspectVolume(ctx, "nope", "v"); !errors.Is(err, ErrWorkspaceNotFound) {
		t.Fatalf("InspectVolume: got %v, want ErrWorkspaceNotFound", err)
	}
	if err := c.DeleteVolume(ctx, "nope", "v"); !errors.Is(err, ErrWorkspaceNotFound) {
		t.Fatalf("DeleteVolume: got %v, want ErrWorkspaceNotFound", err)
	}
	if _, err := c.CreateSnapshot(ctx, "nope", "", ""); !errors.Is(err, ErrWorkspaceNotFound) {
		t.Fatalf("CreateSnapshot: got %v, want ErrWorkspaceNotFound", err)
	}
	if _, err := c.ListSnapshots(ctx, "nope"); !errors.Is(err, ErrWorkspaceNotFound) {
		t.Fatalf("ListSnapshots: got %v, want ErrWorkspaceNotFound", err)
	}
	if _, err := c.GetSnapshot(ctx, "nope", "s"); !errors.Is(err, ErrWorkspaceNotFound) {
		t.Fatalf("GetSnapshot: got %v, want ErrWorkspaceNotFound", err)
	}
	if err := c.RestoreSnapshot(ctx, "nope", "s"); !errors.Is(err, ErrWorkspaceNotFound) {
		t.Fatalf("RestoreSnapshot: got %v, want ErrWorkspaceNotFound", err)
	}
	if err := c.DeleteSnapshot(ctx, "nope", "s"); !errors.Is(err, ErrWorkspaceNotFound) {
		t.Fatalf("DeleteSnapshot: got %v, want ErrWorkspaceNotFound", err)
	}
}

func TestStorageUnavailable(t *testing.T) {
	ctx := context.Background()
	c, _, _ := testSetup(t) // No storage managers.
	ws := mustCreate(t, c)

	if _, err := c.CreateVolume(ctx, ws.ID, "d", 10, "/data"); !errors.Is(err, ErrVolumesUnavailable) {
		t.Fatalf("CreateVolume: got %v, want ErrVolumesUnavailable", err)
	}
	if _, err := c.ListVolumes(ctx, ws.ID); !errors.Is(err, ErrVolumesUnavailable) {
		t.Fatalf("ListVolumes: got %v, want ErrVolumesUnavailable", err)
	}
	if _, err := c.CreateSnapshot(ctx, ws.ID, "vol-1", ""); !errors.Is(err, ErrSnapshotsUnavailable) {
		t.Fatalf("CreateSnapshot: got %v, want ErrSnapshotsUnavailable", err)
	}
	if _, err := c.ListSnapshots(ctx, ws.ID); !errors.Is(err, ErrSnapshotsUnavailable) {
		t.Fatalf("ListSnapshots: got %v, want ErrSnapshotsUnavailable", err)
	}
	if _, err := c.GetSnapshot(ctx, ws.ID, "s"); !errors.Is(err, ErrSnapshotsUnavailable) {
		t.Fatalf("GetSnapshot: got %v, want ErrSnapshotsUnavailable", err)
	}
	if err := c.RestoreSnapshot(ctx, ws.ID, "s"); !errors.Is(err, ErrSnapshotsUnavailable) {
		t.Fatalf("RestoreSnapshot: got %v, want ErrSnapshotsUnavailable", err)
	}
	if err := c.DeleteSnapshot(ctx, ws.ID, "s"); !errors.Is(err, ErrSnapshotsUnavailable) {
		t.Fatalf("DeleteSnapshot: got %v, want ErrSnapshotsUnavailable", err)
	}
}

func TestCreateSnapshotWithoutVolumesNeedsVolumeID(t *testing.T) {
	ctx := context.Background()
	store, err := NewFileStore(filepath.Join(t.TempDir(), "ws.json"))
	if err != nil {
		t.Fatalf("NewFileStore: %v", err)
	}
	// Snapshots but no volumes: only explicit volume ids work.
	c := NewController(store, &FakeBackend{}, protocol.NewEventBus(), WithSnapshots(&snapshots.FakeManager{}))
	ws, err := c.Create(ctx, "user-1", "dev")
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if _, err := c.CreateSnapshot(ctx, ws.ID, "", ""); !errors.Is(err, ErrVolumesUnavailable) {
		t.Fatalf("CreateSnapshot default volume: got %v, want ErrVolumesUnavailable", err)
	}
	if _, err := c.CreateSnapshot(ctx, ws.ID, "vol-1", ""); err != nil {
		t.Fatalf("CreateSnapshot explicit volume: %v", err)
	}
}

func TestDeleteCleansStorage(t *testing.T) {
	ctx := context.Background()
	c, vols, snaps, _ := storageSetup(t)
	ws := mustCreate(t, c)

	if _, err := c.CreateVolume(ctx, ws.ID, "data", 10, "/data"); err != nil {
		t.Fatalf("CreateVolume: %v", err)
	}
	if _, err := c.CreateSnapshot(ctx, ws.ID, "", "base"); err != nil {
		t.Fatalf("CreateSnapshot: %v", err)
	}
	other := mustCreate(t, c)
	if _, err := c.CreateSnapshot(ctx, other.ID, "", "other"); err != nil {
		t.Fatalf("CreateSnapshot other: %v", err)
	}

	if err := c.Delete(ctx, ws.ID); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	if list, _ := vols.List(ctx, ws.ID); len(list) != 0 {
		t.Fatalf("volumes after Delete: got %+v, want empty", list)
	}
	if list, _ := snaps.List(ctx, ws.ID); len(list) != 0 {
		t.Fatalf("snapshots after Delete: got %+v, want empty", list)
	}
	// The other workspace storage is untouched.
	if list, _ := snaps.List(ctx, other.ID); len(list) != 1 {
		t.Fatalf("other snapshots after Delete: got %+v, want 1", list)
	}
}
