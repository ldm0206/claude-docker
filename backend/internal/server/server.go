package server

import (
	"encoding/json"
	"net/http"
	"sync/atomic"

	"github.com/go-chi/chi/v5"
	"github.com/ldm0206/claude-docker/backend/internal/config"
	"github.com/ldm0206/claude-docker/backend/internal/pty"
	"github.com/ldm0206/claude-docker/backend/internal/store"
	"github.com/ldm0206/claude-docker/backend/internal/system"
	"github.com/ldm0206/claude-docker/backend/internal/ui"
)

type Server struct {
	cfg        *config.Config
	db         *store.DB
	restarting atomic.Bool
	pty        *pty.Manager
	provisioner system.AccountProvisioner
}

// New builds a Server wired to the given config, user store, and account
// provisioner. The store remains owned by the caller (main.go opens/closes it);
// New only retains it.
func New(cfg *config.Config, db *store.DB, provisioner system.AccountProvisioner) *Server {
	p := pty.New(pty.Options{
		Cwd:     "/workspace",
		Env:     func() []string { return pty.BuildClaudeEnv(cfg) },
		Command: "bash",
	})
	return &Server{cfg: cfg, db: db, pty: p, provisioner: provisioner}
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
