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

# Primary btrfs mount point
: "${BTRFS_MOUNT_1:=/var/lib/btrfs-csi}"

# Secondary btrfs mount point (for multi-pool testing)
: "${BTRFS_MOUNT_2:=/var/lib/btrfs-csi-extra}"

# ─── Kubernetes and CSI Configuration ────────────────────────────────────────

# Namespace for e2e tests
: "${NAMESPACE:=btrfs-csi-e2e}"

# Primary storage class name
: "${PRIMARY_STORAGECLASS:=btrfs}"

# Secondary storage class name (for multi-pool tests)
: "${SECONDARY_STORAGECLASS:=btrfs-secondary}"

# ─── Shorthand Commands ──────────────────────────────────────────────────────

# These are exported so they're available to subprocesses
export CLUSTER
export RUNTIME
export MK="minikube --profile=${CLUSTER}"
export K="kubectl --context=${CLUSTER}"
