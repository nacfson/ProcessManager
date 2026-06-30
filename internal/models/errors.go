package models

import "errors"

var (
	ErrProcessNameRequired  = errors.New("process name is required")
	ErrProcessImageRequired = errors.New("container image is required")
	ErrProcessNotFound      = errors.New("process not found")
	ErrInvalidReplicas      = errors.New("replicas must be between 0 and 10")
)
