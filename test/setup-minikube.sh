#!/usr/bin/env bash
# setup-minikube.sh — starts a minikube cluster with QEMU driver, formats two extra
# disks as btrfs on independent filesystems, and deploys the btrfs-csi-driver.
#
# Prerequisites: minikube, kubectl, qemu (no root or Docker required on the host).
#
# Usage:
#   IMAGE=btrfs-csi-driver:latest bash test/setup-minikube.sh
set -euo pipefail

IMAGE="${IMAGE:-localhost/btrfs-csi-driver:latest}"
CLUSTER="${CLUSTER:-btrfs-csi}"
EXTRA_DISK_1_DEV="${EXTRA_DISK_1_DEV:-/dev/vda}" # first extra disk added by --extra-disks=2
EXTRA_DISK_2_DEV="${EXTRA_DISK_2_DEV:-/dev/vdb}" # second extra disk added by --extra-disks=2
BTRFS_MOUNT_1="${BTRFS_MOUNT_1:-/var/lib/btrfs-csi}"
BTRFS_MOUNT_2="${BTRFS_MOUNT_2:-/var/lib/btrfs-csi-extra}"
SNAPSHOTTER_VERSION="${SNAPSHOTTER_VERSION:-v8.0.0}"

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
RUNTIME=$(command -v podman 2>/dev/null || command -v docker 2>/dev/null)
MK="minikube --profile=${CLUSTER}"
K="kubectl --context=${CLUSTER}"
EXEC="${MK} ssh --"

echo "==> Starting minikube cluster '${CLUSTER}' (qemu, 2 extra disks)..."
${MK} start \
    --driver=qemu \
    --extra-disks=2

echo "==> Formatting ${EXTRA_DISK_1_DEV} as btrfs and mounting to ${BTRFS_MOUNT_1}..."
${EXEC} "sudo mkfs.btrfs -f ${EXTRA_DISK_1_DEV}"
${EXEC} "sudo mkdir -p ${BTRFS_MOUNT_1} && echo '${EXTRA_DISK_1_DEV} ${BTRFS_MOUNT_1} btrfs defaults 0 0' | sudo tee -a /etc/fstab && sudo mount ${BTRFS_MOUNT_1}"

echo "==> Formatting ${EXTRA_DISK_2_DEV} as btrfs and mounting to ${BTRFS_MOUNT_2}..."
${EXEC} "sudo mkfs.btrfs -f ${EXTRA_DISK_2_DEV}"
${EXEC} "sudo mkdir -p ${BTRFS_MOUNT_2} && echo '${EXTRA_DISK_2_DEV} ${BTRFS_MOUNT_2} btrfs defaults 0 0' | sudo tee -a /etc/fstab && sudo mount ${BTRFS_MOUNT_2}"

echo "==> Installing VolumeSnapshot CRDs (${SNAPSHOTTER_VERSION})..."
BASE="https://raw.githubusercontent.com/kubernetes-csi/external-snapshotter/${SNAPSHOTTER_VERSION}"
${K} apply -f "${BASE}/client/config/crd/snapshot.storage.k8s.io_volumesnapshotclasses.yaml"
${K} apply -f "${BASE}/client/config/crd/snapshot.storage.k8s.io_volumesnapshotcontents.yaml"
${K} apply -f "${BASE}/client/config/crd/snapshot.storage.k8s.io_volumesnapshots.yaml"

echo "==> Deploying snapshot-controller (${SNAPSHOTTER_VERSION})..."
${K} apply -f "${BASE}/deploy/kubernetes/snapshot-controller/rbac-snapshot-controller.yaml"
${K} apply -f "${BASE}/deploy/kubernetes/snapshot-controller/setup-snapshot-controller.yaml"
${K} rollout status deployment/snapshot-controller -n kube-system --timeout=120s

echo "==> Loading driver image into minikube..."
${RUNTIME} save "${IMAGE}" | ${MK} image load -

echo "==> Deploying btrfs-csi-driver..."
# Extract namespace name from manifest and create it first, then wait for it to be active
# before deploying other resources (avoids race condition with resources created in parallel).
NS=$(awk '/^[[:space:]]*name:/ {print $NF; exit}' "${SCRIPT_DIR}/../deploy/namespace.yaml")
${K} apply -f "${SCRIPT_DIR}/../deploy/namespace.yaml"
${K} wait --for=jsonpath='{.status.phase}'=Active "namespace/${NS}" --timeout=10s || true

# Now deploy all resources (they'll be created in the ready namespace)
${K} apply -f "${SCRIPT_DIR}/../deploy/"

echo "==> Patching ConfigMap to add secondary pool..."
${K} patch configmap btrfs-csi-pools -n "${NS}" --type merge -p \
  "{\"data\": {\"secondary\": \"${BTRFS_MOUNT_2}\"}}"

echo "==> Patching DaemonSet to mount secondary btrfs filesystem..."
${K} patch daemonset btrfs-csi-driver -n "${NS}" --type json -p \
  '[
    {"op": "add", "path": "/spec/template/spec/volumes/1", "value": {"name": "btrfs-data-dir-secondary", "hostPath": {"path": "'${BTRFS_MOUNT_2}'", "type": "DirectoryOrCreate"}}},
    {"op": "add", "path": "/spec/template/spec/containers/0/volumeMounts/1", "value": {"name": "btrfs-data-dir-secondary", "mountPath": "'${BTRFS_MOUNT_2}'"}}
  ]'

echo "==> Creating secondary StorageClass pointing to secondary pool..."
sed \
  -e 's/^  name: btrfs$/  name: btrfs-secondary/' \
  -e 's/^# parameters:/parameters:/' \
  -e 's/^#   pool: default.*$/  pool: secondary/' \
  "${SCRIPT_DIR}/../deploy/storageclass.yaml" | ${K} apply -f -

echo "==> Waiting for DaemonSet to be ready..."
${K} rollout status daemonset/btrfs-csi-driver \
    -n "${NS}" --timeout=120s

echo ""
echo "Cluster '${CLUSTER}' is ready."
echo "  kubectl --context=${CLUSTER} get pods -n ${NS}"
echo "  minikube ssh --profile=${CLUSTER}"
