package server

import (
	"bytes"
	"io"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// workspaceFor returns the workspace root for the test user "alice" and seeds
// it with an empty workspace dir on disk. The store knows alice's username;
// the handler derives /home/alice/workspace from it.
func workspaceFor(t *testing.T) string {
	t.Helper()
	// Override systemHomeRoot so /home/<user>/workspace points at our temp.
	systemHomeRoot = filepath.Join(t.TempDir(), "alice-home")
	t.Cleanup(func() { systemHomeRoot = "/home" })
	if err := os.MkdirAll(filepath.Join(systemHomeRoot, "alice", "workspace"), 0o755); err != nil {
		t.Fatal(err)
	}
	return filepath.Join(systemHomeRoot, "alice", "workspace")
}

func TestFilesList_RequiresAuth(t *testing.T) {
	s := newTestServer(t)
	workspaceFor(t)
	req := httptest.NewRequest("GET", "/api/files/list?path=", nil)
	w := httptest.NewRecorder()
	s.Routes().ServeHTTP(w, req)
	if w.Code != 401 {
		t.Fatalf("expected 401, got %d", w.Code)
	}
}

func TestFilesList_ReturnsEntries(t *testing.T) {
	s := newTestServer(t)
	wsRoot := workspaceFor(t)
	os.WriteFile(filepath.Join(wsRoot, "a.txt"), []byte("hi"), 0o644)

	req := httptest.NewRequest("GET", "/api/files/list?path=", nil)
	req.AddCookie(&http.Cookie{Name: "session", Value: loginAsAlice(t, s)})
	w := httptest.NewRecorder()
	s.Routes().ServeHTTP(w, req)
	if w.Code != 200 {
		t.Fatalf("status %d; body=%s", w.Code, w.Body.String())
	}
	if !strings.Contains(w.Body.String(), `"name":"a.txt"`) {
		t.Fatalf("missing a.txt in body: %s", w.Body.String())
	}
}

func TestFilesMkdir_CreatesDir(t *testing.T) {
	s := newTestServer(t)
	wsRoot := workspaceFor(t)
	body := strings.NewReader(`{"path":"newdir"}`)
	req := httptest.NewRequest("POST", "/api/files/mkdir", body)
	req.Header.Set("Content-Type", "application/json")
	req.AddCookie(&http.Cookie{Name: "session", Value: loginAsAlice(t, s)})
	w := httptest.NewRecorder()
	s.Routes().ServeHTTP(w, req)
	if w.Code != 200 {
		t.Fatalf("status %d; body=%s", w.Code, w.Body.String())
	}
	if _, err := os.Stat(filepath.Join(wsRoot, "newdir")); err != nil {
		t.Fatalf("dir not created: %v", err)
	}
}

func TestFilesEdit_SavesContent(t *testing.T) {
	s := newTestServer(t)
	wsRoot := workspaceFor(t)
	body := strings.NewReader(`{"path":"note.txt","content":"hello"}`)
	req := httptest.NewRequest("POST", "/api/files/edit", body)
	req.Header.Set("Content-Type", "application/json")
	req.AddCookie(&http.Cookie{Name: "session", Value: loginAsAlice(t, s)})
	w := httptest.NewRecorder()
	s.Routes().ServeHTTP(w, req)
	if w.Code != 200 {
		t.Fatalf("status %d", w.Code)
	}
	b, _ := os.ReadFile(filepath.Join(wsRoot, "note.txt"))
	if string(b) != "hello" {
		t.Errorf("got %q", b)
	}
}

func TestFilesList_RejectsEscape(t *testing.T) {
	s := newTestServer(t)
	workspaceFor(t)
	req := httptest.NewRequest("GET", "/api/files/list?path=../../etc", nil)
	req.AddCookie(&http.Cookie{Name: "session", Value: loginAsAlice(t, s)})
	w := httptest.NewRecorder()
	s.Routes().ServeHTTP(w, req)
	if w.Code != 400 {
		t.Fatalf("expected 400 for escape, got %d; body=%s", w.Code, w.Body.String())
	}
}

func TestFilesUpload_WritesFile(t *testing.T) {
	s := newTestServer(t)
	wsRoot := workspaceFor(t)
	body := &bytes.Buffer{}
	mw := multipart.NewWriter(body)
	part, err := mw.CreateFormFile("file", "up.txt")
	if err != nil {
		t.Fatal(err)
	}
	io.WriteString(part, "uploaded-bytes")
	mw.Close()

	req := httptest.NewRequest("POST", "/api/files/upload?path=", body)
	req.Header.Set("Content-Type", mw.FormDataContentType())
	req.AddCookie(&http.Cookie{Name: "session", Value: loginAsAlice(t, s)})
	w := httptest.NewRecorder()
	s.Routes().ServeHTTP(w, req)
	if w.Code != 200 {
		t.Fatalf("status %d; body=%s", w.Code, w.Body.String())
	}
	b, _ := os.ReadFile(filepath.Join(wsRoot, "up.txt"))
	if string(b) != "uploaded-bytes" {
		t.Errorf("got %q", b)
	}
}

func TestFilesDelete_RemovesFile(t *testing.T) {
	s := newTestServer(t)
	wsRoot := workspaceFor(t)
	os.WriteFile(filepath.Join(wsRoot, "gone.txt"), []byte("x"), 0o644)
	req := httptest.NewRequest("DELETE", "/api/files?path=gone.txt", nil)
	req.AddCookie(&http.Cookie{Name: "session", Value: loginAsAlice(t, s)})
	w := httptest.NewRecorder()
	s.Routes().ServeHTTP(w, req)
	if w.Code != 200 {
		t.Fatalf("status %d", w.Code)
	}
	if _, err := os.Stat(filepath.Join(wsRoot, "gone.txt")); !os.IsNotExist(err) {
		t.Fatalf("file should be gone: %v", err)
	}
}
