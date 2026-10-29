package controller

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"path/filepath"
	"sort"
	"strings"

	"github.com/phaladamou/personal-cloud/packages/protocol"
	"github.com/phaladamou/personal-cloud/packages/types"
	"github.com/phaladamou/personal-cloud/runtime/filesystem"
)

// Errors returned by the file operations.
var (
	// ErrFilesystemsUnavailable is returned when the controller was
	// built without WithFilesystems.
	ErrFilesystemsUnavailable = errors.New("controller: filesystem provider not configured")
	// ErrNotDirectory is returned when a directory was expected but a
	// file was found.
	ErrNotDirectory = errors.New("controller: not a directory")
	// ErrFileTooLarge is returned when a file exceeds the requested
	// size limit.
	ErrFileTooLarge = errors.New("controller: file exceeds size limit")
	// ErrInvalidPath is returned when a path is rejected before
	// touching the filesystem (for example deleting the root).
	ErrInvalidPath = errors.New("controller: invalid file path")
)

// File change operations published in file.changed events.
const (
	fileOpWrite  = "write"
	fileOpMkdir  = "mkdir"
	fileOpDelete = "delete"
)

// FilesystemProvider returns the filesystem of a workspace. Paths are
// slash-separated and relative to the workspace root.
type FilesystemProvider interface {
	FSFor(workspaceID string) (filesystem.FS, error)
}

// FilesystemProviderFunc adapts a function to a FilesystemProvider.
type FilesystemProviderFunc func(workspaceID string) (filesystem.FS, error)

// FSFor implements FilesystemProvider.
func (f FilesystemProviderFunc) FSFor(workspaceID string) (filesystem.FS, error) {
	return f(workspaceID)
}

// WithFilesystems configures the provider used by the file operations.
// Without it, those operations return ErrFilesystemsUnavailable.
func WithFilesystems(p FilesystemProvider) Option {
	return func(c *Controller) { c.fs = p }
}

// DirFSProvider serves one directory-backed filesystem per workspace
// under Root: Root/<workspaceID>/.
type DirFSProvider struct {
	Root string
}

// FSFor returns the LocalFS rooted at the workspace directory,
// creating it on first use.
func (p DirFSProvider) FSFor(workspaceID string) (filesystem.FS, error) {
	if strings.TrimSpace(p.Root) == "" {
		return nil, fmt.Errorf("controller: empty filesystem root")
	}
	id := strings.TrimSpace(workspaceID)
	if id == "" || id == "." || id == ".." ||
		strings.ContainsRune(id, '/') || strings.ContainsRune(id, filepath.Separator) {
		return nil, fmt.Errorf("controller: invalid workspace id %q", workspaceID)
	}
	return filesystem.NewLocalFS(filepath.Join(p.Root, id), id, "default")
}

// fileChangedPayload is the payload published on file operations.
type fileChangedPayload struct {
	Path string `json:"path"`
	Op   string `json:"op"`
}

// emitFileChanged publishes a file.changed event for the workspace.
func (c *Controller) emitFileChanged(workspaceID, path, op string) {
	ev, err := protocol.NewEvent(protocol.EventFileChanged, workspaceID,
		fileChangedPayload{Path: path, Op: op})
	if err != nil {
		return
	}
	c.bus.Publish(ev)
}

// workspaceFS returns the workspace and its filesystem.
func (c *Controller) workspaceFS(ctx context.Context, workspaceID string) (types.Workspace, filesystem.FS, error) {
	ws, err := c.store.Get(ctx, workspaceID)
	if err != nil {
		return types.Workspace{}, nil, err
	}
	if c.fs == nil {
		return types.Workspace{}, nil, ErrFilesystemsUnavailable
	}
	fsys, err := c.fs.FSFor(workspaceID)
	if err != nil {
		return types.Workspace{}, nil, err
	}
	return ws, fsys, nil
}

// joinDir joins a listed directory with an entry name.
func joinDir(dir, name string) string {
	dir = strings.Trim(strings.TrimSpace(dir), "/")
	if dir == "" || dir == "." {
		return name
	}
	return dir + "/" + name
}

// ListFiles returns the entries of the directory at path, ordered by
// name. Empty path denotes the workspace root.
func (c *Controller) ListFiles(ctx context.Context, workspaceID, path string) ([]types.FileEntry, error) {
	_, fsys, err := c.workspaceFS(ctx, workspaceID)
	if err != nil {
		return nil, err
	}
	st, err := fsys.Stat(ctx, path)
	if err != nil {
		return nil, err
	}
	if !st.IsDir() {
		return nil, fmt.Errorf("%w: %s", ErrNotDirectory, path)
	}
	entries, err := fsys.List(ctx, path)
	if err != nil {
		return nil, err
	}
	out := make([]types.FileEntry, 0, len(entries))
	for _, e := range entries {
		var size int64
		var modTime = st.ModTime()
		isDir := e.IsDir()
		if info, err := e.Info(); err == nil {
			size = info.Size()
			modTime = info.ModTime()
			isDir = info.IsDir()
		}
		entry, err := types.NewFileEntry(joinDir(path, e.Name()), e.Name(), isDir, size, modTime)
		if err != nil {
			return nil, err
		}
		out = append(out, entry)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out, nil
}

// ReadFile returns the content of path. A negative maxBytes disables
// the size cap; otherwise files larger than maxBytes fail with
// ErrFileTooLarge without being read.
func (c *Controller) ReadFile(ctx context.Context, workspaceID, path string, maxBytes int64) ([]byte, error) {
	_, fsys, err := c.workspaceFS(ctx, workspaceID)
	if err != nil {
		return nil, err
	}
	st, err := fsys.Stat(ctx, path)
	if err != nil {
		return nil, err
	}
	if st.IsDir() {
		return nil, fmt.Errorf("%w: %s", ErrNotDirectory, path)
	}
	if maxBytes >= 0 && st.Size() > maxBytes {
		return nil, fmt.Errorf("%w: %s (%d bytes)", ErrFileTooLarge, path, st.Size())
	}
	data, err := fsys.ReadFile(ctx, path)
	if err != nil {
		return nil, err
	}
	if maxBytes >= 0 && int64(len(data)) > maxBytes {
		return nil, fmt.Errorf("%w: %s (%d bytes)", ErrFileTooLarge, path, len(data))
	}
	return data, nil
}

// WriteFile stores data at path, creating parent directories, and emits
// file.changed.
func (c *Controller) WriteFile(ctx context.Context, workspaceID, path string, data []byte) error {
	ws, fsys, err := c.workspaceFS(ctx, workspaceID)
	if err != nil {
		return err
	}
	if err := fsys.WriteFile(ctx, path, data, fs.FileMode(0)); err != nil {
		return err
	}
	c.emitFileChanged(ws.ID, path, fileOpWrite)
	return nil
}

// Mkdir creates the directory at path with its parents, and emits
// file.changed.
func (c *Controller) Mkdir(ctx context.Context, workspaceID, path string) error {
	ws, fsys, err := c.workspaceFS(ctx, workspaceID)
	if err != nil {
		return err
	}
	if err := fsys.Mkdir(ctx, path); err != nil {
		return err
	}
	c.emitFileChanged(ws.ID, path, fileOpMkdir)
	return nil
}

// DeleteFile removes the file or directory at path, and emits
// file.changed. Deleting the workspace root is rejected.
func (c *Controller) DeleteFile(ctx context.Context, workspaceID, path string) error {
	ws, fsys, err := c.workspaceFS(ctx, workspaceID)
	if err != nil {
		return err
	}
	trimmed := strings.Trim(strings.TrimSpace(path), "/")
	if trimmed == "" || trimmed == "." {
		return fmt.Errorf("%w: cannot delete workspace root", ErrInvalidPath)
	}
	if err := fsys.Delete(ctx, path); err != nil {
		return err
	}
	c.emitFileChanged(ws.ID, path, fileOpDelete)
	return nil
}
