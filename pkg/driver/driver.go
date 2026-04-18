package driver

import (
	"context"
	"fmt"
	"maps"
	"net"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/container-storage-interface/spec/lib/go/csi"
	"github.com/mporrato/btrfs-csi/pkg/btrfs"
	"github.com/mporrato/btrfs-csi/pkg/state"
	"google.golang.org/grpc"
	"k8s.io/klog/v2"
)

const (
	driverName           = "btrfs.csi.local"
	version              = "0.1.0"
	qgroupCleanupDelay   = 10 * time.Minute
	startupQgroupCleanup = 1 * time.Minute
	startupQgroupStagger = 5 * time.Second
)

// Driver implements the CSI Identity, Controller, and Node services.
type Driver struct {
	csi.UnimplementedIdentityServer
	csi.UnimplementedControllerServer
	csi.UnimplementedNodeServer
	name       string
	version    string
	nodeID     string
	mounter    Mounter
	grpcServer *grpc.Server
	manager    btrfs.Manager
	store      state.Store

	// controllerMu serializes mutating controller operations (Create/Delete/Expand)
	// to prevent races between concurrent modifications (e.g. delete + clone).
	controllerMu sync.Mutex

	poolsMu sync.RWMutex
	pools   map[string]string // pool name → base path

	qgroupCleanupMu     sync.Mutex
	qgroupCleanupTimers map[string]*time.Timer // keyed by basePath

	quotaEnabledMu sync.Mutex
	quotaEnabled   map[string]bool // basePath → true if quota already enabled

	kubeletPath string // base directory for target path validation (default /var/lib/kubelet)
}

// NewDriver creates a new Driver with the given btrfs manager, state store, and node ID.
// Returns an error if mgr or store is nil.
func NewDriver(mgr btrfs.Manager, store state.Store, nodeID string) (*Driver, error) {
	if mgr == nil {
		return nil, fmt.Errorf("btrfs.Manager must not be nil")
	}
	if store == nil {
		return nil, fmt.Errorf("state.Store must not be nil")
	}

	klog.V(2).InfoS("Creating new driver",
		"driverName", driverName,
		"version", version,
		"nodeID", nodeID,
	)

	d := &Driver{
		name:    driverName,
		version: version,
		nodeID:  nodeID,
		mounter: newRealMounter(),
		manager: mgr,
		store:   store,
	}

	// Schedule initial qgroup cleanup for all known paths, staggered.
	d.scheduleStartupQgroupCleanups(startupQgroupCleanup, startupQgroupStagger)

	return d, nil
}

// SetPools replaces the pool map atomically.
func (d *Driver) SetPools(pools map[string]string) {
	d.poolsMu.Lock()
	d.pools = pools
	d.poolsMu.Unlock()

	// Invalidate the quotaEnabled cache when pools are replaced.
	// This ensures quota is re-enabled if a pool is reformatted.
	d.quotaEnabledMu.Lock()
	d.quotaEnabled = make(map[string]bool)
	d.quotaEnabledMu.Unlock()
}

// SetKubeletPath sets the base directory for target path validation.
// The path is resolved (symlinks followed) at set time so that later
// comparisons against resolved target paths are consistent.
// If the path does not exist yet, it is stored as-is (symlink resolution
// is deferred to validateTargetPath).
func (d *Driver) SetKubeletPath(path string) error {
	if path == "" {
		d.kubeletPath = ""
		return nil
	}
	if !strings.HasPrefix(path, "/") {
		return fmt.Errorf("kubelet path %q must be absolute", path)
	}
	if slices.Contains(strings.Split(path, "/"), "..") {
		return fmt.Errorf("kubelet path %q contains invalid traversal sequence", path)
	}
	resolved, err := filepath.EvalSymlinks(path)
	if err != nil {
		// Path doesn't exist yet; store as-is. validateTargetPath will
		// resolve individual target paths and compare against this prefix.
		d.kubeletPath = path
		return nil
	}
	d.kubeletPath = resolved
	return nil
}

// getPools returns a snapshot of the current pool map.
func (d *Driver) getPools() map[string]string {
	d.poolsMu.RLock()
	defer d.poolsMu.RUnlock()
	return maps.Clone(d.pools)
}

// basePaths returns all base paths managed by this driver.
func (d *Driver) basePaths() []string {
	pools := d.getPools()
	if len(pools) > 0 {
		paths := make([]string, 0, len(pools))
		for _, p := range pools {
			paths = append(paths, p)
		}
		return paths
	}
	return d.store.Dirs()
}

// parseEndpoint extracts the socket path from a CSI endpoint string.
// Supported format: "unix:///path/to/socket" (triple-slash for absolute paths).
// The "unix://" prefix is stripped, so "unix:///abs/path" becomes "/abs/path".
// The single-slash form "unix:/path" is not supported.
func parseEndpoint(endpoint string) (string, error) {
	if !strings.HasPrefix(endpoint, "unix://") {
		return "", fmt.Errorf("unsupported endpoint scheme: %q (expected unix://)", endpoint)
	}
	// Strip "unix://" prefix. Handle both "unix:///abs/path" and "unix://relative/path".
	path := strings.TrimPrefix(endpoint, "unix://")
	if path == "" {
		return "", fmt.Errorf("empty socket path in endpoint %q", endpoint)
	}
	return path, nil
}

// Run starts the gRPC server on the given endpoint. It blocks until Stop() is
// called, at which point it returns nil on successful shutdown or an error.
func (d *Driver) Run(endpoint string) error {
	sockPath, err := parseEndpoint(endpoint)
	if err != nil {
		return err
	}

	// Remove stale socket file from a previous run.
	// Security: Check that the path is not a symlink and is actually a socket before removing.
	if info, err := os.Lstat(sockPath); err == nil {
		// Path exists - verify it's a socket, not a symlink or regular file
		if info.Mode()&os.ModeSymlink != 0 {
			return fmt.Errorf("socket path %q is a symlink, refusing to remove", sockPath)
		}
		if info.Mode()&os.ModeSocket == 0 {
			return fmt.Errorf("socket path %q exists but is not a socket", sockPath)
		}
		// Safe to remove - it's a socket
		if err := os.Remove(sockPath); err != nil {
			return fmt.Errorf("remove stale socket: %w", err)
		}
	}

	listener, err := net.Listen("unix", sockPath)
	if err != nil {
		return fmt.Errorf("listen on %s: %w", sockPath, err)
	}

	// Cap message sizes to prevent resource exhaustion from oversized requests.
	const maxMsgSize = 4 * 1024 * 1024 // 4 MiB
	d.grpcServer = grpc.NewServer(
		grpc.MaxRecvMsgSize(maxMsgSize),
		grpc.MaxSendMsgSize(maxMsgSize),
	)
	csi.RegisterIdentityServer(d.grpcServer, d)
	csi.RegisterControllerServer(d.grpcServer, d)
	csi.RegisterNodeServer(d.grpcServer, d)

	klog.InfoS("Starting gRPC server", "endpoint", endpoint)
	return d.grpcServer.Serve(listener)
}

// scheduleQgroupCleanup schedules a call to ClearStaleQgroups for the given
// basePath after the given delay. Each basePath has its own independent timer.
// If a cleanup for this path is already pending, the timer is reset so the
// cleanup runs delay after this call (providing debouncing when called
// repeatedly from volume deletions).
func (d *Driver) scheduleQgroupCleanup(basePath string, delay time.Duration) {
	d.qgroupCleanupMu.Lock()
	defer d.qgroupCleanupMu.Unlock()
	if d.qgroupCleanupTimers == nil {
		d.qgroupCleanupTimers = make(map[string]*time.Timer)
	}
	if t, ok := d.qgroupCleanupTimers[basePath]; ok {
		t.Reset(delay)
		return
	}
	d.qgroupCleanupTimers[basePath] = time.AfterFunc(delay, func() {
		// Use a generous timeout for scheduled cleanup operations.
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
		defer cancel()
		count, err := d.manager.ClearStaleQgroups(ctx, basePath)
		if err != nil {
			klog.V(4).InfoS("qgroup cleanup failed", "basePath", basePath, "err", err)
		} else {
			klog.V(4).InfoS("qgroup cleanup completed", "basePath", basePath, "removed", count)
		}
		d.qgroupCleanupMu.Lock()
		delete(d.qgroupCleanupTimers, basePath)
		d.qgroupCleanupMu.Unlock()
	})
}

// scheduleStartupQgroupCleanups schedules a qgroup cleanup for each known base
// path. Paths are sorted so the stagger order is deterministic. Each successive
// path gets an additional stagger delay to avoid all cleanups firing at once.
func (d *Driver) scheduleStartupQgroupCleanups(baseDelay, stagger time.Duration) {
	paths := d.basePaths()
	sort.Strings(paths)
	for i, bp := range paths {
		d.scheduleQgroupCleanup(bp, baseDelay+time.Duration(i)*stagger)
	}
}

// ensureQuotaEnabled calls EnsureQuotaEnabled on the manager, caching the
// result per basePath so repeated volume creations don't shell out each time.
func (d *Driver) ensureQuotaEnabled(ctx context.Context, basePath string) error {
	d.quotaEnabledMu.Lock()
	defer d.quotaEnabledMu.Unlock()
	if d.quotaEnabled[basePath] {
		return nil
	}
	if err := d.manager.EnsureQuotaEnabled(ctx, basePath); err != nil {
		return err
	}
	if d.quotaEnabled == nil {
		d.quotaEnabled = make(map[string]bool)
	}
	d.quotaEnabled[basePath] = true
	return nil
}

// Stop gracefully shuts down the gRPC server.
func (d *Driver) Stop() {
	d.qgroupCleanupMu.Lock()
	for bp, t := range d.qgroupCleanupTimers {
		t.Stop()
		delete(d.qgroupCleanupTimers, bp)
	}
	d.qgroupCleanupMu.Unlock()

	if d.grpcServer != nil {
		klog.InfoS("Stopping gRPC server")
		d.grpcServer.GracefulStop()
	}
}
