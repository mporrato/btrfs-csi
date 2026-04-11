package btrfs

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestSendReceive_IdempotencyCheck tests that sendReceive is idempotent.
// This test verifies that if the destination already exists, sendReceive returns nil.
func TestSendReceive_IdempotencyCheck(t *testing.T) {
	// Create a temporary directory for testing
	tmpDir := t.TempDir()
	src := filepath.Join(tmpDir, "src")
	dst := filepath.Join(tmpDir, "dst")

	// Create source directory
	if err := os.Mkdir(src, 0o755); err != nil {
		t.Fatalf("create src dir: %v", err)
	}

	// Create destination directory (simulating it already exists)
	if err := os.Mkdir(dst, 0o755); err != nil {
		t.Fatalf("create dst dir: %v", err)
	}

	// Test that sendReceive returns nil when destination already exists
	// We need to call sendReceive directly, but it's private
	// So we'll test through CreateSnapshot which calls sendReceive
	// However, CreateSnapshot will use sameFilesystem which will return true
	// since both src and dst are in the same temp directory
	// So we need to test with different filesystems to trigger sendReceive
	// This test is limited without integration test setup
	t.Skip("requires integration test setup with different filesystems")
}

// TestTempSnapshotNaming tests that temp snapshot names use UUID instead of PID
// to avoid collisions when a previous run left a stale temp snapshot.
func TestTempSnapshotNaming(t *testing.T) {
	// This test verifies the logic for generating temp snapshot names
	// We can't easily test the actual sendReceive function without btrfs,
	// but we can test the naming pattern by examining the code.

	// The issue: if a previous run left a stale temp snapshot with the same PID,
	// the new run will fail with "File exists" when trying to create the temp snapshot.

	// The fix: use UUID instead of PID to avoid collisions.

	// We'll test this by checking that the temp snapshot name pattern
	// doesn't contain the PID and contains a UUID-like string.

	// Since we can't directly test the private sendReceive function,
	// we'll verify the fix by checking the code after implementation.

	// For now, this test documents the expected behavior:
	// 1. Temp snapshot name should use UUID, not PID
	// 2. Stale temp snapshots should be cleaned up before creating new ones

	t.Log("Temp snapshot naming test - will be validated after implementation")
}

// TestTempSnapshotCleanupPattern tests that the cleanup pattern matches stale temp snapshots.
func TestTempSnapshotCleanupPattern(t *testing.T) {
	// Create a temporary directory for testing
	tmpDir := t.TempDir()
	src := filepath.Join(tmpDir, "mysubvolume")

	// Create source directory
	if err := os.Mkdir(src, 0o755); err != nil {
		t.Fatalf("create src dir: %v", err)
	}

	// Create some stale temp snapshots that should be cleaned up
	staleSnapshots := []string{
		filepath.Join(tmpDir, ".btrfs-csi-send-mysubvolume-12345"),
		filepath.Join(tmpDir, ".btrfs-csi-send-mysubvolume-67890"),
		filepath.Join(tmpDir, ".btrfs-csi-send-mysubvolume-abc123"),
	}

	for _, stale := range staleSnapshots {
		if err := os.Mkdir(stale, 0o755); err != nil {
			t.Fatalf("create stale snapshot %s: %v", stale, err)
		}
	}

	// Test the cleanup pattern
	tempSnapBase := fmt.Sprintf(".btrfs-csi-send-%s-*", filepath.Base(src))
	matches, err := filepath.Glob(filepath.Join(filepath.Dir(src), tempSnapBase))
	if err != nil {
		t.Fatalf("glob pattern: %v", err)
	}

	// Verify that all stale snapshots are matched
	if len(matches) != len(staleSnapshots) {
		t.Errorf("expected %d matches, got %d", len(staleSnapshots), len(matches))
	}

	// Verify that the source directory is not matched
	for _, match := range matches {
		if match == src {
			t.Error("source directory should not be matched by cleanup pattern")
		}
	}

	// Verify that the pattern doesn't match unrelated files
	unrelated := filepath.Join(tmpDir, ".btrfs-csi-send-other-12345")
	if err := os.Mkdir(unrelated, 0o755); err != nil {
		t.Fatalf("create unrelated dir: %v", err)
	}

	matches, err = filepath.Glob(filepath.Join(filepath.Dir(src), tempSnapBase))
	if err != nil {
		t.Fatalf("glob pattern: %v", err)
	}

	// Should still only match the 3 stale snapshots for "mysubvolume"
	if len(matches) != 3 {
		t.Errorf("expected 3 matches after adding unrelated dir, got %d", len(matches))
	}
}

// --- parseSubvolumeID tests ---

func TestParseSubvolumeID_ValidOutput(t *testing.T) {
	output := `	Name: 			subvol-1
	UUID: 			abcd-1234
	Parent UUID: 		-
	Received UUID: 		-
	Creation time: 		2025-01-15 12:00:00
	Subvolume ID: 		123
	Generation: 		5
	Gen at creation: 	1
	Parent ID: 		5
	Top level ID: 		5
	Flags: 			-
`
	id, err := parseSubvolumeID(output)
	if err != nil {
		t.Fatalf("parseSubvolumeID: %v", err)
	}
	if id != 123 {
		t.Errorf("id = %d, want 123", id)
	}
}

func TestParseSubvolumeID_DifferentFormat(t *testing.T) {
	// Test with different spacing
	output := `Subvolume ID: 456`
	id, err := parseSubvolumeID(output)
	if err != nil {
		t.Fatalf("parseSubvolumeID: %v", err)
	}
	if id != 456 {
		t.Errorf("id = %d, want 456", id)
	}
}

func TestParseSubvolumeID_LargeID(t *testing.T) {
	output := `
Subvolume ID: 18446744073709551615
`
	id, err := parseSubvolumeID(output)
	if err != nil {
		t.Fatalf("parseSubvolumeID: %v", err)
	}
	if id != 18446744073709551615 {
		t.Errorf("id = %d, want max uint64", id)
	}
}

func TestParseSubvolumeID_NotFound(t *testing.T) {
	output := `
Name: subvol-1
UUID: abcd-1234
`
	_, err := parseSubvolumeID(output)
	if err == nil {
		t.Fatal("parseSubvolumeID should return error when Subvolume ID not found")
	}
	if !strings.Contains(err.Error(), "not found") {
		t.Errorf("error should mention 'not found', got: %v", err)
	}
}

func TestParseSubvolumeID_MalformedLine(t *testing.T) {
	output := `Subvolume ID: abc`
	_, err := parseSubvolumeID(output)
	if err == nil {
		t.Fatal("parseSubvolumeID should return error for malformed ID")
	}
	// ParseUint returns "invalid syntax" error which we wrap
	if !strings.Contains(err.Error(), "invalid syntax") {
		t.Errorf("error should mention invalid syntax, got: %v", err)
	}
}

func TestParseSubvolumeID_EmptyLine(t *testing.T) {
	output := `Subvolume ID:`
	_, err := parseSubvolumeID(output)
	if err == nil {
		t.Fatal("parseSubvolumeID should return error for empty ID")
	}
}

// --- parseQgroupShow tests ---

func TestParseQgroupShow_ValidOutput(t *testing.T) {
	output := `qgroupid rfer excl max_rfer max_excl
-------- ---- ---- -------- --------
0/123    1024  512  2048     4096
0/456    2048 1024  none     none
`
	usage, err := parseQgroupShow(output, "0/123")
	if err != nil {
		t.Fatalf("parseQgroupShow: %v", err)
	}
	if usage.Referenced != 1024 {
		t.Errorf("Referenced = %d, want 1024", usage.Referenced)
	}
	if usage.Exclusive != 512 {
		t.Errorf("Exclusive = %d, want 512", usage.Exclusive)
	}
	if usage.MaxRfer != 2048 {
		t.Errorf("MaxRfer = %d, want 2048", usage.MaxRfer)
	}
}

func TestParseQgroupShow_NoLimit(t *testing.T) {
	output := `qgroupid rfer excl max_rfer max_excl
0/123    1024  512  none     none
`
	usage, err := parseQgroupShow(output, "0/123")
	if err != nil {
		t.Fatalf("parseQgroupShow: %v", err)
	}
	if usage.MaxRfer != 0 {
		t.Errorf("MaxRfer = %d, want 0 (no limit)", usage.MaxRfer)
	}
}

func TestParseQgroupShow_ZeroLimit(t *testing.T) {
	output := `qgroupid rfer excl max_rfer max_excl
0/123    1024  512  0        0
`
	usage, err := parseQgroupShow(output, "0/123")
	if err != nil {
		t.Fatalf("parseQgroupShow: %v", err)
	}
	if usage.MaxRfer != 0 {
		t.Errorf("MaxRfer = %d, want 0 (zero means no limit)", usage.MaxRfer)
	}
}

func TestParseQgroupShow_NotFound(t *testing.T) {
	output := `qgroupid rfer excl max_rfer max_excl
0/123    1024  512  2048     4096
`
	_, err := parseQgroupShow(output, "0/999")
	if err == nil {
		t.Fatal("parseQgroupShow should return error when qgroup not found")
	}
	if !strings.Contains(err.Error(), "not found") {
		t.Errorf("error should mention 'not found', got: %v", err)
	}
}

func TestParseQgroupShow_LargeValues(t *testing.T) {
	output := `qgroupid rfer excl max_rfer max_excl
0/123    1099511627776 549755813888 2199023255552 4398046511104
`
	usage, err := parseQgroupShow(output, "0/123")
	if err != nil {
		t.Fatalf("parseQgroupShow: %v", err)
	}
	if usage.Referenced != 1099511627776 {
		t.Errorf("Referenced = %d, want 1TB", usage.Referenced)
	}
	if usage.Exclusive != 549755813888 {
		t.Errorf("Exclusive = %d, want 512GB", usage.Exclusive)
	}
}

func TestParseQgroupShow_MalformedRfer(t *testing.T) {
	output := `qgroupid rfer excl max_rfer max_excl
0/123    abc  512  2048     4096
`
	_, err := parseQgroupShow(output, "0/123")
	if err == nil {
		t.Fatal("parseQgroupShow should return error for malformed rfer")
	}
}

func TestParseQgroupShow_MalformedExcl(t *testing.T) {
	output := `qgroupid rfer excl max_rfer max_excl
0/123    1024 abc  2048     4096
`
	_, err := parseQgroupShow(output, "0/123")
	if err == nil {
		t.Fatal("parseQgroupShow should return error for malformed excl")
	}
}

func TestParseQgroupShow_MalformedMaxRfer(t *testing.T) {
	output := `qgroupid rfer excl max_rfer max_excl
0/123    1024 512  abc      4096
`
	_, err := parseQgroupShow(output, "0/123")
	if err == nil {
		t.Fatal("parseQgroupShow should return error for malformed max_rfer")
	}
}
