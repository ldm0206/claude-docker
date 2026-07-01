package system

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

// TestRestoreClaudeConfig_FromBackup verifies that a missing ~/.claude.json is
// restored from the newest backup when one exists.
func TestRestoreClaudeConfig_FromBackup(t *testing.T) {
	root := t.TempDir()
	t.Cleanup(func() { HomeRoot = "/home" })
	HomeRoot = root

	// user "bob" with no .claude.json, but a backup exists.
	home := filepath.Join(root, "bob")
	backups := filepath.Join(home, ".claude", "backups")
	if err := os.MkdirAll(backups, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(backups, ".claude.json.backup.100"), []byte(`{"old":1}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(backups, ".claude.json.backup.900"), []byte(`{"new":2}`), 0o600); err != nil {
		t.Fatal(err)
	}
	// newest by mtime wins regardless of the numeric suffix in the name.
	base := time.Now()
	_ = os.Chtimes(filepath.Join(backups, ".claude.json.backup.100"), base, base)
	_ = os.Chtimes(filepath.Join(backups, ".claude.json.backup.900"), base.Add(time.Second), base.Add(time.Second))

	if err := RestoreClaudeConfig("bob", 2000); err != nil {
		t.Fatalf("restore: %v", err)
	}
	b, err := os.ReadFile(filepath.Join(home, ".claude.json"))
	if err != nil {
		t.Fatalf("read restored: %v", err)
	}
	if string(b) != `{"new":2}` {
		t.Fatalf("restored wrong backup: %s", b)
	}
}

// TestRestoreClaudeConfig_NoopWhenPresent verifies a present .claude.json is
// left untouched.
func TestRestoreClaudeConfig_NoopWhenPresent(t *testing.T) {
	root := t.TempDir()
	t.Cleanup(func() { HomeRoot = "/home" })
	HomeRoot = root

	home := filepath.Join(root, "bob")
	if err := os.MkdirAll(home, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(home, ".claude.json"), []byte(`{"keep":1}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := RestoreClaudeConfig("bob", 2000); err != nil {
		t.Fatalf("restore: %v", err)
	}
	b, _ := os.ReadFile(filepath.Join(home, ".claude.json"))
	if string(b) != `{"keep":1}` {
		t.Fatalf("present config was changed: %s", b)
	}
}

// TestRestoreClaudeConfig_NoopWhenNoBackup verifies a missing config with no
// backup is a no-op (e.g. a brand-new user who has never run claude).
func TestRestoreClaudeConfig_NoopWhenNoBackup(t *testing.T) {
	root := t.TempDir()
	t.Cleanup(func() { HomeRoot = "/home" })
	HomeRoot = root
	home := filepath.Join(root, "bob")
	if err := os.MkdirAll(home, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := RestoreClaudeConfig("bob", 2000); err != nil {
		t.Fatalf("restore should be no-op: %v", err)
	}
	if _, err := os.Stat(filepath.Join(home, ".claude.json")); !os.IsNotExist(err) {
		t.Fatalf("expected no .claude.json created")
	}
}
