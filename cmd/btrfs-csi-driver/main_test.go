package main

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/guru/btrfs-csi/pkg/btrfs"
)

func TestRunVersionFlag(t *testing.T) {
	// Test that --version flag returns without error
	err := run([]string{"--version"})
	if err != nil {
		t.Errorf("run with --version should not return error, got: %v", err)
	}
}

func TestRunFailsWhenRootPathNotBtrfs(t *testing.T) {
	tmpDir := t.TempDir()
	socketPath := filepath.Join(tmpDir, "csi", "csi.sock")

	mgr := &btrfs.MockManager{IsBtrfsFilesystemResult: false}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	err := runWithContext(ctx, []string{
		"--endpoint", "unix://" + socketPath,
		"--root-path", filepath.Join(tmpDir, "root"),
		"--nodeid", "test-node",
	}, mgr)
	if err == nil {
		t.Fatal("expected error when root-path is not a btrfs filesystem")
	}
}

func TestRunCreatesSocketDirectory(t *testing.T) {
	// Create a temporary directory for testing
	tmpDir := t.TempDir()
	socketDir := filepath.Join(tmpDir, "csi")
	socketPath := filepath.Join(socketDir, "csi.sock")
	endpoint := "unix://" + socketPath

	// Verify socket directory doesn't exist yet
	if _, err := os.Stat(socketDir); !os.IsNotExist(err) {
		t.Fatalf("socket directory should not exist yet")
	}

	// Create a temporary root path for state
	rootPath := filepath.Join(tmpDir, "root")

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	mgr := &btrfs.MockManager{IsBtrfsFilesystemResult: true}

	// Run in a goroutine since it blocks
	errCh := make(chan error, 1)
	go func() {
		errCh <- runWithContext(ctx, []string{
			"--endpoint", endpoint,
			"--root-path", rootPath,
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

	// Socket directory must have been created as a side-effect
	if _, err := os.Stat(socketDir); os.IsNotExist(err) {
		t.Errorf("socket directory should have been created")
	}

	// Cancel context to stop the driver — no process-wide signal
	cancel()

	// Wait for runWithContext to return
	select {
	case err := <-errCh:
		if err != nil {
			t.Errorf("runWithContext should return nil after context cancellation, got: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("runWithContext did not return after context cancellation")
	}
}
