package driver

import (
	"context"
	"net"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/container-storage-interface/spec/lib/go/csi"
	"github.com/mporrato/btrfs-csi/pkg/state"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
)

func TestNewDriver_ClearsStaleQgroupsOnStartup(t *testing.T) {
	_, mock, _ := newTestDriverWithMock()
	if len(mock.ClearStaleQgroupsCalls) != 0 {
		t.Fatalf("expected no ClearStaleQgroups call synchronously on startup, got %d", len(mock.ClearStaleQgroupsCalls))
	}
}

func TestScheduleStartupQgroupCleanups_Staggered(t *testing.T) {
	// Use sorted paths so stagger order is deterministic.
	pathA := "/mnt/pool-a"
	pathB := "/mnt/pool-b"

	type stampedCall struct {
		path string
		at   time.Time
	}
	var mu sync.Mutex
	var callLog []stampedCall

	mgr := &funcManager{
		clearStaleQgroups: func(path string) error {
			mu.Lock()
			callLog = append(callLog, stampedCall{path: path, at: time.Now()})
			mu.Unlock()
			return nil
		},
	}

	ms := state.NewMultiStore()
	ms.AddStoreForTest(pathA, newMemStore(pathA))
	ms.AddStoreForTest(pathB, newMemStore(pathB))

	d := NewDriver(mgr, ms, "test-node")
	d.SetPools(map[string]string{"a": pathA, "b": pathB})

	const base, stagger = 20 * time.Millisecond, 30 * time.Millisecond
	d.scheduleStartupQgroupCleanups(base, stagger)

	// After base+stagger/2: only pathA (index 0) should have fired.
	time.Sleep(base + stagger/2)
	mu.Lock()
	midCount := len(callLog)
	var firstPath string
	if midCount > 0 {
		firstPath = callLog[0].path
	}
	mu.Unlock()
	if midCount != 1 {
		t.Fatalf("after first stagger window: want 1 cleanup, got %d", midCount)
	}
	if firstPath != pathA {
		t.Errorf("first cleanup path = %q, want %q", firstPath, pathA)
	}

	// Wait for pathB (index 1) to also fire.
	time.Sleep(stagger + 10*time.Millisecond)
	mu.Lock()
	finalLog := append([]stampedCall(nil), callLog...)
	mu.Unlock()
	if len(finalLog) != 2 {
		t.Fatalf("after all stagger windows: want 2 cleanups, got %d", len(finalLog))
	}
	if finalLog[1].path != pathB {
		t.Errorf("second cleanup path = %q, want %q", finalLog[1].path, pathB)
	}
	if gap := finalLog[1].at.Sub(finalLog[0].at); gap < stagger/2 {
		t.Errorf("cleanup gap = %v, want >= %v", gap, stagger/2)
	}
}

func TestScheduleQgroupCleanup_OnlyTargetsSpecifiedPath(t *testing.T) {
	pathA := "/mnt/pool-a"
	pathB := "/mnt/pool-b"

	var mu sync.Mutex
	var calls []string

	mgr := &funcManager{
		clearStaleQgroups: func(path string) error {
			mu.Lock()
			calls = append(calls, path)
			mu.Unlock()
			return nil
		},
	}

	ms := state.NewMultiStore()
	memA := newMemStore(pathA)
	memB := newMemStore(pathB)
	ms.AddStoreForTest(pathA, memA)
	ms.AddStoreForTest(pathB, memB)

	d := NewDriver(mgr, ms, "test-node")
	d.SetPools(map[string]string{"a": pathA, "b": pathB})

	// Schedule cleanup only for pathA with a very short delay.
	d.scheduleQgroupCleanup(pathA, 10*time.Millisecond)

	// Wait for the timer to fire.
	time.Sleep(50 * time.Millisecond)

	// Only pathA should have been cleaned.
	mu.Lock()
	callCount := len(calls)
	var firstCall string
	if callCount > 0 {
		firstCall = calls[0]
	}
	mu.Unlock()
	if callCount != 1 {
		t.Fatalf("expected 1 ClearStaleQgroups call, got %d", callCount)
	}
	if firstCall != pathA {
		t.Errorf("ClearStaleQgroups called with %q, want %q", firstCall, pathA)
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
