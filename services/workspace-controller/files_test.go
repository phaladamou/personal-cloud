package controller

import (
	"context"
	"errors"
	"path/filepath"
	"sync"
	"testing"

	"github.com/phaladamou/personal-cloud/packages/protocol"
	"github.com/phaladamou/personal-cloud/runtime/filesystem"
)

// memFSProvider serves one MemFS per workspace.
type memFSProvider struct {
	mu sync.Mutex
	m  map[string]*filesystem.MemFS
}

func (p *memFSProvider) FSFor(workspaceID string) (filesystem.FS, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.m == nil {
		p.m = make(map[string]*filesystem.MemFS)
	}
	f, ok := p.m[workspaceID]
	if !ok {
		var err error
		f, err = filesystem.NewMemFS(workspaceID, "vol-1")
		if err != nil {
			return nil, err
		}
		p.m[workspaceID] = f
	}
	return f, nil
}

// filesSetup wires a controller with a per-workspace MemFS provider.
func filesSetup(t *testing.T) (*Controller, *protocol.EventBus) {
	t.Helper()
	store, err := NewFileStore(filepath.Join(t.TempDir(), "ws.json"))
	if err != nil {
		t.Fatalf("NewFileStore: %v", err)
	}
	bus := protocol.NewEventBus()
	ctrl := NewController(store, &FakeBackend{}, bus, WithFilesystems(&memFSProvider{}))
	return ctrl, bus
}

func TestFileRoundTrip(t *testing.T) {
	ctx := context.Background()
	c, bus := filesSetup(t)
	var events []protocol.Event
	bus.Subscribe(protocol.EventFileChanged, func(e protocol.Event) { events = append(events, e) })
	ws := mustCreate(t, c)

	if err := c.WriteFile(ctx, ws.ID, "a/b.txt", []byte("hello")); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	data, err := c.ReadFile(ctx, ws.ID, "a/b.txt", 1<<20)
	if err != nil || string(data) != "hello" {
		t.Fatalf("ReadFile = %q, %v", data, err)
	}
	entries, err := c.ListFiles(ctx, ws.ID, "")
	if err != nil {
		t.Fatalf("ListFiles root: %v", err)
	}
	if len(entries) != 1 || entries[0].Name != "a" || !entries[0].IsDir || entries[0].Path != "a" {
		t.Fatalf("ListFiles root = %+v, want [a/]", entries)
	}
	entries, err = c.ListFiles(ctx, ws.ID, "a")
	if err != nil {
		t.Fatalf("ListFiles a: %v", err)
	}
	if len(entries) != 1 || entries[0].Path != "a/b.txt" || entries[0].Size != 5 || entries[0].IsDir {
		t.Fatalf("ListFiles a = %+v, want [a/b.txt 5B]", entries)
	}
	if err := c.Mkdir(ctx, ws.ID, "a/c"); err != nil {
		t.Fatalf("Mkdir: %v", err)
	}
	entries, err = c.ListFiles(ctx, ws.ID, "a")
	if err != nil || len(entries) != 2 {
		t.Fatalf("ListFiles a after mkdir = %+v, %v, want 2 entries", entries, err)
	}
	if entries[0].Name != "b.txt" || entries[1].Name != "c" {
		t.Fatalf("ListFiles order = %+v, want name order", entries)
	}
	if err := c.DeleteFile(ctx, ws.ID, "a/b.txt"); err != nil {
		t.Fatalf("DeleteFile: %v", err)
	}
	if _, err := c.ReadFile(ctx, ws.ID, "a/b.txt", 1<<20); !errors.Is(err, filesystem.ErrNotFound) {
		t.Fatalf("ReadFile deleted = %v, want ErrNotFound", err)
	}

	if len(events) != 3 {
		t.Fatalf("file.changed events: got %d, want 3", len(events))
	}
	want := []fileChangedPayload{
		{Path: "a/b.txt", Op: "write"},
		{Path: "a/c", Op: "mkdir"},
		{Path: "a/b.txt", Op: "delete"},
	}
	for i, e := range events {
		if e.Type != protocol.EventFileChanged || e.WorkspaceID != ws.ID {
			t.Fatalf("event %d: got %+v", i, e)
		}
		var payload fileChangedPayload
		if err := e.DecodePayload(&payload); err != nil {
			t.Fatalf("event %d payload: %v", i, err)
		}
		if payload != want[i] {
			t.Fatalf("event %d payload = %+v, want %+v", i, payload, want[i])
		}
	}
}

func TestFileErrors(t *testing.T) {
	ctx := context.Background()
	c, _ := filesSetup(t)
	ws := mustCreate(t, c)

	if err := c.WriteFile(ctx, ws.ID, "f.txt", []byte("hello")); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	// Size cap enforced without reading.
	if _, err := c.ReadFile(ctx, ws.ID, "f.txt", 4); !errors.Is(err, ErrFileTooLarge) {
		t.Fatalf("ReadFile capped = %v, want ErrFileTooLarge", err)
	}
	if _, err := c.ReadFile(ctx, ws.ID, "f.txt", -1); err != nil {
		t.Fatalf("ReadFile uncapped: %v", err)
	}
	// Missing paths.
	if _, err := c.ReadFile(ctx, ws.ID, "nope", 1<<20); !errors.Is(err, filesystem.ErrNotFound) {
		t.Fatalf("ReadFile missing = %v, want ErrNotFound", err)
	}
	if _, err := c.ListFiles(ctx, ws.ID, "nope"); !errors.Is(err, filesystem.ErrNotFound) {
		t.Fatalf("ListFiles missing = %v, want ErrNotFound", err)
	}
	if err := c.DeleteFile(ctx, ws.ID, "nope"); !errors.Is(err, filesystem.ErrNotFound) {
		t.Fatalf("DeleteFile missing = %v, want ErrNotFound", err)
	}
	// Directories are not files.
	if _, err := c.ReadFile(ctx, ws.ID, "", 1<<20); !errors.Is(err, ErrNotDirectory) {
		t.Fatalf("ReadFile root = %v, want ErrNotDirectory", err)
	}
	if _, err := c.ListFiles(ctx, ws.ID, "f.txt"); !errors.Is(err, ErrNotDirectory) {
		t.Fatalf("ListFiles file = %v, want ErrNotDirectory", err)
	}
	// The workspace root cannot be deleted.
	for _, root := range []string{"", ".", "/"} {
		if err := c.DeleteFile(ctx, ws.ID, root); !errors.Is(err, ErrInvalidPath) {
			t.Fatalf("DeleteFile %q = %v, want ErrInvalidPath", root, err)
		}
	}
	// Traversal is rejected on every operation.
	if _, err := c.ReadFile(ctx, ws.ID, "../x", 1<<20); !errors.Is(err, filesystem.ErrPathEscape) {
		t.Fatalf("ReadFile escape = %v, want ErrPathEscape", err)
	}
	if err := c.WriteFile(ctx, ws.ID, "../x", []byte("x")); !errors.Is(err, filesystem.ErrPathEscape) {
		t.Fatalf("WriteFile escape = %v, want ErrPathEscape", err)
	}
	if err := c.Mkdir(ctx, ws.ID, "a/../../x"); !errors.Is(err, filesystem.ErrPathEscape) {
		t.Fatalf("Mkdir escape = %v, want ErrPathEscape", err)
	}
	if err := c.DeleteFile(ctx, ws.ID, "../x"); !errors.Is(err, filesystem.ErrPathEscape) {
		t.Fatalf("DeleteFile escape = %v, want ErrPathEscape", err)
	}
	if _, err := c.ListFiles(ctx, ws.ID, "../x"); !errors.Is(err, filesystem.ErrPathEscape) {
		t.Fatalf("ListFiles escape = %v, want ErrPathEscape", err)
	}
}

func TestFilesOnMissingWorkspace(t *testing.T) {
	ctx := context.Background()
	c, _ := filesSetup(t)

	if _, err := c.ListFiles(ctx, "nope", ""); !errors.Is(err, ErrWorkspaceNotFound) {
		t.Fatalf("ListFiles: got %v, want ErrWorkspaceNotFound", err)
	}
	if _, err := c.ReadFile(ctx, "nope", "f", 1<<20); !errors.Is(err, ErrWorkspaceNotFound) {
		t.Fatalf("ReadFile: got %v, want ErrWorkspaceNotFound", err)
	}
	if err := c.WriteFile(ctx, "nope", "f", []byte("x")); !errors.Is(err, ErrWorkspaceNotFound) {
		t.Fatalf("WriteFile: got %v, want ErrWorkspaceNotFound", err)
	}
	if err := c.Mkdir(ctx, "nope", "d"); !errors.Is(err, ErrWorkspaceNotFound) {
		t.Fatalf("Mkdir: got %v, want ErrWorkspaceNotFound", err)
	}
	if err := c.DeleteFile(ctx, "nope", "f"); !errors.Is(err, ErrWorkspaceNotFound) {
		t.Fatalf("DeleteFile: got %v, want ErrWorkspaceNotFound", err)
	}
}

func TestFilesUnavailable(t *testing.T) {
	ctx := context.Background()
	c, _, _ := testSetup(t) // No filesystem provider.
	ws := mustCreate(t, c)

	if _, err := c.ListFiles(ctx, ws.ID, ""); !errors.Is(err, ErrFilesystemsUnavailable) {
		t.Fatalf("ListFiles: got %v, want ErrFilesystemsUnavailable", err)
	}
	if _, err := c.ReadFile(ctx, ws.ID, "f", 1<<20); !errors.Is(err, ErrFilesystemsUnavailable) {
		t.Fatalf("ReadFile: got %v, want ErrFilesystemsUnavailable", err)
	}
	if err := c.WriteFile(ctx, ws.ID, "f", []byte("x")); !errors.Is(err, ErrFilesystemsUnavailable) {
		t.Fatalf("WriteFile: got %v, want ErrFilesystemsUnavailable", err)
	}
	if err := c.Mkdir(ctx, ws.ID, "d"); !errors.Is(err, ErrFilesystemsUnavailable) {
		t.Fatalf("Mkdir: got %v, want ErrFilesystemsUnavailable", err)
	}
	if err := c.DeleteFile(ctx, ws.ID, "f"); !errors.Is(err, ErrFilesystemsUnavailable) {
		t.Fatalf("DeleteFile: got %v, want ErrFilesystemsUnavailable", err)
	}
}

func TestDirFSProvider(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	p := DirFSProvider{Root: root}

	fsys, err := p.FSFor("ws-1")
	if err != nil {
		t.Fatalf("FSFor: %v", err)
	}
	if err := fsys.WriteFile(ctx, "a.txt", []byte("hi"), 0); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	// The file landed under the workspace directory.
	data, err := fsys.ReadFile(ctx, "a.txt")
	if err != nil || string(data) != "hi" {
		t.Fatalf("ReadFile = %q, %v", data, err)
	}
	// A second lookup returns the same rooted filesystem.
	again, err := p.FSFor("ws-1")
	if err != nil {
		t.Fatalf("FSFor again: %v", err)
	}
	if data, err := again.ReadFile(ctx, "a.txt"); err != nil || string(data) != "hi" {
		t.Fatalf("ReadFile again = %q, %v", data, err)
	}
	for _, id := range []string{"", "  ", ".", "..", "a/b"} {
		if _, err := p.FSFor(id); err == nil {
			t.Fatalf("FSFor %q: want error, got nil", id)
		}
	}
	if _, err := (DirFSProvider{}).FSFor("ws-1"); err == nil {
		t.Fatal("FSFor empty root: want error, got nil")
	}
}
