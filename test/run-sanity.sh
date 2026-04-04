#!/usr/bin/env bash
# run-sanity.sh — builds the CSI sanity integration test binary on the host, copies
# it into the minikube VM, and runs it there (root + btrfs provided by the VM).
#
# Prerequisites: minikube cluster running with --extra-disks=1, Go toolchain on host.
#
# Usage:
#   bash test/run-sanity.sh
#   CLUSTER=btrfs-csi VERBOSE=1 bash test/run-sanity.sh
set -euo pipefail

CLUSTER="${CLUSTER:-btrfs-csi}"
EXTRA_DISK="${EXTRA_DISK:-/dev/vda}"
BTRFS_MOUNT="${BTRFS_MOUNT:-/var/lib/btrfs-csi}"
BINARY="/tmp/btrfs-csi-sanity.test"
VERBOSE="${VERBOSE:-0}"

echo "==> Ensuring ${BTRFS_MOUNT} is mounted inside the VM..."
minikube ssh --profile="${CLUSTER}" -- \
    "mountpoint -q ${BTRFS_MOUNT} || sudo mount ${BTRFS_MOUNT}"

echo "==> Building sanity test binary (linux/amd64)..."
GOOS=linux GOARCH=amd64 go test \
    -c -tags integration \
    ./pkg/driver/ \
    -o "${BINARY}"

echo "==> Copying binary to minikube VM..."
minikube cp "${BINARY}" /tmp/btrfs-csi-sanity.test --profile="${CLUSTER}"

TEST_FLAGS="-test.timeout=10m"
[ "${VERBOSE}" = "1" ] && TEST_FLAGS="${TEST_FLAGS} -test.v"

echo "==> Running sanity tests inside the VM..."
minikube ssh --profile="${CLUSTER}" -- \
    "sudo chmod +x /tmp/btrfs-csi-sanity.test && sudo /tmp/btrfs-csi-sanity.test ${TEST_FLAGS}"
