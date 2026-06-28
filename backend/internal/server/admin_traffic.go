package server

import (
	"net/http"
	"strconv"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/ldm0206/claude-docker/backend/internal/store"
)

// GET /api/admin/traffic?user=&month=
//
// Two modes:
//
//	?user=<id>            → list that user's traffic rows (all months, or one if
//	                        ?month= is also given). Returns []TrafficRow.
//	no user (default)     → aggregate for ?month= (defaults to current month):
//	                        {month, totalRx, totalTx} summed across ALL users.
//
// Rationale (task brief): "Keep it simple" — the no-user aggregate is one
// summed row; the per-user query returns the raw rows so the dashboard can
// render a per-month breakdown.
func (s *Server) handleAdminTraffic(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	month := q.Get("month")
	if month == "" {
		month = time.Now().Format("2006-01")
	}

	if userStr := q.Get("user"); userStr != "" {
		uid, err := strconv.Atoi(userStr)
		if err != nil {
			writeJSON(w, 400, map[string]any{"error": "invalid user id"})
			return
		}
		// Verify the user exists so we don't silently return [] for a typo.
		if _, err := s.db.GetUserByID(uid); err != nil {
			writeJSON(w, 404, map[string]any{"error": "user not found"})
			return
		}
		rows, err := s.db.ListTrafficForUser(uid)
		if err != nil {
			writeJSON(w, 500, map[string]any{"error": "list traffic failed"})
			return
		}
		// Filter by month when requested; otherwise return all rows.
		if q.Get("month") != "" {
			filtered := rows[:0]
			for _, row := range rows {
				if row.YearMonth == month {
					filtered = append(filtered, row)
				}
			}
			rows = filtered
		}
		if rows == nil {
			rows = []store.TrafficRow{}
		}
		writeJSON(w, 200, rows)
		return
	}

	// No user → aggregate across all users for the month.
	rx, tx, err := s.db.SumTrafficByMonth(month)
	if err != nil {
		writeJSON(w, 500, map[string]any{"error": "sum traffic failed"})
		return
	}
	writeJSON(w, 200, map[string]any{
		"month":   month,
		"totalRx": rx,
		"totalTx": tx,
	})
}

// POST /api/admin/users/:id/reset-traffic
//
// Zeroes the current-month traffic row for the user (rx=0, tx=0). The row is
// retained (history-friendly) and re-accumulates as the sampler ticks. Past
// months are untouched. 200 on success (including when no row exists — the
// store's ResetTraffic is a no-op UPDATE in that case). 404 if the user id
// does not resolve.
func (s *Server) handleAdminResetTraffic(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.Atoi(chi.URLParam(r, "id"))
	if err != nil {
		writeJSON(w, 400, map[string]any{"error": "invalid id"})
		return
	}
	if _, err := s.db.GetUserByID(id); err != nil {
		writeJSON(w, 404, map[string]any{"error": "not found"})
		return
	}
	month := time.Now().Format("2006-01")
	if err := s.db.ResetTraffic(id, month); err != nil {
		writeJSON(w, 500, map[string]any{"error": "reset traffic failed"})
		return
	}
	writeJSON(w, 200, map[string]any{"ok": true, "month": month})
}
