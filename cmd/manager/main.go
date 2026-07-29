package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"net/http"
	"os"
	"strings"

	"processmanager/internal/auth"
	"processmanager/internal/db"
	"processmanager/internal/handlers"
	"processmanager/internal/handlers/audit"
	"processmanager/internal/handlers/databases"
	"processmanager/internal/handlers/home"
	"processmanager/internal/handlers/observatory"
	"processmanager/internal/handlers/processes"
	"processmanager/internal/handlers/projects"
	"processmanager/internal/handlers/settings"
	"processmanager/internal/handlers/users"
	"processmanager/internal/k8s"
	"processmanager/internal/k8s/fake"
)

func main() {
	addr := flag.String("addr", ":8080", "listen address")
	domain := flag.String("domain", envOr("DOMAIN", "nacfson.cloud"), "base domain for processes")
	authServiceURL := flag.String("auth-service-url", envOr("AUTH_SERVICE_URL", "http://auth-service.nacfson-auth.svc.cluster.local:8080"), "auth service URL")
	authInternalToken := flag.String("auth-internal-token", os.Getenv("AUTH_INTERNAL_TOKEN"), "auth internal token")
	sharedDSN := flag.String("shared-dsn", os.Getenv("SHARED_DATABASE_URL"), "shared app database DSN")
	authHost := flag.String("auth-host", envOr("AUTH_HOST", "auth.nacfson.cloud"), "auth service host")
	ingressHTTPSPort := flag.Int("ingress-https-port", 0, "ingress-nginx HTTPS port (e.g. 30443); 0 = standard port, omitted from URLs")
	fakeK8s := flag.Bool("fake-k8s", false, "use an in-memory fake Kubernetes client")
	flag.Parse()

	if *authInternalToken == "" {
		log.Println("warning: AUTH_INTERNAL_TOKEN is unset; auth API calls will fail")
	}

	handlers.AuthHost = *authHost
	if *ingressHTTPSPort > 0 && *ingressHTTPSPort != 443 {
		handlers.AuthHostWithPort = fmt.Sprintf("%s:%d", *authHost, *ingressHTTPSPort)
	} else {
		handlers.AuthHostWithPort = *authHost
	}

	useFake := *fakeK8s || os.Getenv("USE_FAKE_K8S") == "true"

	var client k8s.ClientInterface
	if useFake {
		log.Println("=================================================================")
		log.Println("RUNNING WITH FAKE K8S CLIENT")
		log.Println("=================================================================")
		client = fake.NewClient()
	} else {
		c, err := k8s.NewClient()
		if err != nil {
			log.Fatalf("failed to create k8s client: %v", err)
		}
		client = c
	}

	tmpl, err := handlers.NewTemplate()
	if err != nil {
		log.Fatalf("failed to load templates: %v", err)
	}

	deps := handlers.Deps{
		Client:           client,
		Domain:           *domain,
		Tmpl:             tmpl,
		IngressHTTPSPort: int32(*ingressHTTPSPort),
	}

	store := auth.NewHTTPAuthStore(*authServiceURL, *authInternalToken)
	deps.AuthStore = store
	deps.AuthMW = auth.NewMiddleware(store, *authHost, *domain)

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
	observatory.New(deps).RegisterRoutes(mux)
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		w.Write([]byte("ok"))
	})

	port := os.Getenv("PORT")
	if port != "" {
		*addr = ":" + port
	}

	log.Printf("process manager listening on %s", *addr)
	if err := http.ListenAndServe(*addr, handlers.SecurityHeaders(handlers.CSRFMiddleware(mux))); err != nil {
		log.Fatalf("server error: %v", err)
	}
}

func envOr(key, fallback string) string {
	if v := strings.TrimSpace(os.Getenv(key)); v != "" {
		return v
	}
	return fallback
}
