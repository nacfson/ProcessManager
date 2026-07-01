package k8s

import (
	"context"
	"testing"
	"time"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes/fake"
)

func TestListNodes(t *testing.T) {
	node1 := &corev1.Node{
		ObjectMeta: metav1.ObjectMeta{
			Name: "node-1",
			Labels: map[string]string{
				"kubernetes.io/arch": "amd64",
			},
		},
		Status: corev1.NodeStatus{
			Conditions: []corev1.NodeCondition{
				{
					Type:   corev1.NodeReady,
					Status: corev1.ConditionTrue,
				},
				{
					Type:   corev1.NodeMemoryPressure,
					Status: corev1.ConditionFalse,
				},
			},
			Capacity: corev1.ResourceList{
				corev1.ResourceCPU:    resource.MustParse("8"),
				corev1.ResourceMemory: resource.MustParse("16Gi"),
			},
			Allocatable: corev1.ResourceList{
				corev1.ResourceCPU:    resource.MustParse("7.5"),
				corev1.ResourceMemory: resource.MustParse("15Gi"),
			},
		},
	}

	clientset := fake.NewSimpleClientset(node1)
	client := &Client{clientset: clientset}

	nodes, err := client.ListNodes(context.Background())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(nodes) != 1 {
		t.Fatalf("expected 1 node, got %d", len(nodes))
	}

	n := nodes[0]
	if n.Name != "node-1" || n.Arch != "amd64" || !n.Ready {
		t.Errorf("unexpected node properties: %+v", n)
	}
	if n.CapacityCPU != "8" || n.CapacityMem != "16Gi" || n.AllocCPU != "7500m" || n.AllocMem != "15Gi" {
		t.Errorf("unexpected resources: %+v", n)
	}
	if len(n.Conditions) != 2 {
		t.Errorf("expected 2 conditions, got %d", len(n.Conditions))
	}
}

func TestListPods(t *testing.T) {
	pod1 := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "pod-1",
			Namespace: "default",
			Labels: map[string]string{
				ManagedByLabel: ManagedByValue,
				PartOfLabel:    "process-a",
			},
			CreationTimestamp: metav1.Time{Time: time.Date(2026, 7, 1, 12, 0, 0, 0, time.UTC)},
		},
		Spec: corev1.PodSpec{
			Containers: []corev1.Container{
				{
					Name: "c1",
					Resources: corev1.ResourceRequirements{
						Requests: corev1.ResourceList{
							corev1.ResourceCPU:    resource.MustParse("100m"),
							corev1.ResourceMemory: resource.MustParse("128Mi"),
						},
						Limits: corev1.ResourceList{
							corev1.ResourceCPU:    resource.MustParse("200m"),
							corev1.ResourceMemory: resource.MustParse("256Mi"),
						},
					},
				},
			},
		},
		Status: corev1.PodStatus{
			Phase: corev1.PodRunning,
			ContainerStatuses: []corev1.ContainerStatus{
				{
					RestartCount: 2,
					LastTerminationState: corev1.ContainerState{
						Terminated: &corev1.ContainerStateTerminated{
							Reason: "OOMKilled",
						},
					},
				},
			},
		},
	}

	clientset := fake.NewSimpleClientset(pod1)
	client := &Client{clientset: clientset}

	pods, err := client.ListPods(context.Background(), "default", "process-a")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(pods) != 1 {
		t.Fatalf("expected 1 pod, got %d", len(pods))
	}

	p := pods[0]
	if p.Name != "pod-1" || p.Phase != "Running" || p.Restarts != 2 || p.LastTermReason != "OOMKilled" {
		t.Errorf("unexpected pod properties: %+v", p)
	}
	if p.RequestsCPU != "100m" || p.RequestsMem != "128Mi" || p.LimitsCPU != "200m" || p.LimitsMem != "256Mi" {
		t.Errorf("unexpected resource config: %+v", p)
	}
}

func TestListEvents(t *testing.T) {
	e1 := &corev1.Event{
		ObjectMeta: metav1.ObjectMeta{
			Name:              "e1",
			Namespace:         "default",
			CreationTimestamp: metav1.Time{Time: time.Date(2026, 7, 1, 12, 0, 0, 0, time.UTC)},
		},
		Type:           "Warning",
		Reason:         "Failed",
		Message:        "Failed to pull image",
		InvolvedObject: corev1.ObjectReference{Kind: "Pod", Name: "pod-1"},
		LastTimestamp:  metav1.Time{Time: time.Date(2026, 7, 1, 12, 5, 0, 0, time.UTC)},
	}

	e2 := &corev1.Event{
		ObjectMeta: metav1.ObjectMeta{
			Name:              "e2",
			Namespace:         "default",
			CreationTimestamp: metav1.Time{Time: time.Date(2026, 7, 1, 12, 0, 0, 0, time.UTC)},
		},
		Type:           "Warning",
		Reason:         "BackOff",
		Message:        "Back-off restarting failed container",
		InvolvedObject: corev1.ObjectReference{Kind: "Pod", Name: "pod-1"},
		LastTimestamp:  metav1.Time{Time: time.Date(2026, 7, 1, 12, 10, 0, 0, time.UTC)},
	}

	clientset := fake.NewSimpleClientset(e1, e2)
	client := &Client{clientset: clientset}

	events, err := client.ListEvents(context.Background(), "default", 1)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(events) != 1 {
		t.Fatalf("expected 1 event due to limit, got %d", len(events))
	}

	if events[0].Reason != "BackOff" {
		t.Errorf("expected BackOff event first, got %s", events[0].Reason)
	}
}

func TestGetProcessDetail(t *testing.T) {
	dep := &appsv1.Deployment{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "process-a",
			Namespace: "default",
			Labels: map[string]string{
				ManagedByLabel: ManagedByValue,
			},
		},
		Spec: appsv1.DeploymentSpec{
			Template: corev1.PodTemplateSpec{
				Spec: corev1.PodSpec{
					Containers: []corev1.Container{
						{
							Image: "nginx:latest",
						},
					},
				},
			},
		},
	}

	pod := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "process-a-pod-1",
			Namespace: "default",
			Labels: map[string]string{
				ManagedByLabel: ManagedByValue,
				PartOfLabel:    "process-a",
			},
		},
	}

	e1 := &corev1.Event{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "e1",
			Namespace: "default",
		},
		Type:           "Warning",
		Reason:         "Failed",
		Message:        "Failed",
		InvolvedObject: corev1.ObjectReference{Kind: "Pod", Name: "process-a-pod-1"},
	}

	clientset := fake.NewSimpleClientset(dep, pod, e1)
	client := &Client{clientset: clientset}

	detail, err := client.GetProcessDetail(context.Background(), "default", "process-a")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if detail.Process.Name != "process-a" {
		t.Errorf("expected process name process-a, got %s", detail.Process.Name)
	}
	if len(detail.Pods) != 1 {
		t.Errorf("expected 1 pod, got %d", len(detail.Pods))
	}
	if len(detail.Events) != 1 {
		t.Errorf("expected 1 event, got %d", len(detail.Events))
	}
}
