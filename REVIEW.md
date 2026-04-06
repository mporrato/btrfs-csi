# Project Review: btrfs-csi

Review date: 2026-04-06

## 1. Bugs

### Data race in MockManager / qgroup cleanup tests (confirmed by `-race`)

`driver_test.go:107-111` reads `mock.ClearStaleQgroupsCalls` from the test
goroutine while the timer goroutine in `scheduleQgroupCleanup` writes to it via
`mock.ClearStaleQgroups()` (`mock.go:112`). The same race affects
`TestScheduleStartupQgroupCleanups_Staggered` at `driver_test.go:66` where
`callLog` is read without the mutex. The production code in
`scheduleQgroupCleanup` (`driver.go:191`) fires `ClearStaleQgroups` in a
`time.AfterFunc` callback, so any unsynchronized mock field is a race.

**Fix**: `TestScheduleQgroupCleanup_OnlyTargetsSpecifiedPath` needs a mutex
around its reads of `mock.ClearStaleQgroupsCalls`, or the mock itself needs
synchronization. In `TestScheduleStartupQgroupCleanups_Staggered`, line 66
reads `callLog[0].path` outside the mutex.

### TOCTOU in stale socket removal

`driver.go:144`: Between the `Lstat` check and the `os.Remove`, the file could
be replaced with a symlink. In practice this is mitigated by the directory
having `0o700` permissions, but it is a theoretical TOCTOU window.

### `watchPoolConfig` fires reload redundantly on first tick

`config.go:75-79`: On the first tick, `lastPools` is `nil`, so
`poolsEqual(pools, nil)` is false whenever there are pools. This means the
reload callback fires immediately on the first tick even though
`initializeStores` already loaded the config. The driver calls
`reloadPoolConfig` redundantly ~30s after startup. Not harmful but wastes work
(re-validates all pools).

### `NodePublishVolume` idempotency check doesn't verify the source

`node.go:98-105`: If the target is already a mount point, the method returns
success without verifying it's the *same volume* mounted there. If a different
volume was mounted at that path (e.g., stale state), the driver silently returns
success. CSI spec recommends verifying the mount matches.

## 2. Security Issues

### Command injection surface in `runCommand`

`real.go:29`: While `exec.Command` properly separates arguments (no shell
expansion), all paths passed to btrfs commands come from volume IDs (UUIDs) and
base paths (from config). The config paths are validated as absolute but not
sanitized for special characters. This is currently safe because paths are
UUID-based, but if a future change introduces user-controlled path components,
this could be exploitable. Consider adding path validation in the `Manager`
methods.

### `privileged: true` in DaemonSet (won't fix)

`plugin.yaml`: The CSI driver container runs as `privileged: true`. While best
practice is to use specific capabilities, Kubernetes requires `privileged: true`
for `mountPropagation: Bidirectional`, which this driver needs to make bind
mounts visible to kubelet. This is a kubelet-enforced requirement and cannot be
replaced with `SYS_ADMIN` alone.

### State file permissions

`state.go:225`: Temp files created by `os.CreateTemp` use the default umask. The
final state file permissions depend on the process umask. Explicitly set `0o600`
on the temp file before writing to ensure the state file isn't world-readable.
Use `tmpFile.Chmod(0o600)` or `os.OpenFile` with explicit perms.

### `hostNetwork: true` in DaemonSet

`plugin.yaml`: `hostNetwork: true` is unnecessary for a CSI driver that
communicates only via Unix socket. This exposes the pod to the host network
stack unnecessarily.

## 3. Performance Concerns

### Linear scan for `GetVolumeByName` / `GetSnapshotByName`

`state.go:263-274`, `state.go:316-327`: These iterate all volumes/snapshots on
every call. Called on every `CreateVolume` and `CreateSnapshot` for idempotency
checks. With many volumes this becomes O(n). Consider adding a `name -> id`
index map.

### `MultiStore` read methods hold `RLock` across all sub-store iterations

`state.go:460-469`: `GetVolume` acquires `ms.mu.RLock()` and then calls
`s.GetVolume()` on each sub-store, which in turn acquires `fs.mu.Lock()`. This
creates a lock hierarchy but also means every read blocks on all stores
serially. With many pools, reads are serialized.

### `ListVolumes` doesn't support pagination

`controller.go:68-78`: `ListVolumes` returns all volumes at once. The CSI spec
recommends pagination via `max_entries`/`starting_token`. `ListSnapshots` has
it, but `ListVolumes` doesn't. With many volumes, this could be a large
response.

### No `fsync` on state directory after rename (won't fix)

`state.go:239`: The atomic write pattern does `Write` -> `Close` -> `Rename`
but never calls `fsync` on the directory. On most filesystems this would warrant
a directory fsync for crash durability. However, on btrfs, directory fsync
triggers a **full transaction commit** that flushes ALL dirty data across the
entire filesystem. Since the state file shares a pool with volume data, this
would cause every state write to sync all in-flight workload I/O. The file
data is fsynced before rename, and CSI operations are idempotent, so the worst
case on crash is replaying an operation — acceptable for this driver.

### Quota enable on every `CreateVolume` with capacity

`controller.go:251`: `EnsureQuotaEnabled` runs a `btrfs quota enable` command
on every volume creation with capacity. This shells out twice (try `--simple`,
fallback). Consider caching the enabled state per-basePath.

## 4. Simplification Opportunities

### Redundant exported/unexported wrapper pairs in `config.go`

`ParsePoolConfig` wraps `parsePoolConfig`, `WatchPoolConfig` wraps
`watchPoolConfig` with identical signatures. Either export the functions
directly or keep only one form. The current pattern adds indirection without
benefit.

### `basePaths()` type-asserts against concrete store types

`driver.go:107-113`: `basePaths()` checks `*state.MultiStore` and
`*state.FileStore` with type assertions. This is fragile; if a new store type
is added, it breaks. Consider adding a `Dirs() []string` method to the `Store`
interface, or always using the pools map (which is already the source of truth
when pools are configured).

### `reloadPoolConfig` type-asserts `ms.(*state.MultiStore)`

`main.go:160`: This hard-casts the `state.Store` to `*state.MultiStore`. If the
store type changes, this panics at runtime. Consider passing the
`*state.MultiStore` directly or adding a `Reloader` interface.

### `poolsEqual` reimplements `maps.Equal`

`config.go:53-63`: Since the project already imports `maps` (in `driver.go`),
use `maps.Equal(a, b)` instead.

## 5. Readability Improvements

### Embedded `btrfs.Manager` in `Driver` struct

`driver.go:38`: Embedding `btrfs.Manager` promotes all its methods onto
`Driver`, making it unclear in call sites whether `d.CreateSubvolume(...)` is a
Driver method or a Manager method. Use a named field (`mgr btrfs.Manager`) and
call `d.mgr.CreateSubvolume(...)` for clarity.

### Exported `Store` field on `Driver`

`driver.go:39`: `Store state.Store` is exported but only accessed within the
`driver` package (tests use the `driver` package too). This should be unexported
(`store state.Store`) to enforce encapsulation.

### `memStore` in tests is not thread-safe

`testutil_test.go:12-108`: The `memStore` has no mutex, yet the driver uses it
from multiple goroutines (controller mutex only serializes some operations).
This contributes to the data races above. Tests that exercise concurrent
behavior need a thread-safe store.

## 6. CSI Best Practices Alignment

### Missing `SINGLE_NODE_SINGLE_WRITER` / `SINGLE_NODE_MULTI_WRITER` access modes

`controller.go:23-26`: The driver supports `SINGLE_NODE_WRITER` and
`SINGLE_NODE_READER_ONLY` but not the newer `SINGLE_NODE_SINGLE_WRITER` or
`SINGLE_NODE_MULTI_WRITER` modes introduced in CSI spec 1.5+. Since you depend
on CSI spec v1.12.0, these should be supported.

### No liveness probe endpoint

CSI best practice is to run the `livenessprobe` sidecar. The DaemonSet in
`plugin.yaml` doesn't include it. Add the
`registry.k8s.io/sig-storage/livenessprobe` sidecar and configure a
`livenessProbe` on the driver container.

### No `--leader-election` for controller sidecars

`plugin.yaml`: The `external-provisioner`, `external-snapshotter`, and
`external-resizer` sidecars don't have `--leader-election` flags. For a
single-node setup this isn't critical, but if the DaemonSet ever runs >1
replica (or during rolling updates), you could get duplicate operations.

### Missing resource requests/limits on all containers

`plugin.yaml`: No container has `resources` set. Kubernetes best practice (and
many cluster policies) require resource requests and limits, especially for
system-critical pods.

### CSIDriver spec missing `podInfoOnMount` and `fsGroupPolicy`

`csi-driver.yaml`: Consider setting `podInfoOnMount: true` (useful for audit
logging) and `fsGroupPolicy: File` (btrfs supports POSIX permissions, so the
kubelet should apply fsGroup via chmod).

### No `VolumeCondition` capability advertised by Node service

The controller advertises `VOLUME_CONDITION` capability, but the Node service
should also advertise `VOLUME_CONDITION` in `NodeGetCapabilities` if you want
kubelet to report volume health via `NodeGetVolumeStats`.

### `DeleteVolume` / `DeleteSnapshot` not under `controllerMu`

`controller.go:268`, `controller.go:448`: These aren't serialized by the
controller mutex. While deletes are idempotent, concurrent delete +
create-from-snapshot could race: a snapshot delete could remove the btrfs
subvolume while a volume clone is reading from it. The CSI spec expects the CO
to coordinate, but defensive serialization is safer.

## Summary Checklist

### High Priority

- [x] Fix data races in qgroup cleanup tests (confirmed by `go test -race`)
- [x] Make `memStore` test helper thread-safe

### Medium Priority

- [x] Verify mount source in `NodePublishVolume` idempotency check
- [x] Set explicit permissions (`0o600`) on state temp file
- [x] ~~`fsync` state directory after atomic rename~~ won't fix: btrfs dir fsync flushes entire pool
- [x] Remove `hostNetwork: true` from DaemonSet
- [x] ~~Replace `privileged: true` with specific capabilities~~ can't: kubelet requires `privileged` for Bidirectional mount propagation
- [x] Add `livenessprobe` sidecar to DaemonSet
- [x] Support `SINGLE_NODE_SINGLE_WRITER` / `SINGLE_NODE_MULTI_WRITER` access modes
- [x] Advertise `VOLUME_CONDITION` in `NodeGetCapabilities`
- [x] Set `podInfoOnMount` and `fsGroupPolicy` in CSIDriver spec
- [x] Add resource requests/limits to all containers in DaemonSet

### Low Priority

- [ ] Add pagination to `ListVolumes`
- [ ] Add `name -> id` index to `FileStore` for O(1) name lookups
- [ ] Replace `poolsEqual` with `maps.Equal`
- [ ] Unexport embedded `Manager` field on `Driver` (use named field)
- [ ] Unexport `Store` field on `Driver`
- [ ] Remove redundant exported/unexported wrapper pairs in `config.go`
- [ ] Eliminate type assertions against concrete store types in `basePaths()`
- [ ] Eliminate type assertion in `reloadPoolConfig`
- [ ] Initialize `lastPools` in `watchPoolConfig` to avoid redundant first reload
- [ ] Cache `EnsureQuotaEnabled` result per basePath
- [ ] Serialize `DeleteVolume`/`DeleteSnapshot` under `controllerMu`
- [ ] Add `--leader-election` to controller sidecars
