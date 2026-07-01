package k8s

import (
	"time"

	"processmanager/internal/models"
)

// NodeInfo represents a cluster node's status.
type NodeInfo struct {
	Name        string
	Arch        string
	Ready       bool
	Conditions  []NodeCondition
	CapacityCPU string
	CapacityMem string
	AllocCPU    string
	AllocMem    string
}

// NodeCondition represents a single node condition.
type NodeCondition struct {
	Type   string // e.g. "MemoryPressure", "DiskPressure"
	Status bool
}

// PodInfo represents a pod's runtime status.
type PodInfo struct {
	Name           string
	Phase          string // Running, Pending, Failed, Succeeded
	Restarts       int32
	LastTermReason string // OOMKilled, Error, etc.
	Age            time.Duration
	RequestsCPU    string
	RequestsMem    string
	LimitsCPU      string
	LimitsMem      string
}

// EventInfo represents a Kubernetes event.
type EventInfo struct {
	Type           string // Normal, Warning
	Reason         string
	Message        string
	InvolvedObject string // e.g. "pod/app-abc123"
	LastTimestamp   time.Time
}

// ProcessDetail aggregates process, pod, and event data.
type ProcessDetail struct {
	Process models.Process
	Pods    []PodInfo
	Events  []EventInfo
}

// TotalRestarts returns the sum of restarts across all pods.
func (d *ProcessDetail) TotalRestarts() int32 {
	var total int32
	for _, p := range d.Pods {
		total += p.Restarts
	}
	return total
}
