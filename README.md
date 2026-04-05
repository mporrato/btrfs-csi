# Btrfs CSI Driver

[![Go Version](https://img.shields.io/github/go-mod/go-version/mporrato/btrfs-csi)](https://github.com/mporrato/btrfs-csi)
[![License: Unlicense](https://img.shields.io/badge/License-Unlicense-green.svg)](LICENSE)

A Kubernetes CSI (Container Storage Interface) storage driver for single-node clusters (e.g., k0s) that leverages btrfs features: subvolumes, snapshots, qgroups, and copy-on-write.

Written in Go. Single binary serving Identity, Controller, and Node gRPC services over a Unix socket.

## Features

- **Subvolume-based volumes**: Each PVC creates a btrfs subvolume
- **Snapshots**: Create VolumeSnapshots from existing PVCs
- **Cloning**: Clone existing PVCs via `dataSource`
- **Volume expansion**: Online capacity expansion via qgroup limits
- **Topology-aware**: Supports `WaitForFirstConsumer` binding mode
- **Idempotent operations**: All CSI operations follow the idempotency spec

## Architecture

This is a **single-node** CSI driver designed for local storage. It does not implement `ControllerPublish/Unpublish` or `NodeStage/Unstage` since subvolumes are already part of the mounted btrfs filesystem.

### CSI Services

| Service | Purpose |
|---------|---------|
| Identity | Plugin info, capabilities, health probe |
| Controller | Create/Delete volumes, snapshots, expansion |
| Node | Bind mount, volume stats, node info |

### Btrfs Concept Mapping

| CSI Concept | Btrfs Feature |
|-------------|---------------|
| Volume | Subvolume (`<basePath>/volumes/<id>`) |
| Capacity | Qgroup limit |
| Snapshot | Readonly snapshot (`<basePath>/snapshots/<id>`) |
| Clone | Writable snapshot |
| Mount | Bind mount |

### Driver Details

- **Driver name**: `btrfs.csi.local`
- **Topology key**: `topology.btrfs.csi.local/node`
- **Default base path**: `/var/lib/btrfs-csi`
- **State file**: `<basePath>/state.json`

## Prerequisites

- Go 1.25+
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
  basePath: "/var/lib/btrfs-csi"  # optional, defaults to --root-path flag
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
| `--endpoint` | `/csi/csi.sock` | CSI endpoint (Unix socket) |
| `--nodeid` | (required) | Node ID for topology |
| `--root-path` | `/var/lib/btrfs-csi` | Default btrfs base path |
| `--version` | (print and exit) | Print version |

### StorageClass Parameters

| Parameter | Description |
|-----------|-------------|
| `basePath` | Override default root path (per StorageClass) |

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
│   └── state/               # JSON-backed metadata
├── deploy/                  # Kubernetes manifests
├── test/                    # Kind cluster config
└── docs/                    # Architecture & tasks
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

- Ensure the socket directory exists and is writable
- Check that `--nodeid` is provided
- Verify btrfs-progs is installed: `btrfs --version`

### Volume Creation Fails

- Verify the btrfs filesystem is mounted at the configured base path
- Check that the base path directory exists and has correct permissions
- Ensure quota is enabled: `btrfs quota enable <path>`

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
