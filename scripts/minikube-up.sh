#!/usr/bin/env bash
# minikube-up.sh — starts a minikube cluster with QEMU driver, formats two extra
# disks as btrfs on independent filesystems, and deploys the btrfs-csi-driver.
#
# Prerequisites: minikube, kubectl, qemu (no root or Docker required on the host).
#
# Usage:
#   bash scripts/minikube-up.sh
set -euo pipefail

IMAGE="localhost/btrfs-csi-driver:latest"
CLUSTER="${CLUSTER:-btrfs-csi}"
EXTRA_DISK_1_DEV="${EXTRA_DISK_1_DEV:-/dev/vda}"
EXTRA_DISK_2_DEV="${EXTRA_DISK_2_DEV:-/dev/vdb}"
BTRFS_MOUNT_1="${BTRFS_MOUNT_1:-/var/lib/btrfs-csi}"
BTRFS_MOUNT_2="${BTRFS_MOUNT_2:-/var/lib/btrfs-csi-extra}"
SNAPSHOTTER_VERSION="${SNAPSHOTTER_VERSION:-v8.0.0}"

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
RUNTIME=$(command -v podman 2>/dev/null || command -v docker 2>/dev/null)
MK="minikube --profile=${CLUSTER}"
K="kubectl --context=${CLUSTER}"
EXEC="${MK} ssh --"

echo "==> Starting minikube cluster '${CLUSTER}' (qemu, 2 extra disks)..."
${MK} start --driver=qemu --extra-disks=2

echo "==> Formatting ${EXTRA_DISK_1_DEV} as btrfs and mounting to ${BTRFS_MOUNT_1}..."
${EXEC} "sudo mkfs.btrfs -f ${EXTRA_DISK_1_DEV}"
${EXEC} "sudo mkdir -p ${BTRFS_MOUNT_1} && echo '${EXTRA_DISK_1_DEV} ${BTRFS_MOUNT_1} btrfs defaults 0 0' | sudo tee -a /etc/fstab && sudo mount ${BTRFS_MOUNT_1}"

echo "==> Formatting ${EXTRA_DISK_2_DEV} as btrfs and mounting to ${BTRFS_MOUNT_2}..."
${EXEC} "sudo mkfs.btrfs -f ${EXTRA_DISK_2_DEV}"
${EXEC} "sudo mkdir -p ${BTRFS_MOUNT_2} && echo '${EXTRA_DISK_2_DEV} ${BTRFS_MOUNT_2} btrfs defaults 0 0' | sudo tee -a /etc/fstab && sudo mount ${BTRFS_MOUNT_2}"

echo "==> Installing VolumeSnapshot CRDs and controller (${SNAPSHOTTER_VERSION})..."
BASE="https://raw.githubusercontent.com/kubernetes-csi/external-snapshotter/${SNAPSHOTTER_VERSION}"
${K} apply -f "${BASE}/client/config/crd/snapshot.storage.k8s.io_volumesnapshotclasses.yaml"
${K} apply -f "${BASE}/client/config/crd/snapshot.storage.k8s.io_volumesnapshotcontents.yaml"
${K} apply -f "${BASE}/client/config/crd/snapshot.storage.k8s.io_volumesnapshots.yaml"
${K} apply -f "${BASE}/deploy/kubernetes/snapshot-controller/rbac-snapshot-controller.yaml"
${K} apply -f "${BASE}/deploy/kubernetes/snapshot-controller/setup-snapshot-controller.yaml"
${K} rollout status deployment/snapshot-controller -n kube-system --timeout=120s

echo "==> Loading driver image into minikube..."
${RUNTIME} save "${IMAGE}" | ${MK} image load -

echo "==> Deploying btrfs-csi-driver (dev overlay)..."
${K} apply -k "${SCRIPT_DIR}/../deploy/overlays/dev/"

echo "==> Waiting for DaemonSet to be ready..."
${K} rollout status daemonset/btrfs-csi-driver -n btrfs-csi --timeout=120s

echo ""
echo "Cluster '${CLUSTER}' is ready."
echo "  kubectl --context=${CLUSTER} get pods -n btrfs-csi"
echo "  minikube ssh --profile=${CLUSTER}"
