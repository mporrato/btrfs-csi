#!/usr/bin/env bash
# CI-only: set up two loop-backed btrfs filesystems inside an already-running
# minikube cluster, then build and deploy the btrfs-csi driver.
#
# Expects minikube to be started externally (e.g., via medyagh/setup-minikube).
#
# Usage (from repo root):
#   bash scripts/ci/setup.sh
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
source "${SCRIPT_DIR}/../common.sh"

echo "==> Loading btrfs kernel module on host..."
sudo modprobe btrfs

echo "==> Installing btrfs-progs in minikube node..."
${EXEC} "sudo apt-get update -qq && sudo apt-get install -y -qq btrfs-progs"

echo "==> Creating loop-backed btrfs filesystems..."
${EXEC} "sudo bash -c '
  truncate -s 512M /tmp/btrfs1.img
  truncate -s 512M /tmp/btrfs2.img
  LOOP1=\$(losetup -f)
  losetup \"\$LOOP1\" /tmp/btrfs1.img
  LOOP2=\$(losetup -f)
  losetup \"\$LOOP2\" /tmp/btrfs2.img
  mkfs.btrfs -f \"\$LOOP1\"
  mkfs.btrfs -f \"\$LOOP2\"
  mkdir -p ${BTRFS_MOUNT_1} ${BTRFS_MOUNT_2}
  mount \"\$LOOP1\" ${BTRFS_MOUNT_1}
  mount \"\$LOOP2\" ${BTRFS_MOUNT_2}
'"

echo "==> Building driver image..."
${RUNTIME} build -t "${IMAGE}" .

echo "==> Deploying driver..."
bash "${SCRIPT_DIR}/../deploy.sh"

echo ""
echo "CI cluster '${CLUSTER}' is ready."
