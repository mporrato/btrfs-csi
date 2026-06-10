package driver

import (
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"
)

// after returns a channel that receives after ms milliseconds.
func after(ms int) <-chan time.Time {
	return time.After(time.Duration(ms) * time.Millisecond)
}

func TestDiscoverPools_SinglePool(t *testing.T) {
	base := t.TempDir()
	if err := os.Mkdir(filepath.Join(base, "default"), 0o755); err != nil {
		t.Fatal(err)
	}
	got, err := DiscoverPools(base)
	if err != nil {
		t.Fatalf("DiscoverPools: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("got %d pools, want 1", len(got))
	}
	if got["default"] != filepath.Join(base, "default") {
		t.Errorf("got[\"default\"] = %q, want %q", got["default"], filepath.Join(base, "default"))
	}
}

func TestDiscoverPools_MultiplePools(t *testing.T) {
	base := t.TempDir()
	for _, name := range []string{"fast", "archive"} {
		if err := os.Mkdir(filepath.Join(base, name), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	got, err := DiscoverPools(base)
	if err != nil {
		t.Fatalf("DiscoverPools: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("got %d pools, want 2", len(got))
	}
	if got["fast"] != filepath.Join(base, "fast") {
		t.Errorf("fast = %q", got["fast"])
	}
	if got["archive"] != filepath.Join(base, "archive") {
		t.Errorf("archive = %q", got["archive"])
	}
}

func TestDiscoverPools_SkipsHiddenAndFiles(t *testing.T) {
	base := t.TempDir()
	if err := os.Mkdir(filepath.Join(base, "default"), 0o755); err != nil {
		t.Fatal(err)
	}
	// hidden dir — should be skipped
	if err := os.Mkdir(filepath.Join(base, ".hidden"), 0o755); err != nil {
		t.Fatal(err)
	}
	// regular file — should be skipped
	if err := os.WriteFile(filepath.Join(base, "notapool"), []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}

	got, err := DiscoverPools(base)
	if err != nil {
		t.Fatalf("DiscoverPools: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("got %v, want only 'default'", got)
	}
}

func TestDiscoverPools_EmptyDir(t *testing.T) {
	base := t.TempDir()
	got, err := DiscoverPools(base)
	if err != nil {
		t.Fatalf("DiscoverPools: %v", err)
	}
	if len(got) != 0 {
		t.Errorf("got %v, want empty", got)
	}
}

func TestDiscoverPools_MissingDir(t *testing.T) {
	_, err := DiscoverPools("/nonexistent/pools-base")
	if err == nil {
		t.Error("expected error for missing dir, got nil")
	}
}

// identityValidate returns its input unchanged; used by tests that don't
// care about pool validation.
func identityValidate(pools map[string]string) map[string]string {
	return pools
}

func TestWatchPools_CallsReloadOnChange(t *testing.T) {
	base := t.TempDir()
	if err := os.Mkdir(filepath.Join(base, "default"), 0o755); err != nil {
		t.Fatal(err)
	}

	called := make(chan map[string]string, 2)
	stop := WatchPools(base, 20, identityValidate, func(pools map[string]string) {
		called <- pools
	})
	defer close(stop)

	// The initial config should NOT trigger a reload since it was already
	// loaded by the caller at startup. Only changes should fire reload.
	select {
	case pools := <-called:
		t.Fatalf("unexpected initial reload with unchanged config: %v", pools)
	case <-after(100):
		// Good — no reload for unchanged config.
	}

	// Add a second pool subdir — watcher should call reload.
	if err := os.Mkdir(filepath.Join(base, "fast"), 0o755); err != nil {
		t.Fatal(err)
	}
	select {
	case pools := <-called:
		if len(pools) != 2 {
			t.Errorf("after update got %v, want 2 pools", pools)
		}
		if pools["fast"] != filepath.Join(base, "fast") {
			t.Errorf("fast = %q, want %q", pools["fast"], filepath.Join(base, "fast"))
		}
	case <-after(2000):
		t.Fatal("timed out waiting for reload after pool addition")
	}
}

// TestWatchPools_PicksUpPoolThatBecomesValidLater covers C-4: a pool
// directory that exists at startup but fails validation (e.g. its
// filesystem is not yet mounted) must be picked up once it becomes valid,
// even though the raw directory listing never changes.
func TestWatchPools_PicksUpPoolThatBecomesValidLater(t *testing.T) {
	base := t.TempDir()
	poolPath := filepath.Join(base, "default")
	if err := os.Mkdir(poolPath, 0o755); err != nil {
		t.Fatal(err)
	}

	// The first validate call is the synchronous seed at startup — it must
	// see the pool as invalid (not yet mounted). Every later call (from
	// ticks) sees it as valid, simulating a filesystem mounted after startup.
	var calls atomic.Int32
	validate := func(pools map[string]string) map[string]string {
		if calls.Add(1) == 1 {
			return map[string]string{}
		}
		return pools
	}

	called := make(chan map[string]string, 2)
	stop := WatchPools(base, 20, validate, func(pools map[string]string) {
		called <- pools
	})
	defer close(stop)

	select {
	case pools := <-called:
		if len(pools) != 1 || pools["default"] != poolPath {
			t.Errorf("after pool became valid, got %v, want {default: %q}", pools, poolPath)
		}
	case <-after(2000):
		t.Fatal("timed out waiting for reload after pool became valid")
	}
}

// TestWatchPools_DropsPoolThatBecomesInvalid covers C-4: a pool that was
// valid at startup but becomes invalid at runtime (e.g. unmounted) must be
// dropped, even though the raw directory listing never changes.
func TestWatchPools_DropsPoolThatBecomesInvalid(t *testing.T) {
	base := t.TempDir()
	poolPath := filepath.Join(base, "default")
	if err := os.Mkdir(poolPath, 0o755); err != nil {
		t.Fatal(err)
	}

	// The first validate call is the synchronous seed at startup — it must
	// see the pool as valid. Every later call (from ticks) sees it as
	// invalid, simulating the filesystem being unmounted at runtime.
	var calls atomic.Int32
	validate := func(pools map[string]string) map[string]string {
		if calls.Add(1) == 1 {
			return pools
		}
		return map[string]string{}
	}

	called := make(chan map[string]string, 2)
	stop := WatchPools(base, 20, validate, func(pools map[string]string) {
		called <- pools
	})
	defer close(stop)

	select {
	case pools := <-called:
		if len(pools) != 0 {
			t.Errorf("after pool became invalid, got %v, want empty", pools)
		}
	case <-after(2000):
		t.Fatal("timed out waiting for reload after pool became invalid")
	}
}
