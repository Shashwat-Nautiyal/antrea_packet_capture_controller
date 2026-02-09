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

type CaptureManager struct {
	clientset    *kubernetes.Clientset
	nodeName     string
	captures     map[string]*CaptureProcess
	capturesLock sync.Mutex
}

type CaptureProcess struct {
	cmd    *exec.Cmd
	cancel context.CancelFunc
	files  []string
}

func main() {
	log.Println("Starting Antrea PacketCapture Controller...")

	// Get node name from environment
	nodeName := os.Getenv("NODE_NAME")
	if nodeName == "" {
		log.Fatal("NODE_NAME environment variable is required")
	}
	log.Printf("Running on node: %s", nodeName)

	// Create in-cluster config
	config, err := rest.InClusterConfig()
	if err != nil {
		log.Fatalf("Failed to create in-cluster config: %v", err)
	}

	// Create clientset
	clientset, err := kubernetes.NewForConfig(config)
	if err != nil {
		log.Fatalf("Failed to create clientset: %v", err)
	}

	manager := &CaptureManager{
		clientset: clientset,
		nodeName:  nodeName,
		captures:  make(map[string]*CaptureProcess),
	}

	// Set up signal handling for graceful shutdown
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
	go func() {
		<-sigCh
		log.Println("Received shutdown signal, cleaning up...")
		manager.cleanupAll()
		cancel()
	}()

	// Start watching pods
	manager.watchPods(ctx)
}

func (m *CaptureManager) watchPods(ctx context.Context) {
	// Create informer factory with field selector for node-local pods
	fieldSelector := fields.OneTermEqualSelector("spec.nodeName", m.nodeName).String()
	
	factory := informers.NewSharedInformerFactoryWithOptions(
		m.clientset,
		30*time.Second,
		informers.WithTweakListOptions(func(options *metav1.ListOptions) {
			options.FieldSelector = fieldSelector
		}),
	)

	podInformer := factory.Core().V1().Pods().Informer()

	// Add event handlers
	podInformer.AddEventHandler(cache.ResourceEventHandlerFuncs{
		AddFunc: func(obj interface{}) {
			pod := obj.(*corev1.Pod)
			m.handlePodUpdate(pod)
		},
		UpdateFunc: func(oldObj, newObj interface{}) {
			pod := newObj.(*corev1.Pod)
			m.handlePodUpdate(pod)
		},
		DeleteFunc: func(obj interface{}) {
			pod := obj.(*corev1.Pod)
			m.handlePodDelete(pod)
		},
	})

	// Start informer
	factory.Start(ctx.Done())

	// Wait for cache sync
	if !cache.WaitForCacheSync(ctx.Done(), podInformer.HasSynced) {
		log.Fatal("Failed to sync cache")
	}

	log.Println("Pod informer synced, watching for changes...")

	// Wait for context cancellation
	<-ctx.Done()
}

func (m *CaptureManager) handlePodUpdate(pod *corev1.Pod) {
	// Skip pods that are not running
	if pod.Status.Phase != corev1.PodRunning {
		return
	}

	podKey := fmt.Sprintf("%s/%s", pod.Namespace, pod.Name)
	annotationValue, hasAnnotation := pod.Annotations[annotationKey]

	m.capturesLock.Lock()
	defer m.capturesLock.Unlock()

	_, isCapturing := m.captures[podKey]

	if hasAnnotation && !isCapturing {
		// Start capture
		log.Printf("Starting capture for pod %s with annotation value: %s", podKey, annotationValue)
		m.startCapture(pod, annotationValue)
	} else if !hasAnnotation && isCapturing {
		// Stop capture
		log.Printf("Stopping capture for pod %s (annotation removed)", podKey)
		m.stopCapture(podKey)
	}
}

func (m *CaptureManager) handlePodDelete(pod *corev1.Pod) {
	podKey := fmt.Sprintf("%s/%s", pod.Namespace, pod.Name)
	
	m.capturesLock.Lock()
	defer m.capturesLock.Unlock()

	if _, isCapturing := m.captures[podKey]; isCapturing {
		log.Printf("Pod %s deleted, stopping capture", podKey)
		m.stopCapture(podKey)
	}
}

func (m *CaptureManager) startCapture(pod *corev1.Pod, annotationValue string) {
	// Parse max number of files
	maxFiles, err := strconv.Atoi(strings.TrimSpace(annotationValue))
	if err != nil || maxFiles <= 0 {
		log.Printf("Invalid annotation value for pod %s/%s: %s", pod.Namespace, pod.Name, annotationValue)
		return
	}

	podKey := fmt.Sprintf("%s/%s", pod.Namespace, pod.Name)
	captureFile := filepath.Join(captureDir, fmt.Sprintf("capture-%s.pcap", pod.Name))

	// Create context for this capture
	ctx, cancel := context.WithCancel(context.Background())

	// Build tcpdump command
	// -C 1: rotate files at 1 million bytes (1MB)
	// -W N: max N files
	// -w: output file
	cmd := exec.CommandContext(ctx, "tcpdump", 
		"-C", "1",
		"-W", strconv.Itoa(maxFiles),
		"-w", captureFile,
		"-i", "any",
	)

	// Capture stderr for error logging
	stderr, err := cmd.StderrPipe()
	if err != nil {
		log.Printf("Failed to create stderr pipe for pod %s: %v", podKey, err)
		cancel()
		return
	}

	// Start the process
	if err := cmd.Start(); err != nil {
		log.Printf("Failed to start tcpdump for pod %s: %v", podKey, err)
		cancel()
		return
	}

	log.Printf("Started tcpdump (PID %d) for pod %s", cmd.Process.Pid, podKey)

	// Store capture process
	m.captures[podKey] = &CaptureProcess{
		cmd:    cmd,
		cancel: cancel,
		files:  []string{captureFile},
	}

	// Monitor process in background
	go func() {
		// Read stderr in background
		buf := make([]byte, 1024)
		for {
			n, err := stderr.Read(buf)
			if n > 0 {
				log.Printf("tcpdump stderr for pod %s: %s", podKey, string(buf[:n]))
			}
			if err != nil {
				break
			}
		}
	}()

	go func() {
		err := cmd.Wait()
		if err != nil && ctx.Err() == nil {
			log.Printf("tcpdump process for pod %s exited with error: %v", podKey, err)
		}
	}()
}

func (m *CaptureManager) stopCapture(podKey string) {
	capture, exists := m.captures[podKey]
	if !exists {
		return
	}

	// Cancel context to stop tcpdump
	capture.cancel()

	// Wait a bit for process to terminate
	time.Sleep(500 * time.Millisecond)

	// Delete pcap files
	for _, filePattern := range capture.files {
		// Handle rotated files (file.pcap0, file.pcap1, etc.)
		basePattern := filePattern + "*"
		matches, err := filepath.Glob(basePattern)
		if err != nil {
			log.Printf("Error finding capture files for %s: %v", podKey, err)
			continue
		}

		for _, file := range matches {
			if err := os.Remove(file); err != nil {
				log.Printf("Failed to delete capture file %s: %v", file, err)
			} else {
				log.Printf("Deleted capture file: %s", file)
			}
		}
	}

	delete(m.captures, podKey)
	log.Printf("Capture stopped and cleaned up for pod %s", podKey)
}

func (m *CaptureManager) cleanupAll() {
	m.capturesLock.Lock()
	defer m.capturesLock.Unlock()

	for podKey := range m.captures {
		m.stopCapture(podKey)
	}
}
