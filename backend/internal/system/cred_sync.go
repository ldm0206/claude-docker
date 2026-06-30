package system

import (
	"errors"
	"io/fs"
	"log"
	"os"
	"path/filepath"
	"strings"
)

// syncSharedConfig copies shared config files from srcDir into dstDir:
// entries named ".credentials*" or "settings.json". srcDir missing or
// containing no matches is a no-op. Files are written mode 0600 and
// chown'd to uid. A per-file failure is logged and skipped; it does not
// abort the sync.
func syncSharedConfig(srcDir, dstDir string, uid int) error {
	entries, err := os.ReadDir(srcDir)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return nil
		}
		return err
	}
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		name := e.Name()
		if !strings.HasPrefix(name, ".credentials") && name != "settings.json" {
			continue
		}
		src := filepath.Join(srcDir, name)
		dst := filepath.Join(dstDir, name)
		data, err := os.ReadFile(src)
		if err != nil {
			log.Printf("[system] warning: read shared config %s: %v", src, err)
			continue
		}
		if err := os.WriteFile(dst, data, 0o600); err != nil {
			log.Printf("[system] warning: write config %s: %v", dst, err)
			continue
		}
		if err := os.Chown(dst, uid, uid); err != nil {
			log.Printf("[system] warning: chown config %s: %v", dst, err)
			continue
		}
	}
	return nil
}

// SyncSharedConfig copies the operator's shared config files (.credentials* and
// settings.json) into the given user's claude-config dir. Source:
// <DataRoot>/shared/claude-config. Target: <DataRoot>/<username>/claude-config.
// No-op if source is absent or has no matching files. uid owns the written
// files (0600).
func SyncSharedConfig(username string, uid int) error {
	src := filepath.Join(DataRoot, "shared", "claude-config")
	dst := filepath.Join(DataRoot, username, "claude-config")
	return syncSharedConfig(src, dst, uid)
}
