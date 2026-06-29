package files

import (
	"os"
	"path/filepath"
	"testing"
)

func TestResolve_CleanAndJoins(t *testing.T) {
	root := t.TempDir()
	got, err := Resolve(root, "sub/file.txt")
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	want := filepath.Join(root, "sub", "file.txt")
	if got != want {
		t.Errorf("got %q want %q", got, want)
	}
}

func TestResolve_RejectsParentEscape(t *testing.T) {
	root := t.TempDir()
	_, err := Resolve(root, "../../etc/passwd")
	if err == nil {
		t.Fatal("expected escape error, got nil")
	}
}

func TestResolve_RejectsAbsoluteOutsideRoot(t *testing.T) {
	root := t.TempDir()
	_, err := Resolve(root, "/etc/passwd")
	if err == nil {
		t.Fatal("expected error for absolute path outside root")
	}
	// An absolute path EQUAL to root is allowed (lists root itself).
	got, err := Resolve(root, root)
	if err != nil {
		t.Fatalf("root itself should resolve: %v", err)
	}
	if got != root {
		t.Errorf("got %q want %q", got, root)
	}
}

func TestResolve_RejectsSymlinkEscape(t *testing.T) {
	root := t.TempDir()
	// Create a symlink inside root that points outside root.
	target := t.TempDir() // outside root
	link := filepath.Join(root, "evil")
	if err := os.Symlink(target, link); err != nil {
		// Windows may require privileges for symlinks; skip if unsupported.
		t.Skipf("cannot create symlink: %v", err)
	}
	_, err := Resolve(root, "evil")
	if err == nil {
		t.Fatal("expected error for symlink escaping root")
	}
}

func TestResolve_AllowsSymlinkInsideRoot(t *testing.T) {
	root := t.TempDir()
	// sub-real exists inside root; link points to it.
	real := filepath.Join(root, "sub-real")
	if err := os.MkdirAll(real, 0o755); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(root, "sub-link")
	if err := os.Symlink(real, link); err != nil {
		t.Skipf("cannot create symlink: %v", err)
	}
	got, err := Resolve(root, "sub-link")
	if err != nil {
		t.Fatalf("in-root symlink should resolve: %v", err)
	}
	if got != link {
		t.Errorf("got %q want %q", got, link)
	}
}
