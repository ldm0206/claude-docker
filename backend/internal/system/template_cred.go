package system

import (
	"errors"
	"fmt"
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
//
// Only .credentials.json (the shared API access credential) is copied — NOT
// .claude.json, which holds per-user oauth login state and user identity and
// must not be shared across accounts.
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

// RestoreClaudeConfig is a crash-recovery fallback for claude's own config file.
// claude CLI sometimes moves ~/.claude.json to a backup under
// ~/.claude/backups/.claude.json.backup.<ts> on a bad exit and fails to restore
// it, leaving the user stuck in a "config not found" loop (which surfaces as
// ERR_ASSERTION on the next launch). If ~/.claude.json is absent but the newest
// backup exists, copy the backup back into place. No-op otherwise. Best-effort:
// failures are logged and never block session creation.
func RestoreClaudeConfig(username string, uid int) error {
	home := filepath.Join(HomeRoot, username)
	main := filepath.Join(home, ".claude.json")
	if _, err := os.Stat(main); err == nil {
		return nil // present, nothing to do
	} else if !errors.Is(err, fs.ErrNotExist) {
		return nil // stat failed for an unexpected reason; don't guess
	}
	backupsDir := filepath.Join(home, ".claude", "backups")
	entries, err := os.ReadDir(backupsDir)
	if err != nil {
		return nil // no backups dir / nothing to restore
	}
	var newest string
	var newestMtime int64
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		name := e.Name()
		if len(name) < len(".claude.json.backup.") || name[:len(".claude.json.backup.")] != ".claude.json.backup." {
			continue
		}
		fi, err := e.Info()
		if err != nil {
			continue
		}
		m := fi.ModTime().Unix()
		if m > newestMtime {
			newestMtime = m
			newest = filepath.Join(backupsDir, name)
		}
	}
	if newest == "" {
		return nil
	}
	data, err := os.ReadFile(newest)
	if err != nil {
		log.Printf("[system] warning: read claude config backup %s: %v", newest, err)
		return nil
	}
	if err := os.WriteFile(main, data, 0o600); err != nil {
		return fmt.Errorf("restore claude config: %w", err)
	}
	if err := os.Chown(main, uid, uid); err != nil {
		log.Printf("[system] warning: chown claude config %s: %v", main, err)
	}
	log.Printf("[system] restored %s .claude.json from %s", username, filepath.Base(newest))
	return nil
}
