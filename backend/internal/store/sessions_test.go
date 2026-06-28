package store

import (
	"errors"
	"path/filepath"
	"testing"
)

func TestCreateAndGetSession(t *testing.T) {
	db, err := Open(filepath.Join(t.TempDir(), "t.db"))
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer db.Close()

	uid := mustCreateUser(t, db, "sess1")
	s := Session{ID: "s1", UserID: uid, Name: "main", StartedAt: 100, LastSeenAt: 100, Alive: true}
	if err := db.CreateSession(s); err != nil {
		t.Fatalf("create session: %v", err)
	}
	got, err := db.GetSession("s1")
	if err != nil {
		t.Fatalf("get session: %v", err)
	}
	if got.ID != "s1" || got.UserID != uid || got.Name != "main" ||
		got.StartedAt != 100 || got.LastSeenAt != 100 || !got.Alive {
		t.Fatalf("round-trip mismatch: got %+v", got)
	}
}

func TestGetSessionNotFound(t *testing.T) {
	db, _ := Open(filepath.Join(t.TempDir(), "t.db"))
	defer db.Close()
	_, err := db.GetSession("nope")
	if !errors.Is(err, ErrNotFound) {
		t.Fatalf("got err=%v, want ErrNotFound", err)
	}
}

func TestListSessionsForUser(t *testing.T) {
	db, _ := Open(filepath.Join(t.TempDir(), "t.db"))
	defer db.Close()

	u1 := mustCreateUser(t, db, "list-a")
	u2 := mustCreateUser(t, db, "list-b")

	db.CreateSession(Session{ID: "a1", UserID: u1, StartedAt: 1, LastSeenAt: 1, Alive: true})
	db.CreateSession(Session{ID: "a2", UserID: u1, StartedAt: 2, LastSeenAt: 2, Alive: false})
	db.CreateSession(Session{ID: "b1", UserID: u2, StartedAt: 3, LastSeenAt: 3, Alive: true})

	got, err := db.ListSessionsForUser(u1)
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("u1 session count = %d, want 2 (got %+v)", len(got), got)
	}
	// Must not leak u2's sessions
	for _, s := range got {
		if s.UserID != u1 {
			t.Fatalf("leaked session %+v", s)
		}
	}
}

func TestTouchSession(t *testing.T) {
	db, _ := Open(filepath.Join(t.TempDir(), "t.db"))
	defer db.Close()
	uid := mustCreateUser(t, db, "touch1")
	db.CreateSession(Session{ID: "t1", UserID: uid, StartedAt: 1, LastSeenAt: 1, Alive: true})

	if err := db.TouchSession("t1", 999); err != nil {
		t.Fatalf("touch: %v", err)
	}
	got, _ := db.GetSession("t1")
	if got.LastSeenAt != 999 {
		t.Fatalf("last_seen_at = %d, want 999", got.LastSeenAt)
	}
}

func TestMarkSessionExited(t *testing.T) {
	db, _ := Open(filepath.Join(t.TempDir(), "t.db"))
	defer db.Close()
	uid := mustCreateUser(t, db, "mark1")
	db.CreateSession(Session{ID: "m1", UserID: uid, StartedAt: 1, LastSeenAt: 1, Alive: true})

	if err := db.MarkSessionExited("m1"); err != nil {
		t.Fatalf("mark exited: %v", err)
	}
	got, _ := db.GetSession("m1")
	if got.Alive {
		t.Fatalf("alive = true, want false")
	}
}

func TestDeleteSession(t *testing.T) {
	db, _ := Open(filepath.Join(t.TempDir(), "t.db"))
	defer db.Close()
	uid := mustCreateUser(t, db, "del1")
	db.CreateSession(Session{ID: "d1", UserID: uid, StartedAt: 1, LastSeenAt: 1, Alive: true})

	if err := db.DeleteSession("d1"); err != nil {
		t.Fatalf("delete: %v", err)
	}
	_, err := db.GetSession("d1")
	if !errors.Is(err, ErrNotFound) {
		t.Fatalf("after delete: err=%v, want ErrNotFound", err)
	}
}

func TestCountAliveSessionsForUser(t *testing.T) {
	db, _ := Open(filepath.Join(t.TempDir(), "t.db"))
	defer db.Close()
	u1 := mustCreateUser(t, db, "cnt-a")
	u2 := mustCreateUser(t, db, "cnt-b")

	db.CreateSession(Session{ID: "c1", UserID: u1, StartedAt: 1, LastSeenAt: 1, Alive: true})
	db.CreateSession(Session{ID: "c2", UserID: u1, StartedAt: 1, LastSeenAt: 1, Alive: true})
	db.CreateSession(Session{ID: "c3", UserID: u1, StartedAt: 1, LastSeenAt: 1, Alive: false}) // dead
	db.CreateSession(Session{ID: "c4", UserID: u2, StartedAt: 1, LastSeenAt: 1, Alive: true})  // other user

	n, err := db.CountAliveSessionsForUser(u1)
	if err != nil {
		t.Fatalf("count alive: %v", err)
	}
	if n != 2 {
		t.Fatalf("alive count = %d, want 2", n)
	}

	// Mark one dead -> count drops to 1
	db.MarkSessionExited("c1")
	n, _ = db.CountAliveSessionsForUser(u1)
	if n != 1 {
		t.Fatalf("after kill: alive count = %d, want 1", n)
	}
}

func mustCreateUser(t *testing.T, db *DB, username string) int {
	t.Helper()
	uid, err := db.AllocateUID()
	if err != nil {
		t.Fatalf("allocate uid: %v", err)
	}
	u, err := db.CreateUser(User{
		UID: uid, Username: username, PasswordHash: "x", Role: "user", CreatedAt: 1,
	})
	if err != nil {
		t.Fatalf("create user %s: %v", username, err)
	}
	return u.ID
}
