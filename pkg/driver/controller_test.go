package driver

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"sync"
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
	basePath := t.TempDir()
	d, mock, _ := newTestDriverWithMock()
	// Register the extra basePath so the MultiStore can route saves there.
	d.Store.(*state.MultiStore).AddStoreForTest(basePath, newMemStore(basePath))

	_, err := d.CreateVolume(context.Background(), &csi.CreateVolumeRequest{
		Name:               "test-pvc",
		CapacityRange:      &csi.CapacityRange{RequiredBytes: 1 << 30},
		VolumeCapabilities: singleNodeWriterCap(),
		Parameters:         map[string]string{"basePath": basePath},
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

func TestDeleteVolume_Exists(t *testing.T) {
	d, mock, store := newTestDriverWithMock()

	vol := &state.Volume{
		ID:       "vol-abc",
		Name:     "test-pvc",
		BasePath: "/tmp/btrfs-csi-test",
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

func TestControllerExpandVolume_Success(t *testing.T) {
	d, mock, store := newTestDriverWithMock()

	// First create a volume
	vol := &state.Volume{
		ID:            "vol-expand",
		Name:          "test-pvc",
		CapacityBytes: 1 << 30, // 1 GiB
		BasePath:      "/tmp/btrfs-csi-test",
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

func TestControllerExpandVolume_VolumeNotFound(t *testing.T) {
	d, _, _ := newTestDriverWithMock()

	_, err := d.ControllerExpandVolume(context.Background(), &csi.ControllerExpandVolumeRequest{
		VolumeId:      "vol-nonexistent",
		CapacityRange: &csi.CapacityRange{RequiredBytes: 2 << 30},
	})
	if code := status.Code(err); code != codes.NotFound {
		t.Errorf("expected NotFound, got %v", code)
	}
}

func TestControllerExpandVolume_ShrinkRejected(t *testing.T) {
	d, _, store := newTestDriverWithMock()

	// First create a volume with 2 GiB
	vol := &state.Volume{
		ID:            "vol-shrink",
		Name:          "test-pvc",
		CapacityBytes: 2 << 30, // 2 GiB
		BasePath:      "/tmp/btrfs-csi-test",
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
	d := newTestDriver()

	resp, err := d.ListVolumes(context.Background(), &csi.ListVolumesRequest{})
	if err != nil {
		t.Fatalf("ListVolumes: %v", err)
	}
	if len(resp.Entries) != 0 {
		t.Errorf("expected 0 entries, got %d", len(resp.Entries))
	}
}

func TestListVolumes_ReturnsAll(t *testing.T) {
	d, _, store := newTestDriverWithMock()
	vols := []*state.Volume{
		{ID: "vol-1", Name: "pvc-1", CapacityBytes: 1 << 30, BasePath: "/tmp/btrfs-csi-test", NodeID: "test-node"},
		{ID: "vol-2", Name: "pvc-2", CapacityBytes: 2 << 30, BasePath: "/tmp/btrfs-csi-test", NodeID: "test-node"},
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

func TestListVolumes_PaginationTokenRejected(t *testing.T) {
	d := newTestDriver()

	_, err := d.ListVolumes(context.Background(), &csi.ListVolumesRequest{StartingToken: "some-token"})
	if code := status.Code(err); code != codes.Aborted {
		t.Errorf("expected Aborted for pagination token, got %v", code)
	}
}

func TestControllerGetVolume_Success(t *testing.T) {
	d, _, store := newTestDriverWithMock()
	vol := &state.Volume{
		ID:            "vol-abc",
		Name:          "test-pvc",
		CapacityBytes: 1 << 30,
		BasePath:      "/tmp/btrfs-csi-test",
		NodeID:        "test-node",
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
	d := newTestDriver()

	_, err := d.ControllerGetVolume(context.Background(), &csi.ControllerGetVolumeRequest{VolumeId: "vol-nonexistent"})
	if code := status.Code(err); code != codes.NotFound {
		t.Errorf("expected NotFound, got %v", code)
	}
}

func TestControllerGetVolume_MissingID(t *testing.T) {
	d := newTestDriver()

	_, err := d.ControllerGetVolume(context.Background(), &csi.ControllerGetVolumeRequest{})
	if code := status.Code(err); code != codes.InvalidArgument {
		t.Errorf("expected InvalidArgument, got %v", code)
	}
}

func TestControllerGetVolume_AbnormalWhenPathMissing(t *testing.T) {
	d, _, store := newTestDriverWithMock()
	vol := &state.Volume{
		ID:       "vol-missing",
		Name:     "test-pvc",
		BasePath: "/nonexistent/base",
		NodeID:   "test-node",
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
	d, _, store := newTestDriverWithMock()
	// Use testRootPath so the memStore hydrates BasePath correctly on GetVolume.
	vol := &state.Volume{
		ID:       "vol-exists",
		Name:     "test-pvc",
		BasePath: testRootPath,
		NodeID:   "test-node",
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
	d, mock, _ := newTestDriverWithMock()

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
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			resp, err := d.CreateVolume(context.Background(), req)
			errs[i] = err
			if err == nil {
				volIDs[i] = resp.Volume.VolumeId
			}
		}()
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
	d, mock, store := newTestDriverWithMock()

	vol := &state.Volume{
		ID:       "vol-src",
		Name:     "source-pvc",
		BasePath: "/tmp/btrfs-csi-test",
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
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			resp, err := d.CreateSnapshot(context.Background(), req)
			errs[i] = err
			if err == nil {
				snapIDs[i] = resp.Snapshot.SnapshotId
			}
		}()
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
