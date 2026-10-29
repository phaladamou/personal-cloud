package filesystem

import (
	"context"
	"fmt"
	"io/fs"
	"path"
	"strings"
	"sync"
	"time"

	"github.com/phaladamou/personal-cloud/packages/types"
)

// MemFS is an in-memory FS for tests.
type MemFS struct {
	workspaceID string
	volumeID    string

	mu        sync.Mutex
	files     map[string]memFile
	dirs      map[string]bool
	snapshots map[string]memSnapshot
	seq       int
	watchers  map[*memWatcher]struct{}
}

// memSnapshot is a captured copy of the filesystem content.
type memSnapshot struct {
	files map[string]memFile
	dirs  map[string]bool
}

type memFile struct {
	data []byte
	mode fs.FileMode
	mod  time.Time
}

type memWatcher struct {
	path string
	ch   chan Event
}

var _ FS = (*MemFS)(nil)

// NewMemFS builds an empty in-memory FS.
func NewMemFS(workspaceID, volumeID string) (*MemFS, error) {
	if strings.TrimSpace(workspaceID) == "" {
		return nil, fmt.Errorf("filesystem: empty workspace id")
	}
	if strings.TrimSpace(volumeID) == "" {
		return nil, fmt.Errorf("filesystem: empty volume id")
	}
	return &MemFS{
		workspaceID: workspaceID,
		volumeID:    volumeID,
		files:       make(map[string]memFile),
		dirs:        make(map[string]bool),
		snapshots:   make(map[string]memSnapshot),
		watchers:    make(map[*memWatcher]struct{}),
	}, nil
}

// clean normalizes a workspace-relative path, rejecting ".." segments.
func clean(p string) (string, error) {
	for _, seg := range strings.Split(p, "/") {
		if seg == ".." {
			return "", fmt.Errorf("%w: %q", ErrPathEscape, p)
		}
	}
	c := path.Clean("/" + p)
	return strings.TrimPrefix(c, "/"), nil
}

// emitLocked notifies watchers of path or of a parent directory.
func (m *MemFS) emitLocked(p string, typ EventType) {
	for w := range m.watchers {
		if w.path != p && w.path != parentDir(p) {
			continue
		}
		select {
		case w.ch <- Event{Path: w.path, Type: typ, At: time.Now().UTC()}:
		default:
		}
	}
}

func parentDir(p string) string {
	if i := strings.LastIndex(p, "/"); i >= 0 {
		return p[:i]
	}
	return ""
}

// ReadFile returns the content of path.
func (m *MemFS) ReadFile(ctx context.Context, p string) ([]byte, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	c, err := clean(p)
	if err != nil {
		return nil, err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	f, ok := m.files[c]
	if !ok {
		return nil, fmt.Errorf("%w: %s", ErrNotFound, p)
	}
	return append([]byte(nil), f.data...), nil
}

// WriteFile stores data at path.
func (m *MemFS) WriteFile(ctx context.Context, p string, data []byte, perm fs.FileMode) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	c, err := clean(p)
	if err != nil {
		return err
	}
	if c == "" {
		return fmt.Errorf("filesystem: cannot write root")
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.dirs[c] {
		return fmt.Errorf("filesystem: is a directory: %s", p)
	}
	_, existed := m.files[c]
	if perm == 0 {
		perm = 0o644
	}
	m.files[c] = memFile{data: append([]byte(nil), data...), mode: perm, mod: time.Now().UTC()}
	if existed {
		m.emitLocked(c, EventModify)
	} else {
		m.emitLocked(c, EventCreate)
	}
	return nil
}

// Mkdir creates the directory at path. It is idempotent.
func (m *MemFS) Mkdir(ctx context.Context, p string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	c, err := clean(p)
	if err != nil {
		return err
	}
	if c == "" {
		return nil
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, ok := m.files[c]; ok {
		return fmt.Errorf("filesystem: not a directory: %s", p)
	}
	if !m.dirs[c] {
		m.dirs[c] = true
		m.emitLocked(c, EventCreate)
	}
	return nil
}

// Move renames oldPath to newPath.
func (m *MemFS) Move(ctx context.Context, oldPath, newPath string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	src, err := clean(oldPath)
	if err != nil {
		return err
	}
	dst, err := clean(newPath)
	if err != nil {
		return err
	}
	if dst == "" {
		return fmt.Errorf("filesystem: cannot move onto root")
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	moved := false
	for k, f := range m.files {
		if k != src && !strings.HasPrefix(k, src+"/") {
			continue
		}
		target := dst + strings.TrimPrefix(k, src)
		delete(m.files, k)
		m.files[target] = f
		moved = true
	}
	for d := range m.dirs {
		if d != src && !strings.HasPrefix(d, src+"/") {
			continue
		}
		target := dst + strings.TrimPrefix(d, src)
		delete(m.dirs, d)
		m.dirs[target] = true
		moved = true
	}
	if !moved {
		return fmt.Errorf("%w: %s", ErrNotFound, oldPath)
	}
	m.emitLocked(src, EventDelete)
	m.emitLocked(dst, EventCreate)
	return nil
}

// Delete removes the file or directory at path.
func (m *MemFS) Delete(ctx context.Context, p string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	c, err := clean(p)
	if err != nil {
		return err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	deleted := false
	for k := range m.files {
		if k != c && !strings.HasPrefix(k, c+"/") {
			continue
		}
		delete(m.files, k)
		deleted = true
	}
	for d := range m.dirs {
		if d != c && !strings.HasPrefix(d, c+"/") {
			continue
		}
		delete(m.dirs, d)
		deleted = true
	}
	if !deleted {
		return fmt.Errorf("%w: %s", ErrNotFound, p)
	}
	m.emitLocked(c, EventDelete)
	return nil
}

// hasDirLocked reports whether dir holds at least one file.
func (m *MemFS) hasDirLocked(dir string) bool {
	prefix := dir + "/"
	for k := range m.files {
		if dir == "" || strings.HasPrefix(k, prefix) {
			return true
		}
	}
	return false
}

// Stat describes the file or directory at path.
func (m *MemFS) Stat(ctx context.Context, p string) (fs.FileInfo, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	c, err := clean(p)
	if err != nil {
		return nil, err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if f, ok := m.files[c]; ok {
		return memInfo{name: base(c), size: int64(len(f.data)), mode: f.mode, mod: f.mod}, nil
	}
	if m.dirs[c] || m.hasDirLocked(c) {
		return memInfo{name: base(c), mode: fs.ModeDir | 0o755, mod: time.Now().UTC(), dir: true}, nil
	}
	return nil, fmt.Errorf("%w: %s", ErrNotFound, p)
}

// List returns the entries of the directory at dir.
func (m *MemFS) List(ctx context.Context, dir string) ([]fs.DirEntry, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	c, err := clean(dir)
	if err != nil {
		return nil, err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, ok := m.files[c]; ok {
		return nil, fmt.Errorf("filesystem: not a directory: %s", dir)
	}
	seen := make(map[string]memEntry)
	prefix := c
	if prefix != "" {
		prefix += "/"
	}
	addChild := func(key string, f memFile, explicitDir bool) {
		rest, ok := strings.CutPrefix(key, prefix)
		if c != "" && !ok {
			return
		}
		name := rest
		isDir := explicitDir
		var mod time.Time
		if i := strings.Index(rest, "/"); i >= 0 {
			name, isDir = rest[:i], true
		}
		if _, dup := seen[name]; dup {
			return
		}
		if isDir {
			mod = f.mod
			if mod.IsZero() {
				mod = time.Now().UTC()
			}
			seen[name] = memEntry{info: memInfo{name: name, mode: fs.ModeDir | 0o755, mod: mod, dir: true}}
		} else {
			seen[name] = memEntry{info: memInfo{name: name, size: int64(len(f.data)), mode: f.mode, mod: f.mod}}
		}
	}
	for k, f := range m.files {
		addChild(k, f, false)
	}
	for d := range m.dirs {
		addChild(d, memFile{}, true)
	}
	entries := make([]fs.DirEntry, 0, len(seen))
	for _, e := range seen {
		entries = append(entries, e)
	}
	return entries, nil
}

// Watch reports changes on path or its direct children until ctx is done.
func (m *MemFS) Watch(ctx context.Context, p string) (<-chan Event, error) {
	c, err := clean(p)
	if err != nil {
		return nil, err
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	w := &memWatcher{path: c, ch: make(chan Event, 16)}
	m.mu.Lock()
	m.watchers[w] = struct{}{}
	m.mu.Unlock()
	go func() {
		<-ctx.Done()
		m.mu.Lock()
		delete(m.watchers, w)
		m.mu.Unlock()
		close(w.ch)
	}()
	return w.ch, nil
}

// Snapshot captures the current content.
func (m *MemFS) Snapshot(ctx context.Context, name string) (types.Snapshot, error) {
	if err := ctx.Err(); err != nil {
		return types.Snapshot{}, err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	m.seq++
	id := fmt.Sprintf("mem-snap-%d", m.seq)
	snap, err := types.NewSnapshot(id, m.workspaceID, m.volumeID, name)
	if err != nil {
		return types.Snapshot{}, err
	}
	cp := make(map[string]memFile, len(m.files))
	var size int64
	for k, f := range m.files {
		cp[k] = memFile{data: append([]byte(nil), f.data...), mode: f.mode, mod: f.mod}
		size += int64(len(f.data))
	}
	dirs := make(map[string]bool, len(m.dirs))
	for d := range m.dirs {
		dirs[d] = true
	}
	snap.SizeBytes = size
	m.snapshots[id] = memSnapshot{files: cp, dirs: dirs}
	return snap, nil
}

// Restore replaces the content with a snapshot.
func (m *MemFS) Restore(ctx context.Context, snapshotID string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	cp, ok := m.snapshots[snapshotID]
	if !ok {
		return fmt.Errorf("%w: snapshot %s", ErrNotFound, snapshotID)
	}
	next := make(map[string]memFile, len(cp.files))
	for k, f := range cp.files {
		next[k] = memFile{data: append([]byte(nil), f.data...), mode: f.mode, mod: f.mod}
	}
	m.files = next
	dirs := make(map[string]bool, len(cp.dirs))
	for d := range cp.dirs {
		dirs[d] = true
	}
	m.dirs = dirs
	return nil
}

func base(p string) string {
	if i := strings.LastIndex(p, "/"); i >= 0 {
		return p[i+1:]
	}
	return p
}

// memInfo is an fs.FileInfo for in-memory files.
type memInfo struct {
	name string
	size int64
	mode fs.FileMode
	mod  time.Time
	dir  bool
}

func (i memInfo) Name() string       { return i.name }
func (i memInfo) Size() int64        { return i.size }
func (i memInfo) Mode() fs.FileMode  { return i.mode }
func (i memInfo) ModTime() time.Time { return i.mod }
func (i memInfo) IsDir() bool        { return i.dir }
func (i memInfo) Sys() any           { return nil }

// memEntry is an fs.DirEntry for in-memory files.
type memEntry struct {
	info memInfo
}

func (e memEntry) Name() string               { return e.info.name }
func (e memEntry) IsDir() bool                { return e.info.dir }
func (e memEntry) Type() fs.FileMode          { return e.info.mode.Type() }
func (e memEntry) Info() (fs.FileInfo, error) { return e.info, nil }
