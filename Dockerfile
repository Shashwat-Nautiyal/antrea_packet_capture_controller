# Build stage
FROM golang:1.24 AS builder
WORKDIR /workspace
COPY go.mod go.sum ./
RUN go mod download
COPY main.go ./
RUN CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build \
    -ldflags="-w -s" -o packet-capture-controller .

# Runtime stage — ubuntu base required for bash + tcpdump
FROM ubuntu:24.04
RUN apt-get update && \
    apt-get install -y --no-install-recommends bash tcpdump ca-certificates && \
    rm -rf /var/lib/apt/lists/*
COPY --from=builder /workspace/packet-capture-controller /usr/local/bin/
ENTRYPOINT ["packet-capture-controller"]
