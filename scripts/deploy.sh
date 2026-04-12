#!/usr/bin/env bash
# deploy.sh — loads the driver image into minikube and deploys all manifests.
# Called by minikube-up.sh (dev) and ci/setup.sh (CI).
#
# Expects the driver image to be already built locally.
#
# Usage:
#   bash scripts/deploy.sh
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
source "${SCRIPT_DIR}/common.sh"

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
