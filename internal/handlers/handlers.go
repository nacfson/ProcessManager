package handlers

import (
	"context"
	"fmt"
	"html/template"
	"io/fs"
	"log/slog"
	"net/http"
	"strings"

	"processmanager/internal/auth"
	"processmanager/internal/k8s"
	"processmanager/web/templates"
)

// Deps carries shared dependencies for all domain handlers.
type Deps struct {
	Client    k8s.ClientInterface
	AuthStore *auth.Store
	Provisioner interface {
		ListDatabases(ctx context.Context) ([]string, error)
	}
	Namespace string
	Domain    string
	AuthMW    *auth.Middleware
	Tmpl      *template.Template
}

// NewTemplate loads all templates under web/templates recursively and registers helpers.
func NewTemplate() (*template.Template, error) {
	root := template.New("").Funcs(template.FuncMap{
		"add":   func(a, b int) int { return a + b },
		"lower": strings.ToLower,
		"upper": strings.ToUpper,
		"dict": func(values ...any) (map[string]any, error) {
			if len(values)%2 != 0 {
				return nil, fmt.Errorf("dict expects even number of arguments")
			}
			m := make(map[string]any, len(values)/2)
			for i := 0; i < len(values); i += 2 {
				key, ok := values[i].(string)
				if !ok {
					return nil, fmt.Errorf("dict keys must be strings")
				}
				m[key] = values[i+1]
			}
			return m, nil
		},
	})

	tmplFS, err := fs.Sub(templates.FS, ".")
	if err != nil {
		return nil, fmt.Errorf("open templates fs: %w", err)
	}

	err = fs.WalkDir(tmplFS, ".", func(path string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() || !strings.HasSuffix(path, ".html") {
			return err
		}
		b, err := fs.ReadFile(tmplFS, path)
		if err != nil {
			return err
		}
		name := strings.TrimSuffix(path, ".html")
		_, err = root.New(name).Parse(string(b))
		return err
	})
	if err != nil {
		return nil, fmt.Errorf("parse templates: %w", err)
	}
	return root, nil
}

// RequireAuth wraps a handler with auth when middleware is present.
func RequireAuth(amw *auth.Middleware, next http.HandlerFunc) http.HandlerFunc {
	if amw == nil {
		return next
	}
	return amw.RequireAuth(next).ServeHTTP
}

// render writes a template to the response with the given content type.
// It is the shared implementation for Render and RenderFragment.
func render(w http.ResponseWriter, contentType string, tmpl *template.Template, name string, data any) {
	w.Header().Set("Content-Type", contentType)
	if err := tmpl.ExecuteTemplate(w, name, data); err != nil {
		slog.Error("template execution failed", "name", name, "error", err)
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = fmt.Fprint(w, "Internal Server Error")
	}
}

// Render writes a full HTML page.
func Render(tmpl *template.Template, w http.ResponseWriter, name string, data any) {
	render(w, "text/html; charset=utf-8", tmpl, name, data)
}

// RenderFragment writes an HTMX partial.
func RenderFragment(tmpl *template.Template, w http.ResponseWriter, name string, data any) {
	render(w, "text/html; charset=utf-8", tmpl, name, data)
}

// RenderError returns a concise, user-safe error label.
// The full error is logged server-side for debugging via SSH/kubectl.
// A nil error is treated as an unspecified error.
func RenderError(w http.ResponseWriter, err error, code int) {
	slog.Error("handler error", "code", code, "error", err)
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	w.WriteHeader(code)
	_, _ = fmt.Fprint(w, "Error")
}
