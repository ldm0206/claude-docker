//go:build linux

package system

import (
	"os"
	"os/user"
	"path/filepath"
	"testing"
)

// TestEnsureProvisionsMissingAccount covers the bootstrap-admin regression:
// a user with no Linux account and no home dir must, after Ensure, have both
// the account and /home/<user>/workspace. Runs as root only (useradd/chown).
func TestEnsureProvisionsMissingAccount(t *testing.T) {
	if os.Geteuid() != 0 {
		t.Skip("requires root (useradd/chown)")
	}
	tmp := t.TempDir()
	HomeRoot = filepath.Join(tmp, "home")
	DataRoot = filepath.Join(tmp, "data")
	const name = "cdensure1"
	const uid = 14001
	if _, err := user.Lookup(name); err == nil {
		t.Skipf("user %s already exists on host", name)
	}
	defer func() {
		_ = LinuxAccountProvisioner{}.Delete(name)
	}()

	if err := LinuxAccountProvisioner{}.Ensure(name, uid); err != nil {
		t.Fatalf("Ensure missing account: %v", err)
	}
	if _, err := user.Lookup(name); err != nil {
		t.Fatalf("account not created: %v", err)
	}
	if _, err := os.Stat(filepath.Join(HomeRoot, name, "workspace")); err != nil {
		t.Fatalf("workspace not created: %v", err)
	}
}

// TestEnsureIdempotentOnExistingAccount pins that a second Ensure on an
// already-provisioned user does not error (useradd would fail on a duplicate;
// Ensure must skip it). This is the boot-restart path.
func TestEnsureIdempotentOnExistingAccount(t *testing.T) {
	if os.Geteuid() != 0 {
		t.Skip("requires root (useradd/chown)")
	}
	tmp := t.TempDir()
	HomeRoot = filepath.Join(tmp, "home")
	DataRoot = filepath.Join(tmp, "data")
	const name = "cdensure2"
	const uid = 14002
	if _, err := user.Lookup(name); err == nil {
		t.Skipf("user %s already exists on host", name)
	}
	defer func() {
		_ = LinuxAccountProvisioner{}.Delete(name)
	}()

	if err := LinuxAccountProvisioner{}.Ensure(name, uid); err != nil {
		t.Fatalf("first Ensure: %v", err)
	}
	if err := LinuxAccountProvisioner{}.Ensure(name, uid); err != nil {
		t.Fatalf("second Ensure (must be idempotent): %v", err)
	}
}
