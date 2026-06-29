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
//
// UserID is populated ONLY by authMiddleware (which re-fetches the live user);
// authedIdentity returns claims straight from the cookie and leaves it zero.
// The /api/files/* REST handlers go through authMiddleware and use it for
// per-user traffic accounting (Plan 8). WS handlers do not need it.
type Identity struct {
	Username string
	Role     string
	UserID   int
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
	ip := s.clientIP(r)
	ua := truncateUA(r.Header.Get("User-Agent"))
	now := time.Now().Unix()
	u, err := s.db.GetUserByUsername(b.Username)
	if errors.Is(err, store.ErrNotFound) {
		// Missing user: run a decoy argon2id verify so this path takes the
		// same time as a wrong-password path, defeating user enumeration via
		// response timing. Result is discarded; identical 401 is returned.
		auth.CheckPasswordDecoy(b.Password)
		_ = s.db.CreateLoginEvent(store.LoginEvent{
			UserID: 0, Username: b.Username, IP: ip, UserAgent: ua, Success: false, At: now,
		})
		writeJSON(w, 401, map[string]any{"error": "unauthorized"})
		return
	}
	if err != nil {
		// Any other DB error: same decoy, same uniform 401.
		auth.CheckPasswordDecoy(b.Password)
		_ = s.db.CreateLoginEvent(store.LoginEvent{
			UserID: 0, Username: b.Username, IP: ip, UserAgent: ua, Success: false, At: now,
		})
		writeJSON(w, 401, map[string]any{"error": "unauthorized"})
		return
	}
	if !auth.CheckPassword(b.Password, u.PasswordHash) {
		_ = s.db.CreateLoginEvent(store.LoginEvent{
			UserID: u.ID, Username: b.Username, IP: ip, UserAgent: ua, Success: false, At: now,
		})
		writeJSON(w, 401, map[string]any{"error": "unauthorized"})
		return
	}
	if u.Suspended {
		// Credentials were valid; the account is just locked. Audit as a
		// success so the lockout is distinguishable from a wrong password.
		_ = s.db.CreateLoginEvent(store.LoginEvent{
			UserID: u.ID, Username: u.Username, IP: ip, UserAgent: ua, Success: true, At: now,
		})
		writeJSON(w, 403, map[string]any{"error": "suspended"})
		return
	}
	_ = s.db.TouchLogin(u.ID, now, ip)
	_ = s.db.CreateLoginEvent(store.LoginEvent{
		UserID: u.ID, Username: u.Username, IP: ip, UserAgent: ua, Success: true, At: now,
	})
	// Plan 3: sessions are per-request now — there is no single shared PTY to
	// retarget. Login ONLY sets the cookie; the WS handler creates/attaches a
	// session scoped to this user via the sessions.Manager on connect.
	cookie, err := auth.SignSession(map[string]any{
		"username": u.Username,
		"role":     u.Role,
		"iat":      time.Now().Unix(),
	}, s.cfg.SessionSecret)
	if err != nil {
		writeJSON(w, 500, map[string]any{"error": "sign failed"})
		return
	}
	s.setSessionCookie(w, cookie)
	writeJSON(w, 200, map[string]any{"role": u.Role, "mustChangePassword": u.MustChangePassword})
}

// truncateUA caps the User-Agent string at 256 bytes so the login_events
// user_agent column stays bounded. A byte-slice truncation is safe here: the
// column is opaque text and a truncated UTF-8 sequence is acceptable for an
// audit log (we prioritize a bounded store over rune-boundary correctness).
func truncateUA(s string) string {
	if len(s) > 256 {
		return s[:256]
	}
	return s
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

// handleMe returns the authenticated user's identity + must-change-pw flag.
// The frontend's boot() calls this to decide login vs. change-password vs. app.
func (s *Server) handleMe(w http.ResponseWriter, r *http.Request) {
	id, _ := IdentityFrom(r.Context())
	u, err := s.db.GetUserByUsername(id.Username)
	if err != nil {
		writeJSON(w, 401, map[string]any{"error": "unauthorized"})
		return
	}
	writeJSON(w, 200, map[string]any{
		"username":           u.Username,
		"role":               u.Role,
		"mustChangePassword": u.MustChangePassword,
	})
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

// sameSiteMode maps the config string to http.SameSite. Unknown values default
// to None (the HTTPS-safe choice that lets the cookie ride the WS upgrade).
func (s *Server) sameSiteMode() http.SameSite {
	switch s.cfg.CookieSameSite {
	case "lax":
		return http.SameSiteLaxMode
	case "strict":
		return http.SameSiteStrictMode
	default:
		return http.SameSiteNoneMode
	}
}

// setSessionCookie sets the auth cookie with Secure + configured SameSite.
func (s *Server) setSessionCookie(w http.ResponseWriter, value string) {
	http.SetCookie(w, &http.Cookie{
		Name:     "session",
		Value:    value,
		Path:     "/",
		HttpOnly: true,
		Secure:   true,
		SameSite: s.sameSiteMode(),
	})
}
