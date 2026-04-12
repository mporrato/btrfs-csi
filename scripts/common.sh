# common.sh — shared configuration variables for btrfs-csi scripts.
# This file is sourced by other scripts; do not execute directly.
#
# Usage in other scripts:
#   SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
#   source "${SCRIPT_DIR}/common.sh"

# ─── Cluster Configuration ─────────────────────────────────────────────────

# Minikube cluster name
: "${CLUSTER:=btrfs-csi}"

# Container runtime (auto-detect: prefer podman over docker)
: "${RUNTIME:=$(command -v podman 2>/dev/null || command -v docker 2>/dev/null)}"

# Driver image for loading into minikube
: "${IMAGE:=localhost/btrfs-csi-driver:latest}"

# ─── Disk and Mount Configuration ────────────────────────────────────────────

# Primary extra disk device (for minikube --extra-disks)
: "${EXTRA_DISK_1_DEV:=/dev/vda}"

# Secondary extra disk device (for multi-pool testing)
: "${EXTRA_DISK_2_DEV:=/dev/vdb}"

# Base directory scanned by the driver for pool subdirectories
: "${POOLS_DIR:=/var/lib/btrfs-csi}"

# Primary btrfs mount point (pool named "default")
: "${BTRFS_MOUNT_1:=${POOLS_DIR}/default}"

# Secondary btrfs mount point (pool named "secondary", for multi-pool testing)
: "${BTRFS_MOUNT_2:=${POOLS_DIR}/secondary}"

# ─── Kubernetes and CSI Configuration ────────────────────────────────────────

# Namespace for e2e tests
: "${NAMESPACE:=btrfs-csi-e2e}"

# Primary storage class name
: "${PRIMARY_STORAGECLASS:=btrfs}"

# Secondary storage class name (for multi-pool tests)
: "${SECONDARY_STORAGECLASS:=btrfs-secondary}"

# ─── Shorthand Commands ──────────────────────────────────────────────────────

# These can be overridden via environment variables (e.g., CI sets sudo prefixes).
: "${MK:=minikube --profile=${CLUSTER}}"
: "${K:=kubectl --context=${CLUSTER}}"
: "${EXEC:=${MK} ssh --}"

export CLUSTER RUNTIME MK K EXEC
