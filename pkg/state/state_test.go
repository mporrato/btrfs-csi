package state

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// newTestStore creates a FileStore backed by a temporary JSON file.
func newTestStore(t *testing.T) *FileStore {
	t.Helper()
	path := filepath.Join(t.TempDir(), "state.json")
	store, err := NewFileStore(path)
	if err != nil {
		t.Fatalf("NewFileStore: %v", err)
	}
	return store
}

func TestSaveAndGetVolume(t *testing.T) {
	store := newTestStore(t)

	vol := &Volume{
		ID:            "vol-1",
		Name:          "pvc-abc",
		CapacityBytes: 1024 * 1024 * 100,
		SubvolumePath: "/var/lib/btrfs-csi/volumes/vol-1",
		NodeID:        "node-1",
	}

	if err := store.SaveVolume(vol); err != nil {
		t.Fatalf("SaveVolume: %v", err)
	}

	got, ok := store.GetVolume("vol-1")
	if !ok {
		t.Fatal("GetVolume returned false, want true")
	}
	if got.ID != vol.ID {
		t.Errorf("ID = %q, want %q", got.ID, vol.ID)
	}
	if got.Name != vol.Name {
		t.Errorf("Name = %q, want %q", got.Name, vol.Name)
	}
	if got.CapacityBytes != vol.CapacityBytes {
		t.Errorf("CapacityBytes = %d, want %d", got.CapacityBytes, vol.CapacityBytes)
	}
	if got.SubvolumePath != vol.SubvolumePath {
		t.Errorf("SubvolumePath = %q, want %q", got.SubvolumePath, vol.SubvolumePath)
	}
	if got.NodeID != vol.NodeID {
		t.Errorf("NodeID = %q, want %q", got.NodeID, vol.NodeID)
	}
}

func TestGetVolumeByName(t *testing.T) {
	store := newTestStore(t)

	vol := &Volume{
		ID:   "vol-1",
		Name: "pvc-abc",
	}

	if err := store.SaveVolume(vol); err != nil {
		t.Fatalf("SaveVolume: %v", err)
	}

	got, ok := store.GetVolumeByName("pvc-abc")
	if !ok {
		t.Fatal("GetVolumeByName returned false, want true")
	}
	if got.ID != "vol-1" {
		t.Errorf("ID = %q, want %q", got.ID, "vol-1")
	}
	if got.Name != "pvc-abc" {
		t.Errorf("Name = %q, want %q", got.Name, "pvc-abc")
	}
}

func TestGetVolume_NotFound(t *testing.T) {
	store := newTestStore(t)

	got, ok := store.GetVolume("does-not-exist")
	if ok {
		t.Error("GetVolume returned true for unknown ID, want false")
	}
	if got != nil {
		t.Errorf("GetVolume returned %v for unknown ID, want nil", got)
	}
}

func TestDeleteVolume(t *testing.T) {
	store := newTestStore(t)

	vol := &Volume{ID: "vol-1", Name: "pvc-abc"}
	if err := store.SaveVolume(vol); err != nil {
		t.Fatalf("SaveVolume: %v", err)
	}

	if err := store.DeleteVolume("vol-1"); err != nil {
		t.Fatalf("DeleteVolume: %v", err)
	}

	got, ok := store.GetVolume("vol-1")
	if ok {
		t.Error("GetVolume returned true after delete, want false")
	}
	if got != nil {
		t.Errorf("GetVolume returned %v after delete, want nil", got)
	}
}

func TestListVolumes(t *testing.T) {
	store := newTestStore(t)

	volumes := []*Volume{
		{ID: "vol-1", Name: "pvc-one"},
		{ID: "vol-2", Name: "pvc-two"},
		{ID: "vol-3", Name: "pvc-three"},
	}

	for _, v := range volumes {
		if err := store.SaveVolume(v); err != nil {
			t.Fatalf("SaveVolume(%s): %v", v.ID, err)
		}
	}

	got := store.ListVolumes()
	if len(got) != 3 {
		t.Fatalf("ListVolumes returned %d volumes, want 3", len(got))
	}

	// Build a set of IDs for easy lookup.
	ids := make(map[string]bool, len(got))
	for _, v := range got {
		ids[v.ID] = true
	}
	for _, v := range volumes {
		if !ids[v.ID] {
			t.Errorf("ListVolumes missing volume %s", v.ID)
		}
	}
}

func TestVolumeOverwrite(t *testing.T) {
	store := newTestStore(t)

	original := &Volume{
		ID:            "vol-1",
		Name:          "pvc-old",
		CapacityBytes: 100,
	}
	if err := store.SaveVolume(original); err != nil {
		t.Fatalf("SaveVolume original: %v", err)
	}

	updated := &Volume{
		ID:            "vol-1",
		Name:          "pvc-new",
		CapacityBytes: 200,
	}
	if err := store.SaveVolume(updated); err != nil {
		t.Fatalf("SaveVolume updated: %v", err)
	}

	got, ok := store.GetVolume("vol-1")
	if !ok {
		t.Fatal("GetVolume returned false after overwrite, want true")
	}
	if got.Name != "pvc-new" {
		t.Errorf("Name = %q, want %q", got.Name, "pvc-new")
	}
	if got.CapacityBytes != 200 {
		t.Errorf("CapacityBytes = %d, want %d", got.CapacityBytes, 200)
	}
}

func TestSaveAndGetSnapshot(t *testing.T) {
	store := newTestStore(t)

	snap := &Snapshot{
		ID:           "snap-1",
		Name:         "backup-1",
		SourceVolID:  "vol-1",
		SnapshotPath: "/var/lib/btrfs-csi/snapshots/snap-1",
		CreatedAt:    time.Date(2025, 1, 15, 12, 0, 0, 0, time.UTC),
		SizeBytes:    512,
		ReadyToUse:   true,
	}

	if err := store.SaveSnapshot(snap); err != nil {
		t.Fatalf("SaveSnapshot: %v", err)
	}

	got, ok := store.GetSnapshot("snap-1")
	if !ok {
		t.Fatal("GetSnapshot returned false, want true")
	}
	if got.ID != snap.ID {
		t.Errorf("ID = %q, want %q", got.ID, snap.ID)
	}
	if got.Name != snap.Name {
		t.Errorf("Name = %q, want %q", got.Name, snap.Name)
	}
	if got.SourceVolID != snap.SourceVolID {
		t.Errorf("SourceVolID = %q, want %q", got.SourceVolID, snap.SourceVolID)
	}
	if got.SnapshotPath != snap.SnapshotPath {
		t.Errorf("SnapshotPath = %q, want %q", got.SnapshotPath, snap.SnapshotPath)
	}
	if !got.CreatedAt.Equal(snap.CreatedAt) {
		t.Errorf("CreatedAt = %v, want %v", got.CreatedAt, snap.CreatedAt)
	}
	if got.SizeBytes != snap.SizeBytes {
		t.Errorf("SizeBytes = %d, want %d", got.SizeBytes, snap.SizeBytes)
	}
	if got.ReadyToUse != snap.ReadyToUse {
		t.Errorf("ReadyToUse = %v, want %v", got.ReadyToUse, snap.ReadyToUse)
	}
}

func TestGetSnapshotByName(t *testing.T) {
	store := newTestStore(t)

	snap := &Snapshot{
		ID:   "snap-1",
		Name: "backup-1",
	}
	if err := store.SaveSnapshot(snap); err != nil {
		t.Fatalf("SaveSnapshot: %v", err)
	}

	got, ok := store.GetSnapshotByName("backup-1")
	if !ok {
		t.Fatal("GetSnapshotByName returned false, want true")
	}
	if got.ID != "snap-1" {
		t.Errorf("ID = %q, want %q", got.ID, "snap-1")
	}
	if got.Name != "backup-1" {
		t.Errorf("Name = %q, want %q", got.Name, "backup-1")
	}
}

func TestDeleteSnapshot(t *testing.T) {
	store := newTestStore(t)

	snap := &Snapshot{ID: "snap-1", Name: "backup-1"}
	if err := store.SaveSnapshot(snap); err != nil {
		t.Fatalf("SaveSnapshot: %v", err)
	}

	if err := store.DeleteSnapshot("snap-1"); err != nil {
		t.Fatalf("DeleteSnapshot: %v", err)
	}

	got, ok := store.GetSnapshot("snap-1")
	if ok {
		t.Error("GetSnapshot returned true after delete, want false")
	}
	if got != nil {
		t.Errorf("GetSnapshot returned %v after delete, want nil", got)
	}
}

func TestListSnapshots(t *testing.T) {
	store := newTestStore(t)

	snapshots := []*Snapshot{
		{ID: "snap-1", Name: "backup-1"},
		{ID: "snap-2", Name: "backup-2"},
		{ID: "snap-3", Name: "backup-3"},
	}

	for _, s := range snapshots {
		if err := store.SaveSnapshot(s); err != nil {
			t.Fatalf("SaveSnapshot(%s): %v", s.ID, err)
		}
	}

	got := store.ListSnapshots()
	if len(got) != 3 {
		t.Fatalf("ListSnapshots returned %d snapshots, want 3", len(got))
	}

	ids := make(map[string]bool, len(got))
	for _, s := range got {
		ids[s.ID] = true
	}
	for _, s := range snapshots {
		if !ids[s.ID] {
			t.Errorf("ListSnapshots missing snapshot %s", s.ID)
		}
	}
}

func TestPersistence(t *testing.T) {
	path := filepath.Join(t.TempDir(), "state.json")

	// Create store, save data.
	store1, err := NewFileStore(path)
	if err != nil {
		t.Fatalf("NewFileStore: %v", err)
	}

	vol := &Volume{ID: "vol-1", Name: "pvc-abc", CapacityBytes: 4096}
	if err := store1.SaveVolume(vol); err != nil {
		t.Fatalf("SaveVolume: %v", err)
	}

	snap := &Snapshot{ID: "snap-1", Name: "backup-1", SourceVolID: "vol-1"}
	if err := store1.SaveSnapshot(snap); err != nil {
		t.Fatalf("SaveSnapshot: %v", err)
	}

	// Open a new FileStore from the same path — data must survive.
	store2, err := NewFileStore(path)
	if err != nil {
		t.Fatalf("NewFileStore (reopen): %v", err)
	}

	gotVol, ok := store2.GetVolume("vol-1")
	if !ok {
		t.Fatal("GetVolume after reopen returned false, want true")
	}
	if gotVol.Name != "pvc-abc" {
		t.Errorf("volume Name after reopen = %q, want %q", gotVol.Name, "pvc-abc")
	}
	if gotVol.CapacityBytes != 4096 {
		t.Errorf("volume CapacityBytes after reopen = %d, want %d", gotVol.CapacityBytes, 4096)
	}

	gotSnap, ok := store2.GetSnapshot("snap-1")
	if !ok {
		t.Fatal("GetSnapshot after reopen returned false, want true")
	}
	if gotSnap.Name != "backup-1" {
		t.Errorf("snapshot Name after reopen = %q, want %q", gotSnap.Name, "backup-1")
	}
	if gotSnap.SourceVolID != "vol-1" {
		t.Errorf("snapshot SourceVolID after reopen = %q, want %q", gotSnap.SourceVolID, "vol-1")
	}
}

func TestFilePermissions(t *testing.T) {
	// Create a nested directory to test directory creation
	baseDir := t.TempDir()
	nestedDir := filepath.Join(baseDir, "nested", "dir")
	path := filepath.Join(nestedDir, "state.json")

	store, err := NewFileStore(path)
	if err != nil {
		t.Fatalf("NewFileStore: %v", err)
	}

	vol := &Volume{ID: "vol-1", Name: "pvc-abc"}
	if err := store.SaveVolume(vol); err != nil {
		t.Fatalf("SaveVolume: %v", err)
	}

	// Check file permissions (should be 0o600)
	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("Stat: %v", err)
	}
	if perm := info.Mode().Perm(); perm != 0o600 {
		t.Errorf("file permissions = %o, want 0o600", perm)
	}

	// Check directory permissions (should be 0o700)
	dirInfo, err := os.Stat(nestedDir)
	if err != nil {
		t.Fatalf("Stat dir: %v", err)
	}
	if perm := dirInfo.Mode().Perm(); perm != 0o700 {
		t.Errorf("directory permissions = %o, want 0o700", perm)
	}
}

func TestAtomicWrite(t *testing.T) {
	path := filepath.Join(t.TempDir(), "state.json")
	store, err := NewFileStore(path)
	if err != nil {
		t.Fatalf("NewFileStore: %v", err)
	}

	// Save initial data
	vol1 := &Volume{ID: "vol-1", Name: "pvc-abc"}
	if err := store.SaveVolume(vol1); err != nil {
		t.Fatalf("SaveVolume: %v", err)
	}

	// Verify no temp files left behind
	matches, err := filepath.Glob(path + "*")
	if err != nil {
		t.Fatalf("Glob: %v", err)
	}
	if len(matches) != 1 {
		t.Errorf("found %d files matching %s*, want 1: %v", len(matches), path, matches)
	}
}

func TestStateFileSizeLimit(t *testing.T) {
	path := filepath.Join(t.TempDir(), "state.json")

	// Create a file larger than 10MB
	largeData := make([]byte, 11*1024*1024) // 11MB
	for i := range largeData {
		largeData[i] = 'x'
	}
	if err := os.WriteFile(path, largeData, 0o600); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	// NewFileStore should fail with size limit error
	_, err := NewFileStore(path)
	if err == nil {
		t.Fatal("NewFileStore should fail for oversized file")
	}
	if !strings.Contains(err.Error(), "exceeds limit") {
		t.Errorf("error should mention exceeds limit, got: %v", err)
	}
}

func TestSaveVolume_NilCheck(t *testing.T) {
	store := newTestStore(t)

	err := store.SaveVolume(nil)
	if err == nil {
		t.Fatal("SaveVolume(nil) should return error")
	}
	if !strings.Contains(err.Error(), "nil") {
		t.Errorf("error should mention nil, got: %v", err)
	}
}

func TestSaveSnapshot_NilCheck(t *testing.T) {
	store := newTestStore(t)

	err := store.SaveSnapshot(nil)
	if err == nil {
		t.Fatal("SaveSnapshot(nil) should return error")
	}
	if !strings.Contains(err.Error(), "nil") {
		t.Errorf("error should mention nil, got: %v", err)
	}
}

func TestSaveVolume_EmptyID(t *testing.T) {
	store := newTestStore(t)

	vol := &Volume{ID: "", Name: "pvc-abc"}
	err := store.SaveVolume(vol)
	if err == nil {
		t.Fatal("SaveVolume with empty ID should return error")
	}
	if !strings.Contains(err.Error(), "empty") {
		t.Errorf("error should mention empty, got: %v", err)
	}
}

func TestSaveSnapshot_EmptyID(t *testing.T) {
	store := newTestStore(t)

	snap := &Snapshot{ID: "", Name: "backup-1"}
	err := store.SaveSnapshot(snap)
	if err == nil {
		t.Fatal("SaveSnapshot with empty ID should return error")
	}
	if !strings.Contains(err.Error(), "empty") {
		t.Errorf("error should mention empty, got: %v", err)
	}
}

func TestNewFileStore_EmptyPath(t *testing.T) {
	_, err := NewFileStore("")
	if err == nil {
		t.Fatal("NewFileStore with empty path should return error")
	}
	if !strings.Contains(err.Error(), "empty") {
		t.Errorf("error should mention empty, got: %v", err)
	}
}

func TestGetVolume_ReturnsDeepCopy(t *testing.T) {
	store := newTestStore(t)

	original := &Volume{
		ID:            "vol-1",
		Name:          "pvc-abc",
		CapacityBytes: 1024,
	}
	if err := store.SaveVolume(original); err != nil {
		t.Fatalf("SaveVolume: %v", err)
	}

	// Get volume and modify it
	got, ok := store.GetVolume("vol-1")
	if !ok {
		t.Fatal("GetVolume returned false, want true")
	}
	got.Name = "modified"

	// Get again - should not be modified
	got2, ok := store.GetVolume("vol-1")
	if !ok {
		t.Fatal("GetVolume returned false, want true")
	}
	if got2.Name == "modified" {
		t.Error("GetVolume returned a reference to internal state, want deep copy")
	}
}

func TestGetSnapshot_ReturnsDeepCopy(t *testing.T) {
	store := newTestStore(t)

	original := &Snapshot{
		ID:   "snap-1",
		Name: "backup-1",
	}
	if err := store.SaveSnapshot(original); err != nil {
		t.Fatalf("SaveSnapshot: %v", err)
	}

	// Get snapshot and modify it
	got, ok := store.GetSnapshot("snap-1")
	if !ok {
		t.Fatal("GetSnapshot returned false, want true")
	}
	got.Name = "modified"

	// Get again - should not be modified
	got2, ok := store.GetSnapshot("snap-1")
	if !ok {
		t.Fatal("GetSnapshot returned false, want true")
	}
	if got2.Name == "modified" {
		t.Error("GetSnapshot returned a reference to internal state, want deep copy")
	}
}

func TestListVolumes_ReturnsDeepCopies(t *testing.T) {
	store := newTestStore(t)

	vol := &Volume{ID: "vol-1", Name: "pvc-abc"}
	if err := store.SaveVolume(vol); err != nil {
		t.Fatalf("SaveVolume: %v", err)
	}

	// List and modify
	list := store.ListVolumes()
	if len(list) != 1 {
		t.Fatalf("ListVolumes returned %d volumes, want 1", len(list))
	}
	list[0].Name = "modified"

	// List again - should not be modified
	list2 := store.ListVolumes()
	if list2[0].Name == "modified" {
		t.Error("ListVolumes returned references to internal state, want deep copies")
	}
}

func TestListSnapshots_ReturnsDeepCopies(t *testing.T) {
	store := newTestStore(t)

	snap := &Snapshot{ID: "snap-1", Name: "backup-1"}
	if err := store.SaveSnapshot(snap); err != nil {
		t.Fatalf("SaveSnapshot: %v", err)
	}

	// List and modify
	list := store.ListSnapshots()
	if len(list) != 1 {
		t.Fatalf("ListSnapshots returned %d snapshots, want 1", len(list))
	}
	list[0].Name = "modified"

	// List again - should not be modified
	list2 := store.ListSnapshots()
	if list2[0].Name == "modified" {
		t.Error("ListSnapshots returned references to internal state, want deep copies")
	}
}
