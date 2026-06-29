//go:build linux

package system

import (
	"os"
	"path/filepath"
	"testing"
)

// TestProvisionDirs_CreatesClaudeSymlink verifies provisioning symlinks
// /home/<user>/.claude to /data/<user>/claude-config.
func TestProvisionDirs_CreatesClaudeSymlink(t *testing.T) {
	home := t.TempDir()
	data := t.TempDir()
	err := provisionDirs(home, data, "bob", 2000)
	if err != nil {
		t.Fatalf("provision: %v", err)
	}
	link := filepath.Join(home, "bob", ".claude")
	fi, err := os.Lstat(link)
	if err != nil {
		t.Fatalf(".claude not created: %v", err)
	}
	if fi.Mode()&os.ModeSymlink == 0 {
		t.Fatal(".claude is not a symlink")
	}
	target, err := os.Readlink(link)
	if err != nil {
		t.Fatalf("readlink: %v", err)
	}
	want := filepath.Join(data, "bob", "claude-config")
	if target != want {
		t.Errorf("symlink target = %q, want %q", target, want)
	}
}

// TestProvisionDirs_SkipsExistingClaude verifies provisioning does NOT clobber
// an existing real .claude directory.
func TestProvisionDirs_SkipsExistingClaude(t *testing.T) {
	home := t.TempDir()
	data := t.TempDir()
	// Pre-create a real .claude dir with a file inside.
	realClaude := filepath.Join(home, "bob", ".claude")
	if err := os.MkdirAll(realClaude, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(realClaude, "keep.txt"), []byte("data"), 0o644); err != nil {
		t.Fatal(err)
	}

	if err := provisionDirs(home, data, "bob", 2000); err != nil {
		t.Fatalf("provision: %v", err)
	}
	// The real dir must still be a dir (not replaced by a symlink), and the
	// file must survive.
	fi, err := os.Lstat(realClaude)
	if err != nil {
		t.Fatal(err)
	}
	if fi.Mode()&os.ModeSymlink != 0 {
		t.Fatal("existing real .claude was replaced by a symlink")
	}
	b, err := os.ReadFile(filepath.Join(realClaude, "keep.txt"))
	if err != nil || string(b) != "data" {
		t.Errorf("existing .claude file lost: %v", err)
	}
}

// TestEnsureSharedCredentialDir verifies the shared source dir is created
// 0700 root-owned and is idempotent.
func TestEnsureSharedCredentialDir(t *testing.T) {
	data := t.TempDir()
	orig := DataRoot
	t.Cleanup(func() { DataRoot = orig })
	DataRoot = data

	if err := EnsureSharedCredentialDir(); err != nil {
		t.Fatalf("ensure: %v", err)
	}
	dir := filepath.Join(data, "shared", "claude-config")
	fi, err := os.Stat(dir)
	if err != nil {
		t.Fatalf("shared dir missing: %v", err)
	}
	if fi.Mode().Perm() != 0o700 {
		t.Fatalf("perm = %o, want 0700", fi.Mode().Perm())
	}
	// Idempotent: second call must not error.
	if err := EnsureSharedCredentialDir(); err != nil {
		t.Fatalf("second ensure: %v", err)
	}
}
