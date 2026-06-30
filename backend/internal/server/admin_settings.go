package server

import (
	"encoding/json"
	"errors"
	"net/http"

	"github.com/ldm0206/claude-docker/backend/internal/store"
)

// templateUserKey is the settings KV key for the panel-selected template user.
const templateUserKey = "template_user"

// resolveTemplateUser returns the effective template username: the DB setting
// if set (non-empty), else cfg.TemplateUser (env CLAUDE_TEMPLATE_USER). Empty
// means the feature is disabled.
func (s *Server) resolveTemplateUser() string {
	if v, err := s.db.GetSetting(templateUserKey); err == nil && v != "" {
		return v
	}
	return s.cfg.TemplateUser
}

// handleAdminGetTemplateUser returns the DB-stored template user (empty when
// unset — env fallback is NOT reported here; the panel only manages the DB
// value).
func (s *Server) handleAdminGetTemplateUser(w http.ResponseWriter, r *http.Request) {
	v, err := s.db.GetSetting(templateUserKey)
	if err != nil {
		writeJSON(w, 500, map[string]any{"error": "get setting"})
		return
	}
	writeJSON(w, 200, map[string]any{"template_user": v})
}

type setTemplateUserReq struct {
	TemplateUser string `json:"template_user"`
}

// handleAdminSetTemplateUser upserts the template user. The value must be
// empty (clears the setting) or an existing user with role 'admin'; anything
// else is a 400.
func (s *Server) handleAdminSetTemplateUser(w http.ResponseWriter, r *http.Request) {
	var b setTemplateUserReq
	if err := json.NewDecoder(r.Body).Decode(&b); err != nil {
		writeJSON(w, 400, map[string]any{"error": "bad body"})
		return
	}
	if b.TemplateUser != "" {
		u, err := s.db.GetUserByUsername(b.TemplateUser)
		if errors.Is(err, store.ErrNotFound) {
			writeJSON(w, 400, map[string]any{"error": "template user must be an existing admin user"})
			return
		}
		if err != nil {
			writeJSON(w, 500, map[string]any{"error": "lookup user"})
			return
		}
		if u.Role != "admin" {
			writeJSON(w, 400, map[string]any{"error": "template user must be an existing admin user"})
			return
		}
	}
	if err := s.db.SetSetting(templateUserKey, b.TemplateUser); err != nil {
		writeJSON(w, 500, map[string]any{"error": "save setting"})
		return
	}
	writeJSON(w, 200, map[string]any{"template_user": b.TemplateUser})
}
