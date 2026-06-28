package store

import (
	"errors"
	"path/filepath"
	"testing"

	"github.com/ldm0206/claude-docker/backend/internal/auth"
)

func TestBootstrapAdminCreatesFirst(t *testing.T) {
	db, _ := Open(filepath.Join(t.TempDir(), "t.db"))
	defer db.Close()
	if err := BootstrapAdmin(db, "root", "initialpw", auth.HashPassword); err != nil {
		t.Fatalf("bootstrap: %v", err)
	}
	u, err := db.GetUserByUsername("root")
	if err != nil || u.Role != "admin" || !u.MustChangePassword {
		t.Fatalf("admin not created correctly: %+v %v", u, err)
	}
	if !auth.CheckPassword("initialpw", u.PasswordHash) {
		t.Fatal("bootstrap password hash mismatch")
	}
	// idempotent: a second call must not create a duplicate / overwrite
	uidBefore := u.UID
	if err := BootstrapAdmin(db, "root", "different", auth.HashPassword); err != nil {
		t.Fatalf("second bootstrap: %v", err)
	}
	u2, _ := db.GetUserByUsername("root")
	if u2.UID != uidBefore || !auth.CheckPassword("initialpw", u2.PasswordHash) {
		t.Fatal("second bootstrap must not alter the existing admin")
	}
}

// TestBootstrapAdminSkipsOnExistingAdminWithEmptyCreds pins the contract that
// a pre-existing /data/app.db with an admin MUST NOT cause BootstrapAdmin to
// error when BOOTSTRAP_* env vars are unset (the normal restart path). Before
// Fix 1 the credential check ran first and returned an error, which main.go's
// log.Fatalf turned into a fatal startup — breaking every restart after the
// first boot. The new contract: empty creds + existing admin → nil.
func TestBootstrapAdminSkipsOnExistingAdminWithEmptyCreds(t *testing.T) {
	db, _ := Open(filepath.Join(t.TempDir(), "t.db"))
	defer db.Close()
	// First boot: create the admin with valid creds.
	if err := BootstrapAdmin(db, "root", "initialpw", auth.HashPassword); err != nil {
		t.Fatalf("bootstrap 1: %v", err)
	}
	// Subsequent restart: no BOOTSTRAP_* env set → empty creds. Must NOT error.
	if err := BootstrapAdmin(db, "", "", auth.HashPassword); err != nil {
		t.Fatalf("bootstrap with empty creds on admin-bearing DB must be a no-op, got: %v", err)
	}
	// The existing admin is unchanged.
	u, err := db.GetUserByUsername("root")
	if err != nil {
		t.Fatalf("get admin: %v", err)
	}
	if u.Role != "admin" || !u.MustChangePassword {
		t.Fatalf("admin altered: %+v", u)
	}
	if !auth.CheckPassword("initialpw", u.PasswordHash) {
		t.Fatal("admin password must be unchanged by the no-op bootstrap")
	}
}

// TestBootstrapAdminSkipsOnEmptyDBWithEmptyCreds pins the defense-in-depth
// branch: a fresh DB with no admin AND no bootstrap creds returns nil (never
// fatal), so the server still starts. An admin can be created later via the
// admin API. Note: this scenario is unrealistic in production (main.go always
// has BOOTSTRAP_* set on first boot) but the function must remain safe.
func TestBootstrapAdminSkipsOnEmptyDBWithEmptyCreds(t *testing.T) {
	db, _ := Open(filepath.Join(t.TempDir(), "t.db"))
	defer db.Close()
	if err := BootstrapAdmin(db, "", "", auth.HashPassword); err != nil {
		t.Fatalf("bootstrap on empty DB with empty creds must not error, got: %v", err)
	}
	// No admin created — DB stays admin-free.
	if _, err := db.GetUserByUsername(""); !errors.Is(err, ErrNotFound) {
		t.Fatalf("expected ErrNotFound querying an empty username, got err=%v", err)
	}
}
