# Antrea PacketCapture Controller

A Kubernetes DaemonSet controller that performs on-demand packet captures using tcpdump, triggered by Pod annotations.

## How It Works

1. Controller runs as a DaemonSet — one instance per node, watching only local Pods.
2. Annotate a Pod with `tcpdump.antrea.io: "<N>"` to start capturing (N = max rotated pcap files).
3. Remove the annotation to stop the capture and auto-delete all pcap files.

## Quick Start

```bash
# 1. Create Kind cluster (CNI disabled) and install Antrea
kind create cluster --name antrea-capture --config kind-config.yaml
helm repo add antrea https://charts.antrea.io && helm repo update
helm install antrea antrea/antrea --namespace kube-system

# 2. Build and load the controller image
docker build -t packet-capture-controller:latest .
kind load docker-image packet-capture-controller:latest --name antrea-capture

# 3. Deploy RBAC + DaemonSet
kubectl apply -f manifests/rbac.yaml
kubectl apply -f manifests/daemonset.yaml

# 4. Deploy a test pod and start capture
kubectl apply -f manifests/test-pod.yaml
kubectl annotate pod test-pod tcpdump.antrea.io="5"

# 5. Stop capture
kubectl annotate pod test-pod tcpdump.antrea.io-
```

## Repository Structure

```
.
├── main.go                 # Controller source code (Go)
├── Dockerfile              # Multi-stage Docker build
├── go.mod / go.sum         # Go module dependencies
├── kind-config.yaml        # Kind cluster config (CNI disabled)
├── manifests/
│   ├── rbac.yaml           # ServiceAccount, ClusterRole, ClusterRoleBinding
│   ├── daemonset.yaml      # DaemonSet manifest
│   └── test-pod.yaml       # Test Pod with traffic generation
├── pod-describe.txt        # kubectl describe output of annotated test Pod
├── pods.txt                # kubectl get pods -A output
├── capture-files.txt       # ls -l of pcap files inside capture Pod
├── capture.pcap            # Extracted pcap file from capture Pod
├── capture-output.txt      # Human-readable tcpdump -r output
└── DEVELOPMENT_REPORT.md   # Detailed step-by-step development log
```

## Verification Evidence

The following deliverable files are included in this repo:

| File | Description |
|---|---|
| `pod-describe.txt` | `kubectl describe` output for the annotated test Pod |
| `pods.txt` | `kubectl get pods -A` showing all running pods |
| `capture-files.txt` | `ls -l /capture-*` showing non-empty pcap files inside the capture Pod |
| `capture.pcap` | Extracted pcap file (viewable with Wireshark or tcpdump) |
| `capture-output.txt` | Human-readable output from `tcpdump -r capture.pcap` |

## Tech Stack

- **Go** with `client-go` (Kubernetes informers)
- **tcpdump** for packet capture
- **Kind** + **Antrea CNI** for the local cluster
- **Docker** multi-stage build (ubuntu:24.04 base)
