package driver

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/container-storage-interface/spec/lib/go/csi"
	"github.com/google/uuid"
	"github.com/guru/btrfs-csi/pkg/state"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/timestamppb"
	"k8s.io/klog/v2"
)

const topologyKey = "topology.btrfs.csi.local/node"

// supportedAccessModes is the set of access modes this single-node driver supports.
var supportedAccessModes = map[csi.VolumeCapability_AccessMode_Mode]struct{}{
	csi.VolumeCapability_AccessMode_SINGLE_NODE_WRITER:      {},
	csi.VolumeCapability_AccessMode_SINGLE_NODE_READER_ONLY: {},
}

func (d *Driver) ControllerGetCapabilities(_ context.Context, _ *csi.ControllerGetCapabilitiesRequest) (*csi.ControllerGetCapabilitiesResponse, error) {
	caps := []csi.ControllerServiceCapability_RPC_Type{
		csi.ControllerServiceCapability_RPC_CREATE_DELETE_VOLUME,
		csi.ControllerServiceCapability_RPC_CREATE_DELETE_SNAPSHOT,
		csi.ControllerServiceCapability_RPC_CLONE_VOLUME,
		csi.ControllerServiceCapability_RPC_EXPAND_VOLUME,
	}
	var out []*csi.ControllerServiceCapability
	for _, c := range caps {
		out = append(out, &csi.ControllerServiceCapability{
			Type: &csi.ControllerServiceCapability_Rpc{
				Rpc: &csi.ControllerServiceCapability_RPC{Type: c},
			},
		})
	}
	return &csi.ControllerGetCapabilitiesResponse{Capabilities: out}, nil
}

func (d *Driver) ValidateVolumeCapabilities(_ context.Context, req *csi.ValidateVolumeCapabilitiesRequest) (*csi.ValidateVolumeCapabilitiesResponse, error) {
	if req.VolumeId == "" {
		return nil, status.Error(codes.InvalidArgument, "volume ID is required")
	}
	if len(req.VolumeCapabilities) == 0 {
		return nil, status.Error(codes.InvalidArgument, "volume capabilities are required")
	}

	if _, ok := d.Store.GetVolume(req.VolumeId); !ok {
		return nil, status.Errorf(codes.NotFound, "volume %s not found", req.VolumeId)
	}

	if !isSupportedCapabilities(req.VolumeCapabilities) {
		return &csi.ValidateVolumeCapabilitiesResponse{}, nil
	}

	return &csi.ValidateVolumeCapabilitiesResponse{
		Confirmed: &csi.ValidateVolumeCapabilitiesResponse_Confirmed{
			VolumeCapabilities: req.VolumeCapabilities,
		},
	}, nil
}

// isSupportedCapabilities returns true if all access modes in caps are supported.
func isSupportedCapabilities(caps []*csi.VolumeCapability) bool {
	for _, cap := range caps {
		if cap.GetAccessMode() == nil {
			continue
		}
		if _, ok := supportedAccessModes[cap.AccessMode.Mode]; !ok {
			return false
		}
	}
	return true
}

func (d *Driver) CreateVolume(ctx context.Context, req *csi.CreateVolumeRequest) (*csi.CreateVolumeResponse, error) {
	if req.Name == "" {
		return nil, status.Error(codes.InvalidArgument, "volume name is required")
	}
	if len(req.VolumeCapabilities) == 0 {
		return nil, status.Error(codes.InvalidArgument, "volume capabilities are required")
	}

	// Idempotency: return existing volume if one with the same name exists.
	if existing, ok := d.Store.GetVolumeByName(req.Name); ok {
		if err := validateContentSourceMatch(existing, req.VolumeContentSource); err != nil {
			return nil, err
		}
		if !isCapacityCompatible(existing.CapacityBytes, req.CapacityRange) {
			return nil, status.Errorf(codes.AlreadyExists,
				"volume %q already exists with capacity %d, incompatible with requested range",
				req.Name, existing.CapacityBytes)
		}
		klog.V(4).InfoS("CreateVolume idempotent", "name", req.Name, "volumeID", existing.ID)
		return &csi.CreateVolumeResponse{Volume: toCSIVolume(existing, d.nodeID)}, nil
	}

	var capacityBytes int64
	if req.CapacityRange != nil {
		capacityBytes = req.CapacityRange.RequiredBytes
	}

	basePath, err := d.resolveBasePath(req.Parameters)
	if err != nil {
		return nil, err
	}
	id := uuid.New().String()
	subvolPath := filepath.Join(basePath, "volumes", id)

	if err := os.MkdirAll(filepath.Dir(subvolPath), 0o755); err != nil {
		return nil, status.Errorf(codes.Internal, "create volumes directory: %v", err)
	}

	vol := &state.Volume{
		ID:            id,
		Name:          req.Name,
		CapacityBytes: capacityBytes,
		SubvolumePath: subvolPath,
		NodeID:        d.nodeID,
	}

	if err := d.provisionVolume(subvolPath, req.VolumeContentSource, vol); err != nil {
		return nil, err
	}

	if capacityBytes > 0 {
		// Quotas must be enabled on the filesystem before setting per-subvolume limits.
		if err := d.Manager.EnsureQuotaEnabled(basePath); err != nil {
			return nil, status.Errorf(codes.Internal, "ensure quota enabled: %v", err)
		}
		if err := d.Manager.SetQgroupLimit(subvolPath, uint64(capacityBytes)); err != nil {
			return nil, status.Errorf(codes.Internal, "set qgroup limit: %v", err)
		}
	}

	if err := d.Store.SaveVolume(vol); err != nil {
		return nil, status.Errorf(codes.Internal, "save volume state: %v", err)
	}

	klog.V(4).InfoS("CreateVolume", "volumeID", id, "name", req.Name, "path", subvolPath)
	return &csi.CreateVolumeResponse{Volume: toCSIVolume(vol, d.nodeID)}, nil
}

func (d *Driver) DeleteVolume(ctx context.Context, req *csi.DeleteVolumeRequest) (*csi.DeleteVolumeResponse, error) {
	if req.VolumeId == "" {
		return nil, status.Error(codes.InvalidArgument, "volume ID is required")
	}

	vol, ok := d.Store.GetVolume(req.VolumeId)
	if !ok {
		// Already deleted — idempotent.
		return &csi.DeleteVolumeResponse{}, nil
	}

	if err := d.Manager.DeleteSubvolume(vol.SubvolumePath); err != nil {
		return nil, status.Errorf(codes.Internal, "delete subvolume: %v", err)
	}

	if err := d.Store.DeleteVolume(req.VolumeId); err != nil {
		return nil, status.Errorf(codes.Internal, "delete volume state: %v", err)
	}

	klog.V(4).InfoS("DeleteVolume", "volumeID", req.VolumeId)
	d.scheduleQgroupCleanup()
	return &csi.DeleteVolumeResponse{}, nil
}

func (d *Driver) ControllerExpandVolume(ctx context.Context, req *csi.ControllerExpandVolumeRequest) (*csi.ControllerExpandVolumeResponse, error) {
	if req.VolumeId == "" {
		return nil, status.Error(codes.InvalidArgument, "volume ID is required")
	}

	vol, ok := d.Store.GetVolume(req.VolumeId)
	if !ok {
		return nil, status.Errorf(codes.NotFound, "volume %s not found", req.VolumeId)
	}

	var newCapacity int64
	if req.CapacityRange != nil {
		newCapacity = req.CapacityRange.RequiredBytes
	}

	// Reject shrink attempts.
	if newCapacity > 0 && newCapacity < vol.CapacityBytes {
		return nil, status.Errorf(codes.InvalidArgument, "cannot shrink volume from %d to %d", vol.CapacityBytes, newCapacity)
	}

	// If no capacity specified or same as current, nothing to do.
	if newCapacity == 0 || newCapacity == vol.CapacityBytes {
		return &csi.ControllerExpandVolumeResponse{
			CapacityBytes:         vol.CapacityBytes,
			NodeExpansionRequired: false,
		}, nil
	}

	// Update qgroup limit.
	if err := d.Manager.SetQgroupLimit(vol.SubvolumePath, uint64(newCapacity)); err != nil {
		return nil, status.Errorf(codes.Internal, "set qgroup limit: %v", err)
	}

	// Update state.
	vol.CapacityBytes = newCapacity
	if err := d.Store.SaveVolume(vol); err != nil {
		return nil, status.Errorf(codes.Internal, "save volume state: %v", err)
	}

	klog.V(4).InfoS("ControllerExpandVolume", "volumeID", req.VolumeId, "oldCapacity", vol.CapacityBytes, "newCapacity", newCapacity)
	return &csi.ControllerExpandVolumeResponse{
		CapacityBytes:         newCapacity,
		NodeExpansionRequired: false,
	}, nil
}

// provisionVolume creates the subvolume at subvolPath, handling content sources for cloning.
// It sets SourceSnapID or SourceVolID on vol when cloning. On success the subvolume exists on disk.
func (d *Driver) provisionVolume(subvolPath string, src *csi.VolumeContentSource, vol *state.Volume) error {
	if src == nil {
		// Fresh empty subvolume.
		if err := d.Manager.CreateSubvolume(subvolPath); err != nil {
			return status.Errorf(codes.Internal, "create subvolume: %v", err)
		}
		return nil
	}

	switch t := src.Type.(type) {
	case *csi.VolumeContentSource_Snapshot:
		snapID := t.Snapshot.GetSnapshotId()
		snap, ok := d.Store.GetSnapshot(snapID)
		if !ok {
			return status.Errorf(codes.NotFound, "snapshot %s not found", snapID)
		}
		if err := d.Manager.CreateSnapshot(snap.SnapshotPath, subvolPath, false); err != nil {
			return status.Errorf(codes.Internal, "clone from snapshot: %v", err)
		}
		vol.SourceSnapID = snapID

	case *csi.VolumeContentSource_Volume:
		srcVolID := t.Volume.GetVolumeId()
		srcVol, ok := d.Store.GetVolume(srcVolID)
		if !ok {
			return status.Errorf(codes.NotFound, "source volume %s not found", srcVolID)
		}
		if err := d.Manager.CreateSnapshot(srcVol.SubvolumePath, subvolPath, false); err != nil {
			return status.Errorf(codes.Internal, "clone volume: %v", err)
		}
		vol.SourceVolID = srcVolID

	default:
		return status.Errorf(codes.InvalidArgument, "unsupported volume content source type")
	}

	return nil
}

func (d *Driver) CreateSnapshot(_ context.Context, req *csi.CreateSnapshotRequest) (*csi.CreateSnapshotResponse, error) {
	klog.V(2).InfoS("CreateSnapshot called", "name", req.Name, "sourceVolumeId", req.SourceVolumeId)

	if req.Name == "" {
		return nil, status.Error(codes.InvalidArgument, "snapshot name is required")
	}
	if req.SourceVolumeId == "" {
		return nil, status.Error(codes.InvalidArgument, "source volume ID is required")
	}

	// Idempotency: return existing snapshot if one with the same name exists.
	if existing, ok := d.Store.GetSnapshotByName(req.Name); ok {
		if existing.SourceVolID != req.SourceVolumeId {
			return nil, status.Errorf(codes.AlreadyExists, "snapshot %q already exists with different source volume %s", req.Name, existing.SourceVolID)
		}
		klog.V(4).InfoS("CreateSnapshot idempotent", "name", req.Name, "snapshotID", existing.ID)
		return &csi.CreateSnapshotResponse{Snapshot: toCSISnapshot(existing)}, nil
	}

	srcVol, ok := d.Store.GetVolume(req.SourceVolumeId)
	if !ok {
		return nil, status.Errorf(codes.NotFound, "source volume %s not found", req.SourceVolumeId)
	}

	// Derive basePath from the source volume's subvolume path: <basePath>/volumes/<id>
	basePath := filepath.Dir(filepath.Dir(srcVol.SubvolumePath))
	id := uuid.New().String()
	snapPath := filepath.Join(basePath, "snapshots", id)

	if err := os.MkdirAll(filepath.Dir(snapPath), 0o755); err != nil {
		return nil, status.Errorf(codes.Internal, "create snapshots directory: %v", err)
	}

	if err := d.Manager.CreateSnapshot(srcVol.SubvolumePath, snapPath, true); err != nil {
		return nil, status.Errorf(codes.Internal, "create snapshot: %v", err)
	}

	now := time.Now()
	snap := &state.Snapshot{
		ID:           id,
		Name:         req.Name,
		SourceVolID:  req.SourceVolumeId,
		SnapshotPath: snapPath,
		CreatedAt:    now,
		ReadyToUse:   true,
	}
	if err := d.Store.SaveSnapshot(snap); err != nil {
		return nil, status.Errorf(codes.Internal, "save snapshot state: %v", err)
	}

	klog.V(4).InfoS("CreateSnapshot", "snapshotID", id, "name", req.Name, "sourceVolID", req.SourceVolumeId)
	return &csi.CreateSnapshotResponse{Snapshot: toCSISnapshot(snap)}, nil
}

func (d *Driver) DeleteSnapshot(_ context.Context, req *csi.DeleteSnapshotRequest) (*csi.DeleteSnapshotResponse, error) {
	if req.SnapshotId == "" {
		return nil, status.Error(codes.InvalidArgument, "snapshot ID is required")
	}

	snap, ok := d.Store.GetSnapshot(req.SnapshotId)
	if !ok {
		// Already deleted — idempotent.
		return &csi.DeleteSnapshotResponse{}, nil
	}

	if err := d.Manager.DeleteSubvolume(snap.SnapshotPath); err != nil {
		return nil, status.Errorf(codes.Internal, "delete snapshot subvolume: %v", err)
	}

	if err := d.Store.DeleteSnapshot(req.SnapshotId); err != nil {
		return nil, status.Errorf(codes.Internal, "delete snapshot state: %v", err)
	}

	klog.V(4).InfoS("DeleteSnapshot", "snapshotID", req.SnapshotId)
	d.scheduleQgroupCleanup()
	return &csi.DeleteSnapshotResponse{}, nil
}

// toCSISnapshot converts a state.Snapshot to the CSI Snapshot proto.
func toCSISnapshot(snap *state.Snapshot) *csi.Snapshot {
	return &csi.Snapshot{
		SnapshotId:     snap.ID,
		SourceVolumeId: snap.SourceVolID,
		SizeBytes:      snap.SizeBytes,
		CreationTime:   timestamppb.New(snap.CreatedAt),
		ReadyToUse:     snap.ReadyToUse,
	}
}

// isCapacityCompatible returns true if existingBytes satisfies the requested CapacityRange.
func isCapacityCompatible(existingBytes int64, cr *csi.CapacityRange) bool {
	if cr == nil {
		return true
	}
	if cr.RequiredBytes > 0 && existingBytes < cr.RequiredBytes {
		return false
	}
	if cr.LimitBytes > 0 && existingBytes > cr.LimitBytes {
		return false
	}
	return true
}

// validateContentSourceMatch checks that an existing volume's content source matches the request.
// Returns AlreadyExists if there is a mismatch.
func validateContentSourceMatch(vol *state.Volume, src *csi.VolumeContentSource) error {
	reqSnapID, reqVolID := contentSourceIDs(src)

	if vol.SourceSnapID != reqSnapID || vol.SourceVolID != reqVolID {
		return status.Errorf(codes.AlreadyExists, "volume %q already exists with different content source", vol.Name)
	}
	return nil
}

// contentSourceIDs extracts the snapshot ID and volume ID from a VolumeContentSource.
func contentSourceIDs(src *csi.VolumeContentSource) (snapID, volID string) {
	if src == nil {
		return "", ""
	}
	switch t := src.Type.(type) {
	case *csi.VolumeContentSource_Snapshot:
		return t.Snapshot.GetSnapshotId(), ""
	case *csi.VolumeContentSource_Volume:
		return "", t.Volume.GetVolumeId()
	}
	return "", ""
}

// resolveBasePath returns the basePath from StorageClass parameters, falling back to rootPath.
// It validates the basePath to prevent path traversal attacks.
func (d *Driver) resolveBasePath(params map[string]string) (string, error) {
	if bp := params["basePath"]; bp != "" {
		// Validate the path is absolute and doesn't contain traversal
		if !filepath.IsAbs(bp) {
			return "", status.Errorf(codes.InvalidArgument, "basePath must be absolute: %q", bp)
		}
		// Clean and check for traversal
		cleaned := filepath.Clean(bp)
		if strings.Contains(cleaned, "..") {
			return "", status.Errorf(codes.InvalidArgument, "basePath contains invalid path traversal: %q", bp)
		}
		return cleaned, nil
	}
	return d.rootPath, nil
}

// toCSIVolume converts a state.Volume to the CSI Volume proto with topology.
func toCSIVolume(vol *state.Volume, nodeID string) *csi.Volume {
	v := &csi.Volume{
		VolumeId:      vol.ID,
		CapacityBytes: vol.CapacityBytes,
		AccessibleTopology: []*csi.Topology{
			{Segments: map[string]string{topologyKey: nodeID}},
		},
	}
	if vol.SourceSnapID != "" {
		v.ContentSource = &csi.VolumeContentSource{
			Type: &csi.VolumeContentSource_Snapshot{
				Snapshot: &csi.VolumeContentSource_SnapshotSource{SnapshotId: vol.SourceSnapID},
			},
		}
	} else if vol.SourceVolID != "" {
		v.ContentSource = &csi.VolumeContentSource{
			Type: &csi.VolumeContentSource_Volume{
				Volume: &csi.VolumeContentSource_VolumeSource{VolumeId: vol.SourceVolID},
			},
		}
	}
	return v
}
