package driver

import (
	"github.com/guru/btrfs-csi/pkg/btrfs"
	"github.com/guru/btrfs-csi/pkg/state"
)

const testRootPath = "/tmp/btrfs-csi-test"

// memStore is a simple in-memory implementation of state.Store for testing.
// dir is the basePath this store represents; it is hydrated onto returned values.
type memStore struct {
	dir       string
	volumes   map[string]*state.Volume
	snapshots map[string]*state.Snapshot
}

func newMemStore(dir string) *memStore {
	return &memStore{
		dir:       dir,
		volumes:   make(map[string]*state.Volume),
		snapshots: make(map[string]*state.Snapshot),
	}
}

func (s *memStore) GetVolume(id string) (*state.Volume, bool) {
	v, ok := s.volumes[id]
	if !ok {
		return nil, false
	}
	cp := *v
	cp.BasePath = s.dir
	return &cp, true
}

func (s *memStore) GetVolumeByName(name string) (*state.Volume, bool) {
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
	out := make([]*state.Volume, 0, len(s.volumes))
	for _, v := range s.volumes {
		cp := *v
		cp.BasePath = s.dir
		out = append(out, &cp)
	}
	return out
}

func (s *memStore) SaveVolume(volume *state.Volume) error {
	cp := *volume
	s.volumes[volume.ID] = &cp
	return nil
}

func (s *memStore) DeleteVolume(id string) error {
	delete(s.volumes, id)
	return nil
}

func (s *memStore) GetSnapshot(id string) (*state.Snapshot, bool) {
	sn, ok := s.snapshots[id]
	if !ok {
		return nil, false
	}
	cp := *sn
	cp.BasePath = s.dir
	return &cp, true
}

func (s *memStore) GetSnapshotByName(name string) (*state.Snapshot, bool) {
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
	out := make([]*state.Snapshot, 0, len(s.snapshots))
	for _, sn := range s.snapshots {
		cp := *sn
		cp.BasePath = s.dir
		out = append(out, &cp)
	}
	return out
}

func (s *memStore) SaveSnapshot(snapshot *state.Snapshot) error {
	cp := *snapshot
	s.snapshots[snapshot.ID] = &cp
	return nil
}

func (s *memStore) DeleteSnapshot(id string) error {
	delete(s.snapshots, id)
	return nil
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
	return NewDriver(&btrfs.MockManager{}, ms, "test-node", testRootPath)
}

// newTestDriverWithPath creates a Driver with a specific root path for testing.
func newTestDriverWithPath(rootPath string) *Driver {
	ms, _ := newTestMultiStore(rootPath)
	return NewDriver(&btrfs.MockManager{}, ms, "test-node", rootPath)
}

// newTestDriverWithMock creates a Driver and returns the mock and store for assertion in tests.
func newTestDriverWithMock() (*Driver, *btrfs.MockManager, *memStore) {
	mock := &btrfs.MockManager{}
	ms, mem := newTestMultiStore(testRootPath)
	return NewDriver(mock, ms, "test-node", testRootPath), mock, mem
}

// newTestDriverWithMounter creates a Driver with mock btrfs, mock mounter, and in-memory store.
func newTestDriverWithMounter() (*Driver, *btrfs.MockManager, *MockMounter, *memStore) {
	mock := &btrfs.MockManager{}
	mounter := &MockMounter{}
	ms, mem := newTestMultiStore(testRootPath)
	d := NewDriver(mock, ms, "test-node", testRootPath)
	d.mounter = mounter
	return d, mock, mounter, mem
}
