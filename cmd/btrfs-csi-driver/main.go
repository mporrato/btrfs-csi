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
	"github.com/mporrato/btrfs-csi/pkg/btrfs"
	"github.com/mporrato/btrfs-csi/pkg/driver"
	"github.com/mporrato/btrfs-csi/pkg/state"
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
	return runWithContext(ctx, args, &btrfs.RealManager{})
}

// runWithContext parses flags, creates the driver, and runs it until the context is cancelled.
// It is the core implementation that can be tested without relying on OS signals.
func runWithContext(ctx context.Context, args []string, mgr btrfs.Manager) error {
	fs := flag.NewFlagSet("btrfs-csi-driver", flag.ContinueOnError)
	klog.InitFlags(fs)

	var (
		endpoint   = fs.String("endpoint", "unix:///csi/csi.sock", "CSI endpoint")
		nodeID     = fs.String("nodeid", "", "Node ID")
		configFile = fs.String("config", "", "Path to config directory (mounted ConfigMap) where each file is a pool name and its content is the btrfs base path")
		version    = fs.Bool("version", false, "Print version and exit")
	)

	if err := fs.Parse(args); err != nil {
		return err
	}

	if *version {
		fmt.Println("btrfs-csi-driver version 0.1.0")
		return nil
	}

	if *configFile == "" {
		return fmt.Errorf("--config is required: provide a path to a config directory (mounted ConfigMap) with pool definitions")
	}

	if *nodeID == "" {
		*nodeID = uuid.New().String()
	}

	klog.InfoS("Starting btrfs-csi-driver", "endpoint", *endpoint, "nodeID", *nodeID, "config", *configFile)

	// Ensure socket directory exists with restrictive permissions
	socketPath := strings.TrimPrefix(*endpoint, "unix://")
	socketDir := filepath.Dir(socketPath)
	if err := os.MkdirAll(socketDir, 0o700); err != nil {
		return fmt.Errorf("create socket directory %s: %w", socketDir, err)
	}

	// Build the MultiStore from the pool config directory.
	ms := state.NewMultiStore()
	pools, err := driver.ParsePoolConfig(*configFile)
	if err != nil {
		return fmt.Errorf("parse pool config: %w", err)
	}
	for name, bp := range pools {
		ok, err := mgr.IsBtrfsFilesystem(bp)
		if err != nil {
			return fmt.Errorf("check filesystem for pool %q at %s: %w", name, bp, err)
		}
		if !ok {
			return fmt.Errorf("pool %q: %s is not a btrfs filesystem", name, bp)
		}
		if err := ms.AddPath(bp); err != nil {
			return fmt.Errorf("open store for pool %q at %s: %w", name, bp, err)
		}
	}

	// Create driver
	drv := driver.NewDriver(mgr, ms, *nodeID)
	drv.SetPools(pools)

	// Watch for changes (ConfigMap kubelet updates) — 30 s poll interval.
	configStop := driver.WatchPoolConfig(*configFile, 30000, func(newPools map[string]string) {
		validPools := make(map[string]string)
		validPaths := make([]string, 0, len(newPools))
		for name, p := range newPools {
			ok, err := mgr.IsBtrfsFilesystem(p)
			if err != nil || !ok {
				klog.ErrorS(err, "Skipping pool on reload: not a btrfs filesystem", "pool", name, "path", p)
				continue
			}
			validPools[name] = p
			validPaths = append(validPaths, p)
		}
		ms.ReloadPaths(validPaths)
		drv.SetPools(validPools)
	})

	// Start driver in a goroutine
	errCh := make(chan error, 1)
	go func() {
		errCh <- drv.Run(*endpoint)
	}()

	// Wait for context cancellation or driver error
	select {
	case <-ctx.Done():
		close(configStop)
		klog.InfoS("Context cancelled, shutting down")
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

	klog.InfoS("Driver stopped successfully")
	return nil
}
