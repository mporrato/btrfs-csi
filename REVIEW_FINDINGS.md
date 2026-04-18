# btrfs-csi Code Review Findings

**Date**: 2026-04-16
**Reviewer**: Code Review
**Scope**: Full codebase review — pkg/, cmd/, deploy/, scripts/, Makefile, go.mod
**Go Version**: 1.26

---

## Summary

The implementation correctly addresses the stated goal — a single-node Kubernetes CSI driver for btrfs with subvolume management, snapshots, qgroup-based quota enforcement, and copy-on-write. The architecture is sound, the test coverage is comprehensive, and the code follows Go best practices in most areas.

This review identifies **3 critical issues**, **6 bugs/edge cases**, **3 security concerns** (2 acknowledged as inherent to the model), **5 quality/maintainability gaps**, and **4 missing capabilities** that should be addressed before or after production use.

---

## Critical Issues

> **Must fix before production. These represent resource leaks, hangs, or incorrect behavior under failure conditions.**

### [x] C-1: WatchPools goroutine leak when driver fails to start

**File**: `cmd/btrfs-csi-driver/main.go:90–114`

**Description**: `runWithContext` spawns a pool-watcher goroutine (`WatchPools`) that exits only when its `stop` channel is closed. The select at lines 101–114 closes `configStop` in the `ctx.Done()` branch but not in the `errCh` branch. If `drv.Run()` fails (e.g., listener error, bind failure), `runWithContext` returns without closing `configStop`, and the `WatchPools` goroutine polls `DiscoverPools` forever.

In production the process exits on error, so the OS reclaims the goroutine. The bug matters for tests that call `runWithContext` directly and for any future code that wraps `run`/`runWithContext` as a library function — leaked goroutines accumulate across invocations.

**Root cause**: `close(configStop)` lives inside the `ctx.Done()` branch instead of running unconditionally on exit.

```go
select {
case <-ctx.Done():
    close(configStop)   // closed here only
    drv.Stop()
    if err := <-errCh; err != nil { ... }
case err := <-errCh:
    // close(configStop) is missing on this path
    return fmt.Errorf("driver failed: %w", err)
}
```

**Remediation**: Use `defer close(configStop)` right after `WatchPools` returns the channel, or close it explicitly in both branches:

```go
configStop := driver.WatchPools(...)
defer close(configStop)
```

A `defer` is simpler and guarantees the channel is closed on every return path (including future error paths).

**Status**: Fixed

---

### [x] C-2: No timeout on btrfs CLI commands

**File**: `pkg/btrfs/real.go:27–37` (`runCommand`)

**Description**: All `btrfs` subcommands execute via `exec.Command(...).Run()` with no context or timeout. A hung filesystem, stuck `btrfs qgroup show` on a large qgroup tree, or a kernel issue blocks the calling RPC goroutine indefinitely. The CSI handler never returns, kubelet retries build up stuck operations, and the driver can only be recovered by restart.

This is particularly dangerous because `Manager` methods are called synchronously from every controller and node RPC. One hung `btrfs` command → one wedged gRPC worker → eventually the server's goroutine pool fills.

**Remediation**: Thread `context.Context` through the `Manager` interface and use `exec.CommandContext`. Drive the timeout from the CSI RPC context (kubelet already applies per-RPC deadlines) rather than a hardcoded value, so timeouts are consistent with the CSI gRPC deadline:

```go
func runCommand(ctx context.Context, name string, args ...string) (string, error) {
    cmd := exec.CommandContext(ctx, name, args...)
    // ...
}
```

Every `Manager` method (`CreateSubvolume`, `GetQgroupUsage`, etc.) should take a `context.Context` first parameter and pass it through. Update `MockManager` and all call sites. For callers without a natural context (e.g., scheduled qgroup cleanup), create one with a generous bounded timeout (e.g., 2 minutes).

**Status**: Fixed

---

### [x] C-3: quotaEnabled cache not invalidated on pool reload

**File**: `pkg/driver/driver.go:91–95` (`SetPools`), `pkg/driver/driver.go:244–258` (`ensureQuotaEnabled`)

**Description**: `ensureQuotaEnabled` caches `basePath → true` in `d.quotaEnabled` so repeated volume creations don't re-invoke `btrfs quota enable`. When pools are reloaded via `SetPools` (e.g., a pool directory is remounted or replaced), the cache is not cleared. If an operator removes a pool, reformats the underlying filesystem (which resets quota state), and re-adds it at the same path, the driver skips `EnsureQuotaEnabled` and the next `SetQgroupLimit` call fails with "quotas not enabled".

**Remediation**: Clear the cache whenever the pool map is replaced. Hold both locks in a consistent order to avoid future lock-ordering hazards:

```go
func (d *Driver) SetPools(pools map[string]string) {
    d.poolsMu.Lock()
    defer d.poolsMu.Unlock()
    d.pools = pools

    d.quotaEnabledMu.Lock()
    d.quotaEnabled = make(map[string]bool)
    d.quotaEnabledMu.Unlock()
}
```

Add a regression test that calls `ensureQuotaEnabled`, then `SetPools`, then verifies the next `ensureQuotaEnabled` call re-invokes the manager.

**Status**: Fixed

---

## Bugs & Edge Cases

> **Should fix. These cause incorrect behavior under specific conditions.**

### [ ] B-1: ValidateVolumeCapabilities does not reject block access type

**File**: `pkg/driver/controller.go:186–208`

**Description**: `ValidateVolumeCapabilities` only checks access modes via `isSupportedCapabilities` and ignores the access type. A caller that supplies a `Block` capability receives a "confirmed" response, implying the driver supports block volumes. `validateCreateVolumeCapabilities` (line 248–253) correctly rejects block access, so a subsequent `CreateVolume` with the same capability fails — a contradiction that violates the CSI spec's consistency expectation.

**Remediation**: Share one validation helper between the two call sites so the rules cannot drift. Extract the checks into a helper that returns a boolean (for `ValidateVolumeCapabilities`, which must not error on unsupported caps) and a companion that returns an error (for `CreateVolume`):

```go
// isConfirmableCapability returns true if the driver can serve this capability.
func isConfirmableCapability(c *csi.VolumeCapability) bool {
    if c.GetBlock() != nil {
        return false
    }
    if am := c.GetAccessMode(); am != nil {
        if _, ok := supportedAccessModes[am.Mode]; !ok {
            return false
        }
    }
    return true
}
```

Then in `ValidateVolumeCapabilities`:

```go
for _, c := range req.VolumeCapabilities {
    if !isConfirmableCapability(c) {
        return &csi.ValidateVolumeCapabilitiesResponse{}, nil
    }
}
```

**Status**: Open

---

### [ ] B-2: Version string hardcoded in two places

**Files**:
- `pkg/driver/driver.go:24` — `version = "0.1.0"`
- `cmd/btrfs-csi-driver/main.go:54` — `fmt.Println("btrfs-csi-driver version 0.1.0")`

**Description**: The same literal version string appears in two unrelated files. A release that bumps one and forgets the other produces a driver where `--version` and `GetPluginInfo` disagree — a debugging nightmare when correlating logs with releases.

**Remediation**: Define the version once as an exported package variable and inject at build time. The `const` in `driver.go` becomes:

```go
// Version is the reported driver version. Overridden at build time via -ldflags.
var Version = "0.1.0"
```

Update `main.go` to print `driver.Version`. In the Makefile:

```make
VERSION ?= $(shell git describe --tags --always --dirty)
LDFLAGS := -X github.com/mporrato/btrfs-csi/pkg/driver.Version=$(VERSION)
go build -ldflags "$(LDFLAGS)" ...
```

This also fixes the "dirty" version reporting so built binaries carry their git provenance.

**Status**: Open

---

### [x] B-3: doSendReceive does not reap killed receive process

**File**: `pkg/btrfs/real.go:149–151`

**Description**: When `sendCmd.Run()` fails, the code calls `receiveCmd.Process.Kill()` but never calls `receiveCmd.Wait()`. The kernel keeps the child's exit status until the parent reaps it, so the killed `btrfs receive` lingers as a zombie until the driver process exits. The associated stdin pipe also remains half-open.

Under repeated failures (e.g., a corrupt source subvolume retried by the external-snapshotter), zombie processes and FDs accumulate.

**Remediation**: Reap the process after killing it, and move this into a helper so the cleanup path is uniform:

```go
if err := sendCmd.Run(); err != nil {
    _ = receiveCmd.Process.Kill()
    _ = receiveCmd.Wait() // reap the killed child
    return fmt.Errorf("btrfs send %s: %w: %s", tempSnap, err, sendStderr.String())
}
```

Additionally, close `sendStdout` on the error path to unblock any in-flight writes — `exec` closes it when `sendCmd.Run()` returns, but being explicit documents the intent.

**Status**: Fixed (addressed as part of C-2)

---

### [ ] B-4: Race window in reloadPoolConfig

**File**: `cmd/btrfs-csi-driver/main.go:179–180`

**Description**: `reloadPoolConfig` updates two independent structures without a common lock:

```go
ms.ReloadPaths(validPaths)
drv.SetPools(validPools)
```

A concurrent CSI request that arrives between these calls sees an inconsistent view: the store may already know (or no longer know) a path before the driver's pool map reflects the change, or vice versa. The request fails with either `InvalidArgument "unknown storage pool"` or an internal store lookup error.

Note: simply swapping the call order does **not** eliminate the race — it only changes which specific error the client sees. In either ordering, there is a window where the two structures disagree.

**Remediation**: The correct fix is to make the update atomic from the perspective of RPC handlers. Two options:

1. **Introduce a reconfiguration lock** held by both `reloadPoolConfig` and any RPC path that resolves pool → path. This serializes resolution against reconfiguration.

2. **Make resolution tolerant of transient misses.** Treat an unresolved pool-or-path during a reload as a retryable condition and return `codes.Unavailable` with a short retry hint, so the external-provisioner retries cleanly.

Option 1 is simpler if reloads are rare (they are — pool changes are operator-driven). Option 2 is friendlier to long-running RPCs but requires careful choice of gRPC code (CSI spec permits `Unavailable` as a "please retry" signal).

A pragmatic middle ground: move both calls behind a single method on the driver that takes a lock shared with `resolveBasePath` and the store's path lookup:

```go
func (d *Driver) ApplyPoolConfig(pools map[string]string, paths []string, ms state.Store) {
    d.configMu.Lock()
    defer d.configMu.Unlock()
    ms.ReloadPaths(paths)
    d.pools = pools
}
```

Swapping the order alone, as originally suggested, is not sufficient.

**Status**: Open

---

### [ ] B-5: NodeExpandVolume lacks path validation

**File**: `pkg/driver/node.go:350–368`

**Description**: Other node-side RPCs that accept a filesystem path (`NodePublishVolume`, `NodeUnpublishVolume`, `NodeGetVolumeStats`) call `validatePathInKubeletDir` to reject paths outside the configured kubelet directory. `NodeExpandVolume` does not.

Currently `NodeExpandVolume` is a no-op (returns the stored capacity without touching the filesystem), so there is no exploit today. But the precedent matters: anyone extending this method to perform filesystem-level expansion (e.g., calling `resize2fs` or any path-based operation) would inherit a path-traversal vulnerability, because the function signals "path validation is not needed here" by omission.

**Remediation**: Add the validation for consistency with sibling methods, even though the code path is currently inert:

```go
if err := d.validatePathInKubeletDir(req.GetVolumePath()); err != nil {
    return nil, err
}
```

This also ensures fuzz coverage on `validatePathInKubeletDir` exercises this call site.

**Status**: Open

---

### [ ] B-6: Misleading slice initialization in ListSnapshots filter

**File**: `pkg/driver/controller.go:124`

**Description**: `filtered := all[:0:0]` uses the three-index slice expression to create a zero-length, zero-capacity slice that shares no backing array with `all`. The code is correct, but the `all[:0:0]` spelling is a notorious Go footgun — it looks identical to `all[:0]` (which *does* share the backing array and can silently mutate `all` on append). Reviewers must squint at the `:0:0` vs `:0` to see the difference.

**Remediation**: Use an explicit `make` that states intent and costs nothing:

```go
filtered := make([]*state.Snapshot, 0, len(all))
```

This is clearer to readers, preserves the original reason for the trick (avoid aliasing), and lets the compiler infer the same allocation behavior.

**Status**: Open

---

## Security Concerns

> **Design-level observations. Some are inherent to the CSI model; others should be addressed.**

### [ ] S-1: Privileged container with ineffective capability drop

**File**: `deploy/base/plugin.yaml:40–45`

**Description**: The driver pod runs as `privileged: true`. This is standard for CSI drivers needing `mountPropagation: Bidirectional`, but privileged mode grants **all** Linux capabilities and disables seccomp/AppArmor confinement. The manifest also specifies `capabilities.drop: ["ALL"]`, which is a **no-op under `privileged: true`** — privileged mode overrides capability constraints, so the drop provides a false sense of hardening. This misleads operators reviewing the manifest.

The stated remediation of switching to `SYS_ADMIN` alone is an investigation, not a drop-in fix: btrfs subvolume/snapshot operations and mount propagation may need additional capabilities (`SYS_MODULE` for auto-loading btrfs on some distros, `MKNOD` for device handling in `/dev/btrfs-control`, etc.). Empirical testing on the target kernel is required.

**Remediation**:

1. **Remove the misleading `drop: ["ALL"]`** under `privileged: true`. Replace it with a comment that documents why privileged is needed:
   ```yaml
   # privileged is required for mountPropagation: Bidirectional and btrfs ioctls.
   # Under privileged:true, capabilities and seccomp/AppArmor are effectively unrestricted.
   securityContext:
     privileged: true
   ```

2. **Investigate a non-privileged alternative** in a follow-up. Start from `privileged: false` + `capabilities.add: ["SYS_ADMIN"]` and add capabilities until mount/ioctl operations succeed. Document the minimal set in the README.

3. **Harden the surrounding surface** since privileged access is unavoidable: pin the image by digest, use a distroless base, and document the trust boundary in the README (a compromise of this pod equals root on the node).

**Status**: Open

---

### [ ] S-2: State file is human-readable JSON on disk

**File**: `pkg/state/state.go:251` (`json.MarshalIndent`)

**Description**: The state file (`<poolPath>/state.json`) contains PVC names harvested from `Volume.Name`, which may reveal application structure (e.g., `stripe-payments-db`) to an attacker with read access to the pool directory. File mode is `0o600` and the directory is `0o700`, so access requires root on the host — an attacker with that access already has complete control regardless of file contents.

**Risk**: Low. Contents are the same information that `kubectl get pvc` exposes to any namespace reader. No secrets, tokens, or cryptographic material are stored.

**Remediation**: No action required. If an operator is concerned about disk forensics, consider switching `MarshalIndent` to compact `Marshal`:

```go
raw, err := json.Marshal(fs.data)
```

This reduces the readability of casual inspection (and saves a few bytes per write) but does not add meaningful security.

**Status**: Acknowledged — no action required

---

### [ ] S-3: No gRPC authentication on Unix socket

**File**: `pkg/driver/driver.go:184–194`

**Description**: The gRPC server accepts any client that can connect to the Unix socket. Access control is delegated entirely to the socket directory's `0o700` permissions (set at `cmd/btrfs-csi-driver/main.go:69`). There is no mTLS, token, or peer-credential check.

**Risk**: Low for the intended single-node model. The kubelet plugin registration path mounts the socket directory with restrictive permissions, so only root processes on the node can connect — and a root attacker has already compromised the node.

**Remediation**: No action required for the current scope. If the driver is ever deployed in a multi-tenant environment or exposed via a forwarded socket, add a peer-credential check with `SO_PEERCRED` to enforce UID-based access control at connection time.

**Status**: Acknowledged — no action required

---

## Quality & Maintainability

> **Technical debt and polish. These improve long-term maintainability and debuggability.**

### [ ] Q-1: No graceful shutdown timeout

**File**: `pkg/driver/driver.go:261–273` (`Stop`)

**Description**: `Stop()` calls `d.grpcServer.GracefulStop()` which blocks until all in-flight RPCs complete. If any RPC is stuck on a hung `btrfs` command (see C-2), the driver never exits. Kubernetes eventually `SIGKILL`s the container after the termination grace period, but the final state (logs, open files, mounts) is truncated.

**Remediation**: Bound `GracefulStop` with a timeout, then fall back to the hard `Stop()`. Log which path was taken so operators can diagnose stuck shutdowns:

```go
done := make(chan struct{})
go func() { d.grpcServer.GracefulStop(); close(done) }()
select {
case <-done:
    klog.InfoS("gRPC server stopped gracefully")
case <-time.After(30 * time.Second):
    klog.InfoS("Graceful stop timed out, forcing stop")
    d.grpcServer.Stop()
}
```

30 s leaves headroom below the default 60 s `terminationGracePeriodSeconds`. Make the timeout configurable if the driver is deployed with a non-default grace period. This remediation is complementary to C-2: once RPCs carry a context, most stuck calls will unblock on context cancellation and the timeout becomes a safety net.

**Status**: Open

---

### [ ] Q-2: No gRPC interceptors for logging or metrics

**File**: `pkg/driver/driver.go:191–194`

**Description**: The gRPC server is constructed with message size limits but no interceptors. This means:
- No structured log entry per RPC (request, caller, duration, error).
- No Prometheus metrics for request count, latency, or error rate.
- No central place to apply per-RPC rate limiting or deadline enforcement.

In production, all driver observability comes from ad-hoc `klog.V(4)` calls scattered through handlers, which are insufficient for diagnosing slow or failing CSI operations.

**Remediation**: Add a unary interceptor that logs request method, duration, and error code:

```go
func (d *Driver) logInterceptor(ctx context.Context, req any,
    info *grpc.UnaryServerInfo, handler grpc.UnaryHandler) (any, error) {
    start := time.Now()
    resp, err := handler(ctx, req)
    klog.V(4).InfoS("rpc",
        "method", info.FullMethod,
        "duration", time.Since(start),
        "code", status.Code(err))
    return resp, err
}

grpcServer := grpc.NewServer(
    grpc.MaxRecvMsgSize(maxMsgSize),
    grpc.MaxSendMsgSize(maxMsgSize),
    grpc.UnaryInterceptor(d.logInterceptor),
)
```

For metrics, consider `grpc-ecosystem/go-grpc-middleware` — its `prometheus` interceptor produces the standard CSI metrics consumed by kube-state-metrics. This is a clean place to also emit request latency histograms.

**Status**: Open

---

### [ ] Q-3: No state-on-disk reconciliation

**File**: *Driver-wide*

**Description**: The state store is the authoritative source of truth for volume and snapshot metadata, but nothing verifies that state entries correspond to real subvolumes on disk. If an operator manually deletes a subvolume (e.g., via `btrfs subvolume delete` during debugging) or a disk corruption removes one, the state file retains a ghost entry. `ControllerGetVolume` reports `VolumeCondition.Abnormal=true` when an orphan is observed, but nothing heals it — volume lifecycle operations on the ghost will fail repeatedly.

**Remediation**: Add a periodic reconciliation loop, analogous to the existing qgroup cleanup scheduler:

1. Every N minutes (configurable, default 1 hour), iterate over every volume and snapshot in the store.
2. For each entry, check whether the corresponding subvolume exists via `SubvolumeExists`.
3. If missing, emit a WARN log with the volume ID and source pool, and emit a Kubernetes Event (via the node publisher or a dedicated recorder) so operators are notified.
4. **Do not auto-delete** state entries by default — silent deletion could mask real bugs. Offer an opt-in `--reconcile-mode=delete` flag for environments where self-healing is preferred.

Document the reconciliation loop and its default behavior in the README.

**Status**: Open

---

### [ ] Q-4: Makefile deploy uses fragile sed for kubelet path replacement

**File**: `Makefile:43–50`

**Description**: The `deploy` target replaces `/var/lib/kubelet` with `$(KUBELET_DIR)` via a pipeline:

```make
kubectl kustomize deploy/overlays/$(OVERLAY)/ | \
    sed 's|/var/lib/kubelet|$(KUBELET_DIR)|g' | \
    kubectl apply -f -
```

This replaces every occurrence globally, including any string in comments, labels, annotations, env values, or unrelated volume mounts. If future manifests reference `/var/lib/kubelet` in a context that must not be rewritten (for example, an annotation documenting the default), the rewrite silently breaks the manifest. Sed also has no awareness of YAML escaping — a path containing special regex characters would corrupt output.

**Remediation**: Let kustomize do the substitution. Declare the kubelet path as a variable via `configMapGenerator` + `replacements`, or parameterize via a small `patches` overlay that rewrites only the specific fields (`hostPath.path`, `volumeMounts[*].mountPath`, `args`) that legitimately contain the kubelet path. Example with `replacements`:

```yaml
# deploy/overlays/default/kustomization.yaml
replacements:
  - source:
      kind: ConfigMap
      name: btrfs-csi-params
      fieldPath: data.kubeletDir
    targets:
      - select: { kind: DaemonSet, name: btrfs-csi-node }
        fieldPaths:
          - spec.template.spec.volumes.[name=plugin-dir].hostPath.path
          - spec.template.spec.volumes.[name=pods-mount-dir].hostPath.path
          # ... explicit paths only
```

Invoke with `make deploy KUBELET_DIR=/var/lib/k0s/kubelet` as today, but have the Makefile render the parameter via `kustomize edit` instead of piping through `sed`.

**Status**: Open

---

### [ ] Q-5: Test utilities use hardcoded /tmp path

**File**: `pkg/driver/testutil_test.go:10`

**Description**: `const testRootPath = "/tmp/btrfs-csi-test"` is shared by every test helper (`newTestDriver`, `newTestDriverWithMock`, `newTestDriverWithMounter`). Tests never touch the real filesystem today (manager is mocked), so the path is cosmetic — but it still appears as `vol.BasePath` in state, making test output identical across isolated `go test -run ...` invocations and invisible to test sandboxing. Any future test that **does** exercise real filesystem code would contend on the same directory if run in parallel.

**Remediation**: Refactor the test helpers to accept `*testing.T` and use `t.TempDir()`, which Go cleans up automatically and isolates per test:

```go
func newTestDriver(t *testing.T) *Driver {
    t.Helper()
    root := t.TempDir()
    ms, _ := newTestMultiStore(root)
    d, err := NewDriver(&btrfs.MockManager{}, ms, "test-node")
    if err != nil {
        t.Fatalf("NewDriver: %v", err)
    }
    d.SetPools(map[string]string{"default": root})
    return d
}
```

This ripples to every caller of `newTestDriver()`, `newTestDriverWithMock()`, and `newTestDriverWithMounter()` — a mechanical change but broad. Do it in one pass so tests stay green throughout.

**Status**: Open

---

## Gaps

> **Missing capabilities or documentation. These are not bugs but represent areas where the driver could be improved.**

### [ ] G-1: Unknown StorageClass parameters are silently accepted

**File**: `pkg/driver/controller.go:664–693` (`resolveBasePath`)

**Description**: `resolveBasePath` reads only the `pool` parameter from `req.Parameters`. Any other key — `compression`, `subvolGroup`, or a typo like `Pool` — is silently ignored. Users who intend to pass tuning parameters get no feedback that the parameter was not understood, and the misconfiguration may go unnoticed until a production incident exposes the unset behavior.

The CSI spec does not require drivers to reject unknown parameters, but strict rejection is standard practice in CSI drivers (AWS EBS, GCE PD, Ceph RBD all do this) and catches configuration errors at `CreateVolume` time rather than run time.

**Remediation**: Maintain a single set of known parameter names and validate against it:

```go
var knownStorageClassParams = map[string]struct{}{
    "pool": {},
    // Add future parameters here.
}

func validateStorageClassParams(params map[string]string) error {
    for key := range params {
        // CSI reserved prefixes (csi.storage.k8s.io/*) come from the
        // external-provisioner and should be accepted.
        if strings.HasPrefix(key, "csi.storage.k8s.io/") {
            continue
        }
        if _, ok := knownStorageClassParams[key]; !ok {
            return status.Errorf(codes.InvalidArgument,
                "unknown StorageClass parameter %q", key)
        }
    }
    return nil
}
```

Call it from `CreateVolume`, `CreateSnapshot`, and `GetCapacity` before `resolveBasePath`. Note the special-case for CSI-reserved prefixes — the external-provisioner adds `csi.storage.k8s.io/pvc/name`, `.../pvc/namespace`, etc., which must not be rejected.

**Status**: Open

---

### [ ] G-2: Volume stats misleading without per-volume quota

**File**: `pkg/driver/node.go:314–328`

**Description**: `NodeGetVolumeStats` reports `Total` and `Available` from the btrfs qgroup when a limit is set. When no limit is set (volume created with `capacityBytes: 0`), it falls back to **filesystem-level** totals. All unquoted volumes in a pool therefore report identical stats — the pool's total and available bytes. Monitoring dashboards show the same gauge for every unquoted PVC, which is unhelpful at best and misleading at worst (an alert like "volume >90% full" fires simultaneously on every unquoted volume).

**Remediation**: Two complementary steps:

1. **Document the behavior.** Add a section to the README explaining that volumes created without an explicit capacity do not get per-volume accounting, and suggest always specifying `capacityBytes` in PVC specs.

2. **Offer an optional default limit.** Add a driver flag `--default-volume-capacity` (or a StorageClass parameter, once G-1 is in place) that, when set, applies a qgroup limit equal to the specified size for otherwise-unconstrained volumes. This gives operators a way to opt into correct per-volume accounting without hardcoding a size in every PVC.

A third option — setting the filesystem total as the limit — sounds appealing but makes true "share the pool" semantics impossible to restore once a limit is in place, so treat it as an opt-in rather than a default.

**Status**: Open

---

### [ ] G-3: Snapshot SizeBytes may be inaccurate immediately after creation

**File**: `pkg/driver/controller.go:537–540`

**Description**: `GetQgroupUsage` is called immediately after `CreateSnapshot` to populate `SizeBytes`. Btrfs qgroup accounting is maintained asynchronously — the `rfer` value for a just-created subvolume is updated by a background kernel thread and may read as `0` or stale for up to a second on busy filesystems. CSI consumers that rely on `SizeBytes` for capacity accounting (e.g., for restore-size calculations) see a too-small value.

**Remediation**: Document the behavior in the README as a known limitation, since the CSI spec allows `SizeBytes=0` to mean "unknown". Optionally add a best-effort retry:

```go
// GetQgroupUsage is best-effort and may lag for newly-created subvolumes.
// Retry once after a short delay to let kernel accounting settle.
if usage, err := d.manager.GetQgroupUsage(snap.Path()); err == nil {
    if usage.Referenced == 0 {
        time.Sleep(500 * time.Millisecond)
        usage, _ = d.manager.GetQgroupUsage(snap.Path())
    }
    if usage != nil {
        snap.SizeBytes = int64(usage.Referenced)
    }
}
```

Gate the sleep behind a flag so tests don't slow down. A cleaner long-term fix is to expose `SizeBytes=0` with a note in the driver docs and let consumers query it later via `ListSnapshots`.

**Status**: Open

---

### [ ] G-4: No integration test coverage for cross-filesystem send/receive

**File**: `pkg/btrfs/real_test.go:13–37`

**Description**: `sendReceive` is the most complex path in the btrfs layer: it spawns two processes, pipes stderr, creates and cleans up a temp snapshot, handles cross-filesystem snapshots, and performs rename + permission adjustment. The only direct test (`TestSendReceive_IdempotencyCheck`) is immediately `t.Skip`-ed because it needs a real btrfs setup. Regressions in this path would slip past CI entirely.

**Remediation**: Add integration tests gated by `//go:build integration` that set up real btrfs loop devices and exercise the full path:

1. Helper (in `pkg/btrfs/integration_test.go`) that creates a tempdir, formats two 100 MiB loop files as btrfs, mounts them, and registers `t.Cleanup` to unmount and remove them.
2. Test `TestSendReceive_CrossFilesystem`: create a subvolume with content in FS A, call `sendReceive` to FS B, verify the destination exists and content matches.
3. Test `TestSendReceive_CleansUpTempSnapshot`: verify no `.btrfs-csi-send-*` subvolume remains in the source directory after success.
4. Test `TestSendReceive_CleansUpOnReceiveFailure`: corrupt the stream midway (e.g., close the pipe) and verify both the temp snapshot and partial destination are cleaned up.
5. Test `TestSendReceive_Idempotent`: run twice with the same destination; second call should return success without error.

The existing `scripts/` runner already has patterns for loopback btrfs setup that can be reused. Add a Makefile target `test-integration` that runs `go test -tags=integration ./...` and documents in the README that it requires root and loopback support.

**Status**: Open

---

## Remediation checklist

### Critical Issues (Must Fix)

- [x] **C-1**: Use `defer close(configStop)` after `WatchPools` so the goroutine is stopped on every return path in `runWithContext`
- [x] **C-2**: Thread `context.Context` through `btrfs.Manager` and use `exec.CommandContext` in `runCommand`
- [x] **C-3**: Invalidate `quotaEnabled` in `SetPools`; add regression test

### Bugs & Edge Cases

- [ ] **B-1**: Extract a shared `isConfirmableCapability` helper; call from both `ValidateVolumeCapabilities` and `validateCreateVolumeCapabilities`
- [ ] **B-2**: Move version to a single exported `driver.Version` variable; inject via `-ldflags` at build time; use `git describe` for version string
- [x] **B-3**: Call `receiveCmd.Wait()` after `receiveCmd.Process.Kill()` in `doSendReceive` to reap the child
- [ ] **B-4**: Introduce a shared reconfiguration lock so `ReloadPaths` and `SetPools` are atomic from the RPC handler's perspective (simply swapping order is not sufficient)
- [ ] **B-5**: Add `validatePathInKubeletDir(req.GetVolumePath())` to `NodeExpandVolume` for consistency with other node RPCs
- [ ] **B-6**: Replace `all[:0:0]` with `make([]*state.Snapshot, 0, len(all))` in `ListSnapshots`

### Security Concerns

- [ ] **S-1**: Remove the misleading `drop: ["ALL"]` under `privileged: true`; document why privileged is required; investigate minimal capability set as a follow-up
- [ ] **S-2**: (Acknowledged — no action) Optionally switch to compact JSON
- [ ] **S-3**: (Acknowledged — no action) Consider `SO_PEERCRED` if the deployment model changes

### Quality & Maintainability

- [ ] **Q-1**: Wrap `GracefulStop` with a 30 s timeout that falls back to hard `Stop()` and logs which path was taken
- [ ] **Q-2**: Add a unary logging interceptor recording method, duration, and error code; plan for Prometheus interceptor next
- [ ] **Q-3**: Add opt-in periodic reconciliation of state against on-disk subvolumes; default to log-only, not auto-delete
- [ ] **Q-4**: Replace `sed` in Makefile `deploy` target with kustomize `replacements` targeting specific field paths
- [ ] **Q-5**: Refactor `newTestDriver*` helpers to accept `*testing.T` and use `t.TempDir()`; update all callers

### Gaps

- [ ] **G-1**: Reject unknown StorageClass parameters via a `knownStorageClassParams` set, allowing the `csi.storage.k8s.io/*` prefix
- [ ] **G-2**: Document fallback behavior in the README; add optional `--default-volume-capacity` flag
- [ ] **G-3**: Document qgroup accounting delay; optionally retry `GetQgroupUsage` once after 500 ms when result is 0
- [ ] **G-4**: Add `//go:build integration` tests exercising the full `sendReceive` path on real btrfs loop devices; add `make test-integration`
