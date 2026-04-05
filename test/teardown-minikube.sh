#!/usr/bin/env bash
# teardown-minikube.sh — tears down a minikube cluster.
#
# Prerequisites: minikube, kubectl, qemu (no root or Docker required on the host).
#
# Usage:
#   bash test/teardown-minikube.sh

set -euo pipefail

CLUSTER="${CLUSTER:-btrfs-csi}"

minikube delete --profile "${CLUSTER}"
