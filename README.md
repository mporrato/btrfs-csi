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

The driver manages one or more btrfs filesystems as **storage pools**. Pools are defined via a configuration directory (typically mounted from a ConfigMap):

```
/etc/btrfs-csi/pools/
├── default    # contains: /mnt/btrfs-default
└── fast       # contains: /mnt/btrfs-nvme
```

Each file in the config directory represents a pool:
- Filename = pool name
- File content = absolute path to a btrfs filesystem

The driver watches this directory for changes and hot-reloads pool definitions when the ConfigMap updates.

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
- **Default base path**: None (pools must be configured via `--config`)
- **State file**: `<poolPath>/state.json` (one per pool)

## Prerequisites

- Go 1.25.8+
- btrfs-progs utilities
- Kubernetes cluster (single-node like k0s recommended)
- Root access (required for btrfs operations and mount)

## Installation

### Build from Source

```bash
git clone https://github.com/mporrato/btrfs-csi.git
cd btrfs-csi
go build ./cmd/btrfs-csi-driver/
```

### Build Container Image

```bash
make image
```

### Deploy to Kubernetes

```bash
make deploy
```

Or manually apply manifests:

```bash
kubectl apply -f deploy/
```

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
  pool: "default"  # references pool name from --config directory
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
| `--config` | (required) | Path to directory with pool definitions |
| `--version` | (print and exit) | Print version |

The `--config` directory should contain files where each filename is a pool name and the file content is the absolute path to a btrfs filesystem mount point.

### StorageClass Parameters

| Parameter | Description |
|-----------|-------------|
| `pool` | Pool name to use (must exist in --config directory) |

## Development

### Running Tests

```bash
# Unit tests
go test ./...

# Integration tests (requires root + btrfs)
go test -tags integration ./pkg/btrfs/
```

### Project Structure

```
btrfs-csi/
├── cmd/btrfs-csi-driver/    # Entry point
├── pkg/
│   ├── driver/              # CSI gRPC services
│   ├── btrfs/               # btrfs CLI wrapper
│   └── state/               # JSON-backed metadata (MultiStore/FileStore)
├── deploy/                  # Kubernetes manifests
└── test/                    # Kind cluster config, e2e helpers
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

- Ensure the `--config` flag points to a valid directory with pool definitions
- Verify the socket directory exists and is writable
- Check that `--nodeid` is provided (or accept auto-generated UUID)
- Verify btrfs-progs is installed: `btrfs --version`

### Pool Configuration Issues

- Each pool file must contain an absolute path to a btrfs filesystem
- Verify the path is mounted: `mount | grep btrfs`
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
- All tests pass (`go test ./...`)
- Code follows Go style conventions (`gofmt`, `go vet`)
- New features include corresponding tests

## References

- [CSI Specification](https://github.com/container-storage-interface/spec)
- [Reference: kubernetes-csi/csi-driver-host-path](https://github.com/kubernetes-csi/csi-driver-host-path)
