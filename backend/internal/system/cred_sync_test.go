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

func TestSyncSharedCredentials_HappyPath(t *testing.T) {
	src := t.TempDir()
	dst := t.TempDir()
	writeCredFile(t, src, ".credentials.json", `{"token":"abc"}`)

	if err := syncSharedCredentials(src, dst, 2000); err != nil {
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

func TestSyncSharedCredentials_Whitelist(t *testing.T) {
	src := t.TempDir()
	dst := t.TempDir()
	writeCredFile(t, src, ".credentials.json", `x`)
	writeCredFile(t, src, "settings.json", `y`)
	if err := os.MkdirAll(filepath.Join(src, "projects"), 0o755); err != nil {
		t.Fatal(err)
	}

	if err := syncSharedCredentials(src, dst, 2000); err != nil {
		t.Fatalf("sync: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dst, "settings.json")); !os.IsNotExist(err) {
		t.Fatalf("settings.json should NOT be copied: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dst, "projects")); !os.IsNotExist(err) {
		t.Fatalf("projects/ should NOT be copied: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dst, ".credentials.json")); err != nil {
		t.Fatalf(".credentials.json should be copied: %v", err)
	}
}

func TestSyncSharedCredentials_SourceMissing(t *testing.T) {
	dst := t.TempDir()
	if err := syncSharedCredentials("/nonexistent/path/xyz", dst, 2000); err != nil {
		t.Fatalf("missing source must be no-op, got: %v", err)
	}
}

func TestSyncSharedCredentials_SourceEmpty(t *testing.T) {
	src := t.TempDir() // exists, no .credentials*
	dst := t.TempDir()
	if err := syncSharedCredentials(src, dst, 2000); err != nil {
		t.Fatalf("empty source must be no-op, got: %v", err)
	}
}

func TestSyncSharedCredentials_Overwrite(t *testing.T) {
	src := t.TempDir()
	dst := t.TempDir()
	writeCredFile(t, src, ".credentials.json", `new`)
	writeCredFile(t, dst, ".credentials.json", `old`)

	if err := syncSharedCredentials(src, dst, 2000); err != nil {
		t.Fatalf("sync: %v", err)
	}
	b, _ := os.ReadFile(filepath.Join(dst, ".credentials.json"))
	if string(b) != `new` {
		t.Fatalf("target not overwritten, content = %q", string(b))
	}
}
