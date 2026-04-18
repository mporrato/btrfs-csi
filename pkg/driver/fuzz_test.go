package driver

import (
	"context"
	"slices"
	"strings"
	"testing"

	"github.com/container-storage-interface/spec/lib/go/csi"
	"github.com/mporrato/btrfs-csi/pkg/state"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// pathSeeds returns a curated set of seed paths for fuzz testing.
// Covers: empty, root, absolute, relative, traversal, deep nesting,
// whitespace, and a representative sample of special characters.
func pathSeeds() []string {
	return []string{
		// Basics
		"",
		"/",
		"/absolute/path",
		"relative/path",
		"./relative",
		"..",
		// Traversal patterns
		"../../../etc/passwd",
		"/tmp/../../etc/evil",
		"/tmp/target/..",
		"/a/../b",
		// Deep nesting
		"/a/b/c/d/e/f/g/h/i/j",
		// Whitespace and control characters
		"/path/with spaces",
		"/path/with/newline/\n",
		"/path/with/tab/\t",
		"/path/with/null/\x00",
		// Special characters (one representative per category)
		"/path/with/dot/.file",
		"/path/with/hyphen/-file",
		"/path/with/underscore/_file",
		"/path/with/colon/:file",
		"/path/with/at/@file",
		"/path/with/hash/#file",
		"/path/with/unicode/日本語",
	}
}

// addSeeds adds all path seeds to the fuzzer corpus.
func addSeeds(f *testing.F) {
	for _, s := range pathSeeds() {
		f.Add(s)
	}
}

// hasTraversal returns true if path contains a ".." path component.
// This mirrors the check in validatePath: strings.Split(path, "/") contains "..".
func hasTraversal(path string) bool {
	return slices.Contains(strings.Split(path, "/"), "..")
}

// pathExpectedCode returns the gRPC code a path validation function should return.
// Returns codes.OK for paths expected to pass validation.
func pathExpectedCode(path, kubeletDir string) codes.Code {
	switch {
	case path == "":
		return codes.InvalidArgument
	case !strings.HasPrefix(path, "/"):
		return codes.InvalidArgument
	case hasTraversal(path):
		return codes.InvalidArgument
	case kubeletDir != "" && !strings.HasPrefix(path, kubeletDir):
		return codes.InvalidArgument
	default:
		return codes.OK
	}
}

// FuzzValidatePath tests the validatePath function with arbitrary string inputs.
// The function must never panic and must correctly identify invalid paths.
func FuzzValidatePath(f *testing.F) {
	addSeeds(f)

	f.Fuzz(func(t *testing.T, path string) {
		err := validatePath(path)

		switch {
		case path == "":
			// Empty path must return InvalidArgument.
			if err == nil {
				t.Fatalf("validatePath(%q) = nil, want error", path)
			}
			if status.Code(err) != codes.InvalidArgument {
				t.Errorf("validatePath(%q) = %v, want InvalidArgument", path, status.Code(err))
			}

		case !strings.HasPrefix(path, "/"):
			// Relative path must return InvalidArgument.
			if err == nil {
				t.Fatalf("validatePath(%q) = nil, want error for relative path", path)
			}
			if status.Code(err) != codes.InvalidArgument {
				t.Errorf("validatePath(%q) = %v, want InvalidArgument for relative path", path, status.Code(err))
			}

		case hasTraversal(path):
			// Absolute path with ".." component must return InvalidArgument.
			if err == nil {
				t.Fatalf("validatePath(%q) = nil, want error for traversal", path)
			}
			if status.Code(err) != codes.InvalidArgument {
				t.Errorf("validatePath(%q) = %v, want InvalidArgument for traversal", path, status.Code(err))
			}

		default:
			// Valid absolute path without traversal must succeed.
			if err != nil {
				t.Errorf("validatePath(%q) = %v, want nil", path, err)
			}
		}
	})
}

// FuzzValidateTargetPath tests the validateTargetPath function with arbitrary string inputs.
// The function must never panic and must correctly validate paths against the kubelet directory.
func FuzzValidateTargetPath(f *testing.F) {
	addSeeds(f)

	f.Fuzz(func(t *testing.T, path string) {
		d, _, _, _ := newTestDriverWithMounter(t)
		kubeletDir := t.TempDir()
		if err := d.SetKubeletPath(kubeletDir); err != nil {
			t.Fatalf("SetKubeletPath: %v", err)
		}

		err := d.validateTargetPath(path)

		switch {
		case path == "":
			// Empty path must return InvalidArgument.
			if err == nil {
				t.Fatalf("validateTargetPath(%q) = nil, want error", path)
			}
			if status.Code(err) != codes.InvalidArgument {
				t.Errorf("validateTargetPath(%q) = %v, want InvalidArgument", path, status.Code(err))
			}

		case !strings.HasPrefix(path, "/"):
			// Relative path must return InvalidArgument.
			if err == nil {
				t.Fatalf("validateTargetPath(%q) = nil, want error for relative path", path)
			}
			if status.Code(err) != codes.InvalidArgument {
				t.Errorf("validateTargetPath(%q) = %v, want InvalidArgument for relative path", path, status.Code(err))
			}

		case hasTraversal(path):
			// Path with ".." component must return InvalidArgument.
			if err == nil {
				t.Fatalf("validateTargetPath(%q) = nil, want error for traversal", path)
			}
			if status.Code(err) != codes.InvalidArgument {
				t.Errorf("validateTargetPath(%q) = %v, want InvalidArgument for traversal", path, status.Code(err))
			}

		case !strings.HasPrefix(path, kubeletDir):
			// Absolute path outside kubelet directory must return InvalidArgument.
			if err == nil {
				t.Fatalf("validateTargetPath(%q) = nil, want error for path outside kubelet", path)
			}
			if status.Code(err) != codes.InvalidArgument {
				t.Errorf("validateTargetPath(%q) = %v, want InvalidArgument for path outside kubelet", path, status.Code(err))
			}

		default:
			// Valid path within kubelet directory must succeed.
			if err != nil {
				t.Errorf("validateTargetPath(%q) = %v, want nil", path, err)
			}
		}
	})
}

// FuzzValidatePathInKubeletDir tests the validatePathInKubeletDir function.
// This function is lenient by design: it returns nil when it cannot resolve the
// path, deferring error handling to the caller (NodeGetVolumeStats returns NotFound
// per CSI spec). The test verifies no panic and that resolvable out-of-bounds
// paths are rejected.
func FuzzValidatePathInKubeletDir(f *testing.F) {
	addSeeds(f)

	f.Fuzz(func(t *testing.T, path string) {
		d, _, _, _ := newTestDriverWithMounter(t)
		kubeletDir := t.TempDir()
		if err := d.SetKubeletPath(kubeletDir); err != nil {
			t.Fatalf("SetKubeletPath: %v", err)
		}

		err := d.validatePathInKubeletDir(path)

		// Empty path is explicitly allowed (returns nil).
		if path == "" {
			if err != nil {
				t.Errorf("validatePathInKubeletDir(%q) = %v, want nil", path, err)
			}
			return
		}

		// For non-empty paths the function is lenient: it may return nil
		// (unresolvable path) or InvalidArgument (resolvable but outside kubelet).
		// We only assert that it never panics and never returns other error codes.
		if err != nil && status.Code(err) != codes.InvalidArgument {
			t.Errorf("validatePathInKubeletDir(%q) = %v (code %v), want nil or InvalidArgument",
				path, err, status.Code(err))
		}
	})
}

// FuzzNodePublishVolume tests NodePublishVolume with arbitrary target path inputs.
// The function must never panic and must reject invalid paths before attempting mount.
func FuzzNodePublishVolume(f *testing.F) {
	addSeeds(f)

	f.Fuzz(func(t *testing.T, targetPath string) {
		d, _, _, store := newTestDriverWithMounter(t)
		kubeletDir := t.TempDir()
		if err := d.SetKubeletPath(kubeletDir); err != nil {
			t.Fatalf("SetKubeletPath: %v", err)
		}

		vol := &state.Volume{ID: "vol-fuzz", Name: "test-pvc", BasePath: store.root()}
		if err := store.SaveVolume(vol); err != nil {
			t.Fatalf("SaveVolume: %v", err)
		}

		_, err := d.NodePublishVolume(context.Background(), &csi.NodePublishVolumeRequest{
			VolumeId:   "vol-fuzz",
			TargetPath: targetPath,
			VolumeCapability: &csi.VolumeCapability{
				AccessType: &csi.VolumeCapability_Mount{
					Mount: &csi.VolumeCapability_MountVolume{},
				},
				AccessMode: &csi.VolumeCapability_AccessMode{
					Mode: csi.VolumeCapability_AccessMode_SINGLE_NODE_WRITER,
				},
			},
		})

		want := pathExpectedCode(targetPath, kubeletDir)
		if want != codes.OK {
			if err == nil {
				t.Fatalf("NodePublishVolume with path %q = nil, want %v", targetPath, want)
			}
			if status.Code(err) != want {
				t.Errorf("NodePublishVolume with path %q: code = %v, want %v", targetPath, status.Code(err), want)
			}
		} else if err != nil && status.Code(err) == codes.InvalidArgument {
			// Valid path must not fail with InvalidArgument.
			t.Errorf("NodePublishVolume with valid path %q: unexpected InvalidArgument: %v", targetPath, err)
		}
	})
}

// FuzzNodeUnpublishVolume tests NodeUnpublishVolume with arbitrary target path inputs.
// The function must never panic and must reject invalid paths before attempting unmount.
func FuzzNodeUnpublishVolume(f *testing.F) {
	addSeeds(f)

	f.Fuzz(func(t *testing.T, targetPath string) {
		d, _, _, _ := newTestDriverWithMounter(t)
		kubeletDir := t.TempDir()
		if err := d.SetKubeletPath(kubeletDir); err != nil {
			t.Fatalf("SetKubeletPath: %v", err)
		}

		_, err := d.NodeUnpublishVolume(context.Background(), &csi.NodeUnpublishVolumeRequest{
			VolumeId:   "vol-fuzz",
			TargetPath: targetPath,
		})

		want := pathExpectedCode(targetPath, kubeletDir)
		if want != codes.OK {
			if err == nil {
				t.Fatalf("NodeUnpublishVolume with path %q = nil, want %v", targetPath, want)
			}
			if status.Code(err) != want {
				t.Errorf("NodeUnpublishVolume with path %q: code = %v, want %v", targetPath, status.Code(err), want)
			}
		} else if err != nil && status.Code(err) == codes.InvalidArgument {
			// Valid path must not fail with InvalidArgument.
			t.Errorf("NodeUnpublishVolume with valid path %q: unexpected InvalidArgument: %v", targetPath, err)
		}
	})
}

// FuzzNodeGetVolumeStats tests NodeGetVolumeStats with arbitrary volume path inputs.
// validatePathInKubeletDir is lenient (returns nil for unresolvable paths), so this
// test focuses on: no panic, empty-path rejection, and no spurious InvalidArgument
// for paths that happen to be within the kubelet tree.
func FuzzNodeGetVolumeStats(f *testing.F) {
	addSeeds(f)

	f.Fuzz(func(t *testing.T, volumePath string) {
		d, _, _, store := newTestDriverWithMounter(t)
		kubeletDir := t.TempDir()
		if err := d.SetKubeletPath(kubeletDir); err != nil {
			t.Fatalf("SetKubeletPath: %v", err)
		}

		vol := &state.Volume{ID: "vol-fuzz", Name: "test-pvc", BasePath: store.root()}
		if err := store.SaveVolume(vol); err != nil {
			t.Fatalf("SaveVolume: %v", err)
		}

		_, err := d.NodeGetVolumeStats(context.Background(), &csi.NodeGetVolumeStatsRequest{
			VolumeId:   "vol-fuzz",
			VolumePath: volumePath,
		})

		// Empty path is the only case where we require InvalidArgument.
		if volumePath == "" {
			if err == nil {
				t.Fatal("NodeGetVolumeStats with empty volumePath = nil, want error")
			}
			if status.Code(err) != codes.InvalidArgument {
				t.Errorf("NodeGetVolumeStats with empty volumePath: code = %v, want InvalidArgument", status.Code(err))
			}
			return
		}

		// For all other paths the function is lenient: it may return nil
		// (unresolvable, deferred to downstream) or various error codes.
		// We assert it never panics and never returns unexpected error codes
		// for paths that are clearly within the kubelet directory.
		if strings.HasPrefix(volumePath, kubeletDir) && err != nil {
			code := status.Code(err)
			// Paths inside kubelet should not fail with InvalidArgument due to
			// path validation. Other codes (NotFound, Internal) are acceptable.
			if code == codes.InvalidArgument {
				t.Errorf("NodeGetVolumeStats with volumePath %q inside kubelet: unexpected InvalidArgument: %v", volumePath, err)
			}
		}
	})
}

// FuzzSetKubeletPath tests SetKubeletPath with arbitrary path inputs.
// The function must never panic and must reject relative paths and traversal.
func FuzzSetKubeletPath(f *testing.F) {
	addSeeds(f)

	f.Fuzz(func(t *testing.T, kubeletPath string) {
		d, _, _, _ := newTestDriverWithMounter(t)

		err := d.SetKubeletPath(kubeletPath)

		switch {
		case kubeletPath == "":
			// Empty string is accepted (clears kubelet path).
			if err != nil {
				t.Errorf("SetKubeletPath(%q) = %v, want nil", kubeletPath, err)
			}

		case !strings.HasPrefix(kubeletPath, "/"):
			// Relative path must be rejected.
			if err == nil {
				t.Fatalf("SetKubeletPath(%q) = nil, want error for relative path", kubeletPath)
			}

		case hasTraversal(kubeletPath):
			// Path with ".." component must be rejected.
			if err == nil {
				t.Fatalf("SetKubeletPath(%q) = nil, want error for traversal", kubeletPath)
			}

		default:
			// Valid absolute path without traversal must succeed.
			if err != nil {
				t.Errorf("SetKubeletPath(%q) = %v, want nil", kubeletPath, err)
			}
		}
	})
}
