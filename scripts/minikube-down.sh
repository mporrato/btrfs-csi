#!/usr/bin/env bash
# minikube-down.sh — tears down the minikube dev cluster.
#
# Usage:
#   bash scripts/minikube-down.sh

set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
source "${SCRIPT_DIR}/common.sh"

${MK} delete
