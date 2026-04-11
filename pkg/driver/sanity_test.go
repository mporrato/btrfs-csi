//go:build integration

package driver

import (
	"os"
	"os/exec"
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
	sanityDriver  *Driver
	sanityErrCh   chan error
	sanityRoot    string
	sanityLoopImg string
	sanityConfig  = sanity.NewTestConfig()
)

func TestSanity(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "CSI Sanity Suite")
}

var _ = BeforeSuite(func() {
	// Create a temporary loopback image and format it as btrfs.
	f, err := os.CreateTemp("", "btrfs-sanity-*.img")
	Expect(err).NotTo(HaveOccurred())
	sanityLoopImg = f.Name()
	Expect(f.Close()).To(Succeed())
	Expect(os.Truncate(sanityLoopImg, 256*1024*1024)).To(Succeed()) // 256 MiB
	Expect(exec.Command("mkfs.btrfs", sanityLoopImg).Run()).To(Succeed())

	sanityRoot, err = os.MkdirTemp("", "btrfs-sanity-mount-*")
	Expect(err).NotTo(HaveOccurred())
	Expect(exec.Command("mount", "-o", "loop", sanityLoopImg, sanityRoot).Run()).To(Succeed())

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
		_ = exec.Command("umount", sanityRoot).Run()
		_ = os.Remove(sanityRoot)
	}
	if sanityLoopImg != "" {
		_ = os.Remove(sanityLoopImg)
	}
})

var _ = Describe("btrfs-csi-driver", func() {
	sanity.GinkgoTest(&sanityConfig)
})
