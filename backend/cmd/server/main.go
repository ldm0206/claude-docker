package main

import (
	"fmt"
	"log"
	"net/http"
	"os"
	"path/filepath"

	"github.com/ldm0206/claude-docker/backend/internal/auth"
	"github.com/ldm0206/claude-docker/backend/internal/config"
	"github.com/ldm0206/claude-docker/backend/internal/pty"
	"github.com/ldm0206/claude-docker/backend/internal/secrets"
	"github.com/ldm0206/claude-docker/backend/internal/server"
	"github.com/ldm0206/claude-docker/backend/internal/sessions"
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
	if err := os.MkdirAll(dbPath, 0o755); err != nil {
		log.Fatalf("[server] mkdir data dir: %v", err)
	}
	dbPath = filepath.Join(dbPath, "app.db")
	db, err := store.Open(dbPath)
	if err != nil {
		log.Fatalf("[server] open store: %v", err)
	}
	defer db.Close()
	if err := store.BootstrapAdmin(db, cfg.BootstrapAdminUser, cfg.BootstrapAdminPassword, auth.HashPassword); err != nil {
		log.Fatalf("[server] bootstrap admin: %v", err)
	}
	// PTY factory: each sessions.Manager.Create call builds a fresh *pty.Manager.
	// The real creack/pty + gosu runtime is Linux-only; on Windows the factory
	// is still constructed (it is not invoked until a session is created, which
	// only happens via /ws/terminal — never hit by Windows `go test`).
	factory := func(o pty.Options) sessions.PTY { return pty.New(o) }
	sess := sessions.NewManager(db, factory)
	// Load MASTER_KEY for AES-256-GCM sealing of credential presets. If unset,
	// masterKey is nil and the credential endpoints return 500. T9 may harden
	// this to a fatal startup error; for now we log and continue so the rest of
	// the server works without credentials configured.
	masterKey, merr := secrets.MasterKey(envLookup)
	if merr != nil {
		log.Printf("[server] warning: MASTER_KEY not configured — credential endpoints disabled (%v)", merr)
	}
	srv := server.New(cfg, db, system.DefaultProvisioner, sess, masterKey)
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
