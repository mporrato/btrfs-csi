#!/usr/bin/env bash
# setup-minikube.sh — starts a minikube cluster with QEMU driver, formats the extra
# disk as btrfs, and deploys the btrfs-csi-driver.
#
# Prerequisites: minikube, kubectl, qemu (no root or Docker required on the host).
#
# Usage:
#   IMAGE=btrfs-csi-driver:latest bash test/setup-minikube.sh
set -euo pipefail

IMAGE="${IMAGE:-btrfs-csi-driver:latest}"
CLUSTER="${CLUSTER:-btrfs-csi}"
EXTRA_DISK_DEV="${EXTRA_DISK_DEV:-/dev/vda}"  # extra disk added by --extra-disks=1
BTRFS_MOUNT="${BTRFS_MOUNT:-/var/lib/btrfs-csi}"
SNAPSHOTTER_VERSION="${SNAPSHOTTER_VERSION:-v8.0.0}"

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"

echo "==> Starting minikube cluster '${CLUSTER}' (qemu, extra disk)..."
minikube start \
  --driver=qemu \
  --extra-disks=1 \
  --profile="${CLUSTER}"

echo "==> Formatting ${EXTRA_DISK_DEV} as btrfs and persisting mount via fstab..."
minikube ssh --profile="${CLUSTER}" -- bash -s <<EOF
  set -euo pipefail
  sudo mkfs.btrfs -f ${EXTRA_DISK_DEV}
  sudo mkdir -p ${BTRFS_MOUNT}
  echo '${EXTRA_DISK_DEV} ${BTRFS_MOUNT} btrfs defaults 0 0' | sudo tee -a /etc/fstab
  sudo mount ${BTRFS_MOUNT}
EOF

echo "==> Installing VolumeSnapshot CRDs (${SNAPSHOTTER_VERSION})..."
BASE="https://raw.githubusercontent.com/kubernetes-csi/external-snapshotter/${SNAPSHOTTER_VERSION}/client/config/crd"
kubectl --context="${CLUSTER}" apply -f "${BASE}/snapshot.storage.k8s.io_volumesnapshotclasses.yaml"
kubectl --context="${CLUSTER}" apply -f "${BASE}/snapshot.storage.k8s.io_volumesnapshotcontents.yaml"
kubectl --context="${CLUSTER}" apply -f "${BASE}/snapshot.storage.k8s.io_volumesnapshots.yaml"

echo "==> Loading driver image into minikube..."
minikube image load "${IMAGE}" --profile="${CLUSTER}"

echo "==> Deploying btrfs-csi-driver..."
kubectl --context="${CLUSTER}" apply -f "${SCRIPT_DIR}/../deploy/"

echo "==> Waiting for DaemonSet to be ready..."
kubectl --context="${CLUSTER}" rollout status daemonset/btrfs-csi-driver \
  -n kube-system --timeout=120s

echo ""
echo "Cluster '${CLUSTER}' is ready."
echo "  kubectl --context=${CLUSTER} get pods -n kube-system"
echo "  minikube ssh --profile=${CLUSTER}"
