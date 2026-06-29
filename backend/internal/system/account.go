package system

import (
	"fmt"
	"os/exec"
	"strconv"
)

// AccountProvisioner is the seam used by the admin user-management handlers.
// The real implementation calls Linux commands (useradd/userdel/usermod);
// tests inject a fake so the HTTP layer is testable on Windows.
type AccountProvisioner interface {
	Create(username string, uid int) error // useradd + ProvisionUserDirs
	Delete(username string) error          // userdel + rm dirs
	Lock(username string) error            // usermod -L
	Unlock(username string) error          // usermod -U
	// Ensure provisions the Linux account + dirs for a user that may already
	// exist. It is idempotent: if the account is present it skips useradd and
	// only re-runs ProvisionUserDirs (which mkdir -p's missing dirs); if the
	// account is absent it creates it first. Used at boot to repair users
	// whose DB row exists but whose Linux account/home was never provisioned
	// (notably the bootstrap admin, which BootstrapAdmin creates as a DB row
	// only — without Ensure it has no /home/<user>/workspace, so gosu fails
	// and the terminal connects-then-immediately-disconnects).
	Ensure(username string, uid int) error
}

// LinuxAccountProvisioner calls the real system commands.
type LinuxAccountProvisioner struct{}

func (LinuxAccountProvisioner) Create(username string, uid int) error {
	if err := CreateUserAccount(username, uid); err != nil {
		return err
	}
	return ProvisionUserDirs(username, uid)
}

// Ensure is the idempotent form of Create: it tolerates an already-existing
// Linux account (Create's useradd would error on a duplicate). It checks
// account existence via `id -u <username>`, useradd's only when absent, then
// always re-runs ProvisionUserDirs so missing home/workspace/.claude dirs are
// (re)created. Safe to call on every boot for every user row.
func (LinuxAccountProvisioner) Ensure(username string, uid int) error {
	if err := validateUsername(username); err != nil {
		return err
	}
	if err := exec.Command("id", "-u", username).Run(); err != nil {
		// Account does not exist (id exits non-zero). Create it. useradd -M
		// keeps the home dir absent on purpose; ProvisionUserDirs builds it.
		if err := CreateUserAccount(username, uid); err != nil {
			return err
		}
	}
	return ProvisionUserDirs(username, uid)
}

func (LinuxAccountProvisioner) Delete(username string) error {
	return DeleteUserAccount(username)
}

func (LinuxAccountProvisioner) Lock(username string) error {
	return LockUserAccount(username)
}

func (LinuxAccountProvisioner) Unlock(username string) error {
	return UnlockUserAccount(username)
}

// DefaultProvisioner is the production provisioner used by main.go.
var DefaultProvisioner AccountProvisioner = LinuxAccountProvisioner{}

func CreateUserAccount(username string, uid int) error {
	if err := validateUsername(username); err != nil {
		return err
	}
	out, err := exec.Command("useradd", "-M", "-u", strconv.Itoa(uid), "-s", "/bin/bash", username).CombinedOutput()
	if err != nil {
		return fmt.Errorf("useradd: %w: %s", err, out)
	}
	return nil
}

func DeleteUserAccount(username string) error {
	if err := validateUsername(username); err != nil {
		return err
	}
	if out, err := exec.Command("userdel", username).CombinedOutput(); err != nil {
		return fmt.Errorf("userdel: %w: %s", err, out)
	}
	_ = exec.Command("rm", "-rf", HomeRoot+"/"+username, DataRoot+"/"+username).Run()
	return nil
}

func LockUserAccount(username string) error {
	return runUsermod(username, "-L")
}
func UnlockUserAccount(username string) error {
	return runUsermod(username, "-U")
}
func runUsermod(username, flag string) error {
	if err := validateUsername(username); err != nil {
		return err
	}
	out, err := exec.Command("usermod", flag, username).CombinedOutput()
	if err != nil {
		return fmt.Errorf("usermod %s: %w: %s", flag, err, out)
	}
	return nil
}
