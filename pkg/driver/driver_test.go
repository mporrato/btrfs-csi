package driver

import (
	"context"
	"net"
	"path/filepath"
	"testing"
	"time"

	"github.com/container-storage-interface/spec/lib/go/csi"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
)

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
		// Try to connect
		ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
		var dialErr error
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
	defer conn.Close()

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
