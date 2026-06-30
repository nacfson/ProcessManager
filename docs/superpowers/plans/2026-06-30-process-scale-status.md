# Process Scale & Status Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Add inline replica scaling and compact pod/service/ingress status badges to the process list in the dashboard.

**Architecture:** Extend the existing `processes` domain handler and Kubernetes client with a `ScaleProcess` endpoint and a richer `GetProcessStatus` lookup. Keep the Go + HTMX stack and reuse the existing component library; the fake Kubernetes client grows enough state to support handler tests.

**Tech Stack:** Go 1.26, `html/template`, HTMX 1.9.x, Kubernetes client-go, pgx/v5.

## Global Constraints

- Keep the existing Go + HTMX stack; do not add React/Vue/Svelte.
- All dashboard pages require authentication when `AUTH_DATABASE_URL` is set.
- Error UI shows only *which* error occurred; detailed debugging stays in SSH/kubectl.
- Live status uses HTMX polling (`hx-trigger="every 3s"`), not WebSocket/SSE.
- Use exact file paths from this plan.
- Each task ends with a passing test or a runnable check.
- Scaling is limited to `0 <= replicas <= 10`.

---

## File Structure

New and modified files:

```
internal/models/process.go              # add ProcessStatus model
internal/k8s/client.go                  # extend ClientInterface, add ScaleProcess/GetProcessStatus
internal/k8s/fake/fake.go               # fake implementations of new methods
internal/k8s/fake/fake_test.go          # tests for fake client
internal/handlers/handlers.go           # add template helpers (sub, statusClass, svcClass, tlsClass, tlsSymbol)
internal/handlers/processes/processes.go # add Scale handler, update Status handler
internal/handlers/processes/processes_test.go # tests for Scale and Status
web/templates/pages/processes/list.html # update table header
web/templates/pages/processes/row.html  # add scale stepper + status placeholder
web/templates/pages/processes/status.html # render compact badges
```

---

## Task 1: Add ProcessStatus Model

**Files:**
- Modify: `internal/models/process.go`
- Test: `internal/models/process_test.go` (create)

**Interfaces:**
- Consumes: nothing new.
- Produces: `models.ProcessStatus` struct.

- [ ] **Step 1: Write the failing test**

Create `internal/models/process_test.go`:

```go
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
```

- [ ] **Step 2: Run test to verify it fails**

```bash
go test ./internal/models -run TestProcessStatusFields -v
```

Expected: FAIL — `ProcessStatus` undefined.

- [ ] **Step 3: Add ProcessStatus model**

Append to `internal/models/process.go`:

```go
// ProcessStatus describes the runtime and routing health of a process.
type ProcessStatus struct {
    Name             string
    Ready            int32
    Total            int32
    ServiceEndpoints int32
    IngressReady     bool
    TLSReady         bool
    Host             string
}
```

- [ ] **Step 4: Run test to verify it passes**

```bash
go test ./internal/models -v
```

Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/models/process.go internal/models/process_test.go
git commit -m "feat(models): add ProcessStatus struct"
```

---

## Task 2: Extend Kubernetes Client Interface and Real Client

**Files:**
- Modify: `internal/k8s/client.go`
- Test: `internal/k8s/client_test.go` (create if missing, or use existing)

**Interfaces:**
- Consumes: `models.ProcessStatus`, `models.ErrProcessNotFound`.
- Produces: `ClientInterface` with `ScaleProcess` and `GetProcessStatus`; real `Client.ScaleProcess`, `Client.GetProcessStatus`.

- [ ] **Step 1: Write the failing test**

Create `internal/k8s/client_test.go`:

```go
package k8s

import (
    "context"
    "testing"
)

func TestClientInterfaceHasScaleAndStatus(t *testing.T) {
    var _ ClientInterface = (*Client)(nil)
}

func TestScaleProcessRequiresManagedLabel(t *testing.T) {
    c := &Client{}
    err := c.ScaleProcess(context.Background(), "default", "unmanaged", 2)
    if err == nil {
        t.Fatal("expected error for nil clientset")
    }
}
```

- [ ] **Step 2: Run test to verify it fails**

```bash
go test ./internal/k8s -run TestClientInterfaceHasScaleAndStatus -v
```

Expected: FAIL — `ScaleProcess`/`GetProcessStatus` not on interface.

- [ ] **Step 3: Extend ClientInterface and add methods**

Edit `internal/k8s/client.go`.

Update `ClientInterface` to:

```go
// ClientInterface is the surface used by HTTP handlers.
type ClientInterface interface {
    ListProcesses(ctx context.Context, namespace string) ([]models.Process, error)
    GetProcess(ctx context.Context, namespace, name string) (*models.Process, error)
    CreateProcess(ctx context.Context, p models.Process, domain string) error
    DeleteProcess(ctx context.Context, namespace, name string) error
    GetPodLogs(ctx context.Context, namespace, name string) (string, error)
    ScaleProcess(ctx context.Context, namespace, name string, replicas int32) error
    GetProcessStatus(ctx context.Context, namespace, name string) (*models.ProcessStatus, error)
}
```

Add `ScaleProcess` after `DeleteProcess`:

```go
// ScaleProcess updates the replica count of a managed Deployment.
func (c *Client) ScaleProcess(ctx context.Context, namespace, name string, replicas int32) error {
    d, err := c.clientset.AppsV1().Deployments(namespace).Get(ctx, name, metav1.GetOptions{})
    if err != nil {
        return fmt.Errorf("get deployment: %w", err)
    }
    if d.Labels[ManagedByLabel] != ManagedByValue {
        return models.ErrProcessNotFound
    }
    d.Spec.Replicas = &replicas
    if _, err := c.clientset.AppsV1().Deployments(namespace).Update(ctx, d, metav1.UpdateOptions{}); err != nil {
        return fmt.Errorf("update deployment: %w", err)
    }
    return nil
}
```

Add `GetProcessStatus` after `ScaleProcess`:

```go
// GetProcessStatus returns runtime and routing health for a process.
func (c *Client) GetProcessStatus(ctx context.Context, namespace, name string) (*models.ProcessStatus, error) {
    d, err := c.clientset.AppsV1().Deployments(namespace).Get(ctx, name, metav1.GetOptions{})
    if err != nil {
        return nil, fmt.Errorf("get deployment: %w", err)
    }
    if d.Labels[ManagedByLabel] != ManagedByValue {
        return nil, models.ErrProcessNotFound
    }

    status := &models.ProcessStatus{
        Name:   d.Name,
        Ready:  d.Status.ReadyReplicas,
        Total:  d.Status.Replicas,
    }
    if d.Spec.Replicas != nil {
        status.Total = *d.Spec.Replicas
    }

    endpoints, err := c.clientset.CoreV1().Endpoints(namespace).Get(ctx, name, metav1.GetOptions{})
    if err == nil {
        for _, subset := range endpoints.Subsets {
            for _, addr := range subset.Addresses {
                if addr.TargetRef != nil {
                    status.ServiceEndpoints++
                }
            }
        }
        status.IngressReady = true
    }

    ingress, err := c.clientset.NetworkingV1().Ingresses(namespace).Get(ctx, name, metav1.GetOptions{})
    if err == nil {
        status.IngressReady = true
        if len(ingress.Spec.Rules) > 0 {
            status.Host = ingress.Spec.Rules[0].Host
        }
        if len(ingress.Spec.TLS) > 0 {
            secretName := ingress.Spec.TLS[0].SecretName
            if secretName != "" {
                _, err := c.clientset.CoreV1().Secrets(namespace).Get(ctx, secretName, metav1.GetOptions{})
                status.TLSReady = err == nil
            }
        }
    }

    return status, nil
}
```

- [ ] **Step 4: Run tests**

```bash
go test ./internal/k8s -v
```

Expected: PASS (interface compile test only; integration with a real cluster is not required).

- [ ] **Step 5: Commit**

```bash
git add internal/k8s/client.go internal/k8s/client_test.go
git commit -m "feat(k8s): add ScaleProcess and GetProcessStatus"
```

---

## Task 3: Extend Fake Kubernetes Client

**Files:**
- Modify: `internal/k8s/fake/fake.go`
- Test: `internal/k8s/fake/fake_test.go`

**Interfaces:**
- Consumes: `models.Process`, `models.ProcessStatus`, `models.ErrProcessNotFound`.
- Produces: fake `ScaleProcess`, `GetProcessStatus`.

- [ ] **Step 1: Write the failing test**

Create `internal/k8s/fake/fake_test.go`:

```go
package fake

import (
    "context"
    "testing"

    "processmanager/internal/models"
)

func TestScaleAndStatus(t *testing.T) {
    c := NewClient()
    ctx := context.Background()
    if err := c.CreateProcess(ctx, models.Process{Name: "pn", Namespace: "default", Image: "x", Port: 8080, Replicas: 1}, "test.local"); err != nil {
        t.Fatalf("create: %v", err)
    }

    if err := c.ScaleProcess(ctx, "default", "pn", 3); err != nil {
        t.Fatalf("scale: %v", err)
    }

    status, err := c.GetProcessStatus(ctx, "default", "pn")
    if err != nil {
        t.Fatalf("status: %v", err)
    }
    if status.Total != 3 {
        t.Errorf("expected total 3, got %d", status.Total)
    }
}
```

- [ ] **Step 2: Run test to verify it fails**

```bash
go test ./internal/k8s/fake -run TestScaleAndStatus -v
```

Expected: FAIL — `ScaleProcess`/`GetProcessStatus` missing on fake.

- [ ] **Step 3: Implement fake methods**

Replace `internal/k8s/fake/fake.go` with:

```go
package fake

import (
    "context"
    "fmt"

    "processmanager/internal/k8s"
    "processmanager/internal/models"
)

// fakeProcess captures the state needed for scale/status tests.
type fakeProcess struct {
    process      models.Process
    replicas     int32
    ready        int32
    endpoints    int32
    ingressReady bool
    tlsReady     bool
    host         string
}

// Client implements k8s.ClientInterface in memory.
type Client struct {
    processes map[string]fakeProcess
}

// NewClient creates an empty fake client.
func NewClient() k8s.ClientInterface {
    return &Client{processes: make(map[string]fakeProcess)}
}

func key(namespace, name string) string {
    return namespace + "/" + name
}

func (c *Client) ListProcesses(ctx context.Context, namespace string) ([]models.Process, error) {
    var out []models.Process
    for k, fp := range c.processes {
        if fp.process.Namespace == namespace {
            p := fp.process
            p.Replicas = fp.replicas
            p.Ready = fp.ready
            p.Total = fp.replicas
            out = append(out, p)
        }
    }
    return out, nil
}

func (c *Client) GetProcess(ctx context.Context, namespace, name string) (*models.Process, error) {
    fp, ok := c.processes[key(namespace, name)]
    if !ok {
        return nil, models.ErrProcessNotFound
    }
    p := fp.process
    p.Replicas = fp.replicas
    p.Ready = fp.ready
    p.Total = fp.replicas
    return &p, nil
}

func (c *Client) CreateProcess(ctx context.Context, p models.Process, domain string) error {
    if p.Namespace == "" {
        p.Namespace = "default"
    }
    if p.Replicas <= 0 {
        p.Replicas = 1
    }
    fp := fakeProcess{
        process:      p,
        replicas:     p.Replicas,
        ready:        p.Replicas,
        endpoints:    p.Replicas,
        ingressReady: true,
        tlsReady:     true,
        host:         fmt.Sprintf("%s.%s", p.Name, domain),
    }
    c.processes[key(p.Namespace, p.Name)] = fp
    return nil
}

func (c *Client) DeleteProcess(ctx context.Context, namespace, name string) error {
    k := key(namespace, name)
    if _, ok := c.processes[k]; !ok {
        return models.ErrProcessNotFound
    }
    delete(c.processes, k)
    return nil
}

func (c *Client) GetPodLogs(ctx context.Context, namespace, name string) (string, error) {
    return fmt.Sprintf("fake logs for %s/%s", namespace, name), nil
}

// ScaleProcess updates the replica count of a fake process.
func (c *Client) ScaleProcess(ctx context.Context, namespace, name string, replicas int32) error {
    k := key(namespace, name)
    fp, ok := c.processes[k]
    if !ok {
        return models.ErrProcessNotFound
    }
    fp.replicas = replicas
    fp.ready = replicas
    fp.endpoints = replicas
    c.processes[k] = fp
    return nil
}

// GetProcessStatus returns the fake runtime and routing health.
func (c *Client) GetProcessStatus(ctx context.Context, namespace, name string) (*models.ProcessStatus, error) {
    fp, ok := c.processes[key(namespace, name)]
    if !ok {
        return nil, models.ErrProcessNotFound
    }
    return &models.ProcessStatus{
        Name:             fp.process.Name,
        Ready:            fp.ready,
        Total:            fp.replicas,
        ServiceEndpoints: fp.endpoints,
        IngressReady:     fp.ingressReady,
        TLSReady:         fp.tlsReady,
        Host:             fp.host,
    }, nil
}
```

- [ ] **Step 4: Run tests**

```bash
go test ./internal/k8s/fake -v
```

Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/k8s/fake/fake.go internal/k8s/fake/fake_test.go
git commit -m "feat(k8s/fake): fake ScaleProcess and GetProcessStatus"
```

---

## Task 4: Add Template Helpers

**Files:**
- Modify: `internal/handlers/handlers.go`
- Test: `internal/handlers/handlers_test.go`

**Interfaces:**
- Consumes: nothing.
- Produces: template functions `sub`, `statusClass`, `svcClass`, `tlsClass`, `tlsSymbol`.

- [ ] **Step 1: Write the failing test**

Create `internal/handlers/helpers_test.go`:

```go
package handlers

import (
    "strings"
    "testing"
)

func TestStatusClassHelper(t *testing.T) {
    tmpl, err := NewTemplate()
    if err != nil {
        t.Fatalf("NewTemplate failed: %v", err)
    }
    testTmpl, err := tmpl.New("helper-test").Parse(`{{statusClass .Ready .Total}}`)
    if err != nil {
        t.Fatalf("parse: %v", err)
    }
    var sb strings.Builder
    if err := testTmpl.Execute(&sb, map[string]any{"Ready": int32(2), "Total": int32(2)}); err != nil {
        t.Fatalf("execute: %v", err)
    }
    if !strings.Contains(sb.String(), "ready") {
        t.Errorf("expected ready, got %q", sb.String())
    }
}
```

- [ ] **Step 2: Run test to verify it fails**

```bash
go test ./internal/handlers -run TestStatusClassHelper -v
```

Expected: FAIL — `statusClass` undefined.

- [ ] **Step 3: Add helper functions and audit method to AuthStore**

Edit `internal/handlers/handlers.go`.

Add `LogAuditEvent` to the `AuthStore` interface:

```go
// AuthStore abstracts auth/shared persistence for handler testing.
type AuthStore interface {
    CreateProject(ctx context.Context, name, namespace, domainHost, projType string, dbRequired, authEnabled bool) (*auth.Project, error)
    ListProjects(ctx context.Context) ([]auth.Project, error)
    ListUsers(ctx context.Context) ([]auth.User, error)
    ListAuditEvents(ctx context.Context, limit int) ([]auth.AuditEvent, error)
    LogAuditEvent(ctx context.Context, actorEmail, action, resourceType, resourceID string) error
}
```

Update the `FuncMap` in `NewTemplate` to include:

```go
"add": func(a, b int32) int32 { return a + b },
"sub": func(a, b int32) int32 { return a - b },
"statusClass": func(ready, total int32) string {
    if total == 0 {
        return "error"
    }
    if ready == total {
        return "ready"
    }
    if ready == 0 {
        return "error"
    }
    return "pending"
},
"svcClass": func(endpoints int32) string {
    if endpoints > 0 {
        return "ready"
    }
    return "error"
},
"tlsClass": func(tlsReady bool) string {
    if tlsReady {
        return "ready"
    }
    return "error"
},
"tlsSymbol": func(tlsReady bool) string {
    if tlsReady {
        return "✓"
    }
    return "…"
},
```

- [ ] **Step 4: Run tests**

```bash
go test ./internal/handlers -v
```

Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/handlers/handlers.go internal/handlers/helpers_test.go
git commit -m "feat(handlers): status badge template helpers"
```

---

## Task 5: Add Scale Handler and Update Status Handler

**Files:**
- Modify: `internal/handlers/processes/processes.go`
- Test: `internal/handlers/processes/processes_test.go`

**Interfaces:**
- Consumes: `handlers.Deps`, `ClientInterface.ScaleProcess`, `ClientInterface.GetProcessStatus`, `models.ProcessStatus`, `auth.Store.LogAuditEvent`.
- Produces: `Handler.Scale`, `Handler.Status` returning compact badge fragments.

- [ ] **Step 1: Write the failing test**

Append to `internal/handlers/processes/processes_test.go`:

```go
func TestScaleProcess(t *testing.T) {
    c := fake.NewClient()
    ctx := context.Background()
    c.CreateProcess(ctx, models.Process{Name: "pn", Namespace: "test-ns", Image: "x", Port: 8080, Replicas: 1}, "test.local")

    deps := handlers.Deps{
        Client:    c,
        Namespace: "test-ns",
        Domain:    "test.local",
        Tmpl:      mustTemplate(t),
    }
    h := New(deps)
    mux := http.NewServeMux()
    h.RegisterRoutes(mux)

    rec := httptest.NewRecorder()
    req := httptest.NewRequest("POST", "/processes/pn/scale?replicas=3", nil)
    mux.ServeHTTP(rec, req)

    if rec.Code != http.StatusOK {
        t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
    }
    body := rec.Body.String()
    if !strings.Contains(body, ">3<") {
        t.Errorf("expected replica count 3 in stepper, got %s", body)
    }
}
```

- [ ] **Step 2: Run test to verify it fails**

```bash
go test ./internal/handlers/processes -run TestScaleProcess -v
```

Expected: FAIL — route or method missing.

- [ ] **Step 3: Implement Scale and update Status handlers**

Edit `internal/handlers/processes/processes.go`.

Add route registration inside `RegisterRoutes`:

```go
mux.HandleFunc("POST /processes/{name}/scale", h.require(h.Scale))
```

Add imports if needed (`log/slog`, `strconv`, `processmanager/internal/auth`).

Add the `Scale` handler:

```go
// Scale changes the replica count of a process.
func (h *Handler) Scale(w http.ResponseWriter, r *http.Request) {
    name := r.PathValue("name")
    replicasStr := r.URL.Query().Get("replicas")
    replicas, err := strconv.Atoi(replicasStr)
    if err != nil || replicas < 0 || replicas > 10 {
        handlers.RenderError(w, fmt.Errorf("replicas must be an integer between 0 and 10"), http.StatusBadRequest)
        return
    }

    ctx := r.Context()
    if err := h.deps.Client.ScaleProcess(ctx, h.deps.Namespace, name, int32(replicas)); err != nil {
        slog.Error("scale process failed", "error", err, "name", name)
        handlers.RenderError(w, fmt.Errorf("failed to scale process"), http.StatusInternalServerError)
        return
    }

    if h.deps.AuthStore != nil {
        email := "unknown"
        if sess := auth.UserContext(ctx); sess != nil && sess.Email != "" {
            email = sess.Email
        }
        if err := h.deps.AuthStore.LogAuditEvent(ctx, email, "scale", "process", name); err != nil {
            slog.Error("audit log failed", "error", err)
        }
    }

    p, err := h.deps.Client.GetProcess(ctx, h.deps.Namespace, name)
    if err != nil {
        slog.Error("get process after scale failed", "error", err)
        handlers.RenderError(w, fmt.Errorf("scaled but failed to refresh"), http.StatusInternalServerError)
        return
    }
    p.PublicURL = fmt.Sprintf("https://%s.%s", p.Name, h.deps.Domain)
    handlers.RenderFragment(h.deps.Tmpl, w, "pages/processes/row", p)
}
```

Replace the existing `Status` handler with:

```go
// Status returns compact status badge fragments for polling.
func (h *Handler) Status(w http.ResponseWriter, r *http.Request) {
    name := r.PathValue("name")
    status, err := h.deps.Client.GetProcessStatus(r.Context(), h.deps.Namespace, name)
    if err != nil {
        handlers.RenderFragment(h.deps.Tmpl, w, "pages/processes/status", &models.ProcessStatus{
            Ready:            0,
            Total:            0,
            ServiceEndpoints: 0,
            IngressReady:     false,
            TLSReady:         false,
        })
        return
    }
    handlers.RenderFragment(h.deps.Tmpl, w, "pages/processes/status", status)
}
```

- [ ] **Step 4: Run tests**

```bash
go test ./internal/handlers/processes -v
```

Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/handlers/processes/processes.go internal/handlers/processes/processes_test.go
git commit -m "feat(processes): scale handler and richer status fragment"
```

---

## Task 6: Update Process Templates

**Files:**
- Modify: `web/templates/pages/processes/list.html`
- Modify: `web/templates/pages/processes/row.html`
- Modify: `web/templates/pages/processes/status.html`
- Test: `internal/handlers/components_test.go`

**Interfaces:**
- Consumes: template helpers `sub`, `add`, `statusClass`, `svcClass`, `tlsClass`, `tlsSymbol`; `ProcessStatus` data.
- Produces: rendered row/status fragments with inline scale stepper and compact badges.

- [ ] **Step 1: Write the failing test**

Append to `internal/handlers/components_test.go`:

```go
func TestStatusTemplateRendersBadges(t *testing.T) {
    tmpl, err := NewTemplate()
    if err != nil {
        t.Fatalf("NewTemplate failed: %v", err)
    }
    var sb strings.Builder
    data := map[string]any{
        "Ready":            int32(2),
        "Total":            int32(2),
        "ServiceEndpoints": int32(2),
        "TLSReady":         true,
    }
    if err := tmpl.ExecuteTemplate(&sb, "pages/processes/status", data); err != nil {
        t.Fatalf("execute: %v", err)
    }
    out := sb.String()
    if !strings.Contains(out, "pods 2/2") || !strings.Contains(out, "svc 2") || !strings.Contains(out, "tls ✓") {
        t.Errorf("expected badges, got %q", out)
    }
}
```

- [ ] **Step 2: Run test to verify it fails**

```bash
go test ./internal/handlers -run TestStatusTemplateRendersBadges -v
```

Expected: FAIL — template does not render expected badges.

- [ ] **Step 3: Update templates**

Edit `web/templates/pages/processes/list.html`. Change the table header:

```html
<thead>
    <tr>
        <th>Name</th>
        <th>Image</th>
        <th>Arch</th>
        <th>Scale & Status</th>
        <th>URL</th>
        <th>Actions</th>
    </tr>
</thead>
```

Edit `web/templates/pages/processes/row.html`:

```html
<tr id="process-{{.Name}}">
    <td>{{.Name}}</td>
    <td>{{.Image}}</td>
    <td>{{.Arch}}</td>
    <td>
        <div style="display:flex;align-items:center;gap:0.75rem">
            <span>
                <button class="btn" hx-post="/processes/{{.Name}}/scale?replicas={{sub .Replicas 1}}" hx-target="#process-{{.Name}}" hx-swap="outerHTML" hx-disabled-elt="this" {{if le .Replicas 0}}disabled{{end}}>-</button>
                <span style="margin:0 0.5rem">{{.Replicas}}</span>
                <button class="btn btn-primary" hx-post="/processes/{{.Name}}/scale?replicas={{add .Replicas 1}}" hx-target="#process-{{.Name}}" hx-swap="outerHTML" hx-disabled-elt="this" {{if ge .Replicas 10}}disabled{{end}}>+</button>
            </span>
            <span hx-get="/processes/{{.Name}}/status" hx-trigger="every 3s" hx-swap="innerHTML">
                {{template "pages/processes/status" dict "Ready" 0 "Total" .Replicas "ServiceEndpoints" 0 "TLSReady" false}}
            </span>
        </div>
    </td>
    <td><a href="{{.PublicURL}}" target="_blank">{{.PublicURL}}</a></td>
    <td>
        <a href="/processes/{{.Name}}/logs">Logs</a>
        <button class="btn btn-danger" hx-post="/processes/{{.Name}}/delete" hx-target="#process-{{.Name}}" hx-swap="outerHTML" hx-confirm="Delete {{.Name}}?" style="margin-left:0.5rem;">Delete</button>
    </td>
</tr>
```

Edit `web/templates/pages/processes/status.html`:

```html
<span class="status {{statusClass .Ready .Total}}">pods {{.Ready}}/{{.Total}}</span>
<span class="status {{svcClass .ServiceEndpoints}}">svc {{.ServiceEndpoints}}</span>
<span class="status {{tlsClass .TLSReady}}">tls {{tlsSymbol .TLSReady}}</span>
```

- [ ] **Step 4: Run tests**

```bash
go test ./internal/handlers -v
```

Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add web/templates/pages/processes/list.html web/templates/pages/processes/row.html web/templates/pages/processes/status.html internal/handlers/components_test.go
git commit -m "feat(processes): inline scale stepper and compact status badges"
```

---

## Task 7: Full Test Run and Smoke Check

**Files:**
- All of the above.

**Interfaces:**
- Consumes: full codebase.
- Produces: passing test suite.

- [ ] **Step 1: Run all tests**

```bash
go test ./...
```

Expected: PASS across all packages.

- [ ] **Step 2: Build the manager binary**

```bash
go build ./cmd/manager
```

Expected: clean build.

- [ ] **Step 3: Commit if any fixes were needed**

If no changes were needed, no commit. If fixes were made:

```bash
git add -A
git commit -m "fix(processes): address scale/status test findings"
```

---

## Self-Review

**Spec coverage:**
- Inline replica scaling → Tasks 2, 5, 6.
- Compact status badges for pods/service/ingress → Tasks 1, 2, 5, 6.
- HTMX polling → Task 6 (row template uses existing polling pattern).
- Authentication → inherited from existing `h.require` wrapper.
- Max replicas 10 → Task 5 validation.
- Audit logging → Task 5.
- Testing → Each task includes tests.

**Placeholder scan:** All steps contain concrete code, commands, and expected outputs. No TBD/TODO/fill-in-later patterns.

**Type consistency:** `ProcessStatus` fields (`Ready`, `Total`, `ServiceEndpoints` as `int32`) match across model, fake, real client, handlers, and templates. `add`/`sub` helpers and replica values use `int32` consistently.
