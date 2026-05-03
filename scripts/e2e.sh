#!/usr/bin/env bash
# e2e.sh — end-to-end tests for btrfs-csi-driver.
# Requires a running Kubernetes cluster with the driver deployed and at least
# two StorageClasses backed by different btrfs filesystems.
# Run: bash scripts/e2e.sh
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
source "${SCRIPT_DIR}/common.sh"

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

log() { echo -e "  ${C_CYAN}•${C_RESET} $*"; }
pass() {
    echo -e "  ${C_GREEN}✓ $*${C_RESET}"
    PASS=$((PASS + 1))
    RESULTS+=("PASS: $*")
}
fail() {
    echo -e "  ${C_RED}✗ $*${C_RESET}"
    FAIL=$((FAIL + 1))
    RESULTS+=("FAIL: $*")
}
skip() {
    echo -e "  ${C_YELLOW}⊘ SKIP: $*${C_RESET}"
    SKIP=$((SKIP + 1))
    RESULTS+=("SKIP: $*")
}
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
    ${K} apply -f - <<EOF
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
    ${K} apply -f - <<EOF
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
    ${K} apply -f - <<EOF
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
    wait_until 120 "${K} get pod ${name} -n ${NAMESPACE} -o jsonpath='{.status.phase}'" "Succeeded"
    local rc=$?
    ${K} delete pod "${name}" -n "${NAMESPACE}" --wait=true 2>/dev/null || true
    return $rc
}

assert_pvc_binds() {
    local name="$1" msg="$2"
    if wait_until 60 "${K} get pvc ${name} -n ${NAMESPACE} -o jsonpath='{.status.phase}'" "Bound"; then
        pass "${msg}"
    else
        fail "${msg}"
    fi
}

# With WaitForFirstConsumer, PVCs stay Pending until a pod references them.
# bind_pvc runs a short-lived pod to trigger provisioning and waits for Bound.
bind_pvc() {
    local name="$1" msg="$2"
    run_pod "bind-${name}" "${name}" "true"
    assert_pvc_binds "${name}" "${msg}"
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
    if wait_until 60 "${K} get volumesnapshot ${name} -n ${NAMESPACE} -o jsonpath='{.status.readyToUse}'" "true"; then
        pass "${msg}"
    else
        fail "${msg}"
    fi
}

delete_resources() {
    for res in "$@"; do
        ${K} delete "${res}" -n "${NAMESPACE}" --wait=true 2>/dev/null || true
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
    ${K} delete namespace "${NAMESPACE}" --ignore-not-found --wait=true 2>/dev/null || true
}
trap cleanup EXIT

echo -e "${C_BOLD}btrfs-csi e2e test suite${C_RESET}"
log "Creating namespace ${NAMESPACE}..."
${K} create namespace "${NAMESPACE}" --dry-run=client -o yaml | ${K} apply -f - 2>/dev/null || true

# ─── Basic volume lifecycle ──────────────────────────────────────────────────

section "Basic volume lifecycle"

apply_pvc e2e-basic-pvc "$PRIMARY_STORAGECLASS" 256Mi

assert_pod_succeeds e2e-basic-writer e2e-basic-pvc \
    "echo hello-btrfs > /data/test.txt && cat /data/test.txt" \
    "PVC binds and writer pod succeeds"

delete_resources pvc/e2e-basic-pvc
pass "Resources cleaned up"

# ─── Snapshot and restore ────────────────────────────────────────────────────

section "Snapshot and restore"

apply_pvc e2e-snap-source-pvc "$PRIMARY_STORAGECLASS" 256Mi
run_pod e2e-snap-writer e2e-snap-source-pvc "echo snapshot-data > /data/snap.txt"

apply_snapshot e2e-snap "$PRIMARY_STORAGECLASS" e2e-snap-source-pvc
assert_snapshot_ready e2e-snap "Snapshot is ReadyToUse"

apply_pvc e2e-snap-restore-pvc "$PRIMARY_STORAGECLASS" 256Mi "$(ds_snapshot e2e-snap)"

assert_pod_succeeds e2e-snap-reader e2e-snap-restore-pvc \
    "grep snapshot-data /data/snap.txt" \
    "Restored data matches original"

delete_resources pvc/e2e-snap-restore-pvc pvc/e2e-snap-source-pvc volumesnapshot/e2e-snap
pass "Resources cleaned up"

# ─── Volume cloning ──────────────────────────────────────────────────────────

section "Volume cloning"

apply_pvc e2e-clone-source-pvc "$PRIMARY_STORAGECLASS" 256Mi
run_pod e2e-clone-writer e2e-clone-source-pvc "echo clone-source-data > /data/clone.txt"

apply_pvc e2e-clone-pvc "$PRIMARY_STORAGECLASS" 256Mi "$(ds_pvc e2e-clone-source-pvc)"

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
bind_pvc e2e-expand-pvc "PVC created at 100Mi"

${K} patch pvc e2e-expand-pvc -n "${NAMESPACE}" \
    -p '{"spec":{"resources":{"requests":{"storage":"500Mi"}}}}'

if wait_until 60 "${K} get pvc e2e-expand-pvc -n ${NAMESPACE} -o jsonpath='{.status.capacity.storage}'" "500Mi"; then
    pass "PVC expanded to 500Mi"
else
    fail "PVC did not expand to 500Mi"
fi

delete_resources pvc/e2e-expand-pvc

# ─── Quota enforcement ───────────────────────────────────────────────────────

section "Quota enforcement"

apply_pvc e2e-quota-pvc "$PRIMARY_STORAGECLASS" 50Mi

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

if ! ${K} get storageclass "${SECONDARY_STORAGECLASS}" &>/dev/null; then
    skip "StorageClass ${SECONDARY_STORAGECLASS} not found"
elif ! ${K} get volumesnapshotclass "${SECONDARY_STORAGECLASS}" &>/dev/null; then
    skip "VolumeSnapshotClass ${SECONDARY_STORAGECLASS} not found"
else
    apply_pvc e2e-xsnap-source-pvc "$PRIMARY_STORAGECLASS" 256Mi
    run_pod e2e-xsnap-writer e2e-xsnap-source-pvc "echo cross-pool-snapshot-data > /data/crosspool.txt"

    apply_snapshot e2e-xsnap "$SECONDARY_STORAGECLASS" e2e-xsnap-source-pvc
    assert_snapshot_ready e2e-xsnap "Snapshot created on secondary pool"

    apply_pvc e2e-xsnap-restore-pvc "$PRIMARY_STORAGECLASS" 256Mi "$(ds_snapshot e2e-xsnap)"

    assert_pod_succeeds e2e-xsnap-reader e2e-xsnap-restore-pvc \
        "grep cross-pool-snapshot-data /data/crosspool.txt" \
        "Restored data from secondary-pool snapshot matches"

    delete_resources pvc/e2e-xsnap-restore-pvc pvc/e2e-xsnap-source-pvc volumesnapshot/e2e-xsnap
    pass "Resources cleaned up"
fi

# ─── Volume clone to different pool ──────────────────────────────────────────

section "Cross-pool volume clone"

if ! ${K} get storageclass "${SECONDARY_STORAGECLASS}" &>/dev/null; then
    skip "StorageClass ${SECONDARY_STORAGECLASS} not found"
else
    apply_pvc e2e-xclone-source-pvc "$PRIMARY_STORAGECLASS" 256Mi
    run_pod e2e-xclone-writer e2e-xclone-source-pvc "echo clone-pool2-data > /data/clone.txt"

    apply_pvc e2e-xclone-dest-pvc "$SECONDARY_STORAGECLASS" 256Mi "$(ds_pvc e2e-xclone-source-pvc)"

    assert_pod_succeeds e2e-xclone-reader e2e-xclone-dest-pvc \
        "grep clone-pool2-data /data/clone.txt" \
        "Cloned data on secondary pool matches source"

    delete_resources pvc/e2e-xclone-dest-pvc pvc/e2e-xclone-source-pvc
    pass "Resources cleaned up"
fi

# ─── Nodatacow volumes ───────────────────────────────────────────────────────

section "Nodatacow — default CoW behavior"

apply_pvc e2e-cow-default-pvc "$PRIMARY_STORAGECLASS" 256Mi
bind_pvc e2e-cow-default-pvc "Default PVC bound"

_pv=$(${K} get pvc e2e-cow-default-pvc -n "${NAMESPACE}" -o jsonpath='{.spec.volumeName}')
_vol_id=$(${K} get pv "${_pv}" -o jsonpath='{.spec.csi.volumeHandle}')

if [ -z "${_vol_id}" ]; then
    fail "PV ${_pv} has empty volumeHandle"
else
    _attrs=$(${EXEC} "sudo lsattr -d /var/lib/btrfs-csi/default/volumes/${_vol_id}" 2>/dev/null | awk '{print $1}')

    if [ -z "${_attrs}" ]; then
        fail "Cannot read attributes for default volume ${_vol_id}"
    elif echo "${_attrs}" | grep -q "C"; then
        fail "Default PVC has nodatacow flag set (expected CoW)"
    else
        pass "Default PVC does not have nodatacow flag"
    fi
fi

delete_resources pvc/e2e-cow-default-pvc
pass "Resources cleaned up"

section "Nodatacow — cow=false volumes"

if ! ${K} get storageclass "btrfs-nodatacow" &>/dev/null; then
    skip "StorageClass btrfs-nodatacow not found"
else
    # Fresh volume with cow=false → verify C flag is set
    apply_pvc e2e-cow-nodatacow-pvc "btrfs-nodatacow" 256Mi
    bind_pvc e2e-cow-nodatacow-pvc "Nodatacow PVC bound"

    _pv=$(${K} get pvc e2e-cow-nodatacow-pvc -n "${NAMESPACE}" -o jsonpath='{.spec.volumeName}')
    _vol_id=$(${K} get pv "${_pv}" -o jsonpath='{.spec.csi.volumeHandle}')

    if [ -z "${_vol_id}" ]; then
        fail "PV ${_pv} has empty volumeHandle"
    else
        _attrs=$(${EXEC} "sudo lsattr -d /var/lib/btrfs-csi/default/volumes/${_vol_id}" 2>/dev/null | awk '{print $1}')

        if [ -z "${_attrs}" ]; then
            fail "Cannot read attributes for nodatacow volume ${_vol_id}"
        elif echo "${_attrs}" | grep -q "C"; then
            pass "Nodatacow PVC has C flag set"
        else
            fail "Nodatacow PVC is missing C flag"
        fi
    fi

    delete_resources pvc/e2e-cow-nodatacow-pvc
    pass "Resources cleaned up"

    # Clone from a nodatacow volume → verify C flag is inherited
    apply_pvc e2e-cow-clone-source-pvc "btrfs-nodatacow" 256Mi
    bind_pvc e2e-cow-clone-source-pvc "Nodatacow clone source bound"

    apply_pvc e2e-cow-clone-pvc "$PRIMARY_STORAGECLASS" 256Mi "$(ds_pvc e2e-cow-clone-source-pvc)"
    bind_pvc e2e-cow-clone-pvc "Clone from nodatacow source bound"

    _pv=$(${K} get pvc e2e-cow-clone-pvc -n "${NAMESPACE}" -o jsonpath='{.spec.volumeName}')
    _vol_id=$(${K} get pv "${_pv}" -o jsonpath='{.spec.csi.volumeHandle}')

    if [ -z "${_vol_id}" ]; then
        fail "PV ${_pv} has empty volumeHandle"
    else
        _attrs=$(${EXEC} "sudo lsattr -d /var/lib/btrfs-csi/default/volumes/${_vol_id}" 2>/dev/null | awk '{print $1}')

        if [ -z "${_attrs}" ]; then
            fail "Cannot read attributes for clone volume ${_vol_id}"
        elif echo "${_attrs}" | grep -q "C"; then
            pass "Clone from nodatacow source inherits C flag"
        else
            fail "Clone from nodatacow source is missing C flag"
        fi
    fi

    delete_resources pvc/e2e-cow-clone-pvc pvc/e2e-cow-clone-source-pvc
    pass "Resources cleaned up"

    # Clone from a normal volume → verify C flag is NOT set
    apply_pvc e2e-cow-reverse-source-pvc "$PRIMARY_STORAGECLASS" 256Mi
    bind_pvc e2e-cow-reverse-source-pvc "Default CoW clone source bound"

    apply_pvc e2e-cow-reverse-clone-pvc "btrfs-nodatacow" 256Mi "$(ds_pvc e2e-cow-reverse-source-pvc)"
    bind_pvc e2e-cow-reverse-clone-pvc "Clone from default source bound"

    _pv=$(${K} get pvc e2e-cow-reverse-clone-pvc -n "${NAMESPACE}" -o jsonpath='{.spec.volumeName}')
    _vol_id=$(${K} get pv "${_pv}" -o jsonpath='{.spec.csi.volumeHandle}')

    if [ -z "${_vol_id}" ]; then
        fail "PV ${_pv} has empty volumeHandle"
    else
        _attrs=$(${EXEC} "sudo lsattr -d /var/lib/btrfs-csi/default/volumes/${_vol_id}" 2>/dev/null | awk '{print $1}')

        if [ -z "${_attrs}" ]; then
            fail "Cannot read attributes for reverse clone volume ${_vol_id}"
        elif echo "${_attrs}" | grep -q "C"; then
            fail "Clone from default source has unexpected C flag"
        else
            pass "Clone from default source correctly does not have C flag"
        fi
    fi

    delete_resources pvc/e2e-cow-reverse-clone-pvc pvc/e2e-cow-reverse-source-pvc
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
