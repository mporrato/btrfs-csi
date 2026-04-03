package driver

import (
	"context"
	"testing"

	"github.com/container-storage-interface/spec/lib/go/csi"
)

func TestGetPluginInfo(t *testing.T) {
	d := newTestDriver()
	resp, err := d.GetPluginInfo(context.Background(), &csi.GetPluginInfoRequest{})
	if err != nil {
		t.Fatalf("GetPluginInfo returned error: %v", err)
	}

	if resp.GetName() != "btrfs.csi.local" {
		t.Errorf("GetPluginInfo name = %q, want %q", resp.GetName(), "btrfs.csi.local")
	}

	if resp.GetVendorVersion() == "" {
		t.Error("GetPluginInfo vendor_version is empty, want non-empty version")
	}
}

func TestGetPluginCapabilities(t *testing.T) {
	d := newTestDriver()
	resp, err := d.GetPluginCapabilities(context.Background(), &csi.GetPluginCapabilitiesRequest{})
	if err != nil {
		t.Fatalf("GetPluginCapabilities returned error: %v", err)
	}

	found := false
	for _, cap := range resp.GetCapabilities() {
		if svc := cap.GetService(); svc != nil {
			if svc.GetType() == csi.PluginCapability_Service_CONTROLLER_SERVICE {
				found = true
				break
			}
		}
	}

	if !found {
		t.Error("GetPluginCapabilities: CONTROLLER_SERVICE capability not found")
	}
}

func TestProbe(t *testing.T) {
	d := newTestDriver()
	resp, err := d.Probe(context.Background(), &csi.ProbeRequest{})
	if err != nil {
		t.Fatalf("Probe returned error: %v", err)
	}

	ready := resp.GetReady()
	if ready == nil {
		t.Fatal("Probe response ready is nil, want non-nil BoolValue")
	}

	if !ready.GetValue() {
		t.Error("Probe ready = false, want true")
	}
}
