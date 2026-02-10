package main

import (
	"context"
	"fmt"
	"log"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/fields"
	"k8s.io/client-go/informers"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/cache"
)

const (
	annotationKey = "tcpdump.antrea.io"
	captureDir    = "/captures"
)

// CaptureManager watches Pods on its node and manages tcpdump processes
// based on the presence of the tcpdump.antrea.io annotation.
type CaptureManager struct {
	clientset *kubernetes.Clientset
	nodeName  string
	mu        sync.Mutex
	captures  map[string]*CaptureProcess
}

type CaptureProcess struct {
	cmd    *exec.Cmd
	cancel context.CancelFunc
	files  []string
}

func main() {
	nodeName := os.Getenv("NODE_NAME")
	if nodeName == "" {
		log.Fatal("NODE_NAME environment variable is required")
	}
	log.Printf("Starting packet-capture controller on node %s", nodeName)

	config, err := rest.InClusterConfig()
	if err != nil {
		log.Fatalf("Failed to create in-cluster config: %v", err)
	}
	clientset, err := kubernetes.NewForConfig(config)
	if err != nil {
		log.Fatalf("Failed to create clientset: %v", err)
	}

	mgr := &CaptureManager{
		clientset: clientset,
		nodeName:  nodeName,
		captures:  make(map[string]*CaptureProcess),
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// Graceful shutdown: stop all captures before exiting
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
	go func() {
		<-sigCh
		log.Println("Shutting down...")
		mgr.cleanupAll()
		cancel()
	}()

	mgr.watchPods(ctx)
}

// watchPods sets up a Pod informer filtered to this node via a field selector.
// This ensures each DaemonSet instance only processes Pods on its own node.
func (m *CaptureManager) watchPods(ctx context.Context) {
	selector := fields.OneTermEqualSelector("spec.nodeName", m.nodeName).String()
	factory := informers.NewSharedInformerFactoryWithOptions(
		m.clientset, 30*time.Second,
		informers.WithTweakListOptions(func(opts *metav1.ListOptions) {
			opts.FieldSelector = selector
		}),
	)

	inf := factory.Core().V1().Pods().Informer()
	inf.AddEventHandler(cache.ResourceEventHandlerFuncs{
		AddFunc:    func(obj interface{}) { m.handlePod(obj.(*corev1.Pod)) },
		UpdateFunc: func(_, obj interface{}) { m.handlePod(obj.(*corev1.Pod)) },
		DeleteFunc: func(obj interface{}) { m.handleDelete(obj.(*corev1.Pod)) },
	})

	factory.Start(ctx.Done())
	if !cache.WaitForCacheSync(ctx.Done(), inf.HasSynced) {
		log.Fatal("Failed to sync informer cache")
	}
	log.Println("Watching for pod annotation changes...")
	<-ctx.Done()
}

// handlePod starts or stops a capture based on annotation presence.
func (m *CaptureManager) handlePod(pod *corev1.Pod) {
	if pod.Status.Phase != corev1.PodRunning {
		return
	}

	key := fmt.Sprintf("%s/%s", pod.Namespace, pod.Name)
	val, annotated := pod.Annotations[annotationKey]

	m.mu.Lock()
	defer m.mu.Unlock()

	_, capturing := m.captures[key]

	switch {
	case annotated && !capturing:
		log.Printf("Starting capture for %s (max files: %s)", key, val)
		m.startCapture(pod, val)
	case !annotated && capturing:
		log.Printf("Stopping capture for %s", key)
		m.stopCapture(key)
	}
}

func (m *CaptureManager) handleDelete(pod *corev1.Pod) {
	key := fmt.Sprintf("%s/%s", pod.Namespace, pod.Name)
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, ok := m.captures[key]; ok {
		log.Printf("Pod %s deleted, stopping capture", key)
		m.stopCapture(key)
	}
}

// startCapture spawns a tcpdump process with file rotation:
//   -C 1   rotate after 1 million bytes (~1MB)
//   -W N   keep at most N rotated files
//   -i any capture on all interfaces
func (m *CaptureManager) startCapture(pod *corev1.Pod, val string) {
	maxFiles, err := strconv.Atoi(strings.TrimSpace(val))
	if err != nil || maxFiles <= 0 {
		log.Printf("Invalid annotation value %q for %s/%s", val, pod.Namespace, pod.Name)
		return
	}

	key := fmt.Sprintf("%s/%s", pod.Namespace, pod.Name)
	pcapPath := filepath.Join(captureDir, fmt.Sprintf("capture-%s.pcap", pod.Name))

	ctx, cancel := context.WithCancel(context.Background())
	cmd := exec.CommandContext(ctx, "tcpdump",
		"-C", "1", "-W", strconv.Itoa(maxFiles),
		"-w", pcapPath, "-i", "any",
	)

	if err := cmd.Start(); err != nil {
		log.Printf("Failed to start tcpdump for %s: %v", key, err)
		cancel()
		return
	}
	log.Printf("tcpdump started (PID %d) for %s", cmd.Process.Pid, key)

	m.captures[key] = &CaptureProcess{cmd: cmd, cancel: cancel, files: []string{pcapPath}}

	// Wait for process exit in background to reap the zombie
	go func() {
		if err := cmd.Wait(); err != nil && ctx.Err() == nil {
			log.Printf("tcpdump for %s exited: %v", key, err)
		}
	}()
}

// stopCapture terminates the tcpdump process and deletes all associated
// pcap files (including rotated ones like capture-pod.pcap0, .pcap1, etc).
func (m *CaptureManager) stopCapture(key string) {
	cap, ok := m.captures[key]
	if !ok {
		return
	}
	cap.cancel()
	time.Sleep(500 * time.Millisecond)

	for _, pattern := range cap.files {
		matches, _ := filepath.Glob(pattern + "*")
		for _, f := range matches {
			if err := os.Remove(f); err != nil {
				log.Printf("Failed to delete %s: %v", f, err)
			} else {
				log.Printf("Deleted %s", f)
			}
		}
	}
	delete(m.captures, key)
}

func (m *CaptureManager) cleanupAll() {
	m.mu.Lock()
	defer m.mu.Unlock()
	for key := range m.captures {
		m.stopCapture(key)
	}
}
