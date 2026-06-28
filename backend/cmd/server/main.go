package main

import (
	"context"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"time"

	"github.com/ldm0206/claude-docker/backend/internal/auth"
	"github.com/ldm0206/claude-docker/backend/internal/config"
	"github.com/ldm0206/claude-docker/backend/internal/pty"
	"github.com/ldm0206/claude-docker/backend/internal/quota"
	"github.com/ldm0206/claude-docker/backend/internal/secrets"
	ser "github.com/ldm0206/claude-docker/backend/internal/server"
	"github.com/ldm0206/claude-docker/backend/internal/sessions"
	sshserver "github.com/ldm0206/claude-docker/backend/internal/ssh"
	"github.com/ldm0206/claude-docker/backend/internal/store"
	"github.com/ldm0206/claude-docker/backend/internal/system"
	"github.com/ldm0206/claude-docker/backend/internal/traffic"
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

	// PTY factory: each sessions.Manager.Create builds a fresh *pty.Manager.
	// Real creack/pty + gosu runtime is Linux-only.
	factory := func(o pty.Options) sessions.PTY { return pty.New(o) }
	sess := sessions.NewManager(db, factory)

	masterKey, merr := secrets.MasterKey(envLookup)
	if merr != nil {
		log.Printf("[server] warning: MASTER_KEY not configured — credential endpoints disabled (%v)", merr)
	}

	// --- Quota: disk soft-limit monitor + cgroup v2 cpu/mem (Linux runtime) ---
	qsvc := quota.New(quota.DuDiskUsage{}, quota.CgroupFSWriter{}, "/home")

	// --- Traffic: nftables cgroup counters → monthly buckets. Probe availability
	// (needs the nft binary + CAP_NET_ADMIN); on failure the sampler runs no-op.
	// Per-uid Read errors are also skipped gracefully inside the sampler.
	tsvc := traffic.New(&traffic.NftCLI{}, db)
	if _, err := exec.LookPath("nft"); err != nil {
		tsvc.MarkAvailable(false)
		log.Printf("[server] warning: nft not found — traffic accounting in no-op mode (%v)", err)
	}

	// --- Embedded SSH/SFTP server (Linux runtime). No-op Start off-Linux. ---
	sftpPort := os.Getenv("SFTP_PORT")
	if sftpPort == "" {
		sftpPort = "22"
	}
	sshSrv := sshserver.New(db, ":"+sftpPort)

	srv := ser.New(cfg, db, system.DefaultProvisioner, sess, masterKey, qsvc, tsvc)

	// Background loops.
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go tsvc.Start(ctx, 5*time.Second)
	go func() {
		if err := sshSrv.Start(ctx); err != nil {
			log.Printf("[server] ssh/sftp: %v", err)
		}
	}()

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
