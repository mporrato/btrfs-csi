//go:build integration

package btrfs

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
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
		f.Close()
		t.Fatalf("truncate loop image: %v", err)
	}
	f.Close()

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
		exec.Command("umount", mntDir).Run() //nolint:errcheck
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

func TestDeleteSubvolume_CleansUpQgroup(t *testing.T) {
	mnt := setupLoopbackBtrfs(t)

	m := &RealManager{}
	subvol := filepath.Join(mnt, "qgroup-cleanup-test")

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

	// Verify no stale qgroups remain.
	out, err := runCommand("btrfs", "qgroup", "show", "--raw", mnt)
	if err != nil {
		t.Fatalf("qgroup show after delete: %v", err)
	}
	for _, line := range strings.Split(strings.TrimSpace(out), "\n") {
		if strings.Contains(line, "<stale>") {
			t.Errorf("stale qgroup found after DeleteSubvolume:\n%s", out)
			break
		}
	}
}

func TestDeleteSubvolume_CleansUpQgroup_NoQuotas(t *testing.T) {
	mnt := setupLoopbackBtrfs(t)

	m := &RealManager{}
	subvol := filepath.Join(mnt, "no-quota-test")

	if err := m.CreateSubvolume(subvol); err != nil {
		t.Fatalf("CreateSubvolume: %v", err)
	}

	// DeleteSubvolume should succeed without error even when quotas are not enabled.
	if err := m.DeleteSubvolume(subvol); err != nil {
		t.Fatalf("DeleteSubvolume without quotas: %v", err)
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
