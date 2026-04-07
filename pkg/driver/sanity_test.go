//go:build integration

package driver

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/kubernetes-csi/csi-test/v5/pkg/sanity"
	"github.com/mporrato/btrfs-csi/pkg/btrfs"
	"github.com/mporrato/btrfs-csi/pkg/state"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var (
	sanityDriver *Driver
	sanityErrCh  chan error
	sanityRoot   string
	sanityConfig = sanity.NewTestConfig()
)

func TestSanity(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "CSI Sanity Suite")
}

// btrfsBase returns the btrfs mount point to use for tests.
// Defaults to /var/lib/btrfs-csi (the extra disk mounted by setup-minikube.sh),
// overridable via BTRFS_ROOT env var.
func btrfsBase() string {
	if v := os.Getenv("BTRFS_ROOT"); v != "" {
		return v
	}
	return "/var/lib/btrfs-csi"
}

var _ = BeforeSuite(func() {
	var err error
	// Create a temp directory on the existing btrfs filesystem.
	sanityRoot, err = os.MkdirTemp(btrfsBase(), "sanity-*")
	Expect(err).NotTo(HaveOccurred())

	sockPath := filepath.Join(sanityRoot, "csi.sock")

	store, err := state.NewFileStore(filepath.Join(sanityRoot, "state.json"))
	Expect(err).NotTo(HaveOccurred())

	sanityDriver, err = NewDriver(&btrfs.RealManager{}, store, "sanity-node")
	Expect(err).NotTo(HaveOccurred())
	sanityDriver.SetPools(map[string]string{"default": sanityRoot})
	sanityErrCh = make(chan error, 1)
	go func() {
		sanityErrCh <- sanityDriver.Run("unix://" + sockPath)
	}()

	Eventually(func() bool {
		_, err := os.Stat(sockPath)
		return err == nil
	}, 5*time.Second, 100*time.Millisecond).Should(BeTrue(), "driver socket did not appear")

	sanityConfig.Address = "unix://" + sockPath
	sanityConfig.TargetPath = filepath.Join(sanityRoot, "mount")
	sanityConfig.StagingPath = filepath.Join(sanityRoot, "staging")
	sanityConfig.TestVolumeSize = 256 * 1024 * 1024       // 256 MiB
	sanityConfig.TestVolumeExpandSize = 512 * 1024 * 1024 // 512 MiB
})

var _ = AfterSuite(func() {
	if sanityDriver != nil {
		sanityDriver.Stop()
		select {
		case <-sanityErrCh:
		case <-time.After(5 * time.Second):
		}
	}
	if sanityRoot != "" {
		_ = os.RemoveAll(sanityRoot)
	}
})

var _ = Describe("btrfs-csi-driver", func() {
	sanity.GinkgoTest(&sanityConfig)
})
