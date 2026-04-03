package driver

import (
	"context"
	"os"

	"github.com/container-storage-interface/spec/lib/go/csi"
	"google.golang.org/protobuf/types/known/wrapperspb"
	"k8s.io/klog/v2"
)

func (d *Driver) GetPluginInfo(_ context.Context, _ *csi.GetPluginInfoRequest) (*csi.GetPluginInfoResponse, error) {
	klog.V(5).InfoS("GetPluginInfo called")
	return &csi.GetPluginInfoResponse{
		Name:          d.name,
		VendorVersion: d.version,
	}, nil
}

func (d *Driver) GetPluginCapabilities(_ context.Context, _ *csi.GetPluginCapabilitiesRequest) (*csi.GetPluginCapabilitiesResponse, error) {
	klog.V(5).InfoS("GetPluginCapabilities called")
	return &csi.GetPluginCapabilitiesResponse{
		Capabilities: []*csi.PluginCapability{
			{
				Type: &csi.PluginCapability_Service_{
					Service: &csi.PluginCapability_Service{
						Type: csi.PluginCapability_Service_CONTROLLER_SERVICE,
					},
				},
			},
			{
				Type: &csi.PluginCapability_Service_{
					Service: &csi.PluginCapability_Service{
						Type: csi.PluginCapability_Service_VOLUME_ACCESSIBILITY_CONSTRAINTS,
					},
				},
			},
		},
	}, nil
}

func (d *Driver) Probe(_ context.Context, _ *csi.ProbeRequest) (*csi.ProbeResponse, error) {
	klog.V(5).InfoS("Probe called", "rootPath", d.rootPath)

	// Verify btrfs is operational by checking if root path exists
	_, err := os.Stat(d.rootPath)
	if err != nil {
		klog.V(2).InfoS("Probe: root path check failed", "rootPath", d.rootPath, "error", err)
		return &csi.ProbeResponse{
			Ready: wrapperspb.Bool(false),
		}, nil
	}

	klog.V(5).InfoS("Probe: healthy", "rootPath", d.rootPath)
	return &csi.ProbeResponse{
		Ready: wrapperspb.Bool(true),
	}, nil
}

// Ensure Driver implements the CSI Identity server (compile-time check).
var _ csi.IdentityServer = (*Driver)(nil)
