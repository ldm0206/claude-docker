package server

import (
	"encoding/json"
	"errors"
	"net/http"
	"strconv"

	"github.com/go-chi/chi/v5"
	"github.com/ldm0206/claude-docker/backend/internal/store"
)

// ---------------------------------------------------------------------------
// Admin role-template CRUD (T7)
// ---------------------------------------------------------------------------
//
// These handlers sit behind requireAdmin in server.Routes(). The store layer
// (store/templates.go) owns the SQL; this file is the JSON <-> struct glue and
// input validation. The Update handler uses the patch-style TemplatePatch so
// omitted fields in the PATCH body leave the column unchanged.

// createTemplateReq is the POST /api/admin/templates body. Permissions is
// optional (defaults to empty string — interpreted as "no explicit grants").
type createTemplateReq struct {
	Name           string `json:"name"`
	DiskQuotaBytes int64  `json:"disk_quota_bytes"`
	CPUQuota       string `json:"cpu_quota"`
	MemoryMaxBytes int64  `json:"memory_max_bytes"`
	MaxSessions    int    `json:"max_sessions"`
	Permissions    string `json:"permissions"`
}

// updateTemplateReq is the PATCH body. Every field is a pointer so we can tell
// "omitted" apart from "set to zero".
type updateTemplateReq struct {
	Name           *string `json:"name,omitempty"`
	DiskQuotaBytes *int64  `json:"disk_quota_bytes,omitempty"`
	CPUQuota       *string `json:"cpu_quota,omitempty"`
	MemoryMaxBytes *int64  `json:"memory_max_bytes,omitempty"`
	MaxSessions    *int    `json:"max_sessions,omitempty"`
	Permissions    *string `json:"permissions,omitempty"`
}

// templateToMap shapes a RoleTemplate for the JSON response. All fields are
// returned (templates are plain metadata, not secrets).
func templateToMap(t store.RoleTemplate) map[string]any {
	return map[string]any{
		"id":               t.ID,
		"name":             t.Name,
		"disk_quota_bytes": t.DiskQuotaBytes,
		"cpu_quota":        t.CPUQuota,
		"memory_max_bytes": t.MemoryMaxBytes,
		"max_sessions":     t.MaxSessions,
		"permissions":      t.Permissions,
		"created_at":       t.CreatedAt,
	}
}

func (s *Server) handleAdminListTemplates(w http.ResponseWriter, _ *http.Request) {
	list, err := s.db.ListTemplates()
	if err != nil {
		writeJSON(w, 500, map[string]any{"error": "list templates failed"})
		return
	}
	out := make([]map[string]any, 0, len(list))
	for _, t := range list {
		out = append(out, templateToMap(t))
	}
	writeJSON(w, 200, out)
}

func (s *Server) handleAdminCreateTemplate(w http.ResponseWriter, r *http.Request) {
	var b createTemplateReq
	if err := json.NewDecoder(r.Body).Decode(&b); err != nil {
		writeJSON(w, 400, map[string]any{"error": "bad body"})
		return
	}
	if b.Name == "" {
		writeJSON(w, 400, map[string]any{"error": "name required"})
		return
	}
	if b.MaxSessions <= 0 {
		writeJSON(w, 400, map[string]any{"error": "max_sessions must be > 0"})
		return
	}
	if b.DiskQuotaBytes < 0 {
		writeJSON(w, 400, map[string]any{"error": "disk_quota_bytes must be >= 0"})
		return
	}
	if b.MemoryMaxBytes < 0 {
		writeJSON(w, 400, map[string]any{"error": "memory_max_bytes must be >= 0"})
		return
	}
	created, err := s.db.CreateTemplate(store.RoleTemplate{
		Name:           b.Name,
		DiskQuotaBytes: b.DiskQuotaBytes,
		CPUQuota:       b.CPUQuota,
		MemoryMaxBytes: b.MemoryMaxBytes,
		MaxSessions:    b.MaxSessions,
		Permissions:    b.Permissions,
	})
	if err != nil {
		// SQLite UNIQUE violation on name → 400 (duplicate). Other → 500.
		if isUniqueViolation(err) {
			writeJSON(w, 400, map[string]any{"error": "template name already exists"})
			return
		}
		writeJSON(w, 500, map[string]any{"error": "create template failed"})
		return
	}
	writeJSON(w, 201, templateToMap(created))
}

func (s *Server) handleAdminUpdateTemplate(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.Atoi(chi.URLParam(r, "id"))
	if err != nil {
		writeJSON(w, 400, map[string]any{"error": "invalid id"})
		return
	}
	var b updateTemplateReq
	if err := json.NewDecoder(r.Body).Decode(&b); err != nil {
		writeJSON(w, 400, map[string]any{"error": "bad body"})
		return
	}
	// Validate provided fields before touching the DB.
	if b.MaxSessions != nil && *b.MaxSessions <= 0 {
		writeJSON(w, 400, map[string]any{"error": "max_sessions must be > 0"})
		return
	}
	if b.DiskQuotaBytes != nil && *b.DiskQuotaBytes < 0 {
		writeJSON(w, 400, map[string]any{"error": "disk_quota_bytes must be >= 0"})
		return
	}
	if b.MemoryMaxBytes != nil && *b.MemoryMaxBytes < 0 {
		writeJSON(w, 400, map[string]any{"error": "memory_max_bytes must be >= 0"})
		return
	}
	patch := store.TemplatePatch{
		Name:           b.Name,
		DiskQuotaBytes: b.DiskQuotaBytes,
		CPUQuota:       b.CPUQuota,
		MemoryMaxBytes: b.MemoryMaxBytes,
		MaxSessions:    b.MaxSessions,
		Permissions:    b.Permissions,
	}
	if err := s.db.UpdateTemplate(id, patch); err != nil {
		if errors.Is(err, store.ErrNotFound) {
			writeJSON(w, 404, map[string]any{"error": "not found"})
			return
		}
		if isUniqueViolation(err) {
			writeJSON(w, 400, map[string]any{"error": "template name already exists"})
			return
		}
		writeJSON(w, 500, map[string]any{"error": "update template failed"})
		return
	}
	updated, err := s.db.GetTemplate(id)
	if err != nil {
		// Unlikely right after a successful update; return a minimal OK.
		writeJSON(w, 200, map[string]any{"ok": true})
		return
	}
	writeJSON(w, 200, templateToMap(updated))
}

func (s *Server) handleAdminDeleteTemplate(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.Atoi(chi.URLParam(r, "id"))
	if err != nil {
		writeJSON(w, 400, map[string]any{"error": "invalid id"})
		return
	}
	// 404 if the row doesn't exist (DeleteTemplate is idempotent on its own,
	// but a missing-id DELETE should tell the caller).
	if _, err := s.db.GetTemplate(id); err != nil {
		if errors.Is(err, store.ErrNotFound) {
			writeJSON(w, 404, map[string]any{"error": "not found"})
			return
		}
		writeJSON(w, 500, map[string]any{"error": "get template failed"})
		return
	}
	if err := s.db.DeleteTemplate(id); err != nil {
		writeJSON(w, 500, map[string]any{"error": "delete template failed"})
		return
	}
	writeJSON(w, 200, map[string]any{"ok": true})
}

// isUniqueViolation reports whether err is a SQLite UNIQUE constraint error.
// modernc.org/sqlite surfaces these as text; we match on the common substring
// rather than depending on driver-specific error types.
func isUniqueViolation(err error) bool {
	if err == nil {
		return false
	}
	msg := err.Error()
	// "UNIQUE constraint failed: ..." is the modernc.org/sqlite phrasing.
	for _, frag := range []string{"UNIQUE constraint", "constraint failed: UNIQUE"} {
		if containsFold(msg, frag) {
			return true
		}
	}
	return false
}

// containsFold is a case-insensitive substring check (avoids pulling in strings
// just for ToLower+Contains at every call site).
func containsFold(s, substr string) bool {
	if len(substr) == 0 {
		return true
	}
	if len(s) < len(substr) {
		return false
	}
	for i := 0; i <= len(s)-len(substr); i++ {
		match := true
		for j := 0; j < len(substr); j++ {
			a, b := s[i+j], substr[j]
			if a >= 'A' && a <= 'Z' {
				a += 'a' - 'A'
			}
			if b >= 'A' && b <= 'Z' {
				b += 'a' - 'A'
			}
			if a != b {
				match = false
				break
			}
		}
		if match {
			return true
		}
	}
	return false
}
