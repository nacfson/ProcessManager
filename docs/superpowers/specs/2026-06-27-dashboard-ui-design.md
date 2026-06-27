# Dashboard UI Design

## Context

ProcessManager is a Kubernetes-based web process manager. It already has a working central auth service, a shared Postgres database for per-project isolated DBs, and a basic Go + HTMX dashboard for registering container images and exposing them as `https://{name}.nacfson.cloud`.

The existing dashboard (`internal/handlers/handlers.go` + `web/templates/*.html`) supports:

- Listing processes
- Creating a process via a form
- Deleting a process
- Viewing pod logs
- A dark-themed shared layout

This design turns that dashboard into a full platform control panel.

## Goals

- Provide a single web UI for managing processes, projects, databases, users, settings, and audit activity.
- Keep the existing Go + HTMX stack; avoid adding a heavy SPA build step.
- Make the UI consistent and maintainable through reusable components.
- Show live process status with lightweight polling.
- Surface concise error indicators in the UI; detailed debugging stays in SSH/kubectl.

## Non-Goals

- Deep log analytics or search within the dashboard.
- Real-time WebSocket/SSE updates (polling is sufficient).
- Browser-based remote shell access.
- Billing or usage metering.

## Design

### Approach

**Component-first HTMX system with domain-split handlers (Approach C).**

The handler layer is split by domain so each section has clear ownership. A shared component library keeps the UI consistent and makes HTMX fragment responses reusable.

### Architecture & Navigation

The dashboard remains a single Go service (`cmd/manager`) serving server-rendered HTML.

```
internal/handlers/
├── handlers.go          # shared layout, auth wrapper, render helpers
├── components/          # reusable template partials
├── processes/           # /processes/*
├── projects/            # /projects/*
├── databases/           # /databases/*
├── users/               # /users/*
├── settings/            # /settings/*
└── audit/               # /activity/*
```

Each domain package exposes:

```go
func RegisterRoutes(mux *http.ServeMux, deps Deps)
```

`Deps` contains the Kubernetes client, auth/shared DB pools, auth middleware, template root, and base domain.

Main navigation:

| Route | Section | Purpose |
|-------|---------|---------|
| `/` | Dashboard | Summary cards: active processes, projects, recent activity |
| `/processes` | Processes | List, create, delete, view logs |
| `/projects` | Projects | Project CRUD and member assignment |
| `/databases` | Databases | Per-project DB provisioning overview |
| `/users` | Users | Admin-only user and membership management |
| `/activity` | Activity | Audit trail |
| `/settings` | Settings | Platform defaults (domain, registry, etc.) |

### Component System

Templates live under `web/templates/components/` and are composed by page templates.

Core components:

- `layout.html` — HTML shell, nav, CSS variables, HTMX script
- `card.html` — bordered panel wrapper
- `page-header.html` — title + primary action button
- `data-table.html` — responsive table with sortable headers
- `form-field.html` — label + input/select/textarea with error slot
- `badge.html` — status badge (Ready, Pending, Error)
- `button.html` — primary/danger variants with HTMX loading states
- `toast.html` — inline flash messages
- `empty-state.html` — "No items yet" placeholder
- `modal.html` — confirmation dialog shell for destructive actions

Example page composition:

```html
{{template "layout" .}}
{{define "content"}}
  {{template "page-header" dict "Title" "Processes" "Action" "New Process" "Href" "/processes/new"}}
  {{template "card"}}
    {{template "data-table" .Table}}
  {{end}}
{{end}}
```

HTMX fragment responses reuse the same components so a status badge or table row looks identical whether it arrives on first load or via polling.

The template root registers a small `dict` helper so components can accept multiple named parameters:

```go
"dict": func(values ...any) (map[string]any, error) { ... }
```

### Data Flow & Polling

**Full-page requests**
The browser requests `/processes`. The handler fetches data from Kubernetes and the relevant DB, renders the full page, and returns HTML.

**HTMX fragment requests**
Actions like `POST /processes` or `POST /processes/{name}/delete` return partial HTML:

- Success: a new table row swapped with `hx-swap="beforeend"`
- Failure: the form with validation errors swapped into the form container

**Polling**
Status badges include:

```html
<span hx-get="/processes/{name}/status" hx-trigger="every 3s" hx-swap="outerHTML">
```

A lightweight `/status` endpoint returns only the badge fragment. Logs use the same pattern with `hx-swap="scroll-bottom"`.

**Backend data sources**

- Kubernetes API for Deployments, Services, Ingresses, Pods, PVCs
- Auth DB for users, sessions, projects, memberships
- Shared app DB for per-project database provisioning state
- Audit events stored in the auth DB or shared DB

### Error Handling, Feedback & Logs

**Error responses**
- HTMX requests return `400`/`500` with a toast fragment or inline error slot.
- Full-page errors render an error view inside the shared layout.
- Validation errors appear next to the offending field using `form-field`'s error slot.

**User feedback**
- Toasts are triggered via `HX-Trigger: {"showToast":"..."}` response headers.
- Destructive actions use `modal.html` for confirmation.

**Logs**
Each process row links to `/processes/{name}/logs`. That page shows:

- A pod selector when multiple pods exist
- A log pane that polls `/processes/{name}/logs/stream` every 2 seconds and scrolls to the bottom
- A "Download logs" button (future enhancement)

The dashboard shows only *which* error occurred; detailed debugging is done via SSH/kubectl.

### Testing Strategy

**Handler tests**
Each domain package has `_test.go` files using `httptest.NewRecorder` and a fake Kubernetes client. Tests cover:

- Route registration
- Happy-path HTML rendering
- Validation error responses
- HTMX fragment responses (`text/html` and expected partial content)

**Component rendering tests**
A small helper renders a component template with sample data and asserts on key strings.

**Fake/stub layers**
- `internal/k8s/fake` — in-memory Kubernetes client implementation
- `internal/db/fake` — in-memory auth/shared DB for handler tests

No browser automation tests are required initially.

## Open Questions / Future Work

- Should the dashboard support dark/light theme toggle?
- Should activity logs include Kubernetes events or only user actions?
- Should logs be persisted to S3 or similar for long-term access?

## Decision Log

| Decision | Choice | Reason |
|----------|--------|--------|
| Stack | Go + HTMX | Fits existing backend, minimal build complexity |
| Handler organization | Domain packages | Clear ownership and testability |
| UI consistency | Shared component library | Avoids one-giant-template problem |
| Live updates | 2–3 second polling | Simpler than WebSocket/SSE, sufficient for status |
| Error detail | Surface-level only | Detailed debugging happens in SSH/kubectl |
