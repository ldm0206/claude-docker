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
	"github.com/ldm0206/claude-docker/backend/internal/sessions"
	"github.com/ldm0206/claude-docker/backend/internal/store"
	"github.com/ldm0206/claude-docker/backend/internal/system"
)

// ---------------------------------------------------------------------------
// Helpers for session API tests
// ---------------------------------------------------------------------------

// newSessionTestServer builds a Server with alice (user) and bob (admin), both
// backed by the fake PTY factory so session create/kill works on Windows.
func newSessionTestServer(t *testing.T) *testServer {
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
	// Default per-user cap is 1 (single-session model). Several tests here
	// exercise multi-session listing/kill-all semantics, so raise both users'
	// cap. Tests that specifically check the cap (OverCap → 409) set it back
	// down themselves.
	for _, name := range []string{"alice", "bob"} {
		u, err := db.GetUserByUsername(name)
		if err != nil {
			t.Fatalf("get %s: %v", name, err)
		}
		if err := db.SetUserMaxSessions(u.ID, 8); err != nil {
			t.Fatalf("set max sessions %s: %v", name, err)
		}
	}
	cfg := &config.Config{SessionSecret: "s", Port: 0}
	factory, created := newFakePTYFactory()
	mgr := sessions.NewManager(db, factory)
	srv := New(cfg, db, system.DefaultProvisioner, mgr, nil, nil, nil)
	return &testServer{Server: srv, createdPTYs: created}
}

// createSessionViaAPI calls POST /api/sessions and returns the response body.
func createSessionViaAPI(t *testing.T, s *testServer, cookie, name string) *httptest.ResponseRecorder {
	t.Helper()
	body := `{}`
	if name != "" {
		body = fmt.Sprintf(`{"name":%q}`, name)
	}
	req := httptest.NewRequest("POST", "/api/sessions", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.AddCookie(&http.Cookie{Name: "session", Value: cookie})
	w := httptest.NewRecorder()
	s.Routes().ServeHTTP(w, req)
	return w
}

// listSessionsViaAPI calls GET /api/sessions and returns the response.
func listSessionsViaAPI(t *testing.T, s *testServer, cookie string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest("GET", "/api/sessions", nil)
	req.AddCookie(&http.Cookie{Name: "session", Value: cookie})
	w := httptest.NewRecorder()
	s.Routes().ServeHTTP(w, req)
	return w
}

// deleteSessionViaAPI calls DELETE /api/sessions/:id and returns the response.
func deleteSessionViaAPI(t *testing.T, s *testServer, cookie, sid string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest("DELETE", "/api/sessions/"+sid, nil)
	req.AddCookie(&http.Cookie{Name: "session", Value: cookie})
	w := httptest.NewRecorder()
	s.Routes().ServeHTTP(w, req)
	return w
}

// ---------------------------------------------------------------------------
// User session API tests
// ---------------------------------------------------------------------------

func TestSessionCreate_Success(t *testing.T) {
	s := newSessionTestServer(t)
	aliceCookie := loginAs(t, s.Server, "alice")
	w := createSessionViaAPI(t, s, aliceCookie, "my-session")
	if w.Code != 201 {
		t.Fatalf("expected 201, got %d; body=%s", w.Code, w.Body.String())
	}
	var resp map[string]any
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if resp["id"] == nil || resp["id"] == "" {
		t.Fatal("expected id in response")
	}
	if resp["name"] != "my-session" {
		t.Fatalf("expected name=my-session, got %v", resp["name"])
	}
}

func TestSessionCreate_OverCap_Returns409(t *testing.T) {
	s := newSessionTestServer(t)
	aliceCookie := loginAs(t, s.Server, "alice")
	alice, err := s.db.GetUserByUsername("alice")
	if err != nil {
		t.Fatalf("get alice: %v", err)
	}
	if err := s.db.SetUserMaxSessions(alice.ID, 1); err != nil {
		t.Fatalf("set max sessions: %v", err)
	}
	// First create succeeds.
	w := createSessionViaAPI(t, s, aliceCookie, "s1")
	if w.Code != 201 {
		t.Fatalf("1st create: expected 201, got %d; body=%s", w.Code, w.Body.String())
	}
	// Second create hits cap → 409.
	w = createSessionViaAPI(t, s, aliceCookie, "s2")
	if w.Code != 409 {
		t.Fatalf("2nd create: expected 409, got %d; body=%s", w.Code, w.Body.String())
	}
}

func TestSessionList_ReturnsOnlyCallerSessions(t *testing.T) {
	s := newSessionTestServer(t)
	aliceCookie := loginAs(t, s.Server, "alice")
	bobCookie := loginAs(t, s.Server, "bob")

	// Alice creates 2 sessions, Bob creates 1.
	w := createSessionViaAPI(t, s, aliceCookie, "a1")
	if w.Code != 201 {
		t.Fatalf("alice create a1: %d; body=%s", w.Code, w.Body.String())
	}
	w = createSessionViaAPI(t, s, aliceCookie, "a2")
	if w.Code != 201 {
		t.Fatalf("alice create a2: %d; body=%s", w.Code, w.Body.String())
	}
	w = createSessionViaAPI(t, s, bobCookie, "b1")
	if w.Code != 201 {
		t.Fatalf("bob create b1: %d; body=%s", w.Code, w.Body.String())
	}

	// Alice lists — must see exactly 2.
	w = listSessionsViaAPI(t, s, aliceCookie)
	if w.Code != 200 {
		t.Fatalf("list: expected 200, got %d", w.Code)
	}
	var list []map[string]any
	if err := json.NewDecoder(w.Body).Decode(&list); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(list) != 2 {
		t.Fatalf("alice should see 2 sessions, got %d", len(list))
	}

	// Bob lists — must see exactly 1.
	w = listSessionsViaAPI(t, s, bobCookie)
	if w.Code != 200 {
		t.Fatalf("list: expected 200, got %d", w.Code)
	}
	if err := json.NewDecoder(w.Body).Decode(&list); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(list) != 1 {
		t.Fatalf("bob should see 1 session, got %d", len(list))
	}
}

func TestSessionList_ResponseShape(t *testing.T) {
	s := newSessionTestServer(t)
	aliceCookie := loginAs(t, s.Server, "alice")
	w := createSessionViaAPI(t, s, aliceCookie, "shapetest")
	if w.Code != 201 {
		t.Fatalf("create: %d; body=%s", w.Code, w.Body.String())
	}

	w = listSessionsViaAPI(t, s, aliceCookie)
	var list []map[string]any
	if err := json.NewDecoder(w.Body).Decode(&list); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(list) != 1 {
		t.Fatalf("expected 1 session, got %d", len(list))
	}
	sess := list[0]
	for _, key := range []string{"id", "name", "startedAt", "lastSeenAt", "alive"} {
		if _, ok := sess[key]; !ok {
			t.Fatalf("session missing key %q; got %v", key, sess)
		}
	}
}

func TestSessionDelete_OwnSession_Returns200(t *testing.T) {
	s := newSessionTestServer(t)
	aliceCookie := loginAs(t, s.Server, "alice")
	w := createSessionViaAPI(t, s, aliceCookie, "to-delete")
	if w.Code != 201 {
		t.Fatalf("create: %d; body=%s", w.Code, w.Body.String())
	}
	var createResp map[string]any
	json.NewDecoder(w.Body).Decode(&createResp)
	sid := createResp["id"].(string)

	// Delete own session → 200.
	w = deleteSessionViaAPI(t, s, aliceCookie, sid)
	if w.Code != 200 {
		t.Fatalf("delete: expected 200, got %d; body=%s", w.Code, w.Body.String())
	}

	// Session no longer in list.
	w = listSessionsViaAPI(t, s, aliceCookie)
	var list []map[string]any
	json.NewDecoder(w.Body).Decode(&list)
	// The session row still exists (alive=false) but the live PTY is gone.
	// Check that it's marked not alive.
	for _, s := range list {
		if s["id"] == sid && s["alive"] == true {
			t.Fatal("deleted session should not be alive")
		}
	}
}

func TestSessionDelete_OtherUserSession_Returns404(t *testing.T) {
	s := newSessionTestServer(t)
	aliceCookie := loginAs(t, s.Server, "alice")
	bobCookie := loginAs(t, s.Server, "bob")

	// Bob creates a session.
	w := createSessionViaAPI(t, s, bobCookie, "bobs-session")
	if w.Code != 201 {
		t.Fatalf("bob create: %d; body=%s", w.Code, w.Body.String())
	}
	var createResp map[string]any
	json.NewDecoder(w.Body).Decode(&createResp)
	bobSID := createResp["id"].(string)

	// Alice tries to delete Bob's session → 404 (no leak).
	w = deleteSessionViaAPI(t, s, aliceCookie, bobSID)
	if w.Code != 404 {
		t.Fatalf("alice deleting bob's session: expected 404, got %d; body=%s", w.Code, w.Body.String())
	}
}

func TestSessionDelete_UnknownSession_Returns404(t *testing.T) {
	s := newSessionTestServer(t)
	aliceCookie := loginAs(t, s.Server, "alice")
	w := deleteSessionViaAPI(t, s, aliceCookie, "nonexistent-id")
	if w.Code != 404 {
		t.Fatalf("delete unknown: expected 404, got %d; body=%s", w.Code, w.Body.String())
	}
}

// TestSessionDelete_OrphanRow_HardDeletes reproduces the "can't delete after a
// restart" bug: a session row exists in the DB (alive=1, leftover from a prior
// run) but is NOT in the live PTY map. The old handleDeleteSession did a live
// Get, missed, and 404'd — so the user could never clear the dead row (and it
// kept holding a cap slot). Now the miss falls through to DeleteOrphan, which
// hard-deletes the row. Cross-user ids still 404 (no leak).
func TestSessionDelete_OrphanRow_HardDeletes(t *testing.T) {
	s := newSessionTestServer(t)
	aliceCookie := loginAs(t, s.Server, "alice")
	alice, err := s.db.GetUserByUsername("alice")
	if err != nil {
		t.Fatalf("get alice: %v", err)
	}
	const orphanSID = "orphan-after-restart"
	if err := s.db.CreateSession(store.Session{
		ID: orphanSID, UserID: alice.ID, Name: "alice", StartedAt: 1, LastSeenAt: 1, Alive: true,
	}); err != nil {
		t.Fatalf("seed orphan: %v", err)
	}
	// Confirm precondition: live map miss.
	if _, ok := s.sess.Get("alice", orphanSID); ok {
		t.Fatal("precondition: orphan must not be in the live map")
	}

	w := deleteSessionViaAPI(t, s, aliceCookie, orphanSID)
	if w.Code != 200 {
		t.Fatalf("delete orphan: expected 200, got %d; body=%s", w.Code, w.Body.String())
	}
	// Hard-deleted: the row is GONE from the list entirely (not alive=0).
	w = listSessionsViaAPI(t, s, aliceCookie)
	var list []map[string]any
	json.NewDecoder(w.Body).Decode(&list)
	for _, row := range list {
		if row["id"] == orphanSID {
			t.Fatalf("orphan row still listed after delete: %v", row)
		}
	}
}

// TestSessionDelete_OrphanRow_OtherUser_Returns404 verifies DeleteOrphan's
// ownership check: a DB-only row owned by bob must 404 when alice tries to
// delete it (same no-leak contract as the live path).
func TestSessionDelete_OrphanRow_OtherUser_Returns404(t *testing.T) {
	s := newSessionTestServer(t)
	aliceCookie := loginAs(t, s.Server, "alice")
	bob, err := s.db.GetUserByUsername("bob")
	if err != nil {
		t.Fatalf("get bob: %v", err)
	}
	const bobOrphan = "bobs-orphan-row"
	if err := s.db.CreateSession(store.Session{
		ID: bobOrphan, UserID: bob.ID, Name: "bob", StartedAt: 1, LastSeenAt: 1, Alive: true,
	}); err != nil {
		t.Fatalf("seed: %v", err)
	}
	w := deleteSessionViaAPI(t, s, aliceCookie, bobOrphan)
	if w.Code != 404 {
		t.Fatalf("alice deleting bob's orphan: expected 404, got %d; body=%s", w.Code, w.Body.String())
	}
	// Bob's row survives.
	if _, err := s.db.GetSession(bobOrphan); err != nil {
		t.Fatalf("bob's orphan row should still exist, got %v", err)
	}
}

// ---------------------------------------------------------------------------
// Admin session API tests
// ---------------------------------------------------------------------------

func TestAdminListSessions_NonAdmin_Forbidden(t *testing.T) {
	s := newSessionTestServer(t)
	aliceCookie := loginAs(t, s.Server, "alice")
	alice, _ := s.db.GetUserByUsername("alice")
	req := httptest.NewRequest("GET", fmt.Sprintf("/api/admin/users/%d/sessions", alice.ID), nil)
	req.AddCookie(&http.Cookie{Name: "session", Value: aliceCookie})
	w := httptest.NewRecorder()
	s.Routes().ServeHTTP(w, req)
	if w.Code != 403 {
		t.Fatalf("expected 403, got %d", w.Code)
	}
}

func TestAdminListSessions_Success(t *testing.T) {
	s := newSessionTestServer(t)
	bobCookie := loginAs(t, s.Server, "bob")
	aliceCookie := loginAs(t, s.Server, "alice")
	alice, _ := s.db.GetUserByUsername("alice")

	// Alice creates a session.
	w := createSessionViaAPI(t, s, aliceCookie, "a1")
	if w.Code != 201 {
		t.Fatalf("alice create: %d; body=%s", w.Code, w.Body.String())
	}

	// Admin lists alice's sessions.
	req := httptest.NewRequest("GET", fmt.Sprintf("/api/admin/users/%d/sessions", alice.ID), nil)
	req.AddCookie(&http.Cookie{Name: "session", Value: bobCookie})
	w = httptest.NewRecorder()
	s.Routes().ServeHTTP(w, req)
	if w.Code != 200 {
		t.Fatalf("admin list: expected 200, got %d; body=%s", w.Code, w.Body.String())
	}
	var list []map[string]any
	if err := json.NewDecoder(w.Body).Decode(&list); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(list) != 1 {
		t.Fatalf("admin should see 1 session for alice, got %d", len(list))
	}
}

func TestAdminKillSession_Success(t *testing.T) {
	s := newSessionTestServer(t)
	bobCookie := loginAs(t, s.Server, "bob")
	aliceCookie := loginAs(t, s.Server, "alice")
	alice, _ := s.db.GetUserByUsername("alice")

	// Alice creates a session.
	w := createSessionViaAPI(t, s, aliceCookie, "a1")
	if w.Code != 201 {
		t.Fatalf("alice create: %d; body=%s", w.Code, w.Body.String())
	}
	var createResp map[string]any
	json.NewDecoder(w.Body).Decode(&createResp)
	sid := createResp["id"].(string)

	// Admin kills alice's session.
	req := httptest.NewRequest("DELETE", fmt.Sprintf("/api/admin/users/%d/sessions/%s", alice.ID, sid), nil)
	req.AddCookie(&http.Cookie{Name: "session", Value: bobCookie})
	w = httptest.NewRecorder()
	s.Routes().ServeHTTP(w, req)
	if w.Code != 200 {
		t.Fatalf("admin kill: expected 200, got %d; body=%s", w.Code, w.Body.String())
	}
}

func TestAdminKillAllSessions_Success(t *testing.T) {
	s := newSessionTestServer(t)
	bobCookie := loginAs(t, s.Server, "bob")
	aliceCookie := loginAs(t, s.Server, "alice")
	alice, _ := s.db.GetUserByUsername("alice")

	// Alice creates 3 sessions.
	for i := 0; i < 3; i++ {
		w := createSessionViaAPI(t, s, aliceCookie, fmt.Sprintf("a%d", i))
		if w.Code != 201 {
			t.Fatalf("alice create %d: %d; body=%s", i, w.Code, w.Body.String())
		}
	}

	// Admin kill-all for alice.
	req := httptest.NewRequest("DELETE", fmt.Sprintf("/api/admin/users/%d/sessions", alice.ID), nil)
	req.AddCookie(&http.Cookie{Name: "session", Value: bobCookie})
	w := httptest.NewRecorder()
	s.Routes().ServeHTTP(w, req)
	if w.Code != 200 {
		t.Fatalf("admin kill-all: expected 200, got %d; body=%s", w.Code, w.Body.String())
	}

	// Verify alice has no alive sessions.
	w = listSessionsViaAPI(t, s, aliceCookie)
	var list []map[string]any
	json.NewDecoder(w.Body).Decode(&list)
	aliveCount := 0
	for _, s := range list {
		if s["alive"] == true {
			aliveCount++
		}
	}
	if aliveCount != 0 {
		t.Fatalf("expected 0 alive sessions after kill-all, got %d", aliveCount)
	}
}

func TestAdminKillAllSessions_NonAdmin_Forbidden(t *testing.T) {
	s := newSessionTestServer(t)
	aliceCookie := loginAs(t, s.Server, "alice")
	alice, _ := s.db.GetUserByUsername("alice")
	req := httptest.NewRequest("DELETE", fmt.Sprintf("/api/admin/users/%d/sessions", alice.ID), nil)
	req.AddCookie(&http.Cookie{Name: "session", Value: aliceCookie})
	w := httptest.NewRecorder()
	s.Routes().ServeHTTP(w, req)
	if w.Code != 403 {
		t.Fatalf("expected 403, got %d", w.Code)
	}
}

func TestAdminKillSession_NonAdmin_Forbidden(t *testing.T) {
	s := newSessionTestServer(t)
	aliceCookie := loginAs(t, s.Server, "alice")
	alice, _ := s.db.GetUserByUsername("alice")
	req := httptest.NewRequest("DELETE", fmt.Sprintf("/api/admin/users/%d/sessions/fake-id", alice.ID), nil)
	req.AddCookie(&http.Cookie{Name: "session", Value: aliceCookie})
	w := httptest.NewRecorder()
	s.Routes().ServeHTTP(w, req)
	if w.Code != 403 {
		t.Fatalf("expected 403, got %d", w.Code)
	}
}
