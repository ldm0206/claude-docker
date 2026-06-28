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
