package server

import (
	"encoding/json"
	"errors"
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/ldm0206/claude-docker/backend/internal/pty"
	"github.com/ldm0206/claude-docker/backend/internal/sessions"
)

// createSessionReq is the JSON body for POST /api/sessions.
type createSessionReq struct {
	Name string `json:"name"`
}

// handleCreateSession creates a new terminal session for the authenticated user.
// On success: 201 {id, name}. On cap reached: 409. On other error: 500.
func (s *Server) handleCreateSession(w http.ResponseWriter, r *http.Request) {
	id, ok := IdentityFrom(r.Context())
	if !ok {
		writeJSON(w, 401, map[string]any{"error": "unauthorized"})
		return
	}
	u, err := s.db.GetUserByUsername(id.Username)
	if err != nil {
		writeJSON(w, 401, map[string]any{"error": "unauthorized"})
		return
	}

	var b createSessionReq
	if err := json.NewDecoder(r.Body).Decode(&b); err != nil {
		writeJSON(w, 400, map[string]any{"error": "bad body"})
		return
	}

	envFactory := s.buildUserEnvFactory(u)
	opts := pty.Options{
		Command:  "bash",
		Cols:     80,
		Rows:     24,
		Username: u.Username,
		ClientIP: s.clientIP(r),
	}

	sid, p, err := s.sess.Create(u.Username, u.ID, "/home/"+u.Username+"/workspace", envFactory, opts)
	if err != nil {
		if errors.Is(err, sessions.ErrSessionCapReached) {
			writeJSON(w, 409, map[string]any{"error": "session cap reached"})
			return
		}
		writeJSON(w, 500, map[string]any{"error": "create session failed"})
		return
	}

	// If a custom name was provided, update the session row's name field.
	// The manager.Create uses opts.Username as the default name; we override
	// it here with the user-supplied name while keeping opts.Username for the
	// PTY spawn (gosu needs the real Linux username).
	name := u.Username // default
	if b.Name != "" {
		name = b.Name
		_ = s.db.UpdateSessionName(sid, b.Name)
	}

	// Lazy-start the PTY so the first bytes are not lost.
	if err := p.Start(); err != nil {
		writeJSON(w, 500, map[string]any{"error": "start session failed"})
		return
	}

	writeJSON(w, 201, map[string]any{"id": sid, "name": name})
}

// handleListSessions returns the authenticated user's sessions.
// 200 [{id, name, startedAt, lastSeenAt, alive}, ...]
func (s *Server) handleListSessions(w http.ResponseWriter, r *http.Request) {
	id, ok := IdentityFrom(r.Context())
	if !ok {
		writeJSON(w, 401, map[string]any{"error": "unauthorized"})
		return
	}
	u, err := s.db.GetUserByUsername(id.Username)
	if err != nil {
		writeJSON(w, 401, map[string]any{"error": "unauthorized"})
		return
	}

	sessions, err := s.sess.List(u.ID)
	if err != nil {
		writeJSON(w, 500, map[string]any{"error": "list sessions failed"})
		return
	}

	out := make([]map[string]any, len(sessions))
	for i, s := range sessions {
		out[i] = map[string]any{
			"id":         s.ID,
			"name":       s.Name,
			"startedAt":  s.StartedAt,
			"lastSeenAt": s.LastSeenAt,
			"alive":      s.Alive,
		}
	}
	writeJSON(w, 200, out)
}

// handleDeleteSession kills and removes a session owned by the caller.
// Verifies the session belongs to the caller (username-scoped Get); if not → 404
// (don't leak existence of other users' sessions).
func (s *Server) handleDeleteSession(w http.ResponseWriter, r *http.Request) {
	id, ok := IdentityFrom(r.Context())
	if !ok {
		writeJSON(w, 401, map[string]any{"error": "unauthorized"})
		return
	}

	sid := chi.URLParam(r, "id")

	// Username-scoped check: Get(username, sid) must hit. This prevents user A
	// from deleting user B's session — they get 404 rather than revealing the
	// session exists.
	if _, ok := s.sess.Get(id.Username, sid); !ok {
		writeJSON(w, 404, map[string]any{"error": "not found"})
		return
	}

	if err := s.sess.Kill(id.Username, sid); err != nil {
		writeJSON(w, 500, map[string]any{"error": "kill session failed"})
		return
	}

	writeJSON(w, 200, map[string]any{"ok": true})
}
