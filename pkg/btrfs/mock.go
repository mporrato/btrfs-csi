package btrfs

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

	// DestroyQgroup
	DestroyQgroupCalls []string
	DestroyQgroupErr   error

	// GetQgroupUsage
	GetQgroupUsageCalls  []string
	GetQgroupUsageResult *QgroupUsage
	GetQgroupUsageErr    error

	// GetFilesystemUsage
	GetFilesystemUsageCalls  []string
	GetFilesystemUsageResult *FsUsage
	GetFilesystemUsageErr    error
}

func (m *MockManager) CreateSubvolume(path string) error {
	m.CreateSubvolumeCalls = append(m.CreateSubvolumeCalls, path)
	return m.CreateSubvolumeErr
}

func (m *MockManager) DeleteSubvolume(path string) error {
	m.DeleteSubvolumeCalls = append(m.DeleteSubvolumeCalls, path)
	return m.DeleteSubvolumeErr
}

func (m *MockManager) SubvolumeExists(path string) (bool, error) {
	m.SubvolumeExistsCalls = append(m.SubvolumeExistsCalls, path)
	return m.SubvolumeExistsResult, m.SubvolumeExistsErr
}

func (m *MockManager) CreateSnapshot(src, dst string, readonly bool) error {
	m.CreateSnapshotCalls = append(m.CreateSnapshotCalls, SnapshotCall{
		Src:      src,
		Dst:      dst,
		Readonly: readonly,
	})
	return m.CreateSnapshotErr
}

func (m *MockManager) EnsureQuotaEnabled(mountpoint string) error {
	m.EnsureQuotaEnabledCalls = append(m.EnsureQuotaEnabledCalls, mountpoint)
	return m.EnsureQuotaEnabledErr
}

func (m *MockManager) SetQgroupLimit(path string, bytes uint64) error {
	m.SetQgroupLimitCalls = append(m.SetQgroupLimitCalls, QgroupLimitCall{
		Path:  path,
		Bytes: bytes,
	})
	return m.SetQgroupLimitErr
}

func (m *MockManager) RemoveQgroupLimit(path string) error {
	m.RemoveQgroupLimitCalls = append(m.RemoveQgroupLimitCalls, path)
	return m.RemoveQgroupLimitErr
}

func (m *MockManager) DestroyQgroup(path string) error {
	m.DestroyQgroupCalls = append(m.DestroyQgroupCalls, path)
	return m.DestroyQgroupErr
}

func (m *MockManager) GetQgroupUsage(path string) (*QgroupUsage, error) {
	m.GetQgroupUsageCalls = append(m.GetQgroupUsageCalls, path)
	return m.GetQgroupUsageResult, m.GetQgroupUsageErr
}

func (m *MockManager) GetFilesystemUsage(path string) (*FsUsage, error) {
	m.GetFilesystemUsageCalls = append(m.GetFilesystemUsageCalls, path)
	return m.GetFilesystemUsageResult, m.GetFilesystemUsageErr
}
