package fake

import (
	"context"
	"fmt"
	"strings"

	"processmanager/internal/k8s"
	"processmanager/internal/models"
)

// Client implements k8s.ClientInterface in memory.
type Client struct {
	processes []models.Process
	// Logs overrides the default fake log output when non-empty.
	Logs string
	// ErrListProcesses, if set, is returned by ListProcesses.
	ErrListProcesses error

	// Nodes holds fake node data for ListNodes.
	Nodes []k8s.NodeInfo
	// Pods holds fake pod data keyed by process name for ListPods.
	Pods map[string][]k8s.PodInfo
	// Events holds fake events for ListEvents.
	Events []k8s.EventInfo
	// ErrListNodes, if set, is returned by ListNodes.
	ErrListNodes error
	// ErrListPods, if set, is returned by ListPods.
	ErrListPods error
	// ErrListEvents, if set, is returned by ListEvents.
	ErrListEvents error
	// ErrGetProcessDetail, if set, is returned by GetProcessDetail.
	ErrGetProcessDetail error
}

// NewClient creates an empty fake client.
func NewClient() k8s.ClientInterface {
	return &Client{}
}

func (c *Client) ListProcesses(ctx context.Context, namespace string) ([]models.Process, error) {
	if c.ErrListProcesses != nil {
		return nil, c.ErrListProcesses
	}
	return c.processes, nil
}

func (c *Client) GetProcess(ctx context.Context, namespace, name string) (*models.Process, error) {
	for i, p := range c.processes {
		if p.Name == name && p.Namespace == namespace {
			return &c.processes[i], nil
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
	if c.Logs != "" {
		return c.Logs, nil
	}
	return fmt.Sprintf("fake logs for %s/%s", namespace, name), nil
}

func (c *Client) ListNodes(ctx context.Context) ([]k8s.NodeInfo, error) {
	if c.ErrListNodes != nil {
		return nil, c.ErrListNodes
	}
	return c.Nodes, nil
}

func (c *Client) ListPods(ctx context.Context, namespace, processName string) ([]k8s.PodInfo, error) {
	if c.ErrListPods != nil {
		return nil, c.ErrListPods
	}
	if c.Pods == nil {
		return nil, nil
	}
	return c.Pods[processName], nil
}

func (c *Client) ListEvents(ctx context.Context, namespace string, limit int) ([]k8s.EventInfo, error) {
	if c.ErrListEvents != nil {
		return nil, c.ErrListEvents
	}
	if limit <= 0 || limit >= len(c.Events) {
		return c.Events, nil
	}
	return c.Events[:limit], nil
}

func (c *Client) GetProcessDetail(ctx context.Context, namespace, name string) (*k8s.ProcessDetail, error) {
	if c.ErrGetProcessDetail != nil {
		return nil, c.ErrGetProcessDetail
	}
	proc, err := c.GetProcess(ctx, namespace, name)
	if err != nil {
		return nil, err
	}
	pods, _ := c.ListPods(ctx, namespace, name)
	allEvents, _ := c.ListEvents(ctx, namespace, 0)

	processes, _ := c.ListProcesses(ctx, namespace)
	allNames := make([]string, len(processes))
	for i, p := range processes {
		allNames[i] = p.Name
	}

	events := k8s.FilterEventsForProcess(allEvents, name, allNames)

	return &k8s.ProcessDetail{
		Process: *proc,
		Pods:    pods,
		Events:  events,
	}, nil
}
