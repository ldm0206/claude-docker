package system

import (
	"errors"
	"io/fs"
	"log"
	"os"
	"path/filepath"
)

// CopyTemplateCredentials copies the template user's .credentials.json into the
// target user's ~/.claude dir. Source: <HomeRoot>/<templateUser>/.claude/
// .credentials.json. Target: <HomeRoot>/<targetUser>/.claude/.credentials.json.
// No-op (nil) if templateUser is empty, templateUser == targetUser, or the source
// file is absent. The copied file is mode 0600, chown'd to uid. A per-step failure
// is logged and skipped; it never blocks session creation.
func CopyTemplateCredentials(templateUser, targetUser string, uid int) error {
	if templateUser == "" || templateUser == targetUser {
		return nil
	}
	src := filepath.Join(HomeRoot, templateUser, ".claude", ".credentials.json")
	dst := filepath.Join(HomeRoot, targetUser, ".claude", ".credentials.json")
	data, err := os.ReadFile(src)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return nil
		}
		log.Printf("[system] warning: read template credential %s: %v", src, err)
		return nil
	}
	if err := os.WriteFile(dst, data, 0o600); err != nil {
		log.Printf("[system] warning: write credential %s: %v", dst, err)
		return nil
	}
	if err := os.Chown(dst, uid, uid); err != nil {
		log.Printf("[system] warning: chown credential %s: %v", dst, err)
	}
	return nil
}
