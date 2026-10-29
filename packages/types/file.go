package types

import (
	"fmt"
	"strings"
	"time"
)

// FileEntry describes one file or directory inside a workspace.
type FileEntry struct {
	// Path is the workspace-relative slash-separated path.
	Path string `json:"path"`
	// Name is the base name of the entry.
	Name string `json:"name"`
	// IsDir reports whether the entry is a directory.
	IsDir bool `json:"is_dir"`
	// Size is the file size in bytes (0 for directories).
	Size int64 `json:"size"`
	// ModTime is the last modification time in UTC.
	ModTime time.Time `json:"mod_time"`
}

// NewFileEntry builds a validated file entry. Path must be non-blank;
// size must be non-negative.
func NewFileEntry(path, name string, isDir bool, size int64, modTime time.Time) (FileEntry, error) {
	path = strings.TrimSpace(path)
	if path == "" {
		return FileEntry{}, fmt.Errorf("types: empty file path")
	}
	name = strings.TrimSpace(name)
	if name == "" {
		return FileEntry{}, ErrEmptyName
	}
	if size < 0 {
		return FileEntry{}, fmt.Errorf("types: negative file size %d", size)
	}
	return FileEntry{
		Path:    path,
		Name:    name,
		IsDir:   isDir,
		Size:    size,
		ModTime: modTime.UTC(),
	}, nil
}

// String returns a short human-readable representation of the entry.
func (e FileEntry) String() string {
	if e.IsDir {
		return fmt.Sprintf("%s/ (dir)", e.Path)
	}
	return fmt.Sprintf("%s (%d bytes)", e.Path, e.Size)
}
