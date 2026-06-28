package server

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"
	"time"

	"github.com/ldm0206/claude-docker/backend/internal/auth"
	"github.com/ldm0206/claude-docker/backend/internal/config"
	"github.com/ldm0206/claude-docker/backend/internal/quota"
	"github.com/ldm0206/claude-docker/backend/internal/sessions"
	"github.com/ldm0206/claude-docker/backend/internal/store"
)

// ---------------------------------------------------------------------------
// Testability seams
//
// Approach (chosen per the task brief option (a)): the Server holds concrete
// *quota.Service / *traffic.Service pointers and treats nil as "services
// unavailable" — every call site guards nil and returns zeros / no-ops. This
// keeps the Server struct lean (no per-method interface proliferation) while
// staying Windows-testable:
//
//   - For usage/disk numbers we inject a REAL *quota.Service whose DiskUsageProvider
//     is a fakeDiskUsage (the seam already exists in the quota package), so
//     CheckDisk returns deterministic used/over values without shelling out.
//   - For suspend→RemoveCgroup we inject a REAL *quota.Service whose CgroupWriter
//     is a fakeCgroupWriter that records Remove calls — observable without
//     touching /sys/fs/cgroup.
//   - The nil-graceful path is covered by tests that pass nil quota/traffic and
//     assert the usage payload comes back with zeros.
// ---------------------------------------------------------------------------

// fakeDiskUsage implements quota.DiskUsageProvider. It returns a fixed byte
// count (and no error) so quota.Service.CheckDisk is deterministic in tests.
type fakeDiskUsage struct {
	bytes int64
	err   error
}

func (f fakeDiskUsage) Usage(homeRoot, username string) (int64, error) {
	if f.err != nil {
		return 0, f.err
	}
	return f.bytes, nil
}

// fakeCgroupWriter implements quota.CgroupWriter. It records Apply/Remove calls
// so tests can assert that suspend invoked RemoveCgroup.
type fakeCgroupWriter struct {
	applies []applyCall
	removes []int
	applyEr error
	rmErr   error
}

type applyCall struct {
	uid      int
	cpuQuota string
	memMax   int64
}

func (f *fakeCgroupWriter) Apply(uid int, cpuQuota string, memMax int64) error {
	f.applies = append(f.applies, applyCall{uid, cpuQuota, memMax})
	return f.applyEr
}
func (f *fakeCgroupWriter) Remove(uid int) error {
	f.removes = append(f.removes, uid)
	return f.rmErr
}

// ---------------------------------------------------------------------------
// newTestServerWithQuota: like newTestServerWithAdmin but wires a *quota.Service
// with fake providers. Returns the fakes alongside the server so tests can
// assert against them. traffic is left nil here (the usage endpoint's traffic
// block reads only from the store, so a nil traffic.Service is fine).
// ---------------------------------------------------------------------------

func newTestServerWithQuota(t *testing.T, diskBytes int64, diskLimit int64) (
	*Server, *fakeProvisioner, *fakeCgroupWriter, *store.DB,
) {
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
	// alice — regular user with a role template carrying the disk limit, so
	// db.EffectiveDiskQuota(alice.ID) returns diskLimit.
	tmpl, err := db.CreateTemplate(store.RoleTemplate{
		Name: "t", DiskQuotaBytes: diskLimit, CPUQuota: "1.0", MaxSessions: 3, Permissions: "{}",
	})
	if err != nil {
		t.Fatalf("create template: %v", err)
	}
	alice, err := db.CreateUser(store.User{
		UID: 2000, Username: "alice", PasswordHash: h, Role: "user", CreatedAt: 1,
	})
	if err != nil {
		t.Fatalf("create alice: %v", err)
	}
	if err := db.BindTemplate(alice.ID, tmpl.ID); err != nil {
		t.Fatalf("bind template: %v", err)
	}
	// bob — admin
	if _, err := db.CreateUser(store.User{
		UID: 2001, Username: "bob", PasswordHash: h, Role: "admin", CreatedAt: 1,
	}); err != nil {
		t.Fatalf("create bob: %v", err)
	}

	disk := fakeDiskUsage{bytes: diskBytes}
	cg := &fakeCgroupWriter{}
	qsvc := quota.New(disk, cg, "/home")
	cfg := &config.Config{SessionSecret: "s", Port: 0}
	mgr := newFakePTYFactoryForAdminAsManager()
	srv := New(cfg, db, &fakeProvisioner{}, mgr, nil, qsvc, nil)
	return srv, srv.provisioner.(*fakeProvisioner), cg, db
}

// ---------------------------------------------------------------------------
// GET /api/admin/users/:id/usage
// ---------------------------------------------------------------------------

func TestAdminUsage_ReturnsShape(t *testing.T) {
	// disk limit 1000, used 1500 → over=true
	s, _, _, db := newTestServerWithQuota(t, 1500, 1000)
	alice, _ := db.GetUserByUsername("alice")

	// Seed some traffic + a session so the traffic/sessions blocks are non-zero.
	month := time.Now().Format("2006-01")
	if err := db.AddTraffic(alice.ID, month, 500, 250); err != nil {
		t.Fatalf("AddTraffic: %v", err)
	}
	if err := db.CreateSession(store.Session{
		ID: "s1", UserID: alice.ID, Name: "alice", StartedAt: 1, LastSeenAt: 1, Alive: true,
	}); err != nil {
		t.Fatalf("CreateSession: %v", err)
	}

	req := httptest.NewRequest("GET",
		fmt.Sprintf("/api/admin/users/%d/usage", alice.ID), nil)
	req.AddCookie(&http.Cookie{Name: "session", Value: adminCookie(t, s)})
	w := httptest.NewRecorder()
	s.Routes().ServeHTTP(w, req)
	if w.Code != 200 {
		t.Fatalf("expected 200, got %d; body=%s", w.Code, w.Body.String())
	}

	var resp map[string]any
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	disk, _ := resp["disk"].(map[string]any)
	if disk == nil {
		t.Fatalf("missing disk block: %+v", resp)
	}
	if used, _ := disk["used"].(float64); int64(used) != 1500 {
		t.Fatalf("disk.used: expected 1500, got %v", disk["used"])
	}
	if lim, _ := disk["limit"].(float64); int64(lim) != 1000 {
		t.Fatalf("disk.limit: expected 1000, got %v", disk["limit"])
	}
	if over, _ := disk["over"].(bool); !over {
		t.Fatalf("disk.over: expected true, got %v", disk["over"])
	}

	tf, _ := resp["traffic"].(map[string]any)
	if tf == nil {
		t.Fatalf("missing traffic block: %+v", resp)
	}
	if tf["month"] != month {
		t.Fatalf("traffic.month: expected %s, got %v", month, tf["month"])
	}
	if rx, _ := tf["rx"].(float64); int64(rx) != 500 {
		t.Fatalf("traffic.rx: expected 500, got %v", tf["rx"])
	}
	if tx, _ := tf["tx"].(float64); int64(tx) != 250 {
		t.Fatalf("traffic.tx: expected 250, got %v", tf["tx"])
	}

	sess, _ := resp["sessions"].(map[string]any)
	if sess == nil {
		t.Fatalf("missing sessions block: %+v", resp)
	}
	if alive, _ := sess["alive"].(float64); int(alive) != 1 {
		t.Fatalf("sessions.alive: expected 1, got %v", sess["alive"])
	}
	if total, _ := sess["total"].(float64); int(total) != 1 {
		t.Fatalf("sessions.total: expected 1, got %v", sess["total"])
	}
}

func TestAdminUsage_NotFound(t *testing.T) {
	s, _, _, _ := newTestServerWithQuota(t, 0, 0)
	req := httptest.NewRequest("GET", "/api/admin/users/9999/usage", nil)
	req.AddCookie(&http.Cookie{Name: "session", Value: adminCookie(t, s)})
	w := httptest.NewRecorder()
	s.Routes().ServeHTTP(w, req)
	if w.Code != 404 {
		t.Fatalf("expected 404, got %d; body=%s", w.Code, w.Body.String())
	}
}

func TestAdminUsage_NonAdmin_Forbidden(t *testing.T) {
	s, _, _, _ := newTestServerWithQuota(t, 0, 0)
	req := httptest.NewRequest("GET", "/api/admin/users/1/usage", nil)
	req.AddCookie(&http.Cookie{Name: "session", Value: userCookie(t, s)})
	w := httptest.NewRecorder()
	s.Routes().ServeHTTP(w, req)
	if w.Code != 403 {
		t.Fatalf("expected 403, got %d", w.Code)
	}
}

// TestAdminUsage_GracefulNilQuota verifies the usage endpoint returns zeros for
// the disk block when s.quota is nil (T6 default until T7 wires the service).
func TestAdminUsage_GracefulNilQuota(t *testing.T) {
	s, _, db := newTestServerWithAdmin(t) // no quota wired
	alice, _ := db.GetUserByUsername("alice")
	// alice has no template → EffectiveDiskQuota returns 0 (no limit).
	req := httptest.NewRequest("GET",
		fmt.Sprintf("/api/admin/users/%d/usage", alice.ID), nil)
	req.AddCookie(&http.Cookie{Name: "session", Value: adminCookie(t, s)})
	w := httptest.NewRecorder()
	s.Routes().ServeHTTP(w, req)
	if w.Code != 200 {
		t.Fatalf("expected 200, got %d; body=%s", w.Code, w.Body.String())
	}
	var resp map[string]any
	_ = json.NewDecoder(w.Body).Decode(&resp)
	disk, _ := resp["disk"].(map[string]any)
	if disk == nil {
		t.Fatalf("missing disk block: %+v", resp)
	}
	if used, _ := disk["used"].(float64); used != 0 {
		t.Fatalf("nil quota: disk.used expected 0, got %v", disk["used"])
	}
	if over, _ := disk["over"].(bool); over {
		t.Fatalf("nil quota: disk.over expected false, got %v", disk["over"])
	}
}

// ---------------------------------------------------------------------------
// POST /api/admin/users/:id/reset-traffic
// ---------------------------------------------------------------------------

func TestAdminResetTraffic_ZeroesCurrentMonth(t *testing.T) {
	s, _, _, db := newTestServerWithQuota(t, 0, 0)
	alice, _ := db.GetUserByUsername("alice")
	month := time.Now().Format("2006-01")
	if err := db.AddTraffic(alice.ID, month, 800, 400); err != nil {
		t.Fatalf("AddTraffic: %v", err)
	}

	req := httptest.NewRequest("POST",
		fmt.Sprintf("/api/admin/users/%d/reset-traffic", alice.ID), nil)
	req.AddCookie(&http.Cookie{Name: "session", Value: adminCookie(t, s)})
	w := httptest.NewRecorder()
	s.Routes().ServeHTTP(w, req)
	if w.Code != 200 {
		t.Fatalf("expected 200, got %d; body=%s", w.Code, w.Body.String())
	}
	rx, tx, err := db.GetTraffic(alice.ID, month)
	if err != nil {
		t.Fatalf("GetTraffic: %v", err)
	}
	if rx != 0 || tx != 0 {
		t.Fatalf("expected 0/0 after reset, got %d/%d", rx, tx)
	}
}

func TestAdminResetTraffic_NotFound(t *testing.T) {
	s, _, _, _ := newTestServerWithQuota(t, 0, 0)
	req := httptest.NewRequest("POST", "/api/admin/users/9999/reset-traffic", nil)
	req.AddCookie(&http.Cookie{Name: "session", Value: adminCookie(t, s)})
	w := httptest.NewRecorder()
	s.Routes().ServeHTTP(w, req)
	if w.Code != 404 {
		t.Fatalf("expected 404, got %d", w.Code)
	}
}

func TestAdminResetTraffic_NonAdmin_Forbidden(t *testing.T) {
	s, _, _, _ := newTestServerWithQuota(t, 0, 0)
	req := httptest.NewRequest("POST", "/api/admin/users/1/reset-traffic", nil)
	req.AddCookie(&http.Cookie{Name: "session", Value: userCookie(t, s)})
	w := httptest.NewRecorder()
	s.Routes().ServeHTTP(w, req)
	if w.Code != 403 {
		t.Fatalf("expected 403, got %d", w.Code)
	}
}

// ---------------------------------------------------------------------------
// GET /api/admin/traffic
// ---------------------------------------------------------------------------

func TestAdminTraffic_AggregateByMonth(t *testing.T) {
	s, _, _, db := newTestServerWithQuota(t, 0, 0)
	alice, _ := db.GetUserByUsername("alice")
	bob, _ := db.GetUserByUsername("bob")
	if err := db.AddTraffic(alice.ID, "2026-06", 100, 50); err != nil {
		t.Fatalf("AddTraffic alice: %v", err)
	}
	if err := db.AddTraffic(bob.ID, "2026-06", 200, 100); err != nil {
		t.Fatalf("AddTraffic bob: %v", err)
	}
	// Other month excluded from the 2026-06 aggregate.
	if err := db.AddTraffic(alice.ID, "2026-05", 999, 999); err != nil {
		t.Fatalf("AddTraffic alice May: %v", err)
	}

	req := httptest.NewRequest("GET", "/api/admin/traffic?month=2026-06", nil)
	req.AddCookie(&http.Cookie{Name: "session", Value: adminCookie(t, s)})
	w := httptest.NewRecorder()
	s.Routes().ServeHTTP(w, req)
	if w.Code != 200 {
		t.Fatalf("expected 200, got %d; body=%s", w.Code, w.Body.String())
	}
	var resp map[string]any
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if resp["month"] != "2026-06" {
		t.Fatalf("month: expected 2026-06, got %v", resp["month"])
	}
	if rx, _ := resp["totalRx"].(float64); int64(rx) != 300 {
		t.Fatalf("totalRx: expected 300, got %v", resp["totalRx"])
	}
	if tx, _ := resp["totalTx"].(float64); int64(tx) != 150 {
		t.Fatalf("totalTx: expected 150, got %v", resp["totalTx"])
	}
}

func TestAdminTraffic_PerUserRows(t *testing.T) {
	s, _, _, db := newTestServerWithQuota(t, 0, 0)
	alice, _ := db.GetUserByUsername("alice")
	if err := db.AddTraffic(alice.ID, "2026-06", 100, 50); err != nil {
		t.Fatalf("AddTraffic: %v", err)
	}
	if err := db.AddTraffic(alice.ID, "2026-05", 10, 5); err != nil {
		t.Fatalf("AddTraffic May: %v", err)
	}

	req := httptest.NewRequest("GET",
		fmt.Sprintf("/api/admin/traffic?user=%d", alice.ID), nil)
	req.AddCookie(&http.Cookie{Name: "session", Value: adminCookie(t, s)})
	w := httptest.NewRecorder()
	s.Routes().ServeHTTP(w, req)
	if w.Code != 200 {
		t.Fatalf("expected 200, got %d; body=%s", w.Code, w.Body.String())
	}
	var rows []map[string]any
	if err := json.NewDecoder(w.Body).Decode(&rows); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(rows) != 2 {
		t.Fatalf("expected 2 rows, got %d", len(rows))
	}
}

func TestAdminTraffic_DefaultsToCurrentMonth(t *testing.T) {
	s, _, _, _ := newTestServerWithQuota(t, 0, 0)
	month := time.Now().Format("2006-01")
	req := httptest.NewRequest("GET", "/api/admin/traffic", nil)
	req.AddCookie(&http.Cookie{Name: "session", Value: adminCookie(t, s)})
	w := httptest.NewRecorder()
	s.Routes().ServeHTTP(w, req)
	if w.Code != 200 {
		t.Fatalf("expected 200, got %d; body=%s", w.Code, w.Body.String())
	}
	var resp map[string]any
	_ = json.NewDecoder(w.Body).Decode(&resp)
	if resp["month"] != month {
		t.Fatalf("expected current month %s, got %v", month, resp["month"])
	}
}

func TestAdminTraffic_NonAdmin_Forbidden(t *testing.T) {
	s, _, _, _ := newTestServerWithQuota(t, 0, 0)
	req := httptest.NewRequest("GET", "/api/admin/traffic", nil)
	req.AddCookie(&http.Cookie{Name: "session", Value: userCookie(t, s)})
	w := httptest.NewRecorder()
	s.Routes().ServeHTTP(w, req)
	if w.Code != 403 {
		t.Fatalf("expected 403, got %d", w.Code)
	}
}

// ---------------------------------------------------------------------------
// Suspend now also reclaims the cgroup (T6)
// ---------------------------------------------------------------------------

// TestAdminSuspend_ReclaimsCgroup verifies that suspend calls
// quota.RemoveCgroup(uid) — the NEW T6 behavior. (KillAll's PTY/DB effect is
// already covered by the sessions package's TestManagerKillAllKillsAllForUser;
// Lock + SetSuspended are covered by admin_users_test.go. Here we assert only
// the new cgroup-reclamation call, observable via the fake CgroupWriter.)
func TestAdminSuspend_ReclaimsCgroup(t *testing.T) {
	s, _, cg, db := newTestServerWithQuota(t, 0, 0)
	// Create a target user with a known uid so we can assert RemoveCgroup(uid).
	target, err := db.CreateUser(store.User{
		UID: 7777, Username: "target", PasswordHash: "x", Role: "user", CreatedAt: 1,
	})
	if err != nil {
		t.Fatalf("create target: %v", err)
	}

	req := httptest.NewRequest("POST",
		fmt.Sprintf("/api/admin/users/%d/suspend", target.ID), nil)
	req.AddCookie(&http.Cookie{Name: "session", Value: adminCookie(t, s)})
	w := httptest.NewRecorder()
	s.Routes().ServeHTTP(w, req)
	if w.Code != 200 {
		t.Fatalf("suspend: expected 200, got %d; body=%s", w.Code, w.Body.String())
	}

	// cgroup reclamation: Remove called with the user's uid.
	if len(cg.removes) != 1 || cg.removes[0] != 7777 {
		t.Fatalf("expected RemoveCgroup(7777), got %v", cg.removes)
	}
	// And the user is suspended in the DB (sanity — existing behavior preserved).
	u, _ := db.GetUserByID(target.ID)
	if !u.Suspended {
		t.Fatal("expected suspended=true in DB")
	}
}

// ---------------------------------------------------------------------------
// helpers
// ---------------------------------------------------------------------------

// newFakePTYFactoryForAdminAsManager wraps newFakePTYFactoryForAdmin into a
// *sessions.Manager so it can be passed to the extended New(). The admin/quota
// tests never create real sessions, so the factory is never invoked.
func newFakePTYFactoryForAdminAsManager() *sessions.Manager {
	return sessions.NewManager(nil, newFakePTYFactoryForAdmin())
}
