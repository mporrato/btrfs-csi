package driver

import (
	"context"
	"path/filepath"

	"github.com/container-storage-interface/spec/lib/go/csi"
	"github.com/google/uuid"
	"github.com/guru/btrfs-csi/pkg/state"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
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
	if existing, ok := d.GetVolumeByName(req.Name); ok {
		klog.V(4).InfoS("CreateVolume idempotent", "name", req.Name, "volumeID", existing.ID)
		return &csi.CreateVolumeResponse{Volume: toCSIVolume(existing, d.nodeID)}, nil
	}

	var capacityBytes int64
	if req.CapacityRange != nil {
		capacityBytes = req.CapacityRange.RequiredBytes
	}

	basePath := d.resolveBasePath(req.Parameters)
	id := uuid.New().String()
	subvolPath := filepath.Join(basePath, "volumes", id)

	// TODO: if CreateSubvolume succeeds but a later step fails, the subvolume is
	// orphaned on disk until a future CreateVolume with the same name recreates it.
	if err := d.Manager.CreateSubvolume(subvolPath); err != nil {
		return nil, status.Errorf(codes.Internal, "create subvolume: %v", err)
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

	vol := &state.Volume{
		ID:            id,
		Name:          req.Name,
		CapacityBytes: capacityBytes,
		SubvolumePath: subvolPath,
		NodeID:        d.nodeID,
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
	return &csi.DeleteVolumeResponse{}, nil
}

// resolveBasePath returns the basePath from StorageClass parameters, falling back to rootPath.
func (d *Driver) resolveBasePath(params map[string]string) string {
	if bp := params["basePath"]; bp != "" {
		return bp
	}
	return d.rootPath
}

// toCSIVolume converts a state.Volume to the CSI Volume proto with topology.
func toCSIVolume(vol *state.Volume, nodeID string) *csi.Volume {
	return &csi.Volume{
		VolumeId:      vol.ID,
		CapacityBytes: vol.CapacityBytes,
		AccessibleTopology: []*csi.Topology{
			{Segments: map[string]string{topologyKey: nodeID}},
		},
	}
}
