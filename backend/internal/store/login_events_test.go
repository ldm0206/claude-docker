package store

import (
	"path/filepath"
	"testing"
)

func TestLoginEvents_CreateAndList(t *testing.T) {
	db, err := Open(filepath.Join(t.TempDir(), "login_events.db"))
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer db.Close()

	u := mustCreateUser(t, db, "alice")
	if err := db.CreateLoginEvent(LoginEvent{
		UserID:    u,
		Username:  "alice",
		IP:        "1.1.1.1",
		UserAgent: "curl",
		Success:   true,
		At:        100,
	}); err != nil {
		t.Fatalf("create: %v", err)
	}
	if err := db.CreateLoginEvent(LoginEvent{
		UserID:    0,
		Username:  "ghost",
		IP:        "2.2.2.2",
		UserAgent: "curl",
		Success:   false,
		At:        200,
	}); err != nil {
		t.Fatalf("create fail: %v", err)
	}

	got, err := db.ListLoginEvents(10)
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("got %d events, want 2", len(got))
	}

	if got[0].Username != "ghost" || got[0].Success {
		t.Errorf("first = %+v, want ghost/fail", got[0])
	}
	if got[0].UserID != 0 {
		t.Errorf("first.UserID = %d, want 0", got[0].UserID)
	}
	if got[1].Username != "alice" || !got[1].Success {
		t.Errorf("second = %+v, want alice/success", got[1])
	}
}

func TestLoginEvents_ListCap(t *testing.T) {
	db, err := Open(filepath.Join(t.TempDir(), "login_events_cap.db"))
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer db.Close()

	u := mustCreateUser(t, db, "alice")
	for i := 0; i < 5; i++ {
		if err := db.CreateLoginEvent(LoginEvent{UserID: u, Username: "alice", At: int64(i)}); err != nil {
			t.Fatalf("create %d: %v", i, err)
		}
	}
	got, err := db.ListLoginEvents(3)
	if err != nil {
		t.Fatalf("list cap: %v", err)
	}
	if len(got) != 3 {
		t.Errorf("cap: got %d, want 3", len(got))
	}
	got2, err := db.ListLoginEvents(0)
	if err != nil {
		t.Fatalf("list default: %v", err)
	}
	if len(got2) != 5 {
		t.Errorf("default limit (<=0 → 100) should return all 5 when under the cap: got %d", len(got2))
	}
}
