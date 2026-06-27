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
