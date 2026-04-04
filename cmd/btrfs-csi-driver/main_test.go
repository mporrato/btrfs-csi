package main

import (
	"os"
	"path/filepath"
	"syscall"
	"testing"
	"time"
)

func TestRunVersionFlag(t *testing.T) {
	// Test that --version flag returns without error
	err := run([]string{"--version"})
	if err != nil {
		t.Errorf("run with --version should not return error, got: %v", err)
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

	// Run in a goroutine since it blocks
	errCh := make(chan error, 1)
	go func() {
		errCh <- run([]string{
			"--endpoint", endpoint,
			"--root-path", rootPath,
			"--nodeid", "test-node",
		})
	}()

	// Wait a bit for the socket to be created
	time.Sleep(100 * time.Millisecond)

	// Check if socket directory was created
	if _, err := os.Stat(socketDir); os.IsNotExist(err) {
		t.Errorf("socket directory should have been created")
	}

	// Send SIGTERM to stop the driver
	p, _ := os.FindProcess(os.Getpid())
	p.Signal(syscall.SIGTERM)

	// Wait for run to return
	select {
	case err := <-errCh:
		if err != nil {
			t.Errorf("run should not return error after SIGTERM, got: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("run did not return after SIGTERM")
	}
}
