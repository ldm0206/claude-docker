//go:build linux

package system

import (
	"os"
	"path/filepath"
	"testing"
)

func TestProvisionDirs(t *testing.T) {
	tmp := t.TempDir()
	homeRoot := filepath.Join(tmp, "home")
	// override package roots for test (see dirs.go)
	if err := provisionDirs(homeRoot, "bob", 2001); err != nil {
		t.Fatalf("provision: %v", err)
	}
	for _, p := range []string{
		filepath.Join(homeRoot, "bob"),
		filepath.Join(homeRoot, "bob", "workspace"),
		filepath.Join(homeRoot, "bob", ".claude"),
	} {
		if _, err := os.Stat(p); err != nil {
			t.Fatalf("expected %s: %v", p, err)
		}
	}
}
