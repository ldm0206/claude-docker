package ssh

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/ldm0206/claude-docker/backend/internal/auth"
	"github.com/ldm0206/claude-docker/backend/internal/store"
)

// compile-time assertion that Server exposes Start(context.Context) error and
// Stop() error — the Linux-runtime wiring must compile on Windows even though
// unit tests never call Start. If either signature drifts, this fails to build.
var _ sshServerWiring = (*Server)(nil)

type sshServerWiring interface {
	Start(context.Context) error
	Stop() error
}

// helperStore opens a fresh SQLite DB on a temp file and seeds two users:
// "alice" (admin, active) and "bob" (user, active). The caller may suspend or
// corrupt bob after seeding.
func helperStore(t *testing.T, db *store.DB) (admin, user store.User) {
	t.Helper()
	adminHash, err := auth.HashPassword("admin-pass")
	if err != nil {
		t.Fatalf("hash admin pw: %v", err)
	}
	userHash, err := auth.HashPassword("user-pass")
	if err != nil {
		t.Fatalf("hash user pw: %v", err)
	}
	uidA, _ := db.AllocateUID()
	a, err := db.CreateUser(store.User{
		UID: uidA, Username: "alice", PasswordHash: adminHash, Role: "admin", CreatedAt: 1,
	})
	if err != nil {
		t.Fatalf("create admin: %v", err)
	}
	uidB, _ := db.AllocateUID()
	b, err := db.CreateUser(store.User{
		UID: uidB, Username: "bob", PasswordHash: userHash, Role: "user", CreatedAt: 1,
	})
	if err != nil {
		t.Fatalf("create user: %v", err)
	}
	return a, b
}

func TestAuthenticate(t *testing.T) {
	db, err := store.Open(filepath.Join(t.TempDir(), "t.db"))
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer db.Close()
	admin, bob := helperStore(t, db)
	_ = admin

	srv := New(db, ":22")

	// Correct admin password accepted, returns the admin user.
	got, ok := Authenticate(srv, "alice", "admin-pass")
	if !ok {
		t.Fatal("correct admin password: expected ok=true")
	}
	if got.ID != admin.ID || got.Username != "alice" || got.Role != "admin" {
		t.Fatalf("correct admin password: got %+v, want %+v", got, admin)
	}

	// Correct regular-user password accepted, returns the user.
	got, ok = Authenticate(srv, "bob", "user-pass")
	if !ok {
		t.Fatal("correct user password: expected ok=true")
	}
	if got.ID != bob.ID || got.Username != "bob" || got.Role != "user" {
		t.Fatalf("correct user password: got %+v, want %+v", got, admin)
	}

	// Wrong password rejected.
	if _, ok := Authenticate(srv, "alice", "WRONG"); ok {
		t.Fatal("wrong password: expected ok=false")
	}

	// Missing user rejected, returning identical false (no enumeration side-channel).
	if _, ok := Authenticate(srv, "ghost", "anything"); ok {
		t.Fatal("missing user: expected ok=false")
	}

	// Suspended user rejected even with the correct password.
	if err := db.SetSuspended(bob.ID, true); err != nil {
		t.Fatalf("suspend bob: %v", err)
	}
	if _, ok := Authenticate(srv, "bob", "user-pass"); ok {
		t.Fatal("suspended user with correct password: expected ok=false")
	}

	// Missing-user and wrong-password MUST be indistinguishable to the caller:
	// both return (zero-value User, false) with no error variant. This test
	// documents that invariant explicitly.
	uMiss, okMiss := Authenticate(srv, "ghost", "x")
	uWrong, okWrong := Authenticate(srv, "alice", "x")
	if okMiss || okWrong {
		t.Fatalf("both missing-user and wrong-pw must be false; got %v %v", okMiss, okWrong)
	}
	if uMiss != (store.User{}) || uWrong != (store.User{}) {
		t.Fatalf("both must return zero-value User; got %+v %+v", uMiss, uWrong)
	}
}

func TestRouter(t *testing.T) {
	db, err := store.Open(filepath.Join(t.TempDir(), "t.db"))
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer db.Close()
	admin, bob := helperStore(t, db)

	srv := New(db, ":22")

	// admin role -> "admin" (full interactive shell as root).
	kind, err := srv.Router(admin)
	if err != nil {
		t.Fatalf("router admin: %v", err)
	}
	if kind != "admin" {
		t.Fatalf("router admin: got %q, want %q", kind, "admin")
	}

	// user role -> "user" (SFTP-only chroot).
	kind, err = srv.Router(bob)
	if err != nil {
		t.Fatalf("router user: %v", err)
	}
	if kind != "user" {
		t.Fatalf("router user: got %q, want %q", kind, "user")
	}

	// Empty/unknown role defaults to "user" (least privilege).
	kind, err = srv.Router(store.User{ID: 99, Username: "unknown", Role: ""})
	if err != nil {
		t.Fatalf("router empty role: %v", err)
	}
	if kind != "user" {
		t.Fatalf("router empty role: got %q, want %q", kind, "user")
	}
}