// Package filesystem abstracts the workspace filesystem.
//
// It defines the FS interface (read, write, mkdir, move, delete, watch
// and snapshot/restore hooks), a local directory-backed implementation
// rooted at a workspace path and an in-memory fake for tests.
package filesystem

import (
	"context"
	"errors"
	"io/fs"
	"time"

	"github.com/phaladamou/personal-cloud/packages/types"
)

// Errors returned by FS implementations.
var (
	// ErrNotFound is returned when a path or snapshot does not exist.
	ErrNotFound = errors.New("filesystem: not found")
	// ErrPathEscape is returned when a path tries to leave the workspace
	// root (for example with ".." segments).
	ErrPathEscape = errors.New("filesystem: path escapes workspace root")
)

// EventType describes what happened to a watched path.
type EventType string

// Valid event types.
const (
	EventCreate EventType = "CREATE"
	EventModify EventType = "MODIFY"
	EventDelete EventType = "DELETE"
)

// Event is a change notification for a watched path.
type Event struct {
	Path string
	Type EventType
	At   time.Time
}

// FS is a workspace filesystem with watch and snapshot support.
//
// Paths are slash-separated and relative to the workspace root; "" and
// "." both denote the root. Implementations must be safe for concurrent
// use.
type FS interface {
	// ReadFile returns the content of path.
	ReadFile(ctx context.Context, path string) ([]byte, error)
	// WriteFile stores data at path, creating parent directories.
	WriteFile(ctx context.Context, path string, data []byte, perm fs.FileMode) error
	// Mkdir creates the directory at path with its parents. It is
	// idempotent: an existing directory is not an error.
	Mkdir(ctx context.Context, path string) error
	// Move renames oldPath to newPath, creating parent directories.
	Move(ctx context.Context, oldPath, newPath string) error
	// Delete removes the file or directory at path.
	Delete(ctx context.Context, path string) error
	// Stat describes the file or directory at path.
	Stat(ctx context.Context, path string) (fs.FileInfo, error)
	// List returns the entries of the directory at dir.
	List(ctx context.Context, dir string) ([]fs.DirEntry, error)
	// Watch reports changes under path until ctx is done, then closes
	// the channel.
	Watch(ctx context.Context, path string) (<-chan Event, error)
	// Snapshot captures the workspace content and returns its metadata.
	Snapshot(ctx context.Context, name string) (types.Snapshot, error)
	// Restore replaces the workspace content with a snapshot.
	Restore(ctx context.Context, snapshotID string) error
}
