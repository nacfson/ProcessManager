package handlers

import (
	"context"
	"fmt"
	"html/template"
	"io/fs"
	"log/slog"
	"net/http"
	"sort"
	"strings"
	"time"

	"processmanager/internal/auth"
	"processmanager/internal/k8s"
	"processmanager/web/templates"
)

var AuthHost string
var AuthHostWithPort string

// AuthStore abstracts auth/shared persistence for handler testing.
type AuthStore interface {
	CreateProject(ctx context.Context, name, namespace, domainHost, projType string, dbRequired, authEnabled bool) (*auth.Project, error)
	GetProjectByName(ctx context.Context, name string) (*auth.Project, error)
	ListProjects(ctx context.Context) ([]auth.Project, error)
	ListUsers(ctx context.Context) ([]auth.User, error)
	ListAuditEvents(ctx context.Context, limit int) ([]auth.AuditEvent, error)
}

// Deps carries shared dependencies for all domain handlers.
type Deps struct {
	Client      k8s.ClientInterface
	AuthStore   AuthStore
	Provisioner interface {
		ListDatabases(ctx context.Context) ([]string, error)
	}
	Domain           string
	IngressHTTPSPort int32
	AuthMW           *auth.Middleware
	Tmpl             *template.Template
}

// ProjectNamespace resolves the Kubernetes namespace for a project by name.
// It returns an error if the auth store is unavailable or the project is unknown.
func (d *Deps) ProjectNamespace(ctx context.Context, projectName string) (string, error) {
	if d.AuthStore == nil {
		return "", fmt.Errorf("auth store not configured")
	}
	p, err := d.AuthStore.GetProjectByName(ctx, projectName)
	if err != nil {
		return "", fmt.Errorf("lookup project: %w", err)
	}
	if p == nil {
		return "", fmt.Errorf("project %q not found", projectName)
	}
	return p.Namespace, nil
}

// ManagedNamespaces returns the distinct Kubernetes namespaces for all registered projects.
func (d *Deps) ManagedNamespaces(ctx context.Context) ([]string, error) {
	if d.AuthStore == nil {
		return nil, fmt.Errorf("auth store not configured")
	}
	projects, err := d.AuthStore.ListProjects(ctx)
	if err != nil {
		return nil, fmt.Errorf("list projects: %w", err)
	}
	seen := make(map[string]struct{}, len(projects))
	namespaces := make([]string, 0, len(projects))
	for _, p := range projects {
		if _, ok := seen[p.Namespace]; ok {
			continue
		}
		seen[p.Namespace] = struct{}{}
		namespaces = append(namespaces, p.Namespace)
	}
	sort.Strings(namespaces)
	return namespaces, nil
}

// PublicURL returns the external HTTPS URL for a process name.
// When IngressHTTPSPort is non-zero and not 443, the port is included.
func (d *Deps) PublicURL(name string) string {
	host := fmt.Sprintf("%s.%s", name, d.Domain)
	if d.IngressHTTPSPort > 0 && d.IngressHTTPSPort != 443 {
		return fmt.Sprintf("https://%s:%d", host, d.IngressHTTPSPort)
	}
	return fmt.Sprintf("https://%s", host)
}

// NewTemplate loads all templates under web/templates recursively and registers helpers.
func NewTemplate() (*template.Template, error) {
	root := template.New("").Funcs(template.FuncMap{
		"add":   func(a, b int) int { return a + b },
		"lower": strings.ToLower,
		"upper": strings.ToUpper,
		"timeago": func(t time.Time) string {
			if t.IsZero() {
				return "never"
			}
			d := time.Since(t)
			switch {
			case d < time.Minute:
				return "just now"
			case d < time.Hour:
				return fmt.Sprintf("%dm ago", int(d.Minutes()))
			case d < 24*time.Hour:
				return fmt.Sprintf("%dh ago", int(d.Hours()))
			default:
				return fmt.Sprintf("%dd ago", int(d.Hours()/24))
			}
		},
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
		"colorForIndex": func(i int) string {
			colors := []string{"#3b82f6", "#a855f7", "#14b8a6", "#f97316", "#ec4899", "#84cc16"}
			return colors[i%len(colors)]
		},
		"barClass": func(percent int) string {
			if percent >= 80 {
				return "fill-red"
			}
			if percent >= 60 {
				return "fill-yellow"
			}
			return "fill-green"
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

// RequireAdmin wraps a handler requiring the user to be an admin.
// It first applies RequireAuth, then checks IsAdmin.
func RequireAdmin(amw *auth.Middleware, next http.HandlerFunc) http.HandlerFunc {
	authed := RequireAuth(amw, func(w http.ResponseWriter, r *http.Request) {
		sess := auth.UserContext(r.Context())
		if sess == nil || !sess.IsAdmin {
			http.Error(w, "Forbidden", http.StatusForbidden)
			return
		}
		next(w, r)
	})
	return authed
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
// Page templates define a content block named "<template-name>-content"; this
// function pre-renders that block and injects it as ContentHTML so the shared
// layout can place it without all pages fighting over a global "content" block.
func Render(tmpl *template.Template, w http.ResponseWriter, r *http.Request, name string, data any) {
	if m, ok := data.(map[string]any); ok {
		m["AuthHost"] = AuthHost
		m["AuthHostWithPort"] = AuthHostWithPort
		m["ManagerHost"] = r.Host
		m["CSRFToken"] = CSRFToken(r)
	}
	if sess := auth.UserContext(r.Context()); sess != nil {
		if m, ok := data.(map[string]any); ok {
			m["UserEmail"] = sess.Email
			m["IsAdmin"] = sess.IsAdmin
		}
	}
	if m, ok := data.(map[string]any); ok {
		if _, exists := m["ContentHTML"]; !exists {
			var buf strings.Builder
			if err := tmpl.ExecuteTemplate(&buf, name+"-content", data); err != nil {
				slog.Error("content template pre-render failed", "name", name+"-content", "error", err)
			} else {
				m["ContentHTML"] = template.HTML(buf.String())
			}
		}
	}
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
