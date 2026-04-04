IMAGE     ?= btrfs-csi-driver
TAG       ?= latest
CLUSTER   ?= btrfs-csi
RUNTIME   ?= $(shell command -v podman 2>/dev/null || command -v docker 2>/dev/null)
PRECOMMIT ?= $(shell command -v prek 2>/dev/null || command -v pre-commit 2>/dev/null)

.PHONY: build test test-integration lint image deploy clean \
        minikube-setup minikube-sanity minikube-e2e

lint:
	$(PRECOMMIT) run --all-files

build:
	go build -o bin/btrfs-csi-driver ./cmd/btrfs-csi-driver/

test:
	go test ./...

# Runs btrfs integration tests — requires root + btrfs on the local machine.
# Use minikube-sanity instead to run without host root.
test-integration:
	go test -tags integration ./pkg/btrfs/

image:
	$(RUNTIME) build -t localhost/$(IMAGE):$(TAG) .

deploy:
	kubectl apply -f deploy/

# Start a minikube cluster with QEMU driver, set up btrfs on the extra disk,
# load the driver image, and deploy all manifests.
minikube-setup:
	IMAGE=localhost/$(IMAGE):$(TAG) CLUSTER=$(CLUSTER) bash test/setup-minikube.sh

# Build the CSI sanity test binary and run it inside the minikube VM.
minikube-sanity:
	CLUSTER=$(CLUSTER) bash test/run-sanity.sh

# Run end-to-end tests against the deployed cluster.
minikube-e2e:
	KUBECTL="kubectl --context=$(CLUSTER)" bash test/e2e.sh

clean:
	rm -rf bin/
