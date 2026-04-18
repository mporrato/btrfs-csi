package btrfs

import (
	"context"
	"errors"
	"fmt"
	"testing"
)

// Compile-time check that MockManager implements Manager.
var _ Manager = (*MockManager)(nil)

func TestMockManagerCreateSubvolume(t *testing.T) {
	m := &MockManager{}
	if err := m.CreateSubvolume(context.Background(), "/volumes/vol1"); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(m.CreateSubvolumeCalls) != 1 {
		t.Fatalf("expected 1 call, got %d", len(m.CreateSubvolumeCalls))
	}
	if m.CreateSubvolumeCalls[0] != "/volumes/vol1" {
		t.Errorf("expected path /volumes/vol1, got %s", m.CreateSubvolumeCalls[0])
	}
}

func TestMockManagerCreateSubvolumeError(t *testing.T) {
	m := &MockManager{
		CreateSubvolumeErr: errTest,
	}
	if err := m.CreateSubvolume(context.Background(), "/volumes/vol1"); !errors.Is(err, errTest) {
		t.Fatalf("expected errTest, got %v", err)
	}
}

func TestMockManagerDeleteSubvolume(t *testing.T) {
	m := &MockManager{}
	if err := m.DeleteSubvolume(context.Background(), "/volumes/vol1"); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(m.DeleteSubvolumeCalls) != 1 {
		t.Fatalf("expected 1 call, got %d", len(m.DeleteSubvolumeCalls))
	}
	if m.DeleteSubvolumeCalls[0] != "/volumes/vol1" {
		t.Errorf("expected path /volumes/vol1, got %s", m.DeleteSubvolumeCalls[0])
	}
}

func TestMockManagerDeleteSubvolumeError(t *testing.T) {
	m := &MockManager{
		DeleteSubvolumeErr: errTest,
	}
	if err := m.DeleteSubvolume(context.Background(), "/volumes/vol1"); !errors.Is(err, errTest) {
		t.Fatalf("expected errTest, got %v", err)
	}
}

func TestMockManagerSubvolumeExists(t *testing.T) {
	m := &MockManager{
		SubvolumeExistsResult: true,
	}
	exists, err := m.SubvolumeExists(context.Background(), "/volumes/vol1")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !exists {
		t.Error("expected exists=true")
	}
	if len(m.SubvolumeExistsCalls) != 1 {
		t.Fatalf("expected 1 call, got %d", len(m.SubvolumeExistsCalls))
	}
}

func TestMockManagerSubvolumeExistsError(t *testing.T) {
	m := &MockManager{
		SubvolumeExistsErr: errTest,
	}
	_, err := m.SubvolumeExists(context.Background(), "/volumes/vol1")
	if !errors.Is(err, errTest) {
		t.Fatalf("expected errTest, got %v", err)
	}
}

func TestMockManagerCreateSnapshot(t *testing.T) {
	m := &MockManager{}
	if err := m.CreateSnapshot(context.Background(), "/volumes/vol1", "/snapshots/snap1", true); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(m.CreateSnapshotCalls) != 1 {
		t.Fatalf("expected 1 call, got %d", len(m.CreateSnapshotCalls))
	}
	call := m.CreateSnapshotCalls[0]
	if call.Src != "/volumes/vol1" || call.Dst != "/snapshots/snap1" || call.Readonly != true {
		t.Errorf("unexpected call args: %+v", call)
	}
}

func TestMockManagerCreateSnapshotError(t *testing.T) {
	m := &MockManager{
		CreateSnapshotErr: errTest,
	}
	if err := m.CreateSnapshot(context.Background(), "/volumes/vol1", "/snapshots/snap1", false); !errors.Is(err, errTest) {
		t.Fatalf("expected errTest, got %v", err)
	}
}

func TestMockManagerEnsureQuotaEnabled(t *testing.T) {
	m := &MockManager{}
	if err := m.EnsureQuotaEnabled(context.Background(), "/mnt/btrfs"); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(m.EnsureQuotaEnabledCalls) != 1 {
		t.Fatalf("expected 1 call, got %d", len(m.EnsureQuotaEnabledCalls))
	}
	if m.EnsureQuotaEnabledCalls[0] != "/mnt/btrfs" {
		t.Errorf("expected mountpoint /mnt/btrfs, got %s", m.EnsureQuotaEnabledCalls[0])
	}
}

func TestMockManagerEnsureQuotaEnabledError(t *testing.T) {
	m := &MockManager{
		EnsureQuotaEnabledErr: errTest,
	}
	if err := m.EnsureQuotaEnabled(context.Background(), "/mnt/btrfs"); !errors.Is(err, errTest) {
		t.Fatalf("expected errTest, got %v", err)
	}
}

func TestMockManagerSetQgroupLimit(t *testing.T) {
	m := &MockManager{}
	if err := m.SetQgroupLimit(context.Background(), "/volumes/vol1", 1024); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(m.SetQgroupLimitCalls) != 1 {
		t.Fatalf("expected 1 call, got %d", len(m.SetQgroupLimitCalls))
	}
	call := m.SetQgroupLimitCalls[0]
	if call.Path != "/volumes/vol1" || call.Bytes != 1024 {
		t.Errorf("unexpected call args: %+v", call)
	}
}

func TestMockManagerSetQgroupLimitError(t *testing.T) {
	m := &MockManager{
		SetQgroupLimitErr: errTest,
	}
	if err := m.SetQgroupLimit(context.Background(), "/volumes/vol1", 1024); !errors.Is(err, errTest) {
		t.Fatalf("expected errTest, got %v", err)
	}
}

func TestMockManagerRemoveQgroupLimit(t *testing.T) {
	m := &MockManager{}
	if err := m.RemoveQgroupLimit(context.Background(), "/volumes/vol1"); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(m.RemoveQgroupLimitCalls) != 1 {
		t.Fatalf("expected 1 call, got %d", len(m.RemoveQgroupLimitCalls))
	}
	if m.RemoveQgroupLimitCalls[0] != "/volumes/vol1" {
		t.Errorf("expected path /volumes/vol1, got %s", m.RemoveQgroupLimitCalls[0])
	}
}

func TestMockManagerRemoveQgroupLimitError(t *testing.T) {
	m := &MockManager{
		RemoveQgroupLimitErr: errTest,
	}
	if err := m.RemoveQgroupLimit(context.Background(), "/volumes/vol1"); !errors.Is(err, errTest) {
		t.Fatalf("expected errTest, got %v", err)
	}
}

func TestMockManagerClearStaleQgroups(t *testing.T) {
	m := &MockManager{ClearStaleQgroupsResult: 5}
	count, err := m.ClearStaleQgroups(context.Background(), "/mnt/btrfs")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if count != 5 {
		t.Errorf("expected count 5, got %d", count)
	}
	if len(m.ClearStaleQgroupsCalls) != 1 {
		t.Fatalf("expected 1 call, got %d", len(m.ClearStaleQgroupsCalls))
	}
	if m.ClearStaleQgroupsCalls[0] != "/mnt/btrfs" {
		t.Errorf("expected mountpoint /mnt/btrfs, got %s", m.ClearStaleQgroupsCalls[0])
	}
}

func TestMockManagerClearStaleQgroupsError(t *testing.T) {
	m := &MockManager{ClearStaleQgroupsErr: errTest}
	_, err := m.ClearStaleQgroups(context.Background(), "/mnt/btrfs")
	if !errors.Is(err, errTest) {
		t.Fatalf("expected errTest, got %v", err)
	}
}

func TestMockManagerGetQgroupUsage(t *testing.T) {
	want := &QgroupUsage{Referenced: 100, Exclusive: 50, MaxRfer: 200}
	m := &MockManager{
		GetQgroupUsageResult: want,
	}
	got, err := m.GetQgroupUsage(context.Background(), "/volumes/vol1")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != want {
		t.Errorf("expected %+v, got %+v", want, got)
	}
	if len(m.GetQgroupUsageCalls) != 1 {
		t.Fatalf("expected 1 call, got %d", len(m.GetQgroupUsageCalls))
	}
}

func TestMockManagerGetQgroupUsageError(t *testing.T) {
	m := &MockManager{
		GetQgroupUsageErr: errTest,
	}
	_, err := m.GetQgroupUsage(context.Background(), "/volumes/vol1")
	if !errors.Is(err, errTest) {
		t.Fatalf("expected errTest, got %v", err)
	}
}

func TestMockManagerGetFilesystemUsage(t *testing.T) {
	want := &FsUsage{Total: 1000, Used: 400, Available: 600}
	m := &MockManager{
		GetFilesystemUsageResult: want,
	}
	got, err := m.GetFilesystemUsage(context.Background(), "/mnt/btrfs")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != want {
		t.Errorf("expected %+v, got %+v", want, got)
	}
	if len(m.GetFilesystemUsageCalls) != 1 {
		t.Fatalf("expected 1 call, got %d", len(m.GetFilesystemUsageCalls))
	}
}

func TestMockManagerGetFilesystemUsageError(t *testing.T) {
	m := &MockManager{
		GetFilesystemUsageErr: errTest,
	}
	_, err := m.GetFilesystemUsage(context.Background(), "/mnt/btrfs")
	if !errors.Is(err, errTest) {
		t.Fatalf("expected errTest, got %v", err)
	}
}

func TestMockManagerIsBtrfsFilesystem(t *testing.T) {
	m := &MockManager{IsBtrfsFilesystemResult: true}
	got, err := m.IsBtrfsFilesystem(context.Background(), "/mnt/btrfs")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !got {
		t.Error("expected true for btrfs path")
	}
	if len(m.IsBtrfsFilesystemCalls) != 1 {
		t.Fatalf("expected 1 call, got %d", len(m.IsBtrfsFilesystemCalls))
	}
	if m.IsBtrfsFilesystemCalls[0] != "/mnt/btrfs" {
		t.Errorf("expected path /mnt/btrfs, got %s", m.IsBtrfsFilesystemCalls[0])
	}
}

func TestMockManagerIsBtrfsFilesystemNotBtrfs(t *testing.T) {
	m := &MockManager{IsBtrfsFilesystemResult: false}
	got, err := m.IsBtrfsFilesystem(context.Background(), "/tmp")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got {
		t.Error("expected false for non-btrfs path")
	}
}

func TestMockManagerIsBtrfsFilesystemError(t *testing.T) {
	m := &MockManager{IsBtrfsFilesystemErr: errTest}
	_, err := m.IsBtrfsFilesystem(context.Background(), "/tmp")
	if !errors.Is(err, errTest) {
		t.Fatalf("expected errTest, got %v", err)
	}
}

func TestMockManagerIsMountpoint(t *testing.T) {
	m := &MockManager{IsMountpointResult: true}
	got, err := m.IsMountpoint(context.Background(), "/mnt/pool")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !got {
		t.Error("expected true for mountpoint")
	}
	if len(m.IsMountpointCalls) != 1 {
		t.Fatalf("expected 1 call, got %d", len(m.IsMountpointCalls))
	}
	if m.IsMountpointCalls[0] != "/mnt/pool" {
		t.Errorf("expected path /mnt/pool, got %s", m.IsMountpointCalls[0])
	}
}

func TestMockManagerIsMountpointFalse(t *testing.T) {
	m := &MockManager{IsMountpointResult: false}
	got, err := m.IsMountpoint(context.Background(), "/tmp/notmount")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got {
		t.Error("expected false for non-mountpoint")
	}
}

func TestMockManagerIsMountpointError(t *testing.T) {
	m := &MockManager{IsMountpointErr: errTest}
	_, err := m.IsMountpoint(context.Background(), "/tmp")
	if !errors.Is(err, errTest) {
		t.Fatalf("expected errTest, got %v", err)
	}
}

// errTest is a sentinel error used in mock tests.
var errTest = fmt.Errorf("test error")
