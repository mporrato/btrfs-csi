package driver

import (
	"context"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/container-storage-interface/spec/lib/go/csi"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
)

func TestNewDriver_ClearsStaleQgroupsOnStartup(t *testing.T) {
	_, mock, _ := newTestDriverWithMock()
	if len(mock.ClearStaleQgroupsCalls) != 1 {
		t.Fatalf("expected 1 ClearStaleQgroups call on startup, got %d", len(mock.ClearStaleQgroupsCalls))
	}
	if mock.ClearStaleQgroupsCalls[0] != "/tmp/btrfs-csi-test" {
		t.Errorf("ClearStaleQgroups mountpoint = %q, want /tmp/btrfs-csi-test", mock.ClearStaleQgroupsCalls[0])
	}
}

func TestParseEndpoint(t *testing.T) {
	tests := []struct {
		name     string
		endpoint string
		wantPath string
		wantErr  bool
	}{
		{
			name:     "triple-slash absolute path",
			endpoint: "unix:///csi/csi.sock",
			wantPath: "/csi/csi.sock",
		},
		{
			name:     "double-slash relative path",
			endpoint: "unix://relative/path.sock",
			wantPath: "relative/path.sock",
		},
		{
			name:     "single-slash is not supported",
			endpoint: "unix:/csi/csi.sock",
			wantErr:  true,
		},
		{
			name:     "empty path after scheme",
			endpoint: "unix://",
			wantErr:  true,
		},
		{
			name:     "tcp scheme rejected",
			endpoint: "tcp://localhost:9000",
			wantErr:  true,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, err := parseEndpoint(tc.endpoint)
			if tc.wantErr {
				if err == nil {
					t.Errorf("parseEndpoint(%q) = %q, nil; want error", tc.endpoint, got)
				}
				return
			}
			if err != nil {
				t.Fatalf("parseEndpoint(%q) unexpected error: %v", tc.endpoint, err)
			}
			if got != tc.wantPath {
				t.Errorf("parseEndpoint(%q) = %q, want %q", tc.endpoint, got, tc.wantPath)
			}
		})
	}
}

func TestDriverStartsAndStops(t *testing.T) {
	// Arrange: create a driver with a temp Unix socket path
	tmpDir := t.TempDir()
	sockPath := filepath.Join(tmpDir, "csi.sock")
	endpoint := "unix://" + sockPath

	d := newTestDriverWithPath(tmpDir)

	// Start the driver in a goroutine
	errCh := make(chan error, 1)
	go func() {
		errCh <- d.Run(endpoint)
	}()

	// Wait for the socket to appear (driver is listening)
	deadline := time.Now().Add(5 * time.Second)
	var conn *grpc.ClientConn
	for {
		if time.Now().After(deadline) {
			t.Fatal("timed out waiting for driver to start listening")
		}
		// Try to connect with blocking to ensure connection is established
		ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
		var dialErr error
		//nolint:staticcheck // SA1019: using deprecated API for test compatibility
		conn, dialErr = grpc.DialContext(ctx, "unix:"+sockPath,
			grpc.WithTransportCredentials(insecure.NewCredentials()),
			grpc.WithBlock(),
		)
		cancel()
		if dialErr == nil {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	defer func() {
		if conn != nil {
			_ = conn.Close()
		}
	}()

	// Act: call Probe via gRPC
	client := csi.NewIdentityClient(conn)
	ctx := context.Background()
	resp, err := client.Probe(ctx, &csi.ProbeRequest{})
	if err != nil {
		t.Fatalf("Probe RPC failed: %v", err)
	}

	// Assert: driver is ready
	ready := resp.GetReady()
	if ready == nil {
		t.Fatal("Probe response ready is nil, want non-nil BoolValue")
	}
	if !ready.GetValue() {
		t.Error("Probe ready = false, want true")
	}

	// Stop the driver
	d.Stop()

	// Assert clean shutdown: Run() should return without error
	select {
	case err := <-errCh:
		if err != nil {
			t.Errorf("Run() returned error after Stop(): %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("timed out waiting for Run() to return after Stop()")
	}

	// Assert the socket is closed (new connections should fail)
	_, dialErr := net.DialTimeout("unix", sockPath, 500*time.Millisecond)
	if dialErr == nil {
		t.Error("expected connection to fail after Stop(), but it succeeded")
	}
}

func TestRunRejectsSymlinkAtSocketPath(t *testing.T) {
	tmpDir := t.TempDir()
	sockPath := filepath.Join(tmpDir, "csi.sock")
	endpoint := "unix://" + sockPath

	// Create a symlink at the socket path (pointing to an arbitrary target)
	target := filepath.Join(tmpDir, "target")
	if err := os.WriteFile(target, []byte("data"), 0o600); err != nil {
		t.Fatalf("create target file: %v", err)
	}
	if err := os.Symlink(target, sockPath); err != nil {
		t.Fatalf("create symlink: %v", err)
	}

	d := newTestDriverWithPath(tmpDir)
	err := d.Run(endpoint)
	if err == nil {
		t.Fatal("Run() returned nil, want error for symlink at socket path")
	}
	if !strings.Contains(err.Error(), "symlink") {
		t.Errorf("Run() error = %q, want it to mention \"symlink\"", err.Error())
	}
}

func TestRunRejectsNonSocketAtSocketPath(t *testing.T) {
	tmpDir := t.TempDir()
	sockPath := filepath.Join(tmpDir, "csi.sock")
	endpoint := "unix://" + sockPath

	// Create a regular file at the socket path
	if err := os.WriteFile(sockPath, []byte("not a socket"), 0o600); err != nil {
		t.Fatalf("create regular file: %v", err)
	}

	d := newTestDriverWithPath(tmpDir)
	err := d.Run(endpoint)
	if err == nil {
		t.Fatal("Run() returned nil, want error for regular file at socket path")
	}
	// The error message should mention it is not a socket
	if !strings.Contains(err.Error(), "not a socket") {
		t.Errorf("Run() error = %q, want it to mention \"not a socket\"", err.Error())
	}
}

func TestRunRemovesStaleSocket(t *testing.T) {
	tmpDir := t.TempDir()
	sockPath := filepath.Join(tmpDir, "csi.sock")
	endpoint := "unix://" + sockPath

	// Create a stale socket: bind a listener then close it (leaves the file)
	stale, err := net.Listen("unix", sockPath)
	if err != nil {
		t.Fatalf("create stale socket: %v", err)
	}
	_ = stale.Close() // Socket file remains on disk

	d := newTestDriverWithPath(tmpDir)

	errCh := make(chan error, 1)
	go func() {
		errCh <- d.Run(endpoint)
	}()

	// Wait for the driver to start listening (proper readiness probe)
	deadline := time.Now().Add(5 * time.Second)
	var conn *grpc.ClientConn
	for {
		if time.Now().After(deadline) {
			t.Fatal("timed out waiting for driver to start after stale socket removal")
		}
		ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
		var dialErr error
		//nolint:staticcheck // SA1019: using deprecated API for test compatibility
		conn, dialErr = grpc.DialContext(ctx, "unix:"+sockPath,
			grpc.WithTransportCredentials(insecure.NewCredentials()),
			grpc.WithBlock(),
		)
		cancel()
		if dialErr == nil {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	defer func() {
		if conn != nil {
			_ = conn.Close()
		}
	}()

	// Stop cleanly
	d.Stop()

	select {
	case err := <-errCh:
		if err != nil {
			t.Errorf("Run() returned error after Stop(): %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("timed out waiting for Run() to return after Stop()")
	}
}
