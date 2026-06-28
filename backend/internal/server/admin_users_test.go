package server

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ldm0206/claude-docker/backend/internal/auth"
	"github.com/ldm0206/claude-docker/backend/internal/config"
	"github.com/ldm0206/claude-docker/backend/internal/pty"
	"github.com/ldm0206/claude-docker/backend/internal/sessions"
	"github.com/ldm0206/claude-docker/backend/internal/store"
	"github.com/ldm0206/claude-docker/backend/internal/system"
)

// ---------------------------------------------------------------------------
// fakeProvisioner — records calls, optionally returns errors
// ---------------------------------------------------------------------------

type fakeProvisioner struct {
	createCalls []createCall
	deleteCalls []string
	lockCalls   []string
	unlockCalls []string
	createErr   error
	deleteErr   error
	lockErr     error
	unlockErr   error
}

type createCall struct {
	username string
	uid      int
}

func (f *fakeProvisioner) Create(username string, uid int) error {
	f.createCalls = append(f.createCalls, createCall{username, uid})
	return f.createErr
}
func (f *fakeProvisioner) Delete(username string) error {
	f.deleteCalls = append(f.deleteCalls, username)
	return f.deleteErr
}
func (f *fakeProvisioner) Lock(username string) error {
	f.lockCalls = append(f.lockCalls, username)
	return f.lockErr
}
func (f *fakeProvisioner) Unlock(username string) error {
	f.unlockCalls = append(f.unlockCalls, username)
	return f.unlockErr
}

// ---------------------------------------------------------------------------
// test helpers
// ---------------------------------------------------------------------------

// newTestServerWithAdmin builds a Server with a fake provisioner and TWO users:
// "alice" (role: user) and "bob" (role: admin). Returns the server, the fake
// provisioner, and the DB (for row-level assertions).
func newTestServerWithAdmin(t *testing.T) (*Server, *fakeProvisioner, *store.DB) {
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
	// alice — regular user
	if _, err := db.CreateUser(store.User{
		UID: 2000, Username: "alice", PasswordHash: h, Role: "user", CreatedAt: 1,
	}); err != nil {
		t.Fatalf("create alice: %v", err)
	}
	// bob — admin
	if _, err := db.CreateUser(store.User{
		UID: 2001, Username: "bob", PasswordHash: h, Role: "admin", CreatedAt: 1,
	}); err != nil {
		t.Fatalf("create bob: %v", err)
	}
	fake := &fakeProvisioner{}
	cfg := &config.Config{SessionSecret: "s", Port: 0}
	// Wire a sessions.Manager backed by a no-op fake PTY factory. The admin
	// tests never create real sessions, so the factory is never invoked; it
	// just has to be non-nil so server.New doesn't panic.
	mgr := sessions.NewManager(db, newFakePTYFactoryForAdmin())
	srv := New(cfg, db, fake, mgr)
	return srv, fake, db
}

// newFakePTYFactoryForAdmin returns a PTYFactory suitable for the admin tests,
// which never create real sessions. Shared here so admin_users_test does not
// duplicate the fakePTY type defined in server_test.go. It needs pty.Options,
// hence the import in this file.
func newFakePTYFactoryForAdmin() sessions.PTYFactory {
	return func(o pty.Options) sessions.PTY { return &fakePTY{} }
}

// loginAs logs in as the given user and returns the session cookie value.
func loginAs(t *testing.T, s *Server, username string) string {
	t.Helper()
	body := fmt.Sprintf(`{"username":"%s","password":"pw123"}`, username)
	req := httptest.NewRequest("POST", "/auth", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	s.Routes().ServeHTTP(w, req)
	if w.Code != 200 {
		t.Fatalf("loginAs %s failed: %d; body=%s", username, w.Code, w.Body.String())
	}
	for _, c := range w.Result().Cookies() {
		if c.Name == "session" {
			return c.Value
		}
	}
	t.Fatal("no session cookie from login")
	return ""
}

// adminCookie returns a session cookie for the admin user "bob".
func adminCookie(t *testing.T, s *Server) string {
	t.Helper()
	return loginAs(t, s, "bob")
}

// userCookie returns a session cookie for the regular user "alice".
func userCookie(t *testing.T, s *Server) string {
	t.Helper()
	return loginAs(t, s, "alice")
}

// ---------------------------------------------------------------------------
// Tests
// ---------------------------------------------------------------------------

func TestAdminCreateUser_NonAdmin_Forbidden(t *testing.T) {
	s, _, _ := newTestServerWithAdmin(t)
	req := httptest.NewRequest("POST", "/api/admin/users",
		strings.NewReader(`{"username":"charlie","password":"pw","role":"user"}`))
	req.Header.Set("Content-Type", "application/json")
	req.AddCookie(&http.Cookie{Name: "session", Value: userCookie(t, s)})
	w := httptest.NewRecorder()
	s.Routes().ServeHTTP(w, req)
	if w.Code != 403 {
		t.Fatalf("expected 403, got %d", w.Code)
	}
}

func TestAdminCreateUser_Success(t *testing.T) {
	s, fake, db := newTestServerWithAdmin(t)
	req := httptest.NewRequest("POST", "/api/admin/users",
		strings.NewReader(`{"username":"charlie","password":"secret","role":"user"}`))
	req.Header.Set("Content-Type", "application/json")
	req.AddCookie(&http.Cookie{Name: "session", Value: adminCookie(t, s)})
	w := httptest.NewRecorder()
	s.Routes().ServeHTTP(w, req)
	if w.Code != 201 {
		t.Fatalf("expected 201, got %d; body=%s", w.Code, w.Body.String())
	}
	var resp map[string]any
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if resp["username"] != "charlie" {
		t.Fatalf("expected username charlie, got %v", resp["username"])
	}
	if resp["role"] != "user" {
		t.Fatalf("expected role user, got %v", resp["role"])
	}
	if resp["id"] == nil {
		t.Fatal("expected id to be set")
	}
	// DB row exists
	u, err := db.GetUserByUsername("charlie")
	if err != nil {
		t.Fatalf("user not in db: %v", err)
	}
	if u.Role != "user" {
		t.Fatalf("expected role user, got %s", u.Role)
	}
	// Fake provisioner was called
	if len(fake.createCalls) != 1 {
		t.Fatalf("expected 1 create call, got %d", len(fake.createCalls))
	}
	if fake.createCalls[0].username != "charlie" {
		t.Fatalf("expected create call for charlie, got %s", fake.createCalls[0].username)
	}
}

func TestAdminCreateUser_InvalidUsername(t *testing.T) {
	s, fake, _ := newTestServerWithAdmin(t)
	req := httptest.NewRequest("POST", "/api/admin/users",
		strings.NewReader(`{"username":"Bad-Name!","password":"pw","role":"user"}`))
	req.Header.Set("Content-Type", "application/json")
	req.AddCookie(&http.Cookie{Name: "session", Value: adminCookie(t, s)})
	w := httptest.NewRecorder()
	s.Routes().ServeHTTP(w, req)
	if w.Code != 400 {
		t.Fatalf("expected 400, got %d", w.Code)
	}
	if len(fake.createCalls) != 0 {
		t.Fatalf("fake should not have been called, got %d calls", len(fake.createCalls))
	}
}

func TestAdminCreateUser_InvalidRole(t *testing.T) {
	s, fake, _ := newTestServerWithAdmin(t)
	req := httptest.NewRequest("POST", "/api/admin/users",
		strings.NewReader(`{"username":"charlie","password":"pw","role":"superadmin"}`))
	req.Header.Set("Content-Type", "application/json")
	req.AddCookie(&http.Cookie{Name: "session", Value: adminCookie(t, s)})
	w := httptest.NewRecorder()
	s.Routes().ServeHTTP(w, req)
	if w.Code != 400 {
		t.Fatalf("expected 400, got %d", w.Code)
	}
	if len(fake.createCalls) != 0 {
		t.Fatalf("fake should not have been called, got %d calls", len(fake.createCalls))
	}
}

func TestAdminCreateUser_ProvisionerError_RollsBackDB(t *testing.T) {
	s, fake, db := newTestServerWithAdmin(t)
	fake.createErr = fmt.Errorf("useradd failed")
	req := httptest.NewRequest("POST", "/api/admin/users",
		strings.NewReader(`{"username":"charlie","password":"pw","role":"user"}`))
	req.Header.Set("Content-Type", "application/json")
	req.AddCookie(&http.Cookie{Name: "session", Value: adminCookie(t, s)})
	w := httptest.NewRecorder()
	s.Routes().ServeHTTP(w, req)
	if w.Code != 500 {
		t.Fatalf("expected 500, got %d; body=%s", w.Code, w.Body.String())
	}
	// DB row should be rolled back (gone)
	_, err := db.GetUserByUsername("charlie")
	if err == nil {
		t.Fatal("expected charlie to be rolled back, but row still exists")
	}
}

func TestAdminListUsers(t *testing.T) {
	s, _, _ := newTestServerWithAdmin(t)
	req := httptest.NewRequest("GET", "/api/admin/users", nil)
	req.AddCookie(&http.Cookie{Name: "session", Value: adminCookie(t, s)})
	w := httptest.NewRecorder()
	s.Routes().ServeHTTP(w, req)
	if w.Code != 200 {
		t.Fatalf("expected 200, got %d", w.Code)
	}
	var resp []map[string]any
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	// Should have at least alice and bob
	if len(resp) < 2 {
		t.Fatalf("expected at least 2 users, got %d", len(resp))
	}
	// Check that each entry has the required fields
	for _, u := range resp {
		if _, ok := u["id"]; !ok {
			t.Fatal("missing id field")
		}
		if _, ok := u["username"]; !ok {
			t.Fatal("missing username field")
		}
		if _, ok := u["role"]; !ok {
			t.Fatal("missing role field")
		}
		if _, ok := u["suspended"]; !ok {
			t.Fatal("missing suspended field")
		}
	}
}

func TestAdminSuspendUnsuspend(t *testing.T) {
	s, fake, db := newTestServerWithAdmin(t)
	// Create a user to suspend
	created, err := db.CreateUser(store.User{
		UID: 3000, Username: "dave", PasswordHash: "x", Role: "user", CreatedAt: 1,
	})
	if err != nil {
		t.Fatalf("create dave: %v", err)
	}

	// Suspend
	req := httptest.NewRequest("POST", fmt.Sprintf("/api/admin/users/%d/suspend", created.ID), nil)
	req.AddCookie(&http.Cookie{Name: "session", Value: adminCookie(t, s)})
	w := httptest.NewRecorder()
	s.Routes().ServeHTTP(w, req)
	if w.Code != 200 {
		t.Fatalf("suspend: expected 200, got %d; body=%s", w.Code, w.Body.String())
	}
	if len(fake.lockCalls) != 1 || fake.lockCalls[0] != "dave" {
		t.Fatalf("expected Lock(dave), got %v", fake.lockCalls)
	}
	u, _ := db.GetUserByID(created.ID)
	if !u.Suspended {
		t.Fatal("expected suspended=true in DB")
	}

	// Unsuspend
	req = httptest.NewRequest("POST", fmt.Sprintf("/api/admin/users/%d/unsuspend", created.ID), nil)
	req.AddCookie(&http.Cookie{Name: "session", Value: adminCookie(t, s)})
	w = httptest.NewRecorder()
	s.Routes().ServeHTTP(w, req)
	if w.Code != 200 {
		t.Fatalf("unsuspend: expected 200, got %d; body=%s", w.Code, w.Body.String())
	}
	if len(fake.unlockCalls) != 1 || fake.unlockCalls[0] != "dave" {
		t.Fatalf("expected Unlock(dave), got %v", fake.unlockCalls)
	}
	u, _ = db.GetUserByID(created.ID)
	if u.Suspended {
		t.Fatal("expected suspended=false in DB after unsuspend")
	}
}

func TestAdminSuspend_NotFound(t *testing.T) {
	s, _, _ := newTestServerWithAdmin(t)
	req := httptest.NewRequest("POST", "/api/admin/users/9999/suspend", nil)
	req.AddCookie(&http.Cookie{Name: "session", Value: adminCookie(t, s)})
	w := httptest.NewRecorder()
	s.Routes().ServeHTTP(w, req)
	if w.Code != 404 {
		t.Fatalf("expected 404, got %d", w.Code)
	}
}

func TestAdminDeleteUser(t *testing.T) {
	s, fake, db := newTestServerWithAdmin(t)
	created, err := db.CreateUser(store.User{
		UID: 4000, Username: "erin", PasswordHash: "x", Role: "user", CreatedAt: 1,
	})
	if err != nil {
		t.Fatalf("create erin: %v", err)
	}

	req := httptest.NewRequest("DELETE", fmt.Sprintf("/api/admin/users/%d", created.ID), nil)
	req.AddCookie(&http.Cookie{Name: "session", Value: adminCookie(t, s)})
	w := httptest.NewRecorder()
	s.Routes().ServeHTTP(w, req)
	if w.Code != 200 {
		t.Fatalf("expected 200, got %d; body=%s", w.Code, w.Body.String())
	}
	// DB row gone
	_, err = db.GetUserByID(created.ID)
	if err == nil {
		t.Fatal("expected erin to be deleted from DB")
	}
	// Fake provisioner called
	if len(fake.deleteCalls) != 1 || fake.deleteCalls[0] != "erin" {
		t.Fatalf("expected Delete(erin), got %v", fake.deleteCalls)
	}
}

func TestAdminDeleteUser_NotFound(t *testing.T) {
	s, _, _ := newTestServerWithAdmin(t)
	req := httptest.NewRequest("DELETE", "/api/admin/users/9999", nil)
	req.AddCookie(&http.Cookie{Name: "session", Value: adminCookie(t, s)})
	w := httptest.NewRecorder()
	s.Routes().ServeHTTP(w, req)
	if w.Code != 404 {
		t.Fatalf("expected 404, got %d", w.Code)
	}
}

func TestAdminCreateUser_MissingFields(t *testing.T) {
	s, fake, _ := newTestServerWithAdmin(t)
	// Missing password
	req := httptest.NewRequest("POST", "/api/admin/users",
		strings.NewReader(`{"username":"charlie","role":"user"}`))
	req.Header.Set("Content-Type", "application/json")
	req.AddCookie(&http.Cookie{Name: "session", Value: adminCookie(t, s)})
	w := httptest.NewRecorder()
	s.Routes().ServeHTTP(w, req)
	if w.Code != 400 {
		t.Fatalf("expected 400 for missing password, got %d", w.Code)
	}
	// Missing username
	req = httptest.NewRequest("POST", "/api/admin/users",
		strings.NewReader(`{"password":"pw","role":"user"}`))
	req.Header.Set("Content-Type", "application/json")
	req.AddCookie(&http.Cookie{Name: "session", Value: adminCookie(t, s)})
	w = httptest.NewRecorder()
	s.Routes().ServeHTTP(w, req)
	if w.Code != 400 {
		t.Fatalf("expected 400 for missing username, got %d", w.Code)
	}
	if len(fake.createCalls) != 0 {
		t.Fatal("fake should not have been called for invalid input")
	}
}

// Verify the system.AccountProvisioner interface is satisfied.
var _ system.AccountProvisioner = (*fakeProvisioner)(nil)
