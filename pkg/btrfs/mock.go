package btrfs

import "context"

// SnapshotCall records arguments passed to CreateSnapshot.
type SnapshotCall struct {
	Src      string
	Dst      string
	Readonly bool
}

// QgroupLimitCall records arguments passed to SetQgroupLimit.
type QgroupLimitCall struct {
	Path  string
	Bytes uint64
}

// MockManager is a test double that implements Manager.
// It records all method calls and returns pre-configured results.
type MockManager struct {
	// CreateSubvolume
	CreateSubvolumeCalls []string
	CreateSubvolumeErr   error

	// DeleteSubvolume
	DeleteSubvolumeCalls []string
	DeleteSubvolumeErr   error

	// SubvolumeExists
	SubvolumeExistsCalls  []string
	SubvolumeExistsResult bool
	SubvolumeExistsErr    error

	// CreateSnapshot
	CreateSnapshotCalls []SnapshotCall
	CreateSnapshotErr   error

	// EnsureQuotaEnabled
	EnsureQuotaEnabledCalls []string
	EnsureQuotaEnabledErr   error

	// SetQgroupLimit
	SetQgroupLimitCalls []QgroupLimitCall
	SetQgroupLimitErr   error

	// RemoveQgroupLimit
	RemoveQgroupLimitCalls []string
	RemoveQgroupLimitErr   error

	// GetQgroupUsage
	GetQgroupUsageCalls  []string
	GetQgroupUsageResult *QgroupUsage
	GetQgroupUsageErr    error

	// ClearStaleQgroups
	ClearStaleQgroupsCalls  []string
	ClearStaleQgroupsResult int
	ClearStaleQgroupsErr    error

	// GetFilesystemUsage
	GetFilesystemUsageCalls  []string
	GetFilesystemUsageResult *FsUsage
	GetFilesystemUsageErr    error

	// IsBtrfsFilesystem
	IsBtrfsFilesystemCalls  []string
	IsBtrfsFilesystemResult bool
	IsBtrfsFilesystemErr    error
	IsBtrfsFilesystemFunc   func(path string) (bool, error)

	// IsMountpoint
	IsMountpointCalls  []string
	IsMountpointResult bool
	IsMountpointErr    error
	IsMountpointFunc   func(path string) (bool, error)
}

func (m *MockManager) CreateSubvolume(ctx context.Context, path string) error {
	m.CreateSubvolumeCalls = append(m.CreateSubvolumeCalls, path)
	return m.CreateSubvolumeErr
}

func (m *MockManager) DeleteSubvolume(ctx context.Context, path string) error {
	m.DeleteSubvolumeCalls = append(m.DeleteSubvolumeCalls, path)
	return m.DeleteSubvolumeErr
}

func (m *MockManager) SubvolumeExists(ctx context.Context, path string) (bool, error) {
	m.SubvolumeExistsCalls = append(m.SubvolumeExistsCalls, path)
	return m.SubvolumeExistsResult, m.SubvolumeExistsErr
}

func (m *MockManager) CreateSnapshot(ctx context.Context, src, dst string, readonly bool) error {
	m.CreateSnapshotCalls = append(m.CreateSnapshotCalls, SnapshotCall{
		Src:      src,
		Dst:      dst,
		Readonly: readonly,
	})
	return m.CreateSnapshotErr
}

func (m *MockManager) EnsureQuotaEnabled(ctx context.Context, mountpoint string) error {
	m.EnsureQuotaEnabledCalls = append(m.EnsureQuotaEnabledCalls, mountpoint)
	return m.EnsureQuotaEnabledErr
}

func (m *MockManager) SetQgroupLimit(ctx context.Context, path string, bytes uint64) error {
	m.SetQgroupLimitCalls = append(m.SetQgroupLimitCalls, QgroupLimitCall{
		Path:  path,
		Bytes: bytes,
	})
	return m.SetQgroupLimitErr
}

func (m *MockManager) RemoveQgroupLimit(ctx context.Context, path string) error {
	m.RemoveQgroupLimitCalls = append(m.RemoveQgroupLimitCalls, path)
	return m.RemoveQgroupLimitErr
}

func (m *MockManager) ClearStaleQgroups(ctx context.Context, mountpoint string) (int, error) {
	m.ClearStaleQgroupsCalls = append(m.ClearStaleQgroupsCalls, mountpoint)
	return m.ClearStaleQgroupsResult, m.ClearStaleQgroupsErr
}

func (m *MockManager) GetQgroupUsage(ctx context.Context, path string) (*QgroupUsage, error) {
	m.GetQgroupUsageCalls = append(m.GetQgroupUsageCalls, path)
	return m.GetQgroupUsageResult, m.GetQgroupUsageErr
}

func (m *MockManager) GetFilesystemUsage(ctx context.Context, path string) (*FsUsage, error) {
	m.GetFilesystemUsageCalls = append(m.GetFilesystemUsageCalls, path)
	return m.GetFilesystemUsageResult, m.GetFilesystemUsageErr
}

func (m *MockManager) IsBtrfsFilesystem(ctx context.Context, path string) (bool, error) {
	m.IsBtrfsFilesystemCalls = append(m.IsBtrfsFilesystemCalls, path)
	if m.IsBtrfsFilesystemFunc != nil {
		return m.IsBtrfsFilesystemFunc(path)
	}
	return m.IsBtrfsFilesystemResult, m.IsBtrfsFilesystemErr
}

func (m *MockManager) IsMountpoint(ctx context.Context, path string) (bool, error) {
	m.IsMountpointCalls = append(m.IsMountpointCalls, path)
	if m.IsMountpointFunc != nil {
		return m.IsMountpointFunc(path)
	}
	return m.IsMountpointResult, m.IsMountpointErr
}
