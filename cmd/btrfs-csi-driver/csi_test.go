package main

import (
	"testing"

	// Import csi-test sanity for dependency management.
	_ "github.com/kubernetes-csi/csi-test/v5/pkg/sanity"
)

func TestCSITestDependency(t *testing.T) {
	// This test ensures csi-test is in go.mod
	t.Log("csi-test dependency is available")
}
