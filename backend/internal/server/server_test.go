package server

import (
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ldm0206/claude-docker/backend/internal/auth"
	"github.com/ldm0206/claude-docker/backend/internal/config"
	"github.com/ldm0206/claude-docker/backend/internal/store"
	"github.com/ldm0206/claude-docker/backend/internal/system"
)

// mustUID allocates a UID via the store, failing the test on error.
func mustUID(t *testing.T, db *store.DB) int {
	t.Helper()
	uid, err := db.AllocateUID()
	if err != nil {
		t.Fatalf("allocate uid: %v", err)
	}
	return uid
}

// newTestServer builds a Server backed by a temp-file store with one user
// "alice" / "pw123" (role "user"). The store is closed via t.Cleanup.
func newTestServer(t *testing.T) *Server {
	t.Helper()
	db, err := store.Open(filepath.Join(t.TempDir(), "t.db"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { db.Close() })
	h, err := auth.HashPassword("pw123")
	if err != nil {
		t.Fatalf("hash: %v", err)
	}
	if _, err := db.CreateUser(store.User{
		UID: mustUID(t, db), Username: "alice", PasswordHash: h, Role: "user", CreatedAt: 1,
	}); err != nil {
		t.Fatalf("create user: %v", err)
	}
	cfg := &config.Config{SessionSecret: "s", Port: 0}
	return New(cfg, db, system.DefaultProvisioner)
}

func TestHealth(t *testing.T) {
	s := newTestServer(t)
	req := httptest.NewRequest("GET", "/health", nil)
	w := httptest.NewRecorder()
	s.Routes().ServeHTTP(w, req)
	if w.Code != 200 {
		t.Fatalf("health status %d", w.Code)
	}
}

func TestLoginRejectsWrongPassword(t *testing.T) {
	s := newTestServer(t)
	req := httptest.NewRequest("POST", "/auth", strings.NewReader(`{"username":"alice","password":"wrong"}`))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	s.Routes().ServeHTTP(w, req)
	if w.Code != 401 {
		t.Fatalf("expected 401, got %d", w.Code)
	}
}

func TestLoginRejectsUnknownUser(t *testing.T) {
	s := newTestServer(t)
	req := httptest.NewRequest("POST", "/auth", strings.NewReader(`{"username":"ghost","password":"x"}`))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	s.Routes().ServeHTTP(w, req)
	if w.Code != 401 {
		t.Fatalf("expected 401, got %d", w.Code)
	}
}

func TestLoginSuccessSetsCookieAndRole(t *testing.T) {
	s := newTestServer(t)
	req := httptest.NewRequest("POST", "/auth", strings.NewReader(`{"username":"alice","password":"pw123"}`))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	s.Routes().ServeHTTP(w, req)
	if w.Code != 200 {
		t.Fatalf("expected 200, got %d; body=%s", w.Code, w.Body.String())
	}
	if !strings.Contains(w.Body.String(), `"role"`) {
		t.Fatalf("body missing role: %s", w.Body.String())
	}
	var hasCookie bool
	for _, c := range w.Result().Cookies() {
		if c.Name == "session" && c.Value != "" {
			hasCookie = true
		}
	}
	if !hasCookie {
		t.Fatal("expected session cookie to be set")
	}
}

// loginAsAlice performs a login and returns the session cookie value.
func loginAsAlice(t *testing.T, s *Server) string {
	t.Helper()
	req := httptest.NewRequest("POST", "/auth", strings.NewReader(`{"username":"alice","password":"pw123"}`))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	s.Routes().ServeHTTP(w, req)
	if w.Code != 200 {
		t.Fatalf("login setup failed: %d", w.Code)
	}
	for _, c := range w.Result().Cookies() {
		if c.Name == "session" {
			return c.Value
		}
	}
	t.Fatal("no session cookie from login")
	return ""
}

func TestStateRequiresAuth(t *testing.T) {
	s := newTestServer(t)
	req := httptest.NewRequest("GET", "/api/state", nil)
	w := httptest.NewRecorder()
	s.Routes().ServeHTTP(w, req)
	if w.Code != 401 {
		t.Fatalf("expected 401, got %d", w.Code)
	}
}

func TestStateWithCookieOK(t *testing.T) {
	s := newTestServer(t)
	req := httptest.NewRequest("GET", "/api/state", nil)
	req.AddCookie(&http.Cookie{Name: "session", Value: loginAsAlice(t, s)})
	w := httptest.NewRecorder()
	s.Routes().ServeHTTP(w, req)
	if w.Code != 200 {
		t.Fatalf("expected 200, got %d", w.Code)
	}
}

func TestChangePasswordRequiresAuth(t *testing.T) {
	s := newTestServer(t)
	req := httptest.NewRequest("POST", "/auth/change-password", strings.NewReader(`{"newPassword":"new"}`))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	s.Routes().ServeHTTP(w, req)
	if w.Code != 401 {
		t.Fatalf("expected 401, got %d", w.Code)
	}
}

func TestChangePasswordRehashesAndClearsFlag(t *testing.T) {
	s := newTestServer(t)
	// Force the must-change flag on so we can verify it clears.
	u, err := s.db.GetUserByUsername("alice")
	if err != nil {
		t.Fatalf("get user: %v", err)
	}

	req := httptest.NewRequest("POST", "/auth/change-password", strings.NewReader(`{"newPassword":"newpw456"}`))
	req.Header.Set("Content-Type", "application/json")
	req.AddCookie(&http.Cookie{Name: "session", Value: loginAsAlice(t, s)})
	w := httptest.NewRecorder()
	s.Routes().ServeHTTP(w, req)
	if w.Code != 200 {
		t.Fatalf("expected 200, got %d; body=%s", w.Code, w.Body.String())
	}

	// The hash must have changed and the new password must verify.
	updated, err := s.db.GetUserByID(u.ID)
	if err != nil {
		t.Fatalf("get updated: %v", err)
	}
	if updated.PasswordHash == u.PasswordHash {
		t.Fatal("password hash was not updated")
	}
	if !auth.CheckPassword("newpw456", updated.PasswordHash) {
		t.Fatal("new password does not verify")
	}
	if updated.MustChangePassword {
		t.Fatal("must_change_password flag should be cleared")
	}
}
