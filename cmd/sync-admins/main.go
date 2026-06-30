package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"os"

	"processmanager/internal/auth"
	"processmanager/internal/db"

	"golang.org/x/crypto/bcrypt"
	"gopkg.in/yaml.v3"
)

type AdminUser struct {
	Email    string `yaml:"email"`
	Password string `yaml:"password"`
}

type adminList struct {
	Admins []AdminUser `yaml:"admins"`
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
	admins := list.Admins

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

	for _, a := range admins {
		if a.Email == "" || a.Password == "" {
			log.Printf("skipping admin with empty email or password")
			continue
		}

		hash, err := bcrypt.GenerateFromPassword([]byte(a.Password), bcrypt.DefaultCost)
		if err != nil {
			log.Fatalf("hash password for %s: %v", a.Email, err)
		}

		existing, err := store.GetUserByEmail(ctx, a.Email)
		if err != nil {
			log.Fatalf("lookup user %s: %v", a.Email, err)
		}

		if existing == nil {
			if _, err := store.CreateUser(ctx, a.Email, string(hash), true); err != nil {
				log.Fatalf("create admin %s: %v", a.Email, err)
			}
			log.Printf("created admin %s", a.Email)
		} else {
			// Update password hash and ensure admin flag.
			if _, err := pool.Exec(ctx,
				`UPDATE users SET password_hash = $1, is_admin = true WHERE email = $2`,
				string(hash), a.Email); err != nil {
				log.Fatalf("update admin %s: %v", a.Email, err)
			}
			log.Printf("updated admin %s", a.Email)
		}
	}

	fmt.Println("admin sync complete")
}
