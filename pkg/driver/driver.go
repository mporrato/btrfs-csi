package driver

import (
	"github.com/container-storage-interface/spec/lib/go/csi"
	"github.com/guru/btrfs-csi/pkg/btrfs"
	"github.com/guru/btrfs-csi/pkg/state"
	"k8s.io/klog/v2"
)

const (
	driverName = "btrfs.csi.local"
	version    = "0.1.0"
)

// Driver implements the CSI Identity, Controller, and Node services.
type Driver struct {
	csi.UnimplementedIdentityServer
	name     string
	version  string
	nodeID   string
	rootPath string
	btrfs.Manager
	state.Store
}

// NewDriver creates a new Driver with the given btrfs manager, state store, node ID, and root path.
// It panics if mgr or store is nil.
func NewDriver(mgr btrfs.Manager, store state.Store, nodeID, rootPath string) *Driver {
	if mgr == nil {
		panic("btrfs.Manager must not be nil")
	}
	if store == nil {
		panic("state.Store must not be nil")
	}

	klog.V(2).InfoS("Creating new driver",
		"driverName", driverName,
		"version", version,
		"nodeID", nodeID,
		"rootPath", rootPath,
	)

	return &Driver{
		name:     driverName,
		version:  version,
		nodeID:   nodeID,
		rootPath: rootPath,
		Manager:  mgr,
		Store:    store,
	}
}
