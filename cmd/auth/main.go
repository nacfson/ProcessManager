package main

import (
	"context"
	"flag"
	"log"
	"net/http"
	"os"
	"strings"
	"time"

	"golang.org/x/crypto/bcrypt"
	"processmanager/internal/auth"
	"processmanager/internal/db"
)

func main() {
	addr := flag.String("addr", ":8080", "listen address")
	dsn := flag.String("dsn", os.Getenv("DATABASE_URL"), "auth database DSN")
	cookieDomain := flag.String("cookie-domain", os.Getenv("COOKIE_DOMAIN"), "session cookie domain")
	sessionTTL := flag.Duration("session-ttl", 7*24*time.Hour, "session lifetime")
	apiAllowedOrigins := flag.String("api-allowed-origins", os.Getenv("AUTH_API_ALLOWED_ORIGINS"), "comma-separated origins allowed to call the JSON auth API")
	flag.Parse()

	if *dsn == "" {
		log.Fatal("DATABASE_URL is required")
	}
	if *cookieDomain == "" {
		*cookieDomain = "nacfson.cloud"
	}

	ctx := context.Background()
	pool, err := db.NewPool(ctx, *dsn)
	if err != nil {
		log.Fatalf("failed to connect to auth db: %v", err)
	}
	defer pool.Close()

	store := auth.NewStore(pool)
	if err := store.Migrate(ctx); err != nil {
		log.Fatalf("failed to migrate auth db: %v", err)
	}

	if err := bootstrapAdmin(ctx, store); err != nil {
		log.Fatalf("failed to bootstrap admin: %v", err)
	}

	limiter := auth.NewLoginLimiter(5, time.Minute)
	defer limiter.Stop()
	h, err := auth.NewHandler(store, *cookieDomain, *sessionTTL, limiter, splitAndTrim(*apiAllowedOrigins))
	if err != nil {
		log.Fatalf("failed to create auth handler: %v", err)
	}

	mux := http.NewServeMux()
	h.RegisterRoutes(mux)

	port := os.Getenv("PORT")
	if port != "" {
		*addr = ":" + port
	}

	log.Printf("auth service listening on %s", *addr)
	if err := http.ListenAndServe(*addr, mux); err != nil {
		log.Fatalf("server error: %v", err)
	}
}

func splitAndTrim(value string) []string {
	if value == "" {
		return nil
	}
	parts := strings.Split(value, ",")
	out := make([]string, 0, len(parts))
	for _, part := range parts {
		part = strings.TrimSpace(part)
		if part != "" {
			out = append(out, part)
		}
	}
	return out
}

func bootstrapAdmin(ctx context.Context, store *auth.Store) error {
	email := os.Getenv("BOOTSTRAP_ADMIN_EMAIL")
	password := os.Getenv("BOOTSTRAP_ADMIN_PASSWORD")
	if email == "" || password == "" {
		return nil
	}
	exists, err := store.UserExists(ctx)
	if err != nil {
		return err
	}
	if exists {
		return nil
	}
	hash, err := bcrypt.GenerateFromPassword([]byte(password), bcrypt.DefaultCost)
	if err != nil {
		return err
	}
	if _, err := store.CreateUser(ctx, email, string(hash), true, true); err != nil {
		return err
	}
	log.Printf("bootstrapped admin user %s", email)
	return nil
}
