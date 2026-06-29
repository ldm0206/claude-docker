package server

import (
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/ldm0206/claude-docker/backend/internal/auth"
	"github.com/ldm0206/claude-docker/backend/internal/config"
	"github.com/ldm0206/claude-docker/backend/internal/pty"
	"github.com/ldm0206/claude-docker/backend/internal/sessions"
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

// fakePTY is a Windows-friendly stand-in for *pty.Manager. It records Start/Stop
// calls and supports the OnData/OnExit callback contract the WS handler relies
// on. It does NOT spawn any process — that's the point (Plan 3 defers real
// creack/pty + gosu to Linux runtime; the wiring logic is unit-testable here).
//
// resolvedEnv captures the env slice the PTY would have been spawned with. The
// sessions.Manager wraps the EnvFactory in opts.Env (a func() []string); the
// fake invokes it once at construction time (matching how the real Manager's
// Start() calls opts.Env()) so tests can assert on the credential injection
// (T8). It is NOT the raw func — it is the materialized []string.
type fakePTY struct {
	opts        pty.Options
	resolvedEnv []string
	startCnt    int32
	stopCnt     int32
	mu          sync.Mutex
	alive       bool
	dataCbs     []func([]byte)
	exitCbs     []func(int)
}

func (f *fakePTY) Start() error {
	atomic.AddInt32(&f.startCnt, 1)
	f.mu.Lock()
	f.alive = true
	f.mu.Unlock()
	return nil
}
func (f *fakePTY) Stop() {
	atomic.AddInt32(&f.stopCnt, 1)
	f.mu.Lock()
	f.alive = false
	f.mu.Unlock()
}
func (f *fakePTY) Write(b []byte) error           { return nil }
func (f *fakePTY) Resize(cols, rows uint16) error { return nil }
func (f *fakePTY) Alive() bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.alive
}
func (f *fakePTY) OnData(cb func([]byte)) func() {
	f.mu.Lock()
	f.dataCbs = append(f.dataCbs, cb)
	f.mu.Unlock()
	return func() {}
}
func (f *fakePTY) OnExit(cb func(int)) func() {
	f.mu.Lock()
	f.exitCbs = append(f.exitCbs, cb)
	f.mu.Unlock()
	return func() {}
}

// newFakePTYFactory returns a PTYFactory that records every PTY it builds so
// tests can assert on Start/Stop counts. Each built fake materializes opts.Env
// (the func() []string set by sessions.Manager) exactly once at construction,
// mirroring how the real *pty.Manager consumes cmd.Env in Start(). This lets T8
// tests inspect the decrypted credential env without a Linux PTY.
func newFakePTYFactory() (sessions.PTYFactory, func() []*fakePTY) {
	var mu sync.Mutex
	var created []*fakePTY
	factory := func(opts pty.Options) sessions.PTY {
		f := &fakePTY{opts: opts}
		if opts.Env != nil {
			f.resolvedEnv = opts.Env()
		}
		mu.Lock()
		created = append(created, f)
		mu.Unlock()
		return f
	}
	return factory, func() []*fakePTY {
		mu.Lock()
		defer mu.Unlock()
		return append([]*fakePTY(nil), created...)
	}
}

// testServer bundles a Server with the fake PTY factory's snapshotter, so
// tests can assert on how many PTYs were created (create-vs-attach behavior).
type testServer struct {
	*Server
	createdPTYs func() []*fakePTY
}

// newTestServer builds a Server backed by a temp-file store with one user
// "alice" / "pw123" (role "user") and a sessions.Manager wired to a fake PTY
// factory. The store is closed via t.Cleanup.
func newTestServer(t *testing.T) *testServer {
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
	factory, created := newFakePTYFactory()
	mgr := sessions.NewManager(db, factory)
	srv := New(cfg, db, system.DefaultProvisioner, mgr, nil, nil, nil, nil)
	return &testServer{Server: srv, createdPTYs: created}
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

// TestLogoutClearsCookie pins the logout endpoint path (/auth/logout — the
// SPA calls POST /auth/logout) and that it expires the session cookie. A past
// bug had the route at /logout while the SPA posted /auth/logout, so the
// sign-out button 404'd silently.
func TestLogoutClearsCookie(t *testing.T) {
	s := newTestServer(t)
	req := httptest.NewRequest("POST", "/auth/logout", nil)
	w := httptest.NewRecorder()
	s.Routes().ServeHTTP(w, req)
	if w.Code != 200 {
		t.Fatalf("expected 200, got %d; body=%s", w.Code, w.Body.String())
	}
	var cleared bool
	for _, c := range w.Result().Cookies() {
		if c.Name == "session" && c.MaxAge < 0 {
			cleared = true
		}
	}
	if !cleared {
		t.Fatal("expected session cookie to be expired (MaxAge < 0)")
	}
}

// TestLoginCookieAttributes verifies the session cookie is set with Secure and
// the configured SameSite (default "none" for HTTPS deployments).
func TestLoginCookieAttributes(t *testing.T) {
	s := newTestServer(t)
	req := httptest.NewRequest("POST", "/auth", strings.NewReader(`{"username":"alice","password":"pw123"}`))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	s.Routes().ServeHTTP(w, req)
	if w.Code != 200 {
		t.Fatalf("status %d; body=%s", w.Code, w.Body.String())
	}
	var sc *http.Cookie
	for _, c := range w.Result().Cookies() {
		if c.Name == "session" {
			sc = c
		}
	}
	if sc == nil {
		t.Fatal("no session cookie")
	}
	if !sc.Secure {
		t.Error("cookie must have Secure=true")
	}
	if sc.SameSite != http.SameSiteNoneMode {
		t.Errorf("SameSite = %v, want None (default)", sc.SameSite)
	}
}

// loginAsAlice performs a login and returns the session cookie value.
func loginAsAlice(t *testing.T, s *testServer) string {
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

// TestStateDropsSessionAlive verifies /api/state returns {captureOn:false} and
// NO sessionAlive key (the shared PTY is gone; per-user liveness is via
// /api/sessions in T6).
func TestStateDropsSessionAlive(t *testing.T) {
	s := newTestServer(t)
	req := httptest.NewRequest("GET", "/api/state", nil)
	req.AddCookie(&http.Cookie{Name: "session", Value: loginAsAlice(t, s)})
	w := httptest.NewRecorder()
	s.Routes().ServeHTTP(w, req)
	if w.Code != 200 {
		t.Fatalf("expected 200, got %d", w.Code)
	}
	body := w.Body.String()
	if !strings.Contains(body, `"captureOn":false`) {
		t.Fatalf("body missing captureOn:false: %s", body)
	}
	if strings.Contains(body, "sessionAlive") {
		t.Fatalf("body must NOT contain sessionAlive: %s", body)
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

// TestRestartRouteRemoved verifies the old global /api/session/restart handler
// is GONE (per-session kill+create replaces it in T6). The chi router has no
// matching route, so the request falls through to the SPA catch-all which
// serves index.html (status 200, HTML body). We assert the response is NOT the
// old handler's `{ok:true}` JSON — proving the restart handler no longer runs.
func TestRestartRouteRemoved(t *testing.T) {
	s := newTestServer(t)
	req := httptest.NewRequest("POST", "/api/session/restart", nil)
	req.AddCookie(&http.Cookie{Name: "session", Value: loginAsAlice(t, s)})
	w := httptest.NewRecorder()
	s.Routes().ServeHTTP(w, req)
	body := w.Body.String()
	if strings.Contains(body, `"ok":true`) {
		t.Fatalf("restart handler still active; body=%s", body)
	}
	// Content-Type must NOT be application/json (the old handler set it).
	ct := w.Header().Get("Content-Type")
	if strings.HasPrefix(ct, "application/json") {
		t.Fatalf("restart handler still setting JSON Content-Type: %s; body=%s", ct, body)
	}
}

// TestEnsureSessionCreatesWhenAbsent drives the extracted helper that the WS
// handler delegates to. With sid=="" it must CREATE a session, lazy-start it,
// and return the new id + status 200. This is the unit-test stand-in for the
// WS create path (no real WebSocket dial needed).
func TestEnsureSessionCreatesWhenAbsent(t *testing.T) {
	s := newTestServer(t)
	alice, err := s.db.GetUserByUsername("alice")
	if err != nil {
		t.Fatalf("get alice: %v", err)
	}

	p, sid, status := s.ensureSession(alice, "", httptest.NewRequest("GET", "/", nil))
	if status != 200 {
		t.Fatalf("status = %d, want 200", status)
	}
	if sid == "" {
		t.Fatal("sid empty for create path")
	}
	if p == nil {
		t.Fatal("pty nil for create path")
	}
	// Create lazy-starts the PTY (WS handler does this before subscribing).
	fp := p.(*fakePTY)
	if atomic.LoadInt32(&fp.startCnt) != 1 {
		t.Fatalf("Start called %d times, want 1", atomic.LoadInt32(&fp.startCnt))
	}
	// The new id must be retrievable via the manager.
	if got, ok := s.sess.Get("alice", sid); !ok || got != p {
		t.Fatalf("manager.Get returned ok=%v, mismatched PTY", ok)
	}
}

// TestEnsureSessionAttachesExisting verifies sid != "" reuses the existing PTY
// rather than creating a new one.
func TestEnsureSessionAttachesExisting(t *testing.T) {
	s := newTestServer(t)
	alice, err := s.db.GetUserByUsername("alice")
	if err != nil {
		t.Fatalf("get alice: %v", err)
	}

	// Seed one session.
	_, sid0, _ := s.ensureSession(alice, "", httptest.NewRequest("GET", "/", nil))
	created := s.createdPTYs()
	if len(created) != 1 {
		t.Fatalf("expected 1 PTY after create, got %d", len(created))
	}

	// Attach to sid0 — must NOT create a second PTY.
	p2, sid2, status := s.ensureSession(alice, sid0, httptest.NewRequest("GET", "/", nil))
	if status != 200 {
		t.Fatalf("status = %d, want 200", status)
	}
	if sid2 != sid0 {
		t.Fatalf("sid changed: %q vs %q", sid2, sid0)
	}
	if len(s.createdPTYs()) != 1 {
		t.Fatalf("attach must not create a new PTY; total=%d", len(s.createdPTYs()))
	}
	if p2 != created[0] {
		t.Fatal("attach returned a different PTY than the seeded one")
	}
}

// TestEnsureSessionUnknownSIDReturns404 verifies an explicit-but-unknown sid
// yields 404 (NOT a create — the client asked for a specific session).
func TestEnsureSessionUnknownSIDReturns404(t *testing.T) {
	s := newTestServer(t)
	alice, err := s.db.GetUserByUsername("alice")
	if err != nil {
		t.Fatalf("get alice: %v", err)
	}
	_, _, status := s.ensureSession(alice, "does-not-exist", httptest.NewRequest("GET", "/", nil))
	if status != 404 {
		t.Fatalf("status = %d, want 404", status)
	}
}

// TestEnsureSessionRevivesDBOnlySID reproduces the post-restart 404: a session
// row exists in the DB (alive=1) but is absent from the live PTY map (the
// process image is gone after a server restart). The frontend lists it via
// /api/sessions and tries to attach — ensureSession must REVIVE the session
// (reuse the same id, rebuild the PTY) rather than 404.
//
// Without the revive path, s.sess.Get returns (nil,false) and ensureSession
// falls through to the unknown-sid 404 branch — the bug in the field log:
// GET /ws/terminal?session=<id> 404 after a restart.
func TestEnsureSessionRevivesDBOnlySID(t *testing.T) {
	s := newTestServer(t)
	alice, err := s.db.GetUserByUsername("alice")
	if err != nil {
		t.Fatalf("get alice: %v", err)
	}

	// Seed a DB session row that has NO live PTY (simulates a server restart:
	// the row persists, the in-memory map is empty).
	const staleSID = "stale-restart-sid"
	if err := s.db.CreateSession(store.Session{
		ID:         staleSID,
		UserID:     alice.ID,
		Name:       "alice",
		StartedAt:  1,
		LastSeenAt: 1,
		Alive:      true,
		ClientIP:   "1.2.3.4",
	}); err != nil {
		t.Fatalf("seed stale session: %v", err)
	}
	if got, ok := s.sess.Get("alice", staleSID); ok {
		t.Fatalf("precondition: stale sid should NOT be in the live map, got %v", got)
	}

	// Attach — must revive, not 404.
	p, sid, status := s.ensureSession(alice, staleSID, httptest.NewRequest("GET", "/", nil))
	if status != 200 {
		t.Fatalf("status = %d, want 200 (revive)", status)
	}
	if sid != staleSID {
		t.Fatalf("revive changed sid: %q, want %q", sid, staleSID)
	}
	if p == nil {
		t.Fatal("revived PTY is nil")
	}
	// The revived PTY must be live in the manager under the SAME id.
	if got, ok := s.sess.Get("alice", staleSID); !ok || got != p {
		t.Fatalf("manager.Get after revive: ok=%v, match=%v", ok, got == p)
	}
	// Exactly one PTY was built (the revive path builds one; it must not also
	// leave a phantom create behind).
	if n := len(s.createdPTYs()); n != 1 {
		t.Fatalf("PTYs built = %d, want 1", n)
	}
}

// TestEnsureSessionCapReachedReturns409 sets max_sessions=0 then 1, creates
// one, and verifies the next create yields 409.
func TestEnsureSessionCapReachedReturns409(t *testing.T) {
	s := newTestServer(t)
	alice, err := s.db.GetUserByUsername("alice")
	if err != nil {
		t.Fatalf("get alice: %v", err)
	}
	if err := s.db.SetUserMaxSessions(alice.ID, 1); err != nil {
		t.Fatalf("set max sessions: %v", err)
	}
	if _, _, st := s.ensureSession(alice, "", httptest.NewRequest("GET", "/", nil)); st != 200 {
		t.Fatalf("1st create status=%d, want 200", st)
	}
	_, _, status := s.ensureSession(alice, "", httptest.NewRequest("GET", "/", nil))
	if status != 409 {
		t.Fatalf("cap-reached status = %d, want 409", status)
	}
}

// TestEnsureSessionStartsDeadPTY verifies the lazy-start: if a PTY exists but
// is NOT alive, ensureSession re-starts it.
func TestEnsureSessionStartsDeadPTY(t *testing.T) {
	s := newTestServer(t)
	alice, err := s.db.GetUserByUsername("alice")
	if err != nil {
		t.Fatalf("get alice: %v", err)
	}
	p, sid, _ := s.ensureSession(alice, "", httptest.NewRequest("GET", "/", nil))
	fp := p.(*fakePTY)
	if atomic.LoadInt32(&fp.startCnt) != 1 {
		t.Fatalf("Start called %d, want 1", atomic.LoadInt32(&fp.startCnt))
	}
	// Kill the underlying process (simulating a natural exit / earlier kill).
	fp.Stop()
	if p.Alive() {
		t.Fatal("pty should be dead after Stop")
	}
	// ensureSession on the same id must lazy-restart.
	if _, _, st := s.ensureSession(alice, sid, httptest.NewRequest("GET", "/", nil)); st != 200 {
		t.Fatalf("re-attach status=%d, want 200", st)
	}
	if atomic.LoadInt32(&fp.startCnt) != 2 {
		t.Fatalf("Start called %d, want 2 (lazy restart)", atomic.LoadInt32(&fp.startCnt))
	}
}

// TestEnsureSessionReviveRejectsOtherUsersSID verifies the revive path does NOT
// let user B attach to a DB-persisted session owned by user A. Without the
// ownership check in ensureSession, B could probe/revive A's session id by
// replaying it after a restart. The fix checks row.UserID == u.ID before
// reviving; a mismatch yields 404 (same as unknown — never leak existence).
func TestEnsureSessionReviveRejectsOtherUsersSID(t *testing.T) {
	s := newTestServer(t)
	alice, err := s.db.GetUserByUsername("alice")
	if err != nil {
		t.Fatalf("get alice: %v", err)
	}
	// Second user "bob".
	h, err := auth.HashPassword("pw123")
	if err != nil {
		t.Fatalf("hash: %v", err)
	}
	if _, err := s.db.CreateUser(store.User{
		UID: mustUID(t, s.db), Username: "bob", PasswordHash: h, Role: "user", CreatedAt: 1,
	}); err != nil {
		t.Fatalf("create bob: %v", err)
	}
	bob, err := s.db.GetUserByUsername("bob")
	if err != nil {
		t.Fatalf("get bob: %v", err)
	}

	// A session owned by alice, no live PTY (restart state).
	const aliceSID = "alice-owned-sid"
	if err := s.db.CreateSession(store.Session{
		ID: aliceSID, UserID: alice.ID, Name: "alice", StartedAt: 1, LastSeenAt: 1, Alive: true,
	}); err != nil {
		t.Fatalf("seed: %v", err)
	}

	// Bob tries to attach alice's sid → must 404, NOT revive.
	_, _, status := s.ensureSession(bob, aliceSID, httptest.NewRequest("GET", "/", nil))
	if status != 404 {
		t.Fatalf("cross-user attach status = %d, want 404", status)
	}
	// And alice's session must NOT have been revived into bob's namespace.
	if got, ok := s.sess.Get("bob", aliceSID); ok {
		t.Fatalf("bob must not have a live PTY for alice's sid, got %v", got)
	}
}

// TestAuthWSUserRejectsSuspended re-confirms the WS auth gate survives the
// rewrite (a stale-but-valid cookie on a suspended user must NOT pass).
func TestAuthWSUserRejectsSuspended(t *testing.T) {
	s := newTestServer(t)
	cookie := loginAsAlice(t, s)
	alice, err := s.db.GetUserByUsername("alice")
	if err != nil {
		t.Fatalf("get alice: %v", err)
	}
	if err := s.db.SetSuspended(alice.ID, true); err != nil {
		t.Fatalf("suspend: %v", err)
	}
	req := httptest.NewRequest("GET", "/ws/terminal", nil)
	req.AddCookie(&http.Cookie{Name: "session", Value: cookie})
	if _, ok := s.authWSUser(req); ok {
		t.Fatal("authWSUser allowed a suspended user")
	}
}

// Compile-time guard: fakePTY must satisfy sessions.PTY so the injected
// factory stays valid if the interface drifts.
var _ sessions.PTY = (*fakePTY)(nil)

// TestClientIP_Priority verifies CF-Connecting-IP wins over X-Real-IP,
// X-Forwarded-For, and RemoteAddr.
func TestClientIP_Priority(t *testing.T) {
	s := newTestServer(t)
	cases := []struct {
		name   string
		headers map[string]string
		remote string
		want   string
	}{
		{"cf", map[string]string{"CF-Connecting-IP": "1.1.1.1"}, "9.9.9.9:1", "1.1.1.1"},
		{"xreal", map[string]string{"X-Real-IP": "2.2.2.2"}, "9.9.9.9:1", "2.2.2.2"},
		{"xff", map[string]string{"X-Forwarded-For": "3.3.3.3, 8.8.8.8"}, "9.9.9.9:1", "3.3.3.3"},
		{"remote", map[string]string{}, "9.9.9.9:1234", "9.9.9.9"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			req := httptest.NewRequest("GET", "/health", nil)
			req.RemoteAddr = c.remote
			for k, v := range c.headers {
				req.Header.Set(k, v)
			}
			if got := s.clientIP(req); got != c.want {
				t.Errorf("got %q, want %q", got, c.want)
			}
		})
	}
}

// TestLogin_WritesAuditEvent verifies a successful login writes a login_event
// (success=1) and updates last_login_ip; a failed login writes success=0 with
// the attempted username even for an unknown user.
func TestLogin_WritesAuditEvent(t *testing.T) {
	s := newTestServer(t)

	// Successful login.
	req := httptest.NewRequest("POST", "/auth", strings.NewReader(`{"username":"alice","password":"pw123"}`))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("CF-Connecting-IP", "203.0.113.10")
	w := httptest.NewRecorder()
	s.Routes().ServeHTTP(w, req)
	if w.Code != 200 {
		t.Fatalf("login: %d", w.Code)
	}
	u, _ := s.db.GetUserByUsername("alice")
	if u.LastLoginIP != "203.0.113.10" {
		t.Errorf("LastLoginIP = %q, want 203.0.113.10", u.LastLoginIP)
	}
	evs, err := s.db.ListLoginEvents(10)
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(evs) != 1 || !evs[0].Success || evs[0].IP != "203.0.113.10" {
		t.Fatalf("event mismatch: %+v", evs)
	}

	// Failed login for unknown user → success=0, username recorded, user_id 0.
	req2 := httptest.NewRequest("POST", "/auth", strings.NewReader(`{"username":"ghost","password":"x"}`))
	req2.Header.Set("Content-Type", "application/json")
	req2.Header.Set("CF-Connecting-IP", "203.0.113.11")
	w2 := httptest.NewRecorder()
	s.Routes().ServeHTTP(w2, req2)
	if w2.Code != 401 {
		t.Fatalf("want 401, got %d", w2.Code)
	}
	evs2, _ := s.db.ListLoginEvents(10)
	if len(evs2) != 2 {
		t.Fatalf("want 2 events, got %d", len(evs2))
	}
	fail := evs2[0] // newest first
	if fail.Success || fail.Username != "ghost" || fail.UserID != 0 || fail.IP != "203.0.113.11" {
		t.Errorf("fail event mismatch: %+v", fail)
	}
}
