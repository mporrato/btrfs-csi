package main

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/mporrato/btrfs-csi/pkg/btrfs"
)

func TestRunVersionFlag(t *testing.T) {
	// Test that --version flag returns without error
	err := run([]string{"--version"})
	if err != nil {
		t.Errorf("run with --version should not return error, got: %v", err)
	}
}

func TestRunFailsWhenConfigNotProvided(t *testing.T) {
	tmpDir := t.TempDir()
	socketPath := filepath.Join(tmpDir, "csi", "csi.sock")

	err := runWithContext(t.Context(), []string{
		"--endpoint", "unix://" + socketPath,
		"--nodeid", "test-node",
	}, &btrfs.MockManager{})
	if err == nil {
		t.Fatal("expected error when --config is not provided")
	}
}

func TestRunToleratesMissingPoolsButFailsWhenAllPoolsMissing(t *testing.T) {
	tmpDir := t.TempDir()
	configDir := filepath.Join(tmpDir, "config")
	if err := os.MkdirAll(configDir, 0o755); err != nil {
		t.Fatal(err)
	}
	// Write two pool configs: one valid, one missing
	goodPool := filepath.Join(tmpDir, "good-pool")
	missingPool := filepath.Join(tmpDir, "missing-pool")
	if err := os.WriteFile(filepath.Join(configDir, "good"), []byte(goodPool), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(configDir, "missing"), []byte(missingPool), 0o644); err != nil {
		t.Fatal(err)
	}

	// MockManager returns true only for the good pool
	mgr := &btrfs.MockManager{
		IsBtrfsFilesystemFunc: func(path string) (bool, error) {
			return path == goodPool, nil
		},
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	errCh := make(chan error, 1)
	go func() {
		errCh <- runWithContext(ctx, []string{
			"--endpoint", "unix://" + filepath.Join(tmpDir, "csi", "csi.sock"),
			"--config", configDir,
			"--nodeid", "test-node",
		}, mgr)
	}()

	// Wait briefly to allow driver to start
	time.Sleep(100 * time.Millisecond)
	cancel()

	select {
	case err := <-errCh:
		if err != nil {
			t.Errorf("expected driver to tolerate missing pool, got error: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("test timed out waiting for driver to start")
	}
}

func TestRunFailsWhenAllPoolsMissing(t *testing.T) {
	tmpDir := t.TempDir()
	bpDir := filepath.Join(tmpDir, "pool")
	configDir := filepath.Join(tmpDir, "config")
	if err := os.MkdirAll(configDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(configDir, "default"), []byte(bpDir), 0o644); err != nil {
		t.Fatal(err)
	}

	err := runWithContext(t.Context(), []string{
		"--endpoint", "unix://" + filepath.Join(tmpDir, "csi", "csi.sock"),
		"--config", configDir,
		"--nodeid", "test-node",
	}, &btrfs.MockManager{IsBtrfsFilesystemResult: false})
	if err == nil {
		t.Fatal("expected error when all pools are missing")
	}
}

func TestRunCreatesSocketDirectory(t *testing.T) {
	tmpDir := t.TempDir()
	socketDir := filepath.Join(tmpDir, "csi")
	socketPath := filepath.Join(socketDir, "csi.sock")
	endpoint := "unix://" + socketPath

	if _, err := os.Stat(socketDir); !os.IsNotExist(err) {
		t.Fatalf("socket directory should not exist yet")
	}

	bpDir := filepath.Join(tmpDir, "pool")
	configDir := filepath.Join(tmpDir, "config")
	if err := os.MkdirAll(configDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(configDir, "default"), []byte(bpDir), 0o644); err != nil {
		t.Fatal(err)
	}

	mgr := &btrfs.MockManager{IsBtrfsFilesystemResult: true}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	errCh := make(chan error, 1)
	go func() {
		errCh <- runWithContext(ctx, []string{
			"--endpoint", endpoint,
			"--config", configDir,
			"--nodeid", "test-node",
		}, mgr)
	}()

	// Wait for the socket to appear (proper readiness probe, no fixed sleep)
	deadline := time.Now().Add(5 * time.Second)
	for {
		if time.Now().After(deadline) {
			t.Fatal("timed out waiting for driver socket to appear")
		}
		if _, err := os.Stat(socketPath); err == nil {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}

	if _, err := os.Stat(socketDir); os.IsNotExist(err) {
		t.Errorf("socket directory should have been created")
	}

	cancel()

	select {
	case err := <-errCh:
		if err != nil {
			t.Errorf("runWithContext should return nil after context cancellation, got: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("runWithContext did not return after context cancellation")
	}
}
