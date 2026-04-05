package driver

import (
	"context"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"time"

	"github.com/container-storage-interface/spec/lib/go/csi"
	"github.com/google/uuid"
	"github.com/mporrato/btrfs-csi/pkg/state"
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

func (d *Driver) ControllerGetCapabilities(_ context.Context,
	_ *csi.ControllerGetCapabilitiesRequest) (*csi.ControllerGetCapabilitiesResponse, error) {
	caps := []csi.ControllerServiceCapability_RPC_Type{
		csi.ControllerServiceCapability_RPC_CREATE_DELETE_VOLUME,
		csi.ControllerServiceCapability_RPC_CREATE_DELETE_SNAPSHOT,
		csi.ControllerServiceCapability_RPC_CLONE_VOLUME,
		csi.ControllerServiceCapability_RPC_EXPAND_VOLUME,
		csi.ControllerServiceCapability_RPC_GET_CAPACITY,
		csi.ControllerServiceCapability_RPC_LIST_VOLUMES,
		csi.ControllerServiceCapability_RPC_LIST_SNAPSHOTS,
		csi.ControllerServiceCapability_RPC_GET_VOLUME,
		csi.ControllerServiceCapability_RPC_VOLUME_CONDITION,
	}
	out := make([]*csi.ControllerServiceCapability, 0, len(caps))
	for _, c := range caps {
		out = append(out, &csi.ControllerServiceCapability{
			Type: &csi.ControllerServiceCapability_Rpc{
				Rpc: &csi.ControllerServiceCapability_RPC{Type: c},
			},
		})
	}
	return &csi.ControllerGetCapabilitiesResponse{Capabilities: out}, nil
}

func (d *Driver) GetCapacity(_ context.Context, req *csi.GetCapacityRequest) (*csi.GetCapacityResponse, error) {
	basePath, err := d.resolveBasePath(req.Parameters)
	if err != nil {
		return nil, err
	}
	usage, err := d.GetFilesystemUsage(basePath)
	if err != nil {
		return nil, status.Errorf(codes.Internal, "get filesystem usage: %v", err)
	}
	//nolint:gosec // filesystem capacity always fits in int64
	return &csi.GetCapacityResponse{AvailableCapacity: int64(usage.Available)}, nil
}

// ListVolumes returns all volumes known to this driver.
// Pagination is not supported: if a starting_token is provided the request is
// rejected. TODO: add pagination support if volume counts grow large.
func (d *Driver) ListVolumes(_ context.Context, req *csi.ListVolumesRequest) (*csi.ListVolumesResponse, error) {
	if req.StartingToken != "" {
		return nil, status.Error(codes.Aborted, "pagination not supported")
	}
	vols := d.Store.ListVolumes()
	entries := make([]*csi.ListVolumesResponse_Entry, 0, len(vols))
	for _, v := range vols {
		entries = append(entries, &csi.ListVolumesResponse_Entry{Volume: toCSIVolume(v, d.nodeID)})
	}
	return &csi.ListVolumesResponse{Entries: entries}, nil
}

// ListSnapshots returns snapshots known to this driver, with optional filtering
// by snapshot ID or source volume ID. Supports max_entries and starting_token
// for pagination; the token is a numeric offset into the sorted snapshot list.
// (TODO: consider cursor-based tokens if snapshot ordering changes under load.)
func (d *Driver) ListSnapshots(_ context.Context, req *csi.ListSnapshotsRequest) (*csi.ListSnapshotsResponse, error) {
	// Fast path: single snapshot lookup by ID.
	if req.SnapshotId != "" {
		snap, ok := d.Store.GetSnapshot(req.SnapshotId)
		if !ok {
			return &csi.ListSnapshotsResponse{}, nil
		}
		return &csi.ListSnapshotsResponse{
			Entries: []*csi.ListSnapshotsResponse_Entry{{Snapshot: toCSISnapshot(snap)}},
		}, nil
	}

	all := d.Store.ListSnapshots()

	// Filter by source volume ID if requested.
	if req.SourceVolumeId != "" {
		filtered := all[:0:0] // zero-capacity slice; appends allocate a fresh backing array
		for _, s := range all {
			if s.SourceVolID == req.SourceVolumeId {
				filtered = append(filtered, s)
			}
		}
		all = filtered
	}

	// Sort by ID for stable pagination.
	sort.Slice(all, func(i, j int) bool { return all[i].ID < all[j].ID })

	// Apply starting token (numeric offset).
	start := 0
	if req.StartingToken != "" {
		idx, err := strconv.Atoi(req.StartingToken)
		if err != nil || idx < 0 {
			return nil, status.Errorf(codes.Aborted, "invalid starting_token: %q", req.StartingToken)
		}
		start = idx
	}
	if start >= len(all) {
		return &csi.ListSnapshotsResponse{}, nil
	}
	all = all[start:]

	// Apply max_entries limit and set next token if more remain.
	var nextToken string
	if req.MaxEntries > 0 && int(req.MaxEntries) < len(all) {
		all = all[:req.MaxEntries]
		nextToken = strconv.Itoa(start + int(req.MaxEntries))
	}

	entries := make([]*csi.ListSnapshotsResponse_Entry, 0, len(all))
	for _, s := range all {
		entries = append(entries, &csi.ListSnapshotsResponse_Entry{Snapshot: toCSISnapshot(s)})
	}
	return &csi.ListSnapshotsResponse{Entries: entries, NextToken: nextToken}, nil
}

func (d *Driver) ControllerGetVolume(_ context.Context,
	req *csi.ControllerGetVolumeRequest) (*csi.ControllerGetVolumeResponse, error) {
	if req.VolumeId == "" {
		return nil, status.Error(codes.InvalidArgument, "volume ID is required")
	}
	vol, ok := d.Store.GetVolume(req.VolumeId)
	if !ok {
		return nil, status.Errorf(codes.NotFound, "volume %s not found", req.VolumeId)
	}
	condition := &csi.VolumeCondition{Message: "volume is healthy"}
	if _, err := os.Stat(vol.Path()); err != nil {
		condition.Abnormal = true
		condition.Message = "subvolume path does not exist on disk"
	}
	return &csi.ControllerGetVolumeResponse{
		Volume: toCSIVolume(vol, d.nodeID),
		Status: &csi.ControllerGetVolumeResponse_VolumeStatus{
			VolumeCondition: condition,
		},
	}, nil
}

func (d *Driver) ValidateVolumeCapabilities(_ context.Context,
	req *csi.ValidateVolumeCapabilitiesRequest) (*csi.ValidateVolumeCapabilitiesResponse, error) {
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

	d.controllerMu.Lock()
	defer d.controllerMu.Unlock()

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

	vol := &state.Volume{
		ID:            uuid.New().String(),
		Name:          req.Name,
		CapacityBytes: capacityBytes,
		BasePath:      basePath,
	}

	if err := os.MkdirAll(filepath.Dir(vol.Path()), 0o750); err != nil {
		return nil, status.Errorf(codes.Internal, "create volumes directory: %v", err)
	}

	if err := d.provisionVolume(vol.Path(), req.VolumeContentSource, vol); err != nil {
		return nil, err
	}

	if capacityBytes > 0 {
		// Quotas must be enabled on the filesystem before setting per-subvolume limits.
		if err := d.EnsureQuotaEnabled(basePath); err != nil {
			return nil, status.Errorf(codes.Internal, "ensure quota enabled: %v", err)
		}
		if err := d.SetQgroupLimit(vol.Path(), uint64(capacityBytes)); err != nil {
			return nil, status.Errorf(codes.Internal, "set qgroup limit: %v", err)
		}
	}

	if err := d.Store.SaveVolume(vol); err != nil {
		return nil, status.Errorf(codes.Internal, "save volume state: %v", err)
	}

	klog.V(4).InfoS("CreateVolume", "volumeID", vol.ID, "name", req.Name, "path", vol.Path())
	return &csi.CreateVolumeResponse{Volume: toCSIVolume(vol, d.nodeID)}, nil
}

//nolint:dupl // DeleteVolume and DeleteSnapshot must have parallel structure per CSI spec
func (d *Driver) DeleteVolume(ctx context.Context, req *csi.DeleteVolumeRequest) (*csi.DeleteVolumeResponse, error) {
	if req.VolumeId == "" {
		return nil, status.Error(codes.InvalidArgument, "volume ID is required")
	}

	vol, ok := d.Store.GetVolume(req.VolumeId)
	if !ok {
		// Already deleted — idempotent.
		return &csi.DeleteVolumeResponse{}, nil
	}

	if err := d.DeleteSubvolume(vol.Path()); err != nil {
		return nil, status.Errorf(codes.Internal, "delete subvolume: %v", err)
	}

	if err := d.Store.DeleteVolume(req.VolumeId); err != nil {
		return nil, status.Errorf(codes.Internal, "delete volume state: %v", err)
	}

	klog.V(4).InfoS("DeleteVolume", "volumeID", req.VolumeId)
	d.scheduleQgroupCleanup(vol.BasePath, qgroupCleanupDelay)
	return &csi.DeleteVolumeResponse{}, nil
}

func (d *Driver) ControllerExpandVolume(ctx context.Context,
	req *csi.ControllerExpandVolumeRequest) (*csi.ControllerExpandVolumeResponse, error) {
	if req.VolumeId == "" {
		return nil, status.Error(codes.InvalidArgument, "volume ID is required")
	}

	d.controllerMu.Lock()
	defer d.controllerMu.Unlock()

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
	if err := d.SetQgroupLimit(vol.Path(), uint64(newCapacity)); err != nil {
		return nil, status.Errorf(codes.Internal, "set qgroup limit: %v", err)
	}

	// Update state.
	vol.CapacityBytes = newCapacity
	if err := d.Store.SaveVolume(vol); err != nil {
		return nil, status.Errorf(codes.Internal, "save volume state: %v", err)
	}

	klog.V(4).InfoS("ControllerExpandVolume", "volumeID", req.VolumeId, "newCapacity", newCapacity)
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
		if err := d.CreateSubvolume(subvolPath); err != nil {
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
		if err := d.Manager.CreateSnapshot(snap.Path(), subvolPath, false); err != nil {
			return status.Errorf(codes.Internal, "clone from snapshot: %v", err)
		}
		vol.SourceSnapID = snapID

	case *csi.VolumeContentSource_Volume:
		srcVolID := t.Volume.GetVolumeId()
		srcVol, ok := d.Store.GetVolume(srcVolID)
		if !ok {
			return status.Errorf(codes.NotFound, "source volume %s not found", srcVolID)
		}
		if err := d.Manager.CreateSnapshot(srcVol.Path(), subvolPath, false); err != nil {
			return status.Errorf(codes.Internal, "clone volume: %v", err)
		}
		vol.SourceVolID = srcVolID

	default:
		return status.Errorf(codes.InvalidArgument, "unsupported volume content source type")
	}

	return nil
}

func (d *Driver) CreateSnapshot(_ context.Context,
	req *csi.CreateSnapshotRequest) (*csi.CreateSnapshotResponse, error) {
	klog.V(2).InfoS("CreateSnapshot called", "name", req.Name, "sourceVolumeId", req.SourceVolumeId)

	if req.Name == "" {
		return nil, status.Error(codes.InvalidArgument, "snapshot name is required")
	}
	if req.SourceVolumeId == "" {
		return nil, status.Error(codes.InvalidArgument, "source volume ID is required")
	}

	d.controllerMu.Lock()
	defer d.controllerMu.Unlock()

	// Resolve the destination pool for the snapshot.
	basePath, err := d.resolveBasePath(req.Parameters)
	if err != nil {
		return nil, err
	}

	// Idempotency: return existing snapshot if one with the same name exists.
	if existing, ok := d.Store.GetSnapshotByName(req.Name); ok {
		if existing.SourceVolID != req.SourceVolumeId {
			return nil, status.Errorf(codes.AlreadyExists,
				"snapshot %q already exists with different source volume", req.Name)
		}
		// Also verify the existing snapshot is in the same pool as the current request.
		if existing.BasePath != basePath {
			return nil, status.Errorf(codes.AlreadyExists,
				"snapshot %q already exists in a different pool", req.Name)
		}
		klog.V(4).InfoS("CreateSnapshot idempotent", "name", req.Name, "snapshotID", existing.ID)
		return &csi.CreateSnapshotResponse{Snapshot: toCSISnapshot(existing)}, nil
	}

	srcVol, ok := d.Store.GetVolume(req.SourceVolumeId)
	if !ok {
		return nil, status.Errorf(codes.NotFound, "source volume %s not found", req.SourceVolumeId)
	}
	id := uuid.New().String()
	snap := &state.Snapshot{
		ID:          id,
		Name:        req.Name,
		SourceVolID: req.SourceVolumeId,
		BasePath:    basePath,
		CreatedAt:   time.Now(),
		ReadyToUse:  true,
	}

	if err := os.MkdirAll(filepath.Dir(snap.Path()), 0o750); err != nil {
		return nil, status.Errorf(codes.Internal, "create snapshots directory: %v", err)
	}

	if err := d.Manager.CreateSnapshot(srcVol.Path(), snap.Path(), true); err != nil {
		return nil, status.Errorf(codes.Internal, "create snapshot: %v", err)
	}
	if err := d.Store.SaveSnapshot(snap); err != nil {
		return nil, status.Errorf(codes.Internal, "save snapshot state: %v", err)
	}

	klog.V(4).InfoS("CreateSnapshot", "snapshotID", id, "name", req.Name, "sourceVolID", req.SourceVolumeId)
	return &csi.CreateSnapshotResponse{Snapshot: toCSISnapshot(snap)}, nil
}

//nolint:dupl // DeleteSnapshot and DeleteVolume must have parallel structure per CSI spec
func (d *Driver) DeleteSnapshot(_ context.Context,
	req *csi.DeleteSnapshotRequest) (*csi.DeleteSnapshotResponse, error) {
	if req.SnapshotId == "" {
		return nil, status.Error(codes.InvalidArgument, "snapshot ID is required")
	}

	snap, ok := d.Store.GetSnapshot(req.SnapshotId)
	if !ok {
		// Already deleted — idempotent.
		return &csi.DeleteSnapshotResponse{}, nil
	}

	if err := d.DeleteSubvolume(snap.Path()); err != nil {
		return nil, status.Errorf(codes.Internal, "delete snapshot subvolume: %v", err)
	}

	if err := d.Store.DeleteSnapshot(req.SnapshotId); err != nil {
		return nil, status.Errorf(codes.Internal, "delete snapshot state: %v", err)
	}

	klog.V(4).InfoS("DeleteSnapshot", "snapshotID", req.SnapshotId)
	d.scheduleQgroupCleanup(snap.BasePath, qgroupCleanupDelay)
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
//
//nolint:gocritic // unnamedResult conflicts with nonamedreturns linter
func contentSourceIDs(src *csi.VolumeContentSource) (string, string) {
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

// resolveBasePath resolves the storage pool for a volume or snapshot request.
// With a "pool" parameter, it looks up the named pool. Without one:
//   - single pool configured: uses it regardless of name
//   - multiple pools: falls back to the "default" pool, or fails if absent
func (d *Driver) resolveBasePath(params map[string]string) (string, error) {
	pools := d.getPools()
	if len(pools) == 0 {
		return "", status.Errorf(codes.Internal, "no storage pools configured")
	}

	poolName := params["pool"]

	if poolName != "" {
		path, ok := pools[poolName]
		if !ok {
			return "", status.Errorf(codes.InvalidArgument, "unknown storage pool %q", poolName)
		}
		return path, nil
	}

	// No pool param specified.
	if len(pools) == 1 {
		for _, path := range pools {
			return path, nil
		}
	}

	// Multiple pools — fall back to "default".
	if path, ok := pools["default"]; ok {
		return path, nil
	}
	return "", status.Errorf(codes.InvalidArgument,
		"multiple storage pools configured without \"default\"; specify pool in StorageClass")
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
