//go:build integration

package btrfs

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// setupLoopbackBtrfs creates a temporary loopback btrfs filesystem for integration tests.
// Returns the mount path. Cleanup is registered via t.Cleanup. Skips if not running as root.
func setupLoopbackBtrfs(t *testing.T) string {
	t.Helper()

	if os.Getuid() != 0 {
		t.Skip("integration tests require root")
	}

	dir := t.TempDir()
	imgPath := filepath.Join(dir, "btrfs.img")

	// Create a 256MB sparse image file.
	f, err := os.Create(imgPath)
	if err != nil {
		t.Fatalf("create loop image: %v", err)
	}
	if err := f.Truncate(256 * 1024 * 1024); err != nil {
		_ = f.Close()
		t.Fatalf("truncate loop image: %v", err)
	}
	if err := f.Close(); err != nil {
		t.Fatalf("close loop image: %v", err)
	}

	if out, err := exec.Command("mkfs.btrfs", imgPath).CombinedOutput(); err != nil {
		t.Fatalf("mkfs.btrfs: %v: %s", err, out)
	}

	mntDir := filepath.Join(dir, "mnt")
	if err := os.Mkdir(mntDir, 0o755); err != nil {
		t.Fatalf("mkdir mount point: %v", err)
	}
	if out, err := exec.Command("mount", "-o", "loop", imgPath, mntDir).CombinedOutput(); err != nil {
		t.Fatalf("mount: %v: %s", err, out)
	}

	t.Cleanup(func() {
		// Best effort unmount; ignore errors during cleanup.
		exec.Command("umount", mntDir).Run() //nolint:errcheck // intentional: cleanup
	})
	return mntDir
}

func TestCreateAndDeleteSubvolume(t *testing.T) {
	mnt := setupLoopbackBtrfs(t)

	m := &RealManager{}
	subvol := filepath.Join(mnt, "test-subvol")

	if err := m.CreateSubvolume(subvol); err != nil {
		t.Fatalf("CreateSubvolume: %v", err)
	}

	exists, err := m.SubvolumeExists(subvol)
	if err != nil {
		t.Fatalf("SubvolumeExists after create: %v", err)
	}
	if !exists {
		t.Fatal("expected subvolume to exist after creation")
	}

	if err := m.DeleteSubvolume(subvol); err != nil {
		t.Fatalf("DeleteSubvolume: %v", err)
	}

	exists, err = m.SubvolumeExists(subvol)
	if err != nil {
		t.Fatalf("SubvolumeExists after delete: %v", err)
	}
	if exists {
		t.Fatal("expected subvolume to not exist after deletion")
	}
}

func TestSubvolumeExists_NotExists(t *testing.T) {
	mnt := setupLoopbackBtrfs(t)

	m := &RealManager{}
	subvol := filepath.Join(mnt, "nonexistent")

	exists, err := m.SubvolumeExists(subvol)
	if err != nil {
		t.Fatalf("SubvolumeExists: %v", err)
	}
	if exists {
		t.Fatal("expected nonexistent path to not exist")
	}
}

func TestCreateSnapshot(t *testing.T) {
	mnt := setupLoopbackBtrfs(t)

	m := &RealManager{}
	src := filepath.Join(mnt, "source")
	dst := filepath.Join(mnt, "snapshot")

	if err := m.CreateSubvolume(src); err != nil {
		t.Fatalf("CreateSubvolume: %v", err)
	}

	testFile := filepath.Join(src, "test.txt")
	if err := os.WriteFile(testFile, []byte("hello"), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	if err := m.CreateSnapshot(src, dst, false); err != nil {
		t.Fatalf("CreateSnapshot: %v", err)
	}

	data, err := os.ReadFile(filepath.Join(dst, "test.txt"))
	if err != nil {
		t.Fatalf("ReadFile from snapshot: %v", err)
	}
	if string(data) != "hello" {
		t.Fatalf("expected 'hello', got %q", string(data))
	}
}

func TestReadonlySnapshot(t *testing.T) {
	mnt := setupLoopbackBtrfs(t)

	m := &RealManager{}
	src := filepath.Join(mnt, "source")
	dst := filepath.Join(mnt, "ro-snapshot")

	if err := m.CreateSubvolume(src); err != nil {
		t.Fatalf("CreateSubvolume: %v", err)
	}

	if err := m.CreateSnapshot(src, dst, true); err != nil {
		t.Fatalf("CreateSnapshot (readonly): %v", err)
	}

	err := os.WriteFile(filepath.Join(dst, "test.txt"), []byte("hello"), 0o644)
	if err == nil {
		t.Fatal("expected write to readonly snapshot to fail")
	}
}

func TestQuotaEnableAndLimit(t *testing.T) {
	mnt := setupLoopbackBtrfs(t)

	m := &RealManager{}
	subvol := filepath.Join(mnt, "quota-test")

	if err := m.CreateSubvolume(subvol); err != nil {
		t.Fatalf("CreateSubvolume: %v", err)
	}

	if err := m.EnsureQuotaEnabled(mnt); err != nil {
		t.Fatalf("EnsureQuotaEnabled: %v", err)
	}

	limit := uint64(100 * 1024 * 1024) // 100MB
	if err := m.SetQgroupLimit(subvol, limit); err != nil {
		t.Fatalf("SetQgroupLimit: %v", err)
	}

	usage, err := m.GetQgroupUsage(subvol)
	if err != nil {
		t.Fatalf("GetQgroupUsage: %v", err)
	}
	if usage.MaxRfer != limit {
		t.Fatalf("expected MaxRfer=%d, got %d", limit, usage.MaxRfer)
	}
}

// hasStaleQgroups checks whether any stale qgroups exist on the given mountpoint.
func hasStaleQgroups(mnt string) (bool, error) {
	out, err := runCommand("btrfs", "qgroup", "show", "--raw", mnt)
	if err != nil {
		return false, err
	}
	for line := range strings.SplitSeq(strings.TrimSpace(out), "\n") {
		if strings.Contains(line, "<stale>") {
			return true, nil
		}
	}
	return false, nil
}

func TestClearStaleQgroups(t *testing.T) {
	mnt := setupLoopbackBtrfs(t)

	m := &RealManager{}
	subvol := filepath.Join(mnt, "stale-qgroup-test")

	if err := m.CreateSubvolume(subvol); err != nil {
		t.Fatalf("CreateSubvolume: %v", err)
	}
	if err := m.EnsureQuotaEnabled(mnt); err != nil {
		t.Fatalf("EnsureQuotaEnabled: %v", err)
	}
	if err := m.SetQgroupLimit(subvol, 100*1024*1024); err != nil {
		t.Fatalf("SetQgroupLimit: %v", err)
	}
	if err := m.DeleteSubvolume(subvol); err != nil {
		t.Fatalf("DeleteSubvolume: %v", err)
	}
	// Wait for the kernel to finish background subvolume cleanup;
	// otherwise the qgroup may still be busy when clear-stale runs.
	if _, err := runCommand("btrfs", "subvolume", "sync", mnt); err != nil {
		t.Fatalf("subvolume sync: %v", err)
	}

	// After deletion without cleanup, a stale qgroup should exist.
	stale, err := hasStaleQgroups(mnt)
	if err != nil {
		t.Skipf("qgroup show failed (quotas unavailable on this kernel): %v", err)
	}
	if !stale {
		t.Skip("no stale qgroups present (may be using squota); skipping cleanup test")
	}

	// ClearStaleQgroups should eventually remove all stale qgroups.
	// The kernel may keep them busy briefly after subvolume deletion,
	// so retry a few times.
	deadline := time.Now().Add(30 * time.Second)
	for {
		count, err := m.ClearStaleQgroups(mnt)
		if err != nil {
			t.Fatalf("ClearStaleQgroups: %v", err)
		}
		if count > 0 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("ClearStaleQgroups did not remove any stale qgroups within timeout")
		}
		time.Sleep(1 * time.Second)
	}
	stale, err = hasStaleQgroups(mnt)
	if err != nil {
		t.Fatalf("qgroup show after ClearStaleQgroups: %v", err)
	}
	if stale {
		t.Error("stale qgroup still present after ClearStaleQgroups")
	}
}

func TestClearStaleQgroups_NoQuotas(t *testing.T) {
	mnt := setupLoopbackBtrfs(t)

	m := &RealManager{}
	// ClearStaleQgroups should be a no-op when quotas are not enabled.
	count, err := m.ClearStaleQgroups(mnt)
	if err != nil {
		t.Fatalf("ClearStaleQgroups without quotas should be no-op, got: %v", err)
	}
	if count != 0 {
		t.Errorf("expected 0 qgroups removed when quotas disabled, got %d", count)
	}
}

func TestRemoveQgroupLimit(t *testing.T) {
	mnt := setupLoopbackBtrfs(t)

	m := &RealManager{}
	subvol := filepath.Join(mnt, "remove-limit-test")

	if err := m.CreateSubvolume(subvol); err != nil {
		t.Fatalf("CreateSubvolume: %v", err)
	}

	if err := m.EnsureQuotaEnabled(mnt); err != nil {
		t.Fatalf("EnsureQuotaEnabled: %v", err)
	}

	limit := uint64(100 * 1024 * 1024)
	if err := m.SetQgroupLimit(subvol, limit); err != nil {
		t.Fatalf("SetQgroupLimit: %v", err)
	}

	if err := m.RemoveQgroupLimit(subvol); err != nil {
		t.Fatalf("RemoveQgroupLimit: %v", err)
	}

	usage, err := m.GetQgroupUsage(subvol)
	if err != nil {
		t.Fatalf("GetQgroupUsage after removing limit: %v", err)
	}
	if usage.MaxRfer != 0 {
		t.Fatalf("expected MaxRfer=0 after removing limit, got %d", usage.MaxRfer)
	}
}

// TestCrossFilesystemSnapshot tests snapshot creation across different btrfs filesystems.
func TestCrossFilesystemSnapshot(t *testing.T) {
	// Create two separate btrfs filesystems
	mnt1 := setupLoopbackBtrfs(t)
	mnt2 := setupLoopbackBtrfs(t)

	m := &RealManager{}
	src := filepath.Join(mnt1, "source")
	dst := filepath.Join(mnt2, "snapshot")

	// Create source subvolume
	if err := m.CreateSubvolume(src); err != nil {
		t.Fatalf("CreateSubvolume: %v", err)
	}

	// Write some data to source
	testFile := filepath.Join(src, "test.txt")
	if err := os.WriteFile(testFile, []byte("cross-filesystem test"), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	// Create snapshot across filesystems
	if err := m.CreateSnapshot(src, dst, false); err != nil {
		t.Fatalf("CreateSnapshot across filesystems: %v", err)
	}

	// Verify snapshot contains the data
	data, err := os.ReadFile(filepath.Join(dst, "test.txt"))
	if err != nil {
		t.Fatalf("ReadFile from snapshot: %v", err)
	}
	if string(data) != "cross-filesystem test" {
		t.Fatalf("expected 'cross-filesystem test', got %q", string(data))
	}

	// Verify snapshot is writable (not readonly)
	if err := os.WriteFile(filepath.Join(dst, "new.txt"), []byte("new data"), 0o644); err != nil {
		t.Fatalf("WriteFile to snapshot: %v", err)
	}
}

// TestCrossFilesystemReadonlySnapshot tests readonly snapshot creation across different btrfs filesystems.
func TestCrossFilesystemReadonlySnapshot(t *testing.T) {
	// Create two separate btrfs filesystems
	mnt1 := setupLoopbackBtrfs(t)
	mnt2 := setupLoopbackBtrfs(t)

	m := &RealManager{}
	src := filepath.Join(mnt1, "source")
	dst := filepath.Join(mnt2, "ro-snapshot")

	// Create source subvolume
	if err := m.CreateSubvolume(src); err != nil {
		t.Fatalf("CreateSubvolume: %v", err)
	}

	// Create readonly snapshot across filesystems
	if err := m.CreateSnapshot(src, dst, true); err != nil {
		t.Fatalf("CreateSnapshot (readonly) across filesystems: %v", err)
	}

	// Verify snapshot is readonly
	err := os.WriteFile(filepath.Join(dst, "test.txt"), []byte("hello"), 0o644)
	if err == nil {
		t.Fatal("expected write to readonly snapshot to fail")
	}
}
