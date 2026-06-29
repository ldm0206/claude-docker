package server

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/ldm0206/claude-docker/backend/internal/auth"
	"github.com/ldm0206/claude-docker/backend/internal/store"
)

// loginAsAdmin seeds an admin user ("root"/"pw123", role "admin"), performs a
// login, and returns the session cookie value. The Server returned by
// newTestServer only seeds "alice" (role "user") — admin endpoints need a
// privileged account.
func loginAsAdmin(t *testing.T, s *testServer) string {
	t.Helper()
	h, err := auth.HashPassword("pw123")
	if err != nil {
		t.Fatalf("hash admin: %v", err)
	}
	if _, err := s.db.CreateUser(store.User{
		UID: mustUID(t, s.db), Username: "root", PasswordHash: h, Role: "admin", CreatedAt: 1,
	}); err != nil {
		t.Fatalf("create admin: %v", err)
	}
	req := httptest.NewRequest("POST", "/auth", strings.NewReader(`{"username":"root","password":"pw123"}`))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	s.Routes().ServeHTTP(w, req)
	if w.Code != 200 {
		t.Fatalf("admin login: want 200, got %d; body=%s", w.Code, w.Body.String())
	}
	for _, c := range w.Result().Cookies() {
		if c.Name == "session" {
			return c.Value
		}
	}
	t.Fatal("no session cookie from admin login")
	return ""
}

// TestAdminLoginEvents_RequiresAdmin verifies a non-admin (alice, role user)
// gets 403 — the route lives under the admin-gated group (requireAdmin), so a
// role "user" must be rejected, not treated as unauthenticated (401).
func TestAdminLoginEvents_RequiresAdmin(t *testing.T) {
	s := newTestServer(t)
	s.db.CreateLoginEvent(store.LoginEvent{Username: "alice", UserID: 1, Success: true, IP: "1.2.3.4"})

	req := httptest.NewRequest("GET", "/api/admin/login-events", nil)
	req.AddCookie(&http.Cookie{Name: "session", Value: loginAsAlice(t, s)})
	w := httptest.NewRecorder()
	s.Routes().ServeHTTP(w, req)
	if w.Code != 403 {
		t.Fatalf("non-admin: want 403, got %d; body=%s", w.Code, w.Body.String())
	}
}

// TestAdminLoginEvents_AdminSeesEvents verifies an admin gets 200 with the
// seeded "ghost" fail event and "alice" success event present (newest-first by
// at, then id). The handler exposes id/userId/username/ip/userAgent/success/at.
func TestAdminLoginEvents_AdminSeesEvents(t *testing.T) {
	s := newTestServer(t)
	cookie := loginAsAdmin(t, s)
	s.db.CreateLoginEvent(store.LoginEvent{Username: "alice", UserID: 1, Success: true, IP: "1.2.3.4", At: 1})
	s.db.CreateLoginEvent(store.LoginEvent{Username: "ghost", UserID: 0, Success: false, IP: "5.6.7.8", At: 2})

	req := httptest.NewRequest("GET", "/api/admin/login-events?limit=10", nil)
	req.AddCookie(&http.Cookie{Name: "session", Value: cookie})
	w := httptest.NewRecorder()
	s.Routes().ServeHTTP(w, req)
	if w.Code != 200 {
		t.Fatalf("admin: want 200, got %d; body=%s", w.Code, w.Body.String())
	}
	body := w.Body.String()
	if !strings.Contains(body, `"username":"ghost"`) {
		t.Fatalf("missing ghost event: %s", body)
	}
	if !strings.Contains(body, `"username":"alice"`) {
		t.Fatalf("missing alice event: %s", body)
	}
	// ghost (at=2) must precede alice (at=1): store orders by at DESC, id DESC.
	if gi, ai := strings.Index(body, `"username":"ghost"`), strings.Index(body, `"username":"alice"`); gi >= 0 && ai >= 0 && gi > ai {
		t.Fatalf("ghost must come before alice (newest-first): %s", body)
	}
	// the handler keys are present
	if !strings.Contains(body, `"ip":"5.6.7.8"`) {
		t.Fatalf("missing ip field for ghost: %s", body)
	}
}
