package system

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
)

const sharedClaudeBin = "/opt/claude/bin/claude"

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

func provisionDirs(homeRoot, username string, uid int) error {
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
	// claude code's config lives directly under the user's home (persistent
	// claude-home volume), NOT under /data. ~/.claude is a real directory so
	// claude owns settings.json / .credentials.json with no symlink indirection
	// (the old /data/<user>/claude-config + symlink scheme caused EACCES on
	// settings.json when the dir or file wasn't owned by the user).
	claudeDir := filepath.Join(home, ".claude")
	if err := os.MkdirAll(claudeDir, 0o700); err != nil {
		return fmt.Errorf("mkdir .claude: %w", err)
	}
	if err := os.Chown(claudeDir, uid, uid); err != nil {
		return err
	}
	// claude's launcher expects to find itself at ~/.local/bin/claude; if it is
	// missing it prints "run claude install to repair", and `claude install` is
	// blocked by DISABLE_UPDATES=1. Provision a symlink to the shared root-owned
	// binary so every user resolves claude without needing the installer.
	if err := linkClaudeBinary(home, uid); err != nil {
		return fmt.Errorf("link claude binary: %w", err)
	}
	return nil
}

// linkClaudeBinary creates ~/.local/bin/claude → /opt/claude/bin/claude for the
// user. Idempotent: skips when the link already points at the right target,
// recreates it when it is missing or points elsewhere. Best-effort when the
// shared binary is absent (e.g. on dev machines without /opt/claude): the
// symlink is the user's PATH-prepend target, so we still create the dir and
// attempt the link — a broken symlink is no worse than the missing-binary case
// the Dockerfile already guards against in production.
func linkClaudeBinary(home string, uid int) error {
	localBin := filepath.Join(home, ".local", "bin")
	if err := os.MkdirAll(localBin, 0o755); err != nil {
		return err
	}
	if err := os.Chown(localBin, uid, uid); err != nil {
		return err
	}
	link := filepath.Join(localBin, "claude")
	if fi, err := os.Lstat(link); err == nil {
		if fi.Mode()&os.ModeSymlink != 0 {
			if tgt, _ := os.Readlink(link); tgt == sharedClaudeBin {
				return nil // already correct
			}
		}
		_ = os.Remove(link) // stale link or wrong target → recreate
	}
	return os.Symlink(sharedClaudeBin, link)
}

func ProvisionUserDirs(username string, uid int) error {
	if err := validateUsername(username); err != nil {
		return err
	}
	return provisionDirs(HomeRoot, username, uid)
}
