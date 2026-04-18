package driver

import (
	"context"
	"sync"

	"github.com/mporrato/btrfs-csi/pkg/btrfs"
	"github.com/mporrato/btrfs-csi/pkg/state"
)

const testRootPath = "/tmp/btrfs-csi-test"

// memStore is a simple in-memory implementation of state.Store for testing.
// dir is the basePath this store represents; it is hydrated onto returned values.
// All methods are safe for concurrent use.
type memStore struct {
	mu              sync.Mutex
	dir             string
	volumes         map[string]*state.Volume
	snapshots       map[string]*state.Snapshot
	SaveSnapshotErr error // if set, SaveSnapshot returns this error
}

func newMemStore(dir string) *memStore {
	return &memStore{
		dir:       dir,
		volumes:   make(map[string]*state.Volume),
		snapshots: make(map[string]*state.Snapshot),
	}
}

func (s *memStore) GetVolume(id string) (*state.Volume, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	v, ok := s.volumes[id]
	if !ok {
		return nil, false
	}
	cp := *v
	cp.BasePath = s.dir
	return &cp, true
}

func (s *memStore) GetVolumeByName(name string) (*state.Volume, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, v := range s.volumes {
		if v.Name == name {
			cp := *v
			cp.BasePath = s.dir
			return &cp, true
		}
	}
	return nil, false
}

func (s *memStore) ListVolumes() []*state.Volume {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]*state.Volume, 0, len(s.volumes))
	for _, v := range s.volumes {
		cp := *v
		cp.BasePath = s.dir
		out = append(out, &cp)
	}
	return out
}

func (s *memStore) SaveVolume(volume *state.Volume) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	cp := *volume
	s.volumes[volume.ID] = &cp
	return nil
}

func (s *memStore) DeleteVolume(id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.volumes, id)
	return nil
}

func (s *memStore) GetSnapshot(id string) (*state.Snapshot, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	sn, ok := s.snapshots[id]
	if !ok {
		return nil, false
	}
	cp := *sn
	cp.BasePath = s.dir
	return &cp, true
}

func (s *memStore) GetSnapshotByName(name string) (*state.Snapshot, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, sn := range s.snapshots {
		if sn.Name == name {
			cp := *sn
			cp.BasePath = s.dir
			return &cp, true
		}
	}
	return nil, false
}

func (s *memStore) ListSnapshots() []*state.Snapshot {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]*state.Snapshot, 0, len(s.snapshots))
	for _, sn := range s.snapshots {
		cp := *sn
		cp.BasePath = s.dir
		out = append(out, &cp)
	}
	return out
}

func (s *memStore) SaveSnapshot(snapshot *state.Snapshot) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.SaveSnapshotErr != nil {
		return s.SaveSnapshotErr
	}
	cp := *snapshot
	s.snapshots[snapshot.ID] = &cp
	return nil
}

func (s *memStore) DeleteSnapshot(id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.snapshots, id)
	return nil
}

func (s *memStore) Dirs() []string {
	return []string{s.dir}
}

func (s *memStore) ReloadPaths(paths []string) {
	// memStore is a single-directory store; reloading is a no-op for testing.
}

// funcManager embeds MockManager and lets individual methods be overridden
// with a closure, useful for recording calls with extra context (e.g. timestamps).
type funcManager struct {
	btrfs.MockManager
	clearStaleQgroups func(ctx context.Context, path string) (int, error)
}

func (f *funcManager) ClearStaleQgroups(ctx context.Context, path string) (int, error) {
	if f.clearStaleQgroups != nil {
		return f.clearStaleQgroups(ctx, path)
	}
	return f.MockManager.ClearStaleQgroups(ctx, path)
}

// newTestMultiStore wraps a memStore in a MultiStore keyed by the given dir.
// The returned MultiStore implements state.Store and routes by BasePath.
func newTestMultiStore(dir string) (*state.MultiStore, *memStore) {
	ms := state.NewMultiStore()
	mem := newMemStore(dir)
	ms.AddStoreForTest(dir, mem)
	return ms, mem
}

// newTestDriver creates a Driver wired with a MockManager and in-memory store for testing.
func newTestDriver() *Driver {
	ms, _ := newTestMultiStore(testRootPath)
	d, err := NewDriver(&btrfs.MockManager{}, ms, "test-node")
	if err != nil {
		panic(err)
	}
	d.SetPools(map[string]string{"default": testRootPath})
	return d
}

// newTestDriverWithPath creates a Driver with a specific base path for testing.
func newTestDriverWithPath(path string) *Driver {
	ms, _ := newTestMultiStore(path)
	d, err := NewDriver(&btrfs.MockManager{IsBtrfsFilesystemResult: true}, ms, "test-node")
	if err != nil {
		panic(err)
	}
	d.SetPools(map[string]string{"default": path})
	return d
}

// newTestDriverWithMock creates a Driver and returns the mock and store for assertion in tests.
func newTestDriverWithMock() (*Driver, *btrfs.MockManager, *memStore) {
	mock := &btrfs.MockManager{}
	ms, mem := newTestMultiStore(testRootPath)
	d, err := NewDriver(mock, ms, "test-node")
	if err != nil {
		panic(err)
	}
	d.SetPools(map[string]string{"default": testRootPath})
	return d, mock, mem
}

// newTestDriverWithMounter creates a Driver with mock btrfs, mock mounter, and in-memory store.
func newTestDriverWithMounter() (*Driver, *btrfs.MockManager, *MockMounter, *memStore) {
	mock := &btrfs.MockManager{}
	mounter := &MockMounter{}
	ms, mem := newTestMultiStore(testRootPath)
	d, err := NewDriver(mock, ms, "test-node")
	if err != nil {
		panic(err)
	}
	d.SetPools(map[string]string{"default": testRootPath})
	d.mounter = mounter
	return d, mock, mounter, mem
}
