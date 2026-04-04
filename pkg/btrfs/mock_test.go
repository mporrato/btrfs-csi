package btrfs

import (
	"fmt"
	"testing"
)

// Compile-time check that MockManager implements Manager.
var _ Manager = (*MockManager)(nil)

func TestMockManagerCreateSubvolume(t *testing.T) {
	m := &MockManager{}
	if err := m.CreateSubvolume("/volumes/vol1"); err != nil {
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
	if err := m.CreateSubvolume("/volumes/vol1"); err != errTest {
		t.Fatalf("expected errTest, got %v", err)
	}
}

func TestMockManagerDeleteSubvolume(t *testing.T) {
	m := &MockManager{}
	if err := m.DeleteSubvolume("/volumes/vol1"); err != nil {
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
	if err := m.DeleteSubvolume("/volumes/vol1"); err != errTest {
		t.Fatalf("expected errTest, got %v", err)
	}
}

func TestMockManagerSubvolumeExists(t *testing.T) {
	m := &MockManager{
		SubvolumeExistsResult: true,
	}
	exists, err := m.SubvolumeExists("/volumes/vol1")
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
	_, err := m.SubvolumeExists("/volumes/vol1")
	if err != errTest {
		t.Fatalf("expected errTest, got %v", err)
	}
}

func TestMockManagerCreateSnapshot(t *testing.T) {
	m := &MockManager{}
	if err := m.CreateSnapshot("/volumes/vol1", "/snapshots/snap1", true); err != nil {
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
	if err := m.CreateSnapshot("/volumes/vol1", "/snapshots/snap1", false); err != errTest {
		t.Fatalf("expected errTest, got %v", err)
	}
}

func TestMockManagerEnsureQuotaEnabled(t *testing.T) {
	m := &MockManager{}
	if err := m.EnsureQuotaEnabled("/mnt/btrfs"); err != nil {
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
	if err := m.EnsureQuotaEnabled("/mnt/btrfs"); err != errTest {
		t.Fatalf("expected errTest, got %v", err)
	}
}

func TestMockManagerSetQgroupLimit(t *testing.T) {
	m := &MockManager{}
	if err := m.SetQgroupLimit("/volumes/vol1", 1024); err != nil {
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
	if err := m.SetQgroupLimit("/volumes/vol1", 1024); err != errTest {
		t.Fatalf("expected errTest, got %v", err)
	}
}

func TestMockManagerRemoveQgroupLimit(t *testing.T) {
	m := &MockManager{}
	if err := m.RemoveQgroupLimit("/volumes/vol1"); err != nil {
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
	if err := m.RemoveQgroupLimit("/volumes/vol1"); err != errTest {
		t.Fatalf("expected errTest, got %v", err)
	}
}

func TestMockManagerDestroyQgroup(t *testing.T) {
	m := &MockManager{}
	if err := m.DestroyQgroup("/volumes/vol1"); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(m.DestroyQgroupCalls) != 1 {
		t.Fatalf("expected 1 call, got %d", len(m.DestroyQgroupCalls))
	}
	if m.DestroyQgroupCalls[0] != "/volumes/vol1" {
		t.Errorf("expected path /volumes/vol1, got %s", m.DestroyQgroupCalls[0])
	}
}

func TestMockManagerDestroyQgroupError(t *testing.T) {
	m := &MockManager{
		DestroyQgroupErr: errTest,
	}
	if err := m.DestroyQgroup("/volumes/vol1"); err != errTest {
		t.Fatalf("expected errTest, got %v", err)
	}
}

func TestMockManagerGetQgroupUsage(t *testing.T) {
	want := &QgroupUsage{Referenced: 100, Exclusive: 50, MaxRfer: 200}
	m := &MockManager{
		GetQgroupUsageResult: want,
	}
	got, err := m.GetQgroupUsage("/volumes/vol1")
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
	_, err := m.GetQgroupUsage("/volumes/vol1")
	if err != errTest {
		t.Fatalf("expected errTest, got %v", err)
	}
}

func TestMockManagerGetFilesystemUsage(t *testing.T) {
	want := &FsUsage{Total: 1000, Used: 400, Available: 600}
	m := &MockManager{
		GetFilesystemUsageResult: want,
	}
	got, err := m.GetFilesystemUsage("/mnt/btrfs")
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
	_, err := m.GetFilesystemUsage("/mnt/btrfs")
	if err != errTest {
		t.Fatalf("expected errTest, got %v", err)
	}
}

// errTest is a sentinel error used in mock tests.
var errTest = fmt.Errorf("test error")
