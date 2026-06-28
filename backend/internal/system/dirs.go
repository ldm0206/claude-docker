package system

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
)

var usernameRe = regexp.MustCompile(`^[a-z_][a-z0-9_-]{1,31}$`)

// UsernameRegex returns the compiled regex used for username validation,
// so other packages (e.g. the admin handler) can reuse the same rule.
func UsernameRegex() *regexp.Regexp { return usernameRe }

// HomeRoot and DataRoot default to the container layout; overridable for tests.
var (
	HomeRoot = "/home"
	DataRoot = "/data"
)

func validateUsername(name string) error {
	if !usernameRe.MatchString(name) {
		return fmt.Errorf("invalid username %q", name)
	}
	return nil
}

func provisionDirs(homeRoot, dataRoot, username string, uid int) error {
	home := filepath.Join(homeRoot, username)
	if err := os.MkdirAll(filepath.Join(home, "workspace"), 0o700); err != nil {
		return fmt.Errorf("mkdir workspace: %w", err)
	}
	// chroot root must be root-owned 0755; workspace owned by the user
	if err := os.Chmod(home, 0o755); err != nil {
		return err
	}
	if err := os.Chown(home, 0, 0); err != nil {
		return err
	}
	if err := os.Chown(filepath.Join(home, "workspace"), uid, uid); err != nil {
		return err
	}
	cfg := filepath.Join(dataRoot, username, "claude-config")
	if err := os.MkdirAll(cfg, 0o700); err != nil {
		return fmt.Errorf("mkdir claude-config: %w", err)
	}
	return os.Chown(cfg, uid, uid)
}

func ProvisionUserDirs(username string, uid int) error {
	if err := validateUsername(username); err != nil {
		return err
	}
	return provisionDirs(HomeRoot, DataRoot, username, uid)
}
