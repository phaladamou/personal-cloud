package snapshots

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"

	"github.com/phaladamou/personal-cloud/packages/types"
)

// LocalManager is a content-addressed SnapshotManager backed by a
// directory tree:
//
//	root/<workspaceID>/<snapshotID>/meta.json
//	root/<workspaceID>/<snapshotID>/data/   (captured volume content)
//
// The volumeData resolver maps (workspaceID, volumeID) to the host
// directory holding the live volume content (for example
// volumes.LocalManager.DataPath).
//
// Content addressing: the snapshot id is "snap-" plus the first 16 hex
// characters of the SHA-256 tree hash (sorted relative paths, modes
// and file contents). Creating a snapshot of unchanged content returns
// the existing snapshot instead of storing a duplicate.
//
// Copy strategy: every snapshot is an independent full copy, published
// atomically (staging directory + rename). Hardlink-based
// copy-on-write was deliberately avoided: in-place writers such as
// os.WriteFile truncate the target, which would corrupt every snapshot
// sharing the inode. Restore is likewise always a full copy, so later
// live writes can never alias snapshot data. A hardlink optimization
// may be added later behind this same interface, but only together
// with a writer contract that forbids in-place mutation.
type LocalManager struct {
	root       string
	volumeData func(workspaceID, volumeID string) string

	mu sync.Mutex
}

// snapshotMeta is the on-disk envelope: the snapshot plus its full
// tree hash.
type snapshotMeta struct {
	Snapshot types.Snapshot `json:"snapshot"`
	TreeHash string         `json:"tree_hash"`
}

var _ SnapshotManager = (*LocalManager)(nil)

// NewLocalManager builds a LocalManager rooted at root. volumeData must
// not be nil.
func NewLocalManager(root string, volumeData func(workspaceID, volumeID string) string) (*LocalManager, error) {
	if strings.TrimSpace(root) == "" {
		return nil, fmt.Errorf("snapshots: empty root")
	}
	if volumeData == nil {
		return nil, fmt.Errorf("snapshots: nil volume resolver")
	}
	abs, err := filepath.Abs(root)
	if err != nil {
		return nil, fmt.Errorf("snapshots: resolve root: %w", err)
	}
	if err := os.MkdirAll(abs, 0o755); err != nil {
		return nil, fmt.Errorf("snapshots: create root: %w", err)
	}
	return &LocalManager{root: abs, volumeData: volumeData}, nil
}

// Root returns the absolute snapshot root.
func (m *LocalManager) Root() string { return m.root }

// cleanID rejects empty ids and path separators so caller-supplied ids
// cannot escape the snapshot root.
func cleanID(what, id string) (string, error) {
	id = strings.TrimSpace(id)
	if id == "" || id == "." || id == ".." ||
		strings.ContainsRune(id, '/') || strings.ContainsRune(id, filepath.Separator) {
		return "", fmt.Errorf("snapshots: invalid %s id %q", what, id)
	}
	return id, nil
}

func (m *LocalManager) snapDir(workspaceID, snapshotID string) string {
	return filepath.Join(m.root, workspaceID, snapshotID)
}

// load reads the snapshot metadata; the caller must hold m.mu.
func (m *LocalManager) load(workspaceID, snapshotID string) (snapshotMeta, error) {
	var meta snapshotMeta
	raw, err := os.ReadFile(filepath.Join(m.snapDir(workspaceID, snapshotID), "meta.json"))
	if err != nil {
		if os.IsNotExist(err) {
			return meta, fmt.Errorf("%w: %s in workspace %s", ErrSnapshotNotFound, snapshotID, workspaceID)
		}
		return meta, fmt.Errorf("snapshots: read meta: %w", err)
	}
	if err := json.Unmarshal(raw, &meta); err != nil {
		return meta, fmt.Errorf("snapshots: decode meta: %w", err)
	}
	return meta, nil
}

// hashTree returns the SHA-256 hex digest of the file tree under root
// plus the total file byte size. Entries are hashed in sorted
// relative-path order (directories by path, files by path, mode and
// content).
func hashTree(root string) (string, int64, error) {
	var rels []string
	var size int64
	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		if rel == "." {
			return nil
		}
		rels = append(rels, rel)
		if !d.IsDir() {
			info, err := d.Info()
			if err != nil {
				return err
			}
			size += info.Size()
		}
		return nil
	})
	if err != nil {
		return "", 0, fmt.Errorf("snapshots: scan tree: %w", err)
	}
	sort.Strings(rels)
	h := sha256.New()
	for _, rel := range rels {
		full := filepath.Join(root, rel)
		st, err := os.Stat(full)
		if err != nil {
			return "", 0, fmt.Errorf("snapshots: stat %s: %w", rel, err)
		}
		if st.IsDir() {
			fmt.Fprintf(h, "d\x00%s\x00", rel)
			continue
		}
		fmt.Fprintf(h, "f\x00%s\x00%o\x00%d\x00", rel, st.Mode().Perm(), st.Size())
		f, err := os.Open(full)
		if err != nil {
			return "", 0, fmt.Errorf("snapshots: open %s: %w", rel, err)
		}
		if _, err := io.Copy(h, f); err != nil {
			f.Close()
			return "", 0, fmt.Errorf("snapshots: hash %s: %w", rel, err)
		}
		f.Close()
	}
	return hex.EncodeToString(h.Sum(nil)), size, nil
}

// copyTree replicates src into dst, returning the copied file byte
// size and preserving file modes.
func copyTree(src, dst string) (int64, error) {
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
		target := filepath.Join(dst, rel)
		if d.IsDir() {
			return os.MkdirAll(target, 0o755)
		}
		n, err := copyFile(path, target)
		size += n
		return err
	})
	if err != nil {
		return 0, fmt.Errorf("snapshots: copy tree: %w", err)
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

// Create captures the current state of the volume. When the content is
// unchanged, the existing snapshot is returned (content-addressed
// dedupe).
func (m *LocalManager) Create(ctx context.Context, workspaceID, volumeID, name string) (types.Snapshot, error) {
	if err := ctx.Err(); err != nil {
		return types.Snapshot{}, err
	}
	workspaceID, err := cleanID("workspace", workspaceID)
	if err != nil {
		return types.Snapshot{}, err
	}
	volumeID, err = cleanID("volume", volumeID)
	if err != nil {
		return types.Snapshot{}, err
	}
	src := m.volumeData(workspaceID, volumeID)
	if st, err := os.Stat(src); err != nil || !st.IsDir() {
		return types.Snapshot{}, fmt.Errorf("%w: %s in workspace %s", ErrVolumeNotFound, volumeID, workspaceID)
	}
	sum, size, err := hashTree(src)
	if err != nil {
		return types.Snapshot{}, err
	}
	id := "snap-" + sum[:16]
	snap, err := types.NewSnapshot(id, workspaceID, volumeID, name)
	if err != nil {
		return types.Snapshot{}, err
	}
	snap.SizeBytes = size

	m.mu.Lock()
	defer m.mu.Unlock()
	if meta, err := m.load(workspaceID, id); err == nil {
		return meta.Snapshot, nil
	} else if !errors.Is(err, ErrSnapshotNotFound) {
		return types.Snapshot{}, err
	}
	var rnd [4]byte
	if _, err := rand.Read(rnd[:]); err != nil {
		return types.Snapshot{}, fmt.Errorf("snapshots: stage snapshot: %w", err)
	}
	staging := filepath.Join(m.root, workspaceID, fmt.Sprintf(".staging-%s-%x", id, rnd))
	if err := os.MkdirAll(filepath.Join(staging, "data"), 0o755); err != nil {
		return types.Snapshot{}, fmt.Errorf("snapshots: stage snapshot: %w", err)
	}
	if _, err := copyTree(src, filepath.Join(staging, "data")); err != nil {
		os.RemoveAll(staging)
		return types.Snapshot{}, err
	}
	raw, err := json.Marshal(snapshotMeta{Snapshot: snap, TreeHash: sum})
	if err != nil {
		os.RemoveAll(staging)
		return types.Snapshot{}, fmt.Errorf("snapshots: encode meta: %w", err)
	}
	if err := os.WriteFile(filepath.Join(staging, "meta.json"), raw, 0o644); err != nil {
		os.RemoveAll(staging)
		return types.Snapshot{}, fmt.Errorf("snapshots: write meta: %w", err)
	}
	if err := os.Rename(staging, m.snapDir(workspaceID, id)); err != nil {
		os.RemoveAll(staging)
		return types.Snapshot{}, fmt.Errorf("snapshots: publish snapshot: %w", err)
	}
	return snap, nil
}

// List returns every snapshot of the workspace, ordered by creation
// time then id.
func (m *LocalManager) List(ctx context.Context, workspaceID string) ([]types.Snapshot, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	workspaceID, err := cleanID("workspace", workspaceID)
	if err != nil {
		return nil, err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	entries, err := os.ReadDir(filepath.Join(m.root, workspaceID))
	if err != nil {
		if os.IsNotExist(err) {
			return []types.Snapshot{}, nil
		}
		return nil, fmt.Errorf("snapshots: list: %w", err)
	}
	out := []types.Snapshot{}
	for _, e := range entries {
		if !e.IsDir() || strings.HasPrefix(e.Name(), ".staging-") {
			continue
		}
		meta, err := m.load(workspaceID, e.Name())
		if err != nil {
			return nil, err
		}
		out = append(out, meta.Snapshot)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].CreatedAt.Equal(out[j].CreatedAt) {
			return out[i].ID < out[j].ID
		}
		return out[i].CreatedAt.Before(out[j].CreatedAt)
	})
	return out, nil
}

// Get returns one snapshot by id.
func (m *LocalManager) Get(ctx context.Context, workspaceID, snapshotID string) (types.Snapshot, error) {
	if err := ctx.Err(); err != nil {
		return types.Snapshot{}, err
	}
	workspaceID, err := cleanID("workspace", workspaceID)
	if err != nil {
		return types.Snapshot{}, err
	}
	snapshotID, err = cleanID("snapshot", snapshotID)
	if err != nil {
		return types.Snapshot{}, err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	meta, err := m.load(workspaceID, snapshotID)
	if err != nil {
		return types.Snapshot{}, err
	}
	return meta.Snapshot, nil
}

// Delete removes a snapshot. Volume data is untouched.
func (m *LocalManager) Delete(ctx context.Context, workspaceID, snapshotID string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	workspaceID, err := cleanID("workspace", workspaceID)
	if err != nil {
		return err
	}
	snapshotID, err = cleanID("snapshot", snapshotID)
	if err != nil {
		return err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	dir := m.snapDir(workspaceID, snapshotID)
	if _, err := os.Stat(filepath.Join(dir, "meta.json")); err != nil {
		if os.IsNotExist(err) {
			return fmt.Errorf("%w: %s in workspace %s", ErrSnapshotNotFound, snapshotID, workspaceID)
		}
		return fmt.Errorf("snapshots: stat snapshot: %w", err)
	}
	return os.RemoveAll(dir)
}

// Restore replaces the snapshot volume content with the captured
// state. The copy is always a full copy, never hardlinks, so later
// live writes cannot corrupt the snapshot.
func (m *LocalManager) Restore(ctx context.Context, workspaceID, snapshotID string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	workspaceID, err := cleanID("workspace", workspaceID)
	if err != nil {
		return err
	}
	snapshotID, err = cleanID("snapshot", snapshotID)
	if err != nil {
		return err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	meta, err := m.load(workspaceID, snapshotID)
	if err != nil {
		return err
	}
	dst := m.volumeData(workspaceID, meta.Snapshot.VolumeID)
	if st, err := os.Stat(dst); err != nil || !st.IsDir() {
		return fmt.Errorf("%w: %s in workspace %s", ErrVolumeNotFound, meta.Snapshot.VolumeID, workspaceID)
	}
	entries, err := os.ReadDir(dst)
	if err != nil {
		return fmt.Errorf("snapshots: list volume: %w", err)
	}
	for _, e := range entries {
		if err := os.RemoveAll(filepath.Join(dst, e.Name())); err != nil {
			return fmt.Errorf("snapshots: clear volume: %w", err)
		}
	}
	_, err = copyTree(filepath.Join(m.snapDir(workspaceID, snapshotID), "data"), dst)
	return err
}
