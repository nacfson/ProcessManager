package processes

import (
	"context"
	"html/template"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"processmanager/internal/auth"
	"processmanager/internal/handlers"
	"processmanager/internal/k8s"
	"processmanager/internal/k8s/fake"
	"processmanager/internal/models"
)

func adminRequest(method, target string, body io.Reader) *http.Request {
	req := httptest.NewRequest(method, target, body)
	sess := &auth.Session{IsAdmin: true, Email: "admin@test.com"}
	return req.WithContext(context.WithValue(req.Context(), auth.ContextUserKey, sess))
}

func TestListRendersPage(t *testing.T) {
	deps := newDeps(t)
	h := New(deps)
	mux := http.NewServeMux()
	h.RegisterRoutes(mux)

	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, httptest.NewRequest("GET", "/processes", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rec.Code)
	}
	body := rec.Body.String()
	if !strings.Contains(body, "Processes") {
		t.Errorf("missing page title")
	}
	if !strings.Contains(body, "No processes yet") {
		t.Errorf("missing empty state")
	}
}

func TestListRendersProcessRows(t *testing.T) {
	deps := newDeps(t)
	mustCreate(t, deps, models.Process{Name: "app", Image: "img:v1", Namespace: "test-ns"})

	h := New(deps)
	mux := http.NewServeMux()
	h.RegisterRoutes(mux)

	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, httptest.NewRequest("GET", "/processes", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rec.Code)
	}
	body := rec.Body.String()
	if !strings.Contains(body, "app") {
		t.Errorf("missing process name")
	}
	if !strings.Contains(body, "https://app.test.local") {
		t.Errorf("missing public url")
	}
}

func TestNewFormRenders(t *testing.T) {
	deps := newDeps(t)
	h := New(deps)
	mux := http.NewServeMux()
	h.RegisterRoutes(mux)

	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, adminRequest("GET", "/processes/new", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rec.Code)
	}
	body := rec.Body.String()
	if !strings.Contains(body, "New Process") {
		t.Errorf("missing page title")
	}
	if !strings.Contains(body, `hx-post="/processes"`) {
		t.Errorf("missing create form")
	}
}

func TestCreateProcess(t *testing.T) {
	deps := newDeps(t)
	h := New(deps)
	mux := http.NewServeMux()
	h.RegisterRoutes(mux)

	form := url.Values{}
	form.Set("name", "web")
	form.Set("image", "nginx:latest")
	form.Set("port", "80")
	form.Set("replicas", "2")
	form.Set("arch", "arm64")
	form.Set("memoryLimit", "128Mi")
	form.Set("env", "FOO=bar\n# comment\nBAZ=qux")

	rec := httptest.NewRecorder()
	req := adminRequest("POST", "/processes", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rec.Code)
	}
	body := rec.Body.String()
	if !strings.Contains(body, `id="process-web"`) {
		t.Errorf("missing row fragment")
	}
	if !strings.Contains(body, "https://web.test.local") {
		t.Errorf("missing public url")
	}

	p, err := deps.Client.GetProcess(context.Background(), "test-ns", "web")
	if err != nil {
		t.Fatalf("expected created process: %v", err)
	}
	if p.Image != "nginx:latest" || p.Port != 80 || p.Replicas != 2 || p.Arch != "arm64" || p.MemoryLimit != "128Mi" {
		t.Errorf("unexpected process fields: %+v", p)
	}
	if p.Env["FOO"] != "bar" || p.Env["BAZ"] != "qux" {
		t.Errorf("unexpected env: %v", p.Env)
	}
}

func TestCreateValidationError(t *testing.T) {
	deps := newDeps(t)
	h := New(deps)
	mux := http.NewServeMux()
	h.RegisterRoutes(mux)

	form := url.Values{}
	form.Set("name", "")
	form.Set("image", "")

	rec := httptest.NewRecorder()
	req := adminRequest("POST", "/processes", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Errorf("expected 400, got %d", rec.Code)
	}
}

func TestDeleteProcess(t *testing.T) {
	deps := newDeps(t)
	mustCreate(t, deps, models.Process{Name: "gone", Image: "img:v1", Namespace: "test-ns"})

	h := New(deps)
	mux := http.NewServeMux()
	h.RegisterRoutes(mux)

	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, adminRequest("POST", "/processes/gone/delete", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rec.Code)
	}
	_, err := deps.Client.GetProcess(context.Background(), "test-ns", "gone")
	if err == nil {
		t.Errorf("expected process to be deleted")
	}
}

func TestLogsRenders(t *testing.T) {
	deps := newDeps(t)
	mustCreate(t, deps, models.Process{Name: "app", Image: "img:v1", Namespace: "test-ns"})

	h := New(deps)
	mux := http.NewServeMux()
	h.RegisterRoutes(mux)

	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, httptest.NewRequest("GET", "/processes/app/logs", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rec.Code)
	}
	body := rec.Body.String()
	if !strings.Contains(body, "app Logs") {
		t.Errorf("missing page title")
	}
	if !strings.Contains(body, "fake logs for test-ns/app") {
		t.Errorf("missing logs")
	}
}

func TestLogStream(t *testing.T) {
	deps := newDeps(t)
	mustCreate(t, deps, models.Process{Name: "app", Image: "img:v1", Namespace: "test-ns"})

	h := New(deps)
	mux := http.NewServeMux()
	h.RegisterRoutes(mux)

	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, httptest.NewRequest("GET", "/processes/app/logs/stream", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rec.Code)
	}
	ct := rec.Header().Get("Content-Type")
	if !strings.HasPrefix(ct, "text/html") {
		t.Errorf("expected text/html content type, got %q", ct)
	}
	if !strings.Contains(rec.Body.String(), "fake logs for test-ns/app") {
		t.Errorf("missing logs")
	}
}

func TestLogStreamEscapesHTML(t *testing.T) {
	fc := &fake.Client{Logs: "<script>alert('xss')</script>"}
	deps := handlers.Deps{
		Client:    fc,
		Namespace: "test-ns",
		Domain:    "test.local",
		Tmpl:      mustTemplate(t),
	}
	mustCreate(t, deps, models.Process{Name: "app", Image: "img:v1", Namespace: "test-ns"})

	h := New(deps)
	mux := http.NewServeMux()
	h.RegisterRoutes(mux)

	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, httptest.NewRequest("GET", "/processes/app/logs/stream", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rec.Code)
	}
	body := rec.Body.String()
	if strings.Contains(body, "<script>") {
		t.Errorf("expected logs to be HTML-escaped, got %q", body)
	}
	if !strings.Contains(body, "&lt;script&gt;") {
		t.Errorf("expected escaped script tag, got %q", body)
	}
}

func TestStatusReady(t *testing.T) {
	deps := newDeps(t)
	mustCreate(t, deps, models.Process{Name: "app", Image: "img:v1", Namespace: "test-ns", Ready: 2, Total: 2})

	h := New(deps)
	mux := http.NewServeMux()
	h.RegisterRoutes(mux)

	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, httptest.NewRequest("GET", "/processes/app/status", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rec.Code)
	}
	body := rec.Body.String()
	if !strings.Contains(body, `class="status ready"`) {
		t.Errorf("expected ready status, got %q", body)
	}
	if !strings.Contains(body, "2/2") {
		t.Errorf("expected label 2/2, got %q", body)
	}
}

func TestStatusPending(t *testing.T) {
	deps := newDeps(t)
	mustCreate(t, deps, models.Process{Name: "app", Image: "img:v1", Namespace: "test-ns", Ready: 1, Total: 3})

	h := New(deps)
	mux := http.NewServeMux()
	h.RegisterRoutes(mux)

	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, httptest.NewRequest("GET", "/processes/app/status", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rec.Code)
	}
	body := rec.Body.String()
	if !strings.Contains(body, `class="status pending"`) {
		t.Errorf("expected pending status, got %q", body)
	}
	if !strings.Contains(body, "1/3") {
		t.Errorf("expected label 1/3, got %q", body)
	}
}

func TestStatusNotFound(t *testing.T) {
	deps := newDeps(t)
	h := New(deps)
	mux := http.NewServeMux()
	h.RegisterRoutes(mux)

	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, httptest.NewRequest("GET", "/processes/missing/status", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rec.Code)
	}
	body := rec.Body.String()
	if !strings.Contains(body, `class="status error"`) {
		t.Errorf("expected error status, got %q", body)
	}
	if !strings.Contains(body, "Error") {
		t.Errorf("expected error label, got %q", body)
	}
}

func TestDetailRendersPage(t *testing.T) {
	deps := newDeps(t)
	mustCreate(t, deps, models.Process{Name: "app", Image: "img:v1", Namespace: "test-ns"})

	h := New(deps)
	mux := http.NewServeMux()
	h.RegisterRoutes(mux)

	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, httptest.NewRequest("GET", "/processes/app/detail", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rec.Code)
	}
	body := rec.Body.String()
	if !strings.Contains(body, "app Detail") {
		t.Errorf("missing page title")
	}
	if !strings.Contains(body, "img:v1") {
		t.Errorf("missing overview image")
	}
	if !strings.Contains(body, "fake logs for test-ns/app") {
		t.Errorf("missing logs in pre-container")
	}
}

func TestDetailNotFound(t *testing.T) {
	deps := newDeps(t)
	h := New(deps)
	mux := http.NewServeMux()
	h.RegisterRoutes(mux)

	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, httptest.NewRequest("GET", "/processes/missing/detail", nil))
	if rec.Code != http.StatusNotFound {
		t.Fatalf("expected 404, got %d", rec.Code)
	}
}

func TestPodsFragmentRenders(t *testing.T) {
	deps := newDeps(t)
	fc := deps.Client.(*fake.Client)
	fc.Pods = map[string][]k8s.PodInfo{
		"app": {
			{
				Name:      "app-pod-1",
				Phase:     "Running",
				Restarts:  2,
				CreatedAt: time.Now(),
			},
		},
	}
	mustCreate(t, deps, models.Process{Name: "app", Image: "img:v1", Namespace: "test-ns"})

	h := New(deps)
	mux := http.NewServeMux()
	h.RegisterRoutes(mux)

	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, httptest.NewRequest("GET", "/processes/app/pods", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rec.Code)
	}
	body := rec.Body.String()
	if !strings.Contains(body, "app-pod-1") {
		t.Errorf("missing pod name")
	}
	if !strings.Contains(body, "Running") {
		t.Errorf("missing pod phase")
	}
	if !strings.Contains(body, "2") {
		t.Errorf("missing restart count")
	}
}

func TestDetailEventsFragmentRenders(t *testing.T) {
	deps := newDeps(t)
	fc := deps.Client.(*fake.Client)
	fc.Events = []k8s.EventInfo{
		{
			Type:           "Warning",
			Reason:         "Failed",
			Message:        "Back-off restarting failed container",
			InvolvedObject: "Pod/app-pod-1",
			LastTimestamp:  time.Now(),
		},
		{
			Type:           "Warning",
			Reason:         "Unrelated",
			Message:        "Unrelated warning",
			InvolvedObject: "Pod/other-pod",
			LastTimestamp:  time.Now(),
		},
	}
	mustCreate(t, deps, models.Process{Name: "app", Image: "img:v1", Namespace: "test-ns"})

	h := New(deps)
	mux := http.NewServeMux()
	h.RegisterRoutes(mux)

	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, httptest.NewRequest("GET", "/processes/app/events", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rec.Code)
	}
	body := rec.Body.String()
	if !strings.Contains(body, "Back-off restarting failed container") {
		t.Errorf("missing process warning event")
	}
	if strings.Contains(body, "Unrelated warning") {
		t.Errorf("should not contain unrelated warning event")
	}
}

func newDeps(t *testing.T) handlers.Deps {
	t.Helper()
	return handlers.Deps{
		Client:    fake.NewClient(),
		Namespace: "test-ns",
		Domain:    "test.local",
		Tmpl:      mustTemplate(t),
	}
}

func mustCreate(t *testing.T, deps handlers.Deps, p models.Process) {
	t.Helper()
	if err := deps.Client.CreateProcess(context.Background(), p, deps.Domain); err != nil {
		t.Fatalf("create process: %v", err)
	}
}

func mustTemplate(t *testing.T) *template.Template {
	tmpl, err := handlers.NewTemplate()
	if err != nil {
		t.Fatalf("load templates: %v", err)
	}
	return tmpl
}

func TestPublicURLWithPort(t *testing.T) {
	deps := newDeps(t)
	deps.IngressHTTPSPort = 30443
	if got := deps.PublicURL("app"); got != "https://app.test.local:30443" {
		t.Errorf("expected https://app.test.local:30443, got %s", got)
	}
}

func TestPublicURLWithoutPort(t *testing.T) {
	deps := newDeps(t)
	if got := deps.PublicURL("app"); got != "https://app.test.local" {
		t.Errorf("expected https://app.test.local, got %s", got)
	}
}

func TestPublicURLPort443(t *testing.T) {
	deps := newDeps(t)
	deps.IngressHTTPSPort = 443
	if got := deps.PublicURL("app"); got != "https://app.test.local" {
		t.Errorf("expected port 443 to be omitted, got %s", got)
	}
}
