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
	"github.com/ldm0206/claude-docker/backend/internal/capture"
	"github.com/ldm0206/claude-docker/backend/internal/config"
	"github.com/ldm0206/claude-docker/backend/internal/sessions"
	"github.com/ldm0206/claude-docker/backend/internal/store"
	"github.com/ldm0206/claude-docker/backend/internal/system"
)

// ---------------------------------------------------------------------------
// P5-T3: per-session MITM env routing + admin capture enable/disable API
// ---------------------------------------------------------------------------
//
// These tests cover the two halves of Task 3:
//  1. Env routing — when capture is enabled for a session, the PTY env gains
//     HTTP_PROXY/HTTPS_PROXY (+lower) = the MITM proxy URL and drops
//     ALL_PROXY/all_proxy (so claude can't bypass via SOCKS). When disabled,
//     the proxy env is omitted.
//  2. Admin API — POST /api/admin/sessions/:id/capture/{enable,disable} toggle
//     the flag, (lazily) start/stop the proxy runner, and Restart the session's
//     PTY so the lazy env factory re-reads the flag.
//
// We use the REAL *capture.Service (T2) with a tiny local fakeRunner rather than
// the capture-package-internal fakeRunner (test files can't reach a package-
// internal type). This keeps the wiring a black box: the server only sees
// *capture.Service + the sessions.Manager.

// p3FakeRunner is a test double for capture.ProxyRunner. Records Start/Stop and
// can be primed to fail Start. Mirrors capture.fakeRunner (package-internal).
type p3FakeRunner struct {
	mu       sync.Mutex
	starts   int32
	stops    int32
	addr     string
	running  bool
	startErr error
}

func (f *p3FakeRunner) Start(addr string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	atomic.AddInt32(&f.starts, 1)
	f.addr = addr
	if f.startErr != nil {
		return f.startErr
	}
	f.running = true
	return nil
}
func (f *p3FakeRunner) Stop() error {
	f.mu.Lock()
	defer f.mu.Unlock()
	atomic.AddInt32(&f.stops, 1)
	f.running = false
	return nil
}
func (f *p3FakeRunner) Running() bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.running
}

// newCaptureTestServer builds a testServer wired with:
//   - a real *capture.Service backed by a p3FakeRunner (port 8888);
//   - the usual sessions.Manager + fakePTY factory;
//   - alice (role user) + bob (role admin), so we can exercise the admin gate.
//
// It returns the server, the fake runner (for Start/Stop assertions), and the
// capture.Service (for direct IsEnabled / ProxyURL assertions in tests).
func newCaptureTestServer(t *testing.T) (*testServer, *p3FakeRunner, *capture.Service) {
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
	// alice — regular user; bob — admin (loginAs helper expects "pw123").
	if _, err := db.CreateUser(store.User{
		UID: mustUID(t, db), Username: "alice", PasswordHash: h, Role: "user", CreatedAt: 1,
	}); err != nil {
		t.Fatalf("create alice: %v", err)
	}
	if _, err := db.CreateUser(store.User{
		UID: mustUID(t, db), Username: "bob", PasswordHash: h, Role: "admin", CreatedAt: 1,
	}); err != nil {
		t.Fatalf("create bob: %v", err)
	}
	cfg := &config.Config{SessionSecret: "s", Port: 0}
	factory, created := newFakePTYFactory()
	mgr := sessions.NewManager(db, factory)

	fr := &p3FakeRunner{}
	cvsvc := capture.NewService(fr, capture.NewStore(), db, 8888)
	srv := New(cfg, db, system.DefaultProvisioner, mgr, nil, nil, cvsvc)
	return &testServer{Server: srv, createdPTYs: created}, fr, cvsvc
}

// envLine reports whether the env slice contains exactly "key=value".
func envLine(env []string, key, value string) bool {
	want := key + "=" + value
	for _, e := range env {
		if e == want {
			return true
		}
	}
	return false
}

// envHasPrefix reports whether the env slice contains any "key=..." entry.
func envHasPrefix(env []string, key string) bool {
	prefix := key + "="
	for _, e := range env {
		if strings.HasPrefix(e, prefix) {
			return true
		}
	}
	return false
}

// ---------------------------------------------------------------------------
// Env routing
// ---------------------------------------------------------------------------

// TestCaptureEnvRouting_EnableAddsProxyDropsAllProxy proves the env factory
// closure, when capture is enabled for the session, yields an env slice with
// HTTP_PROXY/HTTPS_PROXY (+lower) == the proxy URL and NO ALL_PROXY/all_proxy.
// We drive the env factory directly (the same closure the sessions.Manager
// wraps into opts.Env) — that is the unit under test. (The real *pty.Manager
// re-invokes opts.Env() on every Start; the fake materializes it at construct
// time, which is why we call the closure directly here.)
func TestCaptureEnvRouting_EnableAddsProxyDropsAllProxy(t *testing.T) {
	s, _, cvsvc := newCaptureTestServer(t)
	alice, err := s.db.GetUserByUsername("alice")
	if err != nil {
		t.Fatalf("get alice: %v", err)
	}
	const sessionID = "sess-env-1"
	if err := cvsvc.Enable(sessionID, alice.ID); err != nil {
		t.Fatalf("enable capture: %v", err)
	}

	env := s.buildUserEnvFactory(alice)(alice.Username, sessionID)
	wantURL := cvsvc.ProxyURL()
	for _, k := range []string{"HTTP_PROXY", "HTTPS_PROXY", "http_proxy", "https_proxy"} {
		if !envLine(env, k, wantURL) {
			t.Fatalf("env missing %s=%s; env=%v", k, wantURL, env)
		}
	}
	for _, k := range []string{"ALL_PROXY", "all_proxy"} {
		if envHasPrefix(env, k) {
			t.Fatalf("env must NOT contain %s when capture is on; env=%v", k, env)
		}
	}
}

// TestCaptureEnvRouting_DisableOmitsProxy proves that when capture is OFF for
// the session, the env factory omits the proxy URL (no HTTP_PROXY/HTTPS_PROXY
// from the routing layer) — and that disabling a previously-enabled session
// flips the env back to the un-routed state.
func TestCaptureEnvRouting_DisableOmitsProxy(t *testing.T) {
	s, _, cvsvc := newCaptureTestServer(t)
	alice, err := s.db.GetUserByUsername("alice")
	if err != nil {
		t.Fatalf("get alice: %v", err)
	}
	const sessionID = "sess-env-2"
	// Start enabled, then disable — the factory must drop the proxy URL.
	if err := cvsvc.Enable(sessionID, alice.ID); err != nil {
		t.Fatalf("enable: %v", err)
	}
	cvsvc.Disable(sessionID)

	env := s.buildUserEnvFactory(alice)(alice.Username, sessionID)
	wantURL := cvsvc.ProxyURL()
	for _, k := range []string{"HTTP_PROXY", "HTTPS_PROXY", "http_proxy", "https_proxy"} {
		if envLine(env, k, wantURL) {
			t.Fatalf("disabled-session env must NOT contain %s=%s; env=%v", k, wantURL, env)
		}
	}
}

// TestCaptureEnvRouting_PerSessionIsolation proves the flag is per-session:
// enabling session A does NOT route session B's env.
func TestCaptureEnvRouting_PerSessionIsolation(t *testing.T) {
	s, _, cvsvc := newCaptureTestServer(t)
	alice, err := s.db.GetUserByUsername("alice")
	if err != nil {
		t.Fatalf("get alice: %v", err)
	}
	if err := cvsvc.Enable("sess-A", alice.ID); err != nil {
		t.Fatalf("enable A: %v", err)
	}
	// B is not enabled → its env must NOT carry the proxy URL.
	envB := s.buildUserEnvFactory(alice)(alice.Username, "sess-B")
	wantURL := cvsvc.ProxyURL()
	for _, k := range []string{"HTTP_PROXY", "HTTPS_PROXY", "http_proxy", "https_proxy"} {
		if envLine(envB, k, wantURL) {
			t.Fatalf("session B env must NOT contain %s=%s (capture is on for A only); env=%v", k, wantURL, envB)
		}
	}
}

// ---------------------------------------------------------------------------
// Admin API: enable / disable
// ---------------------------------------------------------------------------

// createAliveSession is a small helper that creates one live session for alice
// and returns its id + the underlying fakePTY. It mirrors what the WS handler
// does (ensureSession on the create path) but returns the PTY for assertions.
func createAliveSession(t *testing.T, s *testServer, username string) (string, *fakePTY) {
	t.Helper()
	u, err := s.db.GetUserByUsername(username)
	if err != nil {
		t.Fatalf("get %s: %v", username, err)
	}
	p, sid, status := s.ensureSession(u, "", httptest.NewRequest("GET", "/", nil))
	if status != 200 {
		t.Fatalf("ensureSession status=%d, want 200", status)
	}
	return sid, p.(*fakePTY)
}

// TestAdminCaptureEnable_RoutesEnvAndRestarts proves the full enable path:
//   - POST /api/admin/sessions/:id/capture/enable returns 200 with
//     {captureOn:true, captureUp:true, restarted:true};
//   - the session's PTY was Stop()ped then Start()ed (Restart);
//   - on the next env-factory invocation, the env carries the proxy URL and
//     has NO ALL_PROXY/all_proxy.
func TestAdminCaptureEnable_RoutesEnvAndRestarts(t *testing.T) {
	s, fr, cvsvc := newCaptureTestServer(t)
	sid, fp := createAliveSession(t, s, "alice")
	u, _ := s.db.GetUserByUsername("alice")

	startBefore := atomic.LoadInt32(&fp.startCnt)
	stopBefore := atomic.LoadInt32(&fp.stopCnt)

	req := httptest.NewRequest("POST", "/api/admin/sessions/"+sid+"/capture/enable", nil)
	req.AddCookie(&http.Cookie{Name: "session", Value: adminCookieFor(t, s.Server, "bob")})
	w := httptest.NewRecorder()
	s.Routes().ServeHTTP(w, req)

	if w.Code != 200 {
		t.Fatalf("enable status=%d, want 200; body=%s", w.Code, w.Body.String())
	}
	body := w.Body.String()
	for _, want := range []string{`"captureOn":true`, `"captureUp":true`, `"restarted":true`} {
		if !strings.Contains(body, want) {
			t.Fatalf("enable body missing %s: %s", want, body)
		}
	}
	// Proxy runner was started (lazily) by capture.Enable.
	if got := atomic.LoadInt32(&fr.starts); got != 1 {
		t.Fatalf("runner starts = %d, want 1", got)
	}
	// PTY was restarted: Stop + Start each bumped by 1.
	if got := atomic.LoadInt32(&fp.stopCnt); got != stopBefore+1 {
		t.Fatalf("PTY stop count = %d, want %d (restart)", got, stopBefore+1)
	}
	if got := atomic.LoadInt32(&fp.startCnt); got != startBefore+1 {
		t.Fatalf("PTY start count = %d, want %d (restart)", got, startBefore+1)
	}
	if !cvsvc.IsEnabled(sid) {
		t.Fatal("capture flag should be on for the session after enable")
	}

	// The lazy env factory must now route through the proxy for THIS session.
	env := s.buildUserEnvFactory(u)(u.Username, sid)
	wantURL := cvsvc.ProxyURL()
	for _, k := range []string{"HTTP_PROXY", "HTTPS_PROXY", "http_proxy", "https_proxy"} {
		if !envLine(env, k, wantURL) {
			t.Fatalf("post-enable env missing %s=%s; env=%v", k, wantURL, env)
		}
	}
	for _, k := range []string{"ALL_PROXY", "all_proxy"} {
		if envHasPrefix(env, k) {
			t.Fatalf("post-enable env must NOT contain %s; env=%v", k, env)
		}
	}
}

// TestAdminCaptureDisable_RemovesProxyAndRestarts proves the disable path:
//   - POST /api/admin/sessions/:id/capture/disable returns 200 with
//     {captureOn:false};
//   - the session's PTY was Stop()ped then Start()ed (Restart);
//   - on the next env-factory invocation, the proxy URL is gone.
func TestAdminCaptureDisable_RemovesProxyAndRestarts(t *testing.T) {
	s, _, cvsvc := newCaptureTestServer(t)
	sid, fp := createAliveSession(t, s, "alice")
	u, _ := s.db.GetUserByUsername("alice")

	// Enable first so the disable path has something to flip.
	if err := cvsvc.Enable(sid, u.ID); err != nil {
		t.Fatalf("enable pre-req: %v", err)
	}

	startBefore := atomic.LoadInt32(&fp.startCnt)
	stopBefore := atomic.LoadInt32(&fp.stopCnt)

	req := httptest.NewRequest("POST", "/api/admin/sessions/"+sid+"/capture/disable", nil)
	req.AddCookie(&http.Cookie{Name: "session", Value: adminCookieFor(t, s.Server, "bob")})
	w := httptest.NewRecorder()
	s.Routes().ServeHTTP(w, req)

	if w.Code != 200 {
		t.Fatalf("disable status=%d, want 200; body=%s", w.Code, w.Body.String())
	}
	if !strings.Contains(w.Body.String(), `"captureOn":false`) {
		t.Fatalf("disable body missing captureOn:false: %s", w.Body.String())
	}
	if got := atomic.LoadInt32(&fp.stopCnt); got != stopBefore+1 {
		t.Fatalf("PTY stop count = %d, want %d (restart)", got, stopBefore+1)
	}
	if got := atomic.LoadInt32(&fp.startCnt); got != startBefore+1 {
		t.Fatalf("PTY start count = %d, want %d (restart)", got, startBefore+1)
	}
	if cvsvc.IsEnabled(sid) {
		t.Fatal("capture flag should be off for the session after disable")
	}

	// Env factory must omit the proxy URL now.
	env := s.buildUserEnvFactory(u)(u.Username, sid)
	wantURL := cvsvc.ProxyURL()
	for _, k := range []string{"HTTP_PROXY", "HTTPS_PROXY", "http_proxy", "https_proxy"} {
		if envLine(env, k, wantURL) {
			t.Fatalf("post-disable env must NOT contain %s=%s; env=%v", k, wantURL, env)
		}
	}
}

// TestAdminCaptureEnable_ProxyStartFails Returns500 proves the failure path:
// when the proxy runner fails to start, capture.Enable returns an error and
// the API responds 500 with {captureOn:false, captureUp:false, restarted:false}
// and does NOT restart the PTY.
func TestAdminCaptureEnable_ProxyStartFailsReturns500(t *testing.T) {
	s, fr, cvsvc := newCaptureTestServer(t)
	// Prime the runner to fail the FIRST Start (the lazy start inside Enable).
	fr.startErr = errP3Boom
	sid, fp := createAliveSession(t, s, "alice")
	startBefore := atomic.LoadInt32(&fp.startCnt)
	stopBefore := atomic.LoadInt32(&fp.stopCnt)

	req := httptest.NewRequest("POST", "/api/admin/sessions/"+sid+"/capture/enable", nil)
	req.AddCookie(&http.Cookie{Name: "session", Value: adminCookieFor(t, s.Server, "bob")})
	w := httptest.NewRecorder()
	s.Routes().ServeHTTP(w, req)

	if w.Code != 500 {
		t.Fatalf("enable status=%d, want 500; body=%s", w.Code, w.Body.String())
	}
	body := w.Body.String()
	for _, want := range []string{`"captureOn":false`, `"captureUp":false`, `"restarted":false`} {
		if !strings.Contains(body, want) {
			t.Fatalf("enable-failure body missing %s: %s", want, body)
		}
	}
	// PTY must NOT have been restarted (the proxy never came up).
	if got := atomic.LoadInt32(&fp.stopCnt); got != stopBefore {
		t.Fatalf("PTY stop count = %d, want %d (no restart on failure)", got, stopBefore)
	}
	if got := atomic.LoadInt32(&fp.startCnt); got != startBefore {
		t.Fatalf("PTY start count = %d, want %d (no restart on failure)", got, startBefore)
	}
	if cvsvc.IsEnabled(sid) {
		t.Fatal("capture flag should be off after Enable failed")
	}
}

// TestAdminCaptureEnable_NonAdminForbidden proves the admin gate: a regular
// user hitting the enable endpoint gets 403, and capture is NOT enabled.
func TestAdminCaptureEnable_NonAdminForbidden(t *testing.T) {
	s, _, cvsvc := newCaptureTestServer(t)
	sid, _ := createAliveSession(t, s, "alice")

	req := httptest.NewRequest("POST", "/api/admin/sessions/"+sid+"/capture/enable", nil)
	req.AddCookie(&http.Cookie{Name: "session", Value: adminCookieFor(t, s.Server, "alice")}) // alice is role:user
	w := httptest.NewRecorder()
	s.Routes().ServeHTTP(w, req)

	if w.Code != 403 {
		t.Fatalf("non-admin enable status=%d, want 403; body=%s", w.Code, w.Body.String())
	}
	if cvsvc.IsEnabled(sid) {
		t.Fatal("capture must NOT be enabled when a non-admin calls enable")
	}
}

// TestAdminCaptureDisable_NonAdminForbidden mirrors the above for disable.
func TestAdminCaptureDisable_NonAdminForbidden(t *testing.T) {
	s, _, cvsvc := newCaptureTestServer(t)
	sid, _ := createAliveSession(t, s, "alice")
	u, _ := s.db.GetUserByUsername("alice")
	if err := cvsvc.Enable(sid, u.ID); err != nil { // pre-enable as a regular user
		t.Fatalf("pre-enable: %v", err)
	}

	req := httptest.NewRequest("POST", "/api/admin/sessions/"+sid+"/capture/disable", nil)
	req.AddCookie(&http.Cookie{Name: "session", Value: adminCookieFor(t, s.Server, "alice")})
	w := httptest.NewRecorder()
	s.Routes().ServeHTTP(w, req)

	if w.Code != 403 {
		t.Fatalf("non-admin disable status=%d, want 403; body=%s", w.Code, w.Body.String())
	}
	if !cvsvc.IsEnabled(sid) {
		t.Fatal("capture must still be on when a non-admin calls disable")
	}
}

// TestAdminCaptureEnable_UnknownSession404 proves the lookup path: enabling a
// session id that doesn't exist in the store returns 404, not 500.
func TestAdminCaptureEnable_UnknownSession404(t *testing.T) {
	s, _, _ := newCaptureTestServer(t)
	req := httptest.NewRequest("POST", "/api/admin/sessions/does-not-exist/capture/enable", nil)
	req.AddCookie(&http.Cookie{Name: "session", Value: adminCookieFor(t, s.Server, "bob")})
	w := httptest.NewRecorder()
	s.Routes().ServeHTTP(w, req)
	if w.Code != 404 {
		t.Fatalf("unknown-session enable status=%d, want 404; body=%s", w.Code, w.Body.String())
	}
}

// ---------------------------------------------------------------------------
// helpers
// ---------------------------------------------------------------------------

// errP3Boom is the sentinel Start error for the p3FakeRunner.
var errP3Boom = strError("p3 boom")

// strError is a helper so we don't import "errors" just for errors.New.
type strError string

func (e strError) Error() string { return string(e) }

// adminCookieFor logs in as username and returns the session cookie value.
// (Mirrors the admin_users_test.go adminCookie helper but takes the *Server
// so tests here can call it without the newTestServerWithAdmin wiring.)
func adminCookieFor(t *testing.T, s *Server, username string) string {
	t.Helper()
	body := `{"username":"` + username + `","password":"pw123"}`
	req := httptest.NewRequest("POST", "/auth", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	s.Routes().ServeHTTP(w, req)
	if w.Code != 200 {
		t.Fatalf("adminCookieFor %s failed: %d; body=%s", username, w.Code, w.Body.String())
	}
	for _, c := range w.Result().Cookies() {
		if c.Name == "session" {
			return c.Value
		}
	}
	t.Fatal("no session cookie from login")
	return ""
}

// Compile-time guards: p3FakeRunner satisfies capture.ProxyRunner; fakePTY
// still satisfies sessions.PTY (re-asserted in addition to server_test.go).
var (
	_ capture.ProxyRunner = (*p3FakeRunner)(nil)
	_ sessions.PTY        = (*fakePTY)(nil)
)
