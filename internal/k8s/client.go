package k8s

import (
	"context"
	"fmt"
	"io"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"processmanager/internal/models"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	networkingv1 "k8s.io/api/networking/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/util/intstr"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/clientcmd"
	"k8s.io/client-go/util/homedir"
)

// Prevent unused time import compiler error
var _ time.Time

const (
	ManagedByLabel = "app.kubernetes.io/managed-by"
	ManagedByValue = "processmanager"
	PartOfLabel    = "app.kubernetes.io/part-of"
	NameLabel      = "app.kubernetes.io/name"
)

// ClientInterface is the surface used by HTTP handlers.
type ClientInterface interface {
	ListProcesses(ctx context.Context, namespace string) ([]models.Process, error)
	GetProcess(ctx context.Context, namespace, name string) (*models.Process, error)
	CreateProcess(ctx context.Context, p models.Process, domain string) error
	DeleteProcess(ctx context.Context, namespace, name string) error
	GetPodLogs(ctx context.Context, namespace, name string) (string, error)
	ListNodes(ctx context.Context) ([]NodeInfo, error)
	ListPods(ctx context.Context, namespace, processName string) ([]PodInfo, error)
	ListEvents(ctx context.Context, namespace string, limit int) ([]EventInfo, error)
	GetProcessDetail(ctx context.Context, namespace, name string) (*ProcessDetail, error)
}

// Client wraps a Kubernetes clientset for managing processes.
type Client struct {
	clientset kubernetes.Interface
}

// NewClient creates a Client using in-cluster config or ~/.kube/config.
func NewClient() (*Client, error) {
	config, err := rest.InClusterConfig()
	if err != nil {
		kubeconfig := filepath.Join(homedir.HomeDir(), ".kube", "config")
		config, err = clientcmd.BuildConfigFromFlags("", kubeconfig)
		if err != nil {
			return nil, fmt.Errorf("failed to create k8s config: %w", err)
		}
	}
	cs, err := kubernetes.NewForConfig(config)
	if err != nil {
		return nil, fmt.Errorf("failed to create clientset: %w", err)
	}
	return &Client{clientset: cs}, nil
}

// ListProcesses returns all process-like Deployments in a namespace.
func (c *Client) ListProcesses(ctx context.Context, namespace string) ([]models.Process, error) {
	selector := labels.SelectorFromSet(map[string]string{ManagedByLabel: ManagedByValue})
	deps, err := c.clientset.AppsV1().Deployments(namespace).List(ctx, metav1.ListOptions{LabelSelector: selector.String()})
	if err != nil {
		return nil, err
	}

	var processes []models.Process
	for _, d := range deps.Items {
		p := c.deploymentToProcess(d)
		p.Ready = d.Status.ReadyReplicas
		p.Total = d.Status.Replicas
		processes = append(processes, p)
	}
	return processes, nil
}

// GetProcess returns a single process by name.
func (c *Client) GetProcess(ctx context.Context, namespace, name string) (*models.Process, error) {
	d, err := c.clientset.AppsV1().Deployments(namespace).Get(ctx, name, metav1.GetOptions{})
	if err != nil {
		if apierrors.IsNotFound(err) {
			return nil, models.ErrProcessNotFound
		}
		return nil, err
	}
	if d.Labels[ManagedByLabel] != ManagedByValue {
		return nil, models.ErrProcessNotFound
	}
	p := c.deploymentToProcess(*d)
	p.Ready = d.Status.ReadyReplicas
	p.Total = d.Status.Replicas
	return &p, nil
}

// CreateProcess creates Deployment, Service, and Ingress for a process.
func (c *Client) CreateProcess(ctx context.Context, p models.Process, domain string) error {
	if err := p.Validate(); err != nil {
		return err
	}

	labels := map[string]string{
		ManagedByLabel: ManagedByValue,
		PartOfLabel:    p.Name,
		NameLabel:      p.Name,
	}

	// Build environment variables.
	var env []corev1.EnvVar
	for k, v := range p.Env {
		env = append(env, corev1.EnvVar{Name: k, Value: v})
	}

	// Build resource requirements.
	var resources corev1.ResourceRequirements
	if p.MemoryLimit != "" {
		resources.Limits = corev1.ResourceList{
			corev1.ResourceMemory: resource.MustParse(p.MemoryLimit),
		}
		resources.Requests = corev1.ResourceList{
			corev1.ResourceMemory: resource.MustParse(p.MemoryLimit),
		}
	}

	// Build node selector for architecture.
	var nodeSelector map[string]string
	if p.Arch == "amd64" || p.Arch == "arm64" {
		nodeSelector = map[string]string{"kubernetes.io/arch": p.Arch}
	}

	// Deployment
	deployment := &appsv1.Deployment{
		ObjectMeta: metav1.ObjectMeta{
			Name:      p.Name,
			Namespace: p.Namespace,
			Labels:    labels,
		},
		Spec: appsv1.DeploymentSpec{
			Replicas: &p.Replicas,
			Selector: &metav1.LabelSelector{MatchLabels: labels},
			Template: corev1.PodTemplateSpec{
				ObjectMeta: metav1.ObjectMeta{Labels: labels},
				Spec: corev1.PodSpec{
					NodeSelector: nodeSelector,
					Containers: []corev1.Container{{
						Name:      p.Name,
						Image:     p.Image,
						Ports:     []corev1.ContainerPort{{ContainerPort: p.Port}},
						Env:       env,
						Resources: resources,
					}},
				},
			},
		},
	}

	// Service
	service := &corev1.Service{
		ObjectMeta: metav1.ObjectMeta{
			Name:      p.Name,
			Namespace: p.Namespace,
			Labels:    labels,
		},
		Spec: corev1.ServiceSpec{
			Selector: labels,
			Ports: []corev1.ServicePort{{
				Port:       p.Port,
				TargetPort: intstr.FromInt(int(p.Port)),
			}},
		},
	}

	// Ingress
	pathType := networkingv1.PathTypePrefix
	ingress := &networkingv1.Ingress{
		ObjectMeta: metav1.ObjectMeta{
			Name:      p.Name,
			Namespace: p.Namespace,
			Labels:    labels,
			Annotations: map[string]string{
				"cert-manager.io/cluster-issuer": "letsencrypt-prod",
			},
		},
		Spec: networkingv1.IngressSpec{
			IngressClassName: strPtr("nginx"),
			Rules: []networkingv1.IngressRule{{
				Host: fmt.Sprintf("%s.%s", p.Name, domain),
				IngressRuleValue: networkingv1.IngressRuleValue{
					HTTP: &networkingv1.HTTPIngressRuleValue{
						Paths: []networkingv1.HTTPIngressPath{{
							Path:     "/",
							PathType: &pathType,
							Backend: networkingv1.IngressBackend{
								Service: &networkingv1.IngressServiceBackend{
									Name: p.Name,
									Port: networkingv1.ServiceBackendPort{Number: p.Port},
								},
							},
						}},
					},
				},
			}},
			TLS: []networkingv1.IngressTLS{{
				Hosts:      []string{fmt.Sprintf("%s.%s", p.Name, domain)},
				SecretName: fmt.Sprintf("%s-tls", p.Name),
			}},
		},
	}

	if _, err := c.clientset.AppsV1().Deployments(p.Namespace).Create(ctx, deployment, metav1.CreateOptions{}); err != nil {
		return fmt.Errorf("create deployment: %w", err)
	}
	if _, err := c.clientset.CoreV1().Services(p.Namespace).Create(ctx, service, metav1.CreateOptions{}); err != nil {
		return fmt.Errorf("create service: %w", err)
	}
	if _, err := c.clientset.NetworkingV1().Ingresses(p.Namespace).Create(ctx, ingress, metav1.CreateOptions{}); err != nil {
		return fmt.Errorf("create ingress: %w", err)
	}
	return nil
}

// DeleteProcess removes Deployment, Service, and Ingress for a process.
func (c *Client) DeleteProcess(ctx context.Context, namespace, name string) error {
	propagation := metav1.DeletePropagationForeground
	opts := metav1.DeleteOptions{PropagationPolicy: &propagation}
	if err := c.clientset.NetworkingV1().Ingresses(namespace).Delete(ctx, name, opts); err != nil {
		return fmt.Errorf("delete ingress: %w", err)
	}
	if err := c.clientset.CoreV1().Services(namespace).Delete(ctx, name, opts); err != nil {
		return fmt.Errorf("delete service: %w", err)
	}
	if err := c.clientset.AppsV1().Deployments(namespace).Delete(ctx, name, opts); err != nil {
		return fmt.Errorf("delete deployment: %w", err)
	}
	return nil
}

// GetPodLogs returns logs from the first pod of a process.
func (c *Client) GetPodLogs(ctx context.Context, namespace, name string) (string, error) {
	selector := labels.SelectorFromSet(map[string]string{PartOfLabel: name, ManagedByLabel: ManagedByValue})
	pods, err := c.clientset.CoreV1().Pods(namespace).List(ctx, metav1.ListOptions{LabelSelector: selector.String()})
	if err != nil {
		return "", err
	}
	if len(pods.Items) == 0 {
		return "", fmt.Errorf("no pods found")
	}
	podName := pods.Items[0].Name
	tail := int64(100)
	req := c.clientset.CoreV1().Pods(namespace).GetLogs(podName, &corev1.PodLogOptions{TailLines: &tail})
	stream, err := req.Stream(ctx)
	if err != nil {
		return "", err
	}
	defer stream.Close()
	buf := new(strings.Builder)
	if _, err := io.Copy(buf, stream); err != nil {
		return "", err
	}
	return buf.String(), nil
}

// ListNodes returns status for all cluster nodes.
func (c *Client) ListNodes(ctx context.Context) ([]NodeInfo, error) {
	nodeList, err := c.clientset.CoreV1().Nodes().List(ctx, metav1.ListOptions{})
	if err != nil {
		return nil, fmt.Errorf("list nodes: %w", err)
	}

	var nodes []NodeInfo
	for _, n := range nodeList.Items {
		var ready bool
		for _, cond := range n.Status.Conditions {
			if cond.Type == corev1.NodeReady {
				ready = cond.Status == corev1.ConditionTrue
				break
			}
		}

		conditions := make([]NodeCondition, 0, len(n.Status.Conditions))
		for _, cond := range n.Status.Conditions {
			conditions = append(conditions, NodeCondition{
				Type:   string(cond.Type),
				Status: cond.Status == corev1.ConditionTrue,
			})
		}

		var capacityCPU, capacityMem, allocCPU, allocMem string
		if cpu := n.Status.Capacity.Cpu(); cpu != nil {
			capacityCPU = cpu.String()
		}
		if mem := n.Status.Capacity.Memory(); mem != nil {
			capacityMem = mem.String()
		}
		if cpu := n.Status.Allocatable.Cpu(); cpu != nil {
			allocCPU = cpu.String()
		}
		if mem := n.Status.Allocatable.Memory(); mem != nil {
			allocMem = mem.String()
		}

		nodes = append(nodes, NodeInfo{
			Name:        n.Name,
			Arch:        n.Labels["kubernetes.io/arch"],
			Ready:       ready,
			Conditions:  conditions,
			CapacityCPU: capacityCPU,
			CapacityMem: capacityMem,
			AllocCPU:    allocCPU,
			AllocMem:    allocMem,
		})
	}

	return nodes, nil
}

// ListPods returns pod details for a managed process.
func (c *Client) ListPods(ctx context.Context, namespace, processName string) ([]PodInfo, error) {
	selector := labels.SelectorFromSet(map[string]string{
		ManagedByLabel: ManagedByValue,
		PartOfLabel:    processName,
	})
	pods, err := c.clientset.CoreV1().Pods(namespace).List(ctx, metav1.ListOptions{
		LabelSelector: selector.String(),
	})
	if err != nil {
		return nil, fmt.Errorf("list pods: %w", err)
	}

	var podInfos []PodInfo
	for _, p := range pods.Items {
		var restarts int32
		for _, cs := range p.Status.ContainerStatuses {
			restarts += cs.RestartCount
		}
		for _, cs := range p.Status.InitContainerStatuses {
			restarts += cs.RestartCount
		}

		var lastTermReason string
		for _, cs := range p.Status.ContainerStatuses {
			if cs.State.Terminated != nil && cs.State.Terminated.Reason != "" {
				lastTermReason = cs.State.Terminated.Reason
			} else if cs.LastTerminationState.Terminated != nil && cs.LastTerminationState.Terminated.Reason != "" {
				lastTermReason = cs.LastTerminationState.Terminated.Reason
			}
		}
		for _, cs := range p.Status.InitContainerStatuses {
			if lastTermReason == "" {
				if cs.State.Terminated != nil && cs.State.Terminated.Reason != "" {
					lastTermReason = cs.State.Terminated.Reason
				} else if cs.LastTerminationState.Terminated != nil && cs.LastTerminationState.Terminated.Reason != "" {
					lastTermReason = cs.LastTerminationState.Terminated.Reason
				}
			}
		}

		var reqCPU, reqMem, limCPU, limMem resource.Quantity
		var hasReqCPU, hasReqMem, hasLimCPU, hasLimMem bool
		for _, container := range p.Spec.Containers {
			if cpu, ok := container.Resources.Requests[corev1.ResourceCPU]; ok {
				reqCPU.Add(cpu)
				hasReqCPU = true
			}
			if mem, ok := container.Resources.Requests[corev1.ResourceMemory]; ok {
				reqMem.Add(mem)
				hasReqMem = true
			}
			if cpu, ok := container.Resources.Limits[corev1.ResourceCPU]; ok {
				limCPU.Add(cpu)
				hasLimCPU = true
			}
			if mem, ok := container.Resources.Limits[corev1.ResourceMemory]; ok {
				limMem.Add(mem)
				hasLimMem = true
			}
		}

		var requestsCPUStr, requestsMemStr, limitsCPUStr, limitsMemStr string
		if hasReqCPU {
			requestsCPUStr = reqCPU.String()
		}
		if hasReqMem {
			requestsMemStr = reqMem.String()
		}
		if hasLimCPU {
			limitsCPUStr = limCPU.String()
		}
		if hasLimMem {
			limitsMemStr = limMem.String()
		}

		podInfos = append(podInfos, PodInfo{
			Name:           p.Name,
			Phase:          string(p.Status.Phase),
			Restarts:       restarts,
			LastTermReason: lastTermReason,
			CreatedAt:      p.CreationTimestamp.Time,
			RequestsCPU:    requestsCPUStr,
			RequestsMem:    requestsMemStr,
			LimitsCPU:      limitsCPUStr,
			LimitsMem:      limitsMemStr,
		})
	}

	return podInfos, nil
}

// ListEvents returns recent events for managed processes.
func (c *Client) ListEvents(ctx context.Context, namespace string, limit int) ([]EventInfo, error) {
	eventList, err := c.clientset.CoreV1().Events(namespace).List(ctx, metav1.ListOptions{
		FieldSelector: "type=Warning",
	})
	if err != nil {
		return nil, fmt.Errorf("list events: %w", err)
	}

	var warnings []EventInfo
	for _, e := range eventList.Items {
		if e.Type != "Warning" {
			continue
		}

		t := e.LastTimestamp.Time
		if t.IsZero() {
			if !e.EventTime.IsZero() {
				t = e.EventTime.Time
			} else {
				t = e.FirstTimestamp.Time
			}
		}
		if t.IsZero() {
			t = e.CreationTimestamp.Time
		}

		warnings = append(warnings, EventInfo{
			Type:           e.Type,
			Reason:         e.Reason,
			Message:        e.Message,
			InvolvedObject: fmt.Sprintf("%s/%s", e.InvolvedObject.Kind, e.InvolvedObject.Name),
			LastTimestamp:  t,
		})
	}

	sort.Slice(warnings, func(i, j int) bool {
		return warnings[i].LastTimestamp.After(warnings[j].LastTimestamp)
	})

	if limit > 0 && len(warnings) > limit {
		warnings = warnings[:limit]
	}

	return warnings, nil
}

// GetProcessDetail returns aggregated detail for a single process.
func (c *Client) GetProcessDetail(ctx context.Context, namespace, name string) (*ProcessDetail, error) {
	proc, err := c.GetProcess(ctx, namespace, name)
	if err != nil {
		return nil, err
	}

	pods, err := c.ListPods(ctx, namespace, name)
	if err != nil {
		return nil, err
	}

	allEvents, err := c.ListEvents(ctx, namespace, 0)
	if err != nil {
		return nil, err
	}

	var events []EventInfo
	for _, e := range allEvents {
		parts := strings.SplitN(e.InvolvedObject, "/", 2)
		if len(parts) < 2 {
			continue
		}
		objName := parts[1]
		if objName == name || strings.HasPrefix(objName, name+"-") {
			events = append(events, e)
		}
	}

	return &ProcessDetail{
		Process: *proc,
		Pods:    pods,
		Events:  events,
	}, nil
}

func strPtr(s string) *string {
	return &s
}

func (c *Client) deploymentToProcess(d appsv1.Deployment) models.Process {
	p := models.Process{
		Name:      d.Name,
		Namespace: d.Namespace,
		CreatedAt: d.CreationTimestamp.Time,
	}
	if len(d.Spec.Template.Spec.Containers) > 0 {
		container := d.Spec.Template.Spec.Containers[0]
		p.Image = container.Image
		if len(container.Ports) > 0 {
			p.Port = container.Ports[0].ContainerPort
		}
		if len(container.Env) > 0 {
			p.Env = make(map[string]string)
			for _, e := range container.Env {
				p.Env[e.Name] = e.Value
			}
		}
		if container.Resources.Limits.Memory() != nil {
			p.MemoryLimit = container.Resources.Limits.Memory().String()
		}
	}
	if d.Spec.Template.Spec.NodeSelector != nil {
		p.Arch = d.Spec.Template.Spec.NodeSelector["kubernetes.io/arch"]
	}
	p.Replicas = 1
	if d.Spec.Replicas != nil {
		p.Replicas = *d.Spec.Replicas
	}
	return p
}
