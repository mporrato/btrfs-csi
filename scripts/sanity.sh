#!/usr/bin/env bash
# sanity.sh — builds the CSI sanity integration test binary in a container,
# copies it into the minikube VM, and runs it there (root + btrfs provided by the VM).
#
# Prerequisites: minikube cluster running with --extra-disks=1.
#
# Usage:
#   bash scripts/sanity.sh
#   CLUSTER=btrfs-csi VERBOSE=1 bash scripts/sanity.sh
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
source "${SCRIPT_DIR}/common.sh"

BINARY="bin/btrfs-csi-sanity.test"
VERBOSE="${VERBOSE:-0}"

echo "==> Ensuring ${BTRFS_MOUNT_1} is mounted inside the VM..."
${MK} ssh -- \
    "mountpoint -q ${BTRFS_MOUNT_1} || sudo mount ${BTRFS_MOUNT_1}"

echo "==> Building sanity test binary (linux/amd64)..."
mkdir -p bin
eval ${RUNGO} go test -trimpath -c -tags integration ./pkg/driver/ -o "${BINARY}"

echo "==> Copying binary to minikube VM..."
${MK} cp "${BINARY}" /tmp/btrfs-csi-sanity.test

TEST_FLAGS="-test.timeout=10m"
[ "${VERBOSE}" = "1" ] && TEST_FLAGS="${TEST_FLAGS} -test.v"

echo "==> Running sanity tests inside the VM..."
${MK} ssh -- \
    "sudo chmod +x /tmp/btrfs-csi-sanity.test && sudo /tmp/btrfs-csi-sanity.test ${TEST_FLAGS}"
