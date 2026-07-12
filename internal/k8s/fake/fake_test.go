package fake

import (
	"context"
	"errors"
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
			"default/app": {
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

func TestListPodsFiltersByNamespace(t *testing.T) {
	fc := &Client{
		Pods: map[string][]k8s.PodInfo{
			"ns-a/app": {{Name: "app-a", Phase: "Running", CreatedAt: time.Now()}},
			"ns-b/app": {{Name: "app-b", Phase: "Running", CreatedAt: time.Now()}},
		},
	}
	pods, err := fc.ListPods(context.Background(), "ns-a", "app")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(pods) != 1 || pods[0].Name != "app-a" {
		t.Errorf("expected 1 pod app-a in ns-a, got %+v", pods)
	}
}

func TestListEventsReturnsConfiguredEvents(t *testing.T) {
	now := time.Now()
	fc := &Client{
		Events: []k8s.EventInfo{
			{Namespace: "default", Type: "Warning", Reason: "BackOff", Message: "Back-off restarting", LastTimestamp: now},
			{Namespace: "default", Type: "Normal", Reason: "Pulled", Message: "Pulled image", LastTimestamp: now},
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
			{Namespace: "default", Type: "Warning", Reason: "A"},
			{Namespace: "default", Type: "Warning", Reason: "B"},
			{Namespace: "default", Type: "Warning", Reason: "C"},
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

func TestListEventsFiltersByNamespace(t *testing.T) {
	fc := &Client{
		Events: []k8s.EventInfo{
			{Namespace: "ns-a", Type: "Warning", Reason: "A"},
			{Namespace: "ns-b", Type: "Warning", Reason: "B"},
		},
	}
	events, err := fc.ListEvents(context.Background(), "ns-a", 10)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(events) != 1 || events[0].Reason != "A" {
		t.Errorf("expected 1 event A in ns-a, got %+v", events)
	}
}

func TestListProcessesFiltersByNamespace(t *testing.T) {
	fc := &Client{}
	ctx := context.Background()
	_ = fc.CreateProcess(ctx, models.Process{Name: "app", Namespace: "ns-a"}, "test.local")
	_ = fc.CreateProcess(ctx, models.Process{Name: "app", Namespace: "ns-b"}, "test.local")

	procs, err := fc.ListProcesses(ctx, "ns-a")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(procs) != 1 || procs[0].Namespace != "ns-a" {
		t.Errorf("expected 1 process in ns-a, got %+v", procs)
	}
}

func TestGetProcessDetailAggregates(t *testing.T) {
	fc := &Client{
		Pods: map[string][]k8s.PodInfo{
			"default/web": {{Name: "web-abc", Phase: "Running", Restarts: 1, CreatedAt: time.Now()}},
		},
		Events: []k8s.EventInfo{
			{Namespace: "default", Type: "Warning", Reason: "BackOff", InvolvedObject: "pod/web-abc"},
			{Namespace: "default", Type: "Normal", Reason: "Pulled", InvolvedObject: "pod/other-xyz"},
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
	if len(detail.Events) != 1 {
		t.Errorf("expected 1 event, got %d", len(detail.Events))
	} else if detail.Events[0].InvolvedObject != "pod/web-abc" {
		t.Errorf("expected event for pod/web-abc, got %s", detail.Events[0].InvolvedObject)
	}
}

func TestListNodesReturnsErrListNodes(t *testing.T) {
	wantErr := errors.New("list nodes error")
	fc := &Client{
		ErrListNodes: wantErr,
	}
	_, err := fc.ListNodes(context.Background())
	if err != wantErr {
		t.Errorf("expected error %v, got %v", wantErr, err)
	}
}

func TestListPodsReturnsErrListPods(t *testing.T) {
	wantErr := errors.New("list pods error")
	fc := &Client{
		ErrListPods: wantErr,
	}
	_, err := fc.ListPods(context.Background(), "default", "web")
	if err != wantErr {
		t.Errorf("expected error %v, got %v", wantErr, err)
	}
}

func TestListEventsReturnsErrListEvents(t *testing.T) {
	wantErr := errors.New("list events error")
	fc := &Client{
		ErrListEvents: wantErr,
	}
	_, err := fc.ListEvents(context.Background(), "default", 10)
	if err != wantErr {
		t.Errorf("expected error %v, got %v", wantErr, err)
	}
}

func TestGetProcessDetailReturnsErrGetProcessDetail(t *testing.T) {
	wantErr := errors.New("get process detail error")
	fc := &Client{
		ErrGetProcessDetail: wantErr,
	}
	_, err := fc.GetProcessDetail(context.Background(), "default", "web")
	if err != wantErr {
		t.Errorf("expected error %v, got %v", wantErr, err)
	}
}
