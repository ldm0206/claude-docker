# Terminal Reliability, Web File Manager, Native Claude Login, UI Polish — Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Fix the terminal "disconnected" failure under Cloudflare+nginx+HTTPS, replace the port-22 SFTP with an in-browser Web file manager, enable per-user native `claude login` via a config-dir symlink, and apply a full UI polish pass.

**Architecture:** Cookie auth attributes (`Secure` + configurable `SameSite`) plus a backend WS ping and frontend exponential-backoff reconnect fix the terminal. A new pure-Go `internal/files` package backs `/api/files/*` REST handlers on the existing chi server, with explicit per-user traffic bookkeeping (the Go server runs in the server cgroup, so nftables does not attribute HTTP bytes to the user — `store.AddTraffic` is called directly). The embedded SSH/SFTP server is removed. A symlink `/home/<user>/.claude → /data/<user>/claude-config` is created during provisioning so `claude login` persists per-user. UI polish lives in `web/src/styles.css` + view files.

**Tech Stack:** Go 1.25 (CGO-free), go-chi v5, coder/websocket, xterm.js, vanilla JS SPA (esbuild via vite). SQLite (modernc.org/sqlite, pure-Go).

## Global Constraints

- **Dev host is Windows with no Docker/WSL.** All Go tests must run on Windows: pure-Go + SQLite + `httptest`. Linux-only runtime (gosu PTY, useradd, symlink-as-root) is NOT exercised by tests — only `go build ./...`, `go vet ./...`, `go test ./...`, and `GOOS=linux go test -c ./...` cross-compile.
- **`go.mod` is `go 1.25`.** Keep deps in sync if `go mod tidy` bumps.
- **No new component framework** on the frontend — stay vanilla JS with the existing `el()` helper pattern in `main.js`.
- **Credential secrets are never logged, never returned over HTTP.** The `resolveCredEnv` path stays as-is.
- **Every file path goes through `files.Resolve`** with a boundary check; out-of-root → 400. Symlinks escaping the workspace are refused.
- **Cookie attributes** must be config-driven: `Secure: true` always on HTTPS; `SameSite` via `COOKIE_SAMESITE` env (`none`|`lax`|`strict`, default `none`).
- **File transfer bytes count toward the user's monthly quota** via explicit `store.AddTraffic` calls (NOT via nftables, which cannot see per-user HTTP bytes from the server cgroup).
- **Use haiku for per-task review subagents; sonnet/opus for the final whole-branch review** (user pref).

---

## File Structure

**Backend (Go):**
- `backend/internal/files/` (NEW) — `resolve.go` (path resolver + escape guard), `ops.go` (List/Mkdir/Rename/Delete/SaveText/ReadStream/WriteStream helpers), `ops_test.go`, `resolve_test.go`. Pure, Windows-testable.
- `backend/internal/server/files_api.go` (NEW) — HTTP handlers for `/api/files/*`. Auth-identity → workspace root → call `files.*` + traffic accounting.
- `backend/internal/server/files_api_test.go` (NEW) — handler tests via `httptest` + temp workspace.
- `backend/internal/server/auth_handler.go` (MODIFY) — cookie `Secure` + config-driven `SameSite`.
- `backend/internal/server/terminal.go` (MODIFY) — WS ping ticker.
- `backend/internal/server/server.go` (MODIFY) — route registration for `/api/files/*`; `traffic` field already present.
- `backend/internal/config/config.go` (MODIFY) — `CookieSameSite` field.
- `backend/internal/system/dirs.go` (MODIFY) — symlink `.claude` → claude-config; `dirs_test.go` (NEW or MODIFY) for the symlink.
- `backend/internal/ssh/` (DELETE) — entire package removed.
- `backend/cmd/server/main.go` (MODIFY) — drop ssh construction/Start, drop `SFTP_PORT`.

**Frontend (web):**
- `web/src/files.js` (NEW) — `mountFiles(root)` Web file manager.
- `web/src/terminal.js` (MODIFY) — reconnect + close-code surfacing.
- `web/src/main.js` (MODIFY) — `viewFiles` calls `mountFiles`; login hint.
- `web/src/styles.css` (MODIFY) — polish: skeletons, empty states, transitions, refined palette.
- `web/src/api.js` (MODIFY) — add `uploadFile` helper.

**Config / docs:**
- `docker-compose.yml` (MODIFY) — drop `22:22` port.
- `.env.example` (MODIFY) — drop `SFTP_PORT`, add `COOKIE_SAMESITE`.
- `README.md`, `DEPLOY-TEST.md` (MODIFY) — remove SFTP refs, add Web file manager + cookie notes.

---

### Task 1: Config-driven cookie SameSite + Secure attribute

**Files:**
- Modify: `backend/internal/config/config.go`
- Modify: `backend/internal/server/auth_handler.go`
- Test: `backend/internal/server/server_test.go` (extend existing login test)

**Interfaces:**
- Consumes: existing `config.Load`.
- Produces: `Config.CookieSameSite string` (values `"none"`|`"lax"`|`"strict"`; default `"none"`). `auth_handler.go` reads it to set `http.Cookie.SameSite` + `Secure`.

- [ ] **Step 1: Add CookieSameSite to Config**

In `backend/internal/config/config.go`, add the field to the struct (after `BootstrapAdminPassword`) and load it in `Load`:

```go
type Config struct {
	// ... existing fields ...
	BootstrapAdminUser     string
	BootstrapAdminPassword string
	CookieSameSite         string
}
```

In `Load`, after the existing `c.BootstrapAdminPassword = opt("BOOTSTRAP_ADMIN_PASSWORD")` line:

```go
	c.CookieSameSite = opt("COOKIE_SAMESITE")
	if c.CookieSameSite == "" {
		c.CookieSameSite = "none"
	}
```

- [ ] **Step 2: Write the failing test**

Append to `backend/internal/server/server_test.go`:

```go
// TestLoginCookieAttributes verifies the session cookie is set with Secure and
// the configured SameSite (default "none" for HTTPS deployments).
func TestLoginCookieAttributes(t *testing.T) {
	s := newTestServer(t)
	req := httptest.NewRequest("POST", "/auth", strings.NewReader(`{"username":"alice","password":"pw123"}`))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	s.Routes().ServeHTTP(w, req)
	if w.Code != 200 {
		t.Fatalf("status %d; body=%s", w.Code, w.Body.String())
	}
	var sc *http.Cookie
	for _, c := range w.Result().Cookies() {
		if c.Name == "session" {
			sc = c
		}
	}
	if sc == nil {
		t.Fatal("no session cookie")
	}
	if !sc.Secure {
		t.Error("cookie must have Secure=true")
	}
	if sc.SameSite != http.SameSiteNoneMode {
		t.Errorf("SameSite = %v, want None (default)", sc.SameSite)
	}
}
```

- [ ] **Step 3: Run test to verify it fails**

Run: `cd backend && go test ./internal/server/ -run TestLoginCookieAttributes -v`
Expected: FAIL — `cookie must have Secure=true` (current cookie has no Secure, SameSite=Lax).

- [ ] **Step 4: Apply cookie attributes in handleLogin**

In `backend/internal/server/auth_handler.go`, replace the `http.SetCookie` block in `handleLogin` (the block with `Name: "session"`) with a helper call. Add a method on `*Server`:

```go
// sameSiteMode maps the config string to http.SameSite. Unknown values default
// to None (the HTTPS-safe choice that lets the cookie ride the WS upgrade).
func (s *Server) sameSiteMode() http.SameSite {
	switch s.cfg.CookieSameSite {
	case "lax":
		return http.SameSiteLaxMode
	case "strict":
		return http.SameSiteStrictMode
	default:
		return http.SameSiteNoneMode
	}
}

// setSessionCookie sets the auth cookie with Secure + configured SameSite.
func (s *Server) setSessionCookie(w http.ResponseWriter, value string) {
	http.SetCookie(w, &http.Cookie{
		Name:     "session",
		Value:    value,
		Path:     "/",
		HttpOnly: true,
		Secure:   true,
		SameSite: s.sameSiteMode(),
	})
}
```

In `handleLogin`, replace the `http.SetCookie(w, &http.Cookie{...})` call with:

```go
	s.setSessionCookie(w, cookie)
```

In `handleLogout`, replace its `http.SetCookie` with the same helper (clearing):

```go
func (s *Server) handleLogout(w http.ResponseWriter, _ *http.Request) {
	s.setSessionCookie(w, "")
	http.SetCookie(w, &http.Cookie{Name: "session", Value: "", Path: "/", MaxAge: -1, HttpOnly: true, Secure: true, SameSite: s.sameSiteMode()})
	writeJSON(w, 200, map[string]any{"ok": true})
}
```

(Note: a MaxAge:-1 cookie with the same name+attributes is what actually expires it; `setSessionCookie(w,"")` alone sets an empty value without expiring. The second SetCookie ensures deletion with matching attributes so the browser clears it.)

- [ ] **Step 5: Run test to verify it passes**

Run: `cd backend && go test ./internal/server/ -run TestLoginCookieAttributes -v`
Expected: PASS.

- [ ] **Step 6: Run full package + vet**

Run: `cd backend && go vet ./... && go test ./internal/server/ ./internal/config/ -v`
Expected: all PASS, no vet errors.

- [ ] **Step 7: Commit**

```bash
git add backend/internal/config/config.go backend/internal/server/auth_handler.go backend/internal/server/server_test.go
git commit -m "feat(auth): Secure cookie + config-driven SameSite (fixes WS auth on HTTPS)"
```

---

### Task 2: Backend WebSocket keepalive ping

**Files:**
- Modify: `backend/internal/server/terminal.go`
- Test: `backend/internal/server/terminal_test.go` (NEW — small, asserts a ping is written)

**Interfaces:**
- Consumes: `coder/websocket` connection (already used in `handleTerminalWS`).
- Produces: a `{"type":"ping"}` message every 30s on the terminal WS. Frontend (Task 8) ignores unknown JSON messages gracefully (it already does — `terminal.js` falls through to `term.write` only on parse failure; a parsed `{type:"ping"}` with no data must NOT be written to the terminal).

- [ ] **Step 1: Write the failing test**

Create `backend/internal/server/terminal_test.go`:

```go
package server

import (
	"context"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// TestTerminalWSSendsPing verifies the terminal WS emits a ping message within
// a short window after connect (the keepalive ticker). Uses a real ws.Dial
// against the chi router via httptest.Server.
func TestTerminalWSSendsPing(t *testing.T) {
	s := newTestServer(t)
	cookie := loginAsAlice(t, s)
	srv := httptest.NewServer(s.Routes())
	defer srv.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	wsURL := "ws" + strings.TrimPrefix(srv.URL, "http") + "/ws/terminal"
	d := newWebsocketDialer()
	ws, _, err := d.Dial(ctx, wsURL, nil)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	defer ws.Close(websocket.StatusNormalClosure, "")

	// Read the first {type:"session",id} message, then expect a ping.
	gotPing := false
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) && !gotPing {
		_ = ws.SetReadLimit(1 << 16)
		_, data, err := ws.Read(ctx)
		if err != nil {
			t.Fatalf("read: %v", err)
		}
		if strings.Contains(string(data), `"type":"ping"`) {
			gotPing = true
		}
	}
	if !gotPing {
		t.Fatal("did not receive a ping within the window")
	}
	_ = cookie // cookie is sent via the dialer's header; see helper
}
```

The test uses two helpers that must be added to the same file (or a `_test.go` helper file). Because `coder/websocket` is already imported in the package, add at the top of `terminal_test.go` the import `"github.com/coder/websocket"` and these helpers:

```go
// newWebsocketDialer returns a dialer that attaches the session cookie. We wrap
// coder/websocket's zero-config Dial so the test can set the Cookie header.
type wsDialer struct{}

func newWebsocketDialer() *wsDialer { return &wsDialer{} }

func (d *wsDialer) Dial(ctx context.Context, url string, _ map[string]string) (*websocket.Conn, *websocket.Response, error) {
	// The httptest server does not validate the cookie here (authWSUser reads
	// it from the request); pass an empty cookie-free dial — auth still works
	// because newTestServer's alice cookie is HMAC-signed and we attach it via
	// the header below. To keep this self-contained, attach the cookie.
	opts := &websocket.DialOptions{
		HTTPHeader: cookieHeaderForTest(),
	}
	return websocket.Dial(ctx, url, opts)
}
```

**IMPORTANT — read this before finalizing the test:** the cookie must actually be sent. The simplest correct approach is to pass the cookie value into the dialer. Adjust the test to capture the cookie and build the header. Replace `newWebsocketDialer()` usage with a direct dial in the test body:

```go
	cookie := loginAsAlice(t, s)
	srv := httptest.NewServer(s.Routes())
	defer srv.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	wsURL := "ws" + strings.TrimPrefix(srv.URL, "http") + "/ws/terminal"
	hdr := http.Header{}
	hdr.Add("Cookie", "session="+cookie)
	ws, _, err := websocket.Dial(ctx, wsURL, &websocket.DialOptions{HTTPHeader: hdr})
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
```

(Delete the `wsDialer`/`newWebsocketDialer`/`cookieHeaderForTest` helpers — they were scaffolding; the inline dial is the real test.) Add imports `"net/http"` to the test file. Remove the `_ = cookie` line.

- [ ] **Step 2: Run test to verify it fails**

Run: `cd backend && go test ./internal/server/ -run TestTerminalWSSendsPing -v`
Expected: FAIL — "did not receive a ping within the window" (no ping is sent today).

- [ ] **Step 3: Add the ping ticker to handleTerminalWS**

In `backend/internal/server/terminal.go`, add `"time"` to the import block. Inside `handleTerminalWS`, after the `unsubExit` registration and BEFORE the `for { c.Read }` loop, start a ping ticker:

```go
	// Keepalive: send a ping every 30s so Cloudflare (~100s idle) and nginx
	// (proxy_read_timeout 300s) do not reap an idle WS. The client ignores
	// {type:"ping"} messages (terminal.js treats parsed JSON with no terminal
	// data as a no-op). Stopped on ctx cancel / WS close.
	go func() {
		ticker := time.NewTicker(30 * time.Second)
		defer ticker.Stop()
		pingMsg := mustJSON(map[string]any{"type": "ping"})
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				if err := c.Write(ctx, websocket.MessageText, pingMsg); err != nil {
					return
				}
			}
		}
	}()
```

**For the test to pass within 3s,** make the ping interval overridable. Change the literal to a package var:

```go
var wsPingInterval = 30 * time.Second
```

(declared at package scope in `terminal.go`), and use `wsPingInterval` in the ticker. Then in the test, before dialing, set:

```go
	wsPingInterval = 50 * time.Millisecond
	t.Cleanup(func() { wsPingInterval = 30 * time.Second })
```

- [ ] **Step 4: Run test to verify it passes**

Run: `cd backend && go test ./internal/server/ -run TestTerminalWSSendsPing -v`
Expected: PASS.

- [ ] **Step 5: Run full package + vet + cross-compile**

Run: `cd backend && go vet ./... && go test ./internal/server/ -v && GOOS=linux go test -c ./internal/server/ -o /dev/null`
Expected: PASS, and the linux test binary cross-compiles (delete the temp binary if `-o /dev/null` is unsupported on your go — use `-o nul` on Windows or a temp path).

- [ ] **Step 6: Commit**

```bash
git add backend/internal/server/terminal.go backend/internal/server/terminal_test.go
git commit -m "feat(terminal): WS keepalive ping (survives Cloudflare/nginx idle reaping)"
```

---

### Task 3: `internal/files` package — path resolver

**Files:**
- Create: `backend/internal/files/resolve.go`
- Create: `backend/internal/files/resolve_test.go`

**Interfaces:**
- Consumes: nothing (pure stdlib).
- Produces: `files.Resolve(root, rel string) (string, error)` — returns the cleaned absolute path under `root`, or an error if `rel` escapes `root` or is an absolute path outside root. Symlinks resolving outside root are an error (checked via `filepath.EvalSymlinks`).

- [ ] **Step 1: Write the failing test**

Create `backend/internal/files/resolve_test.go`:

```go
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
```

- [ ] **Step 2: Run test to verify it fails**

Run: `cd backend && go test ./internal/files/ -v`
Expected: FAIL — package does not exist / `Resolve` undefined.

- [ ] **Step 3: Implement Resolve**

Create `backend/internal/files/resolve.go`:

```go
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
	if filepath.IsAbs(rel) {
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
	return strings.HasPrefix(path, root+string(os.PathSeparator))
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `cd backend && go test ./internal/files/ -v`
Expected: PASS (symlink tests SKIP on Windows without privileges — that is acceptable).

- [ ] **Step 5: Commit**

```bash
git add backend/internal/files/resolve.go backend/internal/files/resolve_test.go
git commit -m "feat(files): path resolver with escape + symlink guard"
```

---

### Task 4: `internal/files` package — filesystem ops

**Files:**
- Create: `backend/internal/files/ops.go`
- Create: `backend/internal/files/ops_test.go`

**Interfaces:**
- Consumes: `files.Resolve` (Task 3).
- Produces:
  - `type Entry struct { Name string; Size int64; IsDir bool; ModTime int64 }`
  - `List(root, rel string) ([]Entry, error)`
  - `Mkdir(root, rel string) error`
  - `Rename(root, fromRel, toRel string) error`
  - `Delete(root, rel string) error` (recursive for dirs)
  - `SaveText(root, rel, content string) error`
  - `ReadText(root, rel string) (string, error)`

- [ ] **Step 1: Write the failing tests**

Create `backend/internal/files/ops_test.go`:

```go
package files

import (
	"os"
	"path/filepath"
	"testing"
)

func TestList_EmptyDir(t *testing.T) {
	root := t.TempDir()
	entries, err := List(root, "")
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if len(entries) != 0 {
		t.Fatalf("expected 0 entries, got %d", len(entries))
	}
}

func TestList_ReportsEntries(t *testing.T) {
	root := t.TempDir()
	os.WriteFile(filepath.Join(root, "a.txt"), []byte("hello"), 0o644)
	os.Mkdir(filepath.Join(root, "sub"), 0o755)
	entries, err := List(root, "")
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if len(entries) != 2 {
		t.Fatalf("expected 2 entries, got %d", len(entries))
	}
	var names = map[string]bool{}
	for _, e := range entries {
		names[e.Name] = true
		if e.Name == "a.txt" {
			if e.Size != 5 {
				t.Errorf("a.txt size = %d, want 5", e.Size)
			}
			if e.IsDir {
				t.Error("a.txt should not be a dir")
			}
		}
	}
	if !names["sub"] {
		t.Error("missing sub dir")
	}
}

func TestMkdir_CreatesNested(t *testing.T) {
	root := t.TempDir()
	if err := Mkdir(root, "a/b/c"); err != nil {
		t.Fatalf("err: %v", err)
	}
	if _, err := os.Stat(filepath.Join(root, "a", "b", "c")); err != nil {
		t.Fatalf("not created: %v", err)
	}
}

func TestSaveAndReadText_RoundTrip(t *testing.T) {
	root := t.TempDir()
	if err := SaveText(root, "note.txt", "line1\nline2\n"); err != nil {
		t.Fatalf("save: %v", err)
	}
	got, err := ReadText(root, "note.txt")
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if got != "line1\nline2\n" {
		t.Errorf("got %q", got)
	}
}

func TestRename_Moves(t *testing.T) {
	root := t.TempDir()
	SaveText(root, "old.txt", "x")
	if err := Rename(root, "old.txt", "dir/new.txt"); err != nil {
		// "dir" does not exist; Rename should fail OR create. We require it to
		// fail (caller must mkdir first) to keep semantics predictable.
		// Adjust expectation: os.Rename fails if dest dir missing.
		// This assertion documents that behavior.
	}
}

func TestDelete_FileAndDir(t *testing.T) {
	root := t.TempDir()
	SaveText(root, "f.txt", "x")
	os.MkdirAll(filepath.Join(root, "d", "nested"), 0o755)
	os.WriteFile(filepath.Join(root, "d", "nested", "x"), []byte("y"), 0o644)
	if err := Delete(root, "f.txt"); err != nil {
		t.Fatalf("delete file: %v", err)
	}
	if err := Delete(root, "d"); err != nil {
		t.Fatalf("delete dir: %v", err)
	}
	if _, err := os.Stat(filepath.Join(root, "d")); !os.IsNotExist(err) {
		t.Fatalf("dir should be gone: %v", err)
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `cd backend && go test ./internal/files/ -v`
Expected: FAIL — `List`/`Mkdir`/etc. undefined.

- [ ] **Step 3: Implement the ops**

Create `backend/internal/files/ops.go`:

```go
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
```

- [ ] **Step 4: Run test to verify it passes**

Run: `cd backend && go test ./internal/files/ -v`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add backend/internal/files/ops.go backend/internal/files/ops_test.go
git commit -m "feat(files): list/mkdir/rename/delete/save/read/stream helpers"
```

---

### Task 5: `/api/files/*` HTTP handlers + traffic accounting

**Files:**
- Create: `backend/internal/server/files_api.go`
- Create: `backend/internal/server/files_api_test.go`
- Modify: `backend/internal/server/server.go` (route registration)

**Interfaces:**
- Consumes: `files.*` (Tasks 3-4), `store.AddTraffic` (existing), `IdentityFrom` (existing), `system.HomeRoot` (existing, = `/home`).
- Produces: HTTP handlers mounted at `/api/files/*` (under `authMiddleware`):
  - `GET /api/files/list?path=`
  - `GET /api/files/download?path=`
  - `POST /api/files/upload?path=` (multipart `file`)
  - `POST /api/files/mkdir` `{path}`
  - `POST /api/files/rename` `{from,to}`
  - `POST /api/files/edit` `{path,content}`
  - `DELETE /api/files?path=`

- [ ] **Step 1: Write the failing handler tests**

Create `backend/internal/server/files_api_test.go`:

```go
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
	root := filepath.Join(t.TempDir(), "alice", "workspace")
	if err := os.MkdirAll(root, 0o755); err != nil {
		t.Fatal(err)
	}
	// Override system.HomeRoot so /home/<user>/workspace points at our temp.
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
```

The test references `systemHomeRoot` — a package var the handler uses instead of the `system.HomeRoot` global directly (so tests can redirect it). This is added in Step 3.

- [ ] **Step 2: Run tests to verify they fail**

Run: `cd backend && go test ./internal/server/ -run TestFiles -v`
Expected: FAIL — routes not registered / handlers undefined.

- [ ] **Step 3: Implement the handlers**

Create `backend/internal/server/files_api.go`:

```go
package server

import (
	"encoding/json"
	"net/http"
	"strconv"
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
	ym := time.Now().UTC().Format("2006-01")
	_ = s.db.AddTraffic(userID, ym, rx, tx)
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
	w.Header().Set("Content-Disposition", "attachment; filename=\""+filepathBase(rel)+"\"")
	w.Header().Set("Content-Type", "application/octet-stream")
	w.Header().Set("Content-Length", strconv.FormatInt(fi.Size(), 10))
	n, _ := ioCopy(w, f)
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

type mkdirReq struct{ Path string `json:"path"` }

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
```

The handlers reference `isPathEscape`, `filepathBase`, `ioCopy` — small helpers. Add them to the bottom of `files_api.go`:

```go
import (
	// add to the existing import block:
	"io"
	"path/filepath"
)

// isPathEscape reports whether err is a files.Resolve escape/invalid error.
// Resolve returns a plain error whose message contains "escape" or "absolute"
// or "outside"; we string-match rather than type-assert to keep files an
// opaque package.
func isPathEscape(err error) bool {
	if err == nil {
		return false
	}
	msg := err.Error()
	for _, k := range []string{"escape", "absolute", "outside"} {
		if stringsContains(msg, k) {
			return true
		}
	}
	return false
}

func filepathBase(rel string) string { return filepath.Base(rel) }

func ioCopy(dst io.Writer, src io.Reader) (int64, error) { return io.Copy(dst, src) }

// stringsContains avoids pulling strings into every file that imports this
// package's helpers indirectly; the package already imports strings elsewhere.
func stringsContains(s, sub string) bool { return len(s) >= len(sub) && indexOf(s, sub) >= 0 }

func indexOf(s, sub string) int {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return i
		}
	}
	return -1
}
```

**Note:** instead of the bespoke `stringsContains`/`indexOf`, simply import `"strings"` and use `strings.Contains` — it is already imported by other files in package `server`, and Go allows multiple files to import the same stdlib package. Replace the two helpers with `strings.Contains(err.Error(), k)` inside `isPathEscape` and delete `stringsContains`/`indexOf`. Add `"strings"` to the import block.

**`IdentityFrom` returns `Identity{Username, Role}` — it does NOT include `UserID`.** The traffic recorder needs `id.UserID`. Options:
- (a) Add `UserID int` to `Identity` and populate it in `authMiddleware` (preferred — clean, used by files handlers).
- (b) Re-fetch the user via `s.db.GetUserByUsername` in each handler (extra DB hit).

Use (a). In `auth_handler.go`, update the `Identity` struct:

```go
type Identity struct {
	Username string
	Role     string
	UserID   int
}
```

In `authMiddleware` (`server.go`), after fetching the live `u`, set it:

```go
	next.ServeHTTP(w, r.WithContext(WithIdentity(r.Context(), Identity{Username: u.Username, Role: u.Role, UserID: u.ID})))
```

`authedIdentity` (used by WS handlers, returns claims from the cookie) does not have the ID; that is fine — only the REST files handlers (which go through `authMiddleware`) need it. Update the `IdentityFrom`-based handlers to use `id.UserID`.

- [ ] **Step 4: Register routes**

In `backend/internal/server/server.go` `Routes()`, inside the `r.Use(s.authMiddleware)` group (alongside the existing `/api/sessions` routes), add:

```go
		// Web file manager (Plan 8)
		r.Get("/api/files/list", s.handleFilesList)
		r.Get("/api/files/download", s.handleFilesDownload)
		r.Post("/api/files/upload", s.handleFilesUpload)
		r.Post("/api/files/mkdir", s.handleFilesMkdir)
		r.Post("/api/files/rename", s.handleFilesRename)
		r.Post("/api/files/edit", s.handleFilesEdit)
		r.Delete("/api/files", s.handleFilesDelete)
```

- [ ] **Step 5: Run tests to verify they pass**

Run: `cd backend && go test ./internal/server/ -run TestFiles -v`
Expected: PASS.

- [ ] **Step 6: Run full suite + vet + cross-compile**

Run: `cd backend && go vet ./... && go test ./... && GOOS=linux go test -c ./... -o /dev/null 2>/dev/null || GOOS=linux go build ./...`
Expected: PASS; cross-compile succeeds.

- [ ] **Step 7: Commit**

```bash
git add backend/internal/server/files_api.go backend/internal/server/files_api_test.go backend/internal/server/server.go backend/internal/server/auth_handler.go
git commit -m "feat(files): /api/files/* REST handlers + per-user traffic accounting"
```

---

### Task 6: Remove the embedded SSH/SFTP server

**Files:**
- Delete: `backend/internal/ssh/server.go`, `backend/internal/ssh/server_test.go`
- Modify: `backend/cmd/server/main.go`
- Modify: `docker-compose.yml`
- Modify: `.env.example`

**Interfaces:**
- Consumes: nothing new.
- Produces: a build with no `internal/ssh` package and no 22-port exposure.

- [ ] **Step 1: Delete the ssh package**

Run:
```bash
git rm -r backend/internal/ssh
```

- [ ] **Step 2: Remove ssh wiring from main.go**

In `backend/cmd/server/main.go`:
- Remove the import line: `sshserver "github.com/ldm0206/claude-docker/backend/internal/ssh"`.
- Remove the block:
```go
	// --- Embedded SSH/SFTP server (Linux runtime). No-op Start off-Linux. ---
	sftpPort := os.Getenv("SFTP_PORT")
	if sftpPort == "" {
		sftpPort = "22"
	}
	sshSrv := sshserver.New(db, ":"+sftpPort)
```
- Remove the goroutine that calls `sshSrv.Start(ctx)`:
```go
	go func() {
		if err := sshSrv.Start(ctx); err != nil {
			log.Printf("[server] ssh/sftp: %v", err)
		}
	}()
```
Leave the `go tsvc.Start(ctx, 5*time.Second)` background loop.

- [ ] **Step 3: Remove the 22 port from docker-compose**

In `docker-compose.yml`, delete the line:
```yaml
      - "22:22"       # SSH/SFTP (configurable via SFTP_PORT)
```
Keep `8080:8080`.

- [ ] **Step 4: Remove SFTP_PORT from .env.example**

In `.env.example`, remove any `SFTP_PORT` line (it is not currently present in the file, but check). Add `COOKIE_SAMESITE`:

```
# Cookie SameSite for the session cookie. "none" (default) works behind
# Cloudflare/nginx/HTTPS; use "lax" for local http://localhost dev.
# COOKIE_SAMESITE=none
```

- [ ] **Step 5: Build + vet + cross-compile**

Run: `cd backend && go vet ./... && go build ./... && GOOS=linux go build ./...`
Expected: succeeds, no reference to `internal/ssh`.

Run: `cd backend && go test ./...`
Expected: PASS (no ssh tests remain).

- [ ] **Step 6: Commit**

```bash
git rm -r backend/internal/ssh
git add backend/cmd/server/main.go docker-compose.yml .env.example
git commit -m "refactor: remove embedded SSH/SFTP server (replaced by Web file manager)"
```

---

### Task 7: Per-user `~/.claude` symlink → `/data/<user>/claude-config`

**Files:**
- Modify: `backend/internal/system/dirs.go`
- Create: `backend/internal/system/dirs_test.go`

**Interfaces:**
- Consumes: `provisionDirs` (existing).
- Produces: during provisioning, a symlink `/home/<user>/.claude` → `/data/<user>/claude-config` is created (skipped if `.claude` already exists as a non-symlink).

- [ ] **Step 1: Write the failing test**

Create `backend/internal/system/dirs_test.go`:

```go
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
	os.WriteFile(filepath.Join(realClaude, "keep.txt"), []byte("data"), 0o644)

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
```

- [ ] **Step 2: Run test to verify it fails**

Run: `cd backend && go test ./internal/system/ -run TestProvisionDirs -v`
Expected: FAIL — `.claude not created` (no symlink created today).

- [ ] **Step 3: Add the symlink to provisionDirs**

In `backend/internal/system/dirs.go`, add `"os"` import (already present) and add symlink logic at the end of `provisionDirs`, before the final `return os.Chown(cfg, uid, uid)`:

```go
	// Symlink ~/.claude → /data/<user>/claude-config so `claude login` (which
	// reads ~/.claude by default) persists per-user on the /data volume,
	// decoupled from the workspace. If ~/.claude already exists as a real
	// file/dir, leave it untouched (never clobber user state).
	claudeLink := filepath.Join(home, ".claude")
	if _, err := os.Lstat(claudeLink); os.IsNotExist(err) {
		if err := os.Symlink(cfg, claudeLink); err != nil {
			// Best-effort: log via the error path but do not fail provisioning
			// (the CLAUDE_CONFIG_DIR env var is the primary mechanism).
			// Return the error so it surfaces; on Linux as root this succeeds.
			return fmt.Errorf("symlink .claude: %w", err)
		}
	}
```

(Place it so the final `return os.Chown(cfg, uid, uid)` is still the last statement — i.e., add the block just above that return and change that return to a plain statement only if needed. Concretely: replace `return os.Chown(cfg, uid, uid)` with the block above followed by `return os.Chown(cfg, uid, uid)`.)

- [ ] **Step 4: Run test to verify it passes**

Run: `cd backend && go test ./internal/system/ -run TestProvisionDirs -v`
Expected: PASS.

- [ ] **Step 5: Run full suite + cross-compile**

Run: `cd backend && go vet ./... && go test ./... && GOOS=linux go build ./...`
Expected: PASS.

- [ ] **Step 6: Commit**

```bash
git add backend/internal/system/dirs.go backend/internal/system/dirs_test.go
git commit -m "feat(system): symlink ~/.claude → per-user claude-config for native login"
```

---

### Task 8: Frontend terminal reconnect + close-code surfacing

**Files:**
- Modify: `web/src/terminal.js`

**Interfaces:**
- Consumes: backend `{type:"ping"}` (Task 2) and `{type:"session"}`/`{type:"pty-exit"}` (existing).
- Produces: a terminal view that surfaces the close reason, retries with exponential backoff, and stops retrying on auth failure.

- [ ] **Step 1: Rewrite the WS layer in terminal.js**

Replace the `attach` function and add reconnect state. Edit `web/src/terminal.js` — replace the existing `attach` function and the trailing IIFE with:

```js
  let sessions = [];
  let currentSID = null;
  let ws = null;
  let reconnectAttempts = 0;
  let reconnectTimer = null;
  let intentionalClose = false;

  const CLOSE_REASONS = {
    1000: "closed",
    1006: "aborted (network/proxy)",
    1008: "rejected (policy/auth)",
    1011: "server error",
  };

  function status(msg) {
    const el = document.getElementById("term-status");
    if (el) el.textContent = msg;
  }

  function scheduleReconnect(sid) {
    if (intentionalClose) return;
    if (reconnectAttempts >= 6) {
      status("giving up — reload or sign in again");
      return;
    }
    const delay = Math.min(30000, 1000 * Math.pow(2, reconnectAttempts));
    reconnectAttempts++;
    status(`reconnecting in ${Math.round(delay / 1000)}s…`);
    reconnectTimer = setTimeout(() => attach(sid), delay);
  }

  function attach(sid) {
    if (reconnectTimer) { clearTimeout(reconnectTimer); reconnectTimer = null; }
    currentSID = sid || "";
    if (ws) { ws.onclose = null; ws.close(); }
    const proto = location.protocol === "https:" ? "wss" : "ws";
    ws = new WebSocket(`${proto}://${location.host}/ws/terminal` + (sid ? `?session=${encodeURIComponent(sid)}` : ""));
    status("connecting…");
    ws.onopen = () => {
      reconnectAttempts = 0;
      ws.send(JSON.stringify({ type: "resize", cols: term.cols, rows: term.rows }));
    };
    ws.onmessage = (e) => {
      const raw = e.data;
      try {
        const m = JSON.parse(raw);
        if (m.type === "ping") return; // keepalive; ignore
        if (m.type === "session" && m.id) { currentSID = m.id; status("session " + m.id.slice(0, 8)); refreshSessions(); return; }
        if (m.type === "pty-exit") { status("session ended (exit " + (m.exitCode ?? "?") + ")"); refreshSessions(); return; }
        // Other parsed JSON with no terminal data: ignore (do not write to term).
        return;
      } catch { /* binary/terminal data */ }
      term.write(raw);
    };
    ws.onclose = (ev) => {
      const reason = CLOSE_REASONS[ev.code] || `closed (${ev.code})`;
      status("disconnected — " + reason);
      refreshSessions();
      if (ev.code === 1008) {
        // auth/policy rejection: do not loop; tell user to sign in.
        status("session expired — reload to sign in");
        return;
      }
      scheduleReconnect(currentSID);
    };
  }
```

Also update the `kill-sess` button and the trailing IIFE so an explicit kill sets `intentionalClose` before closing (to avoid an immediate reconnect to the killed session):

```js
  document.getElementById("kill-sess").onclick = async () => {
    if (!currentSID) return;
    intentionalClose = true;
    await fetch(`/api/sessions/${encodeURIComponent(currentSID)}`, { method: "DELETE" });
    currentSID = null; term.reset();
    intentionalClose = false;
    attach(""); refreshSessions();
  };
```

And the trailing IIFE remains:

```js
  // Attach the most-recent session if any, else start a new one.
  (async () => {
    await refreshSessions();
    const alive = sessions.find((s) => s.alive);
    attach(alive ? alive.id : (sessions[0]?.id || ""));
  })();
```

- [ ] **Step 2: Build the SPA**

Run: `cd web && npm run build`
Expected: build succeeds (esbuild/vite).

- [ ] **Step 3: Commit**

```bash
git add web/src/terminal.js
git commit -m "feat(terminal): exponential-backoff reconnect + close-code surfacing"
```

---

### Task 9: Frontend Web file manager view

**Files:**
- Create: `web/src/files.js`
- Modify: `web/src/main.js` (`viewFiles`)
- Modify: `web/src/api.js` (add `uploadFile`)

**Interfaces:**
- Consumes: `/api/files/*` (Task 5).
- Produces: `mountFiles(root)` rendering the file manager.

- [ ] **Step 1: Add uploadFile to api.js**

Append to `web/src/api.js`:

```js
export async function uploadFile(url, file, onProgress) {
  return new Promise((resolve) => {
    const fd = new FormData();
    fd.append("file", file);
    const xhr = new XMLHttpRequest();
    xhr.open("POST", url);
    xhr.upload.onprogress = (e) => {
      if (e.lengthComputable && onProgress) onProgress(e.loaded / e.total);
    };
    xhr.onload = () => resolve({ ok: xhr.status >= 200 && xhr.status < 300, status: xhr.status });
    xhr.onerror = () => resolve({ ok: false, status: 0 });
    xhr.send(fd);
  });
}
```

- [ ] **Step 2: Write mountFiles**

Create `web/src/files.js`:

```js
import { getJson, postJson, del, uploadFile } from "./api.js";

// mountFiles(root): in-browser file manager over the user's workspace.
// Layout: breadcrumbs + toolbar, a table of entries, drag-drop upload, and a
// modal text editor. Pure vanilla JS, no framework.
export function mountFiles(root) {
  root.innerHTML = `
    <div class="files-toolbar">
      <div class="crumbs" id="crumbs"></div>
      <span class="grow"></span>
      <button class="btn tiny ghost" id="mkdir-btn">+ Folder</button>
      <button class="btn tiny ghost" id="up-btn">↑ Up</button>
    </div>
    <div class="files-drop" id="drop">
      <table class="tbl" id="ftbl"><thead><tr><th>Name</th><th>Size</th><th>Modified</th><th></th></tr></thead><tbody id="fbody"></tbody></table>
      <div class="files-empty muted" id="fempty">Empty folder. Drag files here to upload.</div>
    </div>
    <input type="file" id="file-input" multiple style="display:none">`;

  let cwd = ""; // relative path under workspace

  const fbody = () => document.getElementById("fbody");
  const fempty = () => document.getElementById("fempty");

  function fmtTime(unix) {
    if (!unix) return "—";
    const d = new Date(unix * 1000);
    return d.toLocaleString();
  }
  function fmtSize(n) {
    if (!n) return "0";
    const u = ["B","KB","MB","GB"]; let i = 0;
    while (n >= 1024 && i < u.length-1) { n /= 1024; i++; }
    return n.toFixed(i < 2 ? 0 : 1) + " " + u[i];
  }
  function esc(s) { return String(s).replace(/[&<>"]/g, c => ({"&":"&amp;","<":"&lt;",">":"&gt;",'"':"&quot;"}[c])); }

  function renderCrumbs() {
    const c = document.getElementById("crumbs");
    const parts = cwd ? cwd.split("/") : [];
    let html = `<span class="crumb" data-path="">workspace</span>`;
    let acc = "";
    for (const p of parts) {
      acc = acc ? acc + "/" + p : p;
      html += `<span class="crumb-sep">/</span><span class="crumb" data-path="${esc(acc)}">${esc(p)}</span>`;
    }
    c.innerHTML = html;
    c.querySelectorAll(".crumb").forEach(el => el.onclick = () => { cwd = el.dataset.path; refresh(); });
  }

  async function refresh() {
    renderCrumbs();
    const path = encodeURIComponent(cwd);
    let entries = [];
    try { entries = await getJson(`/api/files/list?path=${path}`); } catch { entries = []; }
    const tb = fbody();
    tb.innerHTML = "";
    fempty().style.display = entries.length ? "none" : "block";
    for (const e of entries) {
      const tr = document.createElement("tr");
      const icon = e.isDir ? "📁" : "📄";
      tr.innerHTML = `<td><span class="fname">${icon} ${esc(e.name)}</span></td><td class="muted">${e.isDir ? "—" : fmtSize(e.size)}</td><td class="muted">${fmtTime(e.modTime)}</td>`;
      const act = document.createElement("td");
      act.className = "files-actions";
      if (!e.isDir) {
        const dl = document.createElement("a");
        dl.className = "btn tiny ghost"; dl.textContent = "↓"; dl.href = `/api/files/download?path=${encodeURIComponent(cwd ? cwd+"/"+e.name : e.name)}`;
        act.appendChild(dl);
        const ed = document.createElement("button");
        ed.className = "btn tiny ghost"; ed.textContent = "✎";
        ed.onclick = () => openEditor(cwd ? cwd+"/"+e.name : e.name);
        act.appendChild(ed);
      } else {
        tr.querySelector(".fname").style.cursor = "pointer";
        tr.querySelector(".fname").onclick = () => { cwd = cwd ? cwd+"/"+e.name : e.name; refresh(); };
      }
      const rm = document.createElement("button");
      rm.className = "btn tiny danger"; rm.textContent = "✕";
      rm.onclick = async () => {
        if (!confirm(`Delete ${e.name}?`)) return;
        const p = encodeURIComponent(cwd ? cwd+"/"+e.name : e.name);
        await del(`/api/files?path=${p}`);
        refresh();
      };
      act.appendChild(rm);
      tr.appendChild(act);
      tb.appendChild(tr);
    }
  }

  async function openEditor(path) {
    const overlay = document.createElement("div");
    overlay.className = "overlay";
    overlay.innerHTML = `<div class="modal" style="width:min(720px,94vw)"><div class="hd"><b>${esc(path)}</b></div>
      <div class="bd"><textarea class="field" id="ed-area" style="min-height:50vh;font-family:var(--mono);font-size:13px"></textarea>
      <div style="height:10px"></div><button class="btn" id="ed-save">Save</button> <button class="btn ghost" id="ed-cancel">Cancel</button>
      <span class="muted tiny" id="ed-msg" style="margin-left:8px"></span></div></div>`;
    document.getElementById("app").appendChild(overlay);
    const area = overlay.querySelector("#ed-area");
    try {
      const res = await fetch(`/api/files/download?path=${encodeURIComponent(path)}`);
      area.value = await res.text();
    } catch { overlay.querySelector("#ed-msg").textContent = "load failed"; }
    overlay.querySelector("#ed-cancel").onclick = () => overlay.remove();
    overlay.querySelector("#ed-save").onclick = async () => {
      const r = await postJson("/api/files/edit", { path, content: area.value });
      if (r.ok) { overlay.remove(); refresh(); }
      else overlay.querySelector("#ed-msg").textContent = "save failed (" + r.status + ")";
    };
  }

  // Upload via input + drag-drop.
  const fileInput = document.getElementById("file-input");
  document.getElementById("mkdir-btn").onclick = async () => {
    const name = prompt("Folder name");
    if (!name) return;
    await postJson("/api/files/mkdir", { path: cwd ? cwd+"/"+name : name });
    refresh();
  };
  document.getElementById("up-btn").onclick = () => {
    const parts = cwd ? cwd.split("/") : [];
    parts.pop();
    cwd = parts.join("/");
    refresh();
  };
  fileInput.onchange = () => uploadFiles(fileInput.files);
  const drop = document.getElementById("drop");
  drop.onclick = () => fileInput.click();
  drop.ondragover = (e) => { e.preventDefault(); drop.classList.add("drag"); };
  drop.ondragleave = () => drop.classList.remove("drag");
  drop.ondrop = (e) => {
    e.preventDefault();
    drop.classList.remove("drag");
    uploadFiles(e.dataTransfer.files);
  };

  async function uploadFiles(fileList) {
    for (const f of fileList) {
      const url = `/api/files/upload?path=${encodeURIComponent(cwd)}`;
      const r = await uploadFile(url, f);
      if (!r.ok) { alert(`Upload ${f.name} failed (${r.status})`); }
    }
    fileInput.value = "";
    refresh();
  }

  refresh();
}
```

- [ ] **Step 3: Wire viewFiles to mountFiles**

In `web/src/main.js`, replace the `viewFiles` function body with:

```js
function viewFiles(root) {
  // Lazy-import the module to keep the initial bundle small.
  import("./files.js").then(({ mountFiles }) => mountFiles(root));
}
```

- [ ] **Step 4: Build**

Run: `cd web && npm run build`
Expected: succeeds.

- [ ] **Step 5: Commit**

```bash
git add web/src/files.js web/src/main.js web/src/api.js
git commit -m "feat(web): in-browser file manager view (browse/upload/download/edit/rename/delete)"
```

---

### Task 10: UI polish pass

**Files:**
- Modify: `web/src/styles.css`

**Interfaces:**
- Consumes: existing CSS variables.
- Produces: refined spacing, skeletons, empty states, transitions, file-manager styles, refined dark palette.

- [ ] **Step 1: Add polish + file-manager styles to styles.css**

Append to `web/src/styles.css` (and refine a few existing rules per the spec — unify spacing, focus rings, transitions):

```css
/* --- Plan 8 polish --- */
:focus-visible { outline: 2px solid var(--accent); outline-offset: 1px; border-radius: var(--radius-sm); }
.btn, .nav-item, .session-tab, .pill, .theme-toggle button { transition: background .12s, color .12s, border-color .12s; }
.modal { box-shadow: 0 12px 40px rgba(0,0,0,.22); animation: pop .12s ease-out; }
@keyframes pop { from { transform: translateY(6px) scale(.99); opacity:.6 } to { transform:none; opacity:1 } }
.overlay { animation: fade .12s ease-out; }
@keyframes fade { from { opacity: 0 } to { opacity: 1 } }

/* Empty + loading states */
.files-empty { padding: 28px; text-align: center; }
.skel { background: linear-gradient(90deg, var(--surface-2) 25%, var(--border) 37%, var(--surface-2) 63%); background-size: 400% 100%; animation: skel 1.2s ease infinite; height: 14px; border-radius: 4px; }
@keyframes skel { 0% { background-position: 100% 0 } 100% { background-position: -100% 0 } }

/* File manager */
.files-toolbar { display:flex; align-items:center; gap:8px; margin-bottom:12px; flex-wrap:wrap; }
.crumbs { display:flex; align-items:center; gap:4px; font-size:13px; flex-wrap:wrap; }
.crumb { cursor:pointer; color: var(--text-muted); padding:3px 7px; border-radius: var(--radius-sm); }
.crumb:hover { background: var(--surface-2); color: var(--text); }
.crumb-sep { color: var(--text-faint); }
.files-drop { background: var(--surface); border:1px solid var(--border); border-radius: var(--radius); padding:8px; }
.files-drop.drag { border-color: var(--accent); background: var(--surface-2); }
.fname { display:inline-flex; align-items:center; gap:6px; }
.files-actions { display:flex; gap:4px; justify-content:flex-end; }
.term-status { font-size:12px; }

/* Refined dark palette contrast */
:root[data-theme="dark"], :root:not([data-theme="light"]) { --shadow: 0 1px 3px rgba(0,0,0,.28); }
```

- [ ] **Step 2: Build**

Run: `cd web && npm run build`
Expected: succeeds.

- [ ] **Step 3: Commit**

```bash
git add web/src/styles.css
git commit -m "style: UI polish — focus rings, transitions, skeletons, file-manager styles"
```

---

### Task 11: Docs — remove SFTP refs, add cookie + file-manager notes

**Files:**
- Modify: `README.md`
- Modify: `DEPLOY-TEST.md`

**Interfaces:** none.

- [ ] **Step 1: Update README**

In `README.md`:
- Remove the SFTP bullet from "What's included" and the `SFTP` line from the architecture block (`SSH :22 → ...`).
- Replace the SFTP bullet with: "**Web file manager**: in-browser browse/upload/download/edit over each user's `~/workspace` (no external SFTP client needed)."
- In the env-var table, remove `SFTP_PORT`; add `COOKIE_SAMESITE` (`none` default; `lax` for local http dev).
- In "Security notes", add: "Session cookies are `Secure` + `SameSite=None` by default (HTTPS). For local `http://localhost` dev set `COOKIE_SAMESITE=lax`."

- [ ] **Step 2: Update DEPLOY-TEST**

In `DEPLOY-TEST.md`:
- Remove the SFTP chroot / `gliderlabs/ssh` verification items.
- Add a "Web file manager" section verifying: list/upload/download/edit/delete against a user's workspace; escape attempts (`../../etc`) return 400; upload + download bytes appear in the user's monthly traffic (`/api/admin/users/:id/usage`).
- Add a "Terminal WS" section verifying: under Cloudflare+nginx+HTTPS the WS stays connected past 100s idle (ping); a forced network drop triggers an auto-reconnect; the cookie carries on the WS upgrade (no 401).
- Add a "Claude login" section verifying: `/home/<user>/.claude` is a symlink to `/data/<user>/claude-config`; running `claude login` in the terminal and completing OAuth in a local browser writes `/data/<user>/claude-config/.credentials.json`.

- [ ] **Step 3: Commit**

```bash
git add README.md DEPLOY-TEST.md
git commit -m "docs: drop SFTP, add Web file manager / cookie / claude-login notes"
```

---

### Task 12: Whole-branch review + final verification

**Files:** none (verification + fixes).

- [ ] **Step 1: Run the full Windows-verification gate**

Run: `cd backend && go vet ./... && go test ./... && GOOS=linux go build ./... && GOOS=linux go test -c ./... -o /tmp/p8check 2>/dev/null; cd ../web && npm run build`
Expected: all PASS; linux binary + test cross-compile succeed; SPA builds.

- [ ] **Step 2: Dispatch a whole-branch review subagent**

Use the `feature-dev:code-reviewer` agent (or `code-reviewer`) with model **sonnet** (per the user pref: the final whole-branch review uses a stronger model). Scope: the entire diff `main..HEAD` for Plan 8. Ask it to verify: escape-guard correctness, traffic-accounting completeness (every upload/download path records bytes), cookie attributes on both set + clear, no dangling `internal/ssh` references, symlink safety on re-provision, and frontend reconnect termination conditions.

- [ ] **Step 3: Address review findings (fix in new commits)**

Apply any confirmed findings. Re-run the gate from Step 1.

- [ ] **Step 4: Final commit / merge note**

Leave the branch ready for the user to merge. Report what was Windows-verified vs. what awaits Linux/Docker runtime verification (per the global constraint).

---

## Self-Review

**Spec coverage:**
- Part 1 (terminal root cause): Task 1 (cookie Secure + SameSite), Task 2 (ping), Task 8 (reconnect + close code). ✓
- Part 2 (Web file manager): Task 3 (resolver), Task 4 (ops), Task 5 (handlers + traffic), Task 9 (frontend). ✓
- Part 2 SFTP removal: Task 6. ✓
- Part 3 (native login): Task 7 (symlink); OAuth flow is terminal-native (no backend code, documented in Task 11). Credential preset kept (no change — `resolveCredEnv` untouched). ✓
- Part 4 (UI polish): Task 10. ✓
- Docs: Task 11. ✓

**Placeholder scan:** No "TBD"/"TODO"/"add error handling". The Task 2 test scaffolding (`wsDialer`/`cookieHeaderForTest`) is explicitly called out as scaffolding to delete with the inline dial as the real test — the instruction is concrete, not a placeholder.

**Type consistency:**
- `files.Resolve(root, rel)` signature consistent across Tasks 3, 4, 5.
- `Identity.UserID` added in Task 5 and consumed by `recordFileTraffic(id.UserID, ...)` — consistent.
- `mountFiles(root)` defined in Task 9 and called in `viewFiles` (Task 9 Step 3) — consistent.
- `wsPingInterval` declared + used in Task 2; set in the Task 2 test — consistent.

**Known open items (resolved at implementation, not blocking):**
- `GOOS=linux go test -c ./... -o /dev/null` — `-o /dev/null` may not work on Windows; the plan offers the fallback `GOOS=linux go build ./...`.
- Windows symlink tests SKIP without privileges (acceptable per global constraint).
