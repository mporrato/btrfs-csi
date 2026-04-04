# Btrfs CSI Driver — Task Breakdown

All phases follow strict **Red-Green-Gray** TDD methodology:
1. **Red**: Write a failing test that specifies the desired behavior
2. **Green**: Write the minimum production code to make the test pass
3. **Gray** (Refactor): Clean up code while keeping all tests green

Run `go test ./...` after every Green and Gray step.

---

## Phase 1: Project Skeleton

> Set up the Go module, define core interfaces (no implementations), create the mock, and write a minimal main entry point.

### Task 1.1: Initialize Go module

- **Requirements**: Create `go.mod` with module path and required dependencies
- **Acceptance Criteria**:
  - [x] `go.mod` exists with module `github.com/guru/btrfs-csi`
  - [x] Dependencies added: `container-storage-interface/spec`, `grpc`, `mount-utils`, `klog/v2`, `google/uuid`, `csi-test/v5`
  - [x] `go mod tidy` succeeds

### Task 1.2: Define `btrfs.Manager` interface

- **Requirements**: Define the interface in `pkg/btrfs/btrfs.go` that abstracts all btrfs operations. This is a contract — no implementation yet.
- **Acceptance Criteria**:
  - [x] `pkg/btrfs/btrfs.go` contains `Manager` interface with methods:
    - `CreateSubvolume(path string) error`
    - `DeleteSubvolume(path string) error`
    - `SubvolumeExists(path string) (bool, error)`
    - `CreateSnapshot(src, dst string, readonly bool) error`
    - `EnsureQuotaEnabled(mountpoint string) error`
    - `SetQgroupLimit(path string, bytes uint64) error`
    - `RemoveQgroupLimit(path string) error`
    - `GetQgroupUsage(path string) (*QgroupUsage, error)`
    - `GetFilesystemUsage(path string) (*FsUsage, error)`
  - [x] Supporting types defined: `QgroupUsage`, `FsUsage`
  - [x] Package compiles: `go build ./pkg/btrfs/`

### Task 1.3: Define `state.Store` interface and types

- **Requirements**: Define the interface and data types in `pkg/state/state.go` for volume/snapshot metadata persistence.
- **Acceptance Criteria**:
  - [x] `pkg/state/state.go` contains:
    - `Volume` struct: ID, Name, CapacityBytes, SubvolumePath, SourceSnapID, SourceVolID, NodeID
    - `Snapshot` struct: ID, Name, SourceVolID, SnapshotPath, CreatedAt, SizeBytes, ReadyToUse
    - `Store` interface with: `GetVolume`, `GetVolumeByName`, `ListVolumes`, `SaveVolume`, `DeleteVolume`, `GetSnapshot`, `GetSnapshotByName`, `ListSnapshots`, `SaveSnapshot`, `DeleteSnapshot`
  - [x] Package compiles: `go build ./pkg/state/`

### Task 1.4: Create mock `btrfs.Manager`

- **Requirements**: Create `pkg/btrfs/mock.go` with a mock implementation for use in driver unit tests. The mock should record calls and return configurable results.
- **Acceptance Criteria**:
  - [x] `MockManager` struct implements `Manager` interface
  - [x] Each method records its arguments and returns pre-configured error/results
  - [x] Package compiles: `go build ./pkg/btrfs/`

### Task 1.5: Create minimal `main.go`

- **Requirements**: Entry point that parses CLI flags and exits. No gRPC server yet.
- **Acceptance Criteria**:
  - [x] `cmd/btrfs-csi-driver/main.go` parses flags: `--endpoint`, `--nodeid`, `--root-path`, `--version`
  - [x] `go build ./cmd/btrfs-csi-driver/` produces a binary
  - [x] `./btrfs-csi-driver --help` shows flag descriptions

---

## Phase 2: Identity Service (TDD)

> Implement the CSI Identity gRPC service using Red-Green-Gray.

### Task 2.1: Red — Write Identity service tests

- **Requirements**: Write failing tests in `pkg/driver/identity_test.go` that define the expected behavior.
- **Acceptance Criteria**:
  - [x] `TestGetPluginInfo` — asserts response contains name `btrfs.csi.local` and a non-empty version
  - [x] `TestGetPluginCapabilities` — asserts `CONTROLLER_SERVICE` capability is present
  - [x] `TestProbe` — asserts `Ready` is true
  - [x] All tests fail (RED) because implementation doesn't exist yet
  - [x] Tests use a `newTestDriver()` helper that creates a `Driver` with mock btrfs and in-memory state

### Task 2.2: Green — Implement Identity service

- **Requirements**: Write the minimum code in `pkg/driver/identity.go` and `pkg/driver/driver.go` to make all identity tests pass.
- **Acceptance Criteria**:
  - [x] `Driver` struct defined in `driver.go` with `name`, `version`, `nodeID`, `rootPath`, `btrfs.Manager`, `state.Store` fields
  - [x] `NewDriver()` constructor exists
  - [x] `GetPluginInfo`, `GetPluginCapabilities`, `Probe` implemented
  - [x] `go test ./pkg/driver/` passes — all identity tests GREEN

### Task 2.3: Gray — Refactor Identity service

- **Requirements**: Clean up test helpers and driver setup code.
- **Acceptance Criteria**:
  - [x] Shared `newTestDriver()` helper is clean and reusable for future test files
  - [x] No duplication in test setup
  - [x] `go test ./pkg/driver/` still passes

---

## Phase 3: State Layer (TDD)

> Implement the JSON-backed metadata store using Red-Green-Gray.

### Task 3.1: Red — Write State layer tests

- **Requirements**: Write failing tests in `pkg/state/state_test.go`.
- **Acceptance Criteria**:
  - [x] `TestSaveAndGetVolume` — save a volume, retrieve by ID, fields match
  - [x] `TestGetVolumeByName` — save volume, retrieve by name
  - [x] `TestGetVolume_NotFound` — returns nil/false for unknown ID
  - [x] `TestDeleteVolume` — save, delete, confirm gone
  - [x] `TestListVolumes` — save 3 volumes, list returns all 3
  - [x] `TestVolumeOverwrite` — save same ID twice, second write wins
  - [x] `TestSaveAndGetSnapshot` — save a snapshot, retrieve by ID
  - [x] `TestGetSnapshotByName` — save snapshot, retrieve by name
  - [x] `TestDeleteSnapshot` — save, delete, confirm gone
  - [x] `TestListSnapshots` — save 3 snapshots, list returns all 3
  - [x] `TestPersistence` — save data, create new `FileStore` from same path, data survives
  - [x] All tests fail (RED)

### Task 3.2: Green — Implement `FileStore`

- **Requirements**: Implement `FileStore` in `pkg/state/state.go` backed by a JSON file. Mutex-protected, persists on every mutation.
- **Acceptance Criteria**:
  - [x] `NewFileStore(path string)` constructor that loads existing state or starts empty
  - [x] All CRUD methods implemented for volumes and snapshots
  - [x] State persisted to `<path>/state.json` on every write
  - [x] `go test ./pkg/state/` passes — all tests GREEN

### Task 3.3: Gray — Refactor State layer

- **Requirements**: Clean up serialization and error handling.
- **Acceptance Criteria**:
  - [x] Persistence logic extracted into a single `save()` helper
  - [x] Consistent error wrapping
  - [x] `go test ./pkg/state/` still passes

---

## Phase 4: Btrfs Operations Layer (TDD)

> Implement the btrfs CLI wrapper using Red-Green-Gray. Tests require a real btrfs filesystem (build-tag gated).

### Task 4.1: Red — Write btrfs integration tests

- **Requirements**: Write failing tests in `pkg/btrfs/btrfs_test.go` gated with `//go:build integration`. Include a `setupLoopbackBtrfs(t)` helper.
- **Acceptance Criteria**:
  - [x] `setupLoopbackBtrfs(t)` creates a temp file, formats as btrfs, mounts it, returns path + cleanup
  - [x] `TestCreateAndDeleteSubvolume` — create subvolume, verify exists, delete, verify gone
  - [x] `TestSubvolumeExists_NotExists` — returns false for nonexistent path
  - [x] `TestCreateSnapshot` — create subvolume, write a file, snapshot, verify snapshot has the file
  - [x] `TestReadonlySnapshot` — readonly snapshot rejects writes (write attempt returns error)
  - [x] `TestQuotaEnableAndLimit` — enable quotas, set limit, verify via `GetQgroupUsage` that `MaxRfer` matches
  - [x] `TestRemoveQgroupLimit` — set limit, remove it, verify `MaxRfer` is 0/none
  - [x] All tests fail (RED) when run with `go test -tags integration ./pkg/btrfs/`

### Task 4.2: Green — Implement `RealManager`

- **Requirements**: Implement `RealManager` in `pkg/btrfs/btrfs.go` using `os/exec` to call btrfs CLI commands.
- **Acceptance Criteria**:
  - [x] All `Manager` interface methods implemented
  - [x] `EnsureQuotaEnabled` tries `--simple` flag first, falls back on failure
  - [x] Command stderr included in error messages for debuggability
  - [x] `go test -tags integration ./pkg/btrfs/` passes (requires root + btrfs)

### Task 4.3: Gray — Refactor btrfs layer

- **Requirements**: Extract common command execution patterns.
- **Acceptance Criteria**:
  - [x] Shared `runCommand(name string, args ...string) (string, error)` helper that captures stdout+stderr
  - [x] Consistent error formatting across all methods
  - [x] `go test -tags integration ./pkg/btrfs/` still passes

---

## Phase 5: Controller — CreateVolume / DeleteVolume (TDD)

> Implement volume creation and deletion using Red-Green-Gray with mock btrfs.

### Task 5.1: Red — Write CreateVolume / DeleteVolume tests

- **Requirements**: Write failing tests in `pkg/driver/controller_test.go` using mock `btrfs.Manager`.
- **Acceptance Criteria**:
  - [x] `TestCreateVolume_NewVolume` — asserts: mock `CreateSubvolume` called, mock `SetQgroupLimit` called with requested capacity, state contains the volume, response has correct volume ID and topology
  - [x] `TestCreateVolume_Idempotent` — create with same name twice, returns same volume ID, `CreateSubvolume` only called once
  - [x] `TestCreateVolume_MissingName` — returns gRPC `InvalidArgument`
  - [x] `TestCreateVolume_MissingCapabilities` — returns gRPC `InvalidArgument`
  - [x] `TestCreateVolume_WithBasePath` — sets `basePath` in request parameters, asserts subvolume created under that path instead of default
  - [x] `TestDeleteVolume_Exists` — asserts: mock `DeleteSubvolume` called, state no longer contains volume
  - [x] `TestDeleteVolume_NotFound` — returns success (idempotent), no btrfs calls
  - [x] `TestDeleteVolume_MissingID` — returns gRPC `InvalidArgument`
  - [x] All tests fail (RED)

### Task 5.2: Green — Implement CreateVolume / DeleteVolume

- **Requirements**: Implement in `pkg/driver/controller.go`.
- **Acceptance Criteria**:
  - [x] `CreateVolume` validates request, checks idempotency, creates subvolume, sets qgroup limit, saves state, returns volume with topology
  - [x] `DeleteVolume` validates request, deletes subvolume (if exists), removes state
  - [x] `go test ./pkg/driver/` passes — all tests GREEN

### Task 5.3: Gray — Refactor controller code

- **Requirements**: Extract validation and parameter parsing helpers.
- **Acceptance Criteria**:
  - [x] `basePath` resolution extracted into a helper method
  - [x] Request validation logic is clean and consistent
  - [x] `go test ./pkg/driver/` still passes

---

## Phase 6: Controller — Capabilities + Validation (TDD)

> Implement capability reporting and volume capability validation.

### Task 6.1: Red — Write capabilities tests

- **Requirements**: Write failing tests.
- **Acceptance Criteria**:
  - [x] `TestControllerGetCapabilities` — asserts capabilities: `CREATE_DELETE_VOLUME`, `CREATE_DELETE_SNAPSHOT`, `CLONE_VOLUME`, `EXPAND_VOLUME`
  - [x] `TestValidateVolumeCapabilities_Supported` — `SINGLE_NODE_WRITER` returns confirmed with matching capabilities
  - [x] `TestValidateVolumeCapabilities_Unsupported` — multi-node access modes return empty confirmed
  - [x] `TestValidateVolumeCapabilities_VolumeNotFound` — returns gRPC `NotFound`
  - [x] All tests fail (RED)

### Task 6.2: Green — Implement capabilities

- **Requirements**: Implement `ControllerGetCapabilities` and `ValidateVolumeCapabilities`.
- **Acceptance Criteria**:
  - [x] All capability tests pass
  - [x] `go test ./pkg/driver/` passes

### Task 6.3: Gray — Refactor

- **Requirements**: Extract capability checking into a reusable helper.
- **Acceptance Criteria**:
  - [x] Supported access modes defined as a package-level set/slice
  - [x] `go test ./pkg/driver/` still passes

---

## Phase 7: Controller — ExpandVolume (TDD)

> Implement online volume expansion via qgroup limit adjustment.

### Task 7.1: Red — Write expand tests

- **Requirements**: Write failing tests.
- **Acceptance Criteria**:
  - [x] `TestControllerExpandVolume_Success` — asserts: mock `SetQgroupLimit` called with new capacity, state updated, `NodeExpansionRequired` is false
  - [x] `TestControllerExpandVolume_VolumeNotFound` — returns gRPC `NotFound`
  - [x] `TestControllerExpandVolume_ShrinkRejected` — new capacity < current returns gRPC `InvalidArgument`
  - [x] All tests fail (RED)

### Task 7.2: Green — Implement ControllerExpandVolume

- **Requirements**: Implement in `pkg/driver/controller.go`.
- **Acceptance Criteria**:
  - [x] Validates volume exists and new capacity >= current
  - [x] Updates qgroup limit and state
  - [x] Returns `NodeExpansionRequired: false`
  - [x] `go test ./pkg/driver/` passes

### Task 7.3: Gray — Refactor

- **Requirements**: Clean up any duplication with CreateVolume's qgroup logic.
- **Acceptance Criteria**:
  - [x] `go test ./pkg/driver/` still passes

---

## Phase 8: Node Service (TDD)

> Implement the CSI Node gRPC service for bind mounting, stats, and node identity.

### Task 8.1: Red — Write Node service tests

- **Requirements**: Write failing tests in `pkg/driver/node_test.go`. Mount operations use a `Mounter` interface (mock in tests).
- **Acceptance Criteria**:
  - [x] `Mounter` interface defined with `Mount`, `Unmount`, `IsMountPoint` methods
  - [x] `MockMounter` records calls and returns configurable results
  - [x] `TestNodePublishVolume_Success` — asserts bind mount called with correct source (subvolume path) and target
  - [x] `TestNodePublishVolume_Readonly` — asserts mount options include `ro`
  - [x] `TestNodePublishVolume_MissingVolumeID` — returns gRPC `InvalidArgument`
  - [x] `TestNodePublishVolume_MissingTargetPath` — returns gRPC `InvalidArgument`
  - [x] `TestNodeUnpublishVolume_Success` — asserts unmount called, target directory removed
  - [x] `TestNodeUnpublishVolume_NotMounted` — returns success (idempotent)
  - [x] `TestNodeGetInfo` — asserts node ID and topology key `topology.btrfs.csi.local/node` returned
  - [x] `TestNodeGetCapabilities` — asserts `GET_VOLUME_STATS` capability
  - [x] `TestNodeGetVolumeStats` — asserts usage stats returned from mock qgroup data
  - [x] All tests fail (RED)

### Task 8.2: Green — Implement Node service

- **Requirements**: Implement in `pkg/driver/node.go`.
- **Acceptance Criteria**:
  - [x] `NodePublishVolume` creates target dir, bind mounts subvolume path, handles readonly
  - [x] `NodeUnpublishVolume` unmounts and removes target dir (idempotent)
  - [x] `NodeGetInfo` returns node ID and topology
  - [x] `NodeGetCapabilities` returns `GET_VOLUME_STATS`
  - [x] `NodeGetVolumeStats` returns byte and inode usage
  - [x] `go test ./pkg/driver/` passes

### Task 8.3: Gray — Refactor Node service

- **Requirements**: Clean up mount helpers and error handling.
- **Acceptance Criteria**:
  - [x] Mount logic is clean and well-structured
  - [x] `go test ./pkg/driver/` still passes

---

## Phase 9: Snapshots + Cloning (TDD)

> Implement CSI snapshot and volume clone operations.

### Task 9.1: Red — Write snapshot and clone tests

- **Requirements**: Write failing tests.
- **Acceptance Criteria**:
  - [ ] `TestCreateSnapshot_Success` — asserts: mock `CreateSnapshot` called with `readonly=true`, state saved with `ReadyToUse=true` and creation timestamp
  - [ ] `TestCreateSnapshot_Idempotent` — same name returns existing snapshot, no duplicate btrfs call
  - [ ] `TestCreateSnapshot_SourceNotFound` — source volume not in state, returns gRPC `NotFound`
  - [ ] `TestCreateSnapshot_MissingName` — returns gRPC `InvalidArgument`
  - [ ] `TestDeleteSnapshot_Success` — asserts snapshot subvolume deleted, state removed
  - [ ] `TestDeleteSnapshot_NotFound` — returns success (idempotent)
  - [ ] `TestCreateVolume_FromSnapshot` — asserts: writable snapshot created from snapshot path, state records source snap ID
  - [ ] `TestCreateVolume_FromSnapshot_SourceNotFound` — snapshot not in state, returns gRPC `NotFound`
  - [ ] `TestCreateVolume_Clone` — asserts: writable snapshot created from source volume path, state records source vol ID
  - [ ] `TestCreateVolume_Clone_SourceNotFound` — source volume not in state, returns gRPC `NotFound`
  - [ ] All tests fail (RED)

### Task 9.2: Green — Implement snapshots and cloning

- **Requirements**: Implement `CreateSnapshot`, `DeleteSnapshot` in `pkg/driver/controller.go`. Add content source handling to `CreateVolume`.
- **Acceptance Criteria**:
  - [ ] `CreateSnapshot` creates readonly btrfs snapshot, saves metadata
  - [ ] `DeleteSnapshot` removes snapshot subvolume and state (idempotent)
  - [ ] `CreateVolume` with snapshot source creates writable snapshot of readonly snap
  - [ ] `CreateVolume` with volume source creates writable snapshot of source volume
  - [ ] `go test ./pkg/driver/` passes

### Task 9.3: Gray — Refactor snapshot/clone logic

- **Requirements**: Clean up content source branching in `CreateVolume`.
- **Acceptance Criteria**:
  - [ ] Content source handling is clear and well-structured
  - [ ] `go test ./pkg/driver/` still passes

---

## Phase 10: gRPC Server + Driver Wiring (TDD)

> Wire up the gRPC server that listens on a Unix socket and serves all three CSI services.

### Task 10.1: Red — Write server lifecycle tests

- **Requirements**: Write failing tests in `pkg/driver/driver_test.go`.
- **Acceptance Criteria**:
  - [ ] `TestDriverStartsAndStops` — starts driver on a temp Unix socket, connects a gRPC client, calls `Probe`, asserts `Ready=true`, stops driver, asserts clean shutdown
  - [ ] Test fails (RED)

### Task 10.2: Green — Implement `Driver.Run()`

- **Requirements**: Implement gRPC server setup in `pkg/driver/driver.go`.
- **Acceptance Criteria**:
  - [ ] `Run()` creates Unix socket listener, registers Identity/Controller/Node servers, starts serving
  - [ ] `Stop()` performs graceful shutdown
  - [ ] Socket file is cleaned up on start (remove stale) and stop
  - [ ] `go test ./pkg/driver/` passes

### Task 10.3: Gray — Refactor server code

- **Requirements**: Clean up socket management and shutdown logic.
- **Acceptance Criteria**:
  - [ ] `go test ./pkg/driver/` still passes

### Task 10.4: Wire up `main.go`

- **Requirements**: Update `cmd/btrfs-csi-driver/main.go` to create a real `Driver` with `RealManager` and `FileStore`, and call `Run()`.
- **Acceptance Criteria**:
  - [ ] `go build ./cmd/btrfs-csi-driver/` succeeds
  - [ ] Binary starts, creates socket, responds to gRPC calls, shuts down on SIGTERM

---

## Phase 11: Build + Deploy

> Create container image and Kubernetes deployment manifests.

### Task 11.1: Create Dockerfile

- **Requirements**: Multi-stage build: Go builder stage → minimal runtime with btrfs-progs.
- **Acceptance Criteria**:
  - [ ] Builder stage compiles the binary with `CGO_ENABLED=0` (or appropriate flags)
  - [ ] Runtime stage based on Fedora (or similar) with `btrfs-progs` and `util-linux` installed
  - [ ] `docker build -t btrfs-csi-driver:latest .` succeeds

### Task 11.2: Create Makefile

- **Requirements**: Standard build targets.
- **Acceptance Criteria**:
  - [ ] `make build` — compiles binary to `bin/btrfs-csi-driver`
  - [ ] `make test` — runs `go test ./...`
  - [ ] `make test-integration` — runs `go test -tags integration ./pkg/btrfs/`
  - [ ] `make image` — builds Docker image
  - [ ] `make deploy` — applies all manifests via `kubectl apply -f deploy/`
  - [ ] `make clean` — removes build artifacts

### Task 11.3: Create CSIDriver manifest

- **Requirements**: `deploy/csi-driver.yaml`
- **Acceptance Criteria**:
  - [ ] `CSIDriver` resource with name `btrfs.csi.local`
  - [ ] `attachRequired: false`
  - [ ] `volumeLifecycleModes: [Persistent]`

### Task 11.4: Create RBAC manifests

- **Requirements**: `deploy/rbac.yaml`
- **Acceptance Criteria**:
  - [ ] `ServiceAccount` in `kube-system`
  - [ ] `ClusterRole` with permissions for: persistentvolumes, persistentvolumeclaims, storageclasses, events, volumesnapshots, volumesnapshotcontents, volumesnapshotclasses
  - [ ] `ClusterRoleBinding` linking them

### Task 11.5: Create DaemonSet manifest

- **Requirements**: `deploy/plugin.yaml`
- **Acceptance Criteria**:
  - [ ] DaemonSet in `kube-system` with 4 containers:
    - `btrfs-csi-driver` (privileged, mounts: host btrfs path, `/csi` emptyDir, kubelet pods dir with bidirectional mount propagation)
    - `node-driver-registrar` (mounts: `/csi`, `/registration` hostPath)
    - `external-provisioner` (mounts: `/csi`)
    - `external-snapshotter` (mounts: `/csi`)
  - [ ] Correct socket paths and args for all containers
  - [ ] `NODE_NAME` env var from downward API

### Task 11.6: Create StorageClass manifest

- **Requirements**: `deploy/storageclass.yaml`
- **Acceptance Criteria**:
  - [ ] StorageClass `btrfs` with provisioner `btrfs.csi.local`
  - [ ] `volumeBindingMode: WaitForFirstConsumer`
  - [ ] `reclaimPolicy: Delete`
  - [ ] `allowVolumeExpansion: true`

### Task 11.7: Create VolumeSnapshotClass manifest

- **Requirements**: `deploy/snapshotclass.yaml`
- **Acceptance Criteria**:
  - [ ] VolumeSnapshotClass `btrfs-snapshot` with driver `btrfs.csi.local`
  - [ ] `deletionPolicy: Delete`

---

## Phase 12: CSI Sanity + E2E

> Run conformance tests and end-to-end validation on a kind cluster.

### Task 12.1: CSI sanity suite

- **Requirements**: Run the `kubernetes-csi/csi-test` sanity suite against the driver.
- **Acceptance Criteria**:
  - [ ] Sanity test file exists (e.g., `pkg/driver/sanity_test.go` with `//go:build integration`)
  - [ ] Tests start the driver on a temp socket, run the sanity suite against it
  - [ ] All applicable sanity checks pass

### Task 12.2: Kind cluster setup

- **Requirements**: Create `test/kind-config.yaml` and a setup script for btrfs loopback.
- **Acceptance Criteria**:
  - [ ] `test/kind-config.yaml` with `extraMounts` for btrfs loopback image
  - [ ] Setup script: creates loopback btrfs image (`dd` + `mkfs.btrfs`), creates kind cluster, loads driver image
  - [ ] Cluster comes up with btrfs mount available inside the node

### Task 12.3: E2E — Basic volume lifecycle

- **Requirements**: Test PVC creation, pod mount, data write, pod delete, PVC delete.
- **Acceptance Criteria**:
  - [ ] Create PVC with StorageClass `btrfs` → PVC becomes Bound
  - [ ] Create Pod that mounts the PVC and writes a file → Pod succeeds
  - [ ] Delete Pod → clean unmount
  - [ ] Delete PVC → subvolume cleaned up

### Task 12.4: E2E — Snapshot and restore

- **Requirements**: Test snapshot creation and PVC creation from snapshot.
- **Acceptance Criteria**:
  - [ ] Create PVC, mount in Pod, write known data
  - [ ] Create VolumeSnapshot from PVC → snapshot becomes `ReadyToUse`
  - [ ] Create new PVC from VolumeSnapshot → PVC becomes Bound
  - [ ] Mount new PVC in Pod → data matches original

### Task 12.5: E2E — Volume cloning

- **Requirements**: Test PVC creation from existing PVC (clone).
- **Acceptance Criteria**:
  - [ ] Create source PVC, mount in Pod, write known data
  - [ ] Create new PVC with `dataSource` pointing to source PVC → PVC becomes Bound
  - [ ] Mount new PVC in Pod → data matches original
  - [ ] Writes to clone do not affect source

### Task 12.6: E2E — Volume expansion

- **Requirements**: Test online PVC resize.
- **Acceptance Criteria**:
  - [ ] Create PVC with small capacity (e.g., 100Mi)
  - [ ] Patch PVC to larger capacity (e.g., 500Mi)
  - [ ] PVC capacity updates → qgroup limit reflects new size
  - [ ] Pod can write data up to the new capacity
