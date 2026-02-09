# Multi-stage build for smaller final image
FROM golang:1.24 AS builder

WORKDIR /workspace

# Copy go mod files
COPY go.mod go.sum ./

# Download dependencies
RUN go mod download

# Copy source code
COPY main.go ./

# Build static binary
RUN CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build \
    -ldflags="-w -s" \
    -o packet-capture-controller \
    .

# Final runtime image
FROM ubuntu:24.04

# Install required packages
RUN apt-get update && \
    apt-get install -y --no-install-recommends \
        bash \
        tcpdump \
        ca-certificates && \
    rm -rf /var/lib/apt/lists/*

# Copy binary from builder
COPY --from=builder /workspace/packet-capture-controller /usr/local/bin/packet-capture-controller

# Set executable permissions
RUN chmod +x /usr/local/bin/packet-capture-controller

# Run as root (required for tcpdump)
USER root

ENTRYPOINT ["/usr/local/bin/packet-capture-controller"]
