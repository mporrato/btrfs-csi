IMAGE   ?= btrfs-csi-driver
TAG     ?= latest
RUNTIME ?= $(shell command -v podman 2>/dev/null || command -v docker 2>/dev/null)

.PHONY: build test test-integration image deploy clean

build:
	go build -o bin/btrfs-csi-driver ./cmd/btrfs-csi-driver/

test:
	go test ./...

test-integration:
	go test -tags integration ./pkg/btrfs/

image:
	$(RUNTIME) build -t $(IMAGE):$(TAG) .

deploy:
	kubectl apply -f deploy/

clean:
	rm -rf bin/
