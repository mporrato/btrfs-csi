package btrfs

// QgroupUsage represents quota group usage information for a btrfs subvolume.
type QgroupUsage struct {
	// Referenced is the total amount of data referenced by the subvolume.
	Referenced uint64
	// Exclusive is the amount of data exclusive to this subvolume (not shared).
	Exclusive uint64
	// MaxRfer is the maximum reference limit set on the qgroup (0 means no limit).
	MaxRfer uint64
}

// FsUsage represents filesystem usage information for a btrfs filesystem.
type FsUsage struct {
	// Total is the total size of the filesystem in bytes.
	Total uint64
	// Used is the amount of space used in bytes.
	Used uint64
	// Available is the amount of free space available in bytes.
	Available uint64
}

// Manager abstracts all btrfs CLI operations.
// Implementations must be safe for concurrent use.
type Manager interface {
	// CreateSubvolume creates a new btrfs subvolume at the specified path.
	CreateSubvolume(path string) error

	// DeleteSubvolume deletes the btrfs subvolume at the specified path.
	DeleteSubvolume(path string) error

	// SubvolumeExists checks if a btrfs subvolume exists at the specified path.
	SubvolumeExists(path string) (bool, error)

	// CreateSnapshot creates a snapshot of src at dst.
	// If readonly is true, the snapshot will be read-only.
	CreateSnapshot(src, dst string, readonly bool) error

	// EnsureQuotaEnabled enables quotas on the filesystem at the specified mountpoint.
	// This operation is idempotent.
	EnsureQuotaEnabled(mountpoint string) error

	// SetQgroupLimit sets a quota limit on the subvolume at the specified path.
	SetQgroupLimit(path string, bytes uint64) error

	// RemoveQgroupLimit removes the quota limit from the subvolume at the specified path.
	RemoveQgroupLimit(path string) error

	// GetQgroupUsage returns the quota usage information for the subvolume at the specified path.
	GetQgroupUsage(path string) (*QgroupUsage, error)

	// ClearStaleQgroups removes all qgroup entries that have no corresponding subvolume
	// on the filesystem at the given mountpoint. This is a periodic housekeeping
	// operation; stale entries accumulate because btrfs does not auto-remove qgroups
	// when subvolumes are deleted. Returns the number of qgroups removed.
	ClearStaleQgroups(mountpoint string) (int, error)

	// GetFilesystemUsage returns the filesystem usage information for the filesystem
	// containing the specified path.
	GetFilesystemUsage(path string) (*FsUsage, error)

	// IsBtrfsFilesystem reports whether path (or its nearest existing ancestor)
	// resides on a btrfs filesystem.
	IsBtrfsFilesystem(path string) (bool, error)
}
