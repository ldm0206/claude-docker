package server

import (
	"net/http"
	"strconv"
	"time"

	"github.com/go-chi/chi/v5"
)

// GET /api/admin/users/:id/usage
//
// Returns a composite usage snapshot for the admin dashboard:
//
//	disk:     {used, limit, over}  — limit from db.EffectiveDiskQuota;
//	          used/over from quota.Service.CheckDisk (zeros+false when quota nil
//	          or when CheckDisk errors — disk errors are NOT surfaced to the
//	          admin; the dashboard just shows 0/limit).
//	traffic:  {rx, tx, month}      — rx/tx = db.SumTrafficForUser across all
//	          months; month = current "YYYY-MM".
//	sessions: {alive, total}       — alive = db.CountAliveSessionsForUser;
//	          total = len(db.ListSessionsForUser).
//
// 404 if the user id does not resolve to a row. The route is mounted under
// requireAdmin so non-admins get 403 before reaching here.
func (s *Server) handleAdminUsage(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.Atoi(chi.URLParam(r, "id"))
	if err != nil {
		writeJSON(w, 400, map[string]any{"error": "invalid id"})
		return
	}
	u, err := s.db.GetUserByID(id)
	if err != nil {
		writeJSON(w, 404, map[string]any{"error": "not found"})
		return
	}

	// --- disk ---
	limit, err := s.db.EffectiveDiskQuota(id)
	if err != nil {
		// A store error here is unexpected but not fatal to the rest of the
		// snapshot — fall back to "no limit" (0) rather than 500-ing the whole
		// endpoint. The admin still gets traffic + sessions.
		limit = 0
	}
	var used int64
	var over bool
	if s.quota != nil {
		used, over, err = s.quota.CheckDisk(u.Username, limit)
		if err != nil {
			// du / provider error — degrade to zeros, do not fail the response.
			used, over = 0, false
		}
	}

	// --- traffic (all months) ---
	rx, tx, _ := s.db.SumTrafficForUser(id)
	month := time.Now().Format("2006-01")

	// --- sessions ---
	alive, _ := s.db.CountAliveSessionsForUser(id)
	all, _ := s.db.ListSessionsForUser(id)
	total := len(all)

	writeJSON(w, 200, map[string]any{
		"disk": map[string]any{
			"used":  used,
			"limit": limit,
			"over":  over,
		},
		"traffic": map[string]any{
			"rx":    rx,
			"tx":    tx,
			"month": month,
		},
		"sessions": map[string]any{
			"alive": alive,
			"total": total,
		},
	})
}
