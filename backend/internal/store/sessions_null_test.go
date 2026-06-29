package store

import (
	"path/filepath"
	"testing"
)

// Sessions inserted before the client_ip ALTER (or by a build that didn't pass
// ClientIP) have SQL NULL in the client_ip column. database/sql refuses to
// scan NULL into a plain string ("converting NULL to string is unsupported"),
// which made ListSessionsForUser / GetSession return 500 on legacy rows.
// COALESCE(client_ip, '') in the SELECT collapses NULL to "" at the SQL layer.
// These tests pin that path so a regression is caught without a live DB.

func TestGetSession_NullClientIP(t *testing.T) {
	db, err := Open(filepath.Join(t.TempDir(), "t.db"))
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer db.Close()

	uid := mustCreateUser(t, db, "nullget")
	if _, err := db.sql.Exec(
		`INSERT INTO sessions (id, user_id, name, started_at, last_seen_at, alive, client_ip)
		 VALUES (?, ?, ?, ?, ?, ?, NULL)`, "g1", uid, "legacy", 1, 1, 1); err != nil {
		t.Fatalf("insert null row: %v", err)
	}
	got, err := db.GetSession("g1")
	if err != nil {
		t.Fatalf("GetSession on NULL client_ip: %v", err)
	}
	if got.ClientIP != "" {
		t.Errorf("ClientIP = %q, want empty for NULL", got.ClientIP)
	}
}

func TestListSessions_NullClientIP(t *testing.T) {
	db, err := Open(filepath.Join(t.TempDir(), "t.db"))
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer db.Close()

	uid := mustCreateUser(t, db, "nulllist")
	if _, err := db.sql.Exec(
		`INSERT INTO sessions (id, user_id, name, started_at, last_seen_at, alive, client_ip)
		 VALUES (?, ?, ?, ?, ?, ?, NULL)`, "n1", uid, "legacy", 1, 1, 1); err != nil {
		t.Fatalf("insert null row: %v", err)
	}
	got, err := db.ListSessionsForUser(uid)
	if err != nil {
		t.Fatalf("ListSessionsForUser on NULL client_ip: %v", err)
	}
	if len(got) != 1 || got[0].ID != "n1" || got[0].ClientIP != "" {
		t.Fatalf("unexpected: %+v", got)
	}
}