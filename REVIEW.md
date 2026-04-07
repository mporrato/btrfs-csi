# Third Review Pass (2026-04-07)

All items from the first two review passes have been addressed (checked off in
the summary below). This pass focuses on remaining correctness issues, CSI spec
compliance gaps, security hardening, and code quality improvements discovered
during a fresh full-codebase review.

## Bugs

### `NodeExpandVolume` doesn't validate inputs or return `CapacityBytes`

`node.go:273-278`: Per the CSI spec, `NodeExpandVolume` **must** return
`CapacityBytes` in the response (at minimum the current capacity). Returning an
empty response with `CapacityBytes: 0` may confuse the CO. Additionally, the
method doesn't validate `req.VolumeId` or `req.VolumePath`, unlike every other
Node RPC. While the external-resizer sidecar may not call this for a driver that
sets `NodeExpansionRequired: false`, the CSI spec requires the RPC to be
implemented correctly regardless.

### `NodeGetVolumeStats` reports `Total: 0` when no quota is set

`node.go:253-259`: When a volume is created with `CapacityBytes: 0` (no quota),
`MaxRfer` is 0, so `Total` is reported as 0 and `Available` is also 0. This
makes the volume appear "full" to monitoring systems. The `Total` should fall
back to the filesystem capacity when no per-volume quota is set, or at minimum
`Available` should reflect the filesystem's available space.

### `DeleteVolume`/`DeleteSnapshot` not idempotent when subvolume is gone but state remains

`controller.go:325-334`, `controller.go:525-534`: If `DeleteSubvolume` succeeds
but `DeleteVolume`/`DeleteSnapshot` fails, the subvolume is gone but the state
still references it. On retry, `GetVolume` finds the volume in state, tries to
delete the subvolume again, and gets an error (subvolume doesn't exist). This
returns `codes.Internal` to the caller instead of succeeding idempotently. The
fix is to check `SubvolumeExists` before attempting deletion, or to treat
"subvolume not found" as success during delete.

### `CapacityRange.LimitBytes` ignored in `CreateVolume` and `ControllerExpandVolume`

`controller.go:276-278`, `controller.go:355-361`: The CSI spec's `CapacityRange`
has both `RequiredBytes` and `LimitBytes`. The driver only uses `RequiredBytes`
and ignores `LimitBytes`. If `RequiredBytes` exceeds `LimitBytes`, the spec says
to return `InvalidArgument`. The current code would silently set the limit to
`RequiredBytes`, potentially exceeding what the caller considers acceptable.

## Security Issues

### `validatePath` not called on `NodeGetVolumeStats.VolumePath`

`node.go:211-280`: `validatePath` is only called on `targetPath` in
`NodePublishVolume` and `NodeUnpublishVolume`. The `volume_path` in
`NodeGetVolumeStats` is **not** validated. While the kubelet provides these
paths, validating all user-facing inputs is a CSI best practice.

### `validatePath` doesn't catch encoded traversal sequences

`node.go:9-19`: The check only catches literal `..` path components. It doesn't
catch URL-encoded sequences like `%2e%2e`, overlong UTF-8 encodings, or paths
with embedded null bytes. While the CSI CO should not send these,
defense-in-depth is warranted.

### Sidecar images not pinned by digest

`plugin.yaml`: All sidecar images use tag-only references (e.g.
`registry.k8s.io/sig-storage/livenessprobe:v2.14.0`). For supply-chain
security, images should be pinned by digest (SHA256). Tags are mutable and can
be retagged to point to malicious images.

### No `seccompProfile` or capability drop in security context

`plugin.yaml`: While `privileged: true` is required for bidirectional mount
propagation, best practice is to also add `capabilities: drop: ["ALL"]` and
`seccompProfile: type: RuntimeDefault`. This documents intent and ensures that
if `privileged` is ever removed, the container starts with minimal
capabilities.

## Undesirable Behaviour

### `Snapshot.SizeBytes` is never populated

`controller.go:475-483`: `SizeBytes` is always 0 in the `Snapshot` struct. The
CSI spec says `SizeBytes` should be populated when `ReadyToUse` is true. The
driver could call `GetQgroupUsage` on the snapshot path to get the referenced
size.

### `CreateSnapshot` always creates readonly snapshots

`controller.go:489`: The driver always passes `readonly: true` when creating
snapshots. This means snapshots can never be directly mounted read-write. If a
user wants to restore from a snapshot by mounting it, they must create a volume
from the snapshot first. This is a design choice, not a bug, but it should be
documented. **Fixed**: added an explanatory comment at the call site.

### `NodeGetInfo` doesn't report `MaxVolumesPerNode`

`node.go:178-186`: The response doesn't include `MaxVolumesPerNode`. Since btrfs
subvolumes are limited only by filesystem capacity, this could be left unset (0
means "unlimited"), but explicitly setting it to 0 would be clearer and more
aligned with CSI best practices.

## Simplification Opportunities

### `btrfs_test.go` tests are purely structural and low-value

`pkg/btrfs/btrfs_test.go`: These tests use reflection to verify that the
`Manager` interface has certain methods and that structs have certain fields.
They break if you rename a method but don't catch behavioral bugs. They provide
minimal value over the compile-time `var _ Manager = (*RealManager)(nil)` check
already in `real.go`. Consider removing them or replacing with behavioral tests.

### `real_test.go` has skipped and no-op tests

`pkg/btrfs/real_test.go`: `TestSendReceive_IdempotencyCheck` immediately calls
`t.Skip()`. `TestTempSnapshotNaming` just logs a message.
`TestTempSnapshotCleanupPattern` tests a glob pattern but not the actual cleanup
code. These are documentation tests that add noise to the test output. Either
implement them properly or remove them.

### `memStore` test helper duplicates `FileStore` logic

`testutil_test.go`: The `memStore` is a hand-written in-memory store that
duplicates much of `FileStore`'s logic (deep copies, BasePath hydration, name
indexing). Since `FileStore` already works with temp directories (as shown in
`state_test.go`), the tests could use `FileStore` backed by `t.TempDir()`
instead. This would also test the real store implementation, catching
serialization bugs that `memStore` can't.

### Type assertions on `d.store.(*state.MultiStore)` in tests

Multiple test files do `d.store.(*state.MultiStore).AddStoreForTest(...)`. This
couples tests to the concrete store type. If the store implementation ever
changes, all these tests break. Consider exposing a test-only helper method on
`Driver` like `AddTestStore(basePath string, s state.Store)`.

## Readability Improvements

### Inconsistent error wrapping in controller

Some errors use `status.Errorf(codes.Internal, "%v", err)` which double-wraps
(`controller.go:302`), while others provide context (`controller.go:292`:
`"create subvolume: %v"`). The `"%v"` pattern loses the original error context.
Use descriptive messages consistently.

### `provisionVolume` mutates its `vol` parameter as a side effect

`controller.go:414,422`: `provisionVolume` takes `*state.Volume` and mutates it
(`vol.SourceSnapID = snapID`, `vol.SourceVolID = srcVolID`). This is surprising
— a function named "provision" shouldn't silently modify the volume struct.
Consider returning the source IDs or making the mutation explicit.

### UUID truncation to 8 hex chars in temp snapshot names

`real.go:151`: `uuid.New().String()[:8]` truncates a UUID from 122 bits of
uniqueness to 32 bits. While collisions are still unlikely for temp snapshot
names, using the full UUID (or at least 16 characters) would be safer and more
idiomatic.

### Deferred cleanup silently discards errors

`real.go:155`: `defer func() { _ = m.DeleteSubvolume(tempSnap) }()` silently
discards the error. While this is intentional (best-effort cleanup), logging the
error would help debug stale temp snapshots.

## CSI Best Practices Alignment

### Missing `EXPAND_VOLUME` capability in `NodeGetCapabilities`

The controller advertises `EXPAND_VOLUME`, and `NodeExpandVolume` is implemented
(as a no-op), but the Node service doesn't advertise `RPC_EXPAND_VOLUME` in
`NodeGetCapabilities`. Per the CSI spec, if `ControllerExpandVolume` returns
`NodeExpansionRequired: false`, you don't strictly need the node capability, but
advertising it makes the driver's capabilities explicit.

### `CSIDriver` spec missing `storageCapacity`

`deploy/csi-driver.yaml`: Since the driver implements `GetCapacity` and the
external-provisioner supports capacity-aware scheduling, adding
`storageCapacity: true` to the CSIDriver spec would enable the kube-scheduler to
consider storage capacity when scheduling pods. This is especially important for
a driver that manages finite btrfs pools.

### `Probe` should check btrfs filesystem health, not just path existence

`identity.go:43-60`: `os.Stat` only checks if the directory exists. A more
robust probe would verify the path is still on a btrfs filesystem (e.g., call
`IsBtrfsFilesystem`). If the btrfs filesystem is unmounted or corrupted, the
driver should report not-ready.

## Idiomatic Go

### `panic` in `NewDriver` for nil arguments

`driver.go:57-62`: While panicking on programmer errors is acceptable in Go,
returning an error is more idiomatic and testable. The current approach makes it
impossible to write a test that verifies the error case without recovering from
a panic.

### `context.Context` ignored in all CSI RPCs

All CSI RPCs receive `context.Context` but ignore it (using `_ context.Context`).
While the CSI spec says the CO may set deadlines, and most CSI drivers ignore the
context, it would be more correct to at least check `ctx.Err()` at the start of
long-running operations (like `CreateSnapshot` with send/receive).

## Updated Summary Checklist

### High Priority

- [x] Fix data races in qgroup cleanup tests (confirmed by `go test -race`)
- [x] Make `memStore` test helper thread-safe
- [x] `DeleteVolume`/`DeleteSnapshot`: make idempotent when subvolume is gone but state remains
- [x] `NodeExpandVolume`: return `CapacityBytes` and validate inputs
- [x] `NodeGetVolumeStats`: report filesystem capacity as `Total`/`Available` when no quota is set
- [x] Validate `CapacityRange.LimitBytes` in `CreateVolume` and `ControllerExpandVolume`

### Medium Priority

- [x] ~~Verify mount source in `NodePublishVolume` idempotency check~~ won't fix: bind mounts on btrfs report block device, not source path
- [x] Set explicit permissions (`0o600`) on state temp file
- [x] ~~`fsync` state directory after atomic rename~~ won't fix: btrfs dir fsync flushes entire pool
- [x] Remove `hostNetwork: true` from DaemonSet
- [x] ~~Replace `privileged: true` with specific capabilities~~ can't: kubelet requires `privileged` for Bidirectional mount propagation
- [x] Add `livenessprobe` sidecar to DaemonSet
- [x] Support `SINGLE_NODE_SINGLE_WRITER` / `SINGLE_NODE_MULTI_WRITER` access modes
- [x] Advertise `VOLUME_CONDITION` in `NodeGetCapabilities`
- [x] Set `podInfoOnMount` and `fsGroupPolicy` in CSIDriver spec
- [x] Add resource requests/limits to all containers in DaemonSet
- [x] Add `VolumeCondition` to `NodeGetVolumeStats` response
- [x] Add cleanup on `CreateVolume` failure after subvolume creation
- [x] Call `ensureQuotaEnabled` in `ControllerExpandVolume`
- [x] Populate `Snapshot.SizeBytes` when `ReadyToUse` is true
- [ ] Call `validatePath` on `NodeGetVolumeStats.VolumePath`
- [ ] Pin sidecar images by SHA256 digest in `plugin.yaml`
- [ ] Add `seccompProfile` and `capabilities: drop: ["ALL"]` to security context
- [ ] Add `storageCapacity: true` to CSIDriver spec
- [ ] Improve `Probe` to check btrfs filesystem health (not just path existence)

### Low Priority

- [x] Add pagination to `ListVolumes`
- [x] Add `name -> id` index to `FileStore` for O(1) name lookups
- [x] Replace `poolsEqual` with `maps.Equal`
- [x] Unexport embedded `Manager` field on `Driver` (use named field)
- [x] Unexport `Store` field on `Driver`
- [x] Remove redundant exported/unexported wrapper pairs in `config.go`
- [x] Eliminate type assertions against concrete store types in `basePaths()`
- [x] Eliminate type assertion in `reloadPoolConfig`
- [x] Initialize `lastPools` in `watchPoolConfig`
- [x] Cache `EnsureQuotaEnabled` result per basePath
- [x] Serialize `DeleteVolume`/`DeleteSnapshot` under `controllerMu`
- [x] Add `--leader-election` to controller sidecars
- [x] Replace deprecated `grpc.DialContext` with `grpc.NewClient` in tests
- [x] Move `WatchPoolConfig` select to top of loop for responsive shutdown
- [x] Move `csi_test.go` blank import to `tools.go` with build tag
- [ ] Remove or replace low-value structural tests in `btrfs_test.go`
- [ ] Remove or implement skipped/no-op tests in `real_test.go`
- [ ] Consider using `FileStore` with `t.TempDir()` instead of `memStore` in driver tests
- [ ] Extract `d.store.(*state.MultiStore)` type assertions into a test helper
- [ ] Use consistent error wrapping (descriptive messages instead of bare `"%v"`)
- [ ] Make `provisionVolume` mutation of `vol` explicit (return source IDs)
- [ ] Use full UUID (or at least 16 chars) instead of `[:8]` truncation in temp snapshot names
- [ ] Log errors in deferred cleanup instead of silently discarding
- [ ] Advertise `EXPAND_VOLUME` in `NodeGetCapabilities`
- [x] Set `MaxVolumesPerNode: 0` explicitly in `NodeGetInfo` response
- [ ] Return error from `NewDriver` instead of panicking on nil arguments
- [ ] Check `ctx.Err()` at start of long-running CSI RPCs
