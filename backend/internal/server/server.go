package server

import (
	"encoding/json"
	"errors"
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/ldm0206/claude-docker/backend/internal/config"
	"github.com/ldm0206/claude-docker/backend/internal/pty"
	"github.com/ldm0206/claude-docker/backend/internal/sessions"
	"github.com/ldm0206/claude-docker/backend/internal/store"
	"github.com/ldm0206/claude-docker/backend/internal/system"
	"github.com/ldm0206/claude-docker/backend/internal/ui"
)

// Server is the HTTP backend. Plan 3 replaced Plan 2's single shared PTY
// (currentUser + *pty.Manager) with a per-user, multi-session pool owned by
// *sessions.Manager. Sessions are created lazily from /ws/terminal when the
// client omits ?session=<id>, and attached by id when it supplies one.
type Server struct {
	cfg         *config.Config
	db          *store.DB
	provisioner system.AccountProvisioner

	// sess owns the live PTY pool keyed by username → sessionID. main.go
	// constructs it with the real PTY factory; tests inject a fake.
	sess *sessions.Manager
}

// New wires a Server to the given config, user store, provisioner, and session
// manager. The db and sess remain owned by the caller (main.go opens/closes
// them); New only retains them.
func New(cfg *config.Config, db *store.DB, provisioner system.AccountProvisioner, sess *sessions.Manager) *Server {
	if sess == nil {
		panic("server: New sess must not be nil")
	}
	return &Server{cfg: cfg, db: db, provisioner: provisioner, sess: sess}
}

// buildUserEnvFactory returns an EnvFactory that resolves the per-user env
// slice lazily at PTY spawn time. credEnv is nil here — Plan 3 / T8 fills the
// decrypted credential once MASTER_KEY wiring lands. Returning a function
// (not a precomputed slice) lets credential rotation take effect on the next
// Create without restarting the server.
func (s *Server) buildUserEnvFactory(u store.User) sessions.EnvFactory {
	return func(_ string) []string {
		return pty.BuildUserEnv(s.cfg, u.Username, "/data/"+u.Username+"/claude-config", nil)
	}
}

// ensureSession is the core of /ws/terminal: given a live user and an optional
// session id, it returns the PTY the WS handler should drive, the effective
// session id (newly-minted on the create path), and an HTTP status (200 / 404
// for an unknown explicit id / 409 when the per-user cap is reached).
//
// On the create path it lazy-starts the PTY BEFORE returning so the caller can
// subscribe OnData without missing the first emitted bytes. On the attach path
// it lazy-restarts a dead PTY (process exited between reconnects).
//
// The WS handler is intentionally thin: this method is unit-testable without a
// real WebSocket dial (see server_test.go's ensureSession tests).
func (s *Server) ensureSession(u store.User, sid string) (sessions.PTY, string, int) {
	if sid != "" {
		// Attach to an explicit session. Unknown id → 404 (the client asked for
		// a specific one; silently creating a new one would be surprising).
		p, ok := s.sess.Get(u.Username, sid)
		if !ok {
			return nil, "", http.StatusNotFound
		}
		if !p.Alive() {
			// Lazy restart: process exited while the WS was disconnected.
			if err := p.Start(); err != nil {
				return nil, sid, http.StatusInternalServerError
			}
		}
		return p, sid, http.StatusOK
	}

	// Create path: spin up a fresh session for this user.
	envFactory := s.buildUserEnvFactory(u)
	opts := pty.Options{
		Command:  "bash",
		Cols:     80,
		Rows:     24,
		Username: u.Username,
	}
	newSID, p, err := s.sess.Create(u.Username, u.ID, "/home/"+u.Username+"/workspace", envFactory, opts)
	if err != nil {
		if errors.Is(err, sessions.ErrSessionCapReached) {
			return nil, "", http.StatusConflict
		}
		return nil, "", http.StatusInternalServerError
	}
	// Lazy-start BEFORE the caller subscribes OnData so the first bytes emitted
	// by the shell are not lost (matches the pre-Plan-3 single-PTY behavior).
	if err := p.Start(); err != nil {
		return nil, "", http.StatusInternalServerError
	}
	return p, newSID, http.StatusOK
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
		// NOTE: the old global POST /api/session/restart was REMOVED in Plan 3
		// (per-session kill+create via DELETE /api/sessions/:id lands in T6).
		// The SPA's restart button 404s for now — fine, the SPA is not the
		// Plan 3 test target.
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

// handleState returns the minimal client-facing state. Plan 3 dropped
// `sessionAlive` (it referred to the now-removed shared PTY); per-user session
// liveness is exposed via /api/sessions in T6. captureOn stays false until the
// real MITM capture lands in Plan 5.
func (s *Server) handleState(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, 200, map[string]any{"captureOn": false})
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
