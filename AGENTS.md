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
  base/                          # Core manifests (DaemonSet, RBAC, StorageClass, etc.)
  components/snapshotter/        # Upstream VolumeSnapshot CRDs + controller (kustomize Component)
  overlays/
    snapshot/                    # VolumeSnapshot CRDs + controller (no driver; apply first)
    default/                     # Driver + StorageClass + VolumeSnapshotClass (production)
    dev/                         # Like default + local image + verbose logging + secondary e2e classes
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
# Minimal deployment (no StorageClass, no snapshots)
make deploy OVERLAY=minimal              # Driver only

# Full deployment with VolumeSnapshot support
make deploy OVERLAY=snapshot             # VolumeSnapshot CRDs + controller (apply first)
make deploy                              # Driver + VolumeSnapshot support
make deploy OVERLAY=storageclass         # StorageClass (optional)
make deploy OVERLAY=volumesnapshotclass  # VolumeSnapshotClass (optional)

# Custom kubelet path (e.g., k0s)
make deploy OVERLAY=snapshot KUBELET_DIR=/var/lib/k0s/kubelet
make deploy KUBELET_DIR=/var/lib/k0s/kubelet
make deploy OVERLAY=minimal KUBELET_DIR=/var/lib/k0s/kubelet

# Development (minikube with verbose logging and secondary pool)
make minikube-up        # Automatically uses deploy/overlays/dev/
make minikube-e2e       # Run end-to-end tests

# Teardown dev cluster
make minikube-down
```

Six overlays: `snapshot` (CRDs + controller, no driver; apply first), `minimal` (driver only, no classes, no snapshot), `default` (driver + snapshot support, no classes), `storageclass` (standalone StorageClass), `volumesnapshotclass` (standalone VolumeSnapshotClass), and `dev` (like default + all classes + secondary e2e classes).

Manifests default to `/var/lib/kubelet`. Set `KUBELET_DIR` to override this for distributions that use a different path (e.g., k0s uses `/var/lib/k0s/kubelet`). This replaces all kubelet paths in the rendered manifests — hostPath volumes, container mountPaths, registration path, and the driver's `--kubelet-dir` flag — in one step.

## Toolchain and Pre-commit Checks

All Go commands use `GOTOOLCHAIN=auto` (set in the Makefile and pre-commit hooks) so the correct Go version is downloaded automatically if the locally installed toolchain is older than what `go.mod` requires.

**golangci-lint gotcha**: `golangci-lint` is a pre-compiled binary that embeds its own Go version. If it was installed with an older toolchain it will refuse to lint code targeting a newer Go version with:

```
can't load config: the Go language version used to build golangci-lint is lower than the targeted Go version
```

Fix by reinstalling it with the correct toolchain:

```bash
GOTOOLCHAIN=auto go install github.com/golangci/golangci-lint/v2/cmd/golangci-lint@v2.11.3
```

The pre-commit hook passes `GOTOOLCHAIN=auto` to `golangci-lint run`, but that only affects child processes spawned by lint rules — it does not change the version of Go that golangci-lint itself was compiled with. The binary must be rebuilt to pick up a newer toolchain.

## Important Context

- This is a **single-node** driver: no `ControllerPublish/Unpublish`, no `NodeStage/Unstage`
- The btrfs layer uses CLI (`btrfs` command) via `os/exec`, not kernel ioctls
- Quota enforcement uses simple quotas (`--simple`) when available, falls back to traditional qgroups
- All CSI operations must be **idempotent** per the CSI spec
- Capacity is enforced via qgroup limits, not filesystem-level sizing
- Storage capacity tracking is enabled (`--enable-capacity`): the provisioner reports `CSIStorageCapacity` objects to prevent over-provisioning
