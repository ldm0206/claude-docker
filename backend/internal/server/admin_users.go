package server

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"

	"github.com/go-chi/chi/v5"
	"github.com/ldm0206/claude-docker/backend/internal/auth"
	"github.com/ldm0206/claude-docker/backend/internal/store"
	"github.com/ldm0206/claude-docker/backend/internal/system"
)

// usernameRe matches the same pattern as system.validateUsername.
var usernameRe = system.UsernameRegex()

type createUserReq struct {
	Username string `json:"username"`
	Password string `json:"password"`
	Role     string `json:"role"`
}

func (s *Server) handleAdminCreateUser(w http.ResponseWriter, r *http.Request) {
	var b createUserReq
	if err := json.NewDecoder(r.Body).Decode(&b); err != nil {
		writeJSON(w, 400, map[string]any{"error": "bad body"})
		return
	}
	if b.Username == "" || b.Password == "" || b.Role == "" {
		writeJSON(w, 400, map[string]any{"error": "missing fields"})
		return
	}
	if !usernameRe.MatchString(b.Username) {
		writeJSON(w, 400, map[string]any{"error": "invalid username"})
		return
	}
	if b.Role != "admin" && b.Role != "user" {
		writeJSON(w, 400, map[string]any{"error": "invalid role"})
		return
	}

	hash, err := auth.HashPassword(b.Password)
	if err != nil {
		writeJSON(w, 500, map[string]any{"error": "hash failed"})
		return
	}

	uid, err := s.db.AllocateUID()
	if err != nil {
		writeJSON(w, 500, map[string]any{"error": "allocate uid failed"})
		return
	}

	u, err := s.db.CreateUser(store.User{
		UID:                uid,
		Username:           b.Username,
		PasswordHash:       hash,
		Role:               b.Role,
		MustChangePassword: true,
	})
	if err != nil {
		writeJSON(w, 500, map[string]any{"error": "create user failed"})
		return
	}

	// Provision the Linux account. On failure, roll back the DB row.
	if err := s.provisioner.Create(b.Username, uid); err != nil {
		s.db.Sqlite().Exec("DELETE FROM users WHERE id = ?", u.ID)
		writeJSON(w, 500, map[string]any{"error": "provision failed"})
		return
	}

	writeJSON(w, 201, map[string]any{
		"id":       u.ID,
		"username": u.Username,
		"role":     u.Role,
	})
}

func (s *Server) handleAdminListUsers(w http.ResponseWriter, _ *http.Request) {
	users, err := s.db.ListUsers()
	if err != nil {
		writeJSON(w, 500, map[string]any{"error": "list users failed"})
		return
	}
	out := make([]map[string]any, len(users))
	for i, u := range users {
		out[i] = map[string]any{
			"id":         u.ID,
			"username":   u.Username,
			"role":       u.Role,
			"suspended":  u.Suspended,
		}
	}
	writeJSON(w, 200, out)
}

func (s *Server) handleAdminDeleteUser(w http.ResponseWriter, r *http.Request) {
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
	if err := s.provisioner.Delete(u.Username); err != nil {
		writeJSON(w, 500, map[string]any{"error": "provision delete failed"})
		return
	}
	s.db.Sqlite().Exec("DELETE FROM users WHERE id = ?", id)
	writeJSON(w, 200, map[string]any{"ok": true})
}

func (s *Server) handleAdminSuspendUser(w http.ResponseWriter, r *http.Request) {
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
	if err := s.provisioner.Lock(u.Username); err != nil {
		writeJSON(w, 500, map[string]any{"error": fmt.Sprintf("lock failed: %v", err)})
		return
	}
	if err := s.db.SetSuspended(id, true); err != nil {
		writeJSON(w, 500, map[string]any{"error": "set suspended failed"})
		return
	}
	writeJSON(w, 200, map[string]any{"ok": true})
}

func (s *Server) handleAdminUnsuspendUser(w http.ResponseWriter, r *http.Request) {
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
	if err := s.provisioner.Unlock(u.Username); err != nil {
		writeJSON(w, 500, map[string]any{"error": fmt.Sprintf("unlock failed: %v", err)})
		return
	}
	if err := s.db.SetSuspended(id, false); err != nil {
		writeJSON(w, 500, map[string]any{"error": "set suspended failed"})
		return
	}
	writeJSON(w, 200, map[string]any{"ok": true})
}
