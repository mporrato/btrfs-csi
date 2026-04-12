#!/usr/bin/env bash
# minikube-up.sh — starts a minikube cluster with QEMU driver, formats two extra
# disks as btrfs on independent filesystems, and deploys the btrfs-csi-driver.
#
# Prerequisites: minikube, kubectl, qemu (no root or Docker required on the host).
#
# Usage:
#   bash scripts/minikube-up.sh
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
source "${SCRIPT_DIR}/common.sh"

echo "==> Starting minikube cluster '${CLUSTER}' (qemu, 2 extra disks)..."
${MK} start --driver=qemu --extra-disks=2

echo "==> Formatting ${EXTRA_DISK_1_DEV} as btrfs and mounting to ${BTRFS_MOUNT_1}..."
${EXEC} "sudo mkfs.btrfs -f ${EXTRA_DISK_1_DEV}"
${EXEC} "sudo mkdir -p ${BTRFS_MOUNT_1} && echo '${EXTRA_DISK_1_DEV} ${BTRFS_MOUNT_1} btrfs defaults 0 0' | sudo tee -a /etc/fstab && sudo mount ${BTRFS_MOUNT_1}"

echo "==> Formatting ${EXTRA_DISK_2_DEV} as btrfs and mounting to ${BTRFS_MOUNT_2}..."
${EXEC} "sudo mkfs.btrfs -f ${EXTRA_DISK_2_DEV}"
${EXEC} "sudo mkdir -p ${BTRFS_MOUNT_2} && echo '${EXTRA_DISK_2_DEV} ${BTRFS_MOUNT_2} btrfs defaults 0 0' | sudo tee -a /etc/fstab && sudo mount ${BTRFS_MOUNT_2}"

echo "==> Loading driver image into minikube..."
${RUNTIME} save "${IMAGE}" | ${MK} image load -

echo "==> Installing VolumeSnapshot CRDs and controller..."
${K} apply -k "${SCRIPT_DIR}/../deploy/overlays/snapshot/"

${K} wait --for condition=established --timeout=60s \
    crd/volumesnapshotclasses.snapshot.storage.k8s.io \
    crd/volumesnapshotcontents.snapshot.storage.k8s.io \
    crd/volumesnapshots.snapshot.storage.k8s.io

echo "==> Deploying btrfs-csi-driver (dev overlay)..."
${K} apply -k "${SCRIPT_DIR}/../deploy/overlays/dev/"

echo "==> Waiting for snapshot-controller to be ready..."
${K} rollout status deployment/snapshot-controller -n kube-system --timeout=120s

echo "==> Waiting for DaemonSet to be ready..."
${K} rollout status daemonset/btrfs-csi-driver -n btrfs-csi --timeout=120s

echo ""
echo "Cluster '${CLUSTER}' is ready."
echo "  To check the driver pods    : ${K} get pods -n btrfs-csi"
echo "  To SSH into the minikube VM : ${MK} ssh"
