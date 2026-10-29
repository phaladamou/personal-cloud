package volumes

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"

	"github.com/phaladamou/personal-cloud/packages/types"
)

// LocalManager is a VolumeManager backed by a directory tree:
//
//	root/<workspaceID>/volumes/<volumeID>/meta.json
//	root/<workspaceID>/volumes/<volumeID>/data/   (volume content)
//
// Metadata is written atomically (temp file + rename) and reloaded
// from disk on every call, so a reopened manager sees the same state
// and concurrent processes never observe a half-written meta file.
// Attach/detach only flips the persisted attached flag; there is no
// kernel mount involved in Phase 2.
type LocalManager struct {
	root string

	mu sync.Mutex
}

// volumeMeta is the on-disk envelope: the volume plus its attach state.
type volumeMeta struct {
	Volume   types.Volume `json:"volume"`
	Attached bool         `json:"attached"`
}

var _ VolumeManager = (*LocalManager)(nil)

// NewLocalManager builds a LocalManager rooted at root, creating the
// directory when missing.
func NewLocalManager(root string) (*LocalManager, error) {
	if strings.TrimSpace(root) == "" {
		return nil, fmt.Errorf("volumes: empty root")
	}
	abs, err := filepath.Abs(root)
	if err != nil {
		return nil, fmt.Errorf("volumes: resolve root: %w", err)
	}
	if err := os.MkdirAll(abs, 0o755); err != nil {
		return nil, fmt.Errorf("volumes: create root: %w", err)
	}
	return &LocalManager{root: abs}, nil
}

// Root returns the absolute storage root.
func (m *LocalManager) Root() string { return m.root }

// DataPath returns the host directory holding the volume content. It
// does not check that the volume exists.
func (m *LocalManager) DataPath(workspaceID, volumeID string) string {
	return filepath.Join(m.volDir(workspaceID, volumeID), "data")
}

// cleanID rejects empty ids and path separators so caller-supplied ids
// cannot escape the storage root.
func cleanID(what, id string) (string, error) {
	id = strings.TrimSpace(id)
	if id == "" || id == "." || id == ".." ||
		strings.ContainsRune(id, '/') || strings.ContainsRune(id, filepath.Separator) {
		return "", fmt.Errorf("volumes: invalid %s id %q", what, id)
	}
	return id, nil
}

func (m *LocalManager) volDir(workspaceID, volumeID string) string {
	return filepath.Join(m.root, workspaceID, "volumes", volumeID)
}

func generateID() (string, error) {
	var b [8]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", err
	}
	return "vol-" + hex.EncodeToString(b[:]), nil
}

// load reads the volume metadata; the caller must hold m.mu.
func (m *LocalManager) load(workspaceID, volumeID string) (volumeMeta, error) {
	var meta volumeMeta
	raw, err := os.ReadFile(filepath.Join(m.volDir(workspaceID, volumeID), "meta.json"))
	if err != nil {
		if os.IsNotExist(err) {
			return meta, fmt.Errorf("%w: %s in workspace %s", ErrVolumeNotFound, volumeID, workspaceID)
		}
		return meta, fmt.Errorf("volumes: read meta: %w", err)
	}
	if err := json.Unmarshal(raw, &meta); err != nil {
		return meta, fmt.Errorf("volumes: decode meta: %w", err)
	}
	return meta, nil
}

// save writes the volume metadata atomically; the caller must hold m.mu.
func (m *LocalManager) save(workspaceID string, meta volumeMeta) error {
	dir := m.volDir(workspaceID, meta.Volume.ID)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("volumes: create volume dir: %w", err)
	}
	raw, err := json.Marshal(meta)
	if err != nil {
		return fmt.Errorf("volumes: encode meta: %w", err)
	}
	tmp, err := os.CreateTemp(dir, "meta-*.tmp")
	if err != nil {
		return fmt.Errorf("volumes: write meta: %w", err)
	}
	tmpName := tmp.Name()
	if _, err := tmp.Write(raw); err != nil {
		tmp.Close()
		os.Remove(tmpName)
		return fmt.Errorf("volumes: write meta: %w", err)
	}
	if err := tmp.Close(); err != nil {
		os.Remove(tmpName)
		return fmt.Errorf("volumes: write meta: %w", err)
	}
	if err := os.Rename(tmpName, filepath.Join(dir, "meta.json")); err != nil {
		os.Remove(tmpName)
		return fmt.Errorf("volumes: write meta: %w", err)
	}
	return nil
}

// Create provisions a new detached volume in the workspace.
func (m *LocalManager) Create(ctx context.Context, workspaceID, name string, sizeGB int64, mountPath string) (types.Volume, error) {
	if err := ctx.Err(); err != nil {
		return types.Volume{}, err
	}
	workspaceID, err := cleanID("workspace", workspaceID)
	if err != nil {
		return types.Volume{}, err
	}
	id, err := generateID()
	if err != nil {
		return types.Volume{}, fmt.Errorf("volumes: generate id: %w", err)
	}
	vol, err := types.NewVolume(id, workspaceID, name, sizeGB, mountPath)
	if err != nil {
		return types.Volume{}, err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if err := os.MkdirAll(filepath.Join(m.volDir(workspaceID, id), "data"), 0o755); err != nil {
		return types.Volume{}, fmt.Errorf("volumes: create data dir: %w", err)
	}
	if err := m.save(workspaceID, volumeMeta{Volume: vol}); err != nil {
		os.RemoveAll(m.volDir(workspaceID, id))
		return types.Volume{}, err
	}
	return vol, nil
}

// setAttached flips the persisted attach flag.
func (m *LocalManager) setAttached(ctx context.Context, workspaceID, volumeID string, attached bool) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	workspaceID, err := cleanID("workspace", workspaceID)
	if err != nil {
		return err
	}
	volumeID, err = cleanID("volume", volumeID)
	if err != nil {
		return err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	meta, err := m.load(workspaceID, volumeID)
	if err != nil {
		return err
	}
	meta.Attached = attached
	return m.save(workspaceID, meta)
}

// Attach marks the volume as attached. It is idempotent.
func (m *LocalManager) Attach(ctx context.Context, workspaceID, volumeID string) error {
	return m.setAttached(ctx, workspaceID, volumeID, true)
}

// Detach marks the volume as detached. It is idempotent.
func (m *LocalManager) Detach(ctx context.Context, workspaceID, volumeID string) error {
	return m.setAttached(ctx, workspaceID, volumeID, false)
}

// Attached reports whether the volume is currently attached.
func (m *LocalManager) Attached(ctx context.Context, workspaceID, volumeID string) (bool, error) {
	if err := ctx.Err(); err != nil {
		return false, err
	}
	workspaceID, err := cleanID("workspace", workspaceID)
	if err != nil {
		return false, err
	}
	volumeID, err = cleanID("volume", volumeID)
	if err != nil {
		return false, err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	meta, err := m.load(workspaceID, volumeID)
	if err != nil {
		return false, err
	}
	return meta.Attached, nil
}

// Delete removes the volume and its data.
func (m *LocalManager) Delete(ctx context.Context, workspaceID, volumeID string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	workspaceID, err := cleanID("workspace", workspaceID)
	if err != nil {
		return err
	}
	volumeID, err = cleanID("volume", volumeID)
	if err != nil {
		return err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	dir := m.volDir(workspaceID, volumeID)
	if _, err := os.Stat(filepath.Join(dir, "meta.json")); err != nil {
		if os.IsNotExist(err) {
			return fmt.Errorf("%w: %s in workspace %s", ErrVolumeNotFound, volumeID, workspaceID)
		}
		return fmt.Errorf("volumes: stat volume: %w", err)
	}
	return os.RemoveAll(dir)
}

// Inspect returns the volume metadata.
func (m *LocalManager) Inspect(ctx context.Context, workspaceID, volumeID string) (types.Volume, error) {
	if err := ctx.Err(); err != nil {
		return types.Volume{}, err
	}
	workspaceID, err := cleanID("workspace", workspaceID)
	if err != nil {
		return types.Volume{}, err
	}
	volumeID, err = cleanID("volume", volumeID)
	if err != nil {
		return types.Volume{}, err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	meta, err := m.load(workspaceID, volumeID)
	if err != nil {
		return types.Volume{}, err
	}
	return meta.Volume, nil
}

// List returns every volume of the workspace, ordered by id.
func (m *LocalManager) List(ctx context.Context, workspaceID string) ([]types.Volume, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	workspaceID, err := cleanID("workspace", workspaceID)
	if err != nil {
		return nil, err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	entries, err := os.ReadDir(filepath.Join(m.root, workspaceID, "volumes"))
	if err != nil {
		if os.IsNotExist(err) {
			return []types.Volume{}, nil
		}
		return nil, fmt.Errorf("volumes: list: %w", err)
	}
	out := []types.Volume{}
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		meta, err := m.load(workspaceID, e.Name())
		if err != nil {
			return nil, err
		}
		out = append(out, meta.Volume)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out, nil
}
