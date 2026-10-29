package controller

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/phaladamou/personal-cloud/packages/protocol"
	"github.com/phaladamou/personal-cloud/storage/snapshots"
	"github.com/phaladamou/personal-cloud/storage/volumes"
)

// TestFilesSurviveSnapshotRestore is the Phase 2 promise: files written
// through the file operations must come back after a snapshot restore.
// The file operations and the snapshots must share the same backing
// directory (the workspace default volume), otherwise a restore cannot
// bring the files back.
func TestFilesSurviveSnapshotRestore(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	store, err := NewFileStore(filepath.Join(dir, "ws.json"))
	if err != nil {
		t.Fatalf("NewFileStore: %v", err)
	}
	vols, err := volumes.NewLocalManager(filepath.Join(dir, "volumes"))
	if err != nil {
		t.Fatalf("NewLocalManager volumes: %v", err)
	}
	snaps, err := snapshots.NewLocalManager(filepath.Join(dir, "snapshots"), vols.DataPath)
	if err != nil {
		t.Fatalf("NewLocalManager snapshots: %v", err)
	}
	ctrl := NewController(store, &FakeBackend{}, protocol.NewEventBus(),
		WithVolumes(vols), WithSnapshots(snaps),
		WithFilesystems(VolumeFSProvider{Volumes: vols, DataPath: vols.DataPath}))
	ws := mustCreate(t, ctrl)

	const path = "notes/hello.txt"
	if err := ctrl.WriteFile(ctx, ws.ID, path, []byte("hello")); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	snap, err := ctrl.CreateSnapshot(ctx, ws.ID, "", "s1")
	if err != nil {
		t.Fatalf("CreateSnapshot: %v", err)
	}
	if err := ctrl.DeleteFile(ctx, ws.ID, path); err != nil {
		t.Fatalf("DeleteFile: %v", err)
	}
	if err := ctrl.RestoreSnapshot(ctx, ws.ID, snap.ID); err != nil {
		t.Fatalf("RestoreSnapshot: %v", err)
	}
	got, err := ctrl.ReadFile(ctx, ws.ID, path, -1)
	if err != nil {
		t.Fatalf("ReadFile after restore: %v (file was not restored)", err)
	}
	if string(got) != "hello" {
		t.Fatalf("ReadFile after restore: got %q, want %q", got, "hello")
	}
}
