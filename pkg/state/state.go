package state

import "time"

// Volume represents a btrfs subvolume used as a CSI volume.
type Volume struct {
	// ID is the unique identifier for the volume.
	ID string
	// Name is the CSI volume name (PVC name).
	Name string
	// CapacityBytes is the requested capacity in bytes.
	CapacityBytes int64
	// SubvolumePath is the absolute path to the btrfs subvolume.
	SubvolumePath string
	// SourceSnapID is the snapshot ID if this volume was created from a snapshot.
	SourceSnapID string
	// SourceVolID is the volume ID if this volume was cloned from another volume.
	SourceVolID string
	// NodeID is the node where the volume was created.
	NodeID string
}

// Snapshot represents a readonly btrfs snapshot of a volume.
type Snapshot struct {
	// ID is the unique identifier for the snapshot.
	ID string
	// Name is the CSI snapshot name.
	Name string
	// SourceVolID is the volume ID that was snapshotted.
	SourceVolID string
	// SnapshotPath is the absolute path to the btrfs snapshot.
	SnapshotPath string
	// CreatedAt is the time the snapshot was created.
	CreatedAt time.Time
	// SizeBytes is the size of the snapshot in bytes.
	SizeBytes int64
	// ReadyToUse indicates whether the snapshot is ready to be used.
	ReadyToUse bool
}

// Store abstracts volume and snapshot metadata persistence.
// Implementations must be safe for concurrent use.
type Store interface {
	// GetVolume returns the volume with the given ID, or nil and false if not found.
	GetVolume(id string) (*Volume, bool)

	// GetVolumeByName returns the volume with the given name, or nil and false if not found.
	GetVolumeByName(name string) (*Volume, bool)

	// ListVolumes returns all volumes in the store.
	ListVolumes() []*Volume

	// SaveVolume persists the volume. If a volume with the same ID already exists,
	// it is overwritten.
	SaveVolume(volume *Volume) error

	// DeleteVolume removes the volume with the given ID.
	// It is not an error to delete a volume that does not exist.
	DeleteVolume(id string) error

	// GetSnapshot returns the snapshot with the given ID, or nil and false if not found.
	GetSnapshot(id string) (*Snapshot, bool)

	// GetSnapshotByName returns the snapshot with the given name, or nil and false if not found.
	GetSnapshotByName(name string) (*Snapshot, bool)

	// ListSnapshots returns all snapshots in the store.
	ListSnapshots() []*Snapshot

	// SaveSnapshot persists the snapshot. If a snapshot with the same ID already exists,
	// it is overwritten.
	SaveSnapshot(snapshot *Snapshot) error

	// DeleteSnapshot removes the snapshot with the given ID.
	// It is not an error to delete a snapshot that does not exist.
	DeleteSnapshot(id string) error
}
