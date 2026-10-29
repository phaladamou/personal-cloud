package snapshots

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/phaladamou/personal-cloud/storage/volumes"
)

// localSetup wires a volume manager and a snapshot manager over temp
// roots, with snapshots resolving volume data through the volumes.
func localSetup(t *testing.T) (*volumes.LocalManager, *LocalManager) {
	t.Helper()
	vols, err := volumes.NewLocalManager(filepath.Join(t.TempDir(), "volumes"))
	if err != nil {
		t.Fatalf("NewLocalManager volumes: %v", err)
	}
	snaps, err := NewLocalManager(filepath.Join(t.TempDir(), "snapshots"), vols.DataPath)
	if err != nil {
		t.Fatalf("NewLocalManager snapshots: %v", err)
	}
	return vols, snaps
}

func writeFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
}

func readFile(t *testing.T, path string) string {
	t.Helper()
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	return string(raw)
}

func TestLocalCreateGetList(t *testing.T) {
	ctx := context.Background()
	vols, snaps := localSetup(t)

	vol, err := vols.Create(ctx, "ws-1", "data", 10, "/data")
	if err != nil {
		t.Fatalf("Create volume: %v", err)
	}
	data := vols.DataPath("ws-1", vol.ID)
	writeFile(t, filepath.Join(data, "hello.txt"), "hello")
	writeFile(t, filepath.Join(data, "sub", "nested.txt"), "nested")

	snap, err := snaps.Create(ctx, "ws-1", vol.ID, "first")
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if snap.WorkspaceID != "ws-1" || snap.VolumeID != vol.ID || snap.Name != "first" {
		t.Fatalf("Create fields: got %+v", snap)
	}
	if snap.SizeBytes != int64(len("hello")+len("nested")) {
		t.Fatalf("Create size: got %d, want %d", snap.SizeBytes, len("hello")+len("nested"))
	}

	// Content addressing: unchanged content returns the same snapshot.
	again, err := snaps.Create(ctx, "ws-1", vol.ID, "second")
	if err != nil {
		t.Fatalf("Create unchanged: %v", err)
	}
	if again.ID != snap.ID {
		t.Fatalf("Create unchanged: got %s, want dedupe to %s", again.ID, snap.ID)
	}
	list, err := snaps.List(ctx, "ws-1")
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(list) != 1 {
		t.Fatalf("List after dedupe: got %d snapshots, want 1", len(list))
	}

	// Changed content yields a new snapshot.
	writeFile(t, filepath.Join(data, "hello.txt"), "changed")
	snap2, err := snaps.Create(ctx, "ws-1", vol.ID, "second")
	if err != nil {
		t.Fatalf("Create changed: %v", err)
	}
	if snap2.ID == snap.ID {
		t.Fatalf("Create changed: got same id %s, want new snapshot", snap.ID)
	}

	list, err = snaps.List(ctx, "ws-1")
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(list) != 2 || list[0].ID != snap.ID || list[1].ID != snap2.ID {
		t.Fatalf("List order: got %+v, want [%s %s]", list, snap.ID, snap2.ID)
	}
	list, err = snaps.List(ctx, "ws-unknown")
	if err != nil || len(list) != 0 {
		t.Fatalf("List unknown: got %+v, %v, want empty", list, err)
	}

	got, err := snaps.Get(ctx, "ws-1", snap.ID)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got.ID != snap.ID || got.Name != "first" {
		t.Fatalf("Get: got %+v, want %+v", got, snap)
	}
	if _, err := snaps.Get(ctx, "ws-1", "nope"); !errors.Is(err, ErrSnapshotNotFound) {
		t.Fatalf("Get missing: got %v, want ErrSnapshotNotFound", err)
	}
}

func TestLocalRestoreCorrectness(t *testing.T) {
	ctx := context.Background()
	vols, snaps := localSetup(t)

	vol, err := vols.Create(ctx, "ws-1", "data", 10, "/data")
	if err != nil {
		t.Fatalf("Create volume: %v", err)
	}
	data := vols.DataPath("ws-1", vol.ID)
	writeFile(t, filepath.Join(data, "keep.txt"), "original")
	writeFile(t, filepath.Join(data, "gone.txt"), "doomed")
	writeFile(t, filepath.Join(data, "sub", "file.txt"), "sub")

	snap, err := snaps.Create(ctx, "ws-1", vol.ID, "base")
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	// Mutate the live data: modify, delete, and add.
	writeFile(t, filepath.Join(data, "keep.txt"), "modified")
	if err := os.Remove(filepath.Join(data, "gone.txt")); err != nil {
		t.Fatalf("remove: %v", err)
	}
	writeFile(t, filepath.Join(data, "new.txt"), "new")

	if err := snaps.Restore(ctx, "ws-1", snap.ID); err != nil {
		t.Fatalf("Restore: %v", err)
	}
	if got := readFile(t, filepath.Join(data, "keep.txt")); got != "original" {
		t.Fatalf("keep.txt after Restore: got %q, want %q", got, "original")
	}
	if got := readFile(t, filepath.Join(data, "gone.txt")); got != "doomed" {
		t.Fatalf("gone.txt after Restore: got %q, want %q", got, "doomed")
	}
	if got := readFile(t, filepath.Join(data, "sub", "file.txt")); got != "sub" {
		t.Fatalf("sub/file.txt after Restore: got %q, want %q", got, "sub")
	}
	if _, err := os.Stat(filepath.Join(data, "new.txt")); !os.IsNotExist(err) {
		t.Fatalf("new.txt after Restore: got %v, want not-exist", err)
	}

	// Snapshots are isolated from later live writes (full copy, no
	// shared inodes): rewrite the file in place, restore again.
	writeFile(t, filepath.Join(data, "keep.txt"), "rewritten-in-place")
	if err := snaps.Restore(ctx, "ws-1", snap.ID); err != nil {
		t.Fatalf("Restore again: %v", err)
	}
	if got := readFile(t, filepath.Join(data, "keep.txt")); got != "original" {
		t.Fatalf("keep.txt after second Restore: got %q, want %q", got, "original")
	}
}

func TestLocalDelete(t *testing.T) {
	ctx := context.Background()
	vols, snaps := localSetup(t)

	vol, err := vols.Create(ctx, "ws-1", "data", 10, "/data")
	if err != nil {
		t.Fatalf("Create volume: %v", err)
	}
	writeFile(t, filepath.Join(vols.DataPath("ws-1", vol.ID), "f"), "x")
	snap, err := snaps.Create(ctx, "ws-1", vol.ID, "")
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	if err := snaps.Delete(ctx, "ws-1", snap.ID); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	if _, err := snaps.Get(ctx, "ws-1", snap.ID); !errors.Is(err, ErrSnapshotNotFound) {
		t.Fatalf("Get after Delete: got %v, want ErrSnapshotNotFound", err)
	}
	if err := snaps.Restore(ctx, "ws-1", snap.ID); !errors.Is(err, ErrSnapshotNotFound) {
		t.Fatalf("Restore after Delete: got %v, want ErrSnapshotNotFound", err)
	}
	if err := snaps.Delete(ctx, "ws-1", snap.ID); !errors.Is(err, ErrSnapshotNotFound) {
		t.Fatalf("Delete twice: got %v, want ErrSnapshotNotFound", err)
	}
	// Volume data is untouched by snapshot deletion.
	if got := readFile(t, filepath.Join(vols.DataPath("ws-1", vol.ID), "f")); got != "x" {
		t.Fatalf("volume data after snapshot Delete: got %q, want %q", got, "x")
	}
}

func TestLocalMissingVolume(t *testing.T) {
	ctx := context.Background()
	_, snaps := localSetup(t)

	if _, err := snaps.Create(ctx, "ws-1", "nope", ""); !errors.Is(err, ErrVolumeNotFound) {
		t.Fatalf("Create missing volume: got %v, want ErrVolumeNotFound", err)
	}
	if _, err := snaps.Create(ctx, "../escape", "v", ""); err == nil {
		t.Fatal("Create escaping workspace id: got nil, want error")
	}
	if err := snaps.Restore(ctx, "ws-1", "../escape"); err == nil {
		t.Fatal("Restore escaping snapshot id: got nil, want error")
	}
}

func TestLocalRestoreMissingVolume(t *testing.T) {
	ctx := context.Background()
	vols, snaps := localSetup(t)

	vol, err := vols.Create(ctx, "ws-1", "data", 10, "/data")
	if err != nil {
		t.Fatalf("Create volume: %v", err)
	}
	writeFile(t, filepath.Join(vols.DataPath("ws-1", vol.ID), "f"), "x")
	snap, err := snaps.Create(ctx, "ws-1", vol.ID, "")
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if err := vols.Delete(ctx, "ws-1", vol.ID); err != nil {
		t.Fatalf("Delete volume: %v", err)
	}
	if err := snaps.Restore(ctx, "ws-1", snap.ID); !errors.Is(err, ErrVolumeNotFound) {
		t.Fatalf("Restore deleted volume: got %v, want ErrVolumeNotFound", err)
	}
}

func TestLocalPersistence(t *testing.T) {
	ctx := context.Background()
	volRoot := filepath.Join(t.TempDir(), "volumes")
	snapRoot := filepath.Join(t.TempDir(), "snapshots")
	vols, err := volumes.NewLocalManager(volRoot)
	if err != nil {
		t.Fatalf("NewLocalManager volumes: %v", err)
	}
	snaps, err := NewLocalManager(snapRoot, vols.DataPath)
	if err != nil {
		t.Fatalf("NewLocalManager snapshots: %v", err)
	}
	vol, err := vols.Create(ctx, "ws-1", "data", 10, "/data")
	if err != nil {
		t.Fatalf("Create volume: %v", err)
	}
	writeFile(t, filepath.Join(vols.DataPath("ws-1", vol.ID), "f"), "x")
	snap, err := snaps.Create(ctx, "ws-1", vol.ID, "persisted")
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	reopened, err := NewLocalManager(snapRoot, vols.DataPath)
	if err != nil {
		t.Fatalf("NewLocalManager reopen: %v", err)
	}
	list, err := reopened.List(ctx, "ws-1")
	if err != nil {
		t.Fatalf("List after reopen: %v", err)
	}
	if len(list) != 1 || list[0].ID != snap.ID || list[0].Name != "persisted" {
		t.Fatalf("List after reopen: got %+v, want [%+v]", list, snap)
	}
	writeFile(t, filepath.Join(vols.DataPath("ws-1", vol.ID), "f"), "changed")
	if err := reopened.Restore(ctx, "ws-1", snap.ID); err != nil {
		t.Fatalf("Restore after reopen: %v", err)
	}
	if got := readFile(t, filepath.Join(vols.DataPath("ws-1", vol.ID), "f")); got != "x" {
		t.Fatalf("file after Restore: got %q, want %q", got, "x")
	}
}

func TestNewLocalManagerErrors(t *testing.T) {
	vols, err := volumes.NewLocalManager(t.TempDir())
	if err != nil {
		t.Fatalf("NewLocalManager volumes: %v", err)
	}
	if _, err := NewLocalManager("", vols.DataPath); err == nil {
		t.Fatal("NewLocalManager empty root: got nil, want error")
	}
	if _, err := NewLocalManager(t.TempDir(), nil); err == nil {
		t.Fatal("NewLocalManager nil resolver: got nil, want error")
	}
}

func TestFakeManager(t *testing.T) {
	ctx := context.Background()
	var m FakeManager

	snap, err := m.Create(ctx, "ws-1", "vol-1", "first")
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if snap.WorkspaceID != "ws-1" || snap.VolumeID != "vol-1" || snap.Name != "first" {
		t.Fatalf("Create fields: got %+v", snap)
	}
	snap2, err := m.Create(ctx, "ws-1", "vol-1", "second")
	if err != nil {
		t.Fatalf("Create second: %v", err)
	}
	if snap2.ID == snap.ID {
		t.Fatalf("Create ids: got duplicate %q", snap.ID)
	}

	list, err := m.List(ctx, "ws-1")
	if err != nil || len(list) != 2 {
		t.Fatalf("List: got %+v, %v, want 2 snapshots", list, err)
	}
	list, err = m.List(ctx, "ws-unknown")
	if err != nil || len(list) != 0 {
		t.Fatalf("List unknown: got %+v, %v, want empty", list, err)
	}

	got, err := m.Get(ctx, "ws-1", snap.ID)
	if err != nil || got.ID != snap.ID {
		t.Fatalf("Get: got %+v, %v", got, err)
	}
	if err := m.Restore(ctx, "ws-1", snap.ID); err != nil {
		t.Fatalf("Restore: %v", err)
	}
	if err := m.Delete(ctx, "ws-1", snap.ID); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	if _, err := m.Get(ctx, "ws-1", snap.ID); !errors.Is(err, ErrSnapshotNotFound) {
		t.Fatalf("Get after Delete: got %v, want ErrSnapshotNotFound", err)
	}
	if err := m.Restore(ctx, "ws-1", snap.ID); !errors.Is(err, ErrSnapshotNotFound) {
		t.Fatalf("Restore after Delete: got %v, want ErrSnapshotNotFound", err)
	}
	if err := m.Delete(ctx, "ws-1", snap.ID); !errors.Is(err, ErrSnapshotNotFound) {
		t.Fatalf("Delete twice: got %v, want ErrSnapshotNotFound", err)
	}
	if _, err := m.Create(ctx, "", "vol-1", ""); err == nil {
		t.Fatal("Create empty workspace: got nil, want error")
	}
}
