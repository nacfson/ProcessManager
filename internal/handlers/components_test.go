package handlers

import (
	"strings"
	"testing"
)

func TestComponentsRender(t *testing.T) {
	tmpl, err := NewTemplate()
	if err != nil {
		t.Fatalf("NewTemplate failed: %v", err)
	}
	cases := []struct {
		name string
		tpl  string
		data any
		want string
	}{
		{"components/card", "components/card", map[string]any{"Content": "hello"}, "hello"},
		{"components/badge", "components/badge", map[string]any{"Status": "Ready", "Label": "Ready"}, "Ready"},
		{"components/empty-state", "components/empty-state", map[string]any{"Message": "nothing"}, "nothing"},
	}
	for _, c := range cases {
		var sb strings.Builder
		if err := tmpl.ExecuteTemplate(&sb, c.tpl, c.data); err != nil {
			t.Errorf("%s: %v", c.name, err)
			continue
		}
		if !strings.Contains(sb.String(), c.want) {
			t.Errorf("%s: expected %q in output", c.name, c.want)
		}
	}
}
