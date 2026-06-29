package server

import (
	"net/http"
	"strconv"
)

// handleAdminLoginEvents returns the most recent login attempts (newest-first).
// Admin-only. ?limit=N (default 100, capped 500 by the store).
func (s *Server) handleAdminLoginEvents(w http.ResponseWriter, r *http.Request) {
	limit := 0
	if v := r.URL.Query().Get("limit"); v != "" {
		if n, err := strconv.Atoi(v); err == nil {
			limit = n
		}
	}
	events, err := s.db.ListLoginEvents(limit)
	if err != nil {
		writeJSON(w, 500, map[string]any{"error": "list login events failed"})
		return
	}
	out := make([]map[string]any, len(events))
	for i, e := range events {
		out[i] = map[string]any{
			"id":        e.ID,
			"userId":    e.UserID,
			"username":  e.Username,
			"ip":        e.IP,
			"userAgent": e.UserAgent,
			"success":   e.Success,
			"at":        e.At,
		}
	}
	writeJSON(w, 200, out)
}
