package system

import (
	"fmt"
	"os/exec"
	"strconv"
)

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
