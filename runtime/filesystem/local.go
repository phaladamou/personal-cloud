package filesystem

import (
	"context"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/phaladamou/personal-cloud/packages/types"
)

// snapshotsDir holds snapshot copies inside the workspace root.
const snapshotsDir = ".snapshots"

// LocalFS is an FS backed by a local directory rooted at a workspace path.
type LocalFS struct {
	root        string
	workspaceID string
	volumeID    string

	// PollInterval is the delay between two watch scans.
	PollInterval time.Duration

	mu        sync.Mutex
	snapshots map[string]snapshotRecord
}

type snapshotRecord struct {
	snap types.Snapshot
	dir  string
}

var _ FS = (*LocalFS)(nil)

// NewLocalFS builds a LocalFS rooted at root, creating the directory when
// missing. workspaceID and volumeID identify the volume being snapshotted.
func NewLocalFS(root, workspaceID, volumeID string) (*LocalFS, error) {
	if strings.TrimSpace(root) == "" {
		return nil, fmt.Errorf("filesystem: empty root")
	}
	if strings.TrimSpace(workspaceID) == "" {
		return nil, fmt.Errorf("filesystem: empty workspace id")
	}
	if strings.TrimSpace(volumeID) == "" {
		return nil, fmt.Errorf("filesystem: empty volume id")
	}
	abs, err := filepath.Abs(root)
	if err != nil {
		return nil, fmt.Errorf("filesystem: resolve root: %w", err)
	}
	if err := os.MkdirAll(abs, 0o755); err != nil {
		return nil, fmt.Errorf("filesystem: create root: %w", err)
	}
	return &LocalFS{
		root:         abs,
		workspaceID:  workspaceID,
		volumeID:     volumeID,
		PollInterval: 50 * time.Millisecond,
		snapshots:    make(map[string]snapshotRecord),
	}, nil
}

// Root returns the absolute workspace root.
func (l *LocalFS) Root() string { return l.root }

// resolve maps a workspace-relative path to an absolute path, rejecting
// ".." segments that would leave the root.
func (l *LocalFS) resolve(path string) (string, error) {
	for _, seg := range strings.Split(filepath.ToSlash(path), "/") {
		if seg == ".." {
			return "", fmt.Errorf("%w: %q", ErrPathEscape, path)
		}
	}
	clean := filepath.Clean(string(filepath.Separator) + path)
	full := filepath.Join(l.root, clean)
	rel, err := filepath.Rel(l.root, full)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return "", fmt.Errorf("%w: %q", ErrPathEscape, path)
	}
	return full, nil
}

// reserved reports whether rel (root-relative) lives in the snapshot store.
func reserved(rel string) bool {
	return rel == snapshotsDir || strings.HasPrefix(rel, snapshotsDir+string(filepath.Separator))
}

func (l *LocalFS) checkReserved(full string) error {
	rel, err := filepath.Rel(l.root, full)
	if err != nil {
		return err
	}
	if reserved(rel) {
		return fmt.Errorf("filesystem: reserved path %q", rel)
	}
	return nil
}

func mapNotFound(err error, path string) error {
	if err == nil {
		return nil
	}
	if os.IsNotExist(err) {
		return fmt.Errorf("%w: %s", ErrNotFound, path)
	}
	return err
}

// ReadFile returns the content of path.
func (l *LocalFS) ReadFile(ctx context.Context, path string) ([]byte, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	full, err := l.resolve(path)
	if err != nil {
		return nil, err
	}
	data, err := os.ReadFile(full)
	return data, mapNotFound(err, path)
}

// WriteFile stores data at path, creating parent directories.
func (l *LocalFS) WriteFile(ctx context.Context, path string, data []byte, perm fs.FileMode) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	full, err := l.resolve(path)
	if err != nil {
		return err
	}
	if err := l.checkReserved(full); err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
		return fmt.Errorf("filesystem: create parent: %w", err)
	}
	if perm == 0 {
		perm = 0o644
	}
	return os.WriteFile(full, data, perm)
}

// Move renames oldPath to newPath, creating parent directories.
func (l *LocalFS) Move(ctx context.Context, oldPath, newPath string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	src, err := l.resolve(oldPath)
	if err != nil {
		return err
	}
	dst, err := l.resolve(newPath)
	if err != nil {
		return err
	}
	if err := l.checkReserved(src); err != nil {
		return err
	}
	if err := l.checkReserved(dst); err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		return fmt.Errorf("filesystem: create parent: %w", err)
	}
	return mapNotFound(os.Rename(src, dst), oldPath)
}

// Delete removes the file or directory at path.
func (l *LocalFS) Delete(ctx context.Context, path string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	full, err := l.resolve(path)
	if err != nil {
		return err
	}
	if err := l.checkReserved(full); err != nil {
		return err
	}
	if _, err := os.Lstat(full); err != nil {
		return mapNotFound(err, path)
	}
	return os.RemoveAll(full)
}

// Stat describes the file or directory at path.
func (l *LocalFS) Stat(ctx context.Context, path string) (fs.FileInfo, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	full, err := l.resolve(path)
	if err != nil {
		return nil, err
	}
	st, err := os.Stat(full)
	return st, mapNotFound(err, path)
}

// List returns the entries of the directory at dir.
func (l *LocalFS) List(ctx context.Context, dir string) ([]fs.DirEntry, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	full, err := l.resolve(dir)
	if err != nil {
		return nil, err
	}
	entries, err := os.ReadDir(full)
	return entries, mapNotFound(err, dir)
}

// Watch reports changes under path by polling until ctx is done.
func (l *LocalFS) Watch(ctx context.Context, path string) (<-chan Event, error) {
	full, err := l.resolve(path)
	if err != nil {
		return nil, err
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	interval := l.PollInterval
	if interval <= 0 {
		interval = 50 * time.Millisecond
	}
	// Snapshot synchronously so changes made after Watch returns are
	// always reported, regardless of goroutine scheduling.
	prev, prevErr := fingerprint(full)
	out := make(chan Event, 16)
	go l.watchLoop(ctx, path, full, interval, prev, prevErr, out)
	return out, nil
}

func (l *LocalFS) watchLoop(ctx context.Context, logical, full string, interval time.Duration, prev string, prevErr error, out chan<- Event) {
	defer close(out)
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case now := <-ticker.C:
			cur, err := fingerprint(full)
			var ev *Event
			switch {
			case err != nil && prevErr == nil:
				ev = &Event{Path: logical, Type: EventDelete, At: now}
				prevErr = err
			case err == nil && prevErr != nil:
				ev = &Event{Path: logical, Type: EventCreate, At: now}
				prevErr = nil
				prev = cur
			case err == nil && cur != prev:
				ev = &Event{Path: logical, Type: EventModify, At: now}
				prev = cur
			}
			if ev != nil {
				select {
				case out <- *ev:
				case <-ctx.Done():
					return
				}
			}
		}
	}
}

// fingerprint summarizes a file or directory for change detection.
func fingerprint(full string) (string, error) {
	st, err := os.Stat(full)
	if err != nil {
		return "", err
	}
	if !st.IsDir() {
		return fmt.Sprintf("f:%d:%d", st.Size(), st.ModTime().UnixNano()), nil
	}
	entries, err := os.ReadDir(full)
	if err != nil {
		return "", err
	}
	var b strings.Builder
	b.WriteString("d:")
	for _, e := range entries {
		info, err := e.Info()
		if err != nil {
			continue
		}
		fmt.Fprintf(&b, "%s/%d/%d;", e.Name(), info.Size(), info.ModTime().UnixNano())
	}
	return b.String(), nil
}

// Snapshot copies the workspace content into the snapshot store.
func (l *LocalFS) Snapshot(ctx context.Context, name string) (types.Snapshot, error) {
	if err := ctx.Err(); err != nil {
		return types.Snapshot{}, err
	}
	id := fmt.Sprintf("snap-%d", time.Now().UTC().UnixNano())
	snap, err := types.NewSnapshot(id, l.workspaceID, l.volumeID, name)
	if err != nil {
		return types.Snapshot{}, err
	}
	dest := filepath.Join(l.root, snapshotsDir, id)
	if err := os.MkdirAll(dest, 0o755); err != nil {
		return types.Snapshot{}, fmt.Errorf("filesystem: snapshot dir: %w", err)
	}
	size, err := copyTree(l.root, dest, true)
	if err != nil {
		os.RemoveAll(dest)
		return types.Snapshot{}, err
	}
	snap.SizeBytes = size
	l.mu.Lock()
	l.snapshots[id] = snapshotRecord{snap: snap, dir: dest}
	l.mu.Unlock()
	return snap, nil
}

// Restore replaces the workspace content with a snapshot.
func (l *LocalFS) Restore(ctx context.Context, snapshotID string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	l.mu.Lock()
	rec, ok := l.snapshots[snapshotID]
	l.mu.Unlock()
	if !ok {
		dir := filepath.Join(l.root, snapshotsDir, snapshotID)
		if st, err := os.Stat(dir); err != nil || !st.IsDir() {
			return fmt.Errorf("%w: snapshot %s", ErrNotFound, snapshotID)
		}
		rec = snapshotRecord{dir: dir}
	}
	entries, err := os.ReadDir(l.root)
	if err != nil {
		return fmt.Errorf("filesystem: list root: %w", err)
	}
	for _, e := range entries {
		if e.Name() == snapshotsDir {
			continue
		}
		if err := os.RemoveAll(filepath.Join(l.root, e.Name())); err != nil {
			return fmt.Errorf("filesystem: clear workspace: %w", err)
		}
	}
	_, err = copyTree(rec.dir, l.root, false)
	return err
}

// copyTree replicates src into dst, returning the copied byte size. When
// skipStore is set, the snapshot store is excluded.
func copyTree(src, dst string, skipStore bool) (int64, error) {
	var size int64
	err := filepath.WalkDir(src, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(src, path)
		if err != nil {
			return err
		}
		if rel == "." {
			return nil
		}
		if skipStore && reserved(rel) {
			if d.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}
		target := filepath.Join(dst, rel)
		if d.IsDir() {
			return os.MkdirAll(target, 0o755)
		}
		n, err := copyFile(path, target)
		size += n
		return err
	})
	if err != nil {
		return 0, fmt.Errorf("filesystem: copy tree: %w", err)
	}
	return size, nil
}

func copyFile(src, dst string) (int64, error) {
	in, err := os.Open(src)
	if err != nil {
		return 0, err
	}
	defer in.Close()
	st, err := in.Stat()
	if err != nil {
		return 0, err
	}
	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		return 0, err
	}
	out, err := os.OpenFile(dst, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, st.Mode().Perm())
	if err != nil {
		return 0, err
	}
	n, err := io.Copy(out, in)
	cerr := out.Close()
	if err != nil {
		return n, err
	}
	return n, cerr
}
