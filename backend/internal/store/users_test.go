package store

import (
	"path/filepath"
	"testing"
)

func TestCreateAndGetUser(t *testing.T) {
	db, _ := Open(filepath.Join(t.TempDir(), "t.db"))
	defer db.Close()
	uid, err := db.AllocateUID()
	if err != nil {
		t.Fatalf("allocate uid: %v", err)
	}
	if uid != 2000 {
		t.Fatalf("first uid = %d, want 2000", uid)
	}
	u, err := db.CreateUser(User{
		UID: uid, Username: "alice", PasswordHash: "x", Role: "admin",
		MustChangePassword: true, CreatedAt: 1,
	})
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	got, err := db.GetUserByUsername("alice")
	if err != nil || got.ID != u.ID || got.UID != 2000 || got.Role != "admin" {
		t.Fatalf("get by username: got %+v err %v", got, err)
	}
	uid2, _ := db.AllocateUID()
	if uid2 != 2001 {
		t.Fatalf("second uid = %d, want 2001", uid2)
	}
}
