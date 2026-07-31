package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"os"
	"strings"

	"processmanager/internal/auth"
	"processmanager/internal/db"

	"gopkg.in/yaml.v3"
)

type adminList struct {
	Admins []string `yaml:"admins"`
}

func main() {
	dsn := flag.String("dsn", os.Getenv("DATABASE_URL"), "auth database DSN")
	path := flag.String("path", "/etc/auth/admins.yaml", "path to admin users YAML")
	flag.Parse()

	if *dsn == "" {
		log.Fatal("DATABASE_URL is required")
	}

	data, err := os.ReadFile(*path)
	if err != nil {
		log.Fatalf("read admins file: %v", err)
	}

	var list adminList
	if err := yaml.Unmarshal(data, &list); err != nil {
		log.Fatalf("parse admins yaml: %v", err)
	}

	ctx := context.Background()
	pool, err := db.NewPool(ctx, *dsn)
	if err != nil {
		log.Fatalf("connect to auth db: %v", err)
	}
	defer pool.Close()

	store := auth.NewStore(pool)
	if err := store.Migrate(ctx); err != nil {
		log.Fatalf("migrate auth db: %v", err)
	}

	for _, email := range list.Admins {
		email = strings.ToLower(strings.TrimSpace(email))
		if email == "" {
			log.Printf("skipping empty admin email")
			continue
		}
		if err := store.EnsureAdmin(ctx, email); err != nil {
			log.Fatalf("ensure admin %s: %v", email, err)
		}
		log.Printf("ensured admin %s", email)
	}

	fmt.Println("admin sync complete")
}
