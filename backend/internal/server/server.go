package server

import (
	"encoding/json"
	"net/http"
	"sync"
	"sync/atomic"

	"github.com/go-chi/chi/v5"
	"github.com/ldm0206/claude-docker/backend/internal/config"
	"github.com/ldm0206/claude-docker/backend/internal/pty"
	"github.com/ldm0206/claude-docker/backend/internal/store"
	"github.com/ldm0206/claude-docker/backend/internal/system"
	"github.com/ldm0206/claude-docker/backend/internal/ui"
)

// currentUser holds the identity the shared PTY currently runs as. Plan 2 keeps
// a SINGLE shared terminal (multi-session is Plan 3), so we swap this on login
// and the next lazy Start() / restart picks up the new user. The env factory
// reads these fields under userMu so BuildUserEnv always sees a consistent
// (username, uid, claudeConfigDir) tuple.
type currentUser struct {
	username        string
	uid             int
	claudeConfigDir string
}

type Server struct {
	cfg         *config.Config
	db          *store.DB
	restarting  atomic.Bool
	pty         *pty.Manager
	provisioner system.AccountProvisioner

	userMu sync.RWMutex
	user   currentUser
}

// New builds a Server wired to the given config, user store, and account
// provisioner. The store remains owned by the caller (main.go opens/closes it);
// New only retains it.
//
// The shared PTY's Env factory is LAZY: it reads the current user under
// userMu on every call, so a login that swaps the user (setCurrentUser) is
// observed by the next Start(). Until anyone logs in, the terminal runs as the
// root admin with BuildClaudeEnv (same behavior as pre-Fix-2). Once a user
// logs in, setCurrentUser populates username/uid/claudeConfigDir and the env
// factory switches to BuildUserEnv, driving gosu via Options.Username.
func New(cfg *config.Config, db *store.DB, provisioner system.AccountProvisioner) *Server {
	s := &Server{cfg: cfg, db: db, provisioner: provisioner}
	p := pty.New(pty.Options{
		Cwd:     "/workspace",
		Env:     s.buildPTYEnv,
		Command: "bash",
	})
	s.pty = p
	return s
}

// buildPTYEnv is the lazy env factory for the shared PTY. It reads the
// current user under userMu and builds either BuildUserEnv (a user is set)
// or BuildClaudeEnv (no user yet → root admin's terminal). credEnv is nil
// for now — Plan 3 fills per-user credentials here.
func (s *Server) buildPTYEnv() []string {
	u, ok := s.currentUser()
	if !ok {
		return pty.BuildClaudeEnv(s.cfg)
	}
	return pty.BuildUserEnv(s.cfg, u.username, u.claudeConfigDir, nil)
}

// setCurrentUser records the identity the shared PTY should run as. Called on
// successful login (auth_handler) and from the terminal WS handler before the
// lazy Start(). The NEXT Start() spawns `gosu <username> bash -l` with
// BuildUserEnv-constructed env. An already-alive PTY keeps running as the
// previous user until the operator or frontend restarts the session.
// (Acceptable for Plan 2's single shared PTY; Plan 3 adds per-session PTYs.)
// Mutating the username is idempotent: a no-op if the same user logs in again.
func (s *Server) setCurrentUser(username string, uid int, claudeConfigDir string) {
	s.userMu.Lock()
	s.user = currentUser{username: username, uid: uid, claudeConfigDir: claudeConfigDir}
	s.userMu.Unlock()
	// Mirror into the PTY manager so Start()'s gosu branch fires for the
	// new user. Only the next Start() observes this; a live PTY is unaffected.
	s.pty.SetUsername(username)
}

// currentUser returns the live identity and ok=true if a user is set, or
// ok=false if no user has logged in yet (root admin fallback). Callers read
// the returned struct by value; the lock is released before the value is
// returned, so a concurrent setCurrentUser may swap the underlying field —
// but each call here reflects a consistent prior state.
func (s *Server) currentUser() (currentUser, bool) {
	s.userMu.RLock()
	defer s.userMu.RUnlock()
	if s.user.username == "" {
		return currentUser{}, false
	}
	return s.user, true
}

// authWSUser is the WebSocket auth gate. WS routes are NOT under authMiddleware
// (they check the cookie inline before the upgrade), so they historically
// only called authedIdentity — which verifies the cookie signature but skips
// the live DB lookup, letting a suspended or since-deleted user keep using
// the terminal/metrics WS on a stale but still-valid cookie.
//
// authWSUser closes that gap: it verifies the cookie AND re-fetches the live
// user AND rejects suspended users. Returns (user, true) only for an active,
// non-suspended account. Callers must write 401 and NOT accept the upgrade on
// (User{}, false).
func (s *Server) authWSUser(r *http.Request) (store.User, bool) {
	id, ok := s.authedIdentity(r)
	if !ok {
		return store.User{}, false
	}
	u, err := s.db.GetUserByUsername(id.Username)
	if err != nil {
		return store.User{}, false // deleted since the cookie was issued
	}
	if u.Suspended {
		return store.User{}, false // suspended mid-session
	}
	return u, true
}

func (s *Server) PTY() *pty.Manager { return s.pty }

func (s *Server) Routes() http.Handler {
	r := chi.NewRouter()
	r.Get("/health", s.handleHealth)
	r.Post("/auth", s.handleLogin)
	r.Post("/logout", s.handleLogout)
	// /ws/* routes check the cookie inside the handler before upgrading.
	r.Get("/ws/terminal", s.handleTerminalWS)
	r.Get("/ws/captures", s.handleCapturesWS)
	r.Get("/ws/metrics", s.handleMetricsWS)
	r.Group(func(r chi.Router) {
		r.Use(s.authMiddleware)
		r.Get("/api/state", s.handleState)
		r.Post("/api/session/restart", s.handleRestart)
		r.Post("/auth/change-password", s.handleChangePassword)
		r.Post("/api/capture/enable", s.handleCaptureEnable)
		r.Post("/api/capture/disable", s.handleCaptureDisable)
		r.Post("/api/captures/clear", s.handleCapturesClear)
		// Admin user-management routes
		r.Group(func(r chi.Router) {
			r.Use(s.requireAdmin)
			r.Post("/api/admin/users", s.handleAdminCreateUser)
			r.Get("/api/admin/users", s.handleAdminListUsers)
			r.Delete("/api/admin/users/{id}", s.handleAdminDeleteUser)
			r.Post("/api/admin/users/{id}/suspend", s.handleAdminSuspendUser)
			r.Post("/api/admin/users/{id}/unsuspend", s.handleAdminUnsuspendUser)
		})
	})
	r.Handle("/*", ui.SPA())
	return r
}

func (s *Server) handleHealth(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, 200, map[string]any{"ok": true})
}

func (s *Server) handleLogout(w http.ResponseWriter, _ *http.Request) {
	http.SetCookie(w, &http.Cookie{Name: "session", Value: "", Path: "/", MaxAge: -1})
	writeJSON(w, 200, map[string]any{"ok": true})
}

func (s *Server) handleState(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, 200, map[string]any{"captureOn": false, "sessionAlive": s.pty.Alive()})
}

// handleRestart kills and re-spawns the shared PTY (frontend calls this from
// the "Session ended → Restart" button).
func (s *Server) handleRestart(w http.ResponseWriter, _ *http.Request) {
	s.restarting.Store(true)
	defer s.restarting.Store(false)
	s.pty.Stop()
	if err := s.pty.Start(); err != nil {
		writeJSON(w, 500, map[string]any{"error": "restart failed"})
		return
	}
	writeJSON(w, 200, map[string]any{"ok": true})
}

// Capture is inert in Plan 1 (real MITM arrives in Plan 5). These stubs keep
// the existing SPA's capture panel from erroring.
func (s *Server) handleCaptureEnable(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, 200, map[string]any{"captureOn": false, "captureUp": false, "restarted": false})
}
func (s *Server) handleCaptureDisable(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, 200, map[string]any{"captureOn": false})
}
func (s *Server) handleCapturesClear(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, 200, map[string]any{"ok": true})
}

// authMiddleware guards the authed route group. It verifies the session cookie,
// rejects missing/invalid auth (and suspended users) with 401/403, and stashes
// the Identity in the request context for downstream handlers.
func (s *Server) authMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		id, ok := s.authedIdentity(r)
		if !ok {
			writeJSON(w, 401, map[string]any{"error": "unauthorized"})
			return
		}
		// Re-check the live user so a suspended user can't keep using an
		// already-issued cookie.
		u, err := s.db.GetUserByUsername(id.Username)
		if err != nil {
			writeJSON(w, 401, map[string]any{"error": "unauthorized"})
			return
		}
		if u.Suspended {
			writeJSON(w, 403, map[string]any{"error": "suspended"})
			return
		}
		next.ServeHTTP(w, r.WithContext(WithIdentity(r.Context(), id)))
	})
}

func writeJSON(w http.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(v)
}

// mustJSON serialises v for use as a WebSocket message payload. Encoding errors
// are impossible for the map[string]any shapes we send, so they are ignored.
func mustJSON(v any) []byte {
	b, _ := json.Marshal(v)
	return b
}
