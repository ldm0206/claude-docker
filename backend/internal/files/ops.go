package files

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
)

// Entry is one directory listing item. ModTime is unix seconds.
type Entry struct {
	Name    string `json:"name"`
	Size    int64  `json:"size"`
	IsDir   bool   `json:"isDir"`
	ModTime int64  `json:"modTime"`
}

// List returns the entries of rel under root. rel=="" lists root itself.
func List(root, rel string) ([]Entry, error) {
	abs, err := Resolve(root, rel)
	if err != nil {
		return nil, err
	}
	f, err := os.Open(abs)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	names, err := f.Readdirnames(-1)
	if err != nil {
		return nil, err
	}
	out := make([]Entry, 0, len(names))
	for _, name := range names {
		fi, err := os.Lstat(filepath.Join(abs, name))
		if err != nil {
			continue // race: entry disappeared; skip
		}
		out = append(out, Entry{
			Name:    name,
			Size:    fi.Size(),
			IsDir:   fi.IsDir(),
			ModTime: fi.ModTime().Unix(),
		})
	}
	return out, nil
}

// Mkdir creates rel (and parents) under root.
func Mkdir(root, rel string) error {
	abs, err := Resolve(root, rel)
	if err != nil {
		return err
	}
	return os.MkdirAll(abs, 0o755)
}

// Rename moves fromRel to toRel. Both must resolve under root. The destination
// parent directory must already exist (os.Rename semantics).
func Rename(root, fromRel, toRel string) error {
	from, err := Resolve(root, fromRel)
	if err != nil {
		return err
	}
	to, err := Resolve(root, toRel)
	if err != nil {
		return err
	}
	return os.Rename(from, to)
}

// Delete removes rel (recursive if a directory).
func Delete(root, rel string) error {
	abs, err := Resolve(root, rel)
	if err != nil {
		return err
	}
	return os.RemoveAll(abs)
}

// SaveText writes content to rel under root (truncating).
func SaveText(root, rel, content string) error {
	abs, err := Resolve(root, rel)
	if err != nil {
		return err
	}
	return os.WriteFile(abs, []byte(content), 0o644)
}

// ReadText reads the file at rel under root.
func ReadText(root, rel string) (string, error) {
	abs, err := Resolve(root, rel)
	if err != nil {
		return "", err
	}
	b, err := os.ReadFile(abs)
	if err != nil {
		return "", err
	}
	return string(b), nil
}

// OpenStream opens rel under root for reading (download). Caller closes.
func OpenStream(root, rel string) (*os.File, error) {
	abs, err := Resolve(root, rel)
	if err != nil {
		return nil, err
	}
	return os.Open(abs)
}

// CreateStream creates/truncates rel under root for writing (upload). Caller
// closes. Used by the upload handler to stream multipart data to disk.
func CreateStream(root, rel string) (*os.File, error) {
	abs, err := Resolve(root, rel)
	if err != nil {
		return nil, err
	}
	if err := os.MkdirAll(filepath.Dir(abs), 0o755); err != nil {
		return nil, fmt.Errorf("mkdir parent: %w", err)
	}
	return os.Create(abs)
}

// CopyToStream copies from r into a new file at rel under root, returning the
// byte count. Used for streaming uploads without buffering the whole file.
func CopyToStream(root, rel string, r io.Reader) (int64, error) {
	f, err := CreateStream(root, rel)
	if err != nil {
		return 0, err
	}
	defer f.Close()
	return io.Copy(f, r)
}
