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
	"processmanager/internal/k8s"
)

func main() {
	addr := flag.String("addr", ":8080", "listen address")
	namespace := flag.String("namespace", "nacfson-personal", "namespace to manage")
	domain := flag.String("domain", "nacfson.cloud", "base domain for processes")
	authDSN := flag.String("auth-dsn", os.Getenv("AUTH_DATABASE_URL"), "auth database DSN")
	authHost := flag.String("auth-host", "auth.nacfson.cloud", "auth service host")
	flag.Parse()

	client, err := k8s.NewClient()
	if err != nil {
		log.Fatalf("failed to create k8s client: %v", err)
	}

	var amw *auth.Middleware
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
		amw = auth.NewMiddleware(store, *authHost, *domain)
	} else {
		log.Println("warning: AUTH_DATABASE_URL not set; manager dashboard is unprotected")
	}

	h, err := handlers.New(client, *namespace, *domain, amw)
	if err != nil {
		log.Fatalf("failed to create handler: %v", err)
	}

	mux := http.NewServeMux()
	h.RegisterRoutes(mux)

	port := os.Getenv("PORT")
	if port != "" {
		*addr = ":" + port
	}

	log.Printf("process manager listening on %s", *addr)
	if err := http.ListenAndServe(*addr, mux); err != nil {
		log.Fatalf("server error: %v", err)
	}
}
