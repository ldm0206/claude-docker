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

func helperCreateUser(t *testing.T, db *DB, username string) User {
	t.Helper()
	uid, err := db.AllocateUID()
	if err != nil {
		t.Fatalf("allocate uid: %v", err)
	}
	u, err := db.CreateUser(User{
		UID: uid, Username: username, PasswordHash: "x", Role: "user", CreatedAt: 1,
	})
	if err != nil {
		t.Fatalf("create user %s: %v", username, err)
	}
	return u
}

func TestBindTemplate(t *testing.T) {
	db, err := Open(filepath.Join(t.TempDir(), "t.db"))
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer db.Close()

	tmpl, _ := db.CreateTemplate(RoleTemplate{
		Name: "dev", DiskQuotaBytes: 100, CPUQuota: "1", MemoryMaxBytes: 200, MaxSessions: 5, Permissions: "{}",
	})
	u := helperCreateUser(t, db, "bindtmpl")

	if err := db.BindTemplate(u.ID, tmpl.ID); err != nil {
		t.Fatalf("bind template: %v", err)
	}
	got, err := db.GetUserByID(u.ID)
	if err != nil {
		t.Fatalf("get user: %v", err)
	}
	if !got.RoleTemplateID.Valid || int(got.RoleTemplateID.Int64) != tmpl.ID {
		t.Fatalf("role_template_id = %v, want %d", got.RoleTemplateID, tmpl.ID)
	}
}

func TestBindCredential(t *testing.T) {
	db, err := Open(filepath.Join(t.TempDir(), "t.db"))
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer db.Close()

	preset, _ := db.CreatePreset(CredentialPreset{
		Name: "creds", EncryptedBlob: []byte{0x01}, Note: "",
	})
	u := helperCreateUser(t, db, "bindcred")

	if err := db.BindCredential(u.ID, preset.ID); err != nil {
		t.Fatalf("bind credential: %v", err)
	}
	got, err := db.GetUserByID(u.ID)
	if err != nil {
		t.Fatalf("get user: %v", err)
	}
	if !got.CredentialPresetID.Valid || int(got.CredentialPresetID.Int64) != preset.ID {
		t.Fatalf("credential_preset_id = %v, want %d", got.CredentialPresetID, preset.ID)
	}
}

// TestTouchLogin_RecordsIP verifies TouchLogin now persists last_login_ip.
func TestTouchLogin_RecordsIP(t *testing.T) {
	db, err := Open(filepath.Join(t.TempDir(), "t.db"))
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer db.Close()

	u := helperCreateUser(t, db, "alice")
	if err := db.TouchLogin(u.ID, 1700000000, "203.0.113.9"); err != nil {
		t.Fatalf("touch: %v", err)
	}
	got, err := db.GetUserByID(u.ID)
	if err != nil {
		t.Fatalf("reload: %v", err)
	}
	if got.LastLoginIP != "203.0.113.9" {
		t.Errorf("LastLoginIP = %q, want 203.0.113.9", got.LastLoginIP)
	}
}

func TestEffectiveMaxSessions(t *testing.T) {
	db, err := Open(filepath.Join(t.TempDir(), "t.db"))
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer db.Close()

	// No override, no template -> default 3
	u1 := helperCreateUser(t, db, "eff1")
	ms, err := db.EffectiveMaxSessions(u1.ID)
	if err != nil {
		t.Fatalf("effective max sessions: %v", err)
	}
	if ms != 3 {
		t.Fatalf("no override, no template: got %d, want 3", ms)
	}

	// Bound template but no user override -> template value
	tmpl, _ := db.CreateTemplate(RoleTemplate{
		Name: "sess", DiskQuotaBytes: 0, CPUQuota: "1", MemoryMaxBytes: 0, MaxSessions: 7, Permissions: "{}",
	})
	db.BindTemplate(u1.ID, tmpl.ID)
	ms, err = db.EffectiveMaxSessions(u1.ID)
	if err != nil {
		t.Fatalf("effective max sessions: %v", err)
	}
	if ms != 7 {
		t.Fatalf("template bound, no override: got %d, want 7", ms)
	}

	// User override takes precedence over template
	u2 := helperCreateUser(t, db, "eff2")
	db.BindTemplate(u2.ID, tmpl.ID)
	db.SetUserMaxSessions(u2.ID, 2)
	ms, err = db.EffectiveMaxSessions(u2.ID)
	if err != nil {
		t.Fatalf("effective max sessions: %v", err)
	}
	if ms != 2 {
		t.Fatalf("user override: got %d, want 2", ms)
	}

	// User override without template
	u3 := helperCreateUser(t, db, "eff3")
	db.SetUserMaxSessions(u3.ID, 10)
	ms, err = db.EffectiveMaxSessions(u3.ID)
	if err != nil {
		t.Fatalf("effective max sessions: %v", err)
	}
	if ms != 10 {
		t.Fatalf("user override, no template: got %d, want 10", ms)
	}
}

func TestEffectiveDiskQuota(t *testing.T) {
	db, err := Open(filepath.Join(t.TempDir(), "t.db"))
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer db.Close()

	// No override, no template -> default 0
	u1 := helperCreateUser(t, db, "dq1")
	dq, err := db.EffectiveDiskQuota(u1.ID)
	if err != nil {
		t.Fatalf("effective disk quota: %v", err)
	}
	if dq != 0 {
		t.Fatalf("no override, no template: got %d, want 0", dq)
	}

	// Bound template but no user override -> template value
	tmpl, _ := db.CreateTemplate(RoleTemplate{
		Name: "disk", DiskQuotaBytes: 50 << 30, CPUQuota: "1", MemoryMaxBytes: 0, MaxSessions: 1, Permissions: "{}",
	})
	db.BindTemplate(u1.ID, tmpl.ID)
	dq, err = db.EffectiveDiskQuota(u1.ID)
	if err != nil {
		t.Fatalf("effective disk quota: %v", err)
	}
	if dq != 50<<30 {
		t.Fatalf("template bound, no override: got %d, want %d", dq, 50<<30)
	}

	// User override takes precedence over template
	u2 := helperCreateUser(t, db, "dq2")
	db.BindTemplate(u2.ID, tmpl.ID)
	db.SetUserDiskQuota(u2.ID, 100<<30)
	dq, err = db.EffectiveDiskQuota(u2.ID)
	if err != nil {
		t.Fatalf("effective disk quota: %v", err)
	}
	if dq != 100<<30 {
		t.Fatalf("user override: got %d, want %d", dq, 100<<30)
	}

	// User override without template
	u3 := helperCreateUser(t, db, "dq3")
	db.SetUserDiskQuota(u3.ID, 42)
	dq, err = db.EffectiveDiskQuota(u3.ID)
	if err != nil {
		t.Fatalf("effective disk quota: %v", err)
	}
	if dq != 42 {
		t.Fatalf("user override, no template: got %d, want 42", dq)
	}
}
