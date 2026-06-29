package server

import (
	"net/http"
	"strconv"

	"github.com/go-chi/chi/v5"
	"github.com/ldm0206/claude-docker/backend/internal/store"
)

// handleAdminListSessions lists all sessions for a given user (by user id).
// Admin-only. 200 [{id, name, startedAt, lastSeenAt, alive}, ...]
func (s *Server) handleAdminListSessions(w http.ResponseWriter, r *http.Request) {
	uid, err := strconv.Atoi(chi.URLParam(r, "id"))
	if err != nil {
		writeJSON(w, 400, map[string]any{"error": "invalid user id"})
		return
	}

	// Resolve username from user-id (admin endpoints identify users by id).
	u, err := s.db.GetUserByID(uid)
	if err != nil {
		writeJSON(w, 404, map[string]any{"error": "user not found"})
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
			"clientIp":   s.ClientIP,
		}
	}
	writeJSON(w, 200, out)
}

// handleAdminKillSession kills a specific session for a user.
// Admin-only. Resolves username from user-id, then calls sess.Kill.
func (s *Server) handleAdminKillSession(w http.ResponseWriter, r *http.Request) {
	uid, err := strconv.Atoi(chi.URLParam(r, "id"))
	if err != nil {
		writeJSON(w, 400, map[string]any{"error": "invalid user id"})
		return
	}

	u, err := s.db.GetUserByID(uid)
	if err != nil {
		writeJSON(w, 404, map[string]any{"error": "user not found"})
		return
	}

	sid := chi.URLParam(r, "sid")
	if err := s.sess.Kill(u.Username, sid); err != nil {
		writeJSON(w, 404, map[string]any{"error": "session not found"})
		return
	}

	writeJSON(w, 200, map[string]any{"ok": true})
}

// handleAdminKillAllSessions kills all live sessions for a user.
// Admin-only. Used by suspend/delete to reclaim resources.
func (s *Server) handleAdminKillAllSessions(w http.ResponseWriter, r *http.Request) {
	uid, err := strconv.Atoi(chi.URLParam(r, "id"))
	if err != nil {
		writeJSON(w, 400, map[string]any{"error": "invalid user id"})
		return
	}

	u, err := s.db.GetUserByID(uid)
	if err != nil {
		writeJSON(w, 404, map[string]any{"error": "user not found"})
		return
	}

	if err := s.sess.KillAll(u.Username); err != nil {
		writeJSON(w, 500, map[string]any{"error": "kill all sessions failed"})
		return
	}

	writeJSON(w, 200, map[string]any{"ok": true})
}

// Compile-time guard: admin_sessions.go uses store.User (from the db lookup).
var _ store.User
