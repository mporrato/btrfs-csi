CLUSTER    ?= btrfs-csi
OVERLAY    ?= minikube
RUNTIME    ?= $(shell command -v podman 2>/dev/null || command -v docker 2>/dev/null)
PRECOMMIT  ?= $(shell command -v prek 2>/dev/null || command -v pre-commit 2>/dev/null)
GOVERSION  ?= $(shell grep '^go ' go.mod | awk '{print $$2}')
GOIMAGE    := golang:$(GOVERSION)-alpine

GOCACHE    := gocache
RUNGO      := $(RUNTIME) run --rm --security-opt label=disable -v '$(shell pwd):/src' -v '$(GOCACHE):/go/pkg/mod' -w /src $(GOIMAGE)

.PHONY: build test test-integration lint mod image deploy \
        minikube-up minikube-down minikube-sanity minikube-e2e

lint:
	$(PRECOMMIT) run --all-files

mod:
	$(RUNGO) go mod tidy

build:
	$(RUNGO) go build -trimpath -o bin/btrfs-csi-driver ./cmd/btrfs-csi-driver/

test:
	$(RUNGO) go test ./...

# Runs btrfs integration tests — requires root + btrfs on the local machine.
# Use minikube-sanity instead to run without host root.
test-integration:
	$(RUNGO) go test -tags integration ./pkg/btrfs/

image:
	$(RUNTIME) build -t localhost/btrfs-csi-driver:latest .

deploy:
	kubectl apply -k deploy/overlays/$(OVERLAY)/

# Start a minikube cluster with QEMU driver, set up btrfs on the extra disks,
# load the driver image, and deploy all manifests.
minikube-up:
	CLUSTER=$(CLUSTER) bash scripts/minikube-up.sh

minikube-down:
	CLUSTER=$(CLUSTER) bash scripts/minikube-down.sh

# Build the CSI sanity test binary and run it inside the minikube VM.
minikube-sanity:
	CLUSTER=$(CLUSTER) bash scripts/sanity.sh

# Run end-to-end tests against the deployed cluster.
minikube-e2e:
	KUBECTL="kubectl --context=$(CLUSTER)" bash scripts/e2e.sh

clean:
	rm -rf bin/
