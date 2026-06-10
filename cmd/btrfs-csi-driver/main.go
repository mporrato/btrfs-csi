package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/google/uuid"
	"github.com/mporrato/btrfs-csi/pkg/btrfs"
	"github.com/mporrato/btrfs-csi/pkg/driver"
	"github.com/mporrato/btrfs-csi/pkg/state"
	"google.golang.org/grpc"
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
		fmt.Println("btrfs-csi-driver version " + driver.Version)
		return nil
	}

	if *nodeID == "" {
		*nodeID = uuid.New().String()
	}

	klog.InfoS("Starting btrfs-csi-driver",
		"endpoint", *endpoint, "nodeID", *nodeID,
		"poolsDir", *poolsDir, "kubeletDir", *kubeletDir)

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

	// Watch for pool subdirectory changes — 30 s poll interval. Every tick
	// re-validates discovered pools (btrfs filesystem + separate mountpoint
	// checks) so pools that become valid or invalid after startup are
	// picked up or dropped without requiring a restart.
	configStop := driver.WatchPools(*poolsDir, 30000,
		func(pools map[string]string) map[string]string {
			return validatePoolPaths(pools, mgr)
		},
		func(validPools map[string]string) {
			reloadPoolConfig(validPools, drv)
		},
	)
	defer close(configStop)

	// Start driver in a goroutine
	errCh := make(chan error, 1)
	go func() {
		errCh <- drv.Run(*endpoint)
	}()

	// Wait for context cancellation or driver error
	select {
	case <-ctx.Done():
		klog.InfoS("Context canceled, shutting down")
		drv.Stop()
		// Wait for Run() to return after Stop().
		// ErrServerStopped means GracefulStop() raced ahead of Serve() — still clean.
		if err := <-errCh; err != nil && !errors.Is(err, grpc.ErrServerStopped) {
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

// validatePoolPaths checks each candidate pool path and returns only those
// that are btrfs filesystems mounted as separate mountpoints. Invalid pools
// are dropped with a log message explaining why.
func validatePoolPaths(pools map[string]string, mgr btrfs.Manager) map[string]string {
	valid := make(map[string]string, len(pools))
	for name, p := range pools {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		ok, err := mgr.IsBtrfsFilesystem(ctx, p)
		cancel()
		if err != nil {
			klog.ErrorS(err, "Skipping pool: failed to check if btrfs filesystem", "pool", name, "path", p)
			continue
		}
		if !ok {
			klog.InfoS("Skipping pool: not a btrfs filesystem", "pool", name, "path", p)
			continue
		}
		ctx, cancel = context.WithTimeout(context.Background(), 30*time.Second)
		mount, err := mgr.IsMountpoint(ctx, p)
		cancel()
		if err != nil {
			klog.ErrorS(err, "Skipping pool: failed to check if mountpoint", "pool", name, "path", p)
			continue
		}
		if !mount {
			klog.InfoS("Skipping pool: not a separate mountpoint", "pool", name, "path", p)
			continue
		}
		valid[name] = p
	}
	return valid
}

// initializeStores discovers pool subdirectories, validates them, and
// initializes the MultiStore. Returns an error only if no valid pools are found.
func initializeStores(poolsDir string, mgr btrfs.Manager) (map[string]string, state.Store, error) {
	ms := state.NewMultiStore()
	pools, err := driver.DiscoverPools(poolsDir)
	if err != nil {
		return nil, nil, fmt.Errorf("discover pools: %w", err)
	}
	validPools := validatePoolPaths(pools, mgr)
	for name, bp := range validPools {
		if err := ms.AddPath(bp); err != nil {
			klog.ErrorS(err, "Skipping pool: failed to open store", "pool", name, "path", bp)
			delete(validPools, name)
		}
	}
	if len(validPools) == 0 {
		return nil, nil, fmt.Errorf("no valid btrfs pools found in config")
	}
	return validPools, ms, nil
}

// reloadPoolConfig applies a validated pool map to the driver and store.
func reloadPoolConfig(validPools map[string]string, drv *driver.Driver) {
	validPaths := make([]string, 0, len(validPools))
	for _, p := range validPools {
		validPaths = append(validPaths, p)
	}
	drv.ApplyPoolConfig(validPools, validPaths)
}
