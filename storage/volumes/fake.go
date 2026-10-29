package volumes

import (
	"context"
	"fmt"
	"sort"
	"sync"

	"github.com/phaladamou/personal-cloud/packages/types"
)

// FakeManager is an in-memory VolumeManager for tests.
//
// Volume ids are deterministic (fake-vol-1, fake-vol-2, ...). The zero
// value is ready to use.
type FakeManager struct {
	mu       sync.Mutex
	seq      int
	volumes  map[string]map[string]types.Volume
	attached map[string]map[string]bool
}

var _ VolumeManager = (*FakeManager)(nil)

func (f *FakeManager) ensureLocked() {
	if f.volumes == nil {
		f.volumes = make(map[string]map[string]types.Volume)
	}
	if f.attached == nil {
		f.attached = make(map[string]map[string]bool)
	}
}

// Create provisions a new detached volume in the workspace.
func (f *FakeManager) Create(ctx context.Context, workspaceID, name string, sizeGB int64, mountPath string) (types.Volume, error) {
	if err := ctx.Err(); err != nil {
		return types.Volume{}, err
	}
	workspaceID, err := cleanID("workspace", workspaceID)
	if err != nil {
		return types.Volume{}, err
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	f.ensureLocked()
	f.seq++
	vol, err := types.NewVolume(fmt.Sprintf("fake-vol-%d", f.seq), workspaceID, name, sizeGB, mountPath)
	if err != nil {
		return types.Volume{}, err
	}
	if f.volumes[workspaceID] == nil {
		f.volumes[workspaceID] = make(map[string]types.Volume)
	}
	f.volumes[workspaceID][vol.ID] = vol
	return vol, nil
}

// lookup returns the volume; the caller must hold f.mu.
func (f *FakeManager) lookup(workspaceID, volumeID string) (types.Volume, error) {
	vol, ok := f.volumes[workspaceID][volumeID]
	if !ok {
		return types.Volume{}, fmt.Errorf("%w: %s in workspace %s", ErrVolumeNotFound, volumeID, workspaceID)
	}
	return vol, nil
}

// setAttached flips the attach flag.
func (f *FakeManager) setAttached(ctx context.Context, workspaceID, volumeID string, attached bool) error {
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
	f.mu.Lock()
	defer f.mu.Unlock()
	f.ensureLocked()
	if _, err := f.lookup(workspaceID, volumeID); err != nil {
		return err
	}
	if f.attached[workspaceID] == nil {
		f.attached[workspaceID] = make(map[string]bool)
	}
	f.attached[workspaceID][volumeID] = attached
	return nil
}

// Attach marks the volume as attached. It is idempotent.
func (f *FakeManager) Attach(ctx context.Context, workspaceID, volumeID string) error {
	return f.setAttached(ctx, workspaceID, volumeID, true)
}

// Detach marks the volume as detached. It is idempotent.
func (f *FakeManager) Detach(ctx context.Context, workspaceID, volumeID string) error {
	return f.setAttached(ctx, workspaceID, volumeID, false)
}

// Attached reports whether the volume is currently attached.
func (f *FakeManager) Attached(ctx context.Context, workspaceID, volumeID string) (bool, error) {
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
	f.mu.Lock()
	defer f.mu.Unlock()
	if _, err := f.lookup(workspaceID, volumeID); err != nil {
		return false, err
	}
	return f.attached[workspaceID][volumeID], nil
}

// Delete removes the volume.
func (f *FakeManager) Delete(ctx context.Context, workspaceID, volumeID string) error {
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
	f.mu.Lock()
	defer f.mu.Unlock()
	if _, err := f.lookup(workspaceID, volumeID); err != nil {
		return err
	}
	delete(f.volumes[workspaceID], volumeID)
	delete(f.attached[workspaceID], volumeID)
	return nil
}

// Inspect returns the volume metadata.
func (f *FakeManager) Inspect(ctx context.Context, workspaceID, volumeID string) (types.Volume, error) {
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
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.lookup(workspaceID, volumeID)
}

// List returns every volume of the workspace, ordered by id.
func (f *FakeManager) List(ctx context.Context, workspaceID string) ([]types.Volume, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	workspaceID, err := cleanID("workspace", workspaceID)
	if err != nil {
		return nil, err
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	out := []types.Volume{}
	for _, vol := range f.volumes[workspaceID] {
		out = append(out, vol)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out, nil
}
