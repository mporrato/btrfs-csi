package driver

import (
	"context"
	"path/filepath"
	"strings"
	"testing"

	"github.com/container-storage-interface/spec/lib/go/csi"
	"github.com/guru/btrfs-csi/pkg/state"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

func singleNodeWriterCap() []*csi.VolumeCapability {
	return []*csi.VolumeCapability{
		{
			AccessType: &csi.VolumeCapability_Mount{Mount: &csi.VolumeCapability_MountVolume{}},
			AccessMode: &csi.VolumeCapability_AccessMode{Mode: csi.VolumeCapability_AccessMode_SINGLE_NODE_WRITER},
		},
	}
}

func TestCreateVolume_NewVolume(t *testing.T) {
	d, mock, store := newTestDriverWithMock()

	resp, err := d.CreateVolume(context.Background(), &csi.CreateVolumeRequest{
		Name:               "test-pvc",
		CapacityRange:      &csi.CapacityRange{RequiredBytes: 1 << 30},
		VolumeCapabilities: singleNodeWriterCap(),
	})
	if err != nil {
		t.Fatalf("CreateVolume: %v", err)
	}

	if len(mock.CreateSubvolumeCalls) != 1 {
		t.Fatalf("expected 1 CreateSubvolume call, got %d", len(mock.CreateSubvolumeCalls))
	}

	if len(mock.EnsureQuotaEnabledCalls) != 1 {
		t.Fatalf("expected 1 EnsureQuotaEnabled call, got %d", len(mock.EnsureQuotaEnabledCalls))
	}
	if got := mock.EnsureQuotaEnabledCalls[0]; got != "/tmp/btrfs-csi-test" {
		t.Errorf("EnsureQuotaEnabled mountpoint = %q, want %q", got, "/tmp/btrfs-csi-test")
	}

	if len(mock.SetQgroupLimitCalls) != 1 {
		t.Fatalf("expected 1 SetQgroupLimit call, got %d", len(mock.SetQgroupLimitCalls))
	}
	if got := mock.SetQgroupLimitCalls[0].Bytes; got != 1<<30 {
		t.Errorf("SetQgroupLimit bytes = %d, want %d", got, 1<<30)
	}

	if resp.Volume.VolumeId == "" {
		t.Fatal("expected non-empty volume ID in response")
	}

	if len(resp.Volume.AccessibleTopology) != 1 {
		t.Fatalf("expected 1 topology entry, got %d", len(resp.Volume.AccessibleTopology))
	}
	if got := resp.Volume.AccessibleTopology[0].Segments["topology.btrfs.csi.local/node"]; got != "test-node" {
		t.Errorf("topology node = %q, want %q", got, "test-node")
	}

	if _, ok := store.GetVolume(resp.Volume.VolumeId); !ok {
		t.Error("volume not found in state after CreateVolume")
	}
}

func TestCreateVolume_Idempotent(t *testing.T) {
	d, mock, _ := newTestDriverWithMock()

	req := &csi.CreateVolumeRequest{
		Name:               "test-pvc",
		CapacityRange:      &csi.CapacityRange{RequiredBytes: 1 << 30},
		VolumeCapabilities: singleNodeWriterCap(),
	}

	resp1, err := d.CreateVolume(context.Background(), req)
	if err != nil {
		t.Fatalf("first CreateVolume: %v", err)
	}

	resp2, err := d.CreateVolume(context.Background(), req)
	if err != nil {
		t.Fatalf("second CreateVolume: %v", err)
	}

	if resp1.Volume.VolumeId != resp2.Volume.VolumeId {
		t.Errorf("volume ID changed on idempotent call: %q → %q", resp1.Volume.VolumeId, resp2.Volume.VolumeId)
	}

	if len(mock.CreateSubvolumeCalls) != 1 {
		t.Errorf("CreateSubvolume called %d times, want 1", len(mock.CreateSubvolumeCalls))
	}
}

func TestCreateVolume_MissingName(t *testing.T) {
	d, _, _ := newTestDriverWithMock()

	_, err := d.CreateVolume(context.Background(), &csi.CreateVolumeRequest{
		VolumeCapabilities: singleNodeWriterCap(),
	})
	if code := status.Code(err); code != codes.InvalidArgument {
		t.Errorf("expected InvalidArgument, got %v", code)
	}
}

func TestCreateVolume_MissingCapabilities(t *testing.T) {
	d, _, _ := newTestDriverWithMock()

	_, err := d.CreateVolume(context.Background(), &csi.CreateVolumeRequest{
		Name: "test-pvc",
	})
	if code := status.Code(err); code != codes.InvalidArgument {
		t.Errorf("expected InvalidArgument, got %v", code)
	}
}

func TestCreateVolume_WithBasePath(t *testing.T) {
	d, mock, _ := newTestDriverWithMock()

	_, err := d.CreateVolume(context.Background(), &csi.CreateVolumeRequest{
		Name:               "test-pvc",
		CapacityRange:      &csi.CapacityRange{RequiredBytes: 1 << 30},
		VolumeCapabilities: singleNodeWriterCap(),
		Parameters:         map[string]string{"basePath": "/custom/basepath"},
	})
	if err != nil {
		t.Fatalf("CreateVolume: %v", err)
	}

	if len(mock.CreateSubvolumeCalls) != 1 {
		t.Fatalf("expected 1 CreateSubvolume call, got %d", len(mock.CreateSubvolumeCalls))
	}
	wantPrefix := filepath.Join("/custom/basepath", "volumes") + "/"
	if !strings.HasPrefix(mock.CreateSubvolumeCalls[0], wantPrefix) {
		t.Errorf("subvolume path %q does not start with %q", mock.CreateSubvolumeCalls[0], wantPrefix)
	}

	if len(mock.EnsureQuotaEnabledCalls) != 1 {
		t.Fatalf("expected 1 EnsureQuotaEnabled call, got %d", len(mock.EnsureQuotaEnabledCalls))
	}
	if got := mock.EnsureQuotaEnabledCalls[0]; got != "/custom/basepath" {
		t.Errorf("EnsureQuotaEnabled mountpoint = %q, want %q", got, "/custom/basepath")
	}
}

func TestDeleteVolume_Exists(t *testing.T) {
	d, mock, store := newTestDriverWithMock()

	vol := &state.Volume{
		ID:            "vol-abc",
		Name:          "test-pvc",
		SubvolumePath: "/tmp/btrfs-csi-test/volumes/vol-abc",
	}
	if err := store.SaveVolume(vol); err != nil {
		t.Fatalf("SaveVolume: %v", err)
	}

	_, err := d.DeleteVolume(context.Background(), &csi.DeleteVolumeRequest{VolumeId: "vol-abc"})
	if err != nil {
		t.Fatalf("DeleteVolume: %v", err)
	}

	if len(mock.DeleteSubvolumeCalls) != 1 {
		t.Fatalf("expected 1 DeleteSubvolume call, got %d", len(mock.DeleteSubvolumeCalls))
	}
	if mock.DeleteSubvolumeCalls[0] != vol.SubvolumePath {
		t.Errorf("DeleteSubvolume path = %q, want %q", mock.DeleteSubvolumeCalls[0], vol.SubvolumePath)
	}

	if _, ok := store.GetVolume("vol-abc"); ok {
		t.Error("volume still in state after DeleteVolume")
	}
}

func TestDeleteVolume_NotFound(t *testing.T) {
	d, mock, _ := newTestDriverWithMock()

	_, err := d.DeleteVolume(context.Background(), &csi.DeleteVolumeRequest{VolumeId: "vol-nonexistent"})
	if err != nil {
		t.Fatalf("expected success for missing volume, got: %v", err)
	}

	if len(mock.DeleteSubvolumeCalls) != 0 {
		t.Errorf("expected no DeleteSubvolume calls, got %d", len(mock.DeleteSubvolumeCalls))
	}
}

func TestDeleteVolume_MissingID(t *testing.T) {
	d, _, _ := newTestDriverWithMock()

	_, err := d.DeleteVolume(context.Background(), &csi.DeleteVolumeRequest{})
	if code := status.Code(err); code != codes.InvalidArgument {
		t.Errorf("expected InvalidArgument, got %v", code)
	}
}
