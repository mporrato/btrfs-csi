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

// runWithContext parses flags, creates the driver, and runs it until the context is canceled.
// It is the core implementation that can be tested without relying on OS signals.
func runWithContext(ctx context.Context, args []string, mgr btrfs.Manager) error {
	fs := flag.NewFlagSet("btrfs-csi-driver", flag.ContinueOnError)
	klog.InitFlags(fs)

	var (
		endpoint   = fs.String("endpoint", "unix:///csi/csi.sock", "CSI endpoint")
		nodeID     = fs.String("nodeid", "", "Node ID")
		poolsDir   = fs.String("pools-dir", "/var/lib/btrfs-csi", "Base directory containing pool subdirectories")
		kubeletDir = fs.String("kubelet-dir", "/var/lib/kubelet", "Kubelet base directory for target path validation")
		version    = fs.Bool("version", false, "Print version and exit")
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

	klog.InfoS("Starting btrfs-csi-driver", "endpoint", *endpoint, "nodeID", *nodeID, "poolsDir", *poolsDir)

	// Ensure socket directory exists with restrictive permissions
	socketPath := strings.TrimPrefix(*endpoint, "unix://")
	socketDir := filepath.Dir(socketPath)
	if err := os.MkdirAll(socketDir, 0o700); err != nil {
		return fmt.Errorf("create socket directory %s: %w", socketDir, err)
	}

	// Build the MultiStore by discovering pool subdirectories.
	pools, ms, err := initializeStores(*poolsDir, mgr)
	if err != nil {
		return err
	}

	// Create driver
	drv, err := driver.NewDriver(mgr, ms, *nodeID)
	if err != nil {
		return err
	}
	drv.SetPools(pools)
	if err := drv.SetKubeletPath(*kubeletDir); err != nil {
		return fmt.Errorf("set kubelet path: %w", err)
	}

	// Watch for pool subdirectory changes — 30 s poll interval.
	configStop := driver.WatchPools(*poolsDir, 30000, func(newPools map[string]string) {
		reloadPoolConfig(newPools, mgr, ms, drv)
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
		klog.InfoS("Context canceled, shutting down")
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

// initializeStores discovers pool subdirectories and initializes the MultiStore.
// Pools that are not btrfs filesystems are skipped with a warning.
// Returns an error only if no valid pools are found.
func initializeStores(poolsDir string, mgr btrfs.Manager) (map[string]string, state.Store, error) {
	ms := state.NewMultiStore()
	pools, err := driver.DiscoverPools(poolsDir)
	if err != nil {
		return nil, nil, fmt.Errorf("discover pools: %w", err)
	}
	validPools := make(map[string]string)
	for name, bp := range pools {
		ok, err := mgr.IsBtrfsFilesystem(bp)
		if err != nil {
			klog.ErrorS(err, "Skipping pool: failed to check if btrfs filesystem", "pool", name, "path", bp)
			continue
		}
		if !ok {
			klog.InfoS("Skipping pool: not a btrfs filesystem", "pool", name, "path", bp)
			continue
		}
		mount, err := mgr.IsMountpoint(bp)
		if err != nil {
			klog.ErrorS(err, "Skipping pool: failed to check if mountpoint", "pool", name, "path", bp)
			continue
		}
		if !mount {
			klog.InfoS("Skipping pool: not a separate mountpoint", "pool", name, "path", bp)
			continue
		}
		if err := ms.AddPath(bp); err != nil {
			klog.ErrorS(err, "Skipping pool: failed to open store", "pool", name, "path", bp)
			continue
		}
		validPools[name] = bp
	}
	if len(validPools) == 0 {
		return nil, nil, fmt.Errorf("no valid btrfs pools found in config")
	}
	return validPools, ms, nil
}

// reloadPoolConfig handles configuration changes during runtime.
func reloadPoolConfig(newPools map[string]string, mgr btrfs.Manager, ms state.Store, drv *driver.Driver) {
	validPools := make(map[string]string)
	validPaths := make([]string, 0, len(newPools))
	for name, p := range newPools {
		ok, err := mgr.IsBtrfsFilesystem(p)
		if err != nil || !ok {
			klog.ErrorS(err, "Skipping pool on reload: not a btrfs filesystem", "pool", name, "path", p)
			continue
		}
		mount, err := mgr.IsMountpoint(p)
		if err != nil || !mount {
			klog.ErrorS(err, "Skipping pool on reload: not a separate mountpoint", "pool", name, "path", p)
			continue
		}
		validPools[name] = p
		validPaths = append(validPaths, p)
	}
	ms.ReloadPaths(validPaths)
	drv.SetPools(validPools)
}
