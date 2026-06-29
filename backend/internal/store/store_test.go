package store

import (
	"path/filepath"
	"testing"
)

func TestOpenAppliesSchema(t *testing.T) {
	d := t.TempDir()
	db, err := Open(filepath.Join(d, "test.db"))
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer db.Close()
	// users table exists and is empty
	var n int
	if err := db.Sqlite().QueryRow("SELECT count(*) FROM users").Scan(&n); err != nil {
		t.Fatalf("query users: %v", err)
	}
	if n != 0 {
		t.Fatalf("expected empty users table, got %d", n)
	}
}

func TestSchemaCreatesAllTables(t *testing.T) {
	d := t.TempDir()
	db, err := Open(filepath.Join(d, "test.db"))
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer db.Close()

	wantTables := []string{"users", "role_templates", "credential_presets", "sessions", "traffic"}
	for _, tbl := range wantTables {
		// verify table exists in sqlite_master
		var name string
		if err := db.Sqlite().QueryRow(
			"SELECT name FROM sqlite_master WHERE type='table' AND name=?", tbl,
		).Scan(&name); err != nil {
			t.Fatalf("table %q not found in sqlite_master: %v", tbl, err)
		}
		// verify table is empty
		var n int
		if err := db.Sqlite().QueryRow("SELECT count(*) FROM "+tbl).Scan(&n); err != nil {
			t.Fatalf("count %q: %v", tbl, err)
		}
		if n != 0 {
			t.Fatalf("expected empty %q table, got %d rows", tbl, n)
		}
	}
}

// TestOpen_AlterColumnsIdempotent verifies that re-opening an existing DB
// (which already has last_login_ip / client_ip from a prior Open) does NOT
// error, and that a fresh DB gets the columns.
func TestOpen_AlterColumnsIdempotent(t *testing.T) {
	path := filepath.Join(t.TempDir(), "alter.db")

	db1, err := Open(path)
	if err != nil {
		t.Fatalf("first open: %v", err)
	}
	if _, err := db1.sql.Exec(`UPDATE users SET last_login_ip = ? WHERE id = -1`, "1.2.3.4"); err != nil {
		t.Fatalf("last_login_ip column missing after first open: %v", err)
	}
	if _, err := db1.sql.Exec(`UPDATE sessions SET client_ip = ? WHERE id = 'none'`, "1.2.3.4"); err != nil {
		t.Fatalf("client_ip column missing after first open: %v", err)
	}
	db1.Close()

	db2, err := Open(path)
	if err != nil {
		t.Fatalf("second open (idempotency): %v", err)
	}
	defer db2.Close()
	if _, err := db2.sql.Exec(`UPDATE users SET last_login_ip = ? WHERE id = -1`, "5.6.7.8"); err != nil {
		t.Fatalf("last_login_ip unusable after re-open: %v", err)
	}
}
