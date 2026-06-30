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
	for i, sess := range sessions {
		m := map[string]any{
			"id":         sess.ID,
			"name":       sess.Name,
			"startedAt":  sess.StartedAt,
			"lastSeenAt": sess.LastSeenAt,
			"alive":      sess.Alive,
		}
		// captureOn is admin-only context for the Captures panel toggle. Regular
		// users never see it (capture is an admin feature).
		if u.Role == "admin" && s.capture != nil {
			m["captureOn"] = s.capture.IsEnabled(sess.ID)
		}
		out[i] = m
	}
	writeJSON(w, 200, out)
}

// handleDeleteSession kills and removes a session owned by the caller.
// Verifies ownership: a LIVE session via username-scoped Get, OR — if the live
// map misses (the row's process is gone, e.g. after a restart) — via the DB
// row's user_id. In the orphan case the row is hard-deleted so the user can
// reclaim session-cap slots from dead sessions. A foreign id 404s either way
// (never leak existence of other users' sessions).
func (s *Server) handleDeleteSession(w http.ResponseWriter, r *http.Request) {
	id, ok := IdentityFrom(r.Context())
	if !ok {
		writeJSON(w, 401, map[string]any{"error": "unauthorized"})
		return
	}

	sid := chi.URLParam(r, "id")

	if _, ok := s.sess.Get(id.Username, sid); ok {
		if err := s.sess.Kill(id.Username, sid); err != nil {
			writeJSON(w, 500, map[string]any{"error": "kill session failed"})
			return
		}
		writeJSON(w, 200, map[string]any{"ok": true})
		return
	}

	// Live map miss: the PTY is gone but the row may still be in the DB. Let
	// the user delete it (hard-delete the row) so dead/orphan sessions stop
	// cluttering the list and — once the startup reap lands — stop holding a
	// cap slot. DeleteOrphan returns ErrNotFound for foreign/absent ids.
	if err := s.sess.DeleteOrphan(id.Username, id.UserID, sid); err != nil {
		writeJSON(w, 404, map[string]any{"error": "not found"})
		return
	}
	writeJSON(w, 200, map[string]any{"ok": true})
}
