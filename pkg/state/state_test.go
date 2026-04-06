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

func TestVolumePath(t *testing.T) {
	vol := &Volume{ID: "abc123", BasePath: "/var/lib/btrfs-csi"}
	want := "/var/lib/btrfs-csi/volumes/abc123"
	if got := vol.Path(); got != want {
		t.Errorf("Path() = %q, want %q", got, want)
	}
}

func TestSnapshotPath(t *testing.T) {
	snap := &Snapshot{ID: "snap-1", BasePath: "/mnt/data"}
	want := "/mnt/data/snapshots/snap-1"
	if got := snap.Path(); got != want {
		t.Errorf("Path() = %q, want %q", got, want)
	}
}

func TestSaveAndGetVolume(t *testing.T) {
	store := newTestStore(t)
	dir := store.Dir()

	vol := &Volume{
		ID:            "vol-1",
		Name:          "pvc-abc",
		CapacityBytes: 1024 * 1024 * 100,
		BasePath:      dir,
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
	if got.BasePath != dir {
		t.Errorf("BasePath = %q, want %q", got.BasePath, dir)
	}
	if got.Path() != vol.Path() {
		t.Errorf("Path() = %q, want %q", got.Path(), vol.Path())
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

func TestGetVolumeByName_AfterDelete(t *testing.T) {
	store := newTestStore(t)
	if err := store.SaveVolume(&Volume{ID: "vol-1", Name: "pvc-abc"}); err != nil {
		t.Fatalf("SaveVolume: %v", err)
	}
	if err := store.DeleteVolume("vol-1"); err != nil {
		t.Fatalf("DeleteVolume: %v", err)
	}
	if _, ok := store.GetVolumeByName("pvc-abc"); ok {
		t.Error("GetVolumeByName returned true after delete, want false")
	}
}

func TestGetVolumeByName_AfterRename(t *testing.T) {
	store := newTestStore(t)
	if err := store.SaveVolume(&Volume{ID: "vol-1", Name: "old-name"}); err != nil {
		t.Fatalf("SaveVolume: %v", err)
	}
	if err := store.SaveVolume(&Volume{ID: "vol-1", Name: "new-name"}); err != nil {
		t.Fatalf("SaveVolume (rename): %v", err)
	}
	if _, ok := store.GetVolumeByName("old-name"); ok {
		t.Error("GetVolumeByName found old name after rename")
	}
	got, ok := store.GetVolumeByName("new-name")
	if !ok {
		t.Fatal("GetVolumeByName returned false for new name")
	}
	if got.ID != "vol-1" {
		t.Errorf("ID = %q, want %q", got.ID, "vol-1")
	}
}

func TestGetSnapshotByName_AfterDelete(t *testing.T) {
	store := newTestStore(t)
	if err := store.SaveSnapshot(&Snapshot{ID: "snap-1", Name: "backup-1"}); err != nil {
		t.Fatalf("SaveSnapshot: %v", err)
	}
	if err := store.DeleteSnapshot("snap-1"); err != nil {
		t.Fatalf("DeleteSnapshot: %v", err)
	}
	if _, ok := store.GetSnapshotByName("backup-1"); ok {
		t.Error("GetSnapshotByName returned true after delete, want false")
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
		ID:          "snap-1",
		Name:        "backup-1",
		SourceVolID: "vol-1",
		BasePath:    store.Dir(),
		CreatedAt:   time.Date(2025, 1, 15, 12, 0, 0, 0, time.UTC),
		SizeBytes:   512,
		ReadyToUse:  true,
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
	if got.BasePath != store.Dir() {
		t.Errorf("BasePath = %q, want %q", got.BasePath, store.Dir())
	}
	if got.Path() != snap.Path() {
		t.Errorf("Path() = %q, want %q", got.Path(), snap.Path())
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

func TestSaveVolume_CallerMutationIsolated(t *testing.T) {
	store := newTestStore(t)

	vol := &Volume{ID: "vol-1", Name: "original"}
	if err := store.SaveVolume(vol); err != nil {
		t.Fatalf("SaveVolume: %v", err)
	}

	// Mutate the original after saving — store should not reflect the change.
	vol.Name = "mutated"

	got, ok := store.GetVolume("vol-1")
	if !ok {
		t.Fatal("GetVolume returned false after SaveVolume")
	}
	if got.Name != "original" {
		t.Errorf("store returned mutated name %q; SaveVolume must copy the input", got.Name)
	}
}

// --- FileStore.Dir tests ---

func TestFileStore_Dir(t *testing.T) {
	dir := t.TempDir()
	store, err := NewFileStore(filepath.Join(dir, "state.json"))
	if err != nil {
		t.Fatalf("NewFileStore: %v", err)
	}
	if got := store.Dir(); got != dir {
		t.Errorf("Dir() = %q, want %q", got, dir)
	}
}

func TestFileStore_HydratesBasePath_OnGet(t *testing.T) {
	store := newTestStore(t)
	dir := store.Dir()

	if err := store.SaveVolume(&Volume{ID: "v1", Name: "pvc-1", BasePath: dir}); err != nil {
		t.Fatalf("SaveVolume: %v", err)
	}
	got, ok := store.GetVolume("v1")
	if !ok {
		t.Fatal("GetVolume returned false")
	}
	if got.BasePath != dir {
		t.Errorf("GetVolume BasePath = %q, want %q", got.BasePath, dir)
	}
}

func TestFileStore_HydratesBasePath_OnList(t *testing.T) {
	store := newTestStore(t)
	dir := store.Dir()

	if err := store.SaveVolume(&Volume{ID: "v1", Name: "pvc-1", BasePath: dir}); err != nil {
		t.Fatalf("SaveVolume: %v", err)
	}
	vols := store.ListVolumes()
	if len(vols) != 1 {
		t.Fatalf("ListVolumes returned %d, want 1", len(vols))
	}
	if vols[0].BasePath != dir {
		t.Errorf("ListVolumes BasePath = %q, want %q", vols[0].BasePath, dir)
	}
}

func TestFileStore_BasePathNotInJSON(t *testing.T) {
	store := newTestStore(t)
	dir := store.Dir()

	if err := store.SaveVolume(&Volume{ID: "v1", Name: "pvc-1", BasePath: dir}); err != nil {
		t.Fatalf("SaveVolume: %v", err)
	}
	raw, err := os.ReadFile(filepath.Join(dir, "state.json"))
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	if strings.Contains(string(raw), "BasePath") {
		t.Errorf("state.json contains BasePath field; want json:\"-\" to suppress it")
	}
	if strings.Contains(string(raw), dir) {
		t.Errorf("state.json contains the basePath value %q; it must not be persisted", dir)
	}
}

func TestFileStore_BasePathRehydratedAfterReload(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "state.json")

	s1, err := NewFileStore(path)
	if err != nil {
		t.Fatalf("NewFileStore: %v", err)
	}
	if err := s1.SaveVolume(&Volume{ID: "v1", Name: "pvc-1", BasePath: dir}); err != nil {
		t.Fatalf("SaveVolume: %v", err)
	}

	// Reload from disk — BasePath must be re-derived from the store's location.
	s2, err := NewFileStore(path)
	if err != nil {
		t.Fatalf("NewFileStore reload: %v", err)
	}
	got, ok := s2.GetVolume("v1")
	if !ok {
		t.Fatal("GetVolume returned false after reload")
	}
	if got.BasePath != dir {
		t.Errorf("reloaded BasePath = %q, want %q", got.BasePath, dir)
	}
}

// --- MultiStore tests ---

func TestMultiStore_SaveAndGetVolume_RoutesToCorrectStore(t *testing.T) {
	dir1, dir2 := t.TempDir(), t.TempDir()
	ms := NewMultiStore()
	if err := ms.AddPath(dir1); err != nil {
		t.Fatalf("AddPath %s: %v", dir1, err)
	}
	if err := ms.AddPath(dir2); err != nil {
		t.Fatalf("AddPath %s: %v", dir2, err)
	}

	vol := &Volume{ID: "v1", Name: "pvc-1", BasePath: dir1}
	if err := ms.SaveVolume(vol); err != nil {
		t.Fatalf("SaveVolume: %v", err)
	}

	// Must be found globally.
	got, ok := ms.GetVolume("v1")
	if !ok {
		t.Fatal("GetVolume returned false")
	}
	if got.BasePath != dir1 {
		t.Errorf("BasePath = %q, want %q", got.BasePath, dir1)
	}

	// Must NOT appear in the other store.
	s2, ok := ms.StoreFor(dir2)
	if !ok {
		t.Fatal("StoreFor dir2 returned false")
	}
	if _, ok := s2.GetVolume("v1"); ok {
		t.Error("volume leaked into wrong store")
	}
}

func TestMultiStore_ListVolumes_UnionsAllStores(t *testing.T) {
	dir1, dir2 := t.TempDir(), t.TempDir()
	ms := NewMultiStore()
	_ = ms.AddPath(dir1)
	_ = ms.AddPath(dir2)

	_ = ms.SaveVolume(&Volume{ID: "v1", Name: "pvc-1", BasePath: dir1})
	_ = ms.SaveVolume(&Volume{ID: "v2", Name: "pvc-2", BasePath: dir2})

	vols := ms.ListVolumes()
	if len(vols) != 2 {
		t.Errorf("ListVolumes returned %d, want 2", len(vols))
	}
}

func TestMultiStore_DeleteVolume_FindsAcrossStores(t *testing.T) {
	dir1, dir2 := t.TempDir(), t.TempDir()
	ms := NewMultiStore()
	_ = ms.AddPath(dir1)
	_ = ms.AddPath(dir2)

	_ = ms.SaveVolume(&Volume{ID: "v1", Name: "pvc-1", BasePath: dir1})
	_ = ms.SaveVolume(&Volume{ID: "v2", Name: "pvc-2", BasePath: dir2})

	if err := ms.DeleteVolume("v1"); err != nil {
		t.Fatalf("DeleteVolume: %v", err)
	}
	if _, ok := ms.GetVolume("v1"); ok {
		t.Error("deleted volume still found")
	}
	if _, ok := ms.GetVolume("v2"); !ok {
		t.Error("unrelated volume was deleted")
	}
}

func TestMultiStore_SaveVolume_UnknownBasePathReturnsError(t *testing.T) {
	ms := NewMultiStore()
	_ = ms.AddPath(t.TempDir())

	err := ms.SaveVolume(&Volume{ID: "v1", BasePath: "/nonexistent/path"})
	if err == nil {
		t.Error("expected error for unknown basePath, got nil")
	}
}

func TestMultiStore_RemovePath_DropsThatStore(t *testing.T) {
	dir1, dir2 := t.TempDir(), t.TempDir()
	ms := NewMultiStore()
	_ = ms.AddPath(dir1)
	_ = ms.AddPath(dir2)

	_ = ms.SaveVolume(&Volume{ID: "v1", Name: "pvc-1", BasePath: dir1})

	ms.RemovePath(dir1)

	vols := ms.ListVolumes()
	if len(vols) != 0 {
		t.Errorf("ListVolumes after RemovePath returned %d, want 0", len(vols))
	}
	if _, ok := ms.StoreFor(dir1); ok {
		t.Error("StoreFor still returns store after RemovePath")
	}
}

func TestMultiStore_Dirs(t *testing.T) {
	dir1, dir2 := t.TempDir(), t.TempDir()
	ms := NewMultiStore()
	_ = ms.AddPath(dir1)
	_ = ms.AddPath(dir2)

	dirs := ms.Dirs()
	if len(dirs) != 2 {
		t.Errorf("Dirs() returned %d, want 2", len(dirs))
	}
	seen := map[string]bool{}
	for _, d := range dirs {
		seen[d] = true
	}
	if !seen[dir1] || !seen[dir2] {
		t.Errorf("Dirs() = %v, want both %q and %q", dirs, dir1, dir2)
	}
}

func TestSaveSnapshot_CallerMutationIsolated(t *testing.T) {
	store := newTestStore(t)

	snap := &Snapshot{ID: "snap-1", Name: "original"}
	if err := store.SaveSnapshot(snap); err != nil {
		t.Fatalf("SaveSnapshot: %v", err)
	}

	snap.Name = "mutated"

	got, ok := store.GetSnapshot("snap-1")
	if !ok {
		t.Fatal("GetSnapshot returned false after SaveSnapshot")
	}
	if got.Name != "original" {
		t.Errorf("store returned mutated name %q; SaveSnapshot must copy the input", got.Name)
	}
}
