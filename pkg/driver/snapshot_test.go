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

// --- CreateSnapshot tests ---

func TestCreateSnapshot_Success(t *testing.T) {
	d, mock, store := newTestDriverWithMock()

	vol := &state.Volume{
		ID:            "vol-src",
		Name:          "source-pvc",
		SubvolumePath: "/tmp/btrfs-csi-test/volumes/vol-src",
	}
	if err := store.SaveVolume(vol); err != nil {
		t.Fatalf("SaveVolume: %v", err)
	}

	resp, err := d.CreateSnapshot(context.Background(), &csi.CreateSnapshotRequest{
		SourceVolumeId: "vol-src",
		Name:           "snap-1",
	})
	if err != nil {
		t.Fatalf("CreateSnapshot: %v", err)
	}

	if len(mock.CreateSnapshotCalls) != 1 {
		t.Fatalf("expected 1 CreateSnapshot call, got %d", len(mock.CreateSnapshotCalls))
	}
	call := mock.CreateSnapshotCalls[0]
	if call.Src != vol.SubvolumePath {
		t.Errorf("CreateSnapshot src = %q, want %q", call.Src, vol.SubvolumePath)
	}
	if !call.Readonly {
		t.Error("expected readonly=true for snapshot creation")
	}
	wantDstPrefix := filepath.Join("/tmp/btrfs-csi-test", "snapshots") + string(filepath.Separator)
	if !strings.HasPrefix(call.Dst, wantDstPrefix) {
		t.Errorf("snapshot dst %q should be under %q", call.Dst, wantDstPrefix)
	}

	if resp.Snapshot.SnapshotId == "" {
		t.Fatal("expected non-empty snapshot ID")
	}
	if resp.Snapshot.SourceVolumeId != "vol-src" {
		t.Errorf("SourceVolumeId = %q, want %q", resp.Snapshot.SourceVolumeId, "vol-src")
	}
	if !resp.Snapshot.ReadyToUse {
		t.Error("expected ReadyToUse=true")
	}
	if resp.Snapshot.CreationTime == nil {
		t.Error("expected non-nil CreationTime")
	}

	snap, ok := store.GetSnapshot(resp.Snapshot.SnapshotId)
	if !ok {
		t.Fatal("snapshot not found in state after CreateSnapshot")
	}
	if !snap.ReadyToUse {
		t.Error("snapshot ReadyToUse should be true in state")
	}
	if snap.CreatedAt.IsZero() {
		t.Error("snapshot CreatedAt should be set in state")
	}
}

func TestCreateSnapshot_Idempotent(t *testing.T) {
	d, mock, store := newTestDriverWithMock()

	vol := &state.Volume{
		ID:            "vol-src",
		Name:          "source-pvc",
		SubvolumePath: "/tmp/btrfs-csi-test/volumes/vol-src",
	}
	if err := store.SaveVolume(vol); err != nil {
		t.Fatalf("SaveVolume: %v", err)
	}

	req := &csi.CreateSnapshotRequest{
		SourceVolumeId: "vol-src",
		Name:           "snap-1",
	}

	resp1, err := d.CreateSnapshot(context.Background(), req)
	if err != nil {
		t.Fatalf("first CreateSnapshot: %v", err)
	}
	resp2, err := d.CreateSnapshot(context.Background(), req)
	if err != nil {
		t.Fatalf("second CreateSnapshot: %v", err)
	}

	if resp1.Snapshot.SnapshotId != resp2.Snapshot.SnapshotId {
		t.Errorf("snapshot ID changed on idempotent call: %q → %q", resp1.Snapshot.SnapshotId, resp2.Snapshot.SnapshotId)
	}
	if len(mock.CreateSnapshotCalls) != 1 {
		t.Errorf("CreateSnapshot called %d times, want 1", len(mock.CreateSnapshotCalls))
	}
}

func TestCreateSnapshot_Idempotent_MismatchedSource(t *testing.T) {
	d, _, store := newTestDriverWithMock()

	vol := &state.Volume{
		ID:            "vol-src",
		Name:          "source-pvc",
		SubvolumePath: "/tmp/btrfs-csi-test/volumes/vol-src",
	}
	if err := store.SaveVolume(vol); err != nil {
		t.Fatalf("SaveVolume: %v", err)
	}

	// Create the first snapshot.
	_, err := d.CreateSnapshot(context.Background(), &csi.CreateSnapshotRequest{
		SourceVolumeId: "vol-src",
		Name:           "snap-1",
	})
	if err != nil {
		t.Fatalf("first CreateSnapshot: %v", err)
	}

	// Second call with same name but different source volume should fail.
	_, err = d.CreateSnapshot(context.Background(), &csi.CreateSnapshotRequest{
		SourceVolumeId: "vol-different",
		Name:           "snap-1",
	})
	if code := status.Code(err); code != codes.AlreadyExists {
		t.Errorf("expected AlreadyExists for mismatched source, got %v", code)
	}
}

func TestCreateSnapshot_SourceNotFound(t *testing.T) {
	d, _, _ := newTestDriverWithMock()

	_, err := d.CreateSnapshot(context.Background(), &csi.CreateSnapshotRequest{
		SourceVolumeId: "vol-nonexistent",
		Name:           "snap-1",
	})
	if code := status.Code(err); code != codes.NotFound {
		t.Errorf("expected NotFound, got %v", code)
	}
}

func TestCreateSnapshot_MissingName(t *testing.T) {
	d, _, _ := newTestDriverWithMock()

	_, err := d.CreateSnapshot(context.Background(), &csi.CreateSnapshotRequest{
		SourceVolumeId: "vol-src",
	})
	if code := status.Code(err); code != codes.InvalidArgument {
		t.Errorf("expected InvalidArgument, got %v", code)
	}
}

// --- DeleteSnapshot tests ---

func TestDeleteSnapshot_Success(t *testing.T) {
	d, mock, store := newTestDriverWithMock()

	snap := &state.Snapshot{
		ID:           "snap-abc",
		Name:         "snap-1",
		SourceVolID:  "vol-src",
		SnapshotPath: "/tmp/btrfs-csi-test/snapshots/snap-abc",
		ReadyToUse:   true,
	}
	if err := store.SaveSnapshot(snap); err != nil {
		t.Fatalf("SaveSnapshot: %v", err)
	}

	_, err := d.DeleteSnapshot(context.Background(), &csi.DeleteSnapshotRequest{SnapshotId: "snap-abc"})
	if err != nil {
		t.Fatalf("DeleteSnapshot: %v", err)
	}

	if len(mock.DeleteSubvolumeCalls) != 1 {
		t.Fatalf("expected 1 DeleteSubvolume call, got %d", len(mock.DeleteSubvolumeCalls))
	}
	if mock.DeleteSubvolumeCalls[0] != snap.SnapshotPath {
		t.Errorf("DeleteSubvolume path = %q, want %q", mock.DeleteSubvolumeCalls[0], snap.SnapshotPath)
	}

	if _, ok := store.GetSnapshot("snap-abc"); ok {
		t.Error("snapshot still in state after DeleteSnapshot")
	}
}

func TestDeleteSnapshot_NotFound(t *testing.T) {
	d, mock, _ := newTestDriverWithMock()

	_, err := d.DeleteSnapshot(context.Background(), &csi.DeleteSnapshotRequest{SnapshotId: "snap-nonexistent"})
	if err != nil {
		t.Fatalf("expected success for missing snapshot, got: %v", err)
	}

	if len(mock.DeleteSubvolumeCalls) != 0 {
		t.Errorf("expected no DeleteSubvolume calls, got %d", len(mock.DeleteSubvolumeCalls))
	}
}

// --- CreateVolume content source tests ---

func TestCreateVolume_FromSnapshot(t *testing.T) {
	d, mock, store := newTestDriverWithMock()

	snap := &state.Snapshot{
		ID:           "snap-src",
		Name:         "snap-1",
		SourceVolID:  "vol-orig",
		SnapshotPath: "/tmp/btrfs-csi-test/snapshots/snap-src",
		ReadyToUse:   true,
	}
	if err := store.SaveSnapshot(snap); err != nil {
		t.Fatalf("SaveSnapshot: %v", err)
	}

	resp, err := d.CreateVolume(context.Background(), &csi.CreateVolumeRequest{
		Name:               "clone-from-snap",
		VolumeCapabilities: singleNodeWriterCap(),
		VolumeContentSource: &csi.VolumeContentSource{
			Type: &csi.VolumeContentSource_Snapshot{
				Snapshot: &csi.VolumeContentSource_SnapshotSource{
					SnapshotId: "snap-src",
				},
			},
		},
	})
	if err != nil {
		t.Fatalf("CreateVolume from snapshot: %v", err)
	}

	// Should use CreateSnapshot (writable clone), not CreateSubvolume
	if len(mock.CreateSubvolumeCalls) != 0 {
		t.Errorf("expected no CreateSubvolume calls, got %d", len(mock.CreateSubvolumeCalls))
	}
	if len(mock.CreateSnapshotCalls) != 1 {
		t.Fatalf("expected 1 CreateSnapshot call, got %d", len(mock.CreateSnapshotCalls))
	}
	call := mock.CreateSnapshotCalls[0]
	if call.Src != snap.SnapshotPath {
		t.Errorf("CreateSnapshot src = %q, want %q", call.Src, snap.SnapshotPath)
	}
	if call.Readonly {
		t.Error("expected readonly=false for volume from snapshot")
	}

	// State should record source snapshot ID
	vol, ok := store.GetVolume(resp.Volume.VolumeId)
	if !ok {
		t.Fatal("volume not found in state after CreateVolume from snapshot")
	}
	if vol.SourceSnapID != "snap-src" {
		t.Errorf("SourceSnapID = %q, want %q", vol.SourceSnapID, "snap-src")
	}
}

func TestCreateVolume_FromSnapshot_Idempotent_MismatchedSource(t *testing.T) {
	d, _, store := newTestDriverWithMock()

	snap := &state.Snapshot{
		ID:           "snap-src",
		Name:         "snap-1",
		SourceVolID:  "vol-orig",
		SnapshotPath: "/tmp/btrfs-csi-test/snapshots/snap-src",
		ReadyToUse:   true,
	}
	if err := store.SaveSnapshot(snap); err != nil {
		t.Fatalf("SaveSnapshot: %v", err)
	}

	// Create a volume from snap-src.
	_, err := d.CreateVolume(context.Background(), &csi.CreateVolumeRequest{
		Name:               "vol-from-snap",
		VolumeCapabilities: singleNodeWriterCap(),
		VolumeContentSource: &csi.VolumeContentSource{
			Type: &csi.VolumeContentSource_Snapshot{
				Snapshot: &csi.VolumeContentSource_SnapshotSource{
					SnapshotId: "snap-src",
				},
			},
		},
	})
	if err != nil {
		t.Fatalf("first CreateVolume: %v", err)
	}

	// Same name but different snapshot source should fail.
	_, err = d.CreateVolume(context.Background(), &csi.CreateVolumeRequest{
		Name:               "vol-from-snap",
		VolumeCapabilities: singleNodeWriterCap(),
		VolumeContentSource: &csi.VolumeContentSource{
			Type: &csi.VolumeContentSource_Snapshot{
				Snapshot: &csi.VolumeContentSource_SnapshotSource{
					SnapshotId: "snap-different",
				},
			},
		},
	})
	if code := status.Code(err); code != codes.AlreadyExists {
		t.Errorf("expected AlreadyExists for mismatched snapshot source, got %v", code)
	}

	// Same name but with volume source (different type) should also fail.
	_, err = d.CreateVolume(context.Background(), &csi.CreateVolumeRequest{
		Name:               "vol-from-snap",
		VolumeCapabilities: singleNodeWriterCap(),
		VolumeContentSource: &csi.VolumeContentSource{
			Type: &csi.VolumeContentSource_Volume{
				Volume: &csi.VolumeContentSource_VolumeSource{
					VolumeId: "vol-orig",
				},
			},
		},
	})
	if code := status.Code(err); code != codes.AlreadyExists {
		t.Errorf("expected AlreadyExists for mismatched content source type, got %v", code)
	}

	// Same name with no content source (fresh) when original had a source should also fail.
	_, err = d.CreateVolume(context.Background(), &csi.CreateVolumeRequest{
		Name:               "vol-from-snap",
		VolumeCapabilities: singleNodeWriterCap(),
	})
	if code := status.Code(err); code != codes.AlreadyExists {
		t.Errorf("expected AlreadyExists for missing content source, got %v", code)
	}
}

func TestCreateVolume_FromSnapshot_SourceNotFound(t *testing.T) {
	d, _, _ := newTestDriverWithMock()

	_, err := d.CreateVolume(context.Background(), &csi.CreateVolumeRequest{
		Name:               "clone-from-snap",
		VolumeCapabilities: singleNodeWriterCap(),
		VolumeContentSource: &csi.VolumeContentSource{
			Type: &csi.VolumeContentSource_Snapshot{
				Snapshot: &csi.VolumeContentSource_SnapshotSource{
					SnapshotId: "snap-nonexistent",
				},
			},
		},
	})
	if code := status.Code(err); code != codes.NotFound {
		t.Errorf("expected NotFound, got %v", code)
	}
}

func TestCreateVolume_Clone(t *testing.T) {
	d, mock, store := newTestDriverWithMock()

	srcVol := &state.Volume{
		ID:            "vol-src",
		Name:          "source-pvc",
		SubvolumePath: "/tmp/btrfs-csi-test/volumes/vol-src",
	}
	if err := store.SaveVolume(srcVol); err != nil {
		t.Fatalf("SaveVolume: %v", err)
	}

	resp, err := d.CreateVolume(context.Background(), &csi.CreateVolumeRequest{
		Name:               "clone-from-vol",
		VolumeCapabilities: singleNodeWriterCap(),
		VolumeContentSource: &csi.VolumeContentSource{
			Type: &csi.VolumeContentSource_Volume{
				Volume: &csi.VolumeContentSource_VolumeSource{
					VolumeId: "vol-src",
				},
			},
		},
	})
	if err != nil {
		t.Fatalf("CreateVolume clone: %v", err)
	}

	// Should use CreateSnapshot (writable clone), not CreateSubvolume
	if len(mock.CreateSubvolumeCalls) != 0 {
		t.Errorf("expected no CreateSubvolume calls, got %d", len(mock.CreateSubvolumeCalls))
	}
	if len(mock.CreateSnapshotCalls) != 1 {
		t.Fatalf("expected 1 CreateSnapshot call, got %d", len(mock.CreateSnapshotCalls))
	}
	call := mock.CreateSnapshotCalls[0]
	if call.Src != srcVol.SubvolumePath {
		t.Errorf("CreateSnapshot src = %q, want %q", call.Src, srcVol.SubvolumePath)
	}
	if call.Readonly {
		t.Error("expected readonly=false for volume clone")
	}

	// State should record source volume ID
	vol, ok := store.GetVolume(resp.Volume.VolumeId)
	if !ok {
		t.Fatal("volume not found in state after clone")
	}
	if vol.SourceVolID != "vol-src" {
		t.Errorf("SourceVolID = %q, want %q", vol.SourceVolID, "vol-src")
	}
}

func TestCreateVolume_UnknownContentSource(t *testing.T) {
	d, _, _ := newTestDriverWithMock()

	_, err := d.CreateVolume(context.Background(), &csi.CreateVolumeRequest{
		Name:                "vol-bad-source",
		VolumeCapabilities:  singleNodeWriterCap(),
		VolumeContentSource: &csi.VolumeContentSource{
			// Type is nil — an unrecognized/empty content source.
		},
	})
	if code := status.Code(err); code != codes.InvalidArgument {
		t.Errorf("expected InvalidArgument for unknown content source, got %v", code)
	}
}

func TestCreateVolume_Clone_SourceNotFound(t *testing.T) {
	d, _, _ := newTestDriverWithMock()

	_, err := d.CreateVolume(context.Background(), &csi.CreateVolumeRequest{
		Name:               "clone-from-vol",
		VolumeCapabilities: singleNodeWriterCap(),
		VolumeContentSource: &csi.VolumeContentSource{
			Type: &csi.VolumeContentSource_Volume{
				Volume: &csi.VolumeContentSource_VolumeSource{
					VolumeId: "vol-nonexistent",
				},
			},
		},
	})
	if code := status.Code(err); code != codes.NotFound {
		t.Errorf("expected NotFound, got %v", code)
	}
}
