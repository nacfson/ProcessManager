package models

import "errors"

var (
	ErrProcessNameRequired  = errors.New("process name is required")
	ErrProcessImageRequired = errors.New("container image is required")
	ErrProcessNotFound      = errors.New("process not found")
)
