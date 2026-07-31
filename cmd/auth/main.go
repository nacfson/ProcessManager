package main

import (
	"context"
	"flag"
	"log"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"processmanager/internal/auth"
	"processmanager/internal/db"
)

func main() {
	addr := flag.String("addr", ":8080", "listen address")
	dsn := flag.String("dsn", os.Getenv("DATABASE_URL"), "auth database DSN")
	cookieDomain := flag.String("cookie-domain", os.Getenv("COOKIE_DOMAIN"), "session cookie domain")
	sessionTTL := flag.Duration("session-ttl", envDuration("SESSION_TTL", "SESSION_LIFETIME", 7*24*time.Hour), "session lifetime")
	magicLinkTTL := flag.Duration("magic-link-ttl", defaultMagicLinkTTLFromEnv(), "magic-link token lifetime")
	apiAllowedOrigins := flag.String("api-allowed-origins", os.Getenv("AUTH_API_ALLOWED_ORIGINS"), "comma-separated origins allowed to call the JSON auth API")
	allowedRedirects := flag.String("allowed-redirects", os.Getenv("AUTH_ALLOWED_REDIRECTS"), "comma-separated URLs allowed for redirect after login/logout")
	flag.Parse()

	if *dsn == "" {
		log.Fatal("DATABASE_URL is required")
	}
	if *cookieDomain == "" {
		*cookieDomain = "nacfson.cloud"
	}
	if os.Getenv("AUTH_INTERNAL_TOKEN") == "" {
		log.Println("warning: AUTH_INTERNAL_TOKEN is unset; /api/internal/* will reject requests")
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

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

	go sweepExpiredLoginTokens(ctx, store)

	limiter := auth.NewLoginLimiter(5, time.Minute)
	defer limiter.Stop()
	h, err := auth.NewHandler(store, *cookieDomain, *sessionTTL, *magicLinkTTL, limiter, splitAndTrim(*apiAllowedOrigins), splitAndTrim(*allowedRedirects))
	if err != nil {
		log.Fatalf("failed to create auth handler: %v", err)
	}

	mux := http.NewServeMux()
	h.RegisterRoutes(mux)

	port := os.Getenv("PORT")
	if port != "" {
		*addr = ":" + port
	}

	srv := &http.Server{Addr: *addr, Handler: mux}
	go func() {
		<-ctx.Done()
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = srv.Shutdown(shutdownCtx)
	}()

	log.Printf("auth service listening on %s", *addr)
	if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
		log.Fatalf("server error: %v", err)
	}
}

func defaultMagicLinkTTLFromEnv() time.Duration {
	if v := os.Getenv("MAGIC_LINK_TTL"); v != "" {
		if d, err := time.ParseDuration(v); err == nil {
			return d
		}
	}
	return 15 * time.Minute
}

func sweepExpiredLoginTokens(ctx context.Context, store *auth.Store) {
	ticker := time.NewTicker(10 * time.Minute)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			n, err := store.DeleteExpiredLoginTokens(ctx)
			if err != nil {
				log.Printf("delete expired login tokens: %v", err)
				continue
			}
			if n > 0 {
				log.Printf("deleted %d expired login tokens", n)
			}
		}
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

// envDuration reads the first non-empty env var as a time.Duration, else returns fallback.
func envDuration(primary, secondary string, fallback time.Duration) time.Duration {
	for _, key := range []string{primary, secondary} {
		raw := strings.TrimSpace(os.Getenv(key))
		if raw == "" {
			continue
		}
		d, err := time.ParseDuration(raw)
		if err != nil {
			log.Printf("warning: invalid %s=%q, using default %s", key, raw, fallback)
			return fallback
		}
		return d
	}
	return fallback
}

func bootstrapAdmin(ctx context.Context, store *auth.Store) error {
	email := strings.ToLower(strings.TrimSpace(os.Getenv("BOOTSTRAP_ADMIN_EMAIL")))
	if email == "" {
		return nil
	}
	exists, err := store.UserExists(ctx)
	if err != nil {
		return err
	}
	if exists {
		return nil
	}
	if err := store.EnsureAdmin(ctx, email); err != nil {
		return err
	}
	log.Printf("bootstrapped admin user %s", email)
	return nil
}
