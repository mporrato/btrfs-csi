package driver

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

// after returns a channel that receives after ms milliseconds.
func after(ms int) <-chan time.Time {
	return time.After(time.Duration(ms) * time.Millisecond)
}

func TestParsePoolConfig_SinglePool(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "default"), []byte("/var/lib/btrfs-csi\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	got, err := ParsePoolConfig(dir)
	if err != nil {
		t.Fatalf("ParsePoolConfig: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("got %d pools, want 1", len(got))
	}
	if got["default"] != "/var/lib/btrfs-csi" {
		t.Errorf("got[\"default\"] = %q, want %q", got["default"], "/var/lib/btrfs-csi")
	}
}

func TestParsePoolConfig_MultiplePools(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "fast"), []byte("/mnt/nvme/btrfs-csi"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "archive"), []byte("  /mnt/hdd/btrfs-csi \n"), 0o600); err != nil {
		t.Fatal(err)
	}

	got, err := ParsePoolConfig(dir)
	if err != nil {
		t.Fatalf("ParsePoolConfig: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("got %d pools, want 2", len(got))
	}
	if got["fast"] != "/mnt/nvme/btrfs-csi" {
		t.Errorf("fast = %q", got["fast"])
	}
	if got["archive"] != "/mnt/hdd/btrfs-csi" {
		t.Errorf("archive = %q", got["archive"])
	}
}

func TestParsePoolConfig_SkipsHiddenAndDotDot(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "default"), []byte("/var/lib/btrfs-csi"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, ".hidden"), []byte("/secret"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(filepath.Join(dir, "..data"), 0o755); err != nil {
		t.Fatal(err)
	}

	got, err := ParsePoolConfig(dir)
	if err != nil {
		t.Fatalf("ParsePoolConfig: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("got %v, want only 'default'", got)
	}
}

func TestParsePoolConfig_RelativePathRejected(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "bad"), []byte("relative/path"), 0o600); err != nil {
		t.Fatal(err)
	}

	_, err := ParsePoolConfig(dir)
	if err == nil {
		t.Error("expected error for relative path, got nil")
	}
}

func TestParsePoolConfig_EmptyDir(t *testing.T) {
	dir := t.TempDir()
	got, err := ParsePoolConfig(dir)
	if err != nil {
		t.Fatalf("ParsePoolConfig: %v", err)
	}
	if len(got) != 0 {
		t.Errorf("got %v, want empty", got)
	}
}

func TestParsePoolConfig_MissingDir(t *testing.T) {
	_, err := ParsePoolConfig("/nonexistent/dir")
	if err == nil {
		t.Error("expected error for missing dir, got nil")
	}
}

func TestWatchPoolConfig_CallsReloadOnChange(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "default"), []byte("/mnt/a"), 0o600); err != nil {
		t.Fatal(err)
	}

	called := make(chan map[string]string, 2)
	stop := WatchPoolConfig(dir, 20, func(pools map[string]string) {
		called <- pools
	})
	defer close(stop)

	// Initial load fires on first tick.
	select {
	case pools := <-called:
		if len(pools) != 1 || pools["default"] != "/mnt/a" {
			t.Errorf("initial reload got %v, want map[default:/mnt/a]", pools)
		}
	case <-after(2000):
		t.Fatal("timed out waiting for initial reload")
	}

	// Add a second pool — watcher should call reload again.
	if err := os.WriteFile(filepath.Join(dir, "fast"), []byte("/mnt/fast"), 0o600); err != nil {
		t.Fatal(err)
	}
	select {
	case pools := <-called:
		if len(pools) != 2 {
			t.Errorf("after update got %v, want 2 pools", pools)
		}
		if pools["fast"] != "/mnt/fast" {
			t.Errorf("fast = %q, want /mnt/fast", pools["fast"])
		}
	case <-after(2000):
		t.Fatal("timed out waiting for reload after pool addition")
	}
}
