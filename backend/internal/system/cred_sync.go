package system

import (
	"errors"
	"io/fs"
	"log"
	"os"
	"path/filepath"
	"strings"
)

// syncSharedCredentials copies credential files (names matching ".credentials*")
// from srcDir into dstDir. srcDir missing or containing no matches is a no-op.
// Files are written mode 0600 and chown'd to uid. A per-file failure is logged
// and skipped; it does not abort the sync.
func syncSharedCredentials(srcDir, dstDir string, uid int) error {
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
		if !strings.HasPrefix(e.Name(), ".credentials") {
			continue
		}
		src := filepath.Join(srcDir, e.Name())
		dst := filepath.Join(dstDir, e.Name())
		data, err := os.ReadFile(src)
		if err != nil {
			log.Printf("[system] warning: read shared credential %s: %v", src, err)
			continue
		}
		if err := os.WriteFile(dst, data, 0o600); err != nil {
			log.Printf("[system] warning: write credential %s: %v", dst, err)
			continue
		}
		if err := os.Chown(dst, uid, uid); err != nil {
			log.Printf("[system] warning: chown credential %s: %v", dst, err)
			continue
		}
	}
	return nil
}

// SyncSharedCredentials copies the operator's shared credential files into the
// given user's claude-config dir. Source: <DataRoot>/shared/claude-config.
// Target: <DataRoot>/<username>/claude-config. No-op if source is absent or has
// no .credentials* files. uid owns the written files (0600).
func SyncSharedCredentials(username string, uid int) error {
	src := filepath.Join(DataRoot, "shared", "claude-config")
	dst := filepath.Join(DataRoot, username, "claude-config")
	return syncSharedCredentials(src, dst, uid)
}
