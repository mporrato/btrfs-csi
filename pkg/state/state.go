package state

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"
)

// Path returns the absolute path to the btrfs subvolume for this volume.
func (v *Volume) Path() string {
	return filepath.Join(v.BasePath, "volumes", v.ID)
}

// Path returns the absolute path to the btrfs snapshot for this snapshot.
func (s *Snapshot) Path() string {
	return filepath.Join(s.BasePath, "snapshots", s.ID)
}

// Volume represents a btrfs subvolume used as a CSI volume.
type Volume struct {
	// ID is the unique identifier for the volume.
	ID string
	// Name is the CSI volume name (PVC name).
	Name string
	// CapacityBytes is the requested capacity in bytes.
	CapacityBytes int64
	// BasePath is the root directory under which this volume's subvolume lives.
	// The actual subvolume path is derived via Path().
	BasePath string
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
	// BasePath is the root directory under which this snapshot lives.
	// The actual snapshot path is derived via Path().
	BasePath string
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

// stateData is the JSON-serializable representation of the store's contents.
type stateData struct {
	Volumes   map[string]*Volume   `json:"volumes"`
	Snapshots map[string]*Snapshot `json:"snapshots"`
}

// copyVolume returns a deep copy of a Volume.
func copyVolume(v *Volume) *Volume {
	if v == nil {
		return nil
	}
	cp := *v
	return &cp
}

// copySnapshot returns a deep copy of a Snapshot.
func copySnapshot(s *Snapshot) *Snapshot {
	if s == nil {
		return nil
	}
	cp := *s
	return &cp
}

// validateVolume checks that a Volume is valid for persistence.
func validateVolume(v *Volume) error {
	if v == nil {
		return fmt.Errorf("volume is nil")
	}
	if v.ID == "" {
		return fmt.Errorf("volume ID is empty")
	}
	return nil
}

// validateSnapshot checks that a Snapshot is valid for persistence.
func validateSnapshot(s *Snapshot) error {
	if s == nil {
		return fmt.Errorf("snapshot is nil")
	}
	if s.ID == "" {
		return fmt.Errorf("snapshot ID is empty")
	}
	return nil
}

// FileStore implements Store backed by a JSON file on disk.
type FileStore struct {
	mu   sync.Mutex
	path string
	data stateData
}

// maxStateFileSize is the maximum allowed size for the state file (10MB).
const maxStateFileSize = 10 * 1024 * 1024

// NewFileStore creates a FileStore at the given file path.
// If the file already exists, it loads the existing state.
// Otherwise, it starts with empty state.
func NewFileStore(path string) (*FileStore, error) {
	if path == "" {
		return nil, fmt.Errorf("state file path is empty")
	}

	fs := &FileStore{
		path: path,
		data: stateData{
			Volumes:   make(map[string]*Volume),
			Snapshots: make(map[string]*Snapshot),
		},
	}

	// Try to load existing state file.
	if info, err := os.Stat(path); err == nil {
		if info.Size() > maxStateFileSize {
			return nil, fmt.Errorf("state file size %d exceeds limit %d", info.Size(), maxStateFileSize)
		}
		if err := fs.load(); err != nil {
			return nil, err
		}
	}

	return fs, nil
}

// load reads and unmarshals the state file into fs.data.
// Caller must NOT hold fs.mu.
func (fs *FileStore) load() error {
	raw, err := os.ReadFile(fs.path)
	if err != nil {
		return fmt.Errorf("read state file: %w", err)
	}
	if err := json.Unmarshal(raw, &fs.data); err != nil {
		return fmt.Errorf("unmarshal state: %w", err)
	}
	// Ensure maps are non-nil even if JSON had null/empty entries.
	if fs.data.Volumes == nil {
		fs.data.Volumes = make(map[string]*Volume)
	}
	if fs.data.Snapshots == nil {
		fs.data.Snapshots = make(map[string]*Snapshot)
	}
	return nil
}

// save executes fn under the lock, then persists the state to disk.
// This is the single entry point for all mutations.
func (fs *FileStore) save(fn func()) error {
	fs.mu.Lock()
	defer fs.mu.Unlock()

	fn()
	return fs.persist()
}

// persist writes the current state to disk using atomic write pattern.
// Caller must hold fs.mu.
func (fs *FileStore) persist() error {
	dir := filepath.Dir(fs.path)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return fmt.Errorf("create state directory: %w", err)
	}
	raw, err := json.MarshalIndent(fs.data, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal state: %w", err)
	}

	// Atomic write: use os.CreateTemp for an unpredictable name, then rename.
	tmpFile, err := os.CreateTemp(dir, ".state-*.json.tmp")
	if err != nil {
		return fmt.Errorf("create temp state file: %w", err)
	}
	tmpPath := tmpFile.Name()
	if _, err := tmpFile.Write(raw); err != nil {
		_ = tmpFile.Close()
		_ = os.Remove(tmpPath)
		return fmt.Errorf("write temp state file: %w", err)
	}
	if err := tmpFile.Close(); err != nil {
		_ = os.Remove(tmpPath)
		return fmt.Errorf("close temp state file: %w", err)
	}
	if err := os.Rename(tmpPath, fs.path); err != nil {
		_ = os.Remove(tmpPath)
		return fmt.Errorf("rename state file: %w", err)
	}
	return nil
}

func (fs *FileStore) GetVolume(id string) (*Volume, bool) {
	fs.mu.Lock()
	defer fs.mu.Unlock()
	v, ok := fs.data.Volumes[id]
	return copyVolume(v), ok
}

func (fs *FileStore) GetVolumeByName(name string) (*Volume, bool) {
	fs.mu.Lock()
	defer fs.mu.Unlock()
	for _, v := range fs.data.Volumes {
		if v.Name == name {
			return copyVolume(v), true
		}
	}
	return nil, false
}

func (fs *FileStore) ListVolumes() []*Volume {
	fs.mu.Lock()
	defer fs.mu.Unlock()
	result := make([]*Volume, 0, len(fs.data.Volumes))
	for _, v := range fs.data.Volumes {
		result = append(result, copyVolume(v))
	}
	return result
}

func (fs *FileStore) SaveVolume(volume *Volume) error {
	if err := validateVolume(volume); err != nil {
		return err
	}
	cp := copyVolume(volume)
	return fs.save(func() {
		fs.data.Volumes[cp.ID] = cp
	})
}

func (fs *FileStore) DeleteVolume(id string) error {
	return fs.save(func() {
		delete(fs.data.Volumes, id)
	})
}

func (fs *FileStore) GetSnapshot(id string) (*Snapshot, bool) {
	fs.mu.Lock()
	defer fs.mu.Unlock()
	s, ok := fs.data.Snapshots[id]
	return copySnapshot(s), ok
}

func (fs *FileStore) GetSnapshotByName(name string) (*Snapshot, bool) {
	fs.mu.Lock()
	defer fs.mu.Unlock()
	for _, s := range fs.data.Snapshots {
		if s.Name == name {
			return copySnapshot(s), true
		}
	}
	return nil, false
}

func (fs *FileStore) ListSnapshots() []*Snapshot {
	fs.mu.Lock()
	defer fs.mu.Unlock()
	result := make([]*Snapshot, 0, len(fs.data.Snapshots))
	for _, s := range fs.data.Snapshots {
		result = append(result, copySnapshot(s))
	}
	return result
}

func (fs *FileStore) SaveSnapshot(snapshot *Snapshot) error {
	if err := validateSnapshot(snapshot); err != nil {
		return err
	}
	cp := copySnapshot(snapshot)
	return fs.save(func() {
		fs.data.Snapshots[cp.ID] = cp
	})
}

func (fs *FileStore) DeleteSnapshot(id string) error {
	return fs.save(func() {
		delete(fs.data.Snapshots, id)
	})
}
