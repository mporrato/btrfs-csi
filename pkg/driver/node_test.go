package driver

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/container-storage-interface/spec/lib/go/csi"
	"github.com/guru/btrfs-csi/pkg/btrfs"
	"github.com/guru/btrfs-csi/pkg/state"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// MountCall records arguments passed to Mount.
type MountCall struct {
	Source  string
	Target  string
	FsType  string
	Options []string
}

// MockMounter is a test double that implements Mounter.
// It records all method calls and returns pre-configured results.
type MockMounter struct {
	// Mount
	MountCalls []MountCall
	MountErr   error

	// Unmount
	UnmountCalls []string
	UnmountErr   error

	// IsMountPoint
	IsMountPointCalls  []string
	IsMountPointResult bool
	IsMountPointErr    error
}

func (m *MockMounter) Mount(source, target, fsType string, options ...string) error {
	m.MountCalls = append(m.MountCalls, MountCall{
		Source:  source,
		Target:  target,
		FsType:  fsType,
		Options: options,
	})
	return m.MountErr
}

func (m *MockMounter) Unmount(target string) error {
	m.UnmountCalls = append(m.UnmountCalls, target)
	return m.UnmountErr
}

func (m *MockMounter) IsMountPoint(file string) (bool, error) {
	m.IsMountPointCalls = append(m.IsMountPointCalls, file)
	return m.IsMountPointResult, m.IsMountPointErr
}

// newTestDriverWithMounter creates a Driver with mock btrfs, mock mounter, and in-memory store.
func newTestDriverWithMounter() (*Driver, *btrfs.MockManager, *MockMounter, *memStore) {
	mock := &btrfs.MockManager{}
	mounter := &MockMounter{}
	store := newMemStore()
	d := NewDriver(mock, store, "test-node", "/tmp/btrfs-csi-test")
	d.mounter = mounter
	return d, mock, mounter, store
}

func TestNodePublishVolume_Success(t *testing.T) {
	d, _, mounter, store := newTestDriverWithMounter()

	// Create a volume in state
	vol := &state.Volume{
		ID:            "vol-123",
		Name:          "test-pvc",
		SubvolumePath: "/tmp/btrfs-csi-test/volumes/vol-123",
	}
	if err := store.SaveVolume(vol); err != nil {
		t.Fatalf("SaveVolume: %v", err)
	}

	// Create a temp target directory
	targetPath := filepath.Join(t.TempDir(), "target")
	if err := os.MkdirAll(targetPath, 0o755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}

	resp, err := d.NodePublishVolume(context.Background(), &csi.NodePublishVolumeRequest{
		VolumeId:   "vol-123",
		TargetPath: targetPath,
		VolumeCapability: &csi.VolumeCapability{
			AccessType: &csi.VolumeCapability_Mount{
				Mount: &csi.VolumeCapability_MountVolume{},
			},
			AccessMode: &csi.VolumeCapability_AccessMode{
				Mode: csi.VolumeCapability_AccessMode_SINGLE_NODE_WRITER,
			},
		},
	})
	if err != nil {
		t.Fatalf("NodePublishVolume: %v", err)
	}
	if resp == nil {
		t.Fatal("NodePublishVolume returned nil response")
	}

	// Assert bind mount was called with correct source and target
	if len(mounter.MountCalls) != 1 {
		t.Fatalf("expected 1 Mount call, got %d", len(mounter.MountCalls))
	}
	call := mounter.MountCalls[0]
	if call.Source != vol.SubvolumePath {
		t.Errorf("Mount source = %q, want %q", call.Source, vol.SubvolumePath)
	}
	if call.Target != targetPath {
		t.Errorf("Mount target = %q, want %q", call.Target, targetPath)
	}
	if call.FsType != "" {
		t.Errorf("Mount fsType = %q, want empty string for bind mount", call.FsType)
	}
}

func TestNodePublishVolume_Readonly(t *testing.T) {
	d, _, mounter, store := newTestDriverWithMounter()

	vol := &state.Volume{
		ID:            "vol-readonly",
		Name:          "test-pvc-ro",
		SubvolumePath: "/tmp/btrfs-csi-test/volumes/vol-readonly",
	}
	if err := store.SaveVolume(vol); err != nil {
		t.Fatalf("SaveVolume: %v", err)
	}

	targetPath := filepath.Join(t.TempDir(), "target")
	if err := os.MkdirAll(targetPath, 0o755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}

	_, err := d.NodePublishVolume(context.Background(), &csi.NodePublishVolumeRequest{
		VolumeId:   "vol-readonly",
		TargetPath: targetPath,
		Readonly:   true,
		VolumeCapability: &csi.VolumeCapability{
			AccessType: &csi.VolumeCapability_Mount{
				Mount: &csi.VolumeCapability_MountVolume{},
			},
			AccessMode: &csi.VolumeCapability_AccessMode{
				Mode: csi.VolumeCapability_AccessMode_SINGLE_NODE_READER_ONLY,
			},
		},
	})
	if err != nil {
		t.Fatalf("NodePublishVolume: %v", err)
	}

	if len(mounter.MountCalls) != 1 {
		t.Fatalf("expected 1 Mount call, got %d", len(mounter.MountCalls))
	}

	// Assert mount options include "ro"
	foundRo := false
	for _, opt := range mounter.MountCalls[0].Options {
		if opt == "ro" {
			foundRo = true
			break
		}
	}
	if !foundRo {
		t.Errorf("Mount options = %v, want to contain 'ro'", mounter.MountCalls[0].Options)
	}
}

func TestNodePublishVolume_MissingVolumeID(t *testing.T) {
	d, _, _, _ := newTestDriverWithMounter()

	_, err := d.NodePublishVolume(context.Background(), &csi.NodePublishVolumeRequest{
		TargetPath: "/tmp/target",
		VolumeCapability: &csi.VolumeCapability{
			AccessType: &csi.VolumeCapability_Mount{
				Mount: &csi.VolumeCapability_MountVolume{},
			},
			AccessMode: &csi.VolumeCapability_AccessMode{
				Mode: csi.VolumeCapability_AccessMode_SINGLE_NODE_WRITER,
			},
		},
	})
	if code := status.Code(err); code != codes.InvalidArgument {
		t.Errorf("expected InvalidArgument, got %v", code)
	}
}

func TestNodePublishVolume_MissingTargetPath(t *testing.T) {
	d, _, _, _ := newTestDriverWithMounter()

	_, err := d.NodePublishVolume(context.Background(), &csi.NodePublishVolumeRequest{
		VolumeId: "vol-123",
		VolumeCapability: &csi.VolumeCapability{
			AccessType: &csi.VolumeCapability_Mount{
				Mount: &csi.VolumeCapability_MountVolume{},
			},
			AccessMode: &csi.VolumeCapability_AccessMode{
				Mode: csi.VolumeCapability_AccessMode_SINGLE_NODE_WRITER,
			},
		},
	})
	if code := status.Code(err); code != codes.InvalidArgument {
		t.Errorf("expected InvalidArgument, got %v", code)
	}
}

func TestNodeUnpublishVolume_Success(t *testing.T) {
	d, _, mounter, _ := newTestDriverWithMounter()

	// Set up mock: target is a mount point
	mounter.IsMountPointResult = true

	targetPath := filepath.Join(t.TempDir(), "target")
	if err := os.MkdirAll(targetPath, 0o755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}

	resp, err := d.NodeUnpublishVolume(context.Background(), &csi.NodeUnpublishVolumeRequest{
		VolumeId:   "vol-123",
		TargetPath: targetPath,
	})
	if err != nil {
		t.Fatalf("NodeUnpublishVolume: %v", err)
	}
	if resp == nil {
		t.Fatal("NodeUnpublishVolume returned nil response")
	}

	// Assert unmount was called
	if len(mounter.UnmountCalls) != 1 {
		t.Fatalf("expected 1 Unmount call, got %d", len(mounter.UnmountCalls))
	}
	if mounter.UnmountCalls[0] != targetPath {
		t.Errorf("Unmount target = %q, want %q", mounter.UnmountCalls[0], targetPath)
	}

	// Assert target directory was removed
	if _, err := os.Stat(targetPath); !os.IsNotExist(err) {
		t.Errorf("target directory %q should have been removed", targetPath)
	}
}

func TestNodeUnpublishVolume_NotMounted(t *testing.T) {
	d, _, mounter, _ := newTestDriverWithMounter()

	// Set up mock: target is NOT a mount point
	mounter.IsMountPointResult = false

	targetPath := filepath.Join(t.TempDir(), "target")
	if err := os.MkdirAll(targetPath, 0o755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}

	// Should return success (idempotent)
	resp, err := d.NodeUnpublishVolume(context.Background(), &csi.NodeUnpublishVolumeRequest{
		VolumeId:   "vol-123",
		TargetPath: targetPath,
	})
	if err != nil {
		t.Fatalf("NodeUnpublishVolume: %v", err)
	}
	if resp == nil {
		t.Fatal("NodeUnpublishVolume returned nil response")
	}

	// Assert unmount was NOT called
	if len(mounter.UnmountCalls) != 0 {
		t.Errorf("expected 0 Unmount calls, got %d", len(mounter.UnmountCalls))
	}
}

func TestNodeGetInfo(t *testing.T) {
	d, _, _, _ := newTestDriverWithMounter()

	resp, err := d.NodeGetInfo(context.Background(), &csi.NodeGetInfoRequest{})
	if err != nil {
		t.Fatalf("NodeGetInfo: %v", err)
	}

	if resp.GetNodeId() != "test-node" {
		t.Errorf("NodeId = %q, want %q", resp.GetNodeId(), "test-node")
	}

	topology := resp.GetAccessibleTopology()
	if topology == nil {
		t.Fatal("AccessibleTopology is nil")
	}
	if got, ok := topology.Segments["topology.btrfs.csi.local/node"]; !ok {
		t.Error("topology key 'topology.btrfs.csi.local/node' not found")
	} else if got != "test-node" {
		t.Errorf("topology value = %q, want %q", got, "test-node")
	}
}

func TestNodeGetCapabilities(t *testing.T) {
	d, _, _, _ := newTestDriverWithMounter()

	resp, err := d.NodeGetCapabilities(context.Background(), &csi.NodeGetCapabilitiesRequest{})
	if err != nil {
		t.Fatalf("NodeGetCapabilities: %v", err)
	}

	foundVolumeStats := false
	for _, cap := range resp.GetCapabilities() {
		if rpc := cap.GetRpc(); rpc != nil {
			if rpc.GetType() == csi.NodeServiceCapability_RPC_GET_VOLUME_STATS {
				foundVolumeStats = true
			}
		}
	}
	if !foundVolumeStats {
		t.Error("NodeGetCapabilities: GET_VOLUME_STATS capability not found")
	}
}

func TestNodeGetVolumeStats(t *testing.T) {
	d, mock, mounter, store := newTestDriverWithMounter()

	// Create a volume in state
	vol := &state.Volume{
		ID:            "vol-stats",
		Name:          "test-pvc",
		SubvolumePath: "/tmp/btrfs-csi-test/volumes/vol-stats",
	}
	if err := store.SaveVolume(vol); err != nil {
		t.Fatalf("SaveVolume: %v", err)
	}

	// Simulate: volume is mounted at the target path
	mounter.IsMountPointResult = true

	// Configure mock qgroup usage
	mock.GetQgroupUsageResult = &btrfs.QgroupUsage{
		Referenced: 1024 * 1024 * 100,  // 100 MiB
		Exclusive:  1024 * 1024 * 50,   // 50 MiB
		MaxRfer:    1024 * 1024 * 1024, // 1 GiB
	}

	targetPath := filepath.Join(t.TempDir(), "target")
	if err := os.MkdirAll(targetPath, 0o755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}

	resp, err := d.NodeGetVolumeStats(context.Background(), &csi.NodeGetVolumeStatsRequest{
		VolumeId:   "vol-stats",
		VolumePath: targetPath,
	})
	if err != nil {
		t.Fatalf("NodeGetVolumeStats: %v", err)
	}

	// Assert usage stats returned
	usage := resp.GetUsage()
	if len(usage) == 0 {
		t.Fatal("NodeGetVolumeStats: no usage stats returned")
	}

	// Check for bytes usage
	foundBytes := false
	for _, u := range usage {
		if u.Unit == csi.VolumeUsage_BYTES {
			foundBytes = true
			if u.Total != 1024*1024*1024 {
				t.Errorf("Total bytes = %d, want %d", u.Total, 1024*1024*1024)
			}
			if u.Used != 1024*1024*100 {
				t.Errorf("Used bytes = %d, want %d", u.Used, 1024*1024*100)
			}
			if u.Available != 1024*1024*924 { // 1024 - 100 = 924
				t.Errorf("Available bytes = %d, want %d", u.Available, 1024*1024*924)
			}
		}
	}

	if !foundBytes {
		t.Error("NodeGetVolumeStats: BYTES usage not found")
	}

	// Verify GetQgroupUsage was called with the correct path
	if len(mock.GetQgroupUsageCalls) != 1 {
		t.Fatalf("expected 1 GetQgroupUsage call, got %d", len(mock.GetQgroupUsageCalls))
	}
	if mock.GetQgroupUsageCalls[0] != vol.SubvolumePath {
		t.Errorf("GetQgroupUsage path = %q, want %q", mock.GetQgroupUsageCalls[0], vol.SubvolumePath)
	}
}

func TestNodeGetVolumeStats_VolumeNotFound(t *testing.T) {
	d, _, _, _ := newTestDriverWithMounter()

	targetPath := filepath.Join(t.TempDir(), "target")
	if err := os.MkdirAll(targetPath, 0o755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}

	_, err := d.NodeGetVolumeStats(context.Background(), &csi.NodeGetVolumeStatsRequest{
		VolumeId:   "vol-nonexistent",
		VolumePath: targetPath,
	})
	if code := status.Code(err); code != codes.NotFound {
		t.Errorf("expected NotFound, got %v", code)
	}
}

func TestNodePublishVolume_VolumeNotFound(t *testing.T) {
	d, _, _, _ := newTestDriverWithMounter()

	targetPath := filepath.Join(t.TempDir(), "target")
	if err := os.MkdirAll(targetPath, 0o755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}

	_, err := d.NodePublishVolume(context.Background(), &csi.NodePublishVolumeRequest{
		VolumeId:   "vol-nonexistent",
		TargetPath: targetPath,
		VolumeCapability: &csi.VolumeCapability{
			AccessType: &csi.VolumeCapability_Mount{
				Mount: &csi.VolumeCapability_MountVolume{},
			},
			AccessMode: &csi.VolumeCapability_AccessMode{
				Mode: csi.VolumeCapability_AccessMode_SINGLE_NODE_WRITER,
			},
		},
	})
	if code := status.Code(err); code != codes.NotFound {
		t.Errorf("expected NotFound, got %v", code)
	}
}

func TestNodePublishVolume_AlreadyMounted(t *testing.T) {
	d, _, mounter, store := newTestDriverWithMounter()

	vol := &state.Volume{
		ID:            "vol-already",
		Name:          "test-pvc",
		SubvolumePath: "/tmp/btrfs-csi-test/volumes/vol-already",
	}
	if err := store.SaveVolume(vol); err != nil {
		t.Fatalf("SaveVolume: %v", err)
	}

	targetPath := filepath.Join(t.TempDir(), "target")
	if err := os.MkdirAll(targetPath, 0o755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}

	// Simulate: target is already mounted
	mounter.IsMountPointResult = true

	req := &csi.NodePublishVolumeRequest{
		VolumeId:   "vol-already",
		TargetPath: targetPath,
		VolumeCapability: &csi.VolumeCapability{
			AccessType: &csi.VolumeCapability_Mount{
				Mount: &csi.VolumeCapability_MountVolume{},
			},
			AccessMode: &csi.VolumeCapability_AccessMode{
				Mode: csi.VolumeCapability_AccessMode_SINGLE_NODE_WRITER,
			},
		},
	}

	// First call - should succeed (already mounted, idempotent)
	resp, err := d.NodePublishVolume(context.Background(), req)
	if err != nil {
		t.Fatalf("NodePublishVolume (first call): %v", err)
	}
	if resp == nil {
		t.Fatal("NodePublishVolume returned nil response")
	}

	// Assert mount was NOT called (already mounted)
	if len(mounter.MountCalls) != 0 {
		t.Errorf("expected 0 Mount calls (already mounted), got %d", len(mounter.MountCalls))
	}
}

func TestNodePublishVolume_PathTraversal(t *testing.T) {
	d, _, _, store := newTestDriverWithMounter()

	vol := &state.Volume{
		ID:            "vol-traversal",
		Name:          "test-pvc",
		SubvolumePath: "/tmp/btrfs-csi-test/volumes/vol-traversal",
	}
	if err := store.SaveVolume(vol); err != nil {
		t.Fatalf("SaveVolume: %v", err)
	}

	tests := []struct {
		name       string
		targetPath string
	}{
		{"dotdot prefix", "../../../etc/evil"},
		{"dotdot middle", "/tmp/btrfs-csi-test/../../etc/evil"},
		{"dotdot end", "/tmp/target/.."},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := d.NodePublishVolume(context.Background(), &csi.NodePublishVolumeRequest{
				VolumeId:   "vol-traversal",
				TargetPath: tt.targetPath,
				VolumeCapability: &csi.VolumeCapability{
					AccessType: &csi.VolumeCapability_Mount{
						Mount: &csi.VolumeCapability_MountVolume{},
					},
					AccessMode: &csi.VolumeCapability_AccessMode{
						Mode: csi.VolumeCapability_AccessMode_SINGLE_NODE_WRITER,
					},
				},
			})
			if code := status.Code(err); code != codes.InvalidArgument {
				t.Errorf("expected InvalidArgument for path %q, got %v: %v", tt.targetPath, code, err)
			}
		})
	}
}

func TestNodeUnpublishVolume_PathTraversal(t *testing.T) {
	d, _, _, _ := newTestDriverWithMounter()

	_, err := d.NodeUnpublishVolume(context.Background(), &csi.NodeUnpublishVolumeRequest{
		VolumeId:   "vol-123",
		TargetPath: "../../../etc/evil",
	})
	if code := status.Code(err); code != codes.InvalidArgument {
		t.Errorf("expected InvalidArgument, got %v: %v", code, err)
	}
}

func TestNodeUnpublishVolume_UnmountError(t *testing.T) {
	d, _, mounter, _ := newTestDriverWithMounter()

	// Set up mock: target is a mount point
	mounter.IsMountPointResult = true
	mounter.UnmountErr = fmt.Errorf("unmount failed: device busy")

	targetPath := filepath.Join(t.TempDir(), "target")
	if err := os.MkdirAll(targetPath, 0o755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}

	_, err := d.NodeUnpublishVolume(context.Background(), &csi.NodeUnpublishVolumeRequest{
		VolumeId:   "vol-123",
		TargetPath: targetPath,
	})
	if code := status.Code(err); code != codes.Internal {
		t.Errorf("expected Internal, got %v: %v", code, err)
	}
}

func TestNodeUnpublishVolume_IsMountPointError(t *testing.T) {
	d, _, mounter, _ := newTestDriverWithMounter()

	// Set up mock: IsMountPoint returns an error
	mounter.IsMountPointErr = fmt.Errorf("lstat failed: no such file or directory")

	targetPath := filepath.Join(t.TempDir(), "target")
	if err := os.MkdirAll(targetPath, 0o755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}

	_, err := d.NodeUnpublishVolume(context.Background(), &csi.NodeUnpublishVolumeRequest{
		VolumeId:   "vol-123",
		TargetPath: targetPath,
	})
	if code := status.Code(err); code != codes.Internal {
		t.Errorf("expected Internal, got %v: %v", code, err)
	}

	// Assert unmount was NOT called when IsMountPoint fails
	if len(mounter.UnmountCalls) != 0 {
		t.Errorf("expected 0 Unmount calls when IsMountPoint fails, got %d", len(mounter.UnmountCalls))
	}
}

func TestNodeGetVolumeStats_GetQgroupUsageError(t *testing.T) {
	d, mock, mounter, store := newTestDriverWithMounter()

	vol := &state.Volume{
		ID:            "vol-qgroup-err",
		Name:          "test-pvc",
		SubvolumePath: "/tmp/btrfs-csi-test/volumes/vol-qgroup-err",
	}
	if err := store.SaveVolume(vol); err != nil {
		t.Fatalf("SaveVolume: %v", err)
	}

	// Simulate: volume is mounted at the target path
	mounter.IsMountPointResult = true

	// Configure mock to return error
	mock.GetQgroupUsageErr = fmt.Errorf("qgroup command failed")

	targetPath := filepath.Join(t.TempDir(), "target")
	if err := os.MkdirAll(targetPath, 0o755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}

	_, err := d.NodeGetVolumeStats(context.Background(), &csi.NodeGetVolumeStatsRequest{
		VolumeId:   "vol-qgroup-err",
		VolumePath: targetPath,
	})
	if code := status.Code(err); code != codes.Internal {
		t.Errorf("expected Internal, got %v: %v", code, err)
	}
}

func TestNodeExpandVolume(t *testing.T) {
	d, _, _, _ := newTestDriverWithMounter()

	resp, err := d.NodeExpandVolume(context.Background(), &csi.NodeExpandVolumeRequest{
		VolumeId: "vol-expand",
	})
	if err != nil {
		t.Fatalf("NodeExpandVolume: %v", err)
	}
	if resp == nil {
		t.Fatal("NodeExpandVolume returned nil response")
	}
}

func TestValidatePath_RelativePath(t *testing.T) {
	tests := []struct {
		name string
		path string
	}{
		{"bare relative", "relative/path"},
		{"dot prefix", "./relative"},
		{"no slash", "target"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := validatePath(tt.path)
			if code := status.Code(err); code != codes.InvalidArgument {
				t.Errorf("validatePath(%q) = %v, want InvalidArgument", tt.path, code)
			}
		})
	}
}

func TestNodePublishVolume_BindMountOption(t *testing.T) {
	d, _, mounter, store := newTestDriverWithMounter()

	vol := &state.Volume{
		ID:            "vol-bind",
		Name:          "test-pvc",
		SubvolumePath: "/tmp/btrfs-csi-test/volumes/vol-bind",
	}
	if err := store.SaveVolume(vol); err != nil {
		t.Fatalf("SaveVolume: %v", err)
	}

	targetPath := filepath.Join(t.TempDir(), "target")
	if err := os.MkdirAll(targetPath, 0o755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}

	_, err := d.NodePublishVolume(context.Background(), &csi.NodePublishVolumeRequest{
		VolumeId:   "vol-bind",
		TargetPath: targetPath,
		VolumeCapability: &csi.VolumeCapability{
			AccessType: &csi.VolumeCapability_Mount{
				Mount: &csi.VolumeCapability_MountVolume{},
			},
			AccessMode: &csi.VolumeCapability_AccessMode{
				Mode: csi.VolumeCapability_AccessMode_SINGLE_NODE_WRITER,
			},
		},
	})
	if err != nil {
		t.Fatalf("NodePublishVolume: %v", err)
	}

	if len(mounter.MountCalls) != 1 {
		t.Fatalf("expected 1 Mount call, got %d", len(mounter.MountCalls))
	}
	call := mounter.MountCalls[0]

	// bind mount must use "bind" in options, not fsType
	foundBind := false
	for _, opt := range call.Options {
		if opt == "bind" {
			foundBind = true
		}
	}
	if !foundBind {
		t.Errorf("Mount options = %v, want to contain 'bind'", call.Options)
	}
	if call.FsType != "" {
		t.Errorf("Mount fsType = %q, want empty string for bind mount", call.FsType)
	}
}

func TestNodeGetVolumeStats_PathNotMounted(t *testing.T) {
	d, _, mounter, store := newTestDriverWithMounter()

	vol := &state.Volume{
		ID:            "vol-notmounted",
		Name:          "test-pvc",
		SubvolumePath: "/tmp/btrfs-csi-test/volumes/vol-notmounted",
	}
	if err := store.SaveVolume(vol); err != nil {
		t.Fatalf("SaveVolume: %v", err)
	}

	// IsMountPointResult defaults to false — volume path is not mounted
	mounter.IsMountPointResult = false

	targetPath := filepath.Join(t.TempDir(), "target")
	if err := os.MkdirAll(targetPath, 0o755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}

	_, err := d.NodeGetVolumeStats(context.Background(), &csi.NodeGetVolumeStatsRequest{
		VolumeId:   "vol-notmounted",
		VolumePath: targetPath,
	})
	if code := status.Code(err); code != codes.NotFound {
		t.Errorf("expected NotFound when volume path not mounted, got %v: %v", code, err)
	}
}

func TestNodePublishVolume_MountError(t *testing.T) {
	d, _, mounter, store := newTestDriverWithMounter()

	// Configure mock to return error
	mounter.MountErr = fmt.Errorf("mount failed: device busy")

	vol := &state.Volume{
		ID:            "vol-mounterr",
		Name:          "test-pvc",
		SubvolumePath: "/tmp/btrfs-csi-test/volumes/vol-mounterr",
	}
	if err := store.SaveVolume(vol); err != nil {
		t.Fatalf("SaveVolume: %v", err)
	}

	targetPath := filepath.Join(t.TempDir(), "target")
	if err := os.MkdirAll(targetPath, 0o755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}

	_, err := d.NodePublishVolume(context.Background(), &csi.NodePublishVolumeRequest{
		VolumeId:   "vol-mounterr",
		TargetPath: targetPath,
		VolumeCapability: &csi.VolumeCapability{
			AccessType: &csi.VolumeCapability_Mount{
				Mount: &csi.VolumeCapability_MountVolume{},
			},
			AccessMode: &csi.VolumeCapability_AccessMode{
				Mode: csi.VolumeCapability_AccessMode_SINGLE_NODE_WRITER,
			},
		},
	})
	if code := status.Code(err); code != codes.Internal {
		t.Errorf("expected Internal, got %v", code)
	}
}
