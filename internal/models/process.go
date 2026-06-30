package models

import "time"

// Process represents a web process managed by the dashboard.
type Process struct {
	Name        string            `json:"name"`
	Image       string            `json:"image"`
	Port        int32             `json:"port"`
	Replicas    int32             `json:"replicas"`
	Env         map[string]string `json:"env"`
	Arch        string            `json:"arch"` // any, amd64, arm64
	MemoryLimit string            `json:"memoryLimit"`
	PublicURL   string            `json:"publicURL"`
	Namespace   string            `json:"namespace"`
	Ready       int32             `json:"ready"`
	Total       int32             `json:"total"`
	CreatedAt   time.Time         `json:"createdAt"`
}

// Validate checks basic rules for a Process.
func (p *Process) Validate() error {
	if p.Name == "" {
		return ErrProcessNameRequired
	}
	if p.Image == "" {
		return ErrProcessImageRequired
	}
	if p.Port <= 0 {
		p.Port = 8080
	}
	if p.Replicas <= 0 {
		p.Replicas = 1
	}
	if p.Namespace == "" {
		p.Namespace = "default"
	}
	if p.Arch == "" {
		p.Arch = "any"
	}
	return nil
}
