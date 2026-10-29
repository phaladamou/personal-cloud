package snapshots

import (
	"context"
	"fmt"
	"sort"
	"sync"

	"github.com/phaladamou/personal-cloud/packages/types"
)

// FakeManager is an in-memory SnapshotManager for tests.
//
// It stores snapshot metadata only: Restore validates that the
// snapshot exists but copies no data. Snapshot ids are deterministic
// (fake-snap-1, fake-snap-2, ...). The zero value is ready to use.
type FakeManager struct {
	mu    sync.Mutex
	seq   int
	snaps map[string]map[string]types.Snapshot
}

var _ SnapshotManager = (*FakeManager)(nil)

func (f *FakeManager) ensureLocked() {
	if f.snaps == nil {
		f.snaps = make(map[string]map[string]types.Snapshot)
	}
}

// Create records a snapshot of the volume.
func (f *FakeManager) Create(ctx context.Context, workspaceID, volumeID, name string) (types.Snapshot, error) {
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
	f.mu.Lock()
	defer f.mu.Unlock()
	f.ensureLocked()
	f.seq++
	snap, err := types.NewSnapshot(fmt.Sprintf("fake-snap-%d", f.seq), workspaceID, volumeID, name)
	if err != nil {
		return types.Snapshot{}, err
	}
	if f.snaps[workspaceID] == nil {
		f.snaps[workspaceID] = make(map[string]types.Snapshot)
	}
	f.snaps[workspaceID][snap.ID] = snap
	return snap, nil
}

// lookup returns the snapshot; the caller must hold f.mu.
func (f *FakeManager) lookup(workspaceID, snapshotID string) (types.Snapshot, error) {
	snap, ok := f.snaps[workspaceID][snapshotID]
	if !ok {
		return types.Snapshot{}, fmt.Errorf("%w: %s in workspace %s", ErrSnapshotNotFound, snapshotID, workspaceID)
	}
	return snap, nil
}

// List returns every snapshot of the workspace, ordered by creation
// time then id.
func (f *FakeManager) List(ctx context.Context, workspaceID string) ([]types.Snapshot, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	workspaceID, err := cleanID("workspace", workspaceID)
	if err != nil {
		return nil, err
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	out := []types.Snapshot{}
	for _, snap := range f.snaps[workspaceID] {
		out = append(out, snap)
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
func (f *FakeManager) Get(ctx context.Context, workspaceID, snapshotID string) (types.Snapshot, error) {
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
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.lookup(workspaceID, snapshotID)
}

// Delete removes a snapshot.
func (f *FakeManager) Delete(ctx context.Context, workspaceID, snapshotID string) error {
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
	f.mu.Lock()
	defer f.mu.Unlock()
	if _, err := f.lookup(workspaceID, snapshotID); err != nil {
		return err
	}
	delete(f.snaps[workspaceID], snapshotID)
	return nil
}

// Restore validates that the snapshot exists. It copies no data; use
// LocalManager for restore-correctness tests.
func (f *FakeManager) Restore(ctx context.Context, workspaceID, snapshotID string) error {
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
	f.mu.Lock()
	defer f.mu.Unlock()
	_, err = f.lookup(workspaceID, snapshotID)
	return err
}
