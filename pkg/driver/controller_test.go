package driver

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/container-storage-interface/spec/lib/go/csi"
	"github.com/mporrato/btrfs-csi/pkg/state"
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
	d, mock, store := newTestDriverWithMock(t)

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
	if got := mock.EnsureQuotaEnabledCalls[0]; got != store.root() {
		t.Errorf("EnsureQuotaEnabled mountpoint = %q, want %q", got, store.root())
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
	d, mock, _ := newTestDriverWithMock(t)

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

func TestCreateVolume_CancelledContext(t *testing.T) {
	d, _, _ := newTestDriverWithMock(t)

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	_, err := d.CreateVolume(ctx, &csi.CreateVolumeRequest{
		Name:               "test-pvc",
		VolumeCapabilities: singleNodeWriterCap(),
	})
	if code := status.Code(err); code != codes.Canceled {
		t.Errorf("expected Canceled for canceled context, got %v", code)
	}
}

func TestCreateVolume_MissingName(t *testing.T) {
	d, _, _ := newTestDriverWithMock(t)

	_, err := d.CreateVolume(context.Background(), &csi.CreateVolumeRequest{
		VolumeCapabilities: singleNodeWriterCap(),
	})
	if code := status.Code(err); code != codes.InvalidArgument {
		t.Errorf("expected InvalidArgument, got %v", code)
	}
}

func TestCreateVolume_LimitBytesExceeded(t *testing.T) {
	d, _, _ := newTestDriverWithMock(t)

	_, err := d.CreateVolume(context.Background(), &csi.CreateVolumeRequest{
		Name:               "test-pvc",
		VolumeCapabilities: singleNodeWriterCap(),
		CapacityRange: &csi.CapacityRange{
			RequiredBytes: 2 << 30, // 2 GiB required
			LimitBytes:    1 << 30, // but only 1 GiB limit
		},
	})
	if code := status.Code(err); code != codes.InvalidArgument {
		t.Errorf("expected InvalidArgument when required > limit, got %v", code)
	}
}

func TestCreateVolume_MissingCapabilities(t *testing.T) {
	d, _, _ := newTestDriverWithMock(t)

	_, err := d.CreateVolume(context.Background(), &csi.CreateVolumeRequest{
		Name: "test-pvc",
	})
	if code := status.Code(err); code != codes.InvalidArgument {
		t.Errorf("expected InvalidArgument, got %v", code)
	}
}

func TestCreateVolume_UnsupportedAccessMode(t *testing.T) {
	d, _, _ := newTestDriverWithMock(t)

	_, err := d.CreateVolume(context.Background(), &csi.CreateVolumeRequest{
		Name:          "test-pvc",
		CapacityRange: &csi.CapacityRange{RequiredBytes: 1 << 30},
		VolumeCapabilities: []*csi.VolumeCapability{
			{
				AccessType: &csi.VolumeCapability_Mount{Mount: &csi.VolumeCapability_MountVolume{}},
				AccessMode: &csi.VolumeCapability_AccessMode{Mode: csi.VolumeCapability_AccessMode_MULTI_NODE_MULTI_WRITER},
			},
		},
	})
	if code := status.Code(err); code != codes.InvalidArgument {
		t.Errorf("expected InvalidArgument for unsupported access mode, got %v", code)
	}
}

func TestCreateVolume_BlockAccessNotSupported(t *testing.T) {
	d, _, _ := newTestDriverWithMock(t)

	_, err := d.CreateVolume(context.Background(), &csi.CreateVolumeRequest{
		Name:          "test-pvc",
		CapacityRange: &csi.CapacityRange{RequiredBytes: 1 << 30},
		VolumeCapabilities: []*csi.VolumeCapability{
			{
				AccessType: &csi.VolumeCapability_Block{Block: &csi.VolumeCapability_BlockVolume{}},
				AccessMode: &csi.VolumeCapability_AccessMode{Mode: csi.VolumeCapability_AccessMode_SINGLE_NODE_WRITER},
			},
		},
	})
	if code := status.Code(err); code != codes.InvalidArgument {
		t.Errorf("expected InvalidArgument for block access type, got %v", code)
	}
}

func TestCreateVolume_WithPoolParam(t *testing.T) {
	basePath := t.TempDir()
	d, mock, _ := newTestDriverWithMock(t)
	// Register the extra basePath so the MultiStore can route saves there.
	d.store.(*state.MultiStore).AddStoreForTest(basePath, newMemStore(basePath))
	d.SetPools(map[string]string{"fast": basePath})

	_, err := d.CreateVolume(context.Background(), &csi.CreateVolumeRequest{
		Name:               "test-pvc",
		CapacityRange:      &csi.CapacityRange{RequiredBytes: 1 << 30},
		VolumeCapabilities: singleNodeWriterCap(),
		Parameters:         map[string]string{"pool": "fast"},
	})
	if err != nil {
		t.Fatalf("CreateVolume: %v", err)
	}

	if len(mock.CreateSubvolumeCalls) != 1 {
		t.Fatalf("expected 1 CreateSubvolume call, got %d", len(mock.CreateSubvolumeCalls))
	}
	wantPrefix := filepath.Join(basePath, "volumes") + "/"
	if !strings.HasPrefix(mock.CreateSubvolumeCalls[0], wantPrefix) {
		t.Errorf("subvolume path %q does not start with %q", mock.CreateSubvolumeCalls[0], wantPrefix)
	}

	if len(mock.EnsureQuotaEnabledCalls) != 1 {
		t.Fatalf("expected 1 EnsureQuotaEnabled call, got %d", len(mock.EnsureQuotaEnabledCalls))
	}
	if got := mock.EnsureQuotaEnabledCalls[0]; got != basePath {
		t.Errorf("EnsureQuotaEnabled mountpoint = %q, want %q", got, basePath)
	}
}

func TestCreateVolume_EnsureQuotaCachedPerBasePath(t *testing.T) {
	d, mock, _ := newTestDriverWithMock(t)

	for i, name := range []string{"pvc-a", "pvc-b"} {
		_, err := d.CreateVolume(context.Background(), &csi.CreateVolumeRequest{
			Name:               name,
			CapacityRange:      &csi.CapacityRange{RequiredBytes: 1 << 30},
			VolumeCapabilities: singleNodeWriterCap(),
		})
		if err != nil {
			t.Fatalf("CreateVolume[%d]: %v", i, err)
		}
	}

	if got := len(mock.EnsureQuotaEnabledCalls); got != 1 {
		t.Errorf("EnsureQuotaEnabled called %d times, want 1 (should be cached)", got)
	}
}

// TestCreateVolume_QuotaCacheInvalidatedOnSetPools verifies that the
// quotaEnabled cache is cleared when SetPools is called (C-3 regression test).
func TestCreateVolume_QuotaCacheInvalidatedOnSetPools(t *testing.T) {
	d, mock, store := newTestDriverWithMock(t)

	// Create first volume - this should call EnsureQuotaEnabled
	_, err := d.CreateVolume(context.Background(), &csi.CreateVolumeRequest{
		Name:               "pvc-1",
		CapacityRange:      &csi.CapacityRange{RequiredBytes: 1 << 30},
		VolumeCapabilities: singleNodeWriterCap(),
	})
	if err != nil {
		t.Fatalf("CreateVolume[1]: %v", err)
	}

	if got := len(mock.EnsureQuotaEnabledCalls); got != 1 {
		t.Fatalf("EnsureQuotaEnabled called %d times after first volume, want 1", got)
	}

	// SetPools should invalidate the cache (the call itself clears it, path is unchanged)
	d.SetPools(map[string]string{"default": store.root()})

	// Create second volume - this should call EnsureQuotaEnabled again
	_, err = d.CreateVolume(context.Background(), &csi.CreateVolumeRequest{
		Name:               "pvc-2",
		CapacityRange:      &csi.CapacityRange{RequiredBytes: 1 << 30},
		VolumeCapabilities: singleNodeWriterCap(),
	})
	if err != nil {
		t.Fatalf("CreateVolume[2]: %v", err)
	}

	// Should be called twice now because SetPools invalidated the cache
	if got := len(mock.EnsureQuotaEnabledCalls); got != 2 {
		t.Errorf("EnsureQuotaEnabled called %d times, want 2 (cache should be invalidated by SetPools)", got)
	}
}

func TestResolveBasePath_SinglePoolNoParam(t *testing.T) {
	d := newTestDriver(t)
	poolPath := t.TempDir()
	d.SetPools(map[string]string{"mypool": poolPath})

	got, err := d.resolveBasePath(nil)
	if err != nil {
		t.Fatalf("resolveBasePath: %v", err)
	}
	if got != poolPath {
		t.Errorf("got %q, want %q", got, poolPath)
	}
}

func TestResolveBasePath_MultiplePoolsWithDefault(t *testing.T) {
	d := newTestDriver(t)
	d.SetPools(map[string]string{
		"default": "/mnt/default",
		"fast":    "/mnt/fast",
	})

	got, err := d.resolveBasePath(nil)
	if err != nil {
		t.Fatalf("resolveBasePath: %v", err)
	}
	if got != "/mnt/default" {
		t.Errorf("got %q, want %q", got, "/mnt/default")
	}
}

func TestResolveBasePath_MultiplePoolsNoDefaultFails(t *testing.T) {
	d := newTestDriver(t)
	d.SetPools(map[string]string{
		"fast":    "/mnt/fast",
		"archive": "/mnt/archive",
	})

	_, err := d.resolveBasePath(nil)
	if err == nil {
		t.Fatal("expected error when no pool param and no 'default' pool")
	}
	if code := status.Code(err); code != codes.InvalidArgument {
		t.Errorf("expected InvalidArgument, got %v", code)
	}
}

func TestResolveBasePath_ExplicitPoolParam(t *testing.T) {
	d := newTestDriver(t)
	d.SetPools(map[string]string{
		"default": "/mnt/default",
		"fast":    "/mnt/fast",
	})

	got, err := d.resolveBasePath(map[string]string{"pool": "fast"})
	if err != nil {
		t.Fatalf("resolveBasePath: %v", err)
	}
	if got != "/mnt/fast" {
		t.Errorf("got %q, want %q", got, "/mnt/fast")
	}
}

func TestResolveBasePath_UnknownPoolFails(t *testing.T) {
	d := newTestDriver(t)
	d.SetPools(map[string]string{"default": "/mnt/default"})

	_, err := d.resolveBasePath(map[string]string{"pool": "nonexistent"})
	if err == nil {
		t.Fatal("expected error for unknown pool name")
	}
	if code := status.Code(err); code != codes.InvalidArgument {
		t.Errorf("expected InvalidArgument, got %v", code)
	}
}

func TestResolveBasePath_NoPools(t *testing.T) {
	d := newTestDriver(t)
	d.SetPools(map[string]string{})

	_, err := d.resolveBasePath(nil)
	if err == nil {
		t.Fatal("expected error with no pools configured")
	}
	if code := status.Code(err); code != codes.Internal {
		t.Errorf("expected Internal, got %v", code)
	}
}

func TestDeleteVolume_Exists(t *testing.T) {
	d, mock, store := newTestDriverWithMock(t)
	mock.SubvolumeExistsResult = true

	vol := &state.Volume{
		ID:       "vol-abc",
		Name:     "test-pvc",
		BasePath: store.root(),
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
	if mock.DeleteSubvolumeCalls[0] != vol.Path() {
		t.Errorf("DeleteSubvolume path = %q, want %q", mock.DeleteSubvolumeCalls[0], vol.Path())
	}

	if _, ok := store.GetVolume("vol-abc"); ok {
		t.Error("volume still in state after DeleteVolume")
	}
}

func TestDeleteVolume_SubvolumeGoneButStateRemains(t *testing.T) {
	d, mock, store := newTestDriverWithMock(t)
	mock.SubvolumeExistsResult = false // subvolume already gone

	vol := &state.Volume{
		ID:       "vol-partial",
		Name:     "test-pvc",
		BasePath: store.root(),
	}
	if err := store.SaveVolume(vol); err != nil {
		t.Fatalf("SaveVolume: %v", err)
	}

	_, err := d.DeleteVolume(context.Background(), &csi.DeleteVolumeRequest{VolumeId: "vol-partial"})
	if err != nil {
		t.Fatalf("DeleteVolume with gone subvolume should succeed, got: %v", err)
	}

	if len(mock.DeleteSubvolumeCalls) != 0 {
		t.Errorf("expected no DeleteSubvolume calls when subvolume is already gone, got %d", len(mock.DeleteSubvolumeCalls))
	}

	if _, ok := store.GetVolume("vol-partial"); ok {
		t.Error("volume still in state after DeleteVolume")
	}
}

func TestDeleteVolume_NotFound(t *testing.T) {
	d, mock, _ := newTestDriverWithMock(t)

	_, err := d.DeleteVolume(context.Background(), &csi.DeleteVolumeRequest{VolumeId: "vol-nonexistent"})
	if err != nil {
		t.Fatalf("expected success for missing volume, got: %v", err)
	}

	if len(mock.DeleteSubvolumeCalls) != 0 {
		t.Errorf("expected no DeleteSubvolume calls, got %d", len(mock.DeleteSubvolumeCalls))
	}
}

func TestDeleteVolume_MissingID(t *testing.T) {
	d, _, _ := newTestDriverWithMock(t)

	_, err := d.DeleteVolume(context.Background(), &csi.DeleteVolumeRequest{})
	if code := status.Code(err); code != codes.InvalidArgument {
		t.Errorf("expected InvalidArgument, got %v", code)
	}
}

func TestControllerExpandVolume_Success(t *testing.T) {
	d, mock, store := newTestDriverWithMock(t)

	// First create a volume
	vol := &state.Volume{
		ID:            "vol-expand",
		Name:          "test-pvc",
		CapacityBytes: 1 << 30, // 1 GiB
		BasePath:      store.root(),
	}
	if err := store.SaveVolume(vol); err != nil {
		t.Fatalf("SaveVolume: %v", err)
	}

	// Now expand it to 2 GiB
	resp, err := d.ControllerExpandVolume(context.Background(), &csi.ControllerExpandVolumeRequest{
		VolumeId:      "vol-expand",
		CapacityRange: &csi.CapacityRange{RequiredBytes: 2 << 30},
	})
	if err != nil {
		t.Fatalf("ControllerExpandVolume: %v", err)
	}

	if len(mock.SetQgroupLimitCalls) != 1 {
		t.Fatalf("expected 1 SetQgroupLimit call, got %d", len(mock.SetQgroupLimitCalls))
	}
	if mock.SetQgroupLimitCalls[0].Bytes != 2<<30 {
		t.Errorf("SetQgroupLimit bytes = %d, want %d", mock.SetQgroupLimitCalls[0].Bytes, 2<<30)
	}

	// Verify state was updated
	updated, ok := store.GetVolume("vol-expand")
	if !ok {
		t.Fatal("volume not found in state after expansion")
	}
	if updated.CapacityBytes != 2<<30 {
		t.Errorf("volume capacity = %d, want %d", updated.CapacityBytes, 2<<30)
	}

	if resp.NodeExpansionRequired {
		t.Error("expected NodeExpansionRequired = false, got true")
	}
}

func TestControllerExpandVolume_LimitBytesExceeded(t *testing.T) {
	d, _, store := newTestDriverWithMock(t)

	vol := &state.Volume{
		ID:            "vol-limit",
		Name:          "test-pvc",
		CapacityBytes: 1 << 30, // 1 GiB
		BasePath:      store.root(),
	}
	if err := store.SaveVolume(vol); err != nil {
		t.Fatalf("SaveVolume: %v", err)
	}

	_, err := d.ControllerExpandVolume(context.Background(), &csi.ControllerExpandVolumeRequest{
		VolumeId: "vol-limit",
		CapacityRange: &csi.CapacityRange{
			RequiredBytes: 3 << 30, // 3 GiB required
			LimitBytes:    2 << 30, // but only 2 GiB limit
		},
	})
	if code := status.Code(err); code != codes.InvalidArgument {
		t.Errorf("expected InvalidArgument when required > limit, got %v", code)
	}
}

func TestControllerExpandVolume_VolumeNotFound(t *testing.T) {
	d, _, _ := newTestDriverWithMock(t)

	_, err := d.ControllerExpandVolume(context.Background(), &csi.ControllerExpandVolumeRequest{
		VolumeId:      "vol-nonexistent",
		CapacityRange: &csi.CapacityRange{RequiredBytes: 2 << 30},
	})
	if code := status.Code(err); code != codes.NotFound {
		t.Errorf("expected NotFound, got %v", code)
	}
}

func TestControllerExpandVolume_ShrinkRejected(t *testing.T) {
	d, _, store := newTestDriverWithMock(t)

	// First create a volume with 2 GiB
	vol := &state.Volume{
		ID:            "vol-shrink",
		Name:          "test-pvc",
		CapacityBytes: 2 << 30, // 2 GiB
		BasePath:      store.root(),
	}
	if err := store.SaveVolume(vol); err != nil {
		t.Fatalf("SaveVolume: %v", err)
	}

	// Try to shrink to 1 GiB
	_, err := d.ControllerExpandVolume(context.Background(), &csi.ControllerExpandVolumeRequest{
		VolumeId:      "vol-shrink",
		CapacityRange: &csi.CapacityRange{RequiredBytes: 1 << 30},
	})
	if code := status.Code(err); code != codes.InvalidArgument {
		t.Errorf("expected InvalidArgument for shrink, got %v", code)
	}
}

func TestListVolumes_Empty(t *testing.T) {
	d := newTestDriver(t)

	resp, err := d.ListVolumes(context.Background(), &csi.ListVolumesRequest{})
	if err != nil {
		t.Fatalf("ListVolumes: %v", err)
	}
	if len(resp.Entries) != 0 {
		t.Errorf("expected 0 entries, got %d", len(resp.Entries))
	}
}

func TestListVolumes_ReturnsAll(t *testing.T) {
	d, _, store := newTestDriverWithMock(t)
	vols := []*state.Volume{
		{ID: "vol-1", Name: "pvc-1", CapacityBytes: 1 << 30, BasePath: store.root()},
		{ID: "vol-2", Name: "pvc-2", CapacityBytes: 2 << 30, BasePath: store.root()},
	}
	for _, v := range vols {
		if err := store.SaveVolume(v); err != nil {
			t.Fatalf("SaveVolume: %v", err)
		}
	}

	resp, err := d.ListVolumes(context.Background(), &csi.ListVolumesRequest{})
	if err != nil {
		t.Fatalf("ListVolumes: %v", err)
	}
	if len(resp.Entries) != 2 {
		t.Errorf("expected 2 entries, got %d", len(resp.Entries))
	}
	seen := map[string]bool{}
	for _, e := range resp.Entries {
		seen[e.Volume.VolumeId] = true
	}
	for _, v := range vols {
		if !seen[v.ID] {
			t.Errorf("volume %s missing from ListVolumes response", v.ID)
		}
	}
}

func TestListVolumes_MaxEntries(t *testing.T) {
	d, _, store := newTestDriverWithMock(t)
	for _, v := range []*state.Volume{
		{ID: "vol-a", Name: "pvc-a", BasePath: store.root()},
		{ID: "vol-b", Name: "pvc-b", BasePath: store.root()},
		{ID: "vol-c", Name: "pvc-c", BasePath: store.root()},
	} {
		if err := store.SaveVolume(v); err != nil {
			t.Fatalf("SaveVolume: %v", err)
		}
	}

	resp, err := d.ListVolumes(context.Background(), &csi.ListVolumesRequest{MaxEntries: 2})
	if err != nil {
		t.Fatalf("ListVolumes: %v", err)
	}
	if len(resp.Entries) != 2 {
		t.Errorf("expected 2 entries, got %d", len(resp.Entries))
	}
	if resp.NextToken == "" {
		t.Error("expected non-empty NextToken when more entries remain")
	}
}

func TestListVolumes_PaginationFullWalk(t *testing.T) {
	d, _, store := newTestDriverWithMock(t)
	ids := []string{"vol-a", "vol-b", "vol-c", "vol-d", "vol-e"}
	for _, id := range ids {
		if err := store.SaveVolume(&state.Volume{ID: id, Name: id, BasePath: store.root()}); err != nil {
			t.Fatalf("SaveVolume: %v", err)
		}
	}

	var collected []string
	token := ""
	for {
		resp, err := d.ListVolumes(context.Background(), &csi.ListVolumesRequest{
			MaxEntries:    2,
			StartingToken: token,
		})
		if err != nil {
			t.Fatalf("ListVolumes (token=%q): %v", token, err)
		}
		for _, e := range resp.Entries {
			collected = append(collected, e.Volume.VolumeId)
		}
		if resp.NextToken == "" {
			break
		}
		token = resp.NextToken
	}
	if len(collected) != len(ids) {
		t.Fatalf("collected %d volumes, want %d", len(collected), len(ids))
	}
}

func TestListVolumes_InvalidStartingToken(t *testing.T) {
	d := newTestDriver(t)

	_, err := d.ListVolumes(context.Background(), &csi.ListVolumesRequest{StartingToken: "not-a-number"})
	if code := status.Code(err); code != codes.Aborted {
		t.Errorf("expected Aborted for invalid starting_token, got %v", code)
	}
}

func TestListVolumes_StartingTokenBeyondEnd(t *testing.T) {
	d, _, store := newTestDriverWithMock(t)
	if err := store.SaveVolume(&state.Volume{ID: "vol-1", Name: "pvc-1", BasePath: store.root()}); err != nil {
		t.Fatalf("SaveVolume: %v", err)
	}

	resp, err := d.ListVolumes(context.Background(), &csi.ListVolumesRequest{StartingToken: "100"})
	if err != nil {
		t.Fatalf("ListVolumes: %v", err)
	}
	if len(resp.Entries) != 0 {
		t.Errorf("expected 0 entries for token past end, got %d", len(resp.Entries))
	}
}

func TestListVolumes_NoMaxEntriesNoToken(t *testing.T) {
	d, _, store := newTestDriverWithMock(t)
	for _, v := range []*state.Volume{
		{ID: "vol-a", Name: "pvc-a", BasePath: store.root()},
		{ID: "vol-b", Name: "pvc-b", BasePath: store.root()},
	} {
		if err := store.SaveVolume(v); err != nil {
			t.Fatalf("SaveVolume: %v", err)
		}
	}

	resp, err := d.ListVolumes(context.Background(), &csi.ListVolumesRequest{})
	if err != nil {
		t.Fatalf("ListVolumes: %v", err)
	}
	if len(resp.Entries) != 2 {
		t.Errorf("expected 2 entries, got %d", len(resp.Entries))
	}
	if resp.NextToken != "" {
		t.Errorf("expected empty NextToken when no max_entries, got %q", resp.NextToken)
	}
}

func TestControllerGetVolume_Success(t *testing.T) {
	d, _, store := newTestDriverWithMock(t)
	vol := &state.Volume{
		ID:            "vol-abc",
		Name:          "test-pvc",
		CapacityBytes: 1 << 30,
		BasePath:      store.root(),
	}
	if err := store.SaveVolume(vol); err != nil {
		t.Fatalf("SaveVolume: %v", err)
	}

	resp, err := d.ControllerGetVolume(context.Background(), &csi.ControllerGetVolumeRequest{VolumeId: "vol-abc"})
	if err != nil {
		t.Fatalf("ControllerGetVolume: %v", err)
	}
	if resp.Volume.VolumeId != "vol-abc" {
		t.Errorf("VolumeId = %q, want vol-abc", resp.Volume.VolumeId)
	}
	if resp.Status == nil {
		t.Fatal("expected Status to be set")
	}
}

func TestControllerGetVolume_NotFound(t *testing.T) {
	d := newTestDriver(t)

	_, err := d.ControllerGetVolume(context.Background(), &csi.ControllerGetVolumeRequest{VolumeId: "vol-nonexistent"})
	if code := status.Code(err); code != codes.NotFound {
		t.Errorf("expected NotFound, got %v", code)
	}
}

func TestControllerGetVolume_MissingID(t *testing.T) {
	d := newTestDriver(t)

	_, err := d.ControllerGetVolume(context.Background(), &csi.ControllerGetVolumeRequest{})
	if code := status.Code(err); code != codes.InvalidArgument {
		t.Errorf("expected InvalidArgument, got %v", code)
	}
}

func TestControllerGetVolume_AbnormalWhenPathMissing(t *testing.T) {
	d, _, store := newTestDriverWithMock(t)
	vol := &state.Volume{
		ID:       "vol-missing",
		Name:     "test-pvc",
		BasePath: "/nonexistent/base",
	}
	if err := store.SaveVolume(vol); err != nil {
		t.Fatalf("SaveVolume: %v", err)
	}

	resp, err := d.ControllerGetVolume(context.Background(), &csi.ControllerGetVolumeRequest{VolumeId: "vol-missing"})
	if err != nil {
		t.Fatalf("ControllerGetVolume: %v", err)
	}
	if !resp.Status.VolumeCondition.Abnormal {
		t.Error("expected Abnormal=true when subvolume path does not exist")
	}
}

func TestControllerGetVolume_NormalWhenPathExists(t *testing.T) {
	d, _, store := newTestDriverWithMock(t)
	vol := &state.Volume{
		ID:       "vol-exists",
		Name:     "test-pvc",
		BasePath: store.root(),
	}
	// Create the subvolume directory so os.Stat succeeds.
	if err := os.MkdirAll(vol.Path(), 0o755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(vol.Path()) })
	if err := store.SaveVolume(vol); err != nil {
		t.Fatalf("SaveVolume: %v", err)
	}

	resp, err := d.ControllerGetVolume(context.Background(), &csi.ControllerGetVolumeRequest{VolumeId: "vol-exists"})
	if err != nil {
		t.Fatalf("ControllerGetVolume: %v", err)
	}
	if resp.Status.VolumeCondition.Abnormal {
		t.Errorf("expected Abnormal=false when subvolume path exists, got message: %s", resp.Status.VolumeCondition.Message)
	}
}

func TestCreateVolume_ConcurrentSameNameIdempotent(t *testing.T) {
	d, mock, _ := newTestDriverWithMock(t)

	req := &csi.CreateVolumeRequest{
		Name:               "test-pvc-concurrent",
		VolumeCapabilities: singleNodeWriterCap(),
	}

	const n = 20
	volIDs := make([]string, n)
	errs := make([]error, n)

	start := make(chan struct{})
	var wg sync.WaitGroup
	for i := range n {
		idx := i // capture loop variable
		wg.Go(func() {
			<-start
			resp, err := d.CreateVolume(context.Background(), req)
			errs[idx] = err
			if err == nil {
				volIDs[idx] = resp.Volume.VolumeId
			}
		})
	}
	close(start)
	wg.Wait()

	for i, err := range errs {
		if err != nil {
			t.Errorf("goroutine %d: unexpected error: %v", i, err)
		}
	}
	first := volIDs[0]
	for i, id := range volIDs {
		if id != first {
			t.Errorf("goroutine %d returned volume ID %q, want %q", i, id, first)
		}
	}
	if got := len(mock.CreateSubvolumeCalls); got != 1 {
		t.Errorf("CreateSubvolume called %d times under concurrent requests, want exactly 1", got)
	}
}

func TestCreateSnapshot_ConcurrentSameNameIdempotent(t *testing.T) {
	d, mock, store := newTestDriverWithMock(t)

	vol := &state.Volume{
		ID:       "vol-src",
		Name:     "source-pvc",
		BasePath: store.root(),
	}
	if err := store.SaveVolume(vol); err != nil {
		t.Fatalf("SaveVolume: %v", err)
	}

	req := &csi.CreateSnapshotRequest{
		SourceVolumeId: "vol-src",
		Name:           "snap-concurrent",
	}

	const n = 20
	snapIDs := make([]string, n)
	errs := make([]error, n)

	start := make(chan struct{})
	var wg sync.WaitGroup
	for i := range n {
		idx := i // capture loop variable
		wg.Go(func() {
			<-start
			resp, err := d.CreateSnapshot(context.Background(), req)
			errs[idx] = err
			if err == nil {
				snapIDs[idx] = resp.Snapshot.SnapshotId
			}
		})
	}
	close(start)
	wg.Wait()

	for i, err := range errs {
		if err != nil {
			t.Errorf("goroutine %d: unexpected error: %v", i, err)
		}
	}
	first := snapIDs[0]
	for i, id := range snapIDs {
		if id != first {
			t.Errorf("goroutine %d returned snapshot ID %q, want %q", i, id, first)
		}
	}
	if got := len(mock.CreateSnapshotCalls); got != 1 {
		t.Errorf("CreateSnapshot called %d times under concurrent requests, want exactly 1", got)
	}
}

func TestCreateVolume_CleansUpSubvolumeOnSetQgroupLimitFailure(t *testing.T) {
	d, mock, _ := newTestDriverWithMock(t)

	mock.SetQgroupLimitErr = fmt.Errorf("quota not enabled")

	_, err := d.CreateVolume(context.Background(), &csi.CreateVolumeRequest{
		Name:               "pvc-leak",
		CapacityRange:      &csi.CapacityRange{RequiredBytes: 1 << 30},
		VolumeCapabilities: singleNodeWriterCap(),
	})
	if err == nil {
		t.Fatal("expected error when SetQgroupLimit fails")
	}

	// The subvolume must have been created, then cleaned up.
	if len(mock.CreateSubvolumeCalls) != 1 {
		t.Fatalf("expected 1 CreateSubvolume call, got %d", len(mock.CreateSubvolumeCalls))
	}
	if len(mock.DeleteSubvolumeCalls) != 1 {
		t.Fatalf("expected 1 DeleteSubvolume call to clean up orphan, got %d", len(mock.DeleteSubvolumeCalls))
	}
	if mock.DeleteSubvolumeCalls[0] != mock.CreateSubvolumeCalls[0] {
		t.Errorf("cleanup path %q != create path %q", mock.DeleteSubvolumeCalls[0], mock.CreateSubvolumeCalls[0])
	}
}

func TestControllerExpandVolume_CallsEnsureQuotaEnabled(t *testing.T) {
	d, mock, store := newTestDriverWithMock(t)

	vol := &state.Volume{
		ID:            "vol-expand-quota",
		Name:          "test-pvc",
		CapacityBytes: 0, // created without a quota limit
		BasePath:      store.root(),
	}
	if err := store.SaveVolume(vol); err != nil {
		t.Fatalf("SaveVolume: %v", err)
	}

	_, err := d.ControllerExpandVolume(context.Background(), &csi.ControllerExpandVolumeRequest{
		VolumeId:      "vol-expand-quota",
		CapacityRange: &csi.CapacityRange{RequiredBytes: 2 << 30},
	})
	if err != nil {
		t.Fatalf("ControllerExpandVolume: %v", err)
	}

	if len(mock.EnsureQuotaEnabledCalls) != 1 {
		t.Fatalf("expected 1 EnsureQuotaEnabled call, got %d", len(mock.EnsureQuotaEnabledCalls))
	}
	if got := mock.EnsureQuotaEnabledCalls[0]; got != store.root() {
		t.Errorf("EnsureQuotaEnabled called with %q, want %q", got, store.root())
	}
}

func TestDeleteVolumeAndCreateFromSnapshot_ConcurrentSafe(t *testing.T) {
	d, _, store := newTestDriverWithMock(t)

	srcVol := &state.Volume{ID: "vol-src", Name: "src-pvc", BasePath: store.root()}
	if err := store.SaveVolume(srcVol); err != nil {
		t.Fatalf("SaveVolume: %v", err)
	}
	snap := &state.Snapshot{ID: "snap-1", Name: "snap-1", SourceVolID: "vol-src", BasePath: store.root(), ReadyToUse: true}
	if err := store.SaveSnapshot(snap); err != nil {
		t.Fatalf("SaveSnapshot: %v", err)
	}
	delVol := &state.Volume{ID: "vol-del", Name: "del-pvc", BasePath: store.root()}
	if err := store.SaveVolume(delVol); err != nil {
		t.Fatalf("SaveVolume: %v", err)
	}

	start := make(chan struct{})
	var wg sync.WaitGroup

	wg.Go(func() {
		<-start
		_, _ = d.DeleteVolume(context.Background(), &csi.DeleteVolumeRequest{VolumeId: "vol-del"})
	})

	wg.Go(func() {
		<-start
		_, _ = d.CreateVolume(context.Background(), &csi.CreateVolumeRequest{
			Name:               "clone-pvc",
			VolumeCapabilities: singleNodeWriterCap(),
			VolumeContentSource: &csi.VolumeContentSource{
				Type: &csi.VolumeContentSource_Snapshot{
					Snapshot: &csi.VolumeContentSource_SnapshotSource{SnapshotId: "snap-1"},
				},
			},
		})
	})

	close(start)
	wg.Wait()

	if _, ok := store.GetVolume("vol-del"); ok {
		t.Error("vol-del should have been deleted")
	}
}

func TestDeleteSnapshotAndCreateFromSnapshot_ConcurrentSafe(t *testing.T) {
	d, _, store := newTestDriverWithMock(t)

	srcVol := &state.Volume{ID: "vol-src", Name: "src-pvc", BasePath: store.root()}
	if err := store.SaveVolume(srcVol); err != nil {
		t.Fatalf("SaveVolume: %v", err)
	}
	delSnap := &state.Snapshot{ID: "snap-del", Name: "snap-del", SourceVolID: "vol-src", BasePath: store.root(), ReadyToUse: true}
	if err := store.SaveSnapshot(delSnap); err != nil {
		t.Fatalf("SaveSnapshot: %v", err)
	}
	cloneSnap := &state.Snapshot{ID: "snap-clone", Name: "snap-clone", SourceVolID: "vol-src", BasePath: store.root(), ReadyToUse: true}
	if err := store.SaveSnapshot(cloneSnap); err != nil {
		t.Fatalf("SaveSnapshot: %v", err)
	}

	start := make(chan struct{})
	var wg sync.WaitGroup

	wg.Go(func() {
		<-start
		_, _ = d.DeleteSnapshot(context.Background(), &csi.DeleteSnapshotRequest{SnapshotId: "snap-del"})
	})

	wg.Go(func() {
		<-start
		_, _ = d.CreateVolume(context.Background(), &csi.CreateVolumeRequest{
			Name:               "clone-pvc",
			VolumeCapabilities: singleNodeWriterCap(),
			VolumeContentSource: &csi.VolumeContentSource{
				Type: &csi.VolumeContentSource_Snapshot{
					Snapshot: &csi.VolumeContentSource_SnapshotSource{SnapshotId: "snap-clone"},
				},
			},
		})
	})

	close(start)
	wg.Wait()

	if _, ok := store.GetSnapshot("snap-del"); ok {
		t.Error("snap-del should have been deleted")
	}
}
