# Dashboard UI Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Refactor the existing Go + HTMX process dashboard into a full platform control panel with reusable components, domain-split handlers, live status polling, and handler tests.

**Architecture:** The manager service keeps serving server-rendered HTML. Handler code moves from one file into domain packages (`processes`, `projects`, `databases`, `users`, `settings`, `audit`). A shared `internal/handlers` package provides template loading, auth wrapping, and a `Deps` struct. Reusable HTML components live under `web/templates/components/`. A fake Kubernetes client interface enables table-driven handler tests.

**Tech Stack:** Go 1.22+, `html/template`, HTMX 1.9.x, Kubernetes client-go, pgx/v5, no frontend build step.

## Global Constraints

- Keep the existing Go + HTMX stack; do not add React/Vue/Svelte.
- All dashboard pages require authentication when `AUTH_DATABASE_URL` is set.
- Error UI shows only *which* error occurred; detailed debugging stays in SSH/kubectl.
- Live status uses HTMX polling (`hx-trigger="every 3s"`), not WebSocket/SSE.
- Use exact file paths from this plan.
- Each task ends with a passing test or a runnable check.

---

## File Structure

New and modified files:

```
cmd/manager/main.go                         # register all domain routes
internal/handlers/handlers.go              # shared Deps, template loader, auth helper
internal/handlers/processes/processes.go   # /processes/* routes and handlers
internal/handlers/projects/projects.go     # /projects/* routes and handlers
internal/handlers/databases/databases.go   # /databases/* routes and handlers
internal/handlers/users/users.go           # /users/* routes and handlers
internal/handlers/settings/settings.go     # /settings/* routes and handlers
internal/handlers/audit/audit.go           # /activity/* routes and handlers
internal/handlers/home/home.go             # / dashboard home
internal/k8s/client.go                     # add ClientInterface, GetProcess, ListPods helpers
internal/k8s/fake/fake.go                  # in-memory fake k8s client
internal/db/provisioner.go                 # add ListDatabases
internal/auth/store.go                     # add ListUsers, audit table + methods
web/templates/components/layout.html       # shared shell + nav
web/templates/components/card.html
web/templates/components/page-header.html
web/templates/components/data-table.html
web/templates/components/form-field.html
web/templates/components/badge.html
web/templates/components/button.html
web/templates/components/toast.html
web/templates/components/empty-state.html
web/templates/components/modal.html
web/templates/pages/processes/list.html
web/templates/pages/processes/form.html
web/templates/pages/processes/row.html
web/templates/pages/processes/logs.html
web/templates/pages/processes/status.html
web/templates/pages/projects/list.html
web/templates/pages/projects/form.html
web/templates/pages/projects/row.html
web/templates/pages/databases/list.html
web/templates/pages/users/list.html
web/templates/pages/settings/settings.html
web/templates/pages/activity/list.html
web/templates/pages/home/home.html
```

---

## Task 1: Shared Handler Infrastructure

**Files:**
- Modify: `internal/handlers/handlers.go`
- Test: `internal/handlers/handlers_test.go`

**Interfaces:**
- Consumes: `*auth.Middleware`, `*k8s.Client`, `*pgxpool.Pool` (auth/shared DB pools).
- Produces: `handlers.Deps` struct, `handlers.NewTemplate()`, `handlers.RequireAuth()`.

- [ ] **Step 1: Write the failing test**

Create `internal/handlers/handlers_test.go`:

```go
package handlers

import (
    "net/http"
    "net/http/httptest"
    "testing"
)

func TestNewTemplateLoadsComponents(t *testing.T) {
    tmpl, err := NewTemplate()
    if err != nil {
        t.Fatalf("NewTemplate failed: %v", err)
    }
    if tmpl.Lookup("components/layout") == nil {
        t.Fatalf("layout template not loaded")
    }
}

func TestRequireAuthWithoutMiddleware(t *testing.T) {
    called := false
    h := RequireAuth(nil, func(w http.ResponseWriter, r *http.Request) {
        called = true
        w.WriteHeader(http.StatusOK)
    })
    rec := httptest.NewRecorder()
    h(rec, httptest.NewRequest("GET", "/", nil))
    if rec.Code != http.StatusOK || !called {
        t.Errorf("expected handler to be called without middleware, got %d", rec.Code)
    }
}
```

- [ ] **Step 2: Run test to verify it fails**

```bash
go test ./internal/handlers -run TestNewTemplateLoadsComponents -v
```

Expected: FAIL — `NewTemplate` or component templates missing.

- [ ] **Step 3: Implement shared handler infrastructure**

Replace `internal/handlers/handlers.go` with:

```go
package handlers

import (
    "context"
    "fmt"
    "html/template"
    "io/fs"
    "net/http"
    "os"
    "path/filepath"
    "strings"

    "processmanager/internal/auth"
    "processmanager/internal/k8s"
)

// Deps carries shared dependencies for all domain handlers.
type Deps struct {
    Client      k8s.ClientInterface
    AuthStore   *auth.Store
    Provisioner interface {
        ListDatabases(ctx context.Context) ([]string, error)
    }
    Namespace   string
    Domain      string
    AuthMW      *auth.Middleware
    Tmpl        *template.Template
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

    err := filepath.WalkDir("web/templates", func(path string, d fs.DirEntry, err error) error {
        if err != nil || d.IsDir() || !strings.HasSuffix(path, ".html") {
            return err
        }
        b, err := os.ReadFile(path)
        if err != nil {
            return err
        }
        name := filepath.ToSlash(path)
        name = strings.TrimPrefix(name, "web/templates/")
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
```

Note: `context` import is needed.

- [ ] **Step 4: Run tests**

```bash
go test ./internal/handlers -v
```

Expected: PASS (tests verify infrastructure loads; component templates will be added in Task 2).

- [ ] **Step 5: Commit**

```bash
git add internal/handlers/handlers.go internal/handlers/handlers_test.go
git commit -m "feat(handlers): shared template loader, deps, and auth helper"
```

---

## Task 2: Reusable UI Components

**Files:**
- Create: all files under `web/templates/components/`
- Test: `internal/handlers/components_test.go`

**Interfaces:**
- Consumes: `handlers.NewTemplate()`.
- Produces: component templates usable by page templates.

- [ ] **Step 1: Write the failing test**

Create `internal/handlers/components_test.go`:

```go
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
```

- [ ] **Step 2: Run test to verify it fails**

```bash
go test ./internal/handlers -run TestComponentsRender -v
```

Expected: FAIL — component templates missing.

- [ ] **Step 3: Create component templates**

Create each file under `web/templates/components/`:

`layout.html`:

```html
<!DOCTYPE html>
<html lang="en">
<head>
    <meta charset="UTF-8">
    <meta name="viewport" content="width=device-width, initial-scale=1.0">
    <title>{{or .Title "Process Manager"}}</title>
    <script src="https://unpkg.com/htmx.org@1.9.12"></script>
    <style>
        :root { --bg: #0f172a; --panel: #1e293b; --text: #e2e8f0; --accent: #38bdf8; --danger: #f87171; --muted: #94a3b8; }
        * { box-sizing: border-box; }
        body { margin: 0; font-family: system-ui, -apple-system, sans-serif; background: var(--bg); color: var(--text); line-height: 1.5; }
        header { padding: 1rem 2rem; border-bottom: 1px solid #334155; display: flex; justify-content: space-between; align-items: center; }
        header h1 { margin: 0; font-size: 1.25rem; }
        nav a { color: var(--muted); text-decoration: none; margin-left: 1.25rem; font-size: 0.875rem; }
        nav a:hover, nav a.active { color: var(--accent); }
        main { max-width: 1100px; margin: 2rem auto; padding: 0 1rem; }
        .card { background: var(--panel); border-radius: 0.5rem; padding: 1.5rem; margin-bottom: 1.5rem; }
        .btn { display: inline-block; padding: 0.5rem 1rem; border-radius: 0.375rem; text-decoration: none; border: none; cursor: pointer; font-size: 0.875rem; }
        .btn-primary { background: var(--accent); color: #0f172a; }
        .btn-danger { background: var(--danger); color: #fff; }
        table { width: 100%; border-collapse: collapse; margin-top: 1rem; }
        th, td { text-align: left; padding: 0.75rem; border-bottom: 1px solid #334155; }
        th { color: var(--muted); font-weight: 500; }
        a { color: var(--accent); }
        label { display: block; margin: 1rem 0 0.25rem; color: var(--muted); font-size: 0.875rem; }
        input, select, textarea { width: 100%; padding: 0.5rem; background: #0f172a; border: 1px solid #334155; color: var(--text); border-radius: 0.375rem; }
        textarea { min-height: 120px; font-family: monospace; font-size: 0.875rem; }
        .row { display: flex; gap: 1rem; }
        .row > * { flex: 1; }
        .status { font-size: 0.875rem; }
        .status.ready { color: #4ade80; }
        .status.pending { color: #facc15; }
        .status.error { color: var(--danger); }
        .toast { position: fixed; top: 1rem; right: 1rem; background: var(--panel); border: 1px solid #334155; padding: 1rem; border-radius: 0.375rem; }
        .page-header { display: flex; justify-content: space-between; align-items: center; margin-bottom: 1rem; }
        .page-header h2 { margin: 0; }
        .empty { text-align: center; color: var(--muted); padding: 2rem; }
        .modal-overlay { position: fixed; inset: 0; background: rgba(0,0,0,0.7); display: flex; align-items: center; justify-content: center; }
        .modal { background: var(--panel); padding: 1.5rem; border-radius: 0.5rem; min-width: 320px; }
    </style>
</head>
<body>
    <header>
        <h1>Process Manager</h1>
        <nav>
            <a href="/" class="{{if eq .Nav "home"}}active{{end}}">Dashboard</a>
            <a href="/processes" class="{{if eq .Nav "processes"}}active{{end}}">Processes</a>
            <a href="/projects" class="{{if eq .Nav "projects"}}active{{end}}">Projects</a>
            <a href="/databases" class="{{if eq .Nav "databases"}}active{{end}}">Databases</a>
            <a href="/users" class="{{if eq .Nav "users"}}active{{end}}">Users</a>
            <a href="/activity" class="{{if eq .Nav "activity"}}active{{end}}">Activity</a>
            <a href="/settings" class="{{if eq .Nav "settings"}}active{{end}}">Settings</a>
        </nav>
    </header>
    <main>
        {{template "content" .}}
    </main>
    <div id="toast-container"></div>
</body>
</html>
```

`card.html`:

```html
<div class="card">{{.Content}}</div>
```

`page-header.html`:

```html
<div class="page-header">
    <h2>{{.Title}}</h2>
    {{if .Action}}
    <a href="{{.Href}}" class="btn btn-primary">{{.Action}}</a>
    {{end}}
</div>
```

`data-table.html`:

```html
<table>
    <thead>
        <tr>
            {{range .Headers}}
            <th>{{.}}</th>
            {{end}}
        </tr>
    </thead>
    <tbody>
        {{range .Rows}}
        <tr>
            {{range .}}
            <td>{{.}}</td>
            {{end}}
        </tr>
        {{end}}
    </tbody>
</table>
```

`form-field.html`:

```html
<div>
    <label for="{{.ID}}">{{.Label}}</label>
    {{.Input}}
    {{if .Error}}<div style="color:var(--danger);font-size:0.875rem;margin-top:0.25rem;">{{.Error}}</div>{{end}}
</div>
```

`badge.html`:

```html
<span class="status {{.Status | lower}}">{{.Label}}</span>
```

`button.html`:

```html
<button type="{{or .Type "button"}}" class="btn {{or .Class "btn-primary"}}" {{if .HXPost}}hx-post="{{.HXPost}}"{{end}} {{if .HXGet}}hx-get="{{.HXGet}}"{{end}} {{if .HXTarget}}hx-target="{{.HXTarget}}"{{end}} {{if .HXSwap}}hx-swap="{{.HXSwap}}"{{end}} {{if .HXConfirm}}hx-confirm="{{.HXConfirm}}"{{end}}>
    {{.Label}}
</button>
```

`toast.html`:

```html
<div class="toast" hx-trigger="load" hx-swap="outerHTML settle:3s">
    {{.Message}}
</div>
```

`empty-state.html`:

```html
<div class="empty">{{.Message}}</div>
```

`modal.html`:

```html
<div class="modal-overlay">
    <div class="modal">
        <h3>{{.Title}}</h3>
        <p>{{.Message}}</p>
        <div style="display:flex;justify-content:flex-end;gap:0.5rem;margin-top:1rem;">
            <button class="btn" onclick="this.closest('.modal-overlay').remove()">Cancel</button>
            <button class="btn btn-danger" hx-post="{{.ConfirmURL}}" hx-target="{{.Target}}" hx-swap="{{.Swap}}">{{.ConfirmLabel}}</button>
        </div>
    </div>
</div>
```

- [ ] **Step 4: Add lower/upper helpers**

Edit `internal/handlers/handlers.go` to add to FuncMap:

```go
"lower": strings.ToLower,
"upper": strings.ToUpper,
```

- [ ] **Step 5: Run tests**

```bash
go test ./internal/handlers -run TestComponentsRender -v
```

Expected: PASS.

- [ ] **Step 6: Commit**

```bash
git add web/templates/components internal/handlers/components_test.go internal/handlers/handlers.go
git commit -m "feat(ui): reusable HTMX component library"
```

---

## Task 3: Process Domain Refactor

**Files:**
- Create: `internal/handlers/processes/processes.go`
- Create: page templates under `web/templates/pages/processes/`
- Delete: old `web/templates/list.html`, `form.html`, `process-row.html`, `logs.html`
- Modify: `cmd/manager/main.go` (route wiring in Task 5)

**Interfaces:**
- Consumes: `handlers.Deps`, `k8s.ClientInterface`.
- Produces: `processes.Handler` with `RegisterRoutes(mux *http.ServeMux)`.

- [ ] **Step 1: Write the failing test**

Create `internal/handlers/processes/processes_test.go`:

```go
package processes

import (
    "html/template"
    "net/http"
    "net/http/httptest"
    "strings"
    "testing"

    "processmanager/internal/handlers"
    "processmanager/internal/k8s/fake"
)

func TestListRendersPage(t *testing.T) {
    deps := handlers.Deps{
        Client:    fake.NewClient(),
        Namespace: "test-ns",
        Domain:    "test.local",
        Tmpl:      mustTemplate(t),
    }
    h := New(deps)
    mux := http.NewServeMux()
    h.RegisterRoutes(mux)

    rec := httptest.NewRecorder()
    mux.ServeHTTP(rec, httptest.NewRequest("GET", "/processes", nil))
    if rec.Code != http.StatusOK {
        t.Fatalf("expected 200, got %d", rec.Code)
    }
    if !strings.Contains(rec.Body.String(), "Processes") {
        t.Errorf("missing page title")
    }
}

func mustTemplate(t *testing.T) *template.Template {
    tmpl, err := handlers.NewTemplate()
    if err != nil {
        t.Fatalf("load templates: %v", err)
    }
    return tmpl
}
```

- [ ] **Step 2: Run test to verify it fails**

```bash
go test ./internal/handlers/processes -v
```

Expected: FAIL — package or templates missing.

- [ ] **Step 3: Create k8s client interface and fake**

Modify `internal/k8s/client.go` to add at the top of the file (after imports):

```go
// ClientInterface is the surface used by HTTP handlers.
type ClientInterface interface {
    ListProcesses(ctx context.Context, namespace string) ([]models.Process, error)
    GetProcess(ctx context.Context, namespace, name string) (*models.Process, error)
    CreateProcess(ctx context.Context, p models.Process, domain string) error
    DeleteProcess(ctx context.Context, namespace, name string) error
    GetPodLogs(ctx context.Context, namespace, name string) (string, error)
}
```

Create `internal/k8s/fake/fake.go`:

```go
package fake

import (
    "context"
    "fmt"

    "processmanager/internal/k8s"
    "processmanager/internal/models"
)

// Client implements k8s.ClientInterface in memory.
type Client struct {
    processes []models.Process
}

// NewClient creates an empty fake client.
func NewClient() k8s.ClientInterface {
    return &Client{}
}

func (c *Client) ListProcesses(ctx context.Context, namespace string) ([]models.Process, error) {
    return c.processes, nil
}

func (c *Client) GetProcess(ctx context.Context, namespace, name string) (*models.Process, error) {
    for _, p := range c.processes {
        if p.Name == name && p.Namespace == namespace {
            return &p, nil
        }
    }
    return nil, models.ErrProcessNotFound
}

func (c *Client) CreateProcess(ctx context.Context, p models.Process, domain string) error {
    c.processes = append(c.processes, p)
    return nil
}

func (c *Client) DeleteProcess(ctx context.Context, namespace, name string) error {
    for i, p := range c.processes {
        if p.Name == name && p.Namespace == namespace {
            c.processes = append(c.processes[:i], c.processes[i+1:]...)
            return nil
        }
    }
    return models.ErrProcessNotFound
}

func (c *Client) GetPodLogs(ctx context.Context, namespace, name string) (string, error) {
    return fmt.Sprintf("fake logs for %s/%s", namespace, name), nil
}
```

- [ ] **Step 4: Implement processes handler package**

Create `internal/handlers/processes/processes.go`:

```go
package processes

import (
    "context"
    "fmt"
    "html/template"
    "net/http"
    "strings"

    "processmanager/internal/auth"
    "processmanager/internal/handlers"
    "processmanager/internal/k8s"
    "processmanager/internal/models"
)

// Handler handles process pages and fragments.
type Handler struct {
    deps handlers.Deps
}

// New creates a Handler.
func New(deps handlers.Deps) *Handler {
    return &Handler{deps: deps}
}

// RegisterRoutes registers process routes.
func (h *Handler) RegisterRoutes(mux *http.ServeMux) {
    mux.HandleFunc("GET /processes", h.require(h.List))
    mux.HandleFunc("GET /processes/new", h.require(h.NewForm))
    mux.HandleFunc("POST /processes", h.require(h.Create))
    mux.HandleFunc("POST /processes/{name}/delete", h.require(h.Delete))
    mux.HandleFunc("GET /processes/{name}/logs", h.require(h.Logs))
    mux.HandleFunc("GET /processes/{name}/logs/stream", h.require(h.LogStream))
    mux.HandleFunc("GET /processes/{name}/status", h.require(h.Status))
}

func (h *Handler) require(next http.HandlerFunc) http.HandlerFunc {
    return handlers.RequireAuth(h.deps.AuthMW, next)
}

// List shows all processes.
func (h *Handler) List(w http.ResponseWriter, r *http.Request) {
    processes, err := h.deps.Client.ListProcesses(r.Context(), h.deps.Namespace)
    if err != nil {
        handlers.RenderError(w, err, http.StatusInternalServerError)
        return
    }
    for i := range processes {
        processes[i].PublicURL = fmt.Sprintf("https://%s.%s", processes[i].Name, h.deps.Domain)
    }
    handlers.Render(h.deps.Tmpl, w, "pages/processes/list", map[string]any{
        "Title":     "Processes",
        "Nav":       "processes",
        "Namespace": h.deps.Namespace,
        "Domain":    h.deps.Domain,
        "Processes": processes,
    })
}

// NewForm shows the create form.
func (h *Handler) NewForm(w http.ResponseWriter, r *http.Request) {
    handlers.Render(h.deps.Tmpl, w, "pages/processes/form", map[string]any{
        "Title":     "New Process",
        "Nav":       "processes",
        "Namespace": h.deps.Namespace,
        "Domain":    h.deps.Domain,
    })
}

// Create handles process creation.
func (h *Handler) Create(w http.ResponseWriter, r *http.Request) {
    if err := r.ParseForm(); err != nil {
        handlers.RenderError(w, err, http.StatusBadRequest)
        return
    }
    p := models.Process{
        Name:        strings.TrimSpace(r.FormValue("name")),
        Image:       strings.TrimSpace(r.FormValue("image")),
        Namespace:   h.deps.Namespace,
        Arch:        r.FormValue("arch"),
        MemoryLimit: r.FormValue("memoryLimit"),
        Env:         parseEnv(r.FormValue("env")),
    }
    if r.FormValue("port") != "" {
        var port int32
        if _, err := fmt.Sscanf(r.FormValue("port"), "%d", &port); err == nil {
            p.Port = port
        }
    }
    if r.FormValue("replicas") != "" {
        var replicas int32
        if _, err := fmt.Sscanf(r.FormValue("replicas"), "%d", &replicas); err == nil {
            p.Replicas = replicas
        }
    }
    if err := p.Validate(); err != nil {
        handlers.RenderError(w, err, http.StatusBadRequest)
        return
    }
    if err := h.deps.Client.CreateProcess(r.Context(), p, h.deps.Domain); err != nil {
        handlers.RenderError(w, err, http.StatusInternalServerError)
        return
    }
    p.PublicURL = fmt.Sprintf("https://%s.%s", p.Name, h.deps.Domain)
    handlers.RenderFragment(h.deps.Tmpl, w, "pages/processes/row", p)
}

// Delete removes a process.
func (h *Handler) Delete(w http.ResponseWriter, r *http.Request) {
    name := r.PathValue("name")
    if err := h.deps.Client.DeleteProcess(r.Context(), h.deps.Namespace, name); err != nil {
        handlers.RenderError(w, err, http.StatusInternalServerError)
        return
    }
    w.WriteHeader(http.StatusOK)
}

// Logs returns container logs page.
func (h *Handler) Logs(w http.ResponseWriter, r *http.Request) {
    name := r.PathValue("name")
    logs, err := h.deps.Client.GetPodLogs(r.Context(), h.deps.Namespace, name)
    if err != nil {
        handlers.RenderError(w, err, http.StatusInternalServerError)
        return
    }
    handlers.Render(h.deps.Tmpl, w, "pages/processes/logs", map[string]any{
        "Title": name + " Logs",
        "Nav":   "processes",
        "Name":  name,
        "Logs":  logs,
    })
}

// LogStream returns plain log text for HTMX polling.
func (h *Handler) LogStream(w http.ResponseWriter, r *http.Request) {
    name := r.PathValue("name")
    logs, err := h.deps.Client.GetPodLogs(r.Context(), h.deps.Namespace, name)
    if err != nil {
        handlers.RenderError(w, err, http.StatusInternalServerError)
        return
    }
    w.Header().Set("Content-Type", "text/plain; charset=utf-8")
    w.Write([]byte(logs))
}

// Status returns a status badge fragment for polling.
func (h *Handler) Status(w http.ResponseWriter, r *http.Request) {
    name := r.PathValue("name")
    p, err := h.deps.Client.GetProcess(r.Context(), h.deps.Namespace, name)
    if err != nil {
        handlers.RenderFragment(h.deps.Tmpl, w, "pages/processes/status", map[string]any{
            "Status": "error",
            "Label":  "Error",
        })
        return
    }
    status := "pending"
    label := fmt.Sprintf("%d/%d", p.Ready, p.Total)
    if p.Ready > 0 && p.Ready == p.Total {
        status = "ready"
    }
    handlers.RenderFragment(h.deps.Tmpl, w, "pages/processes/status", map[string]any{
        "Status": status,
        "Label":  label,
    })
}

func parseEnv(input string) map[string]string {
    env := make(map[string]string)
    for _, line := range strings.Split(input, "\n") {
        line = strings.TrimSpace(line)
        if line == "" || strings.HasPrefix(line, "#") {
            continue
        }
        parts := strings.SplitN(line, "=", 2)
        if len(parts) == 2 {
            env[strings.TrimSpace(parts[0])] = strings.TrimSpace(parts[1])
        }
    }
    return env
}
```

- [ ] **Step 5: Create process page templates**

`web/templates/pages/processes/list.html`:

```html
{{template "components/layout" .}}

{{define "content"}}
{{template "components/page-header" dict "Title" "Processes" "Action" "New Process" "Href" "/processes/new"}}
<div class="card">
    {{if .Processes}}
    <table>
        <thead>
            <tr>
                <th>Name</th>
                <th>Image</th>
                <th>Arch</th>
                <th>Status</th>
                <th>URL</th>
                <th>Actions</th>
            </tr>
        </thead>
        <tbody id="process-list">
            {{range .Processes}}
                {{template "pages/processes/row" .}}
            {{end}}
        </tbody>
    </table>
    {{else}}
        {{template "components/empty-state" dict "Message" "No processes yet."}}
    {{end}}
</div>
{{end}}
```

`web/templates/pages/processes/form.html`:

```html
{{template "components/layout" .}}

{{define "content"}}
{{template "components/page-header" dict "Title" "New Process"}}
<div class="card">
    <form hx-post="/processes" hx-target="#process-list" hx-swap="beforeend">
        <label for="name">Name</label>
        <input type="text" id="name" name="name" placeholder="portfolio" required>

        <label for="image">Container Image</label>
        <input type="text" id="image" name="image" placeholder="youruser/portfolioweb:v1" required>

        <div class="row">
            <div>
                <label for="port">Port</label>
                <input type="number" id="port" name="port" value="8080" required>
            </div>
            <div>
                <label for="replicas">Replicas</label>
                <input type="number" id="replicas" name="replicas" value="1" min="1" required>
            </div>
        </div>

        <div class="row">
            <div>
                <label for="arch">Target Architecture</label>
                <select id="arch" name="arch">
                    <option value="any">Any</option>
                    <option value="amd64">amd64 (local control plane)</option>
                    <option value="arm64">arm64 (optional local worker)</option>
                </select>
            </div>
            <div>
                <label for="memoryLimit">Memory Limit</label>
                <input type="text" id="memoryLimit" name="memoryLimit" placeholder="256Mi">
            </div>
        </div>

        <label for="env">Environment Variables</label>
        <textarea id="env" name="env" placeholder="KEY=value&#10;ANOTHER=secret"></textarea>

        <button type="submit" class="btn btn-primary" style="margin-top:1rem;">Create Process</button>
    </form>
</div>
{{end}}
```

`web/templates/pages/processes/row.html`:

```html
<tr id="process-{{.Name}}">
    <td>{{.Name}}</td>
    <td>{{.Image}}</td>
    <td>{{.Arch}}</td>
    <td>
        <span hx-get="/processes/{{.Name}}/status" hx-trigger="every 3s" hx-swap="outerHTML">
            {{template "pages/processes/status" dict "Status" "pending" "Label" "..."}}
        </span>
    </td>
    <td><a href="{{.PublicURL}}" target="_blank">{{.PublicURL}}</a></td>
    <td>
        <a href="/processes/{{.Name}}/logs">Logs</a>
        <button class="btn btn-danger" hx-post="/processes/{{.Name}}/delete" hx-target="#process-{{.Name}}" hx-swap="outerHTML" hx-confirm="Delete {{.Name}}?" style="margin-left:0.5rem;">Delete</button>
    </td>
</tr>
```

`web/templates/pages/processes/status.html`:

```html
<span class="status {{.Status}}">{{.Label}}</span>
```

`web/templates/pages/processes/logs.html`:

```html
{{template "components/layout" .}}

{{define "content"}}
{{template "components/page-header" dict "Title" (printf "%s Logs" .Name)}}
<div class="card">
    <pre id="log-pane" hx-get="/processes/{{.Name}}/logs/stream" hx-trigger="every 2s" hx-swap="innerHTML scroll-bottom" style="max-height:60vh;overflow:auto;">{{.Logs}}</pre>
</div>
{{end}}
```

- [ ] **Step 6: Remove old templates**

```bash
rm web/templates/list.html web/templates/form.html web/templates/process-row.html web/templates/logs.html
```

- [ ] **Step 7: Run tests**

```bash
go test ./internal/handlers/processes -v
```

Expected: PASS.

- [ ] **Step 8: Commit**

```bash
git add internal/handlers/processes internal/k8s/fake internal/k8s/client.go web/templates/pages/processes
git rm web/templates/list.html web/templates/form.html web/templates/process-row.html web/templates/logs.html
git commit -m "feat(processes): refactor into domain package with polling status and logs"
```

---

## Task 4: Dashboard Home

**Files:**
- Create: `internal/handlers/home/home.go`
- Create: `web/templates/pages/home/home.html`

**Interfaces:**
- Consumes: `handlers.Deps`, `auth.Store.ListProjects`, `k8s.ClientInterface.ListProcesses`.
- Produces: `home.Handler` with `RegisterRoutes(mux *http.ServeMux)`.

- [ ] **Step 1: Write the failing test**

Create `internal/handlers/home/home_test.go`:

```go
package home

import (
    "html/template"
    "net/http"
    "net/http/httptest"
    "strings"
    "testing"

    "processmanager/internal/handlers"
    "processmanager/internal/k8s/fake"
)

func TestHomeRenders(t *testing.T) {
    deps := handlers.Deps{
        Client:    fake.NewClient(),
        Namespace: "test-ns",
        Domain:    "test.local",
        Tmpl:      mustTemplate(t),
    }
    h := New(deps)
    mux := http.NewServeMux()
    h.RegisterRoutes(mux)

    rec := httptest.NewRecorder()
    mux.ServeHTTP(rec, httptest.NewRequest("GET", "/", nil))
    if rec.Code != http.StatusOK {
        t.Fatalf("expected 200, got %d", rec.Code)
    }
    if !strings.Contains(rec.Body.String(), "Dashboard") {
        t.Errorf("missing dashboard title")
    }
}
```

- [ ] **Step 2: Run test to verify it fails**

```bash
go test ./internal/handlers/home -v
```

Expected: FAIL.

- [ ] **Step 3: Implement home handler**

Create `internal/handlers/home/home.go`:

```go
package home

import (
    "net/http"

    "processmanager/internal/handlers"
)

// Handler renders the dashboard home page.
type Handler struct {
    deps handlers.Deps
}

// New creates a Handler.
func New(deps handlers.Deps) *Handler {
    return &Handler{deps: deps}
}

// RegisterRoutes registers the home route.
func (h *Handler) RegisterRoutes(mux *http.ServeMux) {
    mux.HandleFunc("GET /", handlers.RequireAuth(h.deps.AuthMW, h.Index))
}

// Index shows summary cards.
func (h *Handler) Index(w http.ResponseWriter, r *http.Request) {
    ctx := r.Context()
    processCount := 0
    if procs, err := h.deps.Client.ListProcesses(ctx, h.deps.Namespace); err == nil {
        processCount = len(procs)
    }
    projectCount := 0
    if h.deps.AuthStore != nil {
        if projects, err := h.deps.AuthStore.ListProjects(ctx); err == nil {
            projectCount = len(projects)
        }
    }
    handlers.Render(h.deps.Tmpl, w, "pages/home/home", map[string]any{
        "Title":        "Dashboard",
        "Nav":          "home",
        "Namespace":    h.deps.Namespace,
        "Domain":       h.deps.Domain,
        "ProcessCount": processCount,
        "ProjectCount": projectCount,
    })
}
```

- [ ] **Step 4: Create home template**

`web/templates/pages/home/home.html`:

```html
{{template "components/layout" .}}

{{define "content"}}
{{template "components/page-header" dict "Title" "Dashboard"}}
<div class="row">
    <div class="card">
        <h3>{{.ProcessCount}}</h3>
        <p>Active Processes</p>
        <a href="/processes">Manage &rarr;</a>
    </div>
    <div class="card">
        <h3>{{.ProjectCount}}</h3>
        <p>Projects</p>
        <a href="/projects">Manage &rarr;</a>
    </div>
</div>
<div class="card">
    <h3>Recent Activity</h3>
    <p style="color:var(--muted);">Activity feed will appear here.</p>
</div>
{{end}}
```

- [ ] **Step 5: Run tests**

```bash
go test ./internal/handlers/home -v
```

Expected: PASS.

- [ ] **Step 6: Commit**

```bash
git add internal/handlers/home web/templates/pages/home/home.html
git commit -m "feat(home): dashboard summary page"
```

---

## Task 5: Projects Domain

**Files:**
- Create: `internal/handlers/projects/projects.go`
- Create: page templates under `web/templates/pages/projects/`

**Interfaces:**
- Consumes: `auth.Store.ListProjects`, `auth.Store.CreateProject`.
- Produces: `projects.Handler` with `RegisterRoutes(mux *http.ServeMux)`.

- [ ] **Step 1: Write the failing test**

Create `internal/handlers/projects/projects_test.go`:

```go
package projects

import (
    "html/template"
    "net/http"
    "net/http/httptest"
    "strings"
    "testing"

    "processmanager/internal/handlers"
)

func TestListRenders(t *testing.T) {
    deps := handlers.Deps{
        Namespace: "test-ns",
        Domain:    "test.local",
        Tmpl:      mustTemplate(t),
    }
    h := New(deps)
    mux := http.NewServeMux()
    h.RegisterRoutes(mux)

    rec := httptest.NewRecorder()
    mux.ServeHTTP(rec, httptest.NewRequest("GET", "/projects", nil))
    if rec.Code != http.StatusOK {
        t.Fatalf("expected 200, got %d", rec.Code)
    }
    if !strings.Contains(rec.Body.String(), "Projects") {
        t.Errorf("missing title")
    }
}
```

- [ ] **Step 2: Run test to verify it fails**

```bash
go test ./internal/handlers/projects -v
```

Expected: FAIL.

- [ ] **Step 3: Implement projects handler**

Create `internal/handlers/projects/projects.go`:

```go
package projects

import (
    "fmt"
    "net/http"
    "strings"

    "processmanager/internal/handlers"
)

// Handler handles project pages.
type Handler struct {
    deps handlers.Deps
}

// New creates a Handler.
func New(deps handlers.Deps) *Handler {
    return &Handler{deps: deps}
}

// RegisterRoutes registers project routes.
func (h *Handler) RegisterRoutes(mux *http.ServeMux) {
    mux.HandleFunc("GET /projects", h.require(h.List))
    mux.HandleFunc("GET /projects/new", h.require(h.NewForm))
    mux.HandleFunc("POST /projects", h.require(h.Create))
}

func (h *Handler) require(next http.HandlerFunc) http.HandlerFunc {
    return handlers.RequireAuth(h.deps.AuthMW, next)
}

// List shows all projects.
func (h *Handler) List(w http.ResponseWriter, r *http.Request) {
    var projects []any
    if h.deps.AuthStore != nil {
        if list, err := h.deps.AuthStore.ListProjects(r.Context()); err == nil {
            for _, p := range list {
                projects = append(projects, p)
            }
        }
    }
    handlers.Render(h.deps.Tmpl, w, "pages/projects/list", map[string]any{
        "Title":    "Projects",
        "Nav":      "projects",
        "Projects": projects,
    })
}

// NewForm shows the create form.
func (h *Handler) NewForm(w http.ResponseWriter, r *http.Request) {
    handlers.Render(h.deps.Tmpl, w, "pages/projects/form", map[string]any{
        "Title": "New Project",
        "Nav":   "projects",
    })
}

// Create handles project creation.
func (h *Handler) Create(w http.ResponseWriter, r *http.Request) {
    if err := r.ParseForm(); err != nil {
        handlers.RenderError(w, err, http.StatusBadRequest)
        return
    }
    name := strings.TrimSpace(r.FormValue("name"))
    namespace := strings.TrimSpace(r.FormValue("namespace"))
    domainHost := strings.TrimSpace(r.FormValue("domainHost"))
    projType := r.FormValue("type")
    if projType == "" {
        projType = "simple"
    }
    dbRequired := r.FormValue("dbRequired") == "on"
    authEnabled := r.FormValue("authEnabled") != "off"

    if name == "" || namespace == "" || domainHost == "" {
        handlers.RenderError(w, fmt.Errorf("name, namespace, and domain host are required"), http.StatusBadRequest)
        return
    }

    if h.deps.AuthStore == nil {
        handlers.RenderError(w, fmt.Errorf("auth store not configured"), http.StatusServiceUnavailable)
        return
    }

    p, err := h.deps.AuthStore.CreateProject(r.Context(), name, namespace, domainHost, projType, dbRequired, authEnabled)
    if err != nil {
        handlers.RenderError(w, err, http.StatusInternalServerError)
        return
    }
    handlers.RenderFragment(h.deps.Tmpl, w, "pages/projects/row", p)
}
```

- [ ] **Step 4: Create project templates**

`web/templates/pages/projects/list.html`:

```html
{{template "components/layout" .}}

{{define "content"}}
{{template "components/page-header" dict "Title" "Projects" "Action" "New Project" "Href" "/projects/new"}}
<div class="card">
    {{if .Projects}}
    <table>
        <thead>
            <tr><th>Name</th><th>Namespace</th><th>Domain Host</th><th>Type</th><th>DB</th><th>Auth</th></tr>
        </thead>
        <tbody id="project-list">
            {{range .Projects}}
                {{template "pages/projects/row" .}}
            {{end}}
        </tbody>
    </table>
    {{else}}
        {{template "components/empty-state" dict "Message" "No projects yet."}}
    {{end}}
</div>
{{end}}
```

`web/templates/pages/projects/form.html`:

```html
{{template "components/layout" .}}

{{define "content"}}
{{template "components/page-header" dict "Title" "New Project"}}
<div class="card">
    <form hx-post="/projects" hx-target="#project-list" hx-swap="beforeend">
        <label for="name">Name</label>
        <input type="text" id="name" name="name" placeholder="portfolio" required>

        <label for="namespace">Namespace</label>
        <input type="text" id="namespace" name="namespace" placeholder="nacfson-personal" required>

        <label for="domainHost">Domain Host</label>
        <input type="text" id="domainHost" name="domainHost" placeholder="portfolio.nacfson.cloud" required>

        <label for="type">Type</label>
        <select id="type" name="type">
            <option value="simple">Simple</option>
            <option value="advanced">Advanced</option>
        </select>

        <div style="margin-top:1rem;">
            <label><input type="checkbox" name="dbRequired" checked> Requires Database</label>
        </div>
        <div>
            <label><input type="checkbox" name="authEnabled" checked> Auth Enabled</label>
        </div>

        <button type="submit" class="btn btn-primary" style="margin-top:1rem;">Create Project</button>
    </form>
</div>
{{end}}
```

`web/templates/pages/projects/row.html`:

```html
<tr id="project-{{.Name}}">
    <td>{{.Name}}</td>
    <td>{{.Namespace}}</td>
    <td>{{.DomainHost}}</td>
    <td>{{.Type}}</td>
    <td>{{if .DBRequired}}Yes{{else}}No{{end}}</td>
    <td>{{if .AuthEnabled}}Yes{{else}}No{{end}}</td>
</tr>
```

- [ ] **Step 5: Run tests**

```bash
go test ./internal/handlers/projects -v
```

Expected: PASS.

- [ ] **Step 6: Commit**

```bash
git add internal/handlers/projects web/templates/pages/projects
git commit -m "feat(projects): project list and create handlers"
```

---

## Task 6: Databases Domain

**Files:**
- Modify: `internal/db/provisioner.go`
- Create: `internal/handlers/databases/databases.go`
- Create: `web/templates/pages/databases/list.html`

**Interfaces:**
- Consumes: `db.Provisioner.ListDatabases`.
- Produces: `databases.Handler` with `RegisterRoutes(mux *http.ServeMux)`.

- [ ] **Step 1: Write the failing test**

Create `internal/handlers/databases/databases_test.go`:

```go
package databases

import (
    "html/template"
    "net/http"
    "net/http/httptest"
    "strings"
    "testing"

    "processmanager/internal/handlers"
)

func TestListRenders(t *testing.T) {
    deps := handlers.Deps{
        Namespace: "test-ns",
        Domain:    "test.local",
        Tmpl:      mustTemplate(t),
    }
    h := New(deps)
    mux := http.NewServeMux()
    h.RegisterRoutes(mux)

    rec := httptest.NewRecorder()
    mux.ServeHTTP(rec, httptest.NewRequest("GET", "/databases", nil))
    if rec.Code != http.StatusOK {
        t.Fatalf("expected 200, got %d", rec.Code)
    }
    if !strings.Contains(rec.Body.String(), "Databases") {
        t.Errorf("missing title")
    }
}
```

- [ ] **Step 2: Run test to verify it fails**

```bash
go test ./internal/handlers/databases -v
```

Expected: FAIL.

- [ ] **Step 3: Add ListDatabases to provisioner**

Edit `internal/db/provisioner.go` to add:

```go
// ListDatabases returns names of databases managed by the provisioner.
func (p *Provisioner) ListDatabases(ctx context.Context) ([]string, error) {
    rows, err := p.pool.Query(ctx, "SELECT datname FROM pg_database WHERE datistemplate = false AND datname NOT IN ('postgres', 'template0', 'template1')")
    if err != nil {
        return nil, fmt.Errorf("list databases: %w", err)
    }
    defer rows.Close()

    var names []string
    for rows.Next() {
        var n string
        if err := rows.Scan(&n); err != nil {
            return nil, err
        }
        names = append(names, n)
    }
    return names, rows.Err()
}
```

- [ ] **Step 4: Implement databases handler**

Create `internal/handlers/databases/databases.go`:

```go
package databases

import (
    "net/http"

    "processmanager/internal/handlers"
)

// Handler handles database pages.
type Handler struct {
    deps handlers.Deps
}

// New creates a Handler.
func New(deps handlers.Deps) *Handler {
    return &Handler{deps: deps}
}

// RegisterRoutes registers database routes.
func (h *Handler) RegisterRoutes(mux *http.ServeMux) {
    mux.HandleFunc("GET /databases", h.require(h.List))
}

func (h *Handler) require(next http.HandlerFunc) http.HandlerFunc {
    return handlers.RequireAuth(h.deps.AuthMW, next)
}

// List shows provisioned databases.
func (h *Handler) List(w http.ResponseWriter, r *http.Request) {
    var dbs []string
    if h.deps.Provisioner != nil {
        if list, err := h.deps.Provisioner.ListDatabases(r.Context()); err == nil {
            dbs = list
        }
    }
    handlers.Render(h.deps.Tmpl, w, "pages/databases/list", map[string]any{
        "Title":     "Databases",
        "Nav":       "databases",
        "Databases": dbs,
    })
}
```

- [ ] **Step 5: Create database template**

`web/templates/pages/databases/list.html`:

```html
{{template "components/layout" .}}

{{define "content"}}
{{template "components/page-header" dict "Title" "Databases"}}
<div class="card">
    {{if .Databases}}
    <table>
        <thead><tr><th>Database Name</th></tr></thead>
        <tbody>
            {{range .Databases}}
            <tr><td>{{.}}</td></tr>
            {{end}}
        </tbody>
    </table>
    {{else}}
        {{template "components/empty-state" dict "Message" "No provisioned databases yet."}}
    {{end}}
</div>
{{end}}
```

- [ ] **Step 6: Run tests**

```bash
go test ./internal/handlers/databases -v
```

Expected: PASS.

- [ ] **Step 7: Commit**

```bash
git add internal/db/provisioner.go internal/handlers/databases web/templates/pages/databases/list.html
git commit -m "feat(databases): list provisioned databases"
```

---

## Task 7: Users Domain

**Files:**
- Modify: `internal/auth/store.go`
- Create: `internal/handlers/users/users.go`
- Create: `web/templates/pages/users/list.html`

**Interfaces:**
- Consumes: `auth.Store.ListUsers`.
- Produces: `users.Handler` with `RegisterRoutes(mux *http.ServeMux)`.

- [ ] **Step 1: Write the failing test**

Create `internal/handlers/users/users_test.go`:

```go
package users

import (
    "html/template"
    "net/http"
    "net/http/httptest"
    "strings"
    "testing"

    "processmanager/internal/handlers"
)

func TestListRenders(t *testing.T) {
    deps := handlers.Deps{
        Namespace: "test-ns",
        Domain:    "test.local",
        Tmpl:      mustTemplate(t),
    }
    h := New(deps)
    mux := http.NewServeMux()
    h.RegisterRoutes(mux)

    rec := httptest.NewRecorder()
    mux.ServeHTTP(rec, httptest.NewRequest("GET", "/users", nil))
    if rec.Code != http.StatusOK {
        t.Fatalf("expected 200, got %d", rec.Code)
    }
    if !strings.Contains(rec.Body.String(), "Users") {
        t.Errorf("missing title")
    }
}
```

- [ ] **Step 2: Run test to verify it fails**

```bash
go test ./internal/handlers/users -v
```

Expected: FAIL.

- [ ] **Step 3: Add ListUsers to auth store**

Edit `internal/auth/store.go` to add after `GetUserByEmail`:

```go
// ListUsers returns all users ordered by creation time.
func (s *Store) ListUsers(ctx context.Context) ([]User, error) {
    rows, err := s.pool.Query(ctx, `SELECT id, email, password_hash, is_admin, created_at FROM users ORDER BY created_at`)
    if err != nil {
        return nil, fmt.Errorf("list users: %w", err)
    }
    defer rows.Close()

    var users []User
    for rows.Next() {
        var u User
        if err := rows.Scan(&u.ID, &u.Email, &u.PasswordHash, &u.IsAdmin, &u.CreatedAt); err != nil {
            return nil, err
        }
        users = append(users, u)
    }
    return users, rows.Err()
}
```

- [ ] **Step 4: Implement users handler**

Create `internal/handlers/users/users.go`:

```go
package users

import (
    "net/http"

    "processmanager/internal/handlers"
)

// Handler handles user pages.
type Handler struct {
    deps handlers.Deps
}

// New creates a Handler.
func New(deps handlers.Deps) *Handler {
    return &Handler{deps: deps}
}

// RegisterRoutes registers user routes.
func (h *Handler) RegisterRoutes(mux *http.ServeMux) {
    mux.HandleFunc("GET /users", h.require(h.List))
}

func (h *Handler) require(next http.HandlerFunc) http.HandlerFunc {
    return handlers.RequireAuth(h.deps.AuthMW, next)
}

// List shows all users.
func (h *Handler) List(w http.ResponseWriter, r *http.Request) {
    var users []any
    if h.deps.AuthStore != nil {
        if list, err := h.deps.AuthStore.ListUsers(r.Context()); err == nil {
            for _, u := range list {
                users = append(users, u)
            }
        }
    }
    handlers.Render(h.deps.Tmpl, w, "pages/users/list", map[string]any{
        "Title": "Users",
        "Nav":   "users",
        "Users": users,
    })
}
```

- [ ] **Step 5: Create users template**

`web/templates/pages/users/list.html`:

```html
{{template "components/layout" .}}

{{define "content"}}
{{template "components/page-header" dict "Title" "Users"}}
<div class="card">
    {{if .Users}}
    <table>
        <thead><tr><th>Email</th><th>Admin</th><th>Created</th></tr></thead>
        <tbody>
            {{range .Users}}
            <tr><td>{{.Email}}</td><td>{{if .IsAdmin}}Yes{{else}}No{{end}}</td><td>{{.CreatedAt}}</td></tr>
            {{end}}
        </tbody>
    </table>
    {{else}}
        {{template "components/empty-state" dict "Message" "No users yet."}}
    {{end}}
</div>
{{end}}
```

- [ ] **Step 6: Run tests**

```bash
go test ./internal/handlers/users -v
```

Expected: PASS.

- [ ] **Step 7: Commit**

```bash
git add internal/auth/store.go internal/handlers/users web/templates/pages/users/list.html
git commit -m "feat(users): list users"
```

---

## Task 8: Settings Domain

**Files:**
- Create: `internal/handlers/settings/settings.go`
- Create: `web/templates/pages/settings/settings.html`

**Interfaces:**
- Consumes: `handlers.Deps`.
- Produces: `settings.Handler` with `RegisterRoutes(mux *http.ServeMux)`.

- [ ] **Step 1: Write the failing test**

Create `internal/handlers/settings/settings_test.go`:

```go
package settings

import (
    "html/template"
    "net/http"
    "net/http/httptest"
    "strings"
    "testing"

    "processmanager/internal/handlers"
)

func TestSettingsRenders(t *testing.T) {
    deps := handlers.Deps{
        Namespace: "test-ns",
        Domain:    "test.local",
        Tmpl:      mustTemplate(t),
    }
    h := New(deps)
    mux := http.NewServeMux()
    h.RegisterRoutes(mux)

    rec := httptest.NewRecorder()
    mux.ServeHTTP(rec, httptest.NewRequest("GET", "/settings", nil))
    if rec.Code != http.StatusOK {
        t.Fatalf("expected 200, got %d", rec.Code)
    }
    if !strings.Contains(rec.Body.String(), "Settings") {
        t.Errorf("missing title")
    }
}
```

- [ ] **Step 2: Run test to verify it fails**

```bash
go test ./internal/handlers/settings -v
```

Expected: FAIL.

- [ ] **Step 3: Implement settings handler**

Create `internal/handlers/settings/settings.go`:

```go
package settings

import (
    "net/http"

    "processmanager/internal/handlers"
)

// Handler handles settings pages.
type Handler struct {
    deps handlers.Deps
}

// New creates a Handler.
func New(deps handlers.Deps) *Handler {
    return &Handler{deps: deps}
}

// RegisterRoutes registers settings routes.
func (h *Handler) RegisterRoutes(mux *http.ServeMux) {
    mux.HandleFunc("GET /settings", h.require(h.Index))
}

func (h *Handler) require(next http.HandlerFunc) http.HandlerFunc {
    return handlers.RequireAuth(h.deps.AuthMW, next)
}

// Index shows current platform settings.
func (h *Handler) Index(w http.ResponseWriter, r *http.Request) {
    handlers.Render(h.deps.Tmpl, w, "pages/settings/settings", map[string]any{
        "Title":     "Settings",
        "Nav":       "settings",
        "Namespace": h.deps.Namespace,
        "Domain":    h.deps.Domain,
    })
}
```

- [ ] **Step 4: Create settings template**

`web/templates/pages/settings/settings.html`:

```html
{{template "components/layout" .}}

{{define "content"}}
{{template "components/page-header" dict "Title" "Settings"}}
<div class="card">
    <table>
        <tr><td>Managed Namespace</td><td>{{.Namespace}}</td></tr>
        <tr><td>Base Domain</td><td>{{.Domain}}</td></tr>
    </table>
</div>
{{end}}
```

- [ ] **Step 5: Run tests**

```bash
go test ./internal/handlers/settings -v
```

Expected: PASS.

- [ ] **Step 6: Commit**

```bash
git add internal/handlers/settings web/templates/pages/settings/settings.html
git commit -m "feat(settings): read-only settings page"
```

---

## Task 9: Audit/Activity Domain

**Files:**
- Modify: `internal/auth/store.go`
- Create: `internal/handlers/audit/audit.go`
- Create: `web/templates/pages/activity/list.html`

**Interfaces:**
- Consumes: `auth.Store.ListAuditEvents`.
- Produces: `audit.Handler` with `RegisterRoutes(mux *http.ServeMux)`.

- [ ] **Step 1: Write the failing test**

Create `internal/handlers/audit/audit_test.go`:

```go
package audit

import (
    "html/template"
    "net/http"
    "net/http/httptest"
    "strings"
    "testing"

    "processmanager/internal/handlers"
)

func TestListRenders(t *testing.T) {
    deps := handlers.Deps{
        Namespace: "test-ns",
        Domain:    "test.local",
        Tmpl:      mustTemplate(t),
    }
    h := New(deps)
    mux := http.NewServeMux()
    h.RegisterRoutes(mux)

    rec := httptest.NewRecorder()
    mux.ServeHTTP(rec, httptest.NewRequest("GET", "/activity", nil))
    if rec.Code != http.StatusOK {
        t.Fatalf("expected 200, got %d", rec.Code)
    }
    if !strings.Contains(rec.Body.String(), "Activity") {
        t.Errorf("missing title")
    }
}
```

- [ ] **Step 2: Run test to verify it fails**

```bash
go test ./internal/handlers/audit -v
```

Expected: FAIL.

- [ ] **Step 3: Add audit table and methods to auth store**

Add the following SQL inside the `schema` constant in `internal/auth/store.go` (before the closing backtick):

```sql
CREATE TABLE IF NOT EXISTS audit_events (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    actor_email TEXT,
    action TEXT NOT NULL,
    resource_type TEXT,
    resource_id TEXT,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX IF NOT EXISTS idx_audit_events_created ON audit_events(created_at DESC);
```

Add types and methods:

```go
// AuditEvent represents a recorded action.
type AuditEvent struct {
    ID           string
    ActorEmail   string
    Action       string
    ResourceType string
    ResourceID   string
    CreatedAt    time.Time
}

// LogAuditEvent records an action.
func (s *Store) LogAuditEvent(ctx context.Context, actorEmail, action, resourceType, resourceID string) error {
    _, err := s.pool.Exec(ctx,
        `INSERT INTO audit_events (actor_email, action, resource_type, resource_id) VALUES ($1, $2, $3, $4)`,
        actorEmail, action, resourceType, resourceID)
    return err
}

// ListAuditEvents returns recent audit events.
func (s *Store) ListAuditEvents(ctx context.Context, limit int) ([]AuditEvent, error) {
    if limit <= 0 {
        limit = 50
    }
    rows, err := s.pool.Query(ctx,
        `SELECT id, actor_email, action, resource_type, resource_id, created_at FROM audit_events ORDER BY created_at DESC LIMIT $1`,
        limit)
    if err != nil {
        return nil, fmt.Errorf("list audit events: %w", err)
    }
    defer rows.Close()

    var events []AuditEvent
    for rows.Next() {
        var e AuditEvent
        if err := rows.Scan(&e.ID, &e.ActorEmail, &e.Action, &e.ResourceType, &e.ResourceID, &e.CreatedAt); err != nil {
            return nil, err
        }
        events = append(events, e)
    }
    return events, rows.Err()
}
```

- [ ] **Step 4: Implement audit handler**

Create `internal/handlers/audit/audit.go`:

```go
package audit

import (
    "net/http"

    "processmanager/internal/handlers"
)

// Handler handles activity pages.
type Handler struct {
    deps handlers.Deps
}

// New creates a Handler.
func New(deps handlers.Deps) *Handler {
    return &Handler{deps: deps}
}

// RegisterRoutes registers activity routes.
func (h *Handler) RegisterRoutes(mux *http.ServeMux) {
    mux.HandleFunc("GET /activity", h.require(h.List))
}

func (h *Handler) require(next http.HandlerFunc) http.HandlerFunc {
    return handlers.RequireAuth(h.deps.AuthMW, next)
}

// List shows recent activity.
func (h *Handler) List(w http.ResponseWriter, r *http.Request) {
    var events []any
    if h.deps.AuthStore != nil {
        if list, err := h.deps.AuthStore.ListAuditEvents(r.Context(), 50); err == nil {
            for _, e := range list {
                events = append(events, e)
            }
        }
    }
    handlers.Render(h.deps.Tmpl, w, "pages/activity/list", map[string]any{
        "Title":  "Activity",
        "Nav":    "activity",
        "Events": events,
    })
}
```

- [ ] **Step 5: Create activity template**

`web/templates/pages/activity/list.html`:

```html
{{template "components/layout" .}}

{{define "content"}}
{{template "components/page-header" dict "Title" "Activity"}}
<div class="card">
    {{if .Events}}
    <table>
        <thead><tr><th>Time</th><th>Actor</th><th>Action</th><th>Resource</th></tr></thead>
        <tbody>
            {{range .Events}}
            <tr><td>{{.CreatedAt}}</td><td>{{.ActorEmail}}</td><td>{{.Action}}</td><td>{{.ResourceType}} {{.ResourceID}}</td></tr>
            {{end}}
        </tbody>
    </table>
    {{else}}
        {{template "components/empty-state" dict "Message" "No activity yet."}}
    {{end}}
</div>
{{end}}
```

- [ ] **Step 6: Run tests**

```bash
go test ./internal/handlers/audit -v
```

Expected: PASS.

- [ ] **Step 7: Commit**

```bash
git add internal/auth/store.go internal/handlers/audit web/templates/pages/activity/list.html
git commit -m "feat(audit): activity feed page"
```

---

## Task 10: Wire All Routes in main.go

**Files:**
- Modify: `cmd/manager/main.go`

**Interfaces:**
- Consumes: all domain handlers.
- Produces: running HTTP server with new routes.

- [ ] **Step 1: Update main.go**

Replace `cmd/manager/main.go` with:

```go
package main

import (
    "context"
    "flag"
    "log"
    "net/http"
    "os"

    "processmanager/internal/auth"
    "processmanager/internal/db"
    "processmanager/internal/handlers"
    "processmanager/internal/handlers/audit"
    "processmanager/internal/handlers/databases"
    "processmanager/internal/handlers/home"
    "processmanager/internal/handlers/processes"
    "processmanager/internal/handlers/projects"
    "processmanager/internal/handlers/settings"
    "processmanager/internal/handlers/users"
    "processmanager/internal/k8s"
)

func main() {
    addr := flag.String("addr", ":8080", "listen address")
    namespace := flag.String("namespace", "nacfson-personal", "namespace to manage")
    domain := flag.String("domain", "nacfson.cloud", "base domain for processes")
    authDSN := flag.String("auth-dsn", os.Getenv("AUTH_DATABASE_URL"), "auth database DSN")
    sharedDSN := flag.String("shared-dsn", os.Getenv("SHARED_DATABASE_URL"), "shared app database DSN")
    authHost := flag.String("auth-host", "auth.nacfson.cloud", "auth service host")
    flag.Parse()

    client, err := k8s.NewClient()
    if err != nil {
        log.Fatalf("failed to create k8s client: %v", err)
    }

    tmpl, err := handlers.NewTemplate()
    if err != nil {
        log.Fatalf("failed to load templates: %v", err)
    }

    deps := handlers.Deps{
        Client:    client,
        Namespace: *namespace,
        Domain:    *domain,
        Tmpl:      tmpl,
    }

    if *authDSN != "" {
        ctx := context.Background()
        pool, err := db.NewPool(ctx, *authDSN)
        if err != nil {
            log.Fatalf("failed to connect to auth db: %v", err)
        }
        defer pool.Close()
        store := auth.NewStore(pool)
        if err := store.Migrate(ctx); err != nil {
            log.Fatalf("failed to migrate auth db: %v", err)
        }
        deps.AuthStore = store
        deps.AuthMW = auth.NewMiddleware(store, *authHost, *domain)
    } else {
        log.Println("warning: AUTH_DATABASE_URL not set; manager dashboard is unprotected")
    }

    if *sharedDSN != "" {
        ctx := context.Background()
        pool, err := db.NewPool(ctx, *sharedDSN)
        if err != nil {
            log.Fatalf("failed to connect to shared db: %v", err)
        }
        defer pool.Close()
        deps.Provisioner = db.NewProvisioner(pool)
    } else {
        log.Println("warning: SHARED_DATABASE_URL not set; database listing disabled")
    }

    mux := http.NewServeMux()
    home.New(deps).RegisterRoutes(mux)
    processes.New(deps).RegisterRoutes(mux)
    projects.New(deps).RegisterRoutes(mux)
    databases.New(deps).RegisterRoutes(mux)
    users.New(deps).RegisterRoutes(mux)
    audit.New(deps).RegisterRoutes(mux)
    settings.New(deps).RegisterRoutes(mux)
    mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, r *http.Request) {
        w.WriteHeader(http.StatusOK)
        w.Write([]byte("ok"))
    })

    port := os.Getenv("PORT")
    if port != "" {
        *addr = ":" + port
    }

    log.Printf("process manager listening on %s", *addr)
    if err := http.ListenAndServe(*addr, mux); err != nil {
        log.Fatalf("server error: %v", err)
    }
}
```

- [ ] **Step 2: Build the binary**

```bash
go build ./cmd/manager
```

Expected: PASS.

- [ ] **Step 3: Run all tests**

```bash
go test ./...
```

Expected: PASS (existing tests plus new ones).

- [ ] **Step 4: Commit**

```bash
git add cmd/manager/main.go
git commit -m "feat(manager): wire all domain routes"
```

---

## Task 11: Integration Smoke Test

**Files:**
- None (manual verification).

- [ ] **Step 1: Run the manager locally**

```bash
go run ./cmd/manager -namespace nacfson-personal -domain nacfson.cloud -fake-k8s
# or: USE_FAKE_K8S=true go run ./cmd/manager -namespace nacfson-personal -domain nacfson.cloud
```

- [ ] **Step 2: Verify pages load**

In another terminal:

```bash
curl -s http://localhost:8080/ | head
curl -s http://localhost:8080/processes | head
curl -s http://localhost:8080/projects | head
curl -s http://localhost:8080/databases | head
curl -s http://localhost:8080/users | head
curl -s http://localhost:8080/activity | head
curl -s http://localhost:8080/settings | head
```

Expected: each returns HTML containing its page title.

- [ ] **Step 3: Commit (if any fixes needed)**

If fixes are required, commit them. If no changes, no commit.

---

## Task 12: Update k8s Deployment Manifest

**Files:**
- Modify: `k8s/manager/deployment.yaml`

- [ ] **Step 1: Add SHARED_DATABASE_URL env**

Edit `k8s/manager/deployment.yaml` to add the shared DB env from the existing secret:

```yaml
- name: SHARED_DATABASE_URL
  valueFrom:
    secretKeyRef:
      name: shared-db-secrets
      key: DATABASE_URL
```

(Assume `k8s/data/secret.yaml` creates `shared-db-secrets` in `nacfson-data`; if not, create or reference the correct secret.)

- [ ] **Step 2: Commit**

```bash
git add k8s/manager/deployment.yaml
git commit -m "chore(k8s): pass shared db url to manager"
```

---

## Self-Review

**Spec coverage:**
- Reusable component library: Task 2.
- Domain-split handlers: Tasks 3–9.
- Live status polling: Task 3 (`/processes/{name}/status`).
- Concise errors in UI: `handlers.RenderError` in Task 1.
- Logs per application: Task 3 (`/processes/{name}/logs`).
- Navigation sections: layout in Task 2, handlers in Tasks 3–9.
- Handler tests: each domain task includes tests.

**Placeholder scan:** No TBD/TODO/fill-in placeholders.

**Type consistency:**
- `handlers.Deps` fields used consistently across all handlers.
- `k8s.ClientInterface` implemented by real client and fake.
- Template names match file basenames.

**Gaps:**
- Audit events are listed but not logged yet. Future task: call `AuthStore.LogAuditEvent` from create/delete handlers.
- Settings page is read-only. Future task: persist settings in DB or configmap.
- User creation/management is read-only. Future task: add create user form.
- Field-level validation errors use plain text `RenderError` rather than per-field error slots.
- Toast notifications are implemented as a component but not triggered from handlers yet.

These gaps are intentional for this iteration per YAGNI.

---

## Execution Handoff

**Plan complete and saved to `docs/superpowers/plans/2026-06-27-dashboard-ui.md`.**

Two execution options:

1. **Subagent-Driven (recommended)** — I dispatch a fresh subagent per task, review between tasks, fast iteration.
2. **Inline Execution** — Execute tasks in this session using executing-plans, batch execution with checkpoints.

Which approach would you like?
