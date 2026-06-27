package handlers

import (
	"context"
	"fmt"
	"html/template"
	"io/fs"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strings"

	"processmanager/internal/auth"
	"processmanager/internal/k8s"
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

// templateRoot returns the absolute path to the web/templates directory.
// It derives the project root from the location of this source file so tests
// work regardless of the current working directory.
func templateRoot() (string, error) {
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		return "", fmt.Errorf("unable to determine source path")
	}
	// internal/handlers/handlers.go -> project root -> web/templates
	root := filepath.Dir(filepath.Dir(filepath.Dir(file)))
	return filepath.Join(root, "web", "templates"), nil
}

// NewTemplate loads all templates under web/templates recursively and registers helpers.
func NewTemplate() (*template.Template, error) {
	root := template.New("").Funcs(template.FuncMap{
		"add": func(a, b int) int { return a + b },
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

	tmplDir, err := templateRoot()
	if err != nil {
		return nil, fmt.Errorf("locate templates: %w", err)
	}

	err = filepath.WalkDir(tmplDir, func(path string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() || !strings.HasSuffix(path, ".html") {
			return err
		}
		b, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		name := filepath.ToSlash(path)
		name = strings.TrimPrefix(name, filepath.ToSlash(tmplDir)+"/")
		name = strings.TrimPrefix(name, "/")
		name = strings.TrimSuffix(name, ".html")
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

// Render writes a full HTML page.
func Render(tmpl *template.Template, w http.ResponseWriter, name string, data any) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if err := tmpl.ExecuteTemplate(w, name, data); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
	}
}

// RenderFragment writes an HTMX partial.
func RenderFragment(tmpl *template.Template, w http.ResponseWriter, name string, data any) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if err := tmpl.ExecuteTemplate(w, name, data); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
	}
}

// RenderError returns a concise error as plain text or HTML fragment.
func RenderError(w http.ResponseWriter, err error, code int) {
	w.Header().Set("Content-Type", "text/plain")
	w.WriteHeader(code)
	fmt.Fprint(w, err.Error())
}
