package server

import (
	"encoding/json"
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/ldm0206/claude-docker/backend/internal/auth"
	"github.com/ldm0206/claude-docker/backend/internal/config"
	"github.com/ldm0206/claude-docker/backend/internal/pty"
)

type Server struct {
	cfg *config.Config
	pty *pty.Manager
}

func New(cfg *config.Config) *Server {
	p := pty.New(pty.Options{
		Cwd:     "/workspace",
		Env:     func() []string { return pty.BuildClaudeEnv(cfg) },
		Command: "bash",
	})
	return &Server{cfg: cfg, pty: p}
}

func (s *Server) PTY() *pty.Manager { return s.pty }

func (s *Server) Routes() http.Handler {
	r := chi.NewRouter()
	r.Get("/health", s.handleHealth)
	r.Post("/auth", s.handleAuth)
	r.Post("/logout", s.handleLogout)
	// /ws/* routes check the cookie inside the handler before upgrading.
	r.Get("/ws/terminal", s.handleTerminalWS)
	r.Get("/ws/captures", s.handleCapturesWS)
	r.Get("/ws/metrics", s.handleMetricsWS)
	r.Group(func(r chi.Router) {
		r.Use(s.authMiddleware)
		r.Get("/api/state", s.handleState)
		r.Post("/api/session/restart", s.handleRestart)
		r.Post("/api/capture/enable", s.handleCaptureEnable)
		r.Post("/api/capture/disable", s.handleCaptureDisable)
		r.Post("/api/captures/clear", s.handleCapturesClear)
	})
	return r
}

func (s *Server) handleHealth(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, 200, map[string]any{"ok": true})
}

type authReq struct{ Key string }

func (s *Server) handleAuth(w http.ResponseWriter, r *http.Request) {
	var body authReq
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeJSON(w, 400, map[string]any{"error": "bad body"})
		return
	}
	if !auth.EqualString(body.Key, s.cfg.AccessKey) {
		writeJSON(w, 401, map[string]any{"error": "unauthorized"})
		return
	}
	cookie, err := auth.SignSession(map[string]any{"iat": time.Now().Unix()}, s.cfg.SessionSecret)
	if err != nil {
		writeJSON(w, 500, map[string]any{"error": "sign failed"})
		return
	}
	http.SetCookie(w, &http.Cookie{
		Name: "session", Value: cookie, Path: "/",
		HttpOnly: true, SameSite: http.SameSiteLaxMode,
	})
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

func (s *Server) authed(r *http.Request) bool {
	c, err := r.Cookie("session")
	if err != nil {
		return false
	}
	_, ok := auth.VerifySession(c.Value, s.cfg.SessionSecret)
	return ok
}

func (s *Server) authMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !s.authed(r) {
			writeJSON(w, 401, map[string]any{"error": "unauthorized"})
			return
		}
		next.ServeHTTP(w, r)
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
