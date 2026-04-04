package driver

import (
	"context"
	"os"
	"strings"

	"github.com/container-storage-interface/spec/lib/go/csi"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"k8s.io/klog/v2"
	mountutils "k8s.io/mount-utils"
)

// validatePath validates that the given path is safe and does not contain path traversal patterns.
func validatePath(path string) error {
	if path == "" {
		return status.Error(codes.InvalidArgument, "path is required")
	}
	// Check for path traversal patterns before cleaning
	for _, part := range strings.Split(path, "/") {
		if part == ".." {
			return status.Errorf(codes.InvalidArgument, "path %q contains invalid traversal sequence", path)
		}
	}
	return nil
}

// Mounter abstracts mount/unmount operations for testability.
type Mounter interface {
	// Mount binds source to target with the given filesystem type and options.
	Mount(source, target, fsType string, options ...string) error
	// Unmount detaches the filesystem at target.
	Unmount(target string) error
	// IsMountPoint returns true if file is a mount point.
	IsMountPoint(file string) (bool, error)
}

// realMounter implements Mounter using k8s.io/mount-utils.
type realMounter struct {
	mounter mountutils.Interface
}

// newRealMounter creates a realMounter using the host's mount namespace.
func newRealMounter() *realMounter {
	return &realMounter{
		mounter: mountutils.New(""),
	}
}

func (m *realMounter) Mount(source, target, fsType string, options ...string) error {
	return m.mounter.Mount(source, target, fsType, options)
}

func (m *realMounter) Unmount(target string) error {
	return m.mounter.Unmount(target)
}

func (m *realMounter) IsMountPoint(file string) (bool, error) {
	return m.mounter.IsMountPoint(file)
}

func (d *Driver) NodePublishVolume(_ context.Context, req *csi.NodePublishVolumeRequest) (*csi.NodePublishVolumeResponse, error) {
	klog.V(5).InfoS("NodePublishVolume called",
		"volumeID", req.GetVolumeId(),
		"targetPath", req.GetTargetPath(),
		"readonly", req.GetReadonly(),
	)

	if req.GetVolumeId() == "" {
		return nil, status.Error(codes.InvalidArgument, "volume ID is required")
	}
	if req.GetTargetPath() == "" {
		return nil, status.Error(codes.InvalidArgument, "target path is required")
	}
	if err := validatePath(req.GetTargetPath()); err != nil {
		return nil, err
	}

	vol, ok := d.Store.GetVolume(req.GetVolumeId())
	if !ok {
		return nil, status.Errorf(codes.NotFound, "volume %s not found", req.GetVolumeId())
	}

	targetPath := req.GetTargetPath()

	if err := os.MkdirAll(targetPath, 0o755); err != nil {
		return nil, status.Errorf(codes.Internal, "create target directory: %v", err)
	}

	// Idempotency check: if target is already mounted, return success
	isMount, err := d.mounter.IsMountPoint(targetPath)
	if err == nil && isMount {
		klog.V(4).InfoS("NodePublishVolume: target already mounted, returning success",
			"volumeID", req.GetVolumeId(),
			"target", targetPath,
		)
		return &csi.NodePublishVolumeResponse{}, nil
	}

	var options []string
	if req.GetReadonly() {
		options = append(options, "ro")
	}

	if err := d.mounter.Mount(vol.SubvolumePath, targetPath, "none", options...); err != nil {
		return nil, status.Errorf(codes.Internal, "mount %s to %s: %v", vol.SubvolumePath, targetPath, err)
	}

	klog.V(4).InfoS("NodePublishVolume success",
		"volumeID", req.GetVolumeId(),
		"source", vol.SubvolumePath,
		"target", targetPath,
		"readonly", req.GetReadonly(),
	)
	return &csi.NodePublishVolumeResponse{}, nil
}

func (d *Driver) NodeUnpublishVolume(_ context.Context, req *csi.NodeUnpublishVolumeRequest) (*csi.NodeUnpublishVolumeResponse, error) {
	klog.V(5).InfoS("NodeUnpublishVolume called",
		"volumeID", req.GetVolumeId(),
		"targetPath", req.GetTargetPath(),
	)

	if req.GetVolumeId() == "" {
		return nil, status.Error(codes.InvalidArgument, "volume ID is required")
	}
	if req.GetTargetPath() == "" {
		return nil, status.Error(codes.InvalidArgument, "target path is required")
	}
	if err := validatePath(req.GetTargetPath()); err != nil {
		return nil, err
	}

	targetPath := req.GetTargetPath()

	isMount, err := d.mounter.IsMountPoint(targetPath)
	if err != nil {
		return nil, status.Errorf(codes.Internal, "check mount point %s: %v", targetPath, err)
	}
	if !isMount {
		klog.V(4).InfoS("NodeUnpublishVolume: target not mounted, returning success", "targetPath", targetPath)
		os.Remove(targetPath)
		return &csi.NodeUnpublishVolumeResponse{}, nil
	}

	if err := d.mounter.Unmount(targetPath); err != nil {
		return nil, status.Errorf(codes.Internal, "unmount %s: %v", targetPath, err)
	}

	if err := os.Remove(targetPath); err != nil && !os.IsNotExist(err) {
		klog.V(2).InfoS("NodeUnpublishVolume: failed to remove target directory", "targetPath", targetPath, "error", err)
	}

	klog.V(4).InfoS("NodeUnpublishVolume success",
		"volumeID", req.GetVolumeId(),
		"target", targetPath,
	)
	return &csi.NodeUnpublishVolumeResponse{}, nil
}

func (d *Driver) NodeGetInfo(_ context.Context, _ *csi.NodeGetInfoRequest) (*csi.NodeGetInfoResponse, error) {
	klog.V(5).InfoS("NodeGetInfo called")
	return &csi.NodeGetInfoResponse{
		NodeId: d.nodeID,
		AccessibleTopology: &csi.Topology{
			Segments: map[string]string{topologyKey: d.nodeID},
		},
	}, nil
}

func (d *Driver) NodeGetCapabilities(_ context.Context, _ *csi.NodeGetCapabilitiesRequest) (*csi.NodeGetCapabilitiesResponse, error) {
	klog.V(5).InfoS("NodeGetCapabilities called")
	return &csi.NodeGetCapabilitiesResponse{
		Capabilities: []*csi.NodeServiceCapability{
			{
				Type: &csi.NodeServiceCapability_Rpc{
					Rpc: &csi.NodeServiceCapability_RPC{
						Type: csi.NodeServiceCapability_RPC_GET_VOLUME_STATS,
					},
				},
			},
		},
	}, nil
}

func (d *Driver) NodeGetVolumeStats(_ context.Context, req *csi.NodeGetVolumeStatsRequest) (*csi.NodeGetVolumeStatsResponse, error) {
	klog.V(5).InfoS("NodeGetVolumeStats called",
		"volumeID", req.GetVolumeId(),
		"volumePath", req.GetVolumePath(),
	)

	if req.GetVolumeId() == "" {
		return nil, status.Error(codes.InvalidArgument, "volume ID is required")
	}
	if req.GetVolumePath() == "" {
		return nil, status.Error(codes.InvalidArgument, "volume path is required")
	}

	vol, ok := d.Store.GetVolume(req.GetVolumeId())
	if !ok {
		return nil, status.Errorf(codes.NotFound, "volume %s not found", req.GetVolumeId())
	}

	usage, err := d.Manager.GetQgroupUsage(vol.SubvolumePath)
	if err != nil {
		return nil, status.Errorf(codes.Internal, "get qgroup usage for %s: %v", vol.SubvolumePath, err)
	}

	var available int64
	if usage.MaxRfer > 0 {
		available = int64(usage.MaxRfer) - int64(usage.Referenced)
		if available < 0 {
			available = 0
		}
	}

	return &csi.NodeGetVolumeStatsResponse{
		Usage: []*csi.VolumeUsage{
			{
				Total:     int64(usage.MaxRfer),
				Used:      int64(usage.Referenced),
				Available: available,
				Unit:      csi.VolumeUsage_BYTES,
			},
		},
	}, nil
}

func (d *Driver) NodeExpandVolume(_ context.Context, req *csi.NodeExpandVolumeRequest) (*csi.NodeExpandVolumeResponse, error) {
	klog.V(5).InfoS("NodeExpandVolume called", "volumeID", req.GetVolumeId())
	// Node expansion is not required for this driver (handled by ControllerExpandVolume).
	return &csi.NodeExpandVolumeResponse{}, nil
}

// Ensure Driver implements the CSI Node server (compile-time check).
var _ csi.NodeServer = (*Driver)(nil)
