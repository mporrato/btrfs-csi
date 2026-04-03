# CLAUDE.md — Project Instructions for AI Agents

## Project Overview

btrfs-csi is a Kubernetes CSI (Container Storage Interface) storage driver for single-node clusters (e.g., k0s) that leverages btrfs features: subvolumes, snapshots, qgroups, and copy-on-write.

Written in Go. Single binary serving Identity, Controller, and Node gRPC services over a Unix socket.

## Development Methodology

**Strict Red-Green-Gray TDD** for all production code:
1. **Red**: Write a failing test first — no production code without one
2. **Green**: Write the minimum code to make the test pass
3. **Gray** (Refactor): Clean up while keeping tests green

Run `go test ./...` after every Green and Gray step.

## Project Structure

```
cmd/btrfs-csi-driver/main.go    # Entry point, flags
pkg/driver/                      # CSI gRPC service implementations
pkg/btrfs/                       # btrfs CLI wrapper (Manager interface)
pkg/state/                       # JSON-backed volume/snapshot metadata
deploy/                          # Kubernetes manifests
test/                            # Kind cluster config, e2e helpers
docs/                            # Architecture doc + task breakdown
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

## Build Commands

- `go build ./cmd/btrfs-csi-driver/` — compile the binary
- `go test ./...` — run unit tests
- `go test -tags integration ./pkg/btrfs/` — run btrfs integration tests (requires root + btrfs)
- `make image` — build container image
- `make deploy` — apply Kubernetes manifests

## CSI Driver Details

- **Driver name**: `btrfs.csi.local`
- **Topology key**: `topology.btrfs.csi.local/node`
- **Default base path**: `/var/lib/btrfs-csi` (configurable via `--root-path` flag and StorageClass `parameters.basePath`)
- **Volumes**: btrfs subvolumes under `<basePath>/volumes/<id>`
- **Snapshots**: readonly btrfs snapshots under `<basePath>/snapshots/<id>`
- **State**: JSON file at `<basePath>/state.json`

## Important Context

- This is a **single-node** driver: no `ControllerPublish/Unpublish`, no `NodeStage/Unstage`
- The btrfs layer uses CLI (`btrfs` command) via `os/exec`, not kernel ioctls
- Quota enforcement uses simple quotas (`--simple`) when available, falls back to traditional qgroups
- All CSI operations must be **idempotent** per the CSI spec
- Capacity is enforced via qgroup limits, not filesystem-level sizing

## Task Tracking

See `docs/tasks.md` for the full phase-by-phase task breakdown with acceptance criteria.
See `docs/architecture.md` for the detailed architecture document.
