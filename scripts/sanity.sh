#!/usr/bin/env bash
# sanity.sh — builds the CSI sanity integration test binary, copies it into
# the minikube VM, and runs it there. The test creates its own loopback btrfs
# filesystem and cleans it up on exit; no extra disk mounts are required.
#
# Prerequisites: minikube cluster running (no --extra-disks needed).
#
# Usage:
#   bash scripts/sanity.sh
#   CLUSTER=btrfs-csi VERBOSE=1 bash scripts/sanity.sh
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
source "${SCRIPT_DIR}/common.sh"

BINARY="bin/btrfs-csi-sanity.test"
VERBOSE="${VERBOSE:-0}"

cleanup() {
    echo "==> Cleaning up..."
    ${EXEC} "sudo rm -f /tmp/btrfs-csi-sanity.test" || true
}
trap cleanup EXIT

echo "==> Building sanity test binary (linux/amd64)..."
mkdir -p bin
GOTOOLCHAIN=auto go test -trimpath -c -tags integration ./pkg/driver/ -o "${BINARY}"

echo "==> Copying binary to minikube VM..."
${MK} cp "${BINARY}" /tmp/btrfs-csi-sanity.test

TEST_FLAGS="-test.timeout=10m"
[ "${VERBOSE}" = "1" ] && TEST_FLAGS="${TEST_FLAGS} -test.v"

echo "==> Running sanity tests inside the VM..."
${EXEC} "sudo chmod +x /tmp/btrfs-csi-sanity.test && sudo /tmp/btrfs-csi-sanity.test ${TEST_FLAGS}"
