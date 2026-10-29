package controller

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/phaladamou/personal-cloud/packages/types"
)

func testWorkspace(t *testing.T, id string) types.Workspace {
	t.Helper()
	ws, err := types.NewWorkspace(id, "user-1", "ws-"+id)
	if err != nil {
		t.Fatalf("NewWorkspace: %v", err)
	}
	return ws
}

func TestFileStoreCRUD(t *testing.T) {
	ctx := context.Background()
	store, err := NewFileStore(filepath.Join(t.TempDir(), "ws.json"))
	if err != nil {
		t.Fatalf("NewFileStore: %v", err)
	}

	ws := testWorkspace(t, "ws-1")
	if err := store.Create(ctx, ws); err != nil {
		t.Fatalf("Create: %v", err)
	}
	if err := store.Create(ctx, ws); !errors.Is(err, ErrWorkspaceExists) {
		t.Fatalf("Create duplicate: got %v, want ErrWorkspaceExists", err)
	}

	got, err := store.Get(ctx, "ws-1")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got.ID != ws.ID || got.Name != ws.Name || got.Status != types.WorkspaceCreated {
		t.Fatalf("Get: got %+v, want id %s", got, ws.ID)
	}
	if _, err := store.Get(ctx, "missing"); !errors.Is(err, ErrWorkspaceNotFound) {
		t.Fatalf("Get missing: got %v, want ErrWorkspaceNotFound", err)
	}

	if err := got.TransitionTo(types.WorkspaceStarting); err != nil {
		t.Fatalf("TransitionTo: %v", err)
	}
	if err := store.Update(ctx, got); err != nil {
		t.Fatalf("Update: %v", err)
	}
	if err := store.Update(ctx, testWorkspace(t, "missing")); !errors.Is(err, ErrWorkspaceNotFound) {
		t.Fatalf("Update missing: got %v, want ErrWorkspaceNotFound", err)
	}

	if err := store.Create(ctx, testWorkspace(t, "ws-2")); err != nil {
		t.Fatalf("Create ws-2: %v", err)
	}
	list, err := store.List(ctx)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(list) != 2 || list[0].ID != "ws-1" || list[1].ID != "ws-2" {
		t.Fatalf("List: got %v, want [ws-1 ws-2]", list)
	}

	if err := store.Delete(ctx, "ws-1"); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	if err := store.Delete(ctx, "ws-1"); !errors.Is(err, ErrWorkspaceNotFound) {
		t.Fatalf("Delete missing: got %v, want ErrWorkspaceNotFound", err)
	}
	if _, err := store.Get(ctx, "ws-1"); !errors.Is(err, ErrWorkspaceNotFound) {
		t.Fatalf("Get after delete: got %v, want ErrWorkspaceNotFound", err)
	}
}

func TestFileStorePersistsAcrossReopen(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "ws.json")

	store, err := NewFileStore(path)
	if err != nil {
		t.Fatalf("NewFileStore: %v", err)
	}
	ws := testWorkspace(t, "ws-1")
	if err := ws.TransitionTo(types.WorkspaceStarting); err != nil {
		t.Fatalf("TransitionTo: %v", err)
	}
	if err := store.Create(ctx, ws); err != nil {
		t.Fatalf("Create: %v", err)
	}

	reopened, err := NewFileStore(path)
	if err != nil {
		t.Fatalf("NewFileStore reopen: %v", err)
	}
	got, err := reopened.Get(ctx, "ws-1")
	if err != nil {
		t.Fatalf("Get after reopen: %v", err)
	}
	if got.Status != types.WorkspaceStarting || !got.UpdatedAt.Equal(ws.UpdatedAt) {
		t.Fatalf("Get after reopen: got %+v, want status STARTING", got)
	}
}

func TestFileStoreCorruptFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "ws.json")
	if err := os.WriteFile(path, []byte("{not json"), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	if _, err := NewFileStore(path); err == nil {
		t.Fatal("NewFileStore on corrupt file: got nil, want error")
	}
}

func TestFileStoreBadVersion(t *testing.T) {
	path := filepath.Join(t.TempDir(), "ws.json")
	if err := os.WriteFile(path, []byte(`{"version":99,"workspaces":{}}`), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	if _, err := NewFileStore(path); err == nil {
		t.Fatal("NewFileStore on bad version: got nil, want error")
	}
}

func TestFileStoreCanceledContext(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	store, err := NewFileStore(filepath.Join(t.TempDir(), "ws.json"))
	if err != nil {
		t.Fatalf("NewFileStore: %v", err)
	}
	if err := store.Create(ctx, testWorkspace(t, "ws-1")); err == nil {
		t.Fatal("Create with canceled ctx: got nil, want error")
	}
}
