package main

import (
	"fmt"
	"log"
	"net/http"
	"os"
	"path/filepath"

	"github.com/ldm0206/claude-docker/backend/internal/config"
	"github.com/ldm0206/claude-docker/backend/internal/server"
	"github.com/ldm0206/claude-docker/backend/internal/store"
	"github.com/ldm0206/claude-docker/backend/internal/system"
)

func main() {
	cfg, err := config.Load(envLookup)
	if err != nil {
		log.Fatalf("[server] config: %v", err)
	}
	if cfg.SessionSecret == "" {
		log.Fatal("[server] SESSION_SECRET environment variable is required")
	}
	dbPath := os.Getenv("DATA_DIR")
	if dbPath == "" {
		dbPath = "/data"
	}
	dbPath = filepath.Join(dbPath, "app.db")
	db, err := store.Open(dbPath)
	if err != nil {
		log.Fatalf("[server] open store: %v", err)
	}
	defer db.Close()
	// (bootstrap wiring is Task 9 — do NOT add it here)
	srv := server.New(cfg, db, system.DefaultProvisioner)
	log.Printf("[server] listening on :%d", cfg.Port)
	if err := httpListenAndServe(cfg.Port, srv.Routes()); err != nil {
		log.Fatalf("[server] %v", err)
	}
}

func envLookup(k string) (string, bool) { return osLookupEnv(k) }

func httpListenAndServe(port int, h http.Handler) error {
	return http.ListenAndServe(fmt.Sprintf(":%d", port), h)
}

func osLookupEnv(k string) (string, bool) { return os.LookupEnv(k) }
