package driver

import (
	"context"
	"errors"
	"testing"

	"github.com/container-storage-interface/spec/lib/go/csi"
	"github.com/mporrato/btrfs-csi/pkg/btrfs"
	"github.com/mporrato/btrfs-csi/pkg/state"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

func TestControllerGetCapabilities(t *testing.T) {
	d := newTestDriver()

	resp, err := d.ControllerGetCapabilities(context.Background(), &csi.ControllerGetCapabilitiesRequest{})
	if err != nil {
		t.Fatalf("ControllerGetCapabilities: %v", err)
	}

	want := map[csi.ControllerServiceCapability_RPC_Type]bool{
		csi.ControllerServiceCapability_RPC_CREATE_DELETE_VOLUME:   false,
		csi.ControllerServiceCapability_RPC_CREATE_DELETE_SNAPSHOT: false,
		csi.ControllerServiceCapability_RPC_CLONE_VOLUME:           false,
		csi.ControllerServiceCapability_RPC_EXPAND_VOLUME:          false,
		csi.ControllerServiceCapability_RPC_GET_CAPACITY:           false,
		csi.ControllerServiceCapability_RPC_LIST_VOLUMES:           false,
		csi.ControllerServiceCapability_RPC_LIST_SNAPSHOTS:         false,
		csi.ControllerServiceCapability_RPC_GET_VOLUME:             false,
		csi.ControllerServiceCapability_RPC_VOLUME_CONDITION:       false,
	}

	for _, cap := range resp.Capabilities {
		rpc := cap.GetRpc()
		if rpc == nil {
			continue
		}
		if _, expected := want[rpc.Type]; expected {
			want[rpc.Type] = true
		} else {
			t.Errorf("unexpected capability: %v", rpc.Type)
		}
	}

	for cap, found := range want {
		if !found {
			t.Errorf("missing capability: %v", cap)
		}
	}
}

func TestValidateVolumeCapabilities_Supported(t *testing.T) {
	d, _, store := newTestDriverWithMock()
	if err := store.SaveVolume(&state.Volume{ID: "vol-123", Name: "test-pvc"}); err != nil {
		t.Fatalf("SaveVolume: %v", err)
	}

	caps := singleNodeWriterCap()
	resp, err := d.ValidateVolumeCapabilities(context.Background(), &csi.ValidateVolumeCapabilitiesRequest{
		VolumeId:           "vol-123",
		VolumeCapabilities: caps,
	})
	if err != nil {
		t.Fatalf("ValidateVolumeCapabilities: %v", err)
	}

	if resp.Confirmed == nil {
		t.Fatal("expected Confirmed to be set for supported capabilities")
	}
	if len(resp.Confirmed.VolumeCapabilities) != len(caps) {
		t.Errorf("confirmed caps count = %d, want %d", len(resp.Confirmed.VolumeCapabilities), len(caps))
	}
}

func TestValidateVolumeCapabilities_Unsupported(t *testing.T) {
	d, _, store := newTestDriverWithMock()
	if err := store.SaveVolume(&state.Volume{ID: "vol-123", Name: "test-pvc"}); err != nil {
		t.Fatalf("SaveVolume: %v", err)
	}

	resp, err := d.ValidateVolumeCapabilities(context.Background(), &csi.ValidateVolumeCapabilitiesRequest{
		VolumeId: "vol-123",
		VolumeCapabilities: []*csi.VolumeCapability{
			{
				AccessType: &csi.VolumeCapability_Mount{Mount: &csi.VolumeCapability_MountVolume{}},
				AccessMode: &csi.VolumeCapability_AccessMode{
					Mode: csi.VolumeCapability_AccessMode_MULTI_NODE_MULTI_WRITER,
				},
			},
		},
	})
	if err != nil {
		t.Fatalf("ValidateVolumeCapabilities: %v", err)
	}

	if resp.Confirmed != nil {
		t.Errorf("expected Confirmed to be nil for unsupported access mode, got %v", resp.Confirmed)
	}
}

func TestValidateVolumeCapabilities_VolumeNotFound(t *testing.T) {
	d := newTestDriver()

	_, err := d.ValidateVolumeCapabilities(context.Background(), &csi.ValidateVolumeCapabilitiesRequest{
		VolumeId:           "vol-nonexistent",
		VolumeCapabilities: singleNodeWriterCap(),
	})
	if code := status.Code(err); code != codes.NotFound {
		t.Errorf("expected NotFound, got %v", code)
	}
}

func TestGetCapacity_Success(t *testing.T) {
	d, mock, _ := newTestDriverWithMock()
	mock.GetFilesystemUsageResult = &btrfs.FsUsage{Total: 10 << 30, Used: 2 << 30, Available: 8 << 30}

	resp, err := d.GetCapacity(context.Background(), &csi.GetCapacityRequest{})
	if err != nil {
		t.Fatalf("GetCapacity: %v", err)
	}

	if resp.AvailableCapacity != int64(8<<30) {
		t.Errorf("AvailableCapacity = %d, want %d", resp.AvailableCapacity, int64(8<<30))
	}
	if len(mock.GetFilesystemUsageCalls) != 1 {
		t.Fatalf("expected 1 GetFilesystemUsage call, got %d", len(mock.GetFilesystemUsageCalls))
	}
	if mock.GetFilesystemUsageCalls[0] != "/tmp/btrfs-csi-test" {
		t.Errorf("GetFilesystemUsage path = %q, want /tmp/btrfs-csi-test", mock.GetFilesystemUsageCalls[0])
	}
}

func TestGetCapacity_Error(t *testing.T) {
	d, mock, _ := newTestDriverWithMock()
	mock.GetFilesystemUsageErr = errors.New("statfs failed")

	_, err := d.GetCapacity(context.Background(), &csi.GetCapacityRequest{})
	if code := status.Code(err); code != codes.Internal {
		t.Errorf("expected Internal, got %v", code)
	}
}
