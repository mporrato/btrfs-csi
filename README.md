# Btrfs CSI Driver

[![Go Version](https://img.shields.io/github/go-mod/go-version/mporrato/btrfs-csi)](https://github.com/mporrato/btrfs-csi)
[![License: Unlicense](https://img.shields.io/badge/License-Unlicense-green.svg)](LICENSE)

A Kubernetes CSI (Container Storage Interface) storage driver for single-node clusters (e.g., k0s, minikube, kind) that leverages btrfs features: subvolumes, snapshots, qgroups, and copy-on-write.

Written in Go. Single binary serving Identity, Controller, and Node gRPC services over a Unix socket.

## Features

- **Subvolume-based volumes**: Each PVC creates a btrfs subvolume
- **Snapshots**: Create VolumeSnapshots from existing PVCs
- **Cloning**: Clone existing PVCs via `dataSource` (same or cross-filesystem via btrfs send/receive)
- **Volume expansion**: Online capacity expansion via qgroup limits
- **Multi-pool support**: Configure multiple btrfs filesystems as named storage pools
- **Topology-aware**: Supports `WaitForFirstConsumer` binding mode
- **Idempotent operations**: All CSI operations follow the idempotency spec

## Architecture

This is a **single-node** CSI driver designed for local storage. It does not implement `ControllerPublish/Unpublish` or `NodeStage/Unstage` since subvolumes are already part of the mounted btrfs filesystem.

### Pool-Based Storage

The driver manages one or more btrfs filesystems as **storage pools**. Pools are discovered automatically by scanning a base directory (default `/var/lib/btrfs-csi`):

```
/var/lib/btrfs-csi/
├── default/   # btrfs filesystem mounted here → pool named "default"
└── fast/      # btrfs filesystem mounted here → pool named "fast"
```

Each immediate subdirectory that is a separate btrfs mountpoint becomes a pool. The driver watches for new or removed subdirectories every 30 seconds and hot-reloads without restart. No ConfigMap is required.

### CSI Services

| Service | Purpose |
|---------|---------|
| Identity | Plugin info, capabilities, health probe |
| Controller | Create/Delete volumes, snapshots, expansion |
| Node | Bind mount, volume stats, node info |

### Btrfs Concept Mapping

| CSI Concept | Btrfs Feature |
|-------------|---------------|
| Volume | Subvolume (`<poolPath>/volumes/<id>`) |
| Capacity | Qgroup limit |
| Snapshot | Readonly snapshot (`<poolPath>/snapshots/<id>`) |
| Clone | Writable snapshot |
| Mount | Bind mount |
| Pool | btrfs filesystem mount point |

### Driver Details

- **Driver name**: `btrfs.csi.local`
- **Topology key**: `topology.btrfs.csi.local/node`
- **Default pools dir**: `/var/lib/btrfs-csi` (override with `--pools-dir`)
- **State file**: `<poolPath>/state.json` (one per pool)

## Prerequisites

- Go 1.26+
- btrfs-progs utilities
- Kubernetes cluster (single-node like k0s recommended)
- Root access (required for btrfs operations and mount)

## Installation

### Build from Source

```bash
git clone https://github.com/mporrato/btrfs-csi.git
cd btrfs-csi
make build
```

Go 1.26+ is required. If your local toolchain is older, `make` uses `GOTOOLCHAIN=auto` to download the right version automatically.

### Build Container Image

```bash
make image
```

### Deploy to Kubernetes

Deployment uses kustomize overlays. Three overlays are provided:

| Overlay | Description |
|---------|-------------|
| `snapshot` | VolumeSnapshot CRDs + snapshot-controller (no driver); apply first |
| `default` | Driver + StorageClass + VolumeSnapshotClass (requires `snapshot` overlay) |
| `dev` | Like `default`, but uses a locally built image, verbose logging, and adds secondary StorageClass/VolumeSnapshotClass for multi-pool e2e testing |

**1. Prepare the node.** Mount the btrfs pool(s) and ensure the root filesystem has shared mount propagation (required for CSI bind mounts).

Check the current propagation mode:

```bash
findmnt -o TARGET,PROPAGATION /
```

If the output shows `private` instead of `shared`, enable it:

```bash
mount --make-rshared /
```

Most distributions (Fedora, Ubuntu, Debian) default to `shared`. Alpine Linux defaults to `private` and needs this step. To persist across reboots on Alpine:

```bash
echo "mount --make-rshared /" > /etc/local.d/shared-mounts.start
chmod +x /etc/local.d/shared-mounts.start
```

Mount a btrfs partition as a storage pool:

```bash
mkdir -p /var/lib/btrfs-csi/default
mount /dev/sdX /var/lib/btrfs-csi/default
btrfs quota enable /var/lib/btrfs-csi/default
```

**2. Install VolumeSnapshot CRDs and controller:**

```bash
make deploy OVERLAY=snapshot
```

**3. Deploy the driver** (requires the snapshot CRDs from step 2):

```bash
make deploy
```

#### Custom kubelet path

The manifests default to `/var/lib/kubelet`, which is correct for standard Kubernetes, minikube, and kind clusters. Some distributions use a different kubelet root directory:

| Distribution | Kubelet path |
|---|---|
| Standard / minikube / kind | `/var/lib/kubelet` (default) |
| k0s | `/var/lib/k0s/kubelet` |
| k3s | `/var/lib/rancher/k3s/agent/kubelet` |

To check your kubelet root:

```bash
ps aux | grep kubelet | grep -o '\--root-dir=[^ ]*'
```

If this prints nothing, the default `/var/lib/kubelet` is used. Otherwise, pass `KUBELET_DIR` when deploying:

```bash
make deploy OVERLAY=snapshot KUBELET_DIR=/var/lib/k0s/kubelet
make deploy KUBELET_DIR=/var/lib/k0s/kubelet
```

This automatically replaces all kubelet paths in the rendered manifests (hostPath volumes, container mountPaths, registration path, and the driver's `--kubelet-dir` flag).

## Usage

### StorageClass Example

```yaml
apiVersion: storage.k8s.io/v1
kind: StorageClass
metadata:
  name: btrfs
provisioner: btrfs.csi.local
volumeBindingMode: WaitForFirstConsumer
allowVolumeExpansion: true
parameters:
  pool: "default"  # references pool name (subdirectory of --pools-dir)
```

### PVC Example

```yaml
apiVersion: v1
kind: PersistentVolumeClaim
metadata:
  name: my-data
spec:
  storageClassName: btrfs
  accessModes:
    - ReadWriteOnce
  resources:
    requests:
      storage: 1Gi
```

### Snapshot Example

```yaml
apiVersion: snapshot.storage.k8s.io/v1
kind: VolumeSnapshot
metadata:
  name: my-snapshot
spec:
  volumeSnapshotClassName: btrfs-snapshot
  source:
    persistentVolumeClaimName: my-data
```

### Clone Example

```yaml
apiVersion: v1
kind: PersistentVolumeClaim
metadata:
  name: my-clone
spec:
  storageClassName: btrfs
  accessModes:
    - ReadWriteOnce
  resources:
    requests:
      storage: 1Gi
  dataSource:
    kind: PersistentVolumeClaim
    name: my-data
```

## Configuration

### Driver Flags

| Flag | Default | Description |
|------|---------|-------------|
| `--endpoint` | `unix:///csi/csi.sock` | CSI endpoint (Unix socket) |
| `--nodeid` | (auto-generated UUID) | Node ID for topology |
| `--pools-dir` | `/var/lib/btrfs-csi` | Base directory containing pool subdirectories |
| `--kubelet-dir` | `/var/lib/kubelet` | Kubelet base directory for target path validation |
| `--version` | (print and exit) | Print version |

Each immediate subdirectory of `--pools-dir` that is a separate btrfs mountpoint becomes a pool. The subdirectory name is the pool name.

### StorageClass Parameters

| Parameter | Description |
|-----------|-------------|
| `pool` | Pool name to use (must match a subdirectory of `--pools-dir`) |

## Development

### Running Tests

```bash
# Unit tests
make test

# Integration tests (requires root + btrfs)
make test-integration
```

Both targets use `GOTOOLCHAIN=auto`, so no container or pre-installed Go 1.26 is needed.

### Project Structure

```
btrfs-csi/
├── cmd/btrfs-csi-driver/        # Entry point
├── pkg/
│   ├── driver/                  # CSI gRPC services
│   ├── btrfs/                   # btrfs CLI wrapper
│   └── state/                   # JSON-backed metadata (MultiStore/FileStore)
├── deploy/
│   ├── base/                    # Core manifests (DaemonSet, RBAC, StorageClass, etc.)
│   ├── components/snapshotter/  # Upstream VolumeSnapshot CRDs + controller
│   └── overlays/
│       ├── default/             # Driver only (production)
│       ├── snapshot/            # VolumeSnapshot CRDs + controller (no driver)
│       └── dev/                 # Driver + local image + verbose logging + e2e classes
└── scripts/                     # Cluster setup and test runner scripts
```

### Key Interfaces

- `btrfs.Manager` — abstracts btrfs CLI operations
- `state.Store` — volume/snapshot metadata CRUD
- `Mounter` — abstracts bind mount/unmount

### Coding Conventions

- **TDD**: Strict Red-Green-Gray methodology
- **Error handling**: Wrap with `fmt.Errorf("operation: %w", err)`
- **gRPC errors**: Use `status.Errorf(codes.X, ...)`
- **Logging**: Use `klog.V(level).InfoS()` / `klog.ErrorS()`
- **Tests**: Hand-written mocks, no mocking frameworks

## Troubleshooting

### Driver Not Starting

- Ensure `--pools-dir` exists and contains at least one btrfs mountpoint subdirectory
- Verify the socket directory exists and is writable
- Check that `--nodeid` is provided (or accept auto-generated UUID)
- Verify btrfs-progs is installed: `btrfs --version`

### Pool Configuration Issues

- Each pool must be a **separate btrfs mount** at `<pools-dir>/<pool-name>` (a full filesystem or a subvolume mounted with `-o subvol=`)
- Verify the mount: `mount | grep btrfs`
- Ensure quota is enabled on each btrfs filesystem: `btrfs quota enable <path>`
- Check the pool path is a btrfs filesystem: `btrfs filesystem df <path>`
- Pool changes are detected automatically (30s polling interval)

### Volume Creation Fails

- Verify the pool's btrfs filesystem is mounted
- Check that the pool path exists and has correct permissions
- Ensure quota is enabled: `btrfs quota enable <path>`
- Check the pool is defined in StorageClass: `parameters.pool: "default"`

### Mount Errors

- The driver requires privileged access for mount operations
- Ensure bidirectional mount propagation is enabled (if using kind)
- Check kubelet logs: `kubectl logs -n btrfs-csi -l app=btrfs-csi-driver`

### Capacity Issues

- Capacity is enforced via qgroup limits, not filesystem-level sizing
- Verify qgroup is set: `btrfs qgroup show <path>`
- Check available space: `btrfs filesystem usage <path>`

## License

This is free and unencumbered software released into the public domain. See [LICENSE](LICENSE) for details.

## Contributing

Contributions are welcome. Please ensure:
- All tests pass (`make test`)
- Code follows Go style conventions (`gofmt`, `go vet`)
- New features include corresponding tests

## References

- [CSI Specification](https://github.com/container-storage-interface/spec)
- [Reference: kubernetes-csi/csi-driver-host-path](https://github.com/kubernetes-csi/csi-driver-host-path)
