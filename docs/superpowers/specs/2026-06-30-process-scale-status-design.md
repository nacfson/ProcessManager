# Process Scale & Status Design

## Context

ProcessManager is a Kubernetes-based web process manager with a Go + HTMX dashboard. The dashboard already lists processes, creates them, deletes them, shows logs, and polls pod status.

This design adds inline replica scaling to the process list and enriches each row with compact status badges for pods, service endpoints, and ingress/TLS readiness.

## Goals

- Let operators scale a process up or down directly from the process list.
- Surface why a process may be unreachable (service endpoints, TLS state) without leaving the dashboard.
- Keep the existing Go + HTMX stack; no new build steps or frontend frameworks.
- Reuse the existing component library and domain-split handler structure.

## Non-Goals

- Editing other process fields (image, env, resources) in this version.
- Restart/rolling-restart controls.
- WebSocket or SSE live updates; polling remains sufficient.
- Automatic rollback or canary scaling.
- Role-based access control for scaling; any authenticated dashboard user may scale.

## Design

### UI/UX

Each process row gains a **Scale & Status** cell with:

- A compact stepper: `-` button, current replica count, `+` button.
- Compact text badges to the right of the stepper:
  - `pods 2/2` (green when ready == total, yellow when 0 < ready < total, red when ready == 0)
  - `svc N` (green when endpoints > 0, red when 0)
  - `tls ✓` / `tls …` / `tls ✗` (green when TLS secret exists and ready, yellow when pending, red when missing)

The user selected **Option A** (compact text badges) from the visual companion mockups.

Clicking `-` or `+` immediately posts via HTMX. The server updates the Deployment and returns the refreshed row fragment, which replaces the existing row. While the request is in flight, the buttons disable themselves. The status badges continue to poll every 3 seconds via the existing status endpoint.

### Architecture

```
internal/handlers/processes/processes.go
  POST /processes/{name}/scale
  GET  /processes/{name}/status   (extended)

internal/k8s/client.go
  ScaleProcess(ctx, namespace, name string, replicas int32) error
  GetProcessStatus(ctx, namespace, name string) (ProcessStatus, error)

web/templates/pages/processes/row.html
web/templates/pages/processes/status.html
```

The `ClientInterface` is extended with the two new methods. The fake client in `internal/k8s/fake/fake.go` implements them in memory for tests.

### Data Model

```go
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

### API & Data Flow

**Scale request**

```
POST /processes/{name}/scale?replicas=3
```

The handler:

1. Parses `replicas` as an integer.
2. Validates `0 <= replicas <= 10`.
3. Calls `Client.ScaleProcess` to patch the Deployment.
4. Logs an audit event (`scale`, `process`, name).
5. Returns the refreshed `pages/processes/row` fragment.

Errors return `400` for validation failures and `500` for Kubernetes errors. Validation errors are rendered inline into the row; server errors trigger a toast via `HX-Trigger`.

**Status request**

```
GET /processes/{name}/status
```

The handler calls `Client.GetProcessStatus` and returns the `pages/processes/status` fragment with the compact badge markup.

### Kubernetes Lookup Details

- **Deployment replicas and ready count**: from `AppsV1().Deployments(namespace).Get`.
- **Service endpoints**: list Endpoints for the process Service and count ready addresses.
- **Ingress & TLS**: check the Ingress exists; check the TLS secret referenced by `ingress.Spec.TLS[0].SecretName` exists and has no failing cert-manager annotations.

### Error Handling & Feedback

- Invalid replica count → `400` with inline error text swapped into the scale cell.
- Kubernetes API errors → `500` plus `HX-Trigger: showToast` with a concise message.
- Busy state → buttons use `hx-disabled-elt="this"` to prevent double submits.
- Polling failures → status fragment renders `pods ?/?` in red without breaking the page.

### Testing Strategy

- **Handler tests** in `internal/handlers/processes/processes_test.go`:
  - Scale endpoint increases/decreases replicas.
  - Validation rejects negative or oversized replica counts.
  - Status endpoint returns the expected badge text.
- **Fake client tests** in `internal/k8s/fake/fake_test.go`:
  - `ScaleProcess` updates in-memory replicas.
  - `GetProcessStatus` returns correct pod/service/ingress state.
- **Component test** in `internal/handlers/components_test.go`:
  - Status template renders the compact badge markup.

## Open Questions / Future Work

- Should scaling be restricted to admin users once roles are enforced?
- Should we expose a numeric input for large jumps, or keep +/- only?
- Should the status badges link to a detail page with full Kubernetes conditions?

## Decision Log

| Decision | Choice | Reason |
|----------|--------|--------|
| Status display style | Compact text badges (Option A) | Chosen by user from visual companion mockups; precise and scan-friendly. |
| Scale control | Inline +/- stepper | Fast daily operation; matches user's inline-in-list preference. |
| Scale API | `POST /processes/{name}/scale?replicas=N` | Idempotent, explicit, easy to validate. |
| Status update method | HTMX polling every 3s | Consistent with existing dashboard; no new real-time infrastructure. |
| Max replicas | 10 | Prevents accidental runaway scaling on a small cluster. |
| Access control | Any authenticated user | Matches current dashboard behavior; revisit when RBAC lands. |
