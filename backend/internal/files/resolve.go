// Package files provides path-safe filesystem helpers for the in-browser Web
// file manager. The central guarantee is that no operation escapes the user's
// workspace root: every path is cleaned, joined under the root, and checked
// (including symlink resolution) to remain within the root before any fs op.
package files

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// Resolve returns the absolute path of rel under root, ensuring the result
// stays within root. It rejects:
//   - parent-dir escapes (../);
//   - absolute paths that are not root itself;
//   - symlinks whose real target lies outside root.
//
// rel may be "" or "." (resolves to root). The returned path is cleaned.
func Resolve(root, rel string) (string, error) {
	cleanRoot := filepath.Clean(root)
	rel = filepath.Clean(rel)
	if rel == "." || rel == "" {
		return cleanRoot, nil
	}

	var joined string
	isWindowsAbs := strings.HasPrefix(rel, string(filepath.Separator)) || strings.HasPrefix(rel, "/") || strings.HasPrefix(rel, "\\")
	if filepath.IsAbs(rel) || isWindowsAbs {
		// Only root itself is allowed as an absolute path.
		if rel != cleanRoot {
			return "", fmt.Errorf("path %q is absolute and not the workspace root", rel)
		}
		joined = cleanRoot
	} else {
		joined = filepath.Join(cleanRoot, rel)
	}

	// Reject if the cleaned join does not sit under root (catches ../).
	if !isUnder(joined, cleanRoot) {
		return "", fmt.Errorf("path %q escapes workspace", rel)
	}

	// Resolve symlinks; if the real target is outside root, refuse.
	real, err := filepath.EvalSymlinks(joined)
	if err != nil {
		// Non-existent path: nothing to resolve. The cleaned join under root
		// is safe to return (caller will create it).
		if os.IsNotExist(err) {
			return joined, nil
		}
		return "", fmt.Errorf("resolve symlinks: %w", err)
	}
	if !isUnder(real, cleanRoot) {
		return "", fmt.Errorf("path %q resolves outside workspace", rel)
	}

	return joined, nil
}

// isUnder reports whether path == root or path is a descendant of root.
func isUnder(path, root string) bool {
	if path == root {
		return true
	}
	if !strings.HasSuffix(root, string(os.PathSeparator)) {
		root = root + string(os.PathSeparator)
	}
	if strings.HasPrefix(path, root) {
		return true
	}
	return false
}
