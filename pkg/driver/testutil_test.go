package driver

import (
	"github.com/guru/btrfs-csi/pkg/btrfs"
	"github.com/guru/btrfs-csi/pkg/state"
)

// memStore is a simple in-memory implementation of state.Store for testing.
type memStore struct {
	volumes   map[string]*state.Volume
	snapshots map[string]*state.Snapshot
}

func newMemStore() *memStore {
	return &memStore{
		volumes:   make(map[string]*state.Volume),
		snapshots: make(map[string]*state.Snapshot),
	}
}

func (s *memStore) GetVolume(id string) (*state.Volume, bool) {
	v, ok := s.volumes[id]
	return v, ok
}

func (s *memStore) GetVolumeByName(name string) (*state.Volume, bool) {
	for _, v := range s.volumes {
		if v.Name == name {
			return v, true
		}
	}
	return nil, false
}

func (s *memStore) ListVolumes() []*state.Volume {
	out := make([]*state.Volume, 0, len(s.volumes))
	for _, v := range s.volumes {
		out = append(out, v)
	}
	return out
}

func (s *memStore) SaveVolume(volume *state.Volume) error {
	s.volumes[volume.ID] = volume
	return nil
}

func (s *memStore) DeleteVolume(id string) error {
	delete(s.volumes, id)
	return nil
}

func (s *memStore) GetSnapshot(id string) (*state.Snapshot, bool) {
	sn, ok := s.snapshots[id]
	return sn, ok
}

func (s *memStore) GetSnapshotByName(name string) (*state.Snapshot, bool) {
	for _, sn := range s.snapshots {
		if sn.Name == name {
			return sn, true
		}
	}
	return nil, false
}

func (s *memStore) ListSnapshots() []*state.Snapshot {
	out := make([]*state.Snapshot, 0, len(s.snapshots))
	for _, sn := range s.snapshots {
		out = append(out, sn)
	}
	return out
}

func (s *memStore) SaveSnapshot(snapshot *state.Snapshot) error {
	s.snapshots[snapshot.ID] = snapshot
	return nil
}

func (s *memStore) DeleteSnapshot(id string) error {
	delete(s.snapshots, id)
	return nil
}

// newTestDriver creates a Driver wired with a MockManager and in-memory store for testing.
func newTestDriver() *Driver {
	return NewDriver(&btrfs.MockManager{}, newMemStore(), "test-node", "/tmp/btrfs-csi-test")
}

// newTestDriverWithPath creates a Driver with a specific root path for testing.
func newTestDriverWithPath(rootPath string) *Driver {
	return NewDriver(&btrfs.MockManager{}, newMemStore(), "test-node", rootPath)
}
