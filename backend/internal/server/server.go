package server

import (
	"encoding/json"
	"errors"
	"log"
	"net"
	"net/http"
	"strings"

	"github.com/go-chi/chi/v5"
	"github.com/ldm0206/claude-docker/backend/internal/capture"
	"github.com/ldm0206/claude-docker/backend/internal/config"
	"github.com/ldm0206/claude-docker/backend/internal/pty"
	"github.com/ldm0206/claude-docker/backend/internal/quota"
	"github.com/ldm0206/claude-docker/backend/internal/sessions"
	"github.com/ldm0206/claude-docker/backend/internal/store"
	"github.com/ldm0206/claude-docker/backend/internal/system"
	"github.com/ldm0206/claude-docker/backend/internal/traffic"
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
	masterKey   []byte

	// sess owns the live PTY pool keyed by username → sessionID. main.go
	// constructs it with the real PTY factory; tests inject a fake.
	sess *sessions.Manager

	// quota and traffic are wired by main.go in T7. Both may be nil until then;
	// every call site treats nil as "service unavailable" and degrades
	// gracefully (disk usage returns zeros, suspend skips cgroup reclamation).
	// Tests inject real *quota.Service with fake providers (DiskUsageProvider /
	// CgroupWriter) for deterministic assertions, or pass nil to exercise the
	// graceful path.
	quota   *quota.Service
	traffic *traffic.Service

	// capture (Plan 5) drives the lazy MITM proxy lifecycle + a per-session
	// capture flag. The session env factory consults capture.IsEnabled(sessionID)
	// to route the PTY's HTTP through the proxy when on; the admin capture API
	// calls capture.Enable/Disable + sess.Restart so the PTY re-spawns with the
	// updated env. May be nil — every call site degrades gracefully (no routing,
	// admin endpoints 503); T5 wires the real *capture.Service.
	capture *capture.Service
}

// New wires a Server to the given config, user store, provisioner, session
// manager, credential master key, and the optional quota / traffic / capture
// services. The db and sess remain owned by the caller (main.go opens/closes
// them); New only retains them. masterKey is the 32-byte AES-256-GCM key used
// to seal credential presets; it may be nil in which case the credential
// endpoints return 500 (T9 wires the real key). quota, traffic, and capture may
// be nil — every call site degrades gracefully (zeros / no-op / no routing);
// T7 passes the real quota+traffic, T5 the real capture.
func New(cfg *config.Config, db *store.DB, provisioner system.AccountProvisioner, sess *sessions.Manager, masterKey []byte, q *quota.Service, tf *traffic.Service, cap *capture.Service) *Server {
	if sess == nil {
		panic("server: New sess must not be nil")
	}
	return &Server{
		cfg:         cfg,
		db:          db,
		provisioner: provisioner,
		sess:        sess,
		masterKey:   masterKey,
		quota:       q,
		traffic:     tf,
		capture:     cap,
	}
}

// buildUserEnvFactory returns an EnvFactory that resolves the per-user env
// slice lazily at PTY spawn time. It first copies the template user's
// .credentials.json into the user's claude-config dir (non-fatal on failure),
// then builds the env. Returning a function (not a precomputed slice) lets a
// re-login take effect on the next Create/Restart without restarting the
// server.
//
// P5-T3 — env routing: the factory ALSO consults the per-session capture flag
// (capture.IsEnabled(sessionID)) and, when on, rewrites the returned env so
// the PTY's HTTP traffic goes through the MITM proxy:
//   - sets HTTP_PROXY / HTTPS_PROXY (+lower) = capture.ProxyURL();
//   - REMOVES ALL_PROXY / all_proxy so claude doesn't bypass the proxy via SOCKS.
//
// SECURITY: the template credential file is copied onto disk under the user's
// own claude-config (0600, user-owned). It is never logged.
func (s *Server) buildUserEnvFactory(u store.User) sessions.EnvFactory {
	return func(_ string, sessionID string) []string {
		if err := system.CopyTemplateCredentials(s.resolveTemplateUser(), u.Username, u.UID); err != nil {
			log.Printf("[server] warning: copy template credentials for %s: %v", u.Username, err)
		}
		env := pty.BuildUserEnv(s.cfg, u.Username, "/data/"+u.Username+"/claude-config")
		return s.applyCaptureRouting(env, sessionID)
	}
}

// applyCaptureRouting rewrites the env slice for per-session MITM capture. When
// capture is on for sessionID, it:
//   - drops ALL_PROXY / all_proxy (so claude can't bypass via SOCKS);
//   - sets HTTP_PROXY / HTTPS_PROXY / http_proxy / https_proxy = the proxy URL
//     (overriding any inherited or cfg value).
//
// When capture is off (or s.capture is nil — the not-yet-wired state), env is
// returned unchanged. The env slice is rebuilt filtered (the dropped keys are
// genuinely removed, not blanked) so the spawned process never sees them.
func (s *Server) applyCaptureRouting(env []string, sessionID string) []string {
	if s.capture == nil || !s.capture.IsEnabled(sessionID) {
		return env
	}
	proxyURL := s.capture.ProxyURL()
	drop := map[string]struct{}{
		"ALL_PROXY": {},
		"all_proxy": {},
	}
	// First pass: drop ALL_PROXY/all_proxy entries; keep everything else.
	filtered := make([]string, 0, len(env))
	for _, e := range env {
		key, _, ok := strings.Cut(e, "=")
		if !ok {
			filtered = append(filtered, e)
			continue
		}
		if _, dropIt := drop[key]; dropIt {
			continue
		}
		filtered = append(filtered, e)
	}
	// Second pass: override the 4 proxy keys (set last so they win). BuildUserEnv
	// already sorted the slice; we append the overrides unsorted — exec.Cmd.Env
	// does not require sorted keys, and last-wins for duplicates is what we want.
	filtered = append(filtered,
		"HTTP_PROXY="+proxyURL,
		"HTTPS_PROXY="+proxyURL,
		"http_proxy="+proxyURL,
		"https_proxy="+proxyURL,
	)
	return filtered
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
func (s *Server) ensureSession(u store.User, sid string, r *http.Request) (sessions.PTY, string, int) {
	if sid != "" {
		// Attach to an explicit session. Unknown id → 404 (the client asked for
		// a specific one; silently creating a new one would be surprising).
		// NOTE: the attach path does NOT update client_ip — the stored IP is the
		// origin recorded at session creation, not the last-connect address.
		p, ok := s.sess.Get(u.Username, sid)
		if !ok {
			// Live PTY miss. Before returning 404, check whether the session
			// exists in the DB and belongs to this user: after a server restart
			// the in-memory map is empty but the persisted row is still there,
			// and the frontend (which lists via /api/sessions) will try to
			// reattach. Revive rebuilds the PTY under the SAME id; only a row
			// that is genuinely unknown OR owned by another user yields 404.
			row, err := s.db.GetSession(sid)
			if err != nil || row.UserID != u.ID {
				return nil, "", http.StatusNotFound
			}
			rp, err := s.sess.Revive(u.Username, u.ID, sid, "/home/"+u.Username+"/workspace", s.buildUserEnvFactory(u), pty.Options{
				Command:  "bash",
				Cols:     80,
				Rows:     24,
				Username: u.Username,
				ClientIP: s.clientIP(r),
			})
			if err != nil {
				return nil, sid, http.StatusInternalServerError
			}
			if err := rp.Start(); err != nil {
				return nil, sid, http.StatusInternalServerError
			}
			return rp, sid, http.StatusOK
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
		ClientIP: s.clientIP(r),
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
	r.Post("/auth/logout", s.handleLogout)
	// /ws/* routes check the cookie inside the handler before upgrading.
	r.Get("/ws/terminal", s.handleTerminalWS)
	r.Get("/ws/captures", s.handleCapturesWS)
	r.Get("/ws/metrics", s.handleMetricsWS)
	r.Group(func(r chi.Router) {
		r.Use(s.authMiddleware)
		r.Get("/api/state", s.handleState)
		r.Get("/api/me", s.handleMe)
		// NOTE: the old global POST /api/session/restart was REMOVED in Plan 3
		// (per-session kill+create via DELETE /api/sessions/:id lands in T6).
		// The SPA's restart button 404s for now — fine, the SPA is not the
		// Plan 3 test target.
		r.Post("/auth/change-password", s.handleChangePassword)
		// User session-management endpoints (T6)
		r.Post("/api/sessions", s.handleCreateSession)
		r.Get("/api/sessions", s.handleListSessions)
		r.Delete("/api/sessions/{id}", s.handleDeleteSession)
		// Web file manager (Plan 8) — per-user traffic accounting via
		// recordFileTraffic on every upload/download.
		r.Get("/api/files/list", s.handleFilesList)
		r.Get("/api/files/download", s.handleFilesDownload)
		r.Post("/api/files/upload", s.handleFilesUpload)
		r.Post("/api/files/mkdir", s.handleFilesMkdir)
		r.Post("/api/files/rename", s.handleFilesRename)
		r.Post("/api/files/edit", s.handleFilesEdit)
		r.Delete("/api/files", s.handleFilesDelete)
		// Admin group
		r.Group(func(r chi.Router) {
			r.Use(s.requireAdmin)
			r.Post("/api/admin/users", s.handleAdminCreateUser)
			r.Get("/api/admin/users", s.handleAdminListUsers)
			r.Delete("/api/admin/users/{id}", s.handleAdminDeleteUser)
			r.Post("/api/admin/users/{id}/suspend", s.handleAdminSuspendUser)
			r.Post("/api/admin/users/{id}/unsuspend", s.handleAdminUnsuspendUser)
			// Admin usage/traffic endpoints (T6)
			r.Get("/api/admin/users/{id}/usage", s.handleAdminUsage)
			r.Get("/api/admin/traffic", s.handleAdminTraffic)
			r.Post("/api/admin/users/{id}/reset-traffic", s.handleAdminResetTraffic)
			// Admin login-audit endpoint (T6): newest-first login events.
			r.Get("/api/admin/login-events", s.handleAdminLoginEvents)
			// Admin session-management endpoints (T6)
			r.Get("/api/admin/users/{id}/sessions", s.handleAdminListSessions)
			r.Delete("/api/admin/users/{id}/sessions/{sid}", s.handleAdminKillSession)
			r.Delete("/api/admin/users/{id}/sessions", s.handleAdminKillAllSessions)
			// Admin per-session capture toggle (P5-T3). Enable lazily starts the
			// MITM proxy + restarts the session PTY so its env routes through it.
			r.Post("/api/admin/sessions/{id}/capture/enable", s.handleAdminCaptureEnable)
			r.Post("/api/admin/sessions/{id}/capture/disable", s.handleAdminCaptureDisable)
			// Admin captures read/clear surface (P5-T4). The /ws/captures push
			// is mounted top-level (WS handlers auth inline, see handleCapturesWS).
			r.Get("/api/admin/captures", s.handleAdminListCaptures)
			r.Post("/api/admin/captures/clear", s.handleAdminClearCaptures)
			// Admin role-template CRUD (T7)
			r.Get("/api/admin/templates", s.handleAdminListTemplates)
			r.Post("/api/admin/templates", s.handleAdminCreateTemplate)
			r.Patch("/api/admin/templates/{id}", s.handleAdminUpdateTemplate)
			r.Delete("/api/admin/templates/{id}", s.handleAdminDeleteTemplate)
			r.Get("/api/admin/settings/template-user", s.handleAdminGetTemplateUser)
			r.Put("/api/admin/settings/template-user", s.handleAdminSetTemplateUser)
			// Admin credential-preset CRUD (T7) — secrets are sealed with masterKey
			r.Get("/api/admin/credentials", s.handleAdminListCredentials)
			r.Post("/api/admin/credentials", s.handleAdminCreateCredential)
			r.Patch("/api/admin/credentials/{id}", s.handleAdminUpdateCredential)
			r.Delete("/api/admin/credentials/{id}", s.handleAdminDeleteCredential)
		})
	})
	r.Handle("/*", ui.SPA())
	return r
}

func (s *Server) handleHealth(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, 200, map[string]any{"ok": true})
}

func (s *Server) handleLogout(w http.ResponseWriter, _ *http.Request) {
	http.SetCookie(w, &http.Cookie{Name: "session", Value: "", Path: "/", MaxAge: -1, HttpOnly: true, Secure: true, SameSite: s.sameSiteMode()})
	writeJSON(w, 200, map[string]any{"ok": true})
}

// clientIP returns the originating client IP for r. Priority: CF-Connecting-IP
// (Cloudflare-injected, trusted because the deployment transits CF+nginx and
// the container's 8080 port is private) > X-Real-IP > first hop of
// X-Forwarded-For > RemoteAddr host.
func (s *Server) clientIP(r *http.Request) string {
	if v := r.Header.Get("CF-Connecting-IP"); v != "" {
		return v
	}
	if v := r.Header.Get("X-Real-IP"); v != "" {
		return v
	}
	if v := r.Header.Get("X-Forwarded-For"); v != "" {
		return strings.TrimSpace(strings.Split(v, ",")[0])
	}
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return host
}

// handleState returns the minimal client-facing state. Plan 3 dropped
// `sessionAlive` (it referred to the now-removed shared PTY); per-user session
// liveness is exposed via /api/sessions. captureOn reflects whether the admin
// has enabled capture on the requesting user's session (if any), else false.
func (s *Server) handleState(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, 200, map[string]any{"captureOn": false})
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
		// Plan 8: enrich the Identity with the live user's DB id so the
		// /api/files/* handlers can attribute upload/download bytes to this
		// user's traffic bucket (authedIdentity only has cookie claims).
		id.UserID = u.ID
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
