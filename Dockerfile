# Stage 1: Build — runs natively on the build host, cross-compiles for the target
FROM --platform=$BUILDPLATFORM golang:1.26-alpine AS builder

ARG TARGETOS
ARG TARGETARCH

WORKDIR /src

COPY go.mod go.sum ./
RUN go mod download

COPY . .
RUN CGO_ENABLED=0 GOOS=${TARGETOS} GOARCH=${TARGETARCH} go build -trimpath -ldflags "-w -s" -o /bin/btrfs-csi-driver ./cmd/btrfs-csi-driver/

# Stage 2: Runtime — uses QEMU only for apk on non-native platforms
FROM alpine:3.23

RUN apk add --no-cache \
    btrfs-progs \
    util-linux

COPY --from=builder /bin/btrfs-csi-driver /bin/btrfs-csi-driver

ENTRYPOINT ["/bin/btrfs-csi-driver"]
