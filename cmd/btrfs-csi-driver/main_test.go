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

// waitForSocket polls for the gRPC socket to appear, providing a proper
// readiness probe instead of a fixed sleep.
func waitForSocket(t *testing.T, socketPath string) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for {
		if time.Now().After(deadline) {
			t.Fatal("timed out waiting for driver socket to appear")
		}
		if _, err := os.Stat(socketPath); err == nil {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func TestRunToleratesMissingPoolsButFailsWhenAllPoolsMissing(t *testing.T) {
	tmpDir := t.TempDir()
	poolsDir := filepath.Join(tmpDir, "pools")
	if err := os.MkdirAll(poolsDir, 0o755); err != nil {
		t.Fatal(err)
	}
	// Create two pool subdirs: one valid, one that mgr will reject
	goodPool := filepath.Join(poolsDir, "good")
	missingPool := filepath.Join(poolsDir, "missing")
	for _, p := range []string{goodPool, missingPool} {
		if err := os.MkdirAll(p, 0o755); err != nil {
			t.Fatal(err)
		}
	}

	// MockManager returns true only for the good pool (both btrfs and mountpoint checks)
	mgr := &btrfs.MockManager{
		IsBtrfsFilesystemFunc: func(path string) (bool, error) {
			return path == goodPool, nil
		},
		IsMountpointFunc: func(path string) (bool, error) {
			return path == goodPool, nil
		},
	}

	socketPath := filepath.Join(tmpDir, "csi", "csi.sock")

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	errCh := make(chan error, 1)
	go func() {
		errCh <- runWithContext(ctx, []string{
			"--endpoint", "unix://" + socketPath,
			"--pools-dir", poolsDir,
			"--nodeid", "test-node",
		}, mgr)
	}()

	// Wait for the socket to appear (proper readiness probe, no fixed sleep)
	waitForSocket(t, socketPath)
	cancel()

	select {
	case err := <-errCh:
		if err != nil {
			t.Errorf("expected driver to tolerate missing pool, got error: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("test timed out waiting for driver to stop")
	}
}

func TestRunRejectsNonMountpointPools(t *testing.T) {
	tmpDir := t.TempDir()
	poolsDir := filepath.Join(tmpDir, "pools")
	if err := os.MkdirAll(filepath.Join(poolsDir, "default"), 0o755); err != nil {
		t.Fatal(err)
	}

	// Pool subdir exists and is btrfs, but is not a separate mountpoint.
	mgr := &btrfs.MockManager{
		IsBtrfsFilesystemResult: true,
		IsMountpointResult:      false,
	}

	err := runWithContext(t.Context(), []string{
		"--endpoint", "unix://" + filepath.Join(tmpDir, "csi", "csi.sock"),
		"--pools-dir", poolsDir,
		"--nodeid", "test-node",
	}, mgr)
	if err == nil {
		t.Fatal("expected error when pool subdir is not a mountpoint")
	}
}

func TestRunFailsWhenAllPoolsMissing(t *testing.T) {
	tmpDir := t.TempDir()
	poolsDir := filepath.Join(tmpDir, "pools")
	if err := os.MkdirAll(poolsDir, 0o755); err != nil {
		t.Fatal(err)
	}
	// One pool subdir that mgr will reject
	if err := os.MkdirAll(filepath.Join(poolsDir, "default"), 0o755); err != nil {
		t.Fatal(err)
	}

	err := runWithContext(t.Context(), []string{
		"--endpoint", "unix://" + filepath.Join(tmpDir, "csi", "csi.sock"),
		"--pools-dir", poolsDir,
		"--nodeid", "test-node",
	}, &btrfs.MockManager{IsBtrfsFilesystemResult: false})
	if err == nil {
		t.Fatal("expected error when all pools are missing")
	}
}

func TestRunPassesKubeletDirToDriver(t *testing.T) {
	tmpDir := t.TempDir()
	poolsDir := filepath.Join(tmpDir, "pools")
	if err := os.MkdirAll(filepath.Join(poolsDir, "default"), 0o755); err != nil {
		t.Fatal(err)
	}

	mgr := &btrfs.MockManager{IsBtrfsFilesystemResult: true, IsMountpointResult: true}

	socketPath := filepath.Join(tmpDir, "csi", "csi.sock")

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	errCh := make(chan error, 1)
	go func() {
		errCh <- runWithContext(ctx, []string{
			"--endpoint", "unix://" + socketPath,
			"--pools-dir", poolsDir,
			"--nodeid", "test-node",
			"--kubelet-dir", "/var/lib/k0s/kubelet",
		}, mgr)
	}()

	// Wait for the socket to appear (proper readiness probe, no fixed sleep)
	waitForSocket(t, socketPath)
	cancel()

	select {
	case err := <-errCh:
		if err != nil {
			t.Errorf("expected driver to start with --kubelet-dir, got error: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("test timed out waiting for driver to stop")
	}
}

func TestRunDefaultKubeletDir(t *testing.T) {
	tmpDir := t.TempDir()
	poolsDir := filepath.Join(tmpDir, "pools")
	if err := os.MkdirAll(filepath.Join(poolsDir, "default"), 0o755); err != nil {
		t.Fatal(err)
	}

	mgr := &btrfs.MockManager{IsBtrfsFilesystemResult: true, IsMountpointResult: true}

	socketPath := filepath.Join(tmpDir, "csi", "csi.sock")

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	errCh := make(chan error, 1)
	go func() {
		// No --kubelet-dir flag — should use default without error
		errCh <- runWithContext(ctx, []string{
			"--endpoint", "unix://" + socketPath,
			"--pools-dir", poolsDir,
			"--nodeid", "test-node",
		}, mgr)
	}()

	// Wait for the socket to appear (proper readiness probe, no fixed sleep)
	waitForSocket(t, socketPath)
	cancel()

	select {
	case err := <-errCh:
		if err != nil {
			t.Errorf("expected driver to start with default kubelet-dir, got error: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("test timed out waiting for driver to stop")
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

	poolsDir := filepath.Join(tmpDir, "pools")
	if err := os.MkdirAll(filepath.Join(poolsDir, "default"), 0o755); err != nil {
		t.Fatal(err)
	}

	mgr := &btrfs.MockManager{IsBtrfsFilesystemResult: true, IsMountpointResult: true}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	errCh := make(chan error, 1)
	go func() {
		errCh <- runWithContext(ctx, []string{
			"--endpoint", endpoint,
			"--pools-dir", poolsDir,
			"--nodeid", "test-node",
		}, mgr)
	}()

	// Wait for the socket to appear (proper readiness probe, no fixed sleep)
	waitForSocket(t, socketPath)

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
