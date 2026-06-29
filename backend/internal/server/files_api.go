package server

import (
	"encoding/json"
	"io"
	"net/http"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/ldm0206/claude-docker/backend/internal/files"
)

// systemHomeRoot is the root used to derive a user's workspace path
// (/home/<user>/workspace). It defaults to /home; tests override it to a temp
// dir. We keep a local var (not system.HomeRoot) so the files handlers own
// their own indirection and tests do not mutate a package-wide global that
// other packages read.
var systemHomeRoot = "/home"

const maxUploadBytes = 200 * 1024 * 1024 // 200 MB per file

// workspaceRoot returns the on-disk workspace root for the authenticated user.
func workspaceRoot(username string) string {
	return systemHomeRoot + "/" + username + "/workspace"
}

// recordFileTraffic adds file-transfer bytes to the user's monthly traffic
// bucket. rx = bytes the user RECEIVED from the server (download); tx = bytes
// the user SENT to the server (upload). Best-effort: never fails a request.
func (s *Server) recordFileTraffic(userID int, rx, tx int64) {
	if s.db == nil || (rx == 0 && tx == 0) {
		return
	}
	ym := time.Now().Format("2006-01")
	_ = s.db.AddTraffic(userID, ym, rx, tx)
}

// isPathEscape reports whether err is a files.Resolve escape/invalid error.
// Resolve returns a plain error whose message contains "escape", "absolute",
// or "outside"; we string-match rather than type-assert to keep files an
// opaque package.
func isPathEscape(err error) bool {
	if err == nil {
		return false
	}
	msg := err.Error()
	for _, k := range []string{"escape", "absolute", "outside"} {
		if strings.Contains(msg, k) {
			return true
		}
	}
	return false
}

// handleFilesList — GET /api/files/list?path=
func (s *Server) handleFilesList(w http.ResponseWriter, r *http.Request) {
	id, ok := IdentityFrom(r.Context())
	if !ok {
		writeJSON(w, 401, map[string]any{"error": "unauthorized"})
		return
	}
	rel := r.URL.Query().Get("path")
	entries, err := files.List(workspaceRoot(id.Username), rel)
	if err != nil {
		if isPathEscape(err) {
			writeJSON(w, 400, map[string]any{"error": "invalid path"})
			return
		}
		writeJSON(w, 400, map[string]any{"error": "list failed"})
		return
	}
	writeJSON(w, 200, entries)
}

// handleFilesDownload — GET /api/files/download?path=
func (s *Server) handleFilesDownload(w http.ResponseWriter, r *http.Request) {
	id, ok := IdentityFrom(r.Context())
	if !ok {
		writeJSON(w, 401, map[string]any{"error": "unauthorized"})
		return
	}
	rel := r.URL.Query().Get("path")
	root := workspaceRoot(id.Username)
	f, err := files.OpenStream(root, rel)
	if err != nil {
		writeJSON(w, 400, map[string]any{"error": "invalid path"})
		return
	}
	defer f.Close()
	fi, err := f.Stat()
	if err != nil {
		writeJSON(w, 500, map[string]any{"error": "stat failed"})
		return
	}
	w.Header().Set("Content-Disposition", "attachment; filename=\""+filepath.Base(rel)+"\"")
	w.Header().Set("Content-Type", "application/octet-stream")
	w.Header().Set("Content-Length", strconv.FormatInt(fi.Size(), 10))
	n, _ := io.Copy(w, f)
	s.recordFileTraffic(id.UserID, n, 0) // download = server→user = rx
}

// handleFilesUpload — POST /api/files/upload?path= (multipart field "file")
func (s *Server) handleFilesUpload(w http.ResponseWriter, r *http.Request) {
	id, ok := IdentityFrom(r.Context())
	if !ok {
		writeJSON(w, 401, map[string]any{"error": "unauthorized"})
		return
	}
	// 200 MB hard cap.
	r.Body = http.MaxBytesReader(w, r.Body, maxUploadBytes)
	if err := r.ParseMultipartForm(32 << 20); err != nil {
		writeJSON(w, 413, map[string]any{"error": "upload too large"})
		return
	}
	rel := r.URL.Query().Get("path")
	file, hdr, err := r.FormFile("file")
	if err != nil {
		writeJSON(w, 400, map[string]any{"error": "no file"})
		return
	}
	defer file.Close()
	root := workspaceRoot(id.Username)
	dest := rel
	if dest != "" {
		dest = dest + "/" + hdr.Filename
	} else {
		dest = hdr.Filename
	}
	n, err := files.CopyToStream(root, dest, file)
	if err != nil {
		writeJSON(w, 400, map[string]any{"error": "save failed"})
		return
	}
	s.recordFileTraffic(id.UserID, 0, n) // upload = user→server = tx
	writeJSON(w, 200, map[string]any{"name": hdr.Filename, "size": n})
}

type mkdirReq struct {
	Path string `json:"path"`
}

func (s *Server) handleFilesMkdir(w http.ResponseWriter, r *http.Request) {
	id, ok := IdentityFrom(r.Context())
	if !ok {
		writeJSON(w, 401, map[string]any{"error": "unauthorized"})
		return
	}
	var b mkdirReq
	if err := json.NewDecoder(r.Body).Decode(&b); err != nil || b.Path == "" {
		writeJSON(w, 400, map[string]any{"error": "bad body"})
		return
	}
	if err := files.Mkdir(workspaceRoot(id.Username), b.Path); err != nil {
		writeJSON(w, 400, map[string]any{"error": "mkdir failed"})
		return
	}
	writeJSON(w, 200, map[string]any{"ok": true})
}

type renameReq struct {
	From string `json:"from"`
	To   string `json:"to"`
}

func (s *Server) handleFilesRename(w http.ResponseWriter, r *http.Request) {
	id, ok := IdentityFrom(r.Context())
	if !ok {
		writeJSON(w, 401, map[string]any{"error": "unauthorized"})
		return
	}
	var b renameReq
	if err := json.NewDecoder(r.Body).Decode(&b); err != nil || b.From == "" || b.To == "" {
		writeJSON(w, 400, map[string]any{"error": "bad body"})
		return
	}
	if err := files.Rename(workspaceRoot(id.Username), b.From, b.To); err != nil {
		writeJSON(w, 400, map[string]any{"error": "rename failed"})
		return
	}
	writeJSON(w, 200, map[string]any{"ok": true})
}

type editReq struct {
	Path    string `json:"path"`
	Content string `json:"content"`
}

func (s *Server) handleFilesEdit(w http.ResponseWriter, r *http.Request) {
	id, ok := IdentityFrom(r.Context())
	if !ok {
		writeJSON(w, 401, map[string]any{"error": "unauthorized"})
		return
	}
	var b editReq
	if err := json.NewDecoder(r.Body).Decode(&b); err != nil || b.Path == "" {
		writeJSON(w, 400, map[string]any{"error": "bad body"})
		return
	}
	if err := files.SaveText(workspaceRoot(id.Username), b.Path, b.Content); err != nil {
		writeJSON(w, 400, map[string]any{"error": "save failed"})
		return
	}
	writeJSON(w, 200, map[string]any{"ok": true})
}

func (s *Server) handleFilesDelete(w http.ResponseWriter, r *http.Request) {
	id, ok := IdentityFrom(r.Context())
	if !ok {
		writeJSON(w, 401, map[string]any{"error": "unauthorized"})
		return
	}
	rel := r.URL.Query().Get("path")
	if rel == "" {
		writeJSON(w, 400, map[string]any{"error": "missing path"})
		return
	}
	if err := files.Delete(workspaceRoot(id.Username), rel); err != nil {
		writeJSON(w, 400, map[string]any{"error": "delete failed"})
		return
	}
	writeJSON(w, 200, map[string]any{"ok": true})
}
