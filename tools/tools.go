//go:build tools

// Package tools imports development-only dependencies to keep them in go.mod.
package tools

import (
	_ "github.com/kubernetes-csi/csi-test/v5/pkg/sanity"
)
