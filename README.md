# Antrea PacketCapture Controller

A Kubernetes DaemonSet controller that performs on-demand packet captures using tcpdump, triggered by Pod annotations.

## How It Works

- The controller runs as a **DaemonSet** — one instance per node, watching only Pods on its own node via a field-selector informer.
- Annotate any running Pod with `tcpdump.antrea.io: "<N>"` to start a capture, where `N` is the maximum number of rotated pcap files (1 MB each).
- Remove the annotation to stop the capture. The controller automatically terminates tcpdump and cleans up all pcap files.

## Prerequisites

- [Kind](https://kind.sigs.k8s.io/) (local Kubernetes cluster)
- [Helm](https://helm.sh/) (for Antrea installation)
- Docker
- Go 1.24+

## Usage

```bash
# 1. Create a Kind cluster with CNI disabled, then install Antrea
kind create cluster --name antrea-capture --config kind-config.yaml
helm repo add antrea https://charts.antrea.io && helm repo update
helm install antrea antrea/antrea -n kube-system

# 2. Build the controller image and load it into Kind
docker build -t packet-capture-controller:latest .
kind load docker-image packet-capture-controller:latest --name antrea-capture

# 3. Deploy the controller (RBAC + DaemonSet)
kubectl apply -f manifests/rbac.yaml
kubectl apply -f manifests/daemonset.yaml

# 4. Deploy a test pod that generates traffic, then start capture
kubectl apply -f manifests/test-pod.yaml
kubectl annotate pod test-pod tcpdump.antrea.io="5"

# 5. Verify capture files exist inside the DaemonSet pod
NODE=$(kubectl get pod test-pod -o jsonpath='{.spec.nodeName}')
CAP_POD=$(kubectl get pods -n kube-system -l app=packet-capture -o json \
  | jq -r ".items[] | select(.spec.nodeName==\"$NODE\") | .metadata.name")
kubectl exec -n kube-system $CAP_POD -- ls -lh /captures/

# 6. Stop capture (removes annotation → controller deletes pcap files)
kubectl annotate pod test-pod tcpdump.antrea.io-
```

## Repo Layout

| Path | Description |
|---|---|
| `main.go` | Controller source — watches Pods, manages tcpdump processes |
| `Dockerfile` | Multi-stage build: `golang:1.24` → `ubuntu:24.04` |
| `kind-config.yaml` | Kind cluster config (default CNI disabled, 3 nodes) |
| `manifests/rbac.yaml` | ServiceAccount, ClusterRole, ClusterRoleBinding |
| `manifests/daemonset.yaml` | DaemonSet with hostNetwork, privileged, emptyDir for captures |
| `manifests/test-pod.yaml` | BusyBox pod that pings 8.8.8.8 in a loop |

## Verification Artifacts

These files are included as evidence that the controller works end-to-end:

| File | Contents |
|---|---|
| `pod-describe.txt` | `kubectl describe pod test-pod` while annotated |
| `pods.txt` | `kubectl get pods -A` |
| `capture-files.txt` | `ls -l /captures/` showing non-empty pcap files |
| `capture.pcap` | Extracted pcap file (viewable with Wireshark / tcpdump) |
| `capture-output.txt` | Human-readable `tcpdump -r` output |
