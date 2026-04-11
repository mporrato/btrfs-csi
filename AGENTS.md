# CLAUDE.md — Project Instructions for AI Agents

## Project Overview

btrfs-csi is a Kubernetes CSI (Container Storage Interface) storage driver for single-node clusters (e.g., k0s) that leverages btrfs features: subvolumes, snapshots, qgroups, and copy-on-write.

Written in Go. Single binary serving Identity, Controller, and Node gRPC services over a Unix socket.

## Development Methodology

**Strict Red-Green-Gray TDD** for all production code:
1. **Red**: Write a failing test first — no production code without one
2. **Green**: Write the minimum code to make the test pass
3. **Gray** (Refactor): Clean up while keeping tests green

Run `make test` after every Green and Gray step.

## Project Structure

```
cmd/btrfs-csi-driver/main.go    # Entry point, flags
pkg/driver/                      # CSI gRPC service implementations
pkg/btrfs/                       # btrfs CLI wrapper (Manager interface)
pkg/state/                       # JSON-backed volume/snapshot metadata
deploy/
  base/                          # Common Kubernetes manifests (CSIDriver, RBAC, etc.)
  overlays/
    minikube/                    # Minikube cluster (standard /var/lib/kubelet)
    kind/                        # Kind cluster (standard /var/lib/kubelet)
    k0s/                         # k0s cluster (patches to /var/lib/k0s/kubelet)
    k3s/                         # k3s cluster (patches to /var/lib/rancher/k3s/agent/kubelet)
    dev/                         # Development: minikube + verbose logging + secondary pool
scripts/                         # Cluster setup and test runner scripts
```

## Key Interfaces

- `btrfs.Manager` — abstracts all btrfs CLI operations; mock in `pkg/btrfs/mock.go` for unit tests
- `state.Store` — volume/snapshot metadata CRUD; backed by JSON file
- `Mounter` — abstracts bind mount/unmount for testability

## Coding Conventions

- **Go style**: follow standard `gofmt`/`go vet` conventions
- **Error handling**: wrap errors with context using `fmt.Errorf("operation: %w", err)`
- **gRPC errors**: use `status.Errorf(codes.X, ...)` from `google.golang.org/grpc/status`
- **Logging**: use `klog.V(level).InfoS()` / `klog.ErrorS()` structured logging
- **Test files**: `*_test.go` alongside production code, same package
- **Integration tests**: gated with `//go:build integration` build tag
- **No mocking frameworks**: use hand-written mocks (simple structs implementing interfaces)

## CSI Driver Details

- **Driver name**: `btrfs.csi.local`
- **Topology key**: `topology.btrfs.csi.local/node`
- **Pool configuration**: via `--pools-dir` flag (default `/var/lib/btrfs-csi`)
  - Each immediate subdirectory of `--pools-dir` is a pool; subdirectory name = pool name, path = pool base path
  - Example: `/var/lib/btrfs-csi/default` is a btrfs mount point → pool named `default`
  - No ConfigMap needed; the driver discovers pools by scanning subdirectories at startup and every 30 s
- **Volumes**: btrfs subvolumes under `<poolPath>/volumes/<id>`
- **Snapshots**: readonly btrfs snapshots under `<poolPath>/snapshots/<id>`
- **State**: JSON file at `<poolPath>/state.json` (one per pool)

## Deployment

Use kustomize overlays to deploy to different environments:

```bash
# Development (minikube with verbose logging and secondary pool)
make minikube-up        # Automatically uses deploy/overlays/dev/
make minikube-e2e       # Run end-to-end tests

# Production deployment (choose your platform)
make deploy OVERLAY=k0s         # Deploy to k0s cluster
make deploy OVERLAY=k3s         # Deploy to k3s cluster
make deploy OVERLAY=minikube    # Deploy to minikube
make deploy OVERLAY=kind        # Deploy to kind cluster

# Teardown dev cluster
make minikube-down
```

The overlays handle platform-specific kubelet paths and configuration. The `dev` overlay adds:
- `--v=4` verbose driver logging
- Local image (`localhost/btrfs-csi-driver:latest`) with `imagePullPolicy: Never`
- Secondary StorageClass for multi-pool testing (mount btrfs at `/var/lib/btrfs-csi/secondary`)

## Important Context

- This is a **single-node** driver: no `ControllerPublish/Unpublish`, no `NodeStage/Unstage`
- The btrfs layer uses CLI (`btrfs` command) via `os/exec`, not kernel ioctls
- Quota enforcement uses simple quotas (`--simple`) when available, falls back to traditional qgroups
- All CSI operations must be **idempotent** per the CSI spec
- Capacity is enforced via qgroup limits, not filesystem-level sizing
- Storage capacity tracking is enabled (`--enable-capacity`): the provisioner reports `CSIStorageCapacity` objects to prevent over-provisioning
