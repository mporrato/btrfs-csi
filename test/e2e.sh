#!/usr/bin/env bash
# e2e.sh — end-to-end tests for btrfs-csi-driver.
# Requires a running Kubernetes cluster with the driver deployed and at least
# two StargeClasses backed by different btrfs filesystem.
# Run: bash test/e2e.sh
set -euo pipefail

KUBECTL="${KUBECTL:-kubectl}"
NAMESPACE="${NAMESPACE:-btrfs-csi-e2e}"
PRIMARY_STORAGECLASS="${PRIMARY_STORAGECLASS:-btrfs}"
SECONDARY_STORAGECLASS="${SECONDARY_STORAGECLASS:-btrfs-secondary}"
PASS=0
FAIL=0

log()  { echo "[$(date -u +%T)] $*"; }
pass() { log "PASS: $*"; PASS=$((PASS + 1)); }
fail() { log "FAIL: $*"; FAIL=$((FAIL + 1)); }

# wait_for_pvc waits until a PVC reaches Bound phase (timeout 60s).
wait_for_pvc() {
  local name="$1" ns="${2:-${NAMESPACE}}"
  for i in $(seq 1 30); do
    phase=$(${KUBECTL} get pvc "${name}" -n "${ns}" -o jsonpath='{.status.phase}' 2>/dev/null || true)
    [ "${phase}" = "Bound" ] && return 0
    sleep 2
  done
  log "PVC ${name} did not reach Bound after 60s (phase=${phase})"
  return 1
}

# wait_for_pod waits until a Pod reaches Succeeded phase (timeout 120s).
wait_for_pod_succeeded() {
  local name="$1" ns="${2:-${NAMESPACE}}"
  for i in $(seq 1 60); do
    phase=$(${KUBECTL} get pod "${name}" -n "${ns}" -o jsonpath='{.status.phase}' 2>/dev/null || true)
    [ "${phase}" = "Succeeded" ] && return 0
    sleep 2
  done
  log "Pod ${name} did not reach Succeeded after 120s (phase=${phase})"
  return 1
}

# wait_for_snapshot waits until a VolumeSnapshot is ReadyToUse (timeout 60s).
wait_for_snapshot() {
  local name="$1" ns="${2:-${NAMESPACE}}"
  for i in $(seq 1 30); do
    ready=$(${KUBECTL} get volumesnapshot "${name}" -n "${ns}" -o jsonpath='{.status.readyToUse}' 2>/dev/null || true)
    [ "${ready}" = "true" ] && return 0
    sleep 2
  done
  log "VolumeSnapshot ${name} not ReadyToUse after 60s"
  return 1
}

cleanup() {
  log "Cleaning up test resources..."
  ${KUBECTL} delete namespace "${NAMESPACE}" --ignore-not-found --wait=true 2>/dev/null || true
}
trap cleanup EXIT

# Create dedicated namespace for e2e tests
log "Creating namespace ${NAMESPACE}..."
${KUBECTL} create namespace "${NAMESPACE}" --dry-run=client -o yaml | ${KUBECTL} apply -f - 2>/dev/null || true

# ─── Basic volume lifecycle ───────────────────────────────────────────────────
log "=== Basic volume lifecycle ==="

${KUBECTL} apply -f - <<EOF
apiVersion: v1
kind: PersistentVolumeClaim
metadata:
  name: e2e-basic-pvc
  namespace: ${NAMESPACE}
  labels:
    btrfs-csi-e2e: "true"
spec:
  accessModes: [ReadWriteOnce]
  storageClassName: ${PRIMARY_STORAGECLASS}
  resources:
    requests:
      storage: 256Mi
EOF

if wait_for_pvc e2e-basic-pvc; then
  pass "PVC e2e-basic-pvc became Bound"
else
  fail "PVC e2e-basic-pvc did not become Bound"
fi

${KUBECTL} apply -f - <<EOF
apiVersion: v1
kind: Pod
metadata:
  name: e2e-basic-writer
  namespace: ${NAMESPACE}
  labels:
    btrfs-csi-e2e: "true"
spec:
  restartPolicy: Never
  containers:
    - name: writer
      image: busybox
      command: [sh, -c, "echo hello-btrfs > /data/test.txt && cat /data/test.txt"]
      volumeMounts:
        - name: vol
          mountPath: /data
  volumes:
    - name: vol
      persistentVolumeClaim:
        claimName: e2e-basic-pvc
EOF

if wait_for_pod_succeeded e2e-basic-writer; then
  pass "Pod e2e-basic-writer succeeded"
else
  fail "Pod e2e-basic-writer did not succeed"
fi

${KUBECTL} delete pod e2e-basic-writer -n "${NAMESPACE}" --wait=true 2>/dev/null || true
${KUBECTL} delete pvc e2e-basic-pvc -n "${NAMESPACE}" --wait=true 2>/dev/null || true
pass "PVC e2e-basic-pvc deleted"

# ─── Snapshot and restore ────────────────────────────────────────────────────
log "=== Snapshot and restore ==="

${KUBECTL} apply -f - <<EOF
apiVersion: v1
kind: PersistentVolumeClaim
metadata:
  name: e2e-snap-source-pvc
  namespace: ${NAMESPACE}
  labels:
    btrfs-csi-e2e: "true"
spec:
  accessModes: [ReadWriteOnce]
  storageClassName: ${PRIMARY_STORAGECLASS}
  resources:
    requests:
      storage: 256Mi
---
apiVersion: v1
kind: Pod
metadata:
  name: e2e-snap-writer
  namespace: ${NAMESPACE}
  labels:
    btrfs-csi-e2e: "true"
spec:
  restartPolicy: Never
  containers:
    - name: writer
      image: busybox
      command: [sh, -c, "echo snapshot-data > /data/snap.txt"]
      volumeMounts:
        - name: vol
          mountPath: /data
  volumes:
    - name: vol
      persistentVolumeClaim:
        claimName: e2e-snap-source-pvc
EOF

wait_for_pvc e2e-snap-source-pvc && wait_for_pod_succeeded e2e-snap-writer
${KUBECTL} delete pod e2e-snap-writer -n "${NAMESPACE}" --wait=true 2>/dev/null || true

${KUBECTL} apply -f - <<EOF
apiVersion: snapshot.storage.k8s.io/v1
kind: VolumeSnapshot
metadata:
  name: e2e-snap
  namespace: ${NAMESPACE}
  labels:
    btrfs-csi-e2e: "true"
spec:
  volumeSnapshotClassName: btrfs-snapshot
  source:
    persistentVolumeClaimName: e2e-snap-source-pvc
EOF

if wait_for_snapshot e2e-snap; then
  pass "VolumeSnapshot e2e-snap is ReadyToUse"
else
  fail "VolumeSnapshot e2e-snap not ready"
fi

${KUBECTL} apply -f - <<EOF
apiVersion: v1
kind: PersistentVolumeClaim
metadata:
  name: e2e-snap-restore-pvc
  namespace: ${NAMESPACE}
  labels:
    btrfs-csi-e2e: "true"
spec:
  accessModes: [ReadWriteOnce]
  storageClassName: ${PRIMARY_STORAGECLASS}
  resources:
    requests:
      storage: 256Mi
  dataSource:
    name: e2e-snap
    kind: VolumeSnapshot
    apiGroup: snapshot.storage.k8s.io
---
apiVersion: v1
kind: Pod
metadata:
  name: e2e-snap-reader
  namespace: ${NAMESPACE}
  labels:
    btrfs-csi-e2e: "true"
spec:
  restartPolicy: Never
  containers:
    - name: reader
      image: busybox
      command: [sh, -c, "grep snapshot-data /data/snap.txt"]
      volumeMounts:
        - name: vol
          mountPath: /data
  volumes:
    - name: vol
      persistentVolumeClaim:
        claimName: e2e-snap-restore-pvc
EOF

wait_for_pvc e2e-snap-restore-pvc
if wait_for_pod_succeeded e2e-snap-reader; then
  pass "Restored data matches original snapshot data"
else
  fail "Data verification after snapshot restore failed"
fi

${KUBECTL} delete pod e2e-snap-reader -n "${NAMESPACE}" --wait=true 2>/dev/null || true
${KUBECTL} delete pvc e2e-snap-restore-pvc e2e-snap-source-pvc -n "${NAMESPACE}" --wait=true 2>/dev/null || true
${KUBECTL} delete volumesnapshot e2e-snap -n "${NAMESPACE}" --wait=true 2>/dev/null || true
pass "Snapshot test resources cleaned up"

# ─── Volume cloning ───────────────────────────────────────────────────────────
log "=== Volume cloning ==="

${KUBECTL} apply -f - <<EOF
apiVersion: v1
kind: PersistentVolumeClaim
metadata:
  name: e2e-clone-source-pvc
  namespace: ${NAMESPACE}
  labels:
    btrfs-csi-e2e: "true"
spec:
  accessModes: [ReadWriteOnce]
  storageClassName: ${PRIMARY_STORAGECLASS}
  resources:
    requests:
      storage: 256Mi
---
apiVersion: v1
kind: Pod
metadata:
  name: e2e-clone-writer
  namespace: ${NAMESPACE}
  labels:
    btrfs-csi-e2e: "true"
spec:
  restartPolicy: Never
  containers:
    - name: writer
      image: busybox
      command: [sh, -c, "echo clone-source-data > /data/clone.txt"]
      volumeMounts:
        - name: vol
          mountPath: /data
  volumes:
    - name: vol
      persistentVolumeClaim:
        claimName: e2e-clone-source-pvc
EOF

wait_for_pvc e2e-clone-source-pvc && wait_for_pod_succeeded e2e-clone-writer
${KUBECTL} delete pod e2e-clone-writer -n "${NAMESPACE}" --wait=true 2>/dev/null || true

${KUBECTL} apply -f - <<EOF
apiVersion: v1
kind: PersistentVolumeClaim
metadata:
  name: e2e-clone-pvc
  namespace: ${NAMESPACE}
  labels:
    btrfs-csi-e2e: "true"
spec:
  accessModes: [ReadWriteOnce]
  storageClassName: ${PRIMARY_STORAGECLASS}
  resources:
    requests:
      storage: 256Mi
  dataSource:
    name: e2e-clone-source-pvc
    kind: PersistentVolumeClaim
---
apiVersion: v1
kind: Pod
metadata:
  name: e2e-clone-verifier
  namespace: ${NAMESPACE}
  labels:
    btrfs-csi-e2e: "true"
spec:
  restartPolicy: Never
  containers:
    - name: verifier
      image: busybox
      command: [sh, -c, "grep clone-source-data /data/clone.txt && echo override > /data/clone.txt"]
      volumeMounts:
        - name: vol
          mountPath: /data
  volumes:
    - name: vol
      persistentVolumeClaim:
        claimName: e2e-clone-pvc
EOF

wait_for_pvc e2e-clone-pvc
if wait_for_pod_succeeded e2e-clone-verifier; then
  pass "Clone contains source data and accepts independent writes"
else
  fail "Clone data verification failed"
fi

# Verify source is unchanged.
${KUBECTL} apply -f - <<EOF
apiVersion: v1
kind: Pod
metadata:
  name: e2e-clone-source-check
  namespace: ${NAMESPACE}
  labels:
    btrfs-csi-e2e: "true"
spec:
  restartPolicy: Never
  containers:
    - name: check
      image: busybox
      command: [sh, -c, "grep clone-source-data /data/clone.txt"]
      volumeMounts:
        - name: vol
          mountPath: /data
  volumes:
    - name: vol
      persistentVolumeClaim:
        claimName: e2e-clone-source-pvc
EOF

if wait_for_pod_succeeded e2e-clone-source-check; then
  pass "Source PVC data unaffected by clone writes"
else
  fail "Source PVC was unexpectedly modified"
fi

${KUBECTL} delete pod e2e-clone-verifier e2e-clone-source-check -n "${NAMESPACE}" --wait=true 2>/dev/null || true
${KUBECTL} delete pvc e2e-clone-pvc e2e-clone-source-pvc -n "${NAMESPACE}" --wait=true 2>/dev/null || true
pass "Clone test resources cleaned up"

# ─── Volume expansion ─────────────────────────────────────────────────────────
log "=== Volume expansion ==="

${KUBECTL} apply -f - <<EOF
apiVersion: v1
kind: PersistentVolumeClaim
metadata:
  name: e2e-expand-pvc
  namespace: ${NAMESPACE}
  labels:
    btrfs-csi-e2e: "true"
spec:
  accessModes: [ReadWriteOnce]
  storageClassName: ${PRIMARY_STORAGECLASS}
  resources:
    requests:
      storage: 100Mi
EOF

wait_for_pvc e2e-expand-pvc
pass "PVC e2e-expand-pvc created at 100Mi"

${KUBECTL} patch pvc e2e-expand-pvc -n "${NAMESPACE}" \
  -p '{"spec":{"resources":{"requests":{"storage":"500Mi"}}}}'

for i in $(seq 1 30); do
  capacity=$(${KUBECTL} get pvc e2e-expand-pvc -n "${NAMESPACE}" \
    -o jsonpath='{.status.capacity.storage}' 2>/dev/null || true)
  if [ "${capacity}" = "500Mi" ]; then
    pass "PVC e2e-expand-pvc expanded to 500Mi"
    break
  fi
  sleep 2
  if [ "${i}" -eq 30 ]; then
    fail "PVC e2e-expand-pvc did not reach 500Mi (got ${capacity})"
  fi
done

${KUBECTL} delete pvc e2e-expand-pvc -n "${NAMESPACE}" --wait=true 2>/dev/null || true

# ─── Quota enforcement ─────────────────────────────────────────────────────────
log "=== Quota enforcement ==="

${KUBECTL} apply -f - <<EOF
apiVersion: v1
kind: PersistentVolumeClaim
metadata:
  name: e2e-quota-pvc
  namespace: ${NAMESPACE}
  labels:
    btrfs-csi-e2e: "true"
spec:
  accessModes: [ReadWriteOnce]
  storageClassName: ${PRIMARY_STORAGECLASS}
  resources:
    requests:
      storage: 50Mi
EOF

wait_for_pvc e2e-quota-pvc
pass "Quota-limited PVC created at 50Mi"

${KUBECTL} apply -f - <<EOF
apiVersion: v1
kind: Pod
metadata:
  name: e2e-quota-writer
  namespace: ${NAMESPACE}
  labels:
    btrfs-csi-e2e: "true"
spec:
  restartPolicy: Never
  containers:
    - name: writer
      image: busybox
      command: [sh, -c, "dd if=/dev/zero of=/data/file bs=1M count=40 && echo 'Write succeeded within limit'"]
      volumeMounts:
        - name: vol
          mountPath: /data
  volumes:
    - name: vol
      persistentVolumeClaim:
        claimName: e2e-quota-pvc
EOF

if wait_for_pod_succeeded e2e-quota-writer; then
  pass "Pod successfully wrote data within quota limit"
else
  fail "Quota limit write test failed"
fi

${KUBECTL} apply -f - <<EOF
apiVersion: v1
kind: Pod
metadata:
  name: e2e-quota-overflow
  namespace: ${NAMESPACE}
  labels:
    btrfs-csi-e2e: "true"
spec:
  restartPolicy: Never
  containers:
    - name: overflow
      image: busybox
      command: [sh, -c, "dd if=/dev/zero of=/data/overflow bs=1M count=100 2>&1 || echo 'Write correctly rejected at quota limit'"]
      volumeMounts:
        - name: vol
          mountPath: /data
  volumes:
    - name: vol
      persistentVolumeClaim:
        claimName: e2e-quota-pvc
EOF

if wait_for_pod_succeeded e2e-quota-overflow; then
  pass "Quota enforcement prevented exceeding limit"
else
  fail "Quota enforcement test failed"
fi

${KUBECTL} delete pod e2e-quota-writer e2e-quota-overflow -n "${NAMESPACE}" --wait=true 2>/dev/null || true
${KUBECTL} delete pvc e2e-quota-pvc -n "${NAMESPACE}" --wait=true 2>/dev/null || true
pass "Quota test resources cleaned up"

# ─── Cross-filesystem snapshot ─────────────────────────────────────────────────
log "=== Cross-filesystem snapshot ==="

# Note: This test assumes the cluster has StorageClasses for different pools/filesystems.
# It may be skipped if multiple pools are not available.
if ! ${KUBECTL} get storageclass "${SECONDARY_STORAGECLASS}" &>/dev/null; then
  log "SKIP: Cross-filesystem snapshot test (${SECONDARY_STORAGECLASS} StorageClass not found)"
else
  ${KUBECTL} apply -f - <<EOF
apiVersion: v1
kind: PersistentVolumeClaim
metadata:
  name: e2e-xfs-snap-source-pvc
  namespace: ${NAMESPACE}
  labels:
    btrfs-csi-e2e: "true"
spec:
  accessModes: [ReadWriteOnce]
  storageClassName: ${SECONDARY_STORAGECLASS}
  resources:
    requests:
      storage: 256Mi
---
apiVersion: v1
kind: Pod
metadata:
  name: e2e-xfs-snap-writer
  namespace: ${NAMESPACE}
  labels:
    btrfs-csi-e2e: "true"
spec:
  restartPolicy: Never
  containers:
    - name: writer
      image: busybox
      command: [sh, -c, "echo cross-filesystem-snap > /data/xfs.txt"]
      volumeMounts:
        - name: vol
          mountPath: /data
  volumes:
    - name: vol
      persistentVolumeClaim:
        claimName: e2e-xfs-snap-source-pvc
EOF

  wait_for_pvc e2e-xfs-snap-source-pvc && wait_for_pod_succeeded e2e-xfs-snap-writer
  ${KUBECTL} delete pod e2e-xfs-snap-writer -n "${NAMESPACE}" --wait=true 2>/dev/null || true

  ${KUBECTL} apply -f - <<EOF
apiVersion: snapshot.storage.k8s.io/v1
kind: VolumeSnapshot
metadata:
  name: e2e-xfs-snap
  namespace: ${NAMESPACE}
  labels:
    btrfs-csi-e2e: "true"
spec:
  volumeSnapshotClassName: btrfs-snapshot
  source:
    persistentVolumeClaimName: e2e-xfs-snap-source-pvc
EOF

  if wait_for_snapshot e2e-xfs-snap; then
    pass "Cross-filesystem snapshot created"
  else
    fail "Cross-filesystem snapshot creation failed"
  fi

  # Restore to different filesystem/pool
  ${KUBECTL} apply -f - <<EOF
apiVersion: v1
kind: PersistentVolumeClaim
metadata:
  name: e2e-xfs-snap-restore-pvc
  namespace: ${NAMESPACE}
  labels:
    btrfs-csi-e2e: "true"
spec:
  accessModes: [ReadWriteOnce]
  storageClassName: ${SECONDARY_STORAGECLASS}
  resources:
    requests:
      storage: 256Mi
  dataSource:
    name: e2e-xfs-snap
    kind: VolumeSnapshot
    apiGroup: snapshot.storage.k8s.io
---
apiVersion: v1
kind: Pod
metadata:
  name: e2e-xfs-snap-reader
  namespace: ${NAMESPACE}
  labels:
    btrfs-csi-e2e: "true"
spec:
  restartPolicy: Never
  containers:
    - name: reader
      image: busybox
      command: [sh, -c, "grep cross-filesystem-snap /data/xfs.txt"]
      volumeMounts:
        - name: vol
          mountPath: /data
  volumes:
    - name: vol
      persistentVolumeClaim:
        claimName: e2e-xfs-snap-restore-pvc
EOF

  wait_for_pvc e2e-xfs-snap-restore-pvc
  if wait_for_pod_succeeded e2e-xfs-snap-reader; then
    pass "Cross-filesystem snapshot restore successful"
  else
    fail "Cross-filesystem snapshot restore failed"
  fi

  ${KUBECTL} delete pod e2e-xfs-snap-reader -n "${NAMESPACE}" --wait=true 2>/dev/null || true
  ${KUBECTL} delete pvc e2e-xfs-snap-restore-pvc e2e-xfs-snap-source-pvc -n "${NAMESPACE}" --wait=true 2>/dev/null || true
  ${KUBECTL} delete volumesnapshot e2e-xfs-snap -n "${NAMESPACE}" --wait=true 2>/dev/null || true
  pass "Cross-filesystem snapshot test resources cleaned up"
fi

# ─── Cross-filesystem clone ───────────────────────────────────────────────────
log "=== Cross-filesystem clone ==="

# Note: This test also requires multiple StorageClasses for different pools.
# It may be skipped if only one pool is available.
if ! ${KUBECTL} get storageclass "${SECONDARY_STORAGECLASS}" &>/dev/null; then
  log "SKIP: Cross-filesystem clone test (${SECONDARY_STORAGECLASS} StorageClass not found)"
else
  ${KUBECTL} apply -f - <<EOF
apiVersion: v1
kind: PersistentVolumeClaim
metadata:
  name: e2e-xfs-clone-source-pvc
  namespace: ${NAMESPACE}
  labels:
    btrfs-csi-e2e: "true"
spec:
  accessModes: [ReadWriteOnce]
  storageClassName: ${PRIMARY_STORAGECLASS}
  resources:
    requests:
      storage: 256Mi
---
apiVersion: v1
kind: Pod
metadata:
  name: e2e-xfs-clone-writer
  namespace: ${NAMESPACE}
  labels:
    btrfs-csi-e2e: "true"
spec:
  restartPolicy: Never
  containers:
    - name: writer
      image: busybox
      command: [sh, -c, "echo cross-filesystem-clone > /data/xfs-clone.txt"]
      volumeMounts:
        - name: vol
          mountPath: /data
  volumes:
    - name: vol
      persistentVolumeClaim:
        claimName: e2e-xfs-clone-source-pvc
EOF

  wait_for_pvc e2e-xfs-clone-source-pvc && wait_for_pod_succeeded e2e-xfs-clone-writer
  ${KUBECTL} delete pod e2e-xfs-clone-writer -n "${NAMESPACE}" --wait=true 2>/dev/null || true

  # Clone to different filesystem/pool
  ${KUBECTL} apply -f - <<EOF
apiVersion: v1
kind: PersistentVolumeClaim
metadata:
  name: e2e-xfs-clone-pvc
  namespace: ${NAMESPACE}
  labels:
    btrfs-csi-e2e: "true"
spec:
  accessModes: [ReadWriteOnce]
  storageClassName: ${SECONDARY_STORAGECLASS}
  resources:
    requests:
      storage: 256Mi
  dataSource:
    name: e2e-xfs-clone-source-pvc
    kind: PersistentVolumeClaim
---
apiVersion: v1
kind: Pod
metadata:
  name: e2e-xfs-clone-reader
  namespace: ${NAMESPACE}
  labels:
    btrfs-csi-e2e: "true"
spec:
  restartPolicy: Never
  containers:
    - name: reader
      image: busybox
      command: [sh, -c, "grep cross-filesystem-clone /data/xfs-clone.txt"]
      volumeMounts:
        - name: vol
          mountPath: /data
  volumes:
    - name: vol
      persistentVolumeClaim:
        claimName: e2e-xfs-clone-pvc
EOF

  wait_for_pvc e2e-xfs-clone-pvc
  if wait_for_pod_succeeded e2e-xfs-clone-reader; then
    pass "Cross-filesystem clone successful"
  else
    fail "Cross-filesystem clone failed"
  fi

  ${KUBECTL} delete pod e2e-xfs-clone-reader -n "${NAMESPACE}" --wait=true 2>/dev/null || true
  ${KUBECTL} delete pvc e2e-xfs-clone-pvc e2e-xfs-clone-source-pvc -n "${NAMESPACE}" --wait=true 2>/dev/null || true
  pass "Cross-filesystem clone test resources cleaned up"
fi

# ─── Summary ──────────────────────────────────────────────────────────────────
echo ""
echo "=== E2E Results: ${PASS} passed, ${FAIL} failed ==="
[ "${FAIL}" -eq 0 ]
