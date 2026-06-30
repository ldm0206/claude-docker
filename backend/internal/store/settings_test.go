package store

import (
	"path/filepath"
	"testing"
)

func openSettingsDB(t *testing.T) *DB {
	t.Helper()
	db, err := Open(filepath.Join(t.TempDir(), "settings.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	return db
}

func TestGetSetting_Absent(t *testing.T) {
	db := openSettingsDB(t)
	v, err := db.GetSetting("nope")
	if err != nil {
		t.Fatalf("absent key must not error, got: %v", err)
	}
	if v != "" {
		t.Fatalf("absent key must return empty, got %q", v)
	}
}

func TestSetSetting_RoundTrip(t *testing.T) {
	db := openSettingsDB(t)
	if err := db.SetSetting("template_user", "alice"); err != nil {
		t.Fatalf("set: %v", err)
	}
	v, err := db.GetSetting("template_user")
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if v != "alice" {
		t.Fatalf("got %q, want alice", v)
	}
}

func TestSetSetting_Upsert(t *testing.T) {
	db := openSettingsDB(t)
	if err := db.SetSetting("template_user", "alice"); err != nil {
		t.Fatal(err)
	}
	if err := db.SetSetting("template_user", "bob"); err != nil {
		t.Fatal(err)
	}
	v, err := db.GetSetting("template_user")
	if err != nil {
		t.Fatalf("upsert get: %v", err)
	}
	if v != "bob" {
		t.Fatalf("upsert: got %q, want bob", v)
	}
}
