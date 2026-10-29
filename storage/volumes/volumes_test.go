package volumes

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
)

// exerciseManager runs the shared lifecycle contract against m.
func exerciseManager(t *testing.T, m VolumeManager) {
	t.Helper()
	ctx := context.Background()

	vol, err := m.Create(ctx, "ws-1", "data", 10, "/data")
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if vol.ID == "" || vol.WorkspaceID != "ws-1" || vol.Name != "data" ||
		vol.SizeGB != 10 || vol.MountPath != "/data" {
		t.Fatalf("Create fields: got %+v", vol)
	}

	if attached, err := m.Attached(ctx, "ws-1", vol.ID); err != nil || attached {
		t.Fatalf("Attached after Create: got %v, %v, want false, nil", attached, err)
	}

	// Attach is idempotent.
	for i := 0; i < 2; i++ {
		if err := m.Attach(ctx, "ws-1", vol.ID); err != nil {
			t.Fatalf("Attach: %v", err)
		}
	}
	if attached, err := m.Attached(ctx, "ws-1", vol.ID); err != nil || !attached {
		t.Fatalf("Attached after Attach: got %v, %v, want true, nil", attached, err)
	}

	// Detach is idempotent.
	for i := 0; i < 2; i++ {
		if err := m.Detach(ctx, "ws-1", vol.ID); err != nil {
			t.Fatalf("Detach: %v", err)
		}
	}
	if attached, err := m.Attached(ctx, "ws-1", vol.ID); err != nil || attached {
		t.Fatalf("Attached after Detach: got %v, %v, want false, nil", attached, err)
	}

	got, err := m.Inspect(ctx, "ws-1", vol.ID)
	if err != nil {
		t.Fatalf("Inspect: %v", err)
	}
	if got.ID != vol.ID || got.Name != "data" {
		t.Fatalf("Inspect: got %+v, want %+v", got, vol)
	}

	vol2, err := m.Create(ctx, "ws-1", "cache", 5, "/cache")
	if err != nil {
		t.Fatalf("Create second: %v", err)
	}
	if vol2.ID == vol.ID {
		t.Fatalf("Create ids: got duplicate %q", vol.ID)
	}
	if _, err := m.Create(ctx, "ws-2", "data", 10, "/data"); err != nil {
		t.Fatalf("Create in ws-2: %v", err)
	}

	list, err := m.List(ctx, "ws-1")
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(list) != 2 || list[0].ID > list[1].ID {
		t.Fatalf("List ws-1: got %+v, want 2 volumes ordered by id", list)
	}
	list, err = m.List(ctx, "ws-2")
	if err != nil || len(list) != 1 {
		t.Fatalf("List ws-2: got %+v, %v, want 1 volume", list, err)
	}
	list, err = m.List(ctx, "ws-unknown")
	if err != nil || len(list) != 0 {
		t.Fatalf("List unknown: got %+v, %v, want empty", list, err)
	}

	if err := m.Delete(ctx, "ws-1", vol.ID); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	if _, err := m.Inspect(ctx, "ws-1", vol.ID); !errors.Is(err, ErrVolumeNotFound) {
		t.Fatalf("Inspect after Delete: got %v, want ErrVolumeNotFound", err)
	}
	if err := m.Delete(ctx, "ws-1", vol.ID); !errors.Is(err, ErrVolumeNotFound) {
		t.Fatalf("Delete twice: got %v, want ErrVolumeNotFound", err)
	}

	// Missing volume on every lookup.
	if err := m.Attach(ctx, "ws-1", "nope"); !errors.Is(err, ErrVolumeNotFound) {
		t.Fatalf("Attach missing: got %v, want ErrVolumeNotFound", err)
	}
	if err := m.Detach(ctx, "ws-1", "nope"); !errors.Is(err, ErrVolumeNotFound) {
		t.Fatalf("Detach missing: got %v, want ErrVolumeNotFound", err)
	}
	if _, err := m.Attached(ctx, "ws-1", "nope"); !errors.Is(err, ErrVolumeNotFound) {
		t.Fatalf("Attached missing: got %v, want ErrVolumeNotFound", err)
	}
	if _, err := m.Inspect(ctx, "ws-1", "nope"); !errors.Is(err, ErrVolumeNotFound) {
		t.Fatalf("Inspect missing: got %v, want ErrVolumeNotFound", err)
	}
	if err := m.Delete(ctx, "ws-1", "nope"); !errors.Is(err, ErrVolumeNotFound) {
		t.Fatalf("Delete missing: got %v, want ErrVolumeNotFound", err)
	}

	// Validation errors surface from Create.
	for name, args := range map[string][4]any{
		"empty name":     {"ws-1", "  ", int64(10), "/data"},
		"zero size":      {"ws-1", "x", int64(0), "/data"},
		"negative size":  {"ws-1", "x", int64(-1), "/data"},
		"relative mount": {"ws-1", "x", int64(10), "data"},
		"empty mount":    {"ws-1", "x", int64(10), ""},
	} {
		ws := args[0].(string)
		vname := args[1].(string)
		size := args[2].(int64)
		mount := args[3].(string)
		if _, err := m.Create(ctx, ws, vname, size, mount); err == nil {
			t.Fatalf("Create %s: got nil, want error", name)
		}
	}
	if _, err := m.Create(ctx, "../escape", "x", 10, "/data"); err == nil {
		t.Fatal("Create escaping workspace id: got nil, want error")
	}
	if _, err := m.Inspect(ctx, "ws-1", "../escape"); err == nil {
		t.Fatal("Inspect escaping volume id: got nil, want error")
	}
}

func TestLocalManager(t *testing.T) {
	m, err := NewLocalManager(t.TempDir())
	if err != nil {
		t.Fatalf("NewLocalManager: %v", err)
	}
	exerciseManager(t, m)
}

func TestLocalManagerPersistence(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()

	m, err := NewLocalManager(root)
	if err != nil {
		t.Fatalf("NewLocalManager: %v", err)
	}
	vol, err := m.Create(ctx, "ws-1", "data", 10, "/data")
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if err := m.Attach(ctx, "ws-1", vol.ID); err != nil {
		t.Fatalf("Attach: %v", err)
	}
	// Volume content lives under DataPath.
	if err := os.WriteFile(filepath.Join(m.DataPath("ws-1", vol.ID), "hello.txt"), []byte("hi"), 0o644); err != nil {
		t.Fatalf("write volume data: %v", err)
	}

	// A reopened manager sees the same metadata, attach state, and data.
	m2, err := NewLocalManager(root)
	if err != nil {
		t.Fatalf("NewLocalManager reopen: %v", err)
	}
	got, err := m2.Inspect(ctx, "ws-1", vol.ID)
	if err != nil {
		t.Fatalf("Inspect after reopen: %v", err)
	}
	if got.ID != vol.ID || got.Name != "data" {
		t.Fatalf("Inspect after reopen: got %+v, want %+v", got, vol)
	}
	if attached, err := m2.Attached(ctx, "ws-1", vol.ID); err != nil || !attached {
		t.Fatalf("Attached after reopen: got %v, %v, want true, nil", attached, err)
	}
	raw, err := os.ReadFile(filepath.Join(m2.DataPath("ws-1", vol.ID), "hello.txt"))
	if err != nil || string(raw) != "hi" {
		t.Fatalf("volume data after reopen: got %q, %v, want %q", raw, err, "hi")
	}
}

func TestLocalManagerDeleteRemovesData(t *testing.T) {
	ctx := context.Background()
	m, err := NewLocalManager(t.TempDir())
	if err != nil {
		t.Fatalf("NewLocalManager: %v", err)
	}
	vol, err := m.Create(ctx, "ws-1", "data", 10, "/data")
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if err := os.WriteFile(filepath.Join(m.DataPath("ws-1", vol.ID), "f"), []byte("x"), 0o644); err != nil {
		t.Fatalf("write volume data: %v", err)
	}
	if err := m.Delete(ctx, "ws-1", vol.ID); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	if _, err := os.Stat(filepath.Join(m.Root(), "ws-1", "volumes", vol.ID)); !os.IsNotExist(err) {
		t.Fatalf("volume dir after Delete: got %v, want not-exist", err)
	}
}

func TestNewLocalManagerErrors(t *testing.T) {
	if _, err := NewLocalManager(""); err == nil {
		t.Fatal("NewLocalManager empty root: got nil, want error")
	}
	if _, err := NewLocalManager("  "); err == nil {
		t.Fatal("NewLocalManager blank root: got nil, want error")
	}
}

func TestFakeManager(t *testing.T) {
	exerciseManager(t, &FakeManager{})
}

func TestFakeManagerZeroValue(t *testing.T) {
	var m FakeManager
	if _, err := m.Create(context.Background(), "ws-1", "data", 10, "/data"); err != nil {
		t.Fatalf("Create on zero value: %v", err)
	}
}
