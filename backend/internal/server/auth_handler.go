package server

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"time"

	"github.com/ldm0206/claude-docker/backend/internal/auth"
	"github.com/ldm0206/claude-docker/backend/internal/store"
)

type ctxKey int

const identityKey ctxKey = 0

// Identity is the authenticated principal stashed in the request context by
// authMiddleware (and returned by authedIdentity for routes that check the
// cookie themselves, e.g. the WebSocket handlers).
type Identity struct {
	Username string
	Role     string
}

// WithIdentity returns ctx with id embedded under identityKey.
func WithIdentity(ctx context.Context, id Identity) context.Context {
	return context.WithValue(ctx, identityKey, id)
}

// IdentityFrom extracts the Identity previously stashed in ctx, if any.
func IdentityFrom(ctx context.Context) (Identity, bool) {
	v, ok := ctx.Value(identityKey).(Identity)
	return v, ok
}

type loginReq struct {
	Username string `json:"username"`
	Password string `json:"password"`
}

func (s *Server) handleLogin(w http.ResponseWriter, r *http.Request) {
	var b loginReq
	if err := json.NewDecoder(r.Body).Decode(&b); err != nil {
		writeJSON(w, 400, map[string]any{"error": "bad body"})
		return
	}
	u, err := s.db.GetUserByUsername(b.Username)
	if errors.Is(err, store.ErrNotFound) || !auth.CheckPassword(b.Password, u.PasswordHash) {
		writeJSON(w, 401, map[string]any{"error": "unauthorized"})
		return
	}
	if u.Suspended {
		writeJSON(w, 403, map[string]any{"error": "suspended"})
		return
	}
	_ = s.db.TouchLogin(u.ID, time.Now().Unix())
	// Plan 2: a single shared PTY runs as the logged-in user via gosu. Capture
	// the identity NOW so the next lazy Start() (terminal WS or restart) spawns
	// `gosu <username> bash -l` with BuildUserEnv env. A live PTY keeps the
	// previous user until the next restart — acceptable for Plan 2.
	s.setCurrentUser(u.Username, u.UID, "/data/"+u.Username+"/claude-config")
	cookie, err := auth.SignSession(map[string]any{
		"username": u.Username,
		"role":     u.Role,
		"iat":      time.Now().Unix(),
	}, s.cfg.SessionSecret)
	if err != nil {
		writeJSON(w, 500, map[string]any{"error": "sign failed"})
		return
	}
	http.SetCookie(w, &http.Cookie{
		Name:     "session",
		Value:    cookie,
		Path:     "/",
		HttpOnly: true,
		SameSite: http.SameSiteLaxMode,
	})
	writeJSON(w, 200, map[string]any{"role": u.Role, "mustChangePassword": u.MustChangePassword})
}

type changePwReq struct {
	NewPassword string `json:"newPassword"`
}

func (s *Server) handleChangePassword(w http.ResponseWriter, r *http.Request) {
	id, _ := IdentityFrom(r.Context())
	u, err := s.db.GetUserByUsername(id.Username)
	if err != nil {
		writeJSON(w, 500, map[string]any{"error": "user gone"})
		return
	}
	var b changePwReq
	if err := json.NewDecoder(r.Body).Decode(&b); err != nil || b.NewPassword == "" {
		writeJSON(w, 400, map[string]any{"error": "bad body"})
		return
	}
	hash, err := auth.HashPassword(b.NewPassword)
	if err != nil {
		writeJSON(w, 500, map[string]any{"error": "hash failed"})
		return
	}
	if err := s.db.SetPassword(u.ID, hash); err != nil {
		writeJSON(w, 500, map[string]any{"error": "persist failed"})
		return
	}
	writeJSON(w, 200, map[string]any{"ok": true})
}

// authedIdentity verifies the session cookie and returns the Identity it
// represents. Used by routes (e.g. WebSocket handlers) that check auth inline
// rather than going through authMiddleware.
func (s *Server) authedIdentity(r *http.Request) (Identity, bool) {
	c, err := r.Cookie("session")
	if err != nil {
		return Identity{}, false
	}
	claims, ok := auth.VerifySession(c.Value, s.cfg.SessionSecret)
	if !ok {
		return Identity{}, false
	}
	uname, _ := claims["username"].(string)
	role, _ := claims["role"].(string)
	if uname == "" {
		return Identity{}, false
	}
	return Identity{Username: uname, Role: role}, true
}

// requireAdmin is an admin-gate middleware (Task 8 mounts it on /api/admin/*).
func (s *Server) requireAdmin(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		id, ok := IdentityFrom(r.Context())
		if !ok || id.Role != "admin" {
			writeJSON(w, 403, map[string]any{"error": "forbidden"})
			return
		}
		next.ServeHTTP(w, r)
	})
}
