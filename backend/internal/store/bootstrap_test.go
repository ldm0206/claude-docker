package store

import (
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
