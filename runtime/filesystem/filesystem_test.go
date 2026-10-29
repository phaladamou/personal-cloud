package filesystem

import (
	"context"
	"errors"
	"testing"
	"time"
)

func newTestFS(t *testing.T) *LocalFS {
	t.Helper()
	lfs, err := NewLocalFS(t.TempDir(), "ws-1", "vol-1")
	if err != nil {
		t.Fatalf("NewLocalFS: %v", err)
	}
	lfs.PollInterval = 10 * time.Millisecond
	return lfs
}

func TestLocalReadWriteRoundTrip(t *testing.T) {
	lfs := newTestFS(t)
	ctx := context.Background()

	if err := lfs.WriteFile(ctx, "sub/dir/a.txt", []byte("hello"), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	data, err := lfs.ReadFile(ctx, "sub/dir/a.txt")
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	if string(data) != "hello" {
		t.Fatalf("data = %q, want %q", data, "hello")
	}
	if _, err := lfs.ReadFile(ctx, "missing.txt"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("ReadFile missing = %v, want ErrNotFound", err)
	}
}

func TestLocalMoveDelete(t *testing.T) {
	lfs := newTestFS(t)
	ctx := context.Background()

	if err := lfs.WriteFile(ctx, "a.txt", []byte("x"), 0); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	if err := lfs.Move(ctx, "a.txt", "nested/b.txt"); err != nil {
		t.Fatalf("Move: %v", err)
	}
	if _, err := lfs.ReadFile(ctx, "a.txt"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("ReadFile old path = %v, want ErrNotFound", err)
	}
	data, err := lfs.ReadFile(ctx, "nested/b.txt")
	if err != nil || string(data) != "x" {
		t.Fatalf("ReadFile new path = %q, %v", data, err)
	}
	if err := lfs.Move(ctx, "nope.txt", "b.txt"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("Move missing = %v, want ErrNotFound", err)
	}
	if err := lfs.Delete(ctx, "nested"); err != nil {
		t.Fatalf("Delete dir: %v", err)
	}
	if _, err := lfs.Stat(ctx, "nested/b.txt"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("Stat deleted = %v, want ErrNotFound", err)
	}
	if err := lfs.Delete(ctx, "nope.txt"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("Delete missing = %v, want ErrNotFound", err)
	}
}

func TestLocalStatList(t *testing.T) {
	lfs := newTestFS(t)
	ctx := context.Background()

	if err := lfs.WriteFile(ctx, "a.txt", []byte("12345"), 0); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	st, err := lfs.Stat(ctx, "a.txt")
	if err != nil {
		t.Fatalf("Stat: %v", err)
	}
	if st.Size() != 5 || st.IsDir() {
		t.Fatalf("Stat = size %d dir %v, want size 5 dir false", st.Size(), st.IsDir())
	}
	entries, err := lfs.List(ctx, ".")
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(entries) != 1 || entries[0].Name() != "a.txt" {
		t.Fatalf("List = %v, want [a.txt]", entries)
	}
	if _, err := lfs.List(ctx, "missing"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("List missing = %v, want ErrNotFound", err)
	}
}

func TestLocalPathEscape(t *testing.T) {
	lfs := newTestFS(t)
	ctx := context.Background()

	for _, p := range []string{"../evil.txt", "sub/../../evil.txt", ".."} {
		if err := lfs.WriteFile(ctx, p, []byte("x"), 0); !errors.Is(err, ErrPathEscape) {
			t.Fatalf("WriteFile %q = %v, want ErrPathEscape", p, err)
		}
		if _, err := lfs.ReadFile(ctx, p); !errors.Is(err, ErrPathEscape) {
			t.Fatalf("ReadFile %q = %v, want ErrPathEscape", p, err)
		}
	}
	if _, err := NewLocalFS("", "ws", "vol"); err == nil {
		t.Fatal("NewLocalFS empty root: want error, got nil")
	}
	if _, err := NewLocalFS(t.TempDir(), "", "vol"); err == nil {
		t.Fatal("NewLocalFS empty workspace: want error, got nil")
	}
}

func nextEvent(t *testing.T, ch <-chan Event) Event {
	t.Helper()
	select {
	case ev, ok := <-ch:
		if !ok {
			t.Fatal("watch channel closed, want event")
		}
		return ev
	case <-time.After(5 * time.Second):
		t.Fatal("timed out waiting for watch event")
		return Event{}
	}
}

func TestLocalWatchFileModify(t *testing.T) {
	lfs := newTestFS(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	if err := lfs.WriteFile(ctx, "w.txt", []byte("v1"), 0); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	ch, err := lfs.Watch(ctx, "w.txt")
	if err != nil {
		t.Fatalf("Watch: %v", err)
	}
	if err := lfs.WriteFile(ctx, "w.txt", []byte("v2-longer"), 0); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	ev := nextEvent(t, ch)
	if ev.Type != EventModify || ev.Path != "w.txt" {
		t.Fatalf("event = %+v, want MODIFY w.txt", ev)
	}
	if err := lfs.Delete(ctx, "w.txt"); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	ev = nextEvent(t, ch)
	if ev.Type != EventDelete {
		t.Fatalf("event = %+v, want DELETE", ev)
	}
}

func TestLocalWatchDirCreate(t *testing.T) {
	lfs := newTestFS(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	ch, err := lfs.Watch(ctx, ".")
	if err != nil {
		t.Fatalf("Watch: %v", err)
	}
	if err := lfs.WriteFile(ctx, "new.txt", []byte("x"), 0); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	ev := nextEvent(t, ch)
	if ev.Type != EventModify {
		t.Fatalf("event = %+v, want MODIFY on watched dir", ev)
	}
}

func TestLocalWatchCancelCloses(t *testing.T) {
	lfs := newTestFS(t)
	ctx, cancel := context.WithCancel(context.Background())

	ch, err := lfs.Watch(ctx, ".")
	if err != nil {
		t.Fatalf("Watch: %v", err)
	}
	cancel()
	select {
	case _, ok := <-ch:
		if ok {
			// A pending event may arrive first; the next read must close.
			if _, ok := <-ch; ok {
				t.Fatal("want channel closed after cancel")
			}
		}
	case <-time.After(5 * time.Second):
		t.Fatal("timed out waiting for watch close")
	}
}

func TestLocalSnapshotRestore(t *testing.T) {
	lfs := newTestFS(t)
	ctx := context.Background()

	if err := lfs.WriteFile(ctx, "a.txt", []byte("v1"), 0); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	if err := lfs.WriteFile(ctx, "sub/b.txt", []byte("b"), 0); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	snap, err := lfs.Snapshot(ctx, "s1")
	if err != nil {
		t.Fatalf("Snapshot: %v", err)
	}
	if snap.Name != "s1" || snap.WorkspaceID != "ws-1" || snap.VolumeID != "vol-1" {
		t.Fatalf("snapshot = %+v, want name s1 ws-1 vol-1", snap)
	}
	if snap.SizeBytes <= 0 {
		t.Fatalf("snapshot size = %d, want positive", snap.SizeBytes)
	}
	// Mutate the workspace, then restore.
	if err := lfs.WriteFile(ctx, "a.txt", []byte("v2"), 0); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	if err := lfs.Delete(ctx, "sub"); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	if err := lfs.Restore(ctx, snap.ID); err != nil {
		t.Fatalf("Restore: %v", err)
	}
	data, err := lfs.ReadFile(ctx, "a.txt")
	if err != nil || string(data) != "v1" {
		t.Fatalf("restored a.txt = %q, %v", data, err)
	}
	data, err = lfs.ReadFile(ctx, "sub/b.txt")
	if err != nil || string(data) != "b" {
		t.Fatalf("restored sub/b.txt = %q, %v", data, err)
	}
	if err := lfs.Restore(ctx, "nope"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("Restore unknown = %v, want ErrNotFound", err)
	}
}

func TestMemFSRoundTrip(t *testing.T) {
	mfs, err := NewMemFS("ws-1", "vol-1")
	if err != nil {
		t.Fatalf("NewMemFS: %v", err)
	}
	ctx := context.Background()

	if err := mfs.WriteFile(ctx, "a.txt", []byte("hi"), 0); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	data, err := mfs.ReadFile(ctx, "a.txt")
	if err != nil || string(data) != "hi" {
		t.Fatalf("ReadFile = %q, %v", data, err)
	}
	if err := mfs.Move(ctx, "a.txt", "b.txt"); err != nil {
		t.Fatalf("Move: %v", err)
	}
	if _, err := mfs.ReadFile(ctx, "a.txt"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("ReadFile old = %v, want ErrNotFound", err)
	}
	entries, err := mfs.List(ctx, "")
	if err != nil || len(entries) != 1 {
		t.Fatalf("List = %v, %v", entries, err)
	}
	st, err := mfs.Stat(ctx, "b.txt")
	if err != nil || st.Size() != 2 {
		t.Fatalf("Stat = %v, %v", st, err)
	}
	if _, err := mfs.ReadFile(ctx, "../x"); !errors.Is(err, ErrPathEscape) {
		t.Fatalf("ReadFile escape = %v, want ErrPathEscape", err)
	}
}

func TestMemFSWatchSnapshot(t *testing.T) {
	mfs, err := NewMemFS("ws-1", "vol-1")
	if err != nil {
		t.Fatalf("NewMemFS: %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	ch, err := mfs.Watch(ctx, "w.txt")
	if err != nil {
		t.Fatalf("Watch: %v", err)
	}
	if err := mfs.WriteFile(ctx, "w.txt", []byte("1"), 0); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	if ev := nextEvent(t, ch); ev.Type != EventCreate {
		t.Fatalf("event = %+v, want CREATE", ev)
	}
	snap, err := mfs.Snapshot(ctx, "m1")
	if err != nil {
		t.Fatalf("Snapshot: %v", err)
	}
	if err := mfs.WriteFile(ctx, "w.txt", []byte("2"), 0); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	if err := mfs.Restore(ctx, snap.ID); err != nil {
		t.Fatalf("Restore: %v", err)
	}
	data, err := mfs.ReadFile(ctx, "w.txt")
	if err != nil || string(data) != "1" {
		t.Fatalf("restored w.txt = %q, %v", data, err)
	}
	if err := mfs.Restore(ctx, "nope"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("Restore unknown = %v, want ErrNotFound", err)
	}
}

func TestMkdirBothBackends(t *testing.T) {
	backends := map[string]FS{}
	backends["local"] = newTestFS(t)
	mfs, err := NewMemFS("ws-1", "vol-1")
	if err != nil {
		t.Fatalf("NewMemFS: %v", err)
	}
	backends["mem"] = mfs

	for name, fsys := range backends {
		t.Run(name, func(t *testing.T) {
			ctx := context.Background()
			if err := fsys.Mkdir(ctx, "a/b"); err != nil {
				t.Fatalf("Mkdir: %v", err)
			}
			// Idempotent, and root is a no-op.
			if err := fsys.Mkdir(ctx, "a/b"); err != nil {
				t.Fatalf("Mkdir again: %v", err)
			}
			if err := fsys.Mkdir(ctx, ""); err != nil {
				t.Fatalf("Mkdir root: %v", err)
			}
			st, err := fsys.Stat(ctx, "a/b")
			if err != nil || !st.IsDir() {
				t.Fatalf("Stat = %v, %v, want dir", st, err)
			}
			entries, err := fsys.List(ctx, "a/b")
			if err != nil || len(entries) != 0 {
				t.Fatalf("List empty dir = %v, %v, want empty", entries, err)
			}
			entries, err = fsys.List(ctx, "")
			if err != nil {
				t.Fatalf("List root: %v", err)
			}
			if len(entries) != 1 || entries[0].Name() != "a" || !entries[0].IsDir() {
				t.Fatalf("List root = %v, want [a/]", entries)
			}
			// A file blocks a directory of the same name.
			if err := fsys.WriteFile(ctx, "f.txt", []byte("x"), 0); err != nil {
				t.Fatalf("WriteFile: %v", err)
			}
			if err := fsys.Mkdir(ctx, "f.txt"); err == nil {
				t.Fatal("Mkdir over file: want error, got nil")
			}
			// Traversal is rejected.
			if err := fsys.Mkdir(ctx, "../evil"); !errors.Is(err, ErrPathEscape) {
				t.Fatalf("Mkdir escape = %v, want ErrPathEscape", err)
			}
			if err := fsys.Delete(ctx, "a"); err != nil {
				t.Fatalf("Delete dir: %v", err)
			}
			if _, err := fsys.Stat(ctx, "a/b"); !errors.Is(err, ErrNotFound) {
				t.Fatalf("Stat deleted = %v, want ErrNotFound", err)
			}
		})
	}
}

func TestMemFSMkdirSnapshot(t *testing.T) {
	mfs, err := NewMemFS("ws-1", "vol-1")
	if err != nil {
		t.Fatalf("NewMemFS: %v", err)
	}
	ctx := context.Background()

	if err := mfs.Mkdir(ctx, "keep"); err != nil {
		t.Fatalf("Mkdir: %v", err)
	}
	snap, err := mfs.Snapshot(ctx, "with-dir")
	if err != nil {
		t.Fatalf("Snapshot: %v", err)
	}
	if err := mfs.Delete(ctx, "keep"); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	if err := mfs.Restore(ctx, snap.ID); err != nil {
		t.Fatalf("Restore: %v", err)
	}
	st, err := mfs.Stat(ctx, "keep")
	if err != nil || !st.IsDir() {
		t.Fatalf("Stat restored dir = %v, %v, want dir", st, err)
	}
	if err := mfs.WriteFile(ctx, "keep", []byte("x"), 0); err == nil {
		t.Fatal("WriteFile over dir: want error, got nil")
	}
}
