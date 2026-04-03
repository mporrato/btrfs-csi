package main

import (
	"flag"
	"fmt"
	"os"

	"github.com/container-storage-interface/spec/lib/go/csi"
	"github.com/google/uuid"
	"google.golang.org/grpc"
	"k8s.io/klog/v2"
	"k8s.io/mount-utils"
)

func main() {
	var (
		endpoint = flag.String("endpoint", "unix:///csi/csi.sock", "CSI endpoint")
		nodeID   = flag.String("nodeid", "", "Node ID")
		rootPath = flag.String("root-path", "/var/lib/btrfs-csi", "Root path for btrfs-csi")
		version  = flag.Bool("version", false, "Print version and exit")
	)

	flag.Parse()

	if *version {
		fmt.Println("btrfs-csi-driver version 0.1.0")
		os.Exit(0)
	}

	if *nodeID == "" {
		*nodeID = uuid.New().String()
	}

	klog.Infof("Starting btrfs-csi-driver")
	klog.Infof("Endpoint: %s", *endpoint)
	klog.Infof("Node ID: %s", *nodeID)
	klog.Infof("Root path: %s", *rootPath)

	// These imports ensure the dependencies are kept in go.mod
	_ = csi.PluginCapability_Service_UNKNOWN
	_ = grpc.NewServer()
	_ = mount.New("")
}
