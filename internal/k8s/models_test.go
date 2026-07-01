package k8s

import (
	"testing"
	"time"

	"processmanager/internal/models"
)

func TestNodeInfoIsReady(t *testing.T) {
	node := NodeInfo{
		Name:  "zlUbuntu",
		Arch:  "amd64",
		Ready: true,
	}
	if !node.Ready {
		t.Errorf("expected node to be ready")
	}
}

func TestPodInfoHasRestarts(t *testing.T) {
	pod := PodInfo{
		Name:           "app-abc123",
		Phase:          "Running",
		Restarts:       3,
		LastTermReason: "OOMKilled",
		CreatedAt:      time.Now().Add(-2 * time.Hour),
		RequestsMem:    "128Mi",
		LimitsMem:      "256Mi",
	}
	if pod.Restarts != 3 {
		t.Errorf("expected 3 restarts, got %d", pod.Restarts)
	}
	if pod.LastTermReason != "OOMKilled" {
		t.Errorf("expected OOMKilled, got %s", pod.LastTermReason)
	}
}

func TestProcessDetailAggregates(t *testing.T) {
	detail := ProcessDetail{
		Process: models.Process{Name: "web", Image: "nginx:latest"},
		Pods: []PodInfo{
			{Name: "web-abc", Phase: "Running", Restarts: 0},
			{Name: "web-def", Phase: "Running", Restarts: 1},
		},
		Events: []EventInfo{
			{Type: "Warning", Reason: "BackOff", Message: "Back-off restarting"},
		},
	}
	if len(detail.Pods) != 2 {
		t.Errorf("expected 2 pods, got %d", len(detail.Pods))
	}
	if detail.TotalRestarts() != 1 {
		t.Errorf("expected 1 total restart, got %d", detail.TotalRestarts())
	}
}
