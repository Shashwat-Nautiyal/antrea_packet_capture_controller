## Goal

The goal of this task is to produce a poor-man's version of the Antrea PacketCapture feature. You will implement a Kubernetes controller that runs as a DaemonSet and performs packet captures on demand.

## High-level guidelines:

- Cluster Setup: Create a Kind cluster. You will need to disable the default CNI, as we will later deploy Antrea for networking. Refer to the Kind documentation for information. Deploy Antrea to the cluster; we recommend using Helm to install Antrea.
- Controller Implementation: Write a simple Go program which implements a K8s controller.
The program will run as a DaemonSet. It will watch all Pods running locally on the same Node. Ensure your DaemonSet has the necessary RBAC permissions to watch Pods.
It will run packet capture with tcpdump on demand when a certain annotation is added to the Pod.
The annotation will use format "tcpdump.antrea.io": "<N>".
N represents the max number of capture files. To implement this, your program will invoke - tcpdump as follows: tcpdump -C 1M -W <N> -w /capture-<Pod name>.pcap.
Packet capture will stop as soon as the annotation is deleted. Pcap files will need to be deleted by your controller when the capture stops.
- Containerization: Define a Dockerfile for your program. Use ubuntu:24.04 as the base image. The container will need to include bash, tcpdump and your compiled Go program. Follow best practices when defining the Dockerfile (you can refer to the antrea-controller Dockerfile for an example). Load the image into your Kind cluster (e.g., using kind load docker-image).

## Verification:

Deploy a test Pod that generates network traffic (e.g., using ping or curl in a loop).
Annotate the test Pod with tcpdump.antrea.io: "5" (or another number).
Verify that the capture has started: exec into the capture Pod running on the same Node as your test Pod and check for the presence of pcap files (e.g., ls -l /capture-*).
Remove the annotation from the test Pod.
Verify that the capture has stopped and the pcap files have been deleted.
Deliverables:

## Create a GitHub repository with the following:

Go source code.
Dockerfile and optionally a Makefile.
Manifest for the capture DaemonSet.
Manifest for the test Pod.
Output of kubectl describe for the test Pod once it is running and annotated (save as pod-describe.txt).
Output of kubectl get pods -A (save as pods.txt).
Output of ls -l /capture-* from inside the capture Pod showing at least one non-empty pcap file (save as capture-files.txt).
A copy of the pcap file extracted from the capture Pod (you can use kubectl cp).
The human-readable text output of the pcap file, obtained by running tcpdump -r <file> on the extracted file (save as capture-output.txt).
A short README.md
Organization of the repository is left to your discretion. Keep it simple, concise, and well-organized.