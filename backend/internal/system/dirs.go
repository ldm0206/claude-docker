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
	// Symlink ~/.claude → /data/<user>/claude-config so `claude login` (which
	// reads ~/.claude by default) persists per-user on the /data volume,
	// decoupled from the workspace. If ~/.claude already exists as a real
	// file/dir, leave it untouched (never clobber user state).
	claudeLink := filepath.Join(home, ".claude")
	if _, err := os.Lstat(claudeLink); os.IsNotExist(err) {
		if err := os.Symlink(cfg, claudeLink); err != nil {
			return fmt.Errorf("symlink .claude: %w", err)
		}
	}
	return os.Chown(cfg, uid, uid)
}

func ProvisionUserDirs(username string, uid int) error {
	if err := validateUsername(username); err != nil {
		return err
	}
	return provisionDirs(HomeRoot, DataRoot, username, uid)
}

// EnsureSharedCredentialDir idempotently creates the shared config source
// dir <DataRoot>/shared/claude-config at mode 0700, root-owned. The operator
// runs `claude login` against it; SyncSharedConfig copies shared config files
// (.credentials* and settings.json) from it into each user's claude-config.
// Safe to call on every boot.
func EnsureSharedCredentialDir() error {
	dir := filepath.Join(DataRoot, "shared", "claude-config")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return fmt.Errorf("mkdir shared claude-config: %w", err)
	}
	if err := os.Chmod(dir, 0o700); err != nil {
		return err
	}
	return os.Chown(dir, 0, 0)
}
