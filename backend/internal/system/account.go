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
}

// LinuxAccountProvisioner calls the real system commands.
type LinuxAccountProvisioner struct{}

func (LinuxAccountProvisioner) Create(username string, uid int) error {
	if err := CreateUserAccount(username, uid); err != nil {
		return err
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
