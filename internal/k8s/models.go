package k8s

import (
	"time"

	"processmanager/internal/models"
)

// NodeInfo represents a cluster node's status.
type NodeInfo struct {
	Name        string          `json:"name"`
	Arch        string          `json:"arch"`
	Ready       bool            `json:"ready"`
	Conditions  []NodeCondition `json:"conditions"`
	CapacityCPU string          `json:"capacityCPU"`
	CapacityMem string          `json:"capacityMem"`
	AllocCPU    string          `json:"allocCPU"`
	AllocMem    string          `json:"allocMem"`
}

// NodeCondition represents a single node condition.
type NodeCondition struct {
	Type   string `json:"type"` // e.g. "MemoryPressure", "DiskPressure"
	Status bool   `json:"status"`
}

// PodInfo represents a pod's runtime status.
type PodInfo struct {
	Name           string    `json:"name"`
	Namespace      string    `json:"namespace"`
	Phase          string    `json:"phase"` // Running, Pending, Failed, Succeeded
	Restarts       int32     `json:"restarts"`
	LastTermReason string    `json:"lastTermReason"` // OOMKilled, Error, etc.
	CreatedAt      time.Time `json:"createdAt"`
	RequestsCPU    string    `json:"requestsCPU"`
	RequestsMem    string    `json:"requestsMem"`
	LimitsCPU      string    `json:"limitsCPU"`
	LimitsMem      string    `json:"limitsMem"`
}

// EventInfo represents a Kubernetes event.
type EventInfo struct {
	Namespace      string    `json:"namespace"`
	Type           string    `json:"type"` // Normal, Warning
	Reason         string    `json:"reason"`
	Message        string    `json:"message"`
	InvolvedObject string    `json:"involvedObject"` // e.g. "pod/app-abc123"
	LastTimestamp  time.Time `json:"lastTimestamp"`
}

// ProcessDetail aggregates process, pod, and event data.
type ProcessDetail struct {
	Process models.Process `json:"process"`
	Pods    []PodInfo      `json:"pods"`
	Events  []EventInfo    `json:"events"`
}

// TotalRestarts returns the sum of restarts across all pods.
func (d *ProcessDetail) TotalRestarts() int32 {
	var total int32
	for _, p := range d.Pods {
		total += p.Restarts
	}
	return total
}

// NodeMetrics represents resource usage for a node.
type NodeMetrics struct {
	Name     string `json:"name"`
	CPUNano  int64  `json:"cpuNano"`  // CPU usage in nanocores
	MemBytes int64  `json:"memBytes"` // Memory usage in bytes
}

// PodMetrics represents resource usage for a pod.
type PodMetrics struct {
	Name      string `json:"name"`
	Namespace string `json:"namespace"`
	CPUNano   int64  `json:"cpuNano"`
	MemBytes  int64  `json:"memBytes"`
}

// IngressInfo represents an Ingress rule.
type IngressInfo struct {
	Name        string `json:"name"`
	Namespace   string `json:"namespace"`
	Host        string `json:"host"`
	ServiceName string `json:"serviceName"`
	ServicePort int32  `json:"servicePort"`
}

// ServiceInfo represents a Service and its endpoint readiness.
type ServiceInfo struct {
	Name      string `json:"name"`
	Namespace string `json:"namespace"`
	ReadyPods int32  `json:"readyPods"`
	TotalPods int32  `json:"totalPods"`
}

// ServiceTopology represents how an Ingress connects to a Service and its Pods.
type ServiceTopology struct {
	IngressHost string `json:"ingressHost"`
	ServiceName string `json:"serviceName"`
	Namespace   string `json:"namespace"`
	Project     string `json:"project"`
	ReadyPods   int32  `json:"readyPods"`
	TotalPods   int32  `json:"totalPods"`
}

