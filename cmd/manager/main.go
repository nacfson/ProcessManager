package main

import (
	"context"
	"errors"
	"flag"
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
	"processmanager/internal/handlers/processes"
	"processmanager/internal/handlers/projects"
	"processmanager/internal/handlers/settings"
	"processmanager/internal/handlers/users"
	"processmanager/internal/k8s"
	"processmanager/internal/k8s/fake"
)

func parseSSLMode(dsn string) string {
	lower := strings.ToLower(dsn)
	i := strings.Index(lower, "sslmode=")
	if i < 0 {
		return ""
	}
	rest := dsn[i+len("sslmode="):]
	j := strings.IndexAny(rest, "& ")
	if j < 0 {
		return strings.ToLower(rest)
	}
	return strings.ToLower(rest[:j])
}

func validateAuthDSN(dsn string) error {
	if dsn == "" {
		return errors.New("AUTH_DATABASE_URL is required")
	}
	mode := parseSSLMode(dsn)
	if mode == "" {
		return errors.New("AUTH_DATABASE_URL must specify sslmode")
	}
	if mode == "disable" {
		return errors.New("AUTH_DATABASE_URL must use TLS; sslmode=disable is not allowed")
	}
	if mode != "require" && mode != "verify-full" {
		return errors.New("AUTH_DATABASE_URL must use sslmode=require or sslmode=verify-full")
	}
	return nil
}

func main() {
	addr := flag.String("addr", ":8080", "listen address")
	namespace := flag.String("namespace", "nacfson-personal", "namespace to manage")
	domain := flag.String("domain", "nacfson.cloud", "base domain for processes")
	authDSN := flag.String("auth-dsn", os.Getenv("AUTH_DATABASE_URL"), "auth database DSN")
	sharedDSN := flag.String("shared-dsn", os.Getenv("SHARED_DATABASE_URL"), "shared app database DSN")
	authHost := flag.String("auth-host", "auth.nacfson.cloud", "auth service host")
	fakeK8s := flag.Bool("fake-k8s", false, "use an in-memory fake Kubernetes client")
	flag.Parse()

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
		Client:    client,
		Namespace: *namespace,
		Domain:    *domain,
		Tmpl:      tmpl,
	}

	if err := validateAuthDSN(*authDSN); err != nil {
		log.Fatalf("failed to validate auth configuration: %v", err)
	}

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
