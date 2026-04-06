#!/usr/bin/env bash
# e2e.sh — end-to-end tests for btrfs-csi-driver.
# Requires a running Kubernetes cluster with the driver deployed and at least
# two StorageClasses backed by different btrfs filesystems.
# Run: bash test/e2e.sh
set -euo pipefail

KUBECTL="${KUBECTL:-kubectl}"
NAMESPACE="${NAMESPACE:-btrfs-csi-e2e}"
PRIMARY_STORAGECLASS="${PRIMARY_STORAGECLASS:-btrfs}"
SECONDARY_STORAGECLASS="${SECONDARY_STORAGECLASS:-btrfs-secondary}"

PASS=0
FAIL=0
SKIP=0
RESULTS=()

# ─── Colors & output ────────────────────────────────────────────────────────

if [ -t 1 ]; then
  C_RESET='\033[0m' C_BOLD='\033[1m'
  C_GREEN='\033[32m' C_RED='\033[31m' C_YELLOW='\033[33m' C_CYAN='\033[36m'
else
  C_RESET='' C_BOLD='' C_GREEN='' C_RED='' C_YELLOW='' C_CYAN=''
fi

log()     { echo -e "  ${C_CYAN}•${C_RESET} $*"; }
pass()    { echo -e "  ${C_GREEN}✓ $*${C_RESET}"; PASS=$((PASS+1)); RESULTS+=("PASS: $*"); }
fail()    { echo -e "  ${C_RED}✗ $*${C_RESET}"; FAIL=$((FAIL+1)); RESULTS+=("FAIL: $*"); }
skip()    { echo -e "  ${C_YELLOW}⊘ SKIP: $*${C_RESET}"; SKIP=$((SKIP+1)); RESULTS+=("SKIP: $*"); }
section() { echo -e "\n${C_BOLD}━━━ $* ━━━${C_RESET}"; }

# ─── Helpers ─────────────────────────────────────────────────────────────────

# wait_for condition on a k8s resource (generic polling loop).
# Usage: wait_until <timeout_secs> <poll_cmd> <expected_value>
wait_until() {
  local timeout="$1" cmd="$2" expected="$3"
  local deadline=$((SECONDS + timeout))
  while [ $SECONDS -lt $deadline ]; do
    local val
    val=$(eval "${cmd}" 2>/dev/null) || true
    [ "${val}" = "${expected}" ] && return 0
    sleep 2
  done
  return 1
}

# apply_pvc <name> <storageclass> <size> [datasource_yaml]
apply_pvc() {
  local name="$1" sc="$2" size="$3" datasource="${4:-}"
  ${KUBECTL} apply -f - <<EOF
apiVersion: v1
kind: PersistentVolumeClaim
metadata:
  name: ${name}
  namespace: ${NAMESPACE}
  labels:
    btrfs-csi-e2e: "true"
spec:
  accessModes: [ReadWriteOnce]
  storageClassName: ${sc}
  resources:
    requests:
      storage: ${size}
${datasource}
EOF
}

# apply_snapshot <name> <snapshot_class> <source_pvc>
apply_snapshot() {
  local name="$1" class="$2" source="$3"
  ${KUBECTL} apply -f - <<EOF
apiVersion: snapshot.storage.k8s.io/v1
kind: VolumeSnapshot
metadata:
  name: ${name}
  namespace: ${NAMESPACE}
  labels:
    btrfs-csi-e2e: "true"
spec:
  volumeSnapshotClassName: ${class}
  source:
    persistentVolumeClaimName: ${source}
EOF
}

# run_pod <name> <pvc> <command>
# Creates a busybox pod mounting the PVC at /data, waits for completion, deletes it.
run_pod() {
  local name="$1" pvc="$2" cmd="$3"
  ${KUBECTL} apply -f - <<EOF
apiVersion: v1
kind: Pod
metadata:
  name: ${name}
  namespace: ${NAMESPACE}
  labels:
    btrfs-csi-e2e: "true"
spec:
  restartPolicy: Never
  containers:
    - name: main
      image: busybox
      command: [sh, -c, "${cmd}"]
      volumeMounts:
        - name: vol
          mountPath: /data
  volumes:
    - name: vol
      persistentVolumeClaim:
        claimName: ${pvc}
EOF
  wait_until 120 "${KUBECTL} get pod ${name} -n ${NAMESPACE} -o jsonpath='{.status.phase}'" "Succeeded"
  local rc=$?
  ${KUBECTL} delete pod "${name}" -n "${NAMESPACE}" --wait=true 2>/dev/null || true
  return $rc
}

assert_pvc_binds() {
  local name="$1" msg="$2"
  if wait_until 60 "${KUBECTL} get pvc ${name} -n ${NAMESPACE} -o jsonpath='{.status.phase}'" "Bound"; then
    pass "${msg}"
  else
    fail "${msg}"
  fi
}

assert_pod_succeeds() {
  local name="$1" pvc="$2" cmd="$3" msg="$4"
  if run_pod "${name}" "${pvc}" "${cmd}"; then
    pass "${msg}"
  else
    fail "${msg}"
  fi
}

assert_snapshot_ready() {
  local name="$1" msg="$2"
  if wait_until 60 "${KUBECTL} get volumesnapshot ${name} -n ${NAMESPACE} -o jsonpath='{.status.readyToUse}'" "true"; then
    pass "${msg}"
  else
    fail "${msg}"
  fi
}

delete_resources() {
  for res in "$@"; do
    ${KUBECTL} delete "${res}" -n "${NAMESPACE}" --wait=true 2>/dev/null || true
  done
}

# datasource snippet helpers
ds_snapshot() { # <snapshot_name>
  cat <<EOF
  dataSource:
    name: $1
    kind: VolumeSnapshot
    apiGroup: snapshot.storage.k8s.io
EOF
}

ds_pvc() { # <pvc_name>
  cat <<EOF
  dataSource:
    name: $1
    kind: PersistentVolumeClaim
EOF
}

# ─── Setup & teardown ────────────────────────────────────────────────────────

cleanup() {
  echo ""
  log "Cleaning up namespace ${NAMESPACE}..."
  ${KUBECTL} delete namespace "${NAMESPACE}" --ignore-not-found --wait=true 2>/dev/null || true
}
trap cleanup EXIT

echo -e "${C_BOLD}btrfs-csi e2e test suite${C_RESET}"
log "Creating namespace ${NAMESPACE}..."
${KUBECTL} create namespace "${NAMESPACE}" --dry-run=client -o yaml | ${KUBECTL} apply -f - 2>/dev/null || true

# ─── Basic volume lifecycle ──────────────────────────────────────────────────

section "Basic volume lifecycle"

apply_pvc e2e-basic-pvc "$PRIMARY_STORAGECLASS" 256Mi
assert_pvc_binds e2e-basic-pvc "PVC becomes Bound"

assert_pod_succeeds e2e-basic-writer e2e-basic-pvc \
  "echo hello-btrfs > /data/test.txt && cat /data/test.txt" \
  "Writer pod succeeds"

delete_resources pvc/e2e-basic-pvc
pass "Resources cleaned up"

# ─── Snapshot and restore ────────────────────────────────────────────────────

section "Snapshot and restore"

apply_pvc e2e-snap-source-pvc "$PRIMARY_STORAGECLASS" 256Mi
assert_pvc_binds e2e-snap-source-pvc "Source PVC becomes Bound"
run_pod e2e-snap-writer e2e-snap-source-pvc "echo snapshot-data > /data/snap.txt"

apply_snapshot e2e-snap "$PRIMARY_STORAGECLASS" e2e-snap-source-pvc
assert_snapshot_ready e2e-snap "Snapshot is ReadyToUse"

apply_pvc e2e-snap-restore-pvc "$PRIMARY_STORAGECLASS" 256Mi "$(ds_snapshot e2e-snap)"
assert_pvc_binds e2e-snap-restore-pvc "Restored PVC becomes Bound"

assert_pod_succeeds e2e-snap-reader e2e-snap-restore-pvc \
  "grep snapshot-data /data/snap.txt" \
  "Restored data matches original"

delete_resources pvc/e2e-snap-restore-pvc pvc/e2e-snap-source-pvc volumesnapshot/e2e-snap
pass "Resources cleaned up"

# ─── Volume cloning ──────────────────────────────────────────────────────────

section "Volume cloning"

apply_pvc e2e-clone-source-pvc "$PRIMARY_STORAGECLASS" 256Mi
assert_pvc_binds e2e-clone-source-pvc "Source PVC becomes Bound"
run_pod e2e-clone-writer e2e-clone-source-pvc "echo clone-source-data > /data/clone.txt"

apply_pvc e2e-clone-pvc "$PRIMARY_STORAGECLASS" 256Mi "$(ds_pvc e2e-clone-source-pvc)"
assert_pvc_binds e2e-clone-pvc "Clone PVC becomes Bound"

assert_pod_succeeds e2e-clone-verifier e2e-clone-pvc \
  "grep clone-source-data /data/clone.txt && echo override > /data/clone.txt" \
  "Clone contains source data and accepts writes"

assert_pod_succeeds e2e-clone-source-check e2e-clone-source-pvc \
  "grep clone-source-data /data/clone.txt" \
  "Source PVC unaffected by clone writes"

delete_resources pvc/e2e-clone-pvc pvc/e2e-clone-source-pvc
pass "Resources cleaned up"

# ─── Volume expansion ────────────────────────────────────────────────────────

section "Volume expansion"

apply_pvc e2e-expand-pvc "$PRIMARY_STORAGECLASS" 100Mi
assert_pvc_binds e2e-expand-pvc "PVC created at 100Mi"

${KUBECTL} patch pvc e2e-expand-pvc -n "${NAMESPACE}" \
  -p '{"spec":{"resources":{"requests":{"storage":"500Mi"}}}}'

if wait_until 60 "${KUBECTL} get pvc e2e-expand-pvc -n ${NAMESPACE} -o jsonpath='{.status.capacity.storage}'" "500Mi"; then
  pass "PVC expanded to 500Mi"
else
  fail "PVC did not expand to 500Mi"
fi

delete_resources pvc/e2e-expand-pvc

# ─── Quota enforcement ───────────────────────────────────────────────────────

section "Quota enforcement"

apply_pvc e2e-quota-pvc "$PRIMARY_STORAGECLASS" 50Mi
assert_pvc_binds e2e-quota-pvc "Quota-limited PVC created at 50Mi"

assert_pod_succeeds e2e-quota-writer e2e-quota-pvc \
  "dd if=/dev/zero of=/data/file bs=1M count=40 && echo 'Write succeeded within limit'" \
  "Write within quota limit succeeds"

assert_pod_succeeds e2e-quota-overflow e2e-quota-pvc \
  "dd if=/dev/zero of=/data/overflow bs=1M count=100 2>&1 || echo 'Write correctly rejected at quota limit'" \
  "Write exceeding quota is rejected"

delete_resources pvc/e2e-quota-pvc
pass "Resources cleaned up"

# ─── Snapshot creation on different pool ─────────────────────────────────────

section "Cross-pool snapshot"

if ! ${KUBECTL} get storageclass "${SECONDARY_STORAGECLASS}" &>/dev/null; then
  skip "StorageClass ${SECONDARY_STORAGECLASS} not found"
elif ! ${KUBECTL} get volumesnapshotclass "${SECONDARY_STORAGECLASS}" &>/dev/null; then
  skip "VolumeSnapshotClass ${SECONDARY_STORAGECLASS} not found"
else
  apply_pvc e2e-xsnap-source-pvc "$PRIMARY_STORAGECLASS" 256Mi
  assert_pvc_binds e2e-xsnap-source-pvc "Source PVC becomes Bound"
  run_pod e2e-xsnap-writer e2e-xsnap-source-pvc "echo cross-pool-snapshot-data > /data/crosspool.txt"

  apply_snapshot e2e-xsnap "$SECONDARY_STORAGECLASS" e2e-xsnap-source-pvc
  assert_snapshot_ready e2e-xsnap "Snapshot created on secondary pool"

  apply_pvc e2e-xsnap-restore-pvc "$PRIMARY_STORAGECLASS" 256Mi "$(ds_snapshot e2e-xsnap)"
  assert_pvc_binds e2e-xsnap-restore-pvc "Restored PVC becomes Bound"

  assert_pod_succeeds e2e-xsnap-reader e2e-xsnap-restore-pvc \
    "grep cross-pool-snapshot-data /data/crosspool.txt" \
    "Restored data from secondary-pool snapshot matches"

  delete_resources pvc/e2e-xsnap-restore-pvc pvc/e2e-xsnap-source-pvc volumesnapshot/e2e-xsnap
  pass "Resources cleaned up"
fi

# ─── Volume clone to different pool ──────────────────────────────────────────

section "Cross-pool volume clone"

if ! ${KUBECTL} get storageclass "${SECONDARY_STORAGECLASS}" &>/dev/null; then
  skip "StorageClass ${SECONDARY_STORAGECLASS} not found"
else
  apply_pvc e2e-xclone-source-pvc "$PRIMARY_STORAGECLASS" 256Mi
  assert_pvc_binds e2e-xclone-source-pvc "Source PVC becomes Bound"
  run_pod e2e-xclone-writer e2e-xclone-source-pvc "echo clone-pool2-data > /data/clone.txt"

  apply_pvc e2e-xclone-dest-pvc "$SECONDARY_STORAGECLASS" 256Mi "$(ds_pvc e2e-xclone-source-pvc)"
  assert_pvc_binds e2e-xclone-dest-pvc "Clone PVC becomes Bound on secondary pool"

  assert_pod_succeeds e2e-xclone-reader e2e-xclone-dest-pvc \
    "grep clone-pool2-data /data/clone.txt" \
    "Cloned data on secondary pool matches source"

  delete_resources pvc/e2e-xclone-dest-pvc pvc/e2e-xclone-source-pvc
  pass "Resources cleaned up"
fi

# ─── Summary ─────────────────────────────────────────────────────────────────

echo ""
echo -e "${C_BOLD}━━━ Results ━━━${C_RESET}"
for r in "${RESULTS[@]}"; do
  case "${r}" in
    PASS:*) echo -e "  ${C_GREEN}✓${C_RESET} ${r#PASS: }" ;;
    FAIL:*) echo -e "  ${C_RED}✗${C_RESET} ${r#FAIL: }" ;;
    SKIP:*) echo -e "  ${C_YELLOW}⊘${C_RESET} ${r#SKIP: }" ;;
  esac
done
echo ""
echo -e "  ${C_GREEN}${PASS} passed${C_RESET}, ${C_RED}${FAIL} failed${C_RESET}, ${C_YELLOW}${SKIP} skipped${C_RESET}"
echo ""
[ "${FAIL}" -eq 0 ]
