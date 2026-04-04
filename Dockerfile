# Stage 1: Build
FROM golang:1.25 AS builder

WORKDIR /src

COPY go.mod go.sum ./
RUN go mod download

COPY . .
RUN CGO_ENABLED=0 GOOS=linux go build -o /bin/btrfs-csi-driver ./cmd/btrfs-csi-driver/

# Stage 2: Runtime
FROM alpine:3.23

RUN apk add --no-cache \
    btrfs-progs \
    util-linux

COPY --from=builder /bin/btrfs-csi-driver /bin/btrfs-csi-driver

ENTRYPOINT ["/bin/btrfs-csi-driver"]
