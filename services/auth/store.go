package auth

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"
)

// ErrTokenNotFound is returned when no record matches a token id.
var ErrTokenNotFound = errors.New("auth: token not found")

// Record is the server-side state of one issued bearer token: who it
// identifies (user + device) and when it stops working.
type Record struct {
	ID        string    `json:"id"`
	UserID    string    `json:"user_id"`
	Device    string    `json:"device"`
	CreatedAt time.Time `json:"created_at"`
	ExpiresAt time.Time `json:"expires_at"`
}

// Expired reports whether the record outlived expiresAt at now.
func (r Record) Expired(now time.Time) bool {
	return !now.Before(r.ExpiresAt)
}

// Store persists token records by id. Implementations must be safe for
// concurrent use. Revocation is deletion: a missing record authenticates
// nothing.
type Store interface {
	Save(ctx context.Context, r Record) error
	Load(ctx context.Context, id string) (Record, error)
	Delete(ctx context.Context, id string) error
}

// FakeStore is an in-memory Store for tests.
type FakeStore struct {
	mu     sync.RWMutex
	tokens map[string]Record
}

// NewFakeStore builds an empty FakeStore.
func NewFakeStore() *FakeStore {
	return &FakeStore{tokens: make(map[string]Record)}
}

// Save stores r by id, replacing any previous record.
func (s *FakeStore) Save(_ context.Context, r Record) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.tokens == nil {
		s.tokens = make(map[string]Record)
	}
	s.tokens[r.ID] = r
	return nil
}

// Load returns the record for id, or ErrTokenNotFound.
func (s *FakeStore) Load(_ context.Context, id string) (Record, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	r, ok := s.tokens[id]
	if !ok {
		return Record{}, ErrTokenNotFound
	}
	return r, nil
}

// Delete drops the record for id. Deleting a missing id is a no-op.
func (s *FakeStore) Delete(_ context.Context, id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.tokens, id)
	return nil
}

// fileStoreVersion versions the FileStore JSON envelope.
const fileStoreVersion = 1

// fileFormat is the on-disk envelope of the token records.
type fileFormat struct {
	Version int               `json:"version"`
	Tokens  map[string]Record `json:"tokens"`
}

// FileStore is a file-backed Store: one JSON document holding every
// record, rewritten atomically (temp file + rename) on each mutation.
type FileStore struct {
	mu     sync.RWMutex
	path   string
	tokens map[string]Record
}

// NewFileStore opens (or creates) the token store at path. A missing file
// means an empty store; a corrupt or version-mismatched file is an error.
func NewFileStore(path string) (*FileStore, error) {
	s := &FileStore{path: path, tokens: make(map[string]Record)}
	raw, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return s, nil
		}
		return nil, fmt.Errorf("auth: read store %s: %w", path, err)
	}
	var doc fileFormat
	if err := json.Unmarshal(raw, &doc); err != nil {
		return nil, fmt.Errorf("auth: decode store %s: %w", path, err)
	}
	if doc.Version != fileStoreVersion {
		return nil, fmt.Errorf("auth: unsupported store version %d in %s", doc.Version, path)
	}
	if doc.Tokens != nil {
		s.tokens = doc.Tokens
	}
	return s, nil
}

// saveLocked persists the in-memory map. The caller must hold s.mu.
func (s *FileStore) saveLocked() error {
	doc := fileFormat{Version: fileStoreVersion, Tokens: s.tokens}
	raw, err := json.MarshalIndent(doc, "", "  ")
	if err != nil {
		return fmt.Errorf("auth: encode store: %w", err)
	}
	if err := os.MkdirAll(filepath.Dir(s.path), 0o755); err != nil {
		return fmt.Errorf("auth: create store dir: %w", err)
	}
	tmp, err := os.CreateTemp(filepath.Dir(s.path), ".tokens-*.tmp")
	if err != nil {
		return fmt.Errorf("auth: write store: %w", err)
	}
	tmpName := tmp.Name()
	if _, err := tmp.Write(raw); err != nil {
		_ = tmp.Close()
		_ = os.Remove(tmpName)
		return fmt.Errorf("auth: write store: %w", err)
	}
	if err := tmp.Close(); err != nil {
		_ = os.Remove(tmpName)
		return fmt.Errorf("auth: write store: %w", err)
	}
	if err := os.Chmod(tmpName, 0o600); err != nil {
		_ = os.Remove(tmpName)
		return fmt.Errorf("auth: protect store: %w", err)
	}
	if err := os.Rename(tmpName, s.path); err != nil {
		_ = os.Remove(tmpName)
		return fmt.Errorf("auth: write store: %w", err)
	}
	return nil
}

// Save stores r by id, replacing any previous record.
func (s *FileStore) Save(_ context.Context, r Record) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.tokens[r.ID] = r
	return s.saveLocked()
}

// Load returns the record for id, or ErrTokenNotFound.
func (s *FileStore) Load(_ context.Context, id string) (Record, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	r, ok := s.tokens[id]
	if !ok {
		return Record{}, ErrTokenNotFound
	}
	return r, nil
}

// Delete drops the record for id, persisting even when id is missing so a
// crash can never resurrect a revoked token.
func (s *FileStore) Delete(_ context.Context, id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.tokens, id)
	return s.saveLocked()
}
