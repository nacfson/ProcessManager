package fake

import (
	"context"
	"testing"
	"time"

	"processmanager/internal/k8s"
	"processmanager/internal/models"
)

func TestListNodesReturnsConfiguredNodes(t *testing.T) {
	fc := &Client{
		Nodes: []k8s.NodeInfo{
			{Name: "zlUbuntu", Arch: "amd64", Ready: true, CapacityCPU: "4", CapacityMem: "8Gi"},
			{Name: "rlLocal", Arch: "arm64", Ready: true, CapacityCPU: "4", CapacityMem: "905Mi"},
		},
	}
	nodes, err := fc.ListNodes(context.Background())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(nodes) != 2 {
		t.Fatalf("expected 2 nodes, got %d", len(nodes))
	}
	if nodes[0].Name != "zlUbuntu" {
		t.Errorf("expected zlUbuntu, got %s", nodes[0].Name)
	}
}

func TestListPodsReturnsFilteredPods(t *testing.T) {
	fc := &Client{
		Pods: map[string][]k8s.PodInfo{
			"app": {
				{Name: "app-abc", Phase: "Running", Restarts: 0, CreatedAt: time.Now()},
				{Name: "app-def", Phase: "Running", Restarts: 2, LastTermReason: "OOMKilled", CreatedAt: time.Now()},
			},
		},
	}
	pods, err := fc.ListPods(context.Background(), "default", "app")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(pods) != 2 {
		t.Fatalf("expected 2 pods, got %d", len(pods))
	}
	if pods[1].Restarts != 2 {
		t.Errorf("expected 2 restarts, got %d", pods[1].Restarts)
	}
}

func TestListPodsReturnsEmptyForUnknownProcess(t *testing.T) {
	fc := &Client{}
	pods, err := fc.ListPods(context.Background(), "default", "unknown")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(pods) != 0 {
		t.Errorf("expected 0 pods, got %d", len(pods))
	}
}

func TestListEventsReturnsConfiguredEvents(t *testing.T) {
	now := time.Now()
	fc := &Client{
		Events: []k8s.EventInfo{
			{Type: "Warning", Reason: "BackOff", Message: "Back-off restarting", LastTimestamp: now},
			{Type: "Normal", Reason: "Pulled", Message: "Pulled image", LastTimestamp: now},
		},
	}
	events, err := fc.ListEvents(context.Background(), "default", 10)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(events) != 2 {
		t.Fatalf("expected 2 events, got %d", len(events))
	}
}

func TestListEventsRespectsLimit(t *testing.T) {
	fc := &Client{
		Events: []k8s.EventInfo{
			{Type: "Warning", Reason: "A"},
			{Type: "Warning", Reason: "B"},
			{Type: "Warning", Reason: "C"},
		},
	}
	events, err := fc.ListEvents(context.Background(), "default", 2)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(events) != 2 {
		t.Errorf("expected 2 events, got %d", len(events))
	}
}

func TestGetProcessDetailAggregates(t *testing.T) {
	fc := &Client{
		Pods: map[string][]k8s.PodInfo{
			"web": {{Name: "web-abc", Phase: "Running", Restarts: 1, CreatedAt: time.Now()}},
		},
		Events: []k8s.EventInfo{
			{Type: "Warning", Reason: "BackOff", InvolvedObject: "pod/web-abc"},
			{Type: "Normal", Reason: "Pulled", InvolvedObject: "pod/other-xyz"},
		},
	}
	fc.CreateProcess(context.Background(), models.Process{Name: "web", Image: "nginx", Namespace: "default"}, "test.local")

	detail, err := fc.GetProcessDetail(context.Background(), "default", "web")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if detail.Process.Name != "web" {
		t.Errorf("expected process name web, got %s", detail.Process.Name)
	}
	if len(detail.Pods) != 1 {
		t.Errorf("expected 1 pod, got %d", len(detail.Pods))
	}
}
