package sessions

import (
	"errors"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/ldm0206/claude-docker/backend/internal/pty"
	"github.com/ldm0206/claude-docker/backend/internal/store"
)

// fakePTY implements PTY for tests. It records calls so tests can assert on them.
type fakePTY struct {
	opts     pty.Options
	startCnt int32
	stopCnt  int32
	mu       sync.Mutex
	alive    bool
	dataCbs  []func([]byte)
	exitCbs  []func(int)
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
func (f *fakePTY) Write(b []byte) error { return nil }
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

// factory tracks created fakes keyed by their sessionID (returned via opts).
func newFakeFactory(t *testing.T) (PTYFactory, func() []*fakePTY) {
	t.Helper()
	var mu sync.Mutex
	var created []*fakePTY
	factory := func(opts pty.Options) PTY {
		f := &fakePTY{opts: opts}
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

func mustOpenDB(t *testing.T) *store.DB {
	t.Helper()
	db, err := store.Open(filepath.Join(t.TempDir(), "t.db"))
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	t.Cleanup(func() { db.Close() })
	return db
}

func mustCreateUser(t *testing.T, db *store.DB, username string) (id int) {
	t.Helper()
	uid, err := db.AllocateUID()
	if err != nil {
		t.Fatalf("allocate uid: %v", err)
	}
	u, err := db.CreateUser(store.User{UID: uid, Username: username, PasswordHash: "x", Role: "user", CreatedAt: 1})
	if err != nil {
		t.Fatalf("create user: %v", err)
	}
	return u.ID
}

// envStub returns an EnvFactory that yields a stable env slice.
func envStub(_ string) []string { return []string{"PATH=/usr/bin"} }

func TestManagerCreateReturnsPTWithoutStart(t *testing.T) {
	db := mustOpenDB(t)
	uid := mustCreateUser(t, db, "alice")
	factory, created := newFakeFactory(t)
	mgr := NewManager(db, factory)

	id, p, err := mgr.Create("alice", uid, "/tmp", envStub, pty.Options{Cols: 80, Rows: 24})
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if id == "" {
		t.Fatal("empty session id")
	}
	if p == nil {
		t.Fatal("nil PTY returned")
	}
	// Create must NOT call Start (lazy-start is the caller's job, e.g. WS handler).
	if fp, ok := p.(*fakePTY); !ok || atomic.LoadInt32(&fp.startCnt) != 0 {
		t.Fatalf("Start was called by Create (startCnt=%d)", atomic.LoadInt32(&fp.startCnt))
	}
	if len(created()) != 1 {
		t.Fatalf("factory invoked %d times, want 1", len(created()))
	}

	// The row should be alive in the DB.
	row, err := db.GetSession(id)
	if err != nil {
		t.Fatalf("get session: %v", err)
	}
	if !row.Alive || row.UserID != uid {
		t.Fatalf("row = %+v, want alive+uid=%d", row, uid)
	}
}

func TestManagerGetHitAndMiss(t *testing.T) {
	db := mustOpenDB(t)
	uid := mustCreateUser(t, db, "bob")
	factory, _ := newFakeFactory(t)
	mgr := NewManager(db, factory)

	id, _, err := mgr.Create("bob", uid, "/tmp", envStub, pty.Options{})
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if _, ok := mgr.Get("bob", id); !ok {
		t.Fatal("Get returned false for existing session")
	}
	// Wrong user must not find another user's session.
	if _, ok := mgr.Get("alice", id); ok {
		t.Fatal("Get returned true for wrong user")
	}
	if _, ok := mgr.Get("bob", "nope"); ok {
		t.Fatal("Get returned true for unknown session id")
	}
}

func TestManagerCapEnforcement(t *testing.T) {
	db := mustOpenDB(t)
	uid := mustCreateUser(t, db, "cap-user")
	if err := db.SetUserMaxSessions(uid, 2); err != nil {
		t.Fatalf("set max sessions: %v", err)
	}
	factory, _ := newFakeFactory(t)
	mgr := NewManager(db, factory)

	mk := func() error {
		_, _, err := mgr.Create("cap-user", uid, "/tmp", envStub, pty.Options{})
		return err
	}
	if err := mk(); err != nil {
		t.Fatalf("1st create: %v", err)
	}
	if err := mk(); err != nil {
		t.Fatalf("2nd create: %v", err)
	}
	err := mk()
	if !errors.Is(err, ErrSessionCapReached) {
		t.Fatalf("3rd create err = %v, want ErrSessionCapReached", err)
	}
}

func TestManagerCapZeroMeansUnlimited(t *testing.T) {
	// cap == 0 means "no limit" per spec — must NOT reject creation.
	db := mustOpenDB(t)
	uid := mustCreateUser(t, db, "unlimited")
	if err := db.SetUserMaxSessions(uid, 0); err != nil {
		t.Fatalf("set max sessions: %v", err)
	}
	// EffectiveMaxSessions returns the override only if Valid; an explicit 0
	// is a NULL in SQLite? No — SetUserMaxSessions writes 0 as a real value.
	// Verify the contract: 0 => unlimited.
	factory, _ := newFakeFactory(t)
	mgr := NewManager(db, factory)
	for i := 0; i < 5; i++ {
		if _, _, err := mgr.Create("unlimited", uid, "/tmp", envStub, pty.Options{}); err != nil {
			t.Fatalf("create #%d with cap=0: %v", i+1, err)
		}
	}
}

func TestManagerKillStopsAndMarksExited(t *testing.T) {
	db := mustOpenDB(t)
	uid := mustCreateUser(t, db, "killer")
	factory, _ := newFakeFactory(t)
	mgr := NewManager(db, factory)

	id, p, err := mgr.Create("killer", uid, "/tmp", envStub, pty.Options{})
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	fp := p.(*fakePTY)

	if err := mgr.Kill("killer", id); err != nil {
		t.Fatalf("kill: %v", err)
	}
	if atomic.LoadInt32(&fp.stopCnt) != 1 {
		t.Fatalf("Stop called %d times, want 1", fp.stopCnt)
	}
	// DB row must be marked exited (alive=0)
	row, _ := db.GetSession(id)
	if row.Alive {
		t.Fatal("db row still alive after Kill")
	}
	// Map entry removed
	if _, ok := mgr.Get("killer", id); ok {
		t.Fatal("Get returned true after Kill")
	}
}

func TestManagerKillErrorsOnUnknown(t *testing.T) {
	db := mustOpenDB(t)
	mgr := NewManager(db, func(pty.Options) PTY { return &fakePTY{} })
	if err := mgr.Kill("nobody", "nope"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("kill unknown err = %v, want ErrNotFound", err)
	}
}

func TestManagerKillAllKillsAllForUser(t *testing.T) {
	db := mustOpenDB(t)
	uid := mustCreateUser(t, db, "allkiller")
	other := mustCreateUser(t, db, "other")
	factory, _ := newFakeFactory(t)
	mgr := NewManager(db, factory)

	id1, p1, _ := mgr.Create("allkiller", uid, "/tmp", envStub, pty.Options{})
	id2, p2, _ := mgr.Create("allkiller", uid, "/tmp", envStub, pty.Options{})
	_, p3, _ := mgr.Create("other", other, "/tmp", envStub, pty.Options{}) // must survive

	if err := mgr.KillAll("allkiller"); err != nil {
		t.Fatalf("killall: %v", err)
	}
	if atomic.LoadInt32(&p1.(*fakePTY).stopCnt) != 1 {
		t.Fatalf("p1 stopCnt = %d, want 1", atomic.LoadInt32(&p1.(*fakePTY).stopCnt))
	}
	if atomic.LoadInt32(&p2.(*fakePTY).stopCnt) != 1 {
		t.Fatalf("p2 stopCnt = %d, want 1", atomic.LoadInt32(&p2.(*fakePTY).stopCnt))
	}
	if atomic.LoadInt32(&p3.(*fakePTY).stopCnt) != 0 {
		t.Fatalf("p3 (other user) stopCnt = %d, want 0", atomic.LoadInt32(&p3.(*fakePTY).stopCnt))
	}
	// Both sessions removed from map for the user; other user's intact.
	if _, ok := mgr.Get("allkiller", id1); ok {
		t.Fatal("id1 still in map after KillAll")
	}
	if _, ok := mgr.Get("allkiller", id2); ok {
		t.Fatal("id2 still in map after KillAll")
	}
}

func TestManagerListReturnsSessionsForUser(t *testing.T) {
	db := mustOpenDB(t)
	uid := mustCreateUser(t, db, "lister")
	factory, _ := newFakeFactory(t)
	mgr := NewManager(db, factory)

	mgr.Create("lister", uid, "/tmp", envStub, pty.Options{})
	id2, _, _ := mgr.Create("lister", uid, "/tmp", envStub, pty.Options{})
	mgr.Kill("lister", id2) // marks one exited

	got, err := mgr.List(uid)
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("list len = %d, want 2 (got %+v)", len(got), got)
	}
	aliveN := 0
	for _, s := range got {
		if s.UserID != uid {
			t.Fatalf("leaked session %+v", s)
		}
		if s.Alive {
			aliveN++
		}
	}
	if aliveN != 1 {
		t.Fatalf("alive sessions = %d, want 1", aliveN)
	}
}

func TestManagerConcurrentCreateNoRace(t *testing.T) {
	// Smoke test: concurrent Creates against the same user must not panic/race.
	db := mustOpenDB(t)
	uid := mustCreateUser(t, db, "racer")
	if err := db.SetUserMaxSessions(uid, 0); err != nil { // unlimited
		t.Fatalf("set max sessions: %v", err)
	}
	factory, _ := newFakeFactory(t)
	mgr := NewManager(db, factory)

	var wg sync.WaitGroup
	for i := 0; i < 20; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, _, _ = mgr.Create("racer", uid, "/tmp", envStub, pty.Options{})
		}()
	}
	wg.Wait()

	// Every created session must be reachable (no lost map writes).
	list, _ := mgr.List(uid)
	if len(list) != 20 {
		t.Fatalf("after concurrent create: list len = %d, want 20", len(list))
	}
}
