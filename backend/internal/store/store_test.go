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

	wantTables := []string{"users", "role_templates", "credential_presets", "sessions"}
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
