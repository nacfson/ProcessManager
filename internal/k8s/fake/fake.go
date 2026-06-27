package fake

import (
	"context"
	"fmt"

	"processmanager/internal/k8s"
	"processmanager/internal/models"
)

// Client implements k8s.ClientInterface in memory.
type Client struct {
	processes []models.Process
}

// NewClient creates an empty fake client.
func NewClient() k8s.ClientInterface {
	return &Client{}
}

func (c *Client) ListProcesses(ctx context.Context, namespace string) ([]models.Process, error) {
	return c.processes, nil
}

func (c *Client) GetProcess(ctx context.Context, namespace, name string) (*models.Process, error) {
	for _, p := range c.processes {
		if p.Name == name && p.Namespace == namespace {
			return &p, nil
		}
	}
	return nil, models.ErrProcessNotFound
}

func (c *Client) CreateProcess(ctx context.Context, p models.Process, domain string) error {
	c.processes = append(c.processes, p)
	return nil
}

func (c *Client) DeleteProcess(ctx context.Context, namespace, name string) error {
	for i, p := range c.processes {
		if p.Name == name && p.Namespace == namespace {
			c.processes = append(c.processes[:i], c.processes[i+1:]...)
			return nil
		}
	}
	return models.ErrProcessNotFound
}

func (c *Client) GetPodLogs(ctx context.Context, namespace, name string) (string, error) {
	return fmt.Sprintf("fake logs for %s/%s", namespace, name), nil
}
