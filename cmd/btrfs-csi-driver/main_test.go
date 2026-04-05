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

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	err := runWithContext(ctx, []string{
		"--endpoint", "unix://" + socketPath,
		"--nodeid", "test-node",
	}, &btrfs.MockManager{})
	if err == nil {
		t.Fatal("expected error when --config is not provided")
	}
}

func TestRunFailsWhenConfigPathNotBtrfs(t *testing.T) {
	tmpDir := t.TempDir()
	bpDir := filepath.Join(tmpDir, "pool")
	configFile := filepath.Join(tmpDir, "basepaths.txt")
	if err := os.WriteFile(configFile, []byte(bpDir+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	err := runWithContext(ctx, []string{
		"--endpoint", "unix://" + filepath.Join(tmpDir, "csi", "csi.sock"),
		"--config", configFile,
		"--nodeid", "test-node",
	}, &btrfs.MockManager{IsBtrfsFilesystemResult: false})
	if err == nil {
		t.Fatal("expected error when config path is not a btrfs filesystem")
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
	configFile := filepath.Join(tmpDir, "basepaths.txt")
	if err := os.WriteFile(configFile, []byte(bpDir+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	mgr := &btrfs.MockManager{IsBtrfsFilesystemResult: true}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	errCh := make(chan error, 1)
	go func() {
		errCh <- runWithContext(ctx, []string{
			"--endpoint", endpoint,
			"--config", configFile,
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
