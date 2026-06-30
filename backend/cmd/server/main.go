package main

import (
	"context"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"time"

	"github.com/ldm0206/claude-docker/backend/internal/auth"
	"github.com/ldm0206/claude-docker/backend/internal/capture"
	"github.com/ldm0206/claude-docker/backend/internal/config"
	"github.com/ldm0206/claude-docker/backend/internal/pty"
	"github.com/ldm0206/claude-docker/backend/internal/quota"
	ser "github.com/ldm0206/claude-docker/backend/internal/server"
	"github.com/ldm0206/claude-docker/backend/internal/sessions"
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
	// The live PTY map is in-memory and does not survive a restart; any session
	// row still marked alive=1 is an orphan whose process is gone. Reap them now
	// so they stop counting toward per-user session caps (otherwise a fresh
	// /ws/terminal create 409s immediately after deploy).
	if n, err := db.ReapStaleSessions(); err != nil {
		log.Fatalf("[server] reap stale sessions: %v", err)
	} else if n > 0 {
		log.Printf("[server] reaped %d stale session(s) left alive by a prior run", n)
	}
	if err := store.BootstrapAdmin(db, cfg.BootstrapAdminUser, cfg.BootstrapAdminPassword, auth.HashPassword); err != nil {
		log.Fatalf("[server] bootstrap admin: %v", err)
	}
	if err := ensureUsersProvisioned(db, system.DefaultProvisioner); err != nil {
		log.Fatalf("[server] provision existing users: %v", err)
	}
	if cfg.TemplateUser != "" {
		if _, err := db.GetUserByUsername(cfg.TemplateUser); err != nil {
			log.Printf("[server] warning: CLAUDE_TEMPLATE_USER=%q is not a known user; credential copy will be a no-op (%v)", cfg.TemplateUser, err)
		}
	}

	// PTY factory: each sessions.Manager.Create builds a fresh *pty.Manager.
	// Real creack/pty + gosu runtime is Linux-only.
	factory := func(o pty.Options) sessions.PTY { return pty.New(o) }
	sess := sessions.NewManager(db, factory)

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

	// --- Capture: admin-only, per-session MITM (Linux runtime). The real
	// MITMRunner is built from the CA the entrypoint generates + installs into
	// the trust store; the proxy starts LAZILY on the first admin enable
	// (capture.Enable), not at boot. The Response hook (session resolution +
	// redaction + store.Add) is Linux-runtime; see capture/service_linux.go.
	// -- platform-aware runner construction via build-tagged NewMITMRunner --
	capPort := 8888
	if v, err := strconv.Atoi(os.Getenv("CLAUDE_DEBUG_PROXY_PORT")); err == nil && v > 0 {
		capPort = v
	}
	caRoot := os.Getenv("CLAUDE_DEBUG_SSL_CA_DIR")
	capStore := capture.NewStore()
	capRunner := capture.NewMITMRunner(caRoot, nil, capStore, db)
	capSvc := capture.NewService(capRunner, capStore, db, capPort)

	srv := ser.New(cfg, db, system.DefaultProvisioner, sess, qsvc, tsvc, capSvc)

	// Background loops.
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go tsvc.Start(ctx, 5*time.Second)

	log.Printf("[server] listening on :%d", cfg.Port)
	if err := httpListenAndServe(cfg.Port, srv.Routes()); err != nil {
		log.Fatalf("[server] %v", err)
	}
}

func envLookup(k string) (string, bool) { return osLookupEnv(k) }

// ensureUsersProvisioned repairs any user whose DB row exists without a
// provisioned Linux account/home. BootstrapAdmin (called just before this)
// inserts the first admin as a DB row only — without this pass that admin has
// no /home/<user>/workspace, so the first /ws/terminal connect spawns
// `gosu <admin> bash -l` into a non-existent home and the PTY exits instantly
// (the "connects then immediately disconnects" symptom). Ensure is idempotent,
// so already-provisioned users (e.g. those created via the admin API, which
// provisions at creation time) are a no-op here.
func ensureUsersProvisioned(db *store.DB, p system.AccountProvisioner) error {
	users, err := db.ListUsers()
	if err != nil {
		return fmt.Errorf("list users: %w", err)
	}
	for _, u := range users {
		if err := p.Ensure(u.Username, u.UID); err != nil {
			log.Printf("[server] warning: provision user %s (uid %d): %v", u.Username, u.UID, err)
		}
	}
	return nil
}

func httpListenAndServe(port int, h http.Handler) error {
	return http.ListenAndServe(fmt.Sprintf(":%d", port), h)
}

func osLookupEnv(k string) (string, bool) { return os.LookupEnv(k) }
