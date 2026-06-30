//go:build linux

package system

import (
	"os"
	"path/filepath"
	"testing"
)

func writeCred(t *testing.T, dir, name, content string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
}

func TestCopyTemplateCredentials_HappyPath(t *testing.T) {
	data := t.TempDir()
	orig := DataRoot
	t.Cleanup(func() { DataRoot = orig })
	DataRoot = data

	srcDir := filepath.Join(data, "tpl", "claude-config")
	dstDir := filepath.Join(data, "bob", "claude-config")
	if err := os.MkdirAll(srcDir, 0o700); err != nil { t.Fatal(err) }
	if err := os.MkdirAll(dstDir, 0o700); err != nil { t.Fatal(err) }
	writeCred(t, srcDir, ".credentials.json", `{"token":"abc"}`)

	if err := CopyTemplateCredentials("tpl", "bob", 2000); err != nil {
		t.Fatalf("copy: %v", err)
	}
	b, err := os.ReadFile(filepath.Join(dstDir, ".credentials.json"))
	if err != nil { t.Fatalf("target missing: %v", err) }
	if string(b) != `{"token":"abc"}` { t.Fatalf("content = %q", string(b)) }
	fi, err := os.Stat(filepath.Join(dstDir, ".credentials.json"))
	if err != nil { t.Fatalf("target stat: %v", err) }
	if fi.Mode().Perm() != 0o600 { t.Fatalf("perm = %o, want 0600", fi.Mode().Perm()) }
}

// Only .credentials.json is copied; other files in the template dir are ignored.
func TestCopyTemplateCredentials_OnlyCredentialsCopied(t *testing.T) {
	data := t.TempDir()
	orig := DataRoot
	t.Cleanup(func() { DataRoot = orig })
	DataRoot = data

	srcDir := filepath.Join(data, "tpl", "claude-config")
	dstDir := filepath.Join(data, "bob", "claude-config")
	if err := os.MkdirAll(srcDir, 0o700); err != nil { t.Fatal(err) }
	if err := os.MkdirAll(dstDir, 0o700); err != nil { t.Fatal(err) }
	writeCred(t, srcDir, ".credentials.json", `x`)
	writeCred(t, srcDir, "settings.json", `y`)
	if err := os.MkdirAll(filepath.Join(srcDir, "projects"), 0o755); err != nil { t.Fatal(err) }

	if err := CopyTemplateCredentials("tpl", "bob", 2000); err != nil {
		t.Fatalf("copy: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dstDir, "settings.json")); !os.IsNotExist(err) {
		t.Fatalf("settings.json must NOT be copied: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dstDir, "projects")); !os.IsNotExist(err) {
		t.Fatalf("projects/ must NOT be copied: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dstDir, ".credentials.json")); err != nil {
		t.Fatalf(".credentials.json should be copied: %v", err)
	}
}

func TestCopyTemplateCredentials_EmptyTemplateUser(t *testing.T) {
	data := t.TempDir()
	orig := DataRoot
	t.Cleanup(func() { DataRoot = orig })
	DataRoot = data
	dstDir := filepath.Join(data, "bob", "claude-config")
	if err := os.MkdirAll(dstDir, 0o700); err != nil { t.Fatal(err) }

	if err := CopyTemplateCredentials("", "bob", 2000); err != nil {
		t.Fatalf("empty templateUser must be no-op, got: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dstDir, ".credentials.json")); !os.IsNotExist(err) {
		t.Fatalf("nothing should be copied: %v", err)
	}
}

func TestCopyTemplateCredentials_SelfCopySkipped(t *testing.T) {
	data := t.TempDir()
	orig := DataRoot
	t.Cleanup(func() { DataRoot = orig })
	DataRoot = data
	dir := filepath.Join(data, "tpl", "claude-config")
	if err := os.MkdirAll(dir, 0o700); err != nil { t.Fatal(err) }
	writeCred(t, dir, ".credentials.json", `orig`)

	if err := CopyTemplateCredentials("tpl", "tpl", 2000); err != nil {
		t.Fatalf("self-copy must be no-op, got: %v", err)
	}
	// File untouched.
	b, _ := os.ReadFile(filepath.Join(dir, ".credentials.json"))
	if string(b) != `orig` { t.Fatalf("self-copy clobbered content: %q", string(b)) }
}

func TestCopyTemplateCredentials_SourceMissing(t *testing.T) {
	data := t.TempDir()
	orig := DataRoot
	t.Cleanup(func() { DataRoot = orig })
	DataRoot = data
	srcDir := filepath.Join(data, "tpl", "claude-config")
	dstDir := filepath.Join(data, "bob", "claude-config")
	if err := os.MkdirAll(srcDir, 0o700); err != nil { t.Fatal(err) } // exists, no .credentials.json
	if err := os.MkdirAll(dstDir, 0o700); err != nil { t.Fatal(err) }

	if err := CopyTemplateCredentials("tpl", "bob", 2000); err != nil {
		t.Fatalf("missing source must be no-op, got: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dstDir, ".credentials.json")); !os.IsNotExist(err) {
		t.Fatalf("nothing should be copied: %v", err)
	}
}

func TestCopyTemplateCredentials_Overwrite(t *testing.T) {
	data := t.TempDir()
	orig := DataRoot
	t.Cleanup(func() { DataRoot = orig })
	DataRoot = data
	srcDir := filepath.Join(data, "tpl", "claude-config")
	dstDir := filepath.Join(data, "bob", "claude-config")
	if err := os.MkdirAll(srcDir, 0o700); err != nil { t.Fatal(err) }
	if err := os.MkdirAll(dstDir, 0o700); err != nil { t.Fatal(err) }
	writeCred(t, srcDir, ".credentials.json", `new`)
	writeCred(t, dstDir, ".credentials.json", `old`)

	if err := CopyTemplateCredentials("tpl", "bob", 2000); err != nil {
		t.Fatalf("copy: %v", err)
	}
	b, _ := os.ReadFile(filepath.Join(dstDir, ".credentials.json"))
	if string(b) != `new` { t.Fatalf("not overwritten, content = %q", string(b)) }
}
