//go:build linux

package system

import (
	"os"
	"path/filepath"
	"testing"
)

func writeCredFile(t *testing.T, dir, name, content string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
}

func TestSyncSharedConfig_HappyPath(t *testing.T) {
	src := t.TempDir()
	dst := t.TempDir()
	writeCredFile(t, src, ".credentials.json", `{"token":"abc"}`)

	if err := syncSharedConfig(src, dst, 2000); err != nil {
		t.Fatalf("sync: %v", err)
	}
	b, err := os.ReadFile(filepath.Join(dst, ".credentials.json"))
	if err != nil {
		t.Fatalf("target file missing: %v", err)
	}
	if string(b) != `{"token":"abc"}` {
		t.Fatalf("content = %q", string(b))
	}
	fi, _ := os.Stat(filepath.Join(dst, ".credentials.json"))
	if fi.Mode().Perm() != 0o600 {
		t.Fatalf("perm = %o, want 0600", fi.Mode().Perm())
	}
}

// settings.json is now in the copy whitelist; projects/ and other dirs stay excluded.
func TestSyncSharedConfig_SettingsCopied(t *testing.T) {
	src := t.TempDir()
	dst := t.TempDir()
	writeCredFile(t, src, ".credentials.json", `x`)
	writeCredFile(t, src, "settings.json", `{"permissions":{}}`)
	if err := os.MkdirAll(filepath.Join(src, "projects"), 0o755); err != nil {
		t.Fatal(err)
	}

	if err := syncSharedConfig(src, dst, 2000); err != nil {
		t.Fatalf("sync: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dst, "settings.json")); err != nil {
		t.Fatalf("settings.json should be copied: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dst, "projects")); !os.IsNotExist(err) {
		t.Fatalf("projects/ should NOT be copied: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dst, ".credentials.json")); err != nil {
		t.Fatalf(".credentials.json should be copied: %v", err)
	}
}

func TestSyncSharedConfig_SourceMissing(t *testing.T) {
	dst := t.TempDir()
	if err := syncSharedConfig("/nonexistent/path/xyz", dst, 2000); err != nil {
		t.Fatalf("missing source must be no-op, got: %v", err)
	}
}

func TestSyncSharedConfig_SourceEmpty(t *testing.T) {
	src := t.TempDir() // exists, no .credentials* / settings.json
	dst := t.TempDir()
	if err := syncSharedConfig(src, dst, 2000); err != nil {
		t.Fatalf("empty source must be no-op, got: %v", err)
	}
}

func TestSyncSharedConfig_Overwrite(t *testing.T) {
	src := t.TempDir()
	dst := t.TempDir()
	writeCredFile(t, src, ".credentials.json", `new`)
	writeCredFile(t, dst, ".credentials.json", `old`)

	if err := syncSharedConfig(src, dst, 2000); err != nil {
		t.Fatalf("sync: %v", err)
	}
	b, _ := os.ReadFile(filepath.Join(dst, ".credentials.json"))
	if string(b) != `new` {
		t.Fatalf("target not overwritten, content = %q", string(b))
	}
}

func TestSyncSharedConfig_SettingsOverwrite(t *testing.T) {
	src := t.TempDir()
	dst := t.TempDir()
	writeCredFile(t, src, "settings.json", `{"new":true}`)
	writeCredFile(t, dst, "settings.json", `{"stale":true}`)

	if err := syncSharedConfig(src, dst, 2000); err != nil {
		t.Fatalf("sync: %v", err)
	}
	b, _ := os.ReadFile(filepath.Join(dst, "settings.json"))
	if string(b) != `{"new":true}` {
		t.Fatalf("settings.json not overwritten, content = %q", string(b))
	}
}
