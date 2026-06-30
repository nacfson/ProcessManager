package models

import "testing"

func TestProcessStatusFields(t *testing.T) {
	s := ProcessStatus{
		Name:             "portfolio",
		Ready:            2,
		Total:            3,
		ServiceEndpoints: 2,
		IngressReady:     true,
		TLSReady:         true,
		Host:             "portfolio.nacfson.cloud",
	}
	if s.Ready != 2 {
		t.Fatalf("expected Ready 2, got %d", s.Ready)
	}
}
