# Btrfs CSI Driver — Architecture

## Context

A Kubernetes CSI (Container Storage Interface) storage driver for single-node clusters (e.g., k0s) that leverages btrfs filesystem features: copy-on-write, subvolumes, snapshots, and quota groups.

The driver is a single Go binary serving three gRPC services (Identity, Controller, Node) over a Unix domain socket, deployed as a DaemonSet with standard Kubernetes CSI sidecar containers.

### Reference Projects

- [`kubernetes-csi/csi-driver-host-path`](https://github.com/kubernetes-csi/csi-driver-host-path) — CSI reference implementation
- [`erikmagkekse/btrfs-nfs-csi`](https://github.com/erikmagkekse/btrfs-nfs-csi) — Btrfs CSI with NFS export support
- [`openebs/lvm-localpv`](https://github.com/openebs/lvm-localpv) — LVM-based local CSI driver

## Architecture

**Single binary, three gRPC services, one Unix socket.** Sidecars (provisioner, snapshotter, registrar) run in the same pod and communicate with the driver over the shared socket.

### Btrfs Concept Mapping

| CSI Concept | Btrfs Feature | Details |
|---|---|---|
| Volume | Subvolume | Created under `<basePath>/volumes/<id>` |
| Capacity | Qgroup limit | `btrfs qgroup limit <bytes> <path>` |
| Snapshot | Readonly snapshot | Created under `<basePath>/snapshots/<id>` |
| Clone | Writable snapshot | Writable btrfs snapshot of source volume |
| Expansion | Qgroup limit adjustment | No fs resize needed — subvolumes share the filesystem |
| Mount | Bind mount | Subvolume path bind-mounted to pod target path |

### CSI Services

#### Identity Service
- `GetPluginInfo` — Returns driver name (`btrfs.csi.local`) and version
- `GetPluginCapabilities` — Advertises `CONTROLLER_SERVICE` and `VOLUME_ACCESSIBILITY_CONSTRAINTS`
- `Probe` — Health check (verifies btrfs is operational)

#### Controller Service
- `CreateVolume` — Creates btrfs subvolume, sets qgroup limit, handles clone/restore from snapshot
- `DeleteVolume` — Deletes subvolume and state (idempotent)
- `CreateSnapshot` — Creates readonly btrfs snapshot
- `DeleteSnapshot` — Deletes snapshot subvolume
- `ControllerExpandVolume` — Adjusts qgroup limit (`NodeExpansionRequired: false`)
- `ValidateVolumeCapabilities` — Confirms single-node access modes
- `ControllerGetCapabilities` — Advertises: `CREATE_DELETE_VOLUME`, `CREATE_DELETE_SNAPSHOT`, `CLONE_VOLUME`, `EXPAND_VOLUME`

#### Node Service
- `NodePublishVolume` — Bind mounts subvolume to pod target path
- `NodeUnpublishVolume` — Unmounts and removes target directory
- `NodeGetInfo` — Returns node ID and topology key
- `NodeGetCapabilities` — Advertises `GET_VOLUME_STATS`
- `NodeGetVolumeStats` — Returns per-subvolume usage via qgroup + statfs for inodes

Not implemented (not needed for local storage):
- `NodeStageVolume` / `NodeUnstageVolume` — Subvolumes are already on a mounted btrfs fs
- `ControllerPublishVolume` / `ControllerUnpublishVolume` — No multi-node attach

## Key Design Decisions

### CLI over ioctl
Use the `btrfs` CLI via `os/exec` rather than direct kernel ioctls. The CLI is the stable user-facing API and is dramatically simpler to implement (dozens of lines of exec calls vs. hundreds of lines of unsafe ioctl code with kernel struct definitions). The cost is fork/exec overhead per operation, which is negligible for a CSI driver where volume operations are infrequent.

### Simple quotas first
Modern btrfs (kernel 6.7+) supports "simple quotas" (`btrfs quota enable --simple`), which avoid the expensive full-tree metadata scan of traditional qgroups. The driver tries simple quotas first and falls back to traditional `btrfs quota enable` if the kernel doesn't support them.

### No staging
`NodeStageVolume`/`NodeUnstageVolume` are skipped because there is no device-to-filesystem preparation step. Subvolumes are already part of the mounted btrfs filesystem. The flow is: `CreateVolume` makes the subvolume, `NodePublishVolume` bind-mounts it to the pod's target path.

### JSON state file
Volume and snapshot metadata is persisted in a mutex-protected JSON file (same pattern as `csi-driver-host-path`). For a single-node driver, this is perfectly adequate — no database or CRDs needed. The state file lives at `<basePath>/state.json`.

### Base path configuration
Two-tier configuration:
1. **CLI flag** `--root-path` sets the default (e.g., `/var/lib/btrfs-csi`)
2. **StorageClass `parameters.basePath`** overrides per-class, allowing different StorageClasses to point at different btrfs mounts (e.g., NVMe vs HDD)

The controller reads `basePath` from `req.Parameters` in `CreateVolume` and falls back to the CLI flag. This is the standard CSI pattern used by hostpath, LVM, and NFS drivers.

### Topology awareness
Although single-node, the driver implements `VOLUME_ACCESSIBILITY_CONSTRAINTS` and returns a topology segment (`topology.btrfs.csi.local/node`) in `CreateVolume` and `NodeGetInfo`. This enables `WaitForFirstConsumer` binding mode in the StorageClass, which is the recommended practice for local storage.

## Project Structure

```
btrfs-csi/
├── cmd/
│   └── btrfs-csi-driver/
│       └── main.go                     # Entry point, flag parsing, driver startup
├── pkg/
│   ├── driver/
│   │   ├── driver.go                   # Driver struct, gRPC server setup, Run()
│   │   ├── driver_test.go              # Server start/stop tests
│   │   ├── identity.go                 # Identity service RPCs
│   │   ├── identity_test.go            # Identity service tests
│   │   ├── controller.go               # Controller service RPCs
│   │   ├── controller_test.go          # Controller service tests
│   │   ├── node.go                     # Node service RPCs
│   │   └── node_test.go               # Node service tests
│   ├── btrfs/
│   │   ├── btrfs.go                    # Manager interface + exec-based implementation
│   │   ├── btrfs_test.go              # Integration tests (build-tag gated)
│   │   └── mock.go                     # Mock Manager for driver unit tests
│   └── state/
│       ├── state.go                    # JSON-backed volume/snapshot metadata store
│       └── state_test.go              # State layer unit tests
├── deploy/
│   ├── csi-driver.yaml                 # CSIDriver object (attachRequired: false)
│   ├── rbac.yaml                       # ServiceAccount, ClusterRole, ClusterRoleBinding
│   ├── plugin.yaml                     # DaemonSet: driver + 3 sidecars
│   ├── storageclass.yaml               # StorageClass "btrfs" with WaitForFirstConsumer
│   └── snapshotclass.yaml              # VolumeSnapshotClass
├── test/
│   └── kind-config.yaml                # Kind cluster config with btrfs loopback extraMounts
├── docs/
│   ├── architecture.md                 # This file
│   └── tasks.md                        # Task breakdown and progress tracking
├── Dockerfile                          # Multi-stage: golang builder → fedora + btrfs-progs
├── Makefile                            # build, test, test-integration, image, deploy
└── go.mod
```

## Btrfs Operations Layer

Each `btrfs.Manager` method maps to CLI commands:

| Method | Command | Notes |
|---|---|---|
| `CreateSubvolume(path)` | `btrfs subvolume create <path>` | |
| `DeleteSubvolume(path)` | `btrfs subvolume delete <path>` | May need `btrfs property set <path> ro false` first |
| `SubvolumeExists(path)` | `btrfs subvolume show <path>` | Check exit code |
| `CreateSnapshot(src, dst, ro)` | `btrfs subvolume snapshot [-r] <src> <dst>` | `-r` for readonly |
| `EnsureQuotaEnabled(mp)` | `btrfs quota enable [--simple] <mp>` | Idempotent, try simple first |
| `SetQgroupLimit(path, bytes)` | `btrfs qgroup limit <bytes> <path>` | Sets limit on auto-created 0/N qgroup |
| `RemoveQgroupLimit(path)` | `btrfs qgroup limit none <path>` | |
| `GetQgroupUsage(path)` | `btrfs qgroup show --raw -rfe -F <path>` | Parse referenced, exclusive, max_rfer |
| `GetFilesystemUsage(path)` | `btrfs filesystem usage -b <path>` | Total/used/available |

## Deployment

### Pod Structure (DaemonSet)

Single DaemonSet pod with 4 containers:

1. **btrfs-csi-driver** — The main binary (privileged, for mount operations and btrfs commands)
2. **node-driver-registrar** — Registers the CSI driver with kubelet
3. **external-provisioner** — Watches PVCs, calls `CreateVolume`/`DeleteVolume`
4. **external-snapshotter** — Watches VolumeSnapshots, calls `CreateSnapshot`/`DeleteSnapshot`

### Kubernetes Objects

- `CSIDriver` — `attachRequired: false` (no ControllerPublish needed)
- `StorageClass` — `btrfs`, `WaitForFirstConsumer`, `allowVolumeExpansion: true`
- `VolumeSnapshotClass` — `btrfs-snapshot`, `deletionPolicy: Delete`
- RBAC — ServiceAccount + ClusterRole for PV/PVC/snapshot permissions

## Dependencies

| Package | Purpose |
|---|---|
| `github.com/container-storage-interface/spec` | CSI protobuf/gRPC interfaces |
| `google.golang.org/grpc` | gRPC server |
| `k8s.io/mount-utils` | Bind mount/unmount operations |
| `k8s.io/klog/v2` | Structured logging |
| `github.com/google/uuid` | Volume/snapshot ID generation |
| `github.com/kubernetes-csi/csi-test/v5` | CSI sanity/conformance test suite |

## Verification Plan

1. **Unit tests** — Mock-based driver tests, state layer tests (`go test ./...`)
2. **CSI sanity** — `csi-test` conformance suite against running driver
3. **Integration tests** — Btrfs layer tests on loopback btrfs fs (`go test -tags integration ./pkg/btrfs/`)
4. **E2E on kind** — Full workflow on a kind cluster with btrfs loopback mount:
   - Create PVC → Pod → write data → VolumeSnapshot → PVC from snapshot → verify data
   - Resize PVC → verify new capacity
   - Delete everything → verify subvolumes cleaned up
