# Panel-Selectable Template User Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Let an admin pick the template user (whose `.credentials.json` is copied to every user at PTY spawn) from the admin Users page, persisted in a DB KV table, overriding the existing env fallback.

**Architecture:** Add a generic `settings(key,value)` table (idempotent migration via the embedded `schema.sql`) + `GetSetting`/`SetSetting` store methods. Add `GET/PUT /api/admin/settings/template-user` admin endpoints (PUT validates the value is an existing `role='admin'` user). Add a `resolveTemplateUser()` server helper used by `buildUserEnvFactory` (DB value wins, then env `CLAUDE_TEMPLATE_USER`, then disabled). Render a `<select>` on the Users page wired to the endpoints.

**Tech Stack:** Go 1.26 (chi, modernc sqlite, `//go:embed`), vanilla JS SPA. Store tests run on the host (pure sqlite, no build tag); `internal/system` PTY tests are `//go:build linux` (in-container only).

## Global Constraints

- Go conventions: no comments unless WHY non-obvious; no emojis in code.
- modernc.org/sqlite is the driver — NULL-able columns scanned via `COALESCE` or `sql.Null*`, never NULL into a plain string/int.
- Auto-commit rule (CLAUDE.md): after each task, run the relevant suite (`cd backend && go test ./...` for host-runnable tests), then stage ONLY the files you changed by name (never `-A`/`.`); never commit `backend/server.exe` (untracked stray); new commit (never amend); conventional-commit message; `git push` to origin/main.
- The effective template user precedence is EXACTLY: DB setting (`settings.template_user`, non-empty) → env `cfg.TemplateUser` → disabled (empty). DB wins over env.
- PUT candidate validation: the value must be empty (clears) OR an existing user with `role='admin'`; anything else is `400`.
- Prior commits (env path, `CopyTemplateCredentials`) are RETAINED — this plan is purely additive except the one-line `buildUserEnvFactory` change to use `resolveTemplateUser()`.
- Frontend verify: `cd web && npm run build` clean.

---

## File Structure

- `backend/internal/store/schema.sql` — add `settings` table. (responsibility: schema)
- `backend/internal/store/settings.go` — **create**; `GetSetting`/`SetSetting`. (responsibility: KV settings access)
- `backend/internal/store/settings_test.go` — **create**; round-trip/upsert/absent tests. (responsibility: lock KV contract)
- `backend/internal/server/admin_settings.go` — **create**; `GET`/`PUT` handlers + `resolveTemplateUser`. (responsibility: admin template-user API + resolution)
- `backend/internal/server/admin_settings_test.go` — **create**; httptest tests for both endpoints. (responsibility: lock API contract)
- `backend/internal/server/server.go` — register the two routes in the admin group; change `buildUserEnvFactory` to use `resolveTemplateUser`. (responsibility: routing + PTY env factory)
- `web/src/main.js` — render the template-user `<select>` in `viewAdminUsers`, wired to the endpoints. (responsibility: admin Users view)

---

## Task 1: Settings KV table + store methods (TDD)

**Files:**
- Modify: `backend/internal/store/schema.sql`
- Create: `backend/internal/store/settings.go`
- Create: `backend/internal/store/settings_test.go`

**Interfaces:**
- Produces: `(d *DB) GetSetting(key string) (string, error)` — returns `"", nil` when the key is absent (NOT an error); `(d *DB) SetSetting(key, value string) error` — upsert. Consumed by Task 3's `resolveTemplateUser` and the handlers.

- [ ] **Step 1: Add the settings table to the schema**

Append to `backend/internal/store/schema.sql`:

```sql

CREATE TABLE IF NOT EXISTS settings (
  key TEXT PRIMARY KEY,
  value TEXT NOT NULL
);
```

- [ ] **Step 2: Write the failing tests**

Create `backend/internal/store/settings_test.go` with:

```go
package store

import (
	"path/filepath"
	"testing"
)

func openSettingsDB(t *testing.T) *DB {
	t.Helper()
	db, err := Open(filepath.Join(t.TempDir(), "settings.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	return db
}

func TestGetSetting_Absent(t *testing.T) {
	db := openSettingsDB(t)
	v, err := db.GetSetting("nope")
	if err != nil {
		t.Fatalf("absent key must not error, got: %v", err)
	}
	if v != "" {
		t.Fatalf("absent key must return empty, got %q", v)
	}
}

func TestSetSetting_RoundTrip(t *testing.T) {
	db := openSettingsDB(t)
	if err := db.SetSetting("template_user", "alice"); err != nil {
		t.Fatalf("set: %v", err)
	}
	v, err := db.GetSetting("template_user")
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if v != "alice" {
		t.Fatalf("got %q, want alice", v)
	}
}

func TestSetSetting_Upsert(t *testing.T) {
	db := openSettingsDB(t)
	if err := db.SetSetting("template_user", "alice"); err != nil {
		t.Fatal(err)
	}
	if err := db.SetSetting("template_user", "bob"); err != nil {
		t.Fatal(err)
	}
	v, _ := db.GetSetting("template_user")
	if v != "bob" {
		t.Fatalf("upsert: got %q, want bob", v)
	}
}
```

- [ ] **Step 3: Verify the tests fail**

Run: `cd backend && go test ./internal/store/ -run TestGetSetting_Absent -v`
Expected: FAIL — `db.GetSetting undefined` (compile error).

- [ ] **Step 4: Implement GetSetting / SetSetting**

Create `backend/internal/store/settings.go` with:

```go
package store

import (
	"database/sql"
	"fmt"
)

// GetSetting returns the value for key, or "" with a nil error when the key is
// absent. Absence is not an error.
func (d *DB) GetSetting(key string) (string, error) {
	var v string
	err := d.sql.QueryRow(`SELECT value FROM settings WHERE key = ?`, key).Scan(&v)
	if err == sql.ErrNoRows {
		return "", nil
	}
	if err != nil {
		return "", fmt.Errorf("get setting %q: %w", key, err)
	}
	return v, nil
}

// SetSetting upserts key=value.
func (d *DB) SetSetting(key, value string) error {
	_, err := d.sql.Exec(
		`INSERT INTO settings (key, value) VALUES (?, ?)
		 ON CONFLICT(key) DO UPDATE SET value = excluded.value`,
		key, value)
	if err != nil {
		return fmt.Errorf("set setting %q: %w", key, err)
	}
	return nil
}
```

- [ ] **Step 5: Run the tests**

Run: `cd backend && go test ./internal/store/ -run 'TestGetSetting|TestSetSetting' -v`
Expected: PASS (3 tests).

- [ ] **Step 6: Run the full host suite + vet, then commit + push**

Run: `cd backend && go test ./... && go vet ./...`
Expected: all PASS, vet clean. (Host-runnable; no `//go:build linux` in this package.)

```bash
git -C C:/PythonProject/claude-docker add backend/internal/store/schema.sql backend/internal/store/settings.go backend/internal/store/settings_test.go
git -C C:/PythonProject/claude-docker commit -m "feat(store): add generic settings KV (GetSetting/SetSetting)"
git -C C:/PythonProject/claude-docker push
```

---

## Task 2: Admin template-user API + resolver (TDD)

**Files:**
- Create: `backend/internal/server/admin_settings.go`
- Create: `backend/internal/server/admin_settings_test.go`
- Modify: `backend/internal/server/server.go` (register routes only — the `buildUserEnvFactory` change is Task 3)

**Interfaces:**
- Consumes: `store.GetSetting`/`SetSetting` (Task 1), `store.GetUserByUsername` (existing), `s.cfg.TemplateUser` (existing), `s.requireAdmin` middleware + admin route group (existing), `writeJSON` (existing).
- Produces: handlers `handleAdminGetTemplateUser` + `handleAdminSetTemplateUser` (registered at `GET`/`PUT /api/admin/settings/template-user`), and `func (s *Server) resolveTemplateUser() string` (DB setting non-empty → else `cfg.TemplateUser`). Consumed by Task 3's `buildUserEnvFactory`.

- [ ] **Step 1: Write the failing tests**

Create `backend/internal/server/admin_settings_test.go` with:

```go
package server

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func getTemplateUser(t *testing.T, s *Server, cookie string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest("GET", "/api/admin/settings/template-user", nil)
	req.AddCookie(&http.Cookie{Name: "session", Value: cookie})
	w := httptest.NewRecorder()
	s.Routes().ServeHTTP(w, req)
	return w
}

func putTemplateUser(t *testing.T, s *Server, cookie, body string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest("PUT", "/api/admin/settings/template-user", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.AddCookie(&http.Cookie{Name: "session", Value: cookie})
	w := httptest.NewRecorder()
	s.Routes().ServeHTTP(w, req)
	return w
}

func TestTemplateUser_NonAdmin_Forbidden(t *testing.T) {
	s, _, _ := newTestServerWithAdmin(t)
	w := getTemplateUser(t, s, userCookie(t, s))
	if w.Code != 403 {
		t.Fatalf("expected 403, got %d", w.Code)
	}
}

func TestTemplateUser_Get_Unset(t *testing.T) {
	s, _, _ := newTestServerWithAdmin(t)
	w := getTemplateUser(t, s, adminCookie(t, s))
	if w.Code != 200 {
		t.Fatalf("expected 200, got %d; body=%s", w.Code, w.Body.String())
	}
	var got map[string]any
	if err := json.NewDecoder(w.Body).Decode(&got); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if got["template_user"] != "" {
		t.Fatalf("expected empty template_user, got %v", got["template_user"])
	}
}

func TestTemplateUser_Put_ValidAdmin_RoundTrips(t *testing.T) {
	s, _, _ := newTestServerWithAdmin(t)
	cookie := adminCookie(t, s)

	// newTestServerWithAdmin seeds a fixed admin user "bob" (see admin_users_test.go:86-91).
	w := putTemplateUser(t, s, cookie, `{"template_user":"bob"}`)
	if w.Code != 200 {
		t.Fatalf("put: expected 200, got %d; body=%s", w.Code, w.Body.String())
	}

	w = getTemplateUser(t, s, cookie)
	if w.Code != 200 {
		t.Fatalf("get: expected 200, got %d", w.Code)
	}
	var got map[string]any
	_ = json.NewDecoder(w.Body).Decode(&got)
	if got["template_user"] != "bob" {
		t.Fatalf("expected %q, got %v", "bob", got["template_user"])
	}
}

func TestTemplateUser_Put_NonAdmin_Rejected(t *testing.T) {
	s, _, _ := newTestServerWithAdmin(t)
	cookie := adminCookie(t, s)

	// "alice" is the seeded regular (role=user) user.
	w := putTemplateUser(t, s, cookie, `{"template_user":"alice"}`)
	if w.Code != 400 {
		t.Fatalf("expected 400 for non-admin, got %d; body=%s", w.Code, w.Body.String())
	}
}

func TestTemplateUser_Put_Unknown_Rejected(t *testing.T) {
	s, _, _ := newTestServerWithAdmin(t)
	cookie := adminCookie(t, s)
	w := putTemplateUser(t, s, cookie, `{"template_user":"ghost"}`)
	if w.Code != 400 {
		t.Fatalf("expected 400 for unknown user, got %d; body=%s", w.Code, w.Body.String())
	}
}

func TestTemplateUser_Put_Empty_Clears(t *testing.T) {
	s, _, _ := newTestServerWithAdmin(t)
	cookie := adminCookie(t, s)

	if w := putTemplateUser(t, s, cookie, `{"template_user":"bob"}`); w.Code != 200 {
		t.Fatalf("seed put: %d %s", w.Code, w.Body.String())
	}
	w := putTemplateUser(t, s, cookie, `{"template_user":""}`)
	if w.Code != 200 {
		t.Fatalf("clear: expected 200, got %d; body=%s", w.Code, w.Body.String())
	}
	got := getTemplateUser(t, s, cookie)
	var m map[string]any
	_ = json.NewDecoder(got.Body).Decode(&m)
	if m["template_user"] != "" {
		t.Fatalf("expected cleared, got %v", m["template_user"])
	}
}
```

`newTestServerWithAdmin` (in `admin_users_test.go:69`) seeds exactly two users: "alice" (`role=user`) and "bob" (`role=admin`); `adminCookie` logs in as bob, `userCookie` as alice. The tests use those fixed names directly.

- [ ] **Step 2: Verify the tests fail**

Run: `cd backend && go test ./internal/server/ -run TestTemplateUser -v`
Expected: FAIL — routes not registered / handlers undefined (404 or compile error).

- [ ] **Step 3: Implement the handlers + resolver**

Create `backend/internal/server/admin_settings.go` with:

```go
package server

import (
	"encoding/json"
	"errors"
	"net/http"

	"github.com/ldm0206/claude-docker/backend/internal/store"
)

// templateUserKey is the settings KV key for the panel-selected template user.
const templateUserKey = "template_user"

// resolveTemplateUser returns the effective template username: the DB setting
// if set (non-empty), else cfg.TemplateUser (env CLAUDE_TEMPLATE_USER). Empty
// means the feature is disabled.
func (s *Server) resolveTemplateUser() string {
	if v, err := s.db.GetSetting(templateUserKey); err == nil && v != "" {
		return v
	}
	return s.cfg.TemplateUser
}

// handleAdminGetTemplateUser returns the DB-stored template user (empty when
// unset — env fallback is NOT reported here; the panel only manages the DB
// value).
func (s *Server) handleAdminGetTemplateUser(w http.ResponseWriter, r *http.Request) {
	v, err := s.db.GetSetting(templateUserKey)
	if err != nil {
		writeJSON(w, 500, map[string]any{"error": "get setting"})
		return
	}
	writeJSON(w, 200, map[string]any{"template_user": v})
}

type setTemplateUserReq struct {
	TemplateUser string `json:"template_user"`
}

// handleAdminSetTemplateUser upserts the template user. The value must be
// empty (clears the setting) or an existing user with role 'admin'; anything
// else is a 400.
func (s *Server) handleAdminSetTemplateUser(w http.ResponseWriter, r *http.Request) {
	var b setTemplateUserReq
	if err := json.NewDecoder(r.Body).Decode(&b); err != nil {
		writeJSON(w, 400, map[string]any{"error": "bad body"})
		return
	}
	if b.TemplateUser != "" {
		u, err := s.db.GetUserByUsername(b.TemplateUser)
		if errors.Is(err, store.ErrNotFound) || u.Role != "admin" {
			writeJSON(w, 400, map[string]any{"error": "template user must be an existing admin user"})
			return
		}
		if err != nil {
			writeJSON(w, 500, map[string]any{"error": "lookup user"})
			return
		}
	}
	if err := s.db.SetSetting(templateUserKey, b.TemplateUser); err != nil {
		writeJSON(w, 500, map[string]any{"error": "save setting"})
		return
	}
	writeJSON(w, 200, map[string]any{"template_user": b.TemplateUser})
}
```

- [ ] **Step 4: Register the routes**

In `backend/internal/server/server.go`, inside the admin `r.Group(func(r chi.Router) { r.Use(s.requireAdmin); ... })` block (the same block that registers `/api/admin/users`, `/api/admin/credentials`, etc., around line 281-315), add two lines (place them near the other admin GET/PUT pairs):

```go
			r.Get("/api/admin/settings/template-user", s.handleAdminGetTemplateUser)
			r.Put("/api/admin/settings/template-user", s.handleAdminSetTemplateUser)
```

- [ ] **Step 5: Run the tests**

Run: `cd backend && go test ./internal/server/ -run TestTemplateUser -v`
Expected: PASS (6 tests).

- [ ] **Step 6: Run the full host suite + vet, then commit + push**

Run: `cd backend && go test ./... && go vet ./...`
Expected: all PASS, vet clean.

```bash
git -C C:/PythonProject/claude-docker add backend/internal/server/admin_settings.go backend/internal/server/admin_settings_test.go backend/internal/server/server.go
git -C C:/PythonProject/claude-docker commit -m "feat(server): admin API for panel-selectable template user"
git -C C:/PythonProject/claude-docker push
```

---

## Task 3: Wire resolveTemplateUser into spawn + frontend dropdown

**Files:**
- Modify: `backend/internal/server/server.go` (the `buildUserEnvFactory` call site)
- Modify: `web/src/main.js` (the `viewAdminUsers` function, ~L206)

**Interfaces:**
- Consumes: `s.resolveTemplateUser()` (Task 2), `GET`/`PUT /api/admin/settings/template-user` (Task 2), `/api/admin/users` (existing).

- [ ] **Step 1: Use resolveTemplateUser in buildUserEnvFactory**

In `backend/internal/server/server.go`, in `buildUserEnvFactory` (the returned closure, around line 96), replace:

```go
		if err := system.CopyTemplateCredentials(s.cfg.TemplateUser, u.Username, u.UID); err != nil {
			log.Printf("[server] warning: copy template credentials for %s: %v", u.Username, err)
		}
```

with:

```go
		if err := system.CopyTemplateCredentials(s.resolveTemplateUser(), u.Username, u.UID); err != nil {
			log.Printf("[server] warning: copy template credentials for %s: %v", u.Username, err)
		}
```

- [ ] **Step 2: Verify backend still builds + vets + tests**

Run: `cd backend && go build ./... && go vet ./... && go test ./...`
Expected: clean, all PASS.

- [ ] **Step 3: Add the template-user dropdown to viewAdminUsers**

In `web/src/main.js`, the `viewAdminUsers(root)` function currently renders a row with a "+ New user" button then the users table. Read the function (around line 206-213) to see its exact current shape. Add a settings card BEFORE the existing `<div class="row">...</div>` that holds the "+ New user" button. The card contains a label + a `<select id="tpl-user">` that loads its options from the already-fetched admin users and wires GET on render + PUT on change.

Replace the start of `viewAdminUsers` so it begins with the settings card. Concretely, change:

```js
async function viewAdminUsers(root) {
  root.innerHTML = `<div class="row"><span class="grow"></span><button class="btn" id="add-user">+ New user</button></div>
    <div class="card" style="margin-top:12px;overflow:auto"><table class="tbl"><thead><tr>
      <th>User</th><th>Role</th><th>Status</th><th>Disk</th><th>Traffic</th><th>Sessions</th><th>Last login</th><th></th>
    </tr></thead><tbody id="utbody"></tbody></table></div>`;
  document.getElementById("add-user").onclick = () => userModal(null, () => viewAdminUsers(root));
  await refreshUsers();
}
```

with:

```js
async function viewAdminUsers(root) {
  root.innerHTML = `<div class="card pads" style="margin-bottom:12px">
      <div class="row">
        <div><div class="lbl">Template user</div>
        <select class="field" id="tpl-user" style="width:auto"></select></div>
        <span class="grow"></span>
        <span class="muted tiny" id="tpl-note"></span>
      </div>
      <p class="muted tiny" style="margin:8px 0 0">The template user's <code>.credentials.json</code> is copied into every user's terminal at session start. Must be an admin. Overrides CLAUDE_TEMPLATE_USER.</p>
    </div>
    <div class="row"><span class="grow"></span><button class="btn" id="add-user">+ New user</button></div>
    <div class="card" style="margin-top:12px;overflow:auto"><table class="tbl"><thead><tr>
      <th>User</th><th>Role</th><th>Status</th><th>Disk</th><th>Traffic</th><th>Sessions</th><th>Last login</th><th></th>
    </tr></thead><tbody id="utbody"></tbody></table></div>`;
  document.getElementById("add-user").onclick = () => userModal(null, () => viewAdminUsers(root));
  await refreshUsers();
  await loadTemplateUser();
}

async function loadTemplateUser() {
  const sel = document.getElementById("tpl-user");
  if (!sel) return;
  let admins = [];
  try { admins = (await getJson("/api/admin/users")).filter(u => u.role === "admin"); } catch {}
  // "(env / disabled)" option — empty value.
  sel.innerHTML = `<option value="">(env / disabled)</option>` +
    admins.map(a => `<option value="${esc(a.username)}">${esc(a.username)}</option>`).join("");
  try {
    const cur = await getJson("/api/admin/settings/template-user");
    sel.value = cur.template_user || "";
    document.getElementById("tpl-note").textContent = cur.template_user ? "" : "(using CLAUDE_TEMPLATE_USER if set)";
  } catch {
    document.getElementById("tpl-note").textContent = "";
  }
  sel.onchange = async () => {
    const r = await fetch("/api/admin/settings/template-user", {
      method: "PUT", headers: { "Content-Type": "application/json" },
      body: JSON.stringify({ template_user: sel.value }),
    });
    if (!r.ok) alert("Save failed (" + r.status + ")");
    await loadTemplateUser();
  };
}
```

(If the existing `viewAdminUsers` markup differs slightly from the snippet above, preserve its real markup — only ADD the settings card before the existing `<div class="row">` and append `await loadTemplateUser();` after `await refreshUsers();`. Use `getJson` from `web/src/api.js` — confirm it exists; if the file exports a differently-named helper, use that.)

- [ ] **Step 4: Build the SPA**

Run: `cd web && npm run build`
Expected: vite builds `dist/` clean, exit 0.

- [ ] **Step 5: Commit + push**

```bash
git -C C:/PythonProject/claude-docker add backend/internal/server/server.go web/src/main.js
git -C C:/PythonProject/claude-docker commit -m "feat(web): template-user dropdown on Users page; wire resolveTemplateUser"
git -C C:/PythonProject/claude-docker push
```

---

## Self-Review

**1. Spec coverage:**
- KV table + `GetSetting`/`SetSetting` → Task 1.
- Admin API `GET/PUT /api/admin/settings/template-user` with admin-only candidate validation + clear-on-empty → Task 2 (6 tests cover forbidden/get-unset/valid-roundtrip/non-admin-reject/unknown-reject/clear).
- `resolveTemplateUser` (DB → env → empty) → Task 2 impl; consumed in Task 3 Step 1.
- `buildUserEnvFactory` uses resolver → Task 3 Step 1.
- Frontend dropdown on Users page, populated by admin users, wired GET/PUT → Task 3 Step 3.
- env retained as fallback → `resolveTemplateUser` second branch; no env deletion anywhere. Covered.
- Prior commits retained → plan is additive except the one-line spawn call change. Covered.

**2. Placeholder scan:** The one `newUser`/UID-helper ambiguity in Task 2 Step 1 is called out explicitly with a resolution instruction (check `admin_users_test.go`, mirror `admin_audit_test.go:23-25`), not left as TBD. The `viewAdminUsers` markup variance in Task 3 Step 3 is handled the same way. No other placeholders. All code blocks are complete.

**3. Type/name consistency:** `GetSetting(key) (string, error)` / `SetSetting(key, value) error` — same in Task 1 impl, Task 1 tests, Task 2 resolver, Task 2 handlers. `templateUserKey = "template_user"` constant used in resolver + both handlers + (value only) frontend. `handleAdminGetTemplateUser`/`handleAdminSetTemplateUser` match the Task 2 Step 4 registration and the Task 2 tests. `resolveTemplateUser()` matches between Task 2 (definition) and Task 3 (call). `setTemplateUserReq.TemplateUser` (Go field) ↔ JSON `template_user` (wire) ↔ frontend `template_user` (fetch body) — consistent. No drift.
