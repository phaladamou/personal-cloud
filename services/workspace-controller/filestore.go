package controller

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"sync"

	"github.com/phaladamou/personal-cloud/packages/types"
)

// fileFormat is the on-disk envelope. The version field lets a future
// migration detect stores written by older binaries.
type fileFormat struct {
	Version    int                        `json:"version"`
	Workspaces map[string]types.Workspace `json:"workspaces"`
}

// fileStoreVersion is the only envelope version written and accepted.
const fileStoreVersion = 1

// FileStore is a Store backed by a single JSON file.
//
// NOTE: this is the Phase 1 persistence. A Postgres-backed Store comes
// later behind the same interface; nothing outside this file may assume
// a file layout.
//
// Every mutation rewrites the file atomically (write temp + rename), so
// readers never observe a half-written store. Use NewFileStore to open
// (or create) the store at path.
type FileStore struct {
	path string
	mu   sync.Mutex
	data map[string]types.Workspace
}

// NewFileStore opens the JSON store at path, creating an empty store
// when the file does not exist. The parent directory must exist.
func NewFileStore(path string) (*FileStore, error) {
	s := &FileStore{path: path, data: make(map[string]types.Workspace)}
	raw, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return s, nil
		}
		return nil, fmt.Errorf("controller: read store %s: %w", path, err)
	}
	var doc fileFormat
	if err := json.Unmarshal(raw, &doc); err != nil {
		return nil, fmt.Errorf("controller: decode store %s: %w", path, err)
	}
	if doc.Version != fileStoreVersion {
		return nil, fmt.Errorf("controller: unsupported store version %d in %s", doc.Version, path)
	}
	if doc.Workspaces != nil {
		s.data = doc.Workspaces
	}
	return s, nil
}

// saveLocked persists the in-memory map. The caller must hold s.mu.
func (s *FileStore) saveLocked() error {
	doc := fileFormat{Version: fileStoreVersion, Workspaces: s.data}
	raw, err := json.MarshalIndent(doc, "", "  ")
	if err != nil {
		return fmt.Errorf("controller: encode store: %w", err)
	}
	tmp, err := os.CreateTemp(filepath.Dir(s.path), ".workspaces-*.tmp")
	if err != nil {
		return fmt.Errorf("controller: write store: %w", err)
	}
	tmpName := tmp.Name()
	if _, err := tmp.Write(raw); err != nil {
		_ = tmp.Close()
		_ = os.Remove(tmpName)
		return fmt.Errorf("controller: write store: %w", err)
	}
	if err := tmp.Close(); err != nil {
		_ = os.Remove(tmpName)
		return fmt.Errorf("controller: write store: %w", err)
	}
	if err := os.Rename(tmpName, s.path); err != nil {
		_ = os.Remove(tmpName)
		return fmt.Errorf("controller: write store: %w", err)
	}
	return nil
}

// Create persists a new workspace.
func (s *FileStore) Create(ctx context.Context, ws types.Workspace) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.data[ws.ID]; ok {
		return fmt.Errorf("%w: %s", ErrWorkspaceExists, ws.ID)
	}
	s.data[ws.ID] = ws
	return s.saveLocked()
}

// Get returns the workspace with the given id.
func (s *FileStore) Get(ctx context.Context, id string) (types.Workspace, error) {
	if err := ctx.Err(); err != nil {
		return types.Workspace{}, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	ws, ok := s.data[id]
	if !ok {
		return types.Workspace{}, fmt.Errorf("%w: %s", ErrWorkspaceNotFound, id)
	}
	return ws, nil
}

// List returns every stored workspace, ordered by id.
func (s *FileStore) List(ctx context.Context) ([]types.Workspace, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]types.Workspace, 0, len(s.data))
	for _, ws := range s.data {
		out = append(out, ws)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out, nil
}

// Update replaces the stored workspace.
func (s *FileStore) Update(ctx context.Context, ws types.Workspace) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.data[ws.ID]; !ok {
		return fmt.Errorf("%w: %s", ErrWorkspaceNotFound, ws.ID)
	}
	s.data[ws.ID] = ws
	return s.saveLocked()
}

// Delete removes the workspace.
func (s *FileStore) Delete(ctx context.Context, id string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.data[id]; !ok {
		return fmt.Errorf("%w: %s", ErrWorkspaceNotFound, id)
	}
	delete(s.data, id)
	return s.saveLocked()
}
