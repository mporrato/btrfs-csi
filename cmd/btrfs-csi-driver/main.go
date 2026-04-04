package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"

	"github.com/google/uuid"
	"github.com/guru/btrfs-csi/pkg/btrfs"
	"github.com/guru/btrfs-csi/pkg/driver"
	"github.com/guru/btrfs-csi/pkg/state"
	"k8s.io/klog/v2"
)

func main() {
	if err := run(os.Args[1:]); err != nil {
		klog.ErrorS(err, "Fatal error")
		os.Exit(1)
	}
}

// run creates an OS-signal context and delegates to runWithContext.
// It is separated from main() to make testing easier.
func run(args []string) error {
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGTERM, syscall.SIGINT)
	defer stop()
	return runWithContext(ctx, args)
}

// runWithContext parses flags, creates the driver, and runs it until the context is cancelled.
// It is the core implementation that can be tested without relying on OS signals.
func runWithContext(ctx context.Context, args []string) error {
	fs := flag.NewFlagSet("btrfs-csi-driver", flag.ContinueOnError)

	var (
		endpoint = fs.String("endpoint", "unix:///csi/csi.sock", "CSI endpoint")
		nodeID   = fs.String("nodeid", "", "Node ID")
		rootPath = fs.String("root-path", "/var/lib/btrfs-csi", "Root path for btrfs-csi")
		version  = fs.Bool("version", false, "Print version and exit")
	)

	if err := fs.Parse(args); err != nil {
		return err
	}

	if *version {
		fmt.Println("btrfs-csi-driver version 0.1.0")
		return nil
	}

	if *nodeID == "" {
		*nodeID = uuid.New().String()
	}

	klog.Infof("Starting btrfs-csi-driver")
	klog.Infof("Endpoint: %s", *endpoint)
	klog.Infof("Node ID: %s", *nodeID)
	klog.Infof("Root path: %s", *rootPath)

	// Ensure socket directory exists with restrictive permissions
	socketPath := strings.TrimPrefix(*endpoint, "unix://")
	socketDir := filepath.Dir(socketPath)
	if err := os.MkdirAll(socketDir, 0o700); err != nil {
		return fmt.Errorf("create socket directory %s: %w", socketDir, err)
	}

	// Create btrfs manager
	mgr := &btrfs.RealManager{}

	// Create state store
	statePath := filepath.Join(*rootPath, "state.json")
	store, err := state.NewFileStore(statePath)
	if err != nil {
		return fmt.Errorf("create state store: %w", err)
	}

	// Create driver
	drv := driver.NewDriver(mgr, store, *nodeID, *rootPath)

	// Start driver in a goroutine
	errCh := make(chan error, 1)
	go func() {
		errCh <- drv.Run(*endpoint)
	}()

	// Wait for context cancellation or driver error
	select {
	case <-ctx.Done():
		klog.Infof("Context cancelled, shutting down")
		drv.Stop()
		// Wait for Run() to return after Stop()
		if err := <-errCh; err != nil {
			return fmt.Errorf("driver stopped with error: %w", err)
		}
	case err := <-errCh:
		if err != nil {
			return fmt.Errorf("driver failed: %w", err)
		}
	}

	klog.Infof("Driver stopped successfully")
	return nil
}
