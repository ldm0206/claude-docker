package server

import (
	"errors"
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/ldm0206/claude-docker/backend/internal/store"
)

// ---------------------------------------------------------------------------
// P5-T3: admin per-session capture enable/disable API
// ---------------------------------------------------------------------------
//
// POST /api/admin/sessions/:id/capture/enable
// POST /api/admin/sessions/:id/capture/disable
//
// These sit behind requireAdmin (mounted in server.Routes). They:
//  1. Resolve the session's owning user from the :id (db.GetSession →
//     db.GetUserByID) so the capture flag is bound to a real user. An unknown
//     session id → 404.
//  2. enable: capture.Enable(sessionID, userID) lazily starts the MITM proxy
//     (the first enable brings it up; subsequent enables reuse it). If Start
//     fails the flag is rolled back and the API returns 500. On success the
//     session's PTY is Restarted so its env factory re-reads the (now-on) flag
//     and routes the new process through the proxy. If the PTY isn't live
//     (already exited) Restart is a no-op ErrNotFound — the flag was set, the
//     next WS attach will pick it up.
//  3. disable: capture.Disable(sessionID) + Restart (env now omits the proxy).
//
// Response shapes:
//   enable  200: {captureOn:true,  captureUp:<runner.Running()>, restarted:<bool>}
//   enable  500: {captureOn:false, captureUp:false,               restarted:false, error:"..."}
//   disable 200: {captureOn:false}
//
// capture==nil (T5 not yet wired) → both endpoints return 503 (service
// unavailable); the admin sees a clean message rather than a panic.

// handleAdminCaptureEnable turns on per-session capture and restarts the
// session's PTY so its env routes through the MITM proxy.
func (s *Server) handleAdminCaptureEnable(w http.ResponseWriter, r *http.Request) {
	if s.capture == nil {
		writeJSON(w, 503, map[string]any{"error": "capture service unavailable"})
		return
	}
	sid := chi.URLParam(r, "id")
	session, err := s.db.GetSession(sid)
	if err != nil {
		// Unknown session id — store.GetSession returns store.ErrNotFound for
		// absent rows; anything else is a DB failure. Treat ErrNotFound as 404
		// (the admin's session id is wrong/orphaned); other errors as 500.
		if errors.Is(err, store.ErrNotFound) {
			writeJSON(w, 404, map[string]any{"error": "session not found"})
			return
		}
		writeJSON(w, 500, map[string]any{"error": "lookup session failed"})
		return
	}
	user, err := s.db.GetUserByID(session.UserID)
	if err != nil {
		// Session row exists but the user was deleted — treat as 404 (the
		// session is orphaned; there is no PTY to restart).
		writeJSON(w, 404, map[string]any{"error": "session user not found"})
		return
	}

	if err := s.capture.Enable(sid, user.ID); err != nil {
		// Proxy failed to come up — flag was rolled back by capture.Enable.
		// Do NOT restart the PTY (the env would route into a dead proxy).
		writeJSON(w, 500, map[string]any{
			"captureOn": false,
			"captureUp": false,
			"restarted": false,
			"error":     err.Error(),
		})
		return
	}

	// Restart the live PTY so the lazy env factory re-reads the flag and the
	// new process routes through the proxy. If the PTY is no longer live
	// (already exited between lift-off and here), Restart returns ErrNotFound —
	// the flag is set, the session row stays alive=1, the next WS attach will
	// re-spawn under the env factory and pick up the flag. We surface
	// restarted=false but keep captureOn:true (the toggle succeeded).
	restarted := true
	if err := s.sess.Restart(user.Username, sid); err != nil {
		restarted = false
	}
	writeJSON(w, 200, map[string]any{
		"captureOn": true,
		"captureUp": s.capture.IsAnyEnabled(),
		"restarted": restarted,
	})
}

// handleAdminCaptureDisable turns off per-session capture and restarts the
// session's PTY so its env omits the proxy.
func (s *Server) handleAdminCaptureDisable(w http.ResponseWriter, r *http.Request) {
	if s.capture == nil {
		writeJSON(w, 503, map[string]any{"error": "capture service unavailable"})
		return
	}
	sid := chi.URLParam(r, "id")
	session, err := s.db.GetSession(sid)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			writeJSON(w, 404, map[string]any{"error": "session not found"})
			return
		}
		writeJSON(w, 500, map[string]any{"error": "lookup session failed"})
		return
	}
	user, err := s.db.GetUserByID(session.UserID)
	if err != nil {
		writeJSON(w, 404, map[string]any{"error": "session user not found"})
		return
	}

	s.capture.Disable(sid)
	// Restart so the env factory re-reads the (now-off) flag and the new process
	// stops routing through the proxy. Best-effort: a non-live PTY (ErrNotFound)
	// is fine — the next WS attach will spawn without the proxy env.
	_ = s.sess.Restart(user.Username, sid)
	writeJSON(w, 200, map[string]any{"captureOn": false})
}
