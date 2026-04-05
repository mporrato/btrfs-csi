package driver

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/container-storage-interface/spec/lib/go/csi"
	"github.com/guru/btrfs-csi/pkg/btrfs"
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

	foundController := false
	foundAccessibility := false
	for _, cap := range resp.GetCapabilities() {
		if svc := cap.GetService(); svc != nil {
			switch svc.GetType() {
			case csi.PluginCapability_Service_CONTROLLER_SERVICE:
				foundController = true
			case csi.PluginCapability_Service_VOLUME_ACCESSIBILITY_CONSTRAINTS:
				foundAccessibility = true
			}
		}
	}

	if !foundController {
		t.Error("GetPluginCapabilities: CONTROLLER_SERVICE capability not found")
	}

	if !foundAccessibility {
		t.Error("GetPluginCapabilities: VOLUME_ACCESSIBILITY_CONSTRAINTS capability not found")
	}
}

func TestProbeHealthy(t *testing.T) {
	// Create a temporary directory to serve as root path
	tmpDir := t.TempDir()

	d := newTestDriverWithPath(tmpDir)
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

func TestProbeUnhealthy(t *testing.T) {
	// Use a non-existent path
	nonExistent := filepath.Join(t.TempDir(), "does-not-exist")

	d := newTestDriverWithPath(nonExistent)
	resp, err := d.Probe(context.Background(), &csi.ProbeRequest{})
	if err != nil {
		t.Fatalf("Probe returned error: %v", err)
	}

	ready := resp.GetReady()
	if ready == nil {
		t.Fatal("Probe response ready is nil, want non-nil BoolValue")
	}

	if ready.GetValue() {
		t.Error("Probe ready = true, want false for non-existent root path")
	}
}

func TestNewDriverValidation(t *testing.T) {
	ms, _ := newTestMultiStore(testRootPath)

	t.Run("nil manager", func(t *testing.T) {
		defer func() {
			if r := recover(); r == nil {
				t.Error("NewDriver with nil manager should panic")
			}
		}()
		NewDriver(nil, ms, "node1")
	})

	t.Run("nil store", func(t *testing.T) {
		defer func() {
			if r := recover(); r == nil {
				t.Error("NewDriver with nil store should panic")
			}
		}()
		NewDriver(&btrfs.MockManager{}, nil, "node1")
	})
}

func TestNewDriverSetsFields(t *testing.T) {
	mgr := &btrfs.MockManager{}
	ms, _ := newTestMultiStore(testRootPath)
	nodeID := "test-node"

	d := NewDriver(mgr, ms, nodeID)

	if d.nodeID != nodeID {
		t.Errorf("nodeID = %q, want %q", d.nodeID, nodeID)
	}

	paths := d.basePaths()
	if len(paths) != 1 || paths[0] != testRootPath {
		t.Errorf("basePaths = %v, want [%q]", paths, testRootPath)
	}
}

func TestGetPluginInfoWithNodeID(t *testing.T) {
	d := newTestDriver()
	resp, err := d.GetPluginInfo(context.Background(), &csi.GetPluginInfoRequest{})
	if err != nil {
		t.Fatalf("GetPluginInfo returned error: %v", err)
	}

	if resp.GetName() != "btrfs.csi.local" {
		t.Errorf("GetPluginInfo name = %q, want %q", resp.GetName(), "btrfs.csi.local")
	}
}
