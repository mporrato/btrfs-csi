package driver

import (
	"github.com/container-storage-interface/spec/lib/go/csi"
	"github.com/guru/btrfs-csi/pkg/btrfs"
	"github.com/guru/btrfs-csi/pkg/state"
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

// NewDriver creates a new Driver with the given btrfs manager and state store.
func NewDriver(mgr btrfs.Manager, store state.Store) *Driver {
	return &Driver{
		name:    driverName,
		version: version,
		Manager: mgr,
		Store:   store,
	}
}
