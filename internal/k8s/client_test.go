package k8s

import (
	"context"
	"errors"
	"testing"

	"processmanager/internal/models"
)

func TestClientInterfaceHasScaleAndStatus(t *testing.T) {
	var _ ClientInterface = (*Client)(nil)
}

func TestScaleProcessNilClientset(t *testing.T) {
	c := &Client{}
	err := c.ScaleProcess(context.Background(), "default", "unmanaged", 2)
	if err == nil {
		t.Fatal("expected error for nil clientset")
	}
}

func TestScaleProcessEnforcesReplicaBounds(t *testing.T) {
	c := &Client{}
	if err := c.ScaleProcess(context.Background(), "default", "x", -1); !errors.Is(err, models.ErrInvalidReplicas) {
		t.Fatalf("expected ErrInvalidReplicas for negative replicas, got %v", err)
	}
	if err := c.ScaleProcess(context.Background(), "default", "x", 11); !errors.Is(err, models.ErrInvalidReplicas) {
		t.Fatalf("expected ErrInvalidReplicas for replicas > 10, got %v", err)
	}
}
