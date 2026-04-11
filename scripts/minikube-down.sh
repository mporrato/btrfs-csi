#!/usr/bin/env bash
# minikube-down.sh — tears down the minikube dev cluster.
#
# Usage:
#   bash scripts/minikube-down.sh

set -euo pipefail

CLUSTER="${CLUSTER:-btrfs-csi}"

minikube delete --profile "${CLUSTER}"
