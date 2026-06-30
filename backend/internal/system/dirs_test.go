//go:build linux

package system

import (
	"os"
	"path/filepath"
	"testing"
)

// TestProvisionDirs_CreatesClaudeDir verifies provisioning creates
// /home/<user>/.claude as a real directory (0700, user-owned), NOT a symlink.
func TestProvisionDirs_CreatesClaudeDir(t *testing.T) {
	home := t.TempDir()
	if err := provisionDirs(home, "bob", 2000); err != nil {
		t.Fatalf("provision: %v", err)
	}
	dir := filepath.Join(home, "bob", ".claude")
	fi, err := os.Lstat(dir)
	if err != nil {
		t.Fatalf(".claude not created: %v", err)
	}
	if fi.Mode()&os.ModeSymlink != 0 {
		t.Fatal(".claude is a symlink, want a real directory")
	}
	if !fi.IsDir() {
		t.Fatalf(".claude mode = %v, want a directory", fi.Mode())
	}
	if fi.Mode().Perm() != 0o700 {
		t.Errorf(".claude perm = %o, want 0700", fi.Mode().Perm())
	}
}

// TestProvisionDirs_PreservesExistingClaude verifies provisioning does NOT
// clobber an existing real .claude directory (idempotent MkdirAll).
func TestProvisionDirs_PreservesExistingClaude(t *testing.T) {
	home := t.TempDir()
	realClaude := filepath.Join(home, "bob", ".claude")
	if err := os.MkdirAll(realClaude, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(realClaude, "keep.txt"), []byte("data"), 0o600); err != nil {
		t.Fatal(err)
	}

	if err := provisionDirs(home, "bob", 2000); err != nil {
		t.Fatalf("provision: %v", err)
	}
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

// TestProvisionDirs_CreatesClaudeSymlink verifies provisioning creates
// ~/.local/bin/claude as a symlink to the shared /opt/claude/bin/claude. This
// is what lets `claude` resolve its own launcher path without `claude install`
// (which DISABLE_UPDATES blocks).
func TestProvisionDirs_CreatesClaudeSymlink(t *testing.T) {
	home := t.TempDir()
	if err := provisionDirs(home, "bob", 2000); err != nil {
		t.Fatalf("provision: %v", err)
	}
	link := filepath.Join(home, "bob", ".local", "bin", "claude")
	fi, err := os.Lstat(link)
	if err != nil {
		t.Fatalf("~/.local/bin/claude not created: %v", err)
	}
	if fi.Mode()&os.ModeSymlink == 0 {
		t.Fatalf("~/.local/bin/claude mode = %v, want a symlink", fi.Mode())
	}
	tgt, err := os.Readlink(link)
	if err != nil {
		t.Fatalf("readlink: %v", err)
	}
	if tgt != sharedClaudeBin {
		t.Errorf("symlink target = %q, want %q", tgt, sharedClaudeBin)
	}
}

// TestProvisionDirs_SymlinkIdempotent verifies re-provisioning does not fail and
// keeps the symlink pointing at the right target.
func TestProvisionDirs_SymlinkIdempotent(t *testing.T) {
	home := t.TempDir()
	if err := provisionDirs(home, "bob", 2000); err != nil {
		t.Fatalf("first provision: %v", err)
	}
	if err := provisionDirs(home, "bob", 2000); err != nil {
		t.Fatalf("second provision: %v", err)
	}
	link := filepath.Join(home, "bob", ".local", "bin", "claude")
	tgt, err := os.Readlink(link)
	if err != nil {
		t.Fatalf("readlink after re-provision: %v", err)
	}
	if tgt != sharedClaudeBin {
		t.Errorf("symlink target after re-provision = %q, want %q", tgt, sharedClaudeBin)
	}
}

// TestProvisionDirs_ReplacesStaleSymlink verifies a pre-existing symlink that
// points elsewhere is replaced with the correct target.
func TestProvisionDirs_ReplacesStaleSymlink(t *testing.T) {
	home := t.TempDir()
	localBin := filepath.Join(home, "bob", ".local", "bin")
	if err := os.MkdirAll(localBin, 0o755); err != nil {
		t.Fatal(err)
	}
	stale := filepath.Join(localBin, "claude")
	if err := os.Symlink("/usr/bin/false", stale); err != nil {
		t.Fatal(err)
	}
	if err := provisionDirs(home, "bob", 2000); err != nil {
		t.Fatalf("provision: %v", err)
	}
	tgt, err := os.Readlink(stale)
	if err != nil {
		t.Fatalf("readlink: %v", err)
	}
	if tgt != sharedClaudeBin {
		t.Errorf("stale symlink not replaced: target = %q, want %q", tgt, sharedClaudeBin)
	}
}
