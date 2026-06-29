# Login IP/Session Audit + Preinstalled Toolchain — Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Give admins an audit trail of login IPs and per-session client IPs (new Audit page + login_events table), and preinstall node/npm + python alongside the existing git + claude binary in the user shell.

**Architecture:** The store has no migration framework (schema is `CREATE TABLE IF NOT EXISTS`), so adding columns to `users`/`sessions` uses idempotent `ALTER TABLE ADD COLUMN` run in `store.Open()`. A new `login_events` table records every login attempt (success+failure). A `clientIP(r)` helper reads `CF-Connecting-IP` first (Cloudflare deployment). The session-creation path threads the IP through a new `pty.Options.ClientIP` field into `store.Session.ClientIP`. A new admin `/api/admin/login-events` endpoint backs a new "Audit" sidebar view. The Dockerfile gains NodeSource node 22 + apt python3/pip/venv and drops the leftover `EXPOSE 22`.

**Tech Stack:** Go 1.25 (CGO-free), go-chi v5, modernc.org/sqlite (pure-Go), vanilla JS SPA.

## Global Constraints

- **Dev host is Windows with no Docker/WSL.** All Go tests run on Windows: pure-Go + SQLite + `httptest`. Linux-only runtime (gosu PTY, useradd) is NOT tested — only `go build`/`go vet`/`go test` + `GOOS=linux go build`.
- **No migration framework.** New tables go in `schema.sql` (`CREATE TABLE IF NOT EXISTS`); new columns on existing tables use idempotent `ALTER TABLE ADD COLUMN` statements run in `store.Open()` after the base schema (ignore the "duplicate column" error on re-run).
- **IP trust model:** `CF-Connecting-IP` is trusted because the deployment transits Cloudflare+nginx and the container's 8080 port is private behind nginx. DEPLOY-TEST notes that 8080 must stay private.
- **Audit data is admin-only.** Never expose another user's IP to a regular user.
- **`user_agent` truncated to 256 chars** on write to bound table growth.
- **Use haiku for per-task review subagents; sonnet for the final whole-branch review** (user pref).
- **Commit your work.** Every task ends with a `git add` + `git commit`. (Earlier plans saw implementers forget this — the dispatch will remind, but the plan step shows it too.)

---

## File Structure

**Backend (Go):**
- `backend/internal/store/schema.sql` (MODIFY) — add `login_events` table + index.
- `backend/internal/store/store.go` (MODIFY) — idempotent ALTERs in `Open()`.
- `backend/internal/store/users.go` (MODIFY) — `User.LastLoginIP`; SELECT/scan it; `TouchLogin(id, ts, ip)`.
- `backend/internal/store/sessions.go` (MODIFY) — `Session.ClientIP`; write/read it in Create/Get/List.
- `backend/internal/store/login_events.go` (NEW) — `LoginEvent` struct + `CreateLoginEvent` + `ListLoginEvents`.
- `backend/internal/store/login_events_test.go` (NEW) — tests.
- `backend/internal/store/store_test.go` (MODIFY or extend) — ALTER idempotency test.
- `backend/internal/pty/manager.go` (MODIFY) — add `ClientIP string` to `Options`.
- `backend/internal/sessions/manager.go` (MODIFY) — `Create` writes `opts.ClientIP` into `store.Session.ClientIP`.
- `backend/internal/server/server.go` (MODIFY) — `clientIP(r)` helper; route registration.
- `backend/internal/server/auth_handler.go` (MODIFY) — `handleLogin` writes login_events + TouchLogin(ip).
- `backend/internal/server/terminal.go` (MODIFY) — `ensureSession` sets `opts.ClientIP`.
- `backend/internal/server/sessions_api.go` (MODIFY) — `handleCreateSession` sets `opts.ClientIP`.
- `backend/internal/server/admin_audit.go` (NEW) — `GET /api/admin/login-events`.
- `backend/internal/server/admin_audit_test.go` (NEW) — handler tests.
- `backend/internal/server/admin_users.go` (MODIFY) — list response adds `last_login_ip`/`last_login_at`.
- `backend/internal/server/admin_sessions.go` (MODIFY) — session list response adds `client_ip`.

**Frontend (web):**
- `web/src/main.js` (MODIFY) — add "Audit" nav + view; Users page "Last login" column; fix stale "SFTP" text in traffic view.

**Image / docs:**
- `Dockerfile` (MODIFY) — +python3/pip/venv, +NodeSource node 22, `EXPOSE 8080` (drop 22).
- `DEPLOY-TEST.md` (MODIFY) — add toolchain-version check + 8080-private note + audit verification.

---

### Task 1: Schema — login_events table + idempotent ALTERs

**Files:**
- Modify: `backend/internal/store/schema.sql`
- Modify: `backend/internal/store/store.go`
- Test: `backend/internal/store/store_test.go`

**Interfaces:**
- Consumes: nothing.
- Produces: `users.last_login_ip` and `sessions.client_ip` columns (writable); `login_events` table. Later tasks read/write these.

- [ ] **Step 1: Add login_events to schema.sql**

Append to `backend/internal/store/schema.sql`:

```sql

CREATE TABLE IF NOT EXISTS login_events (
  id INTEGER PRIMARY KEY AUTOINCREMENT,
  user_id INTEGER NOT NULL,   -- 0 when the username does not exist
  username TEXT NOT NULL,     -- the attempted username (audited even if no row)
  ip TEXT,
  user_agent TEXT,
  success INTEGER NOT NULL,   -- 1 success / 0 failure
  at INTEGER NOT NULL
);
CREATE INDEX IF NOT EXISTS idx_login_events_at ON login_events(at DESC);
```

- [ ] **Step 2: Write the failing test for ALTER idempotency**

Append to `backend/internal/store/store_test.go` (or create the test if the file lacks a table-opening helper — it has `Open` on temp paths):

```go
// TestOpen_AlterColumnsIdempotent verifies that re-opening an existing DB
// (which already has last_login_ip / client_ip from a prior Open) does NOT
// error, and that a fresh DB gets the columns.
func TestOpen_AlterColumnsIdempotent(t *testing.T) {
	path := filepath.Join(t.TempDir(), "alter.db")

	// First open: columns added.
	db1, err := Open(path)
	if err != nil {
		t.Fatalf("first open: %v", err)
	}
	// Sanity: last_login_ip is writable (proves the column exists).
	if _, err := db1.sql.Exec(`UPDATE users SET last_login_ip = ? WHERE id = -1`, "1.2.3.4"); err != nil {
		t.Fatalf("last_login_ip column missing after first open: %v", err)
	}
	if _, err := db1.sql.Exec(`UPDATE sessions SET client_ip = ? WHERE id = 'none'`, "1.2.3.4"); err != nil {
		t.Fatalf("client_ip column missing after first open: %v", err)
	}
	db1.Close()

	// Second open on the SAME file: ALTERs must be idempotent (no "duplicate
	// column" error surfaces).
	db2, err := Open(path)
	if err != nil {
		t.Fatalf("second open (idempotency): %v", err)
	}
	defer db2.Close()
	if _, err := db2.sql.Exec(`UPDATE users SET last_login_ip = ? WHERE id = -1`, "5.6.7.8"); err != nil {
		t.Fatalf("last_login_ip unusable after re-open: %v", err)
	}
}
```

(If `store_test.go` does not import `"path/filepath"`, add it.)

- [ ] **Step 3: Run test to verify it fails**

Run: `cd backend && go test ./internal/store/ -run TestOpen_AlterColumnsIdempotent -v`
Expected: FAIL — `last_login_ip column missing` (the ALTERs do not exist yet; only the base schema runs).

- [ ] **Step 4: Add the idempotent ALTERs to store.Open()**

In `backend/internal/store/store.go`, after the `sq.Exec(schemaSQL)` block in `Open`, add the migrations (each ALTER ignores the "duplicate column" error):

```go
	// Idempotent column adds (the store has no migration framework; CREATE TABLE
	// IF NOT EXISTS does not add columns to an existing table, so we ALTER and
	// ignore the "duplicate column" error on re-runs).
	for _, alter := range []string{
		`ALTER TABLE users ADD COLUMN last_login_ip TEXT`,
		`ALTER TABLE sessions ADD COLUMN client_ip TEXT`,
	} {
		if _, err := sq.Exec(alter); err != nil {
			// modernc.org/sqlite returns "duplicate column name" on re-run.
			// Any other error is a real schema problem — surface it.
			if !strings.Contains(err.Error(), "duplicate column") {
				sq.Close()
				return nil, fmt.Errorf("migrate %q: %w", alter, err)
			}
		}
	}
```

Add `"strings"` to the import block of `store.go` (it is not currently imported there).

- [ ] **Step 5: Run test to verify it passes**

Run: `cd backend && go test ./internal/store/ -run TestOpen_AlterColumnsIdempotent -v`
Expected: PASS.

- [ ] **Step 6: Run full store package + vet**

Run: `cd backend && go vet ./... && go test ./internal/store/ -v`
Expected: all PASS (existing store tests still green — the new columns/tables are additive).

- [ ] **Step 7: Commit**

```bash
git add backend/internal/store/schema.sql backend/internal/store/store.go backend/internal/store/store_test.go
git commit -m "feat(store): login_events table + idempotent ALTERs (users.last_login_ip, sessions.client_ip)"
```

---

### Task 2: Store — User.LastLoginIP + Session.ClientIP + TouchLogin(ip)

**Files:**
- Modify: `backend/internal/store/users.go`
- Modify: `backend/internal/store/sessions.go`
- Test: `backend/internal/store/users_test.go`, `backend/internal/store/sessions_test.go`

**Interfaces:**
- Consumes: Task 1 columns.
- Produces:
  - `User.LastLoginIP string` (populated by GetUser/ListUsers).
  - `Session.ClientIP string` (populated by GetSession/ListSessionsForUser; written by CreateSession).
  - `TouchLogin(id int, ts int64, ip string) error` (signature changed — adds ip).

- [ ] **Step 1: Write the failing tests**

Append to `backend/internal/store/users_test.go`:

```go
// TestTouchLogin_RecordsIP verifies TouchLogin now persists last_login_ip.
func TestTouchLogin_RecordsIP(t *testing.T) {
	db := newTestDB(t) // the existing helper that opens a temp store + seeds a user
	u, err := db.GetUserByUsername("alice")
	if err != nil {
		t.Fatalf("get alice: %v", err)
	}
	if err := db.TouchLogin(u.ID, 1700000000, "203.0.113.9"); err != nil {
		t.Fatalf("touch: %v", err)
	}
	got, err := db.GetUserByID(u.ID)
	if err != nil {
		t.Fatalf("reload: %v", err)
	}
	if got.LastLoginIP != "203.0.113.9" {
		t.Errorf("LastLoginIP = %q, want 203.0.113.9", got.LastLoginIP)
	}
}
```

(If `users_test.go` uses a different db-open helper name than `newTestDB`, match the existing one. Inspect `users_test.go` first and use its actual helper.)

Append to `backend/internal/store/sessions_test.go`:

```go
// TestCreateSession_StoresClientIP verifies CreateSession persists client_ip and
// ListSessionsForUser returns it.
func TestCreateSession_StoresClientIP(t *testing.T) {
	db := newTestDB(t)
	u, _ := db.GetUserByUsername("alice")
	s := Session{ID: "sess-ip-1", UserID: u.ID, Name: "alice", StartedAt: 1, LastSeenAt: 1, Alive: true, ClientIP: "198.51.100.7"}
	if err := db.CreateSession(s); err != nil {
		t.Fatalf("create: %v", err)
	}
	list, err := db.ListSessionsForUser(u.ID)
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	var found *Session
	for i := range list {
		if list[i].ID == "sess-ip-1" {
			found = &list[i]
		}
	}
	if found == nil {
		t.Fatal("session not listed")
	}
	if found.ClientIP != "198.51.100.7" {
		t.Errorf("ClientIP = %q, want 198.51.100.7", found.ClientIP)
	}
}
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `cd backend && go test ./internal/store/ -run 'TestTouchLogin_RecordsIP|TestCreateSession_StoresClientIP' -v`
Expected: FAIL — `User.LastLoginIP` undefined / `Session.ClientIP` undefined (or compile error).

- [ ] **Step 3: Add fields + update queries in users.go**

In `backend/internal/store/users.go`:
- Add to the `User` struct (after `LastLoginAt`):
  ```go
  	LastLoginAt        sql.NullInt64
  	LastLoginIP        string
  ```
- In ALL three SELECT statements (`GetUserByUsername`, `GetUserByID`, `ListUsers`) add `, last_login_ip` after `last_login_at`. Example for `GetUserByUsername`:
  ```go
  row := d.sql.QueryRow(`SELECT id, uid, username, password_hash, role, must_change_password, suspended, role_template_id, credential_preset_id, disk_quota_bytes, max_sessions, created_at, last_login_at, last_login_ip FROM users WHERE username = ?`, name)
  ```
  Do the same column addition for `GetUserByID` and `ListUsers`.
- Update `scanUser` to scan the new column. Change its Scan call to append `&u.LastLoginIP` after `&u.LastLoginAt`:
  ```go
  err := row.Scan(&u.ID, &u.UID, &u.Username, &u.PasswordHash, &u.Role, &mcp, &sus, &u.RoleTemplateID, &u.CredentialPresetID, &u.DiskQuotaBytes, &u.MaxSessions, &u.CreatedAt, &u.LastLoginAt, &u.LastLoginIP)
  ```
- Update `ListUsers`'s row scan to append `&u.LastLoginIP` after `&u.LastLoginAt`.
- Change `TouchLogin`:
  ```go
  func (d *DB) TouchLogin(id int, ts int64, ip string) error {
  	_, err := d.sql.Exec(`UPDATE users SET last_login_at = ?, last_login_ip = ? WHERE id = ?`, ts, ip, id)
  	return err
  }
  ```

- [ ] **Step 4: Add field + update queries in sessions.go**

In `backend/internal/store/sessions.go`:
- Add to the `Session` struct (after `Alive`):
  ```go
  	Alive      bool
  	ClientIP   string
  ```
- `CreateSession`: add `client_ip` to the INSERT:
  ```go
  func (d *DB) CreateSession(s Session) error {
  	_, err := d.sql.Exec(
  		`INSERT INTO sessions (id, user_id, name, started_at, last_seen_at, alive, client_ip)
  		 VALUES (?, ?, ?, ?, ?, ?, ?)`,
  		s.ID, s.UserID, s.Name, s.StartedAt, s.LastSeenAt, btoi(s.Alive), s.ClientIP,
  	)
  	if err != nil {
  		return fmt.Errorf("create session: %w", err)
  	}
  	return nil
  }
  ```
- `GetSession`: add `, client_ip` to SELECT and `&s.ClientIP` to Scan (after `&alive`).
- `ListSessionsForUser`: add `, client_ip` to SELECT and `&s.ClientIP` to Scan (after `&alive`).

- [ ] **Step 5: Run tests to verify they pass**

Run: `cd backend && go test ./internal/store/ -v`
Expected: PASS (new + existing store tests).

- [ ] **Step 6: Find every TouchLogin caller and update**

Run: `cd backend && grep -rn "TouchLogin" --include=*.go`
For each caller (e.g. `auth_handler.go`), the signature now requires an ip arg. If a caller is in a later task (Task 3 updates `handleLogin`), leave a temporary placeholder ONLY if the build would otherwise break — but Task 3 is the only caller, so do Task 3 right after. **If the build breaks now** because `handleLogin` still calls `TouchLogin(id, ts)`, that is expected and is fixed in Task 3 — but to keep this task independently green, update `handleLogin`'s call site to `s.db.TouchLogin(u.ID, time.Now().Unix(), s.clientIP(r))` ONLY IF `clientIP` already exists; since it doesn't yet, instead pass `""` for now and fix in Task 3:
```go
_ = s.db.TouchLogin(u.ID, time.Now().Unix(), "")
```
Actually — `clientIP` is added in Task 3. To avoid a build break, make this task's commit compile by temporarily passing `""`. Then Task 3 replaces `""` with the real IP. Add a `// TODO Task 3: real IP` comment on that line.

Run: `cd backend && go build ./...`
Expected: builds (with the temporary `""`).

- [ ] **Step 7: Commit**

```bash
git add backend/internal/store/users.go backend/internal/store/sessions.go backend/internal/store/users_test.go backend/internal/store/sessions_test.go backend/internal/server/auth_handler.go
git commit -m "feat(store): User.LastLoginIP + Session.ClientIP; TouchLogin(id,ts,ip)"
```

---

### Task 3: login_events store layer

**Files:**
- Create: `backend/internal/store/login_events.go`
- Create: `backend/internal/store/login_events_test.go`

**Interfaces:**
- Consumes: Task 1 `login_events` table.
- Produces:
  - `type LoginEvent struct { ID int; UserID int; Username string; IP string; UserAgent string; Success bool; At int64 }`
  - `CreateLoginEvent(e LoginEvent) error`
  - `ListLoginEvents(limit int) ([]LoginEvent, error)` (newest-first; `limit<=0` → default 100, capped at 500).

- [ ] **Step 1: Write the failing test**

Create `backend/internal/store/login_events_test.go`:

```go
package store

import "testing"

func TestLoginEvents_CreateAndList(t *testing.T) {
	db := newTestDB(t)
	u, _ := db.GetUserByUsername("alice")

	if err := db.CreateLoginEvent(LoginEvent{
		UserID: u.ID, Username: u.Username, IP: "1.1.1.1", UserAgent: "curl", Success: true, At: 100,
	}); err != nil {
		t.Fatalf("create: %v", err)
	}
	if err := db.CreateLoginEvent(LoginEvent{
		UserID: 0, Username: "ghost", IP: "2.2.2.2", UserAgent: "curl", Success: false, At: 200,
	}); err != nil {
		t.Fatalf("create fail: %v", err)
	}

	got, err := db.ListLoginEvents(10)
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("got %d events, want 2", len(got))
	}
	// Newest-first (at DESC): the at=200 event comes first.
	if got[0].Username != "ghost" || got[0].Success {
		t.Errorf("first = %+v, want ghost/fail", got[0])
	}
	if got[1].Username != "alice" || !got[1].Success {
		t.Errorf("second = %+v, want alice/success", got[1])
	}
}

func TestLoginEvents_ListCap(t *testing.T) {
	db := newTestDB(t)
	u, _ := db.GetUserByUsername("alice")
	for i := 0; i < 5; i++ {
		if err := db.CreateLoginEvent(LoginEvent{UserID: u.ID, Username: u.Username, At: int64(i)}); err != nil {
			t.Fatal(err)
		}
	}
	got, _ := db.ListLoginEvents(3)
	if len(got) != 3 {
		t.Errorf("cap: got %d, want 3", len(got))
	}
	got2, _ := db.ListLoginEvents(0)
	if len(got2) != 5 {
		t.Errorf("default should return all when <100: got %d", len(got2))
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `cd backend && go test ./internal/store/ -run TestLoginEvents -v`
Expected: FAIL — `LoginEvent` undefined.

- [ ] **Step 3: Implement login_events.go**

Create `backend/internal/store/login_events.go`:

```go
package store

import "fmt"

// LoginEvent is one row of the login audit stream (every /auth attempt).
type LoginEvent struct {
	ID        int
	UserID    int
	Username  string
	IP        string
	UserAgent string
	Success   bool
	At        int64
}

// CreateLoginEvent appends one audit row. user_id is 0 when the username does
// not exist (failed login for unknown user) — username is always recorded.
func (d *DB) CreateLoginEvent(e LoginEvent) error {
	_, err := d.sql.Exec(
		`INSERT INTO login_events (user_id, username, ip, user_agent, success, at)
		 VALUES (?, ?, ?, ?, ?, ?)`,
		e.UserID, e.Username, e.IP, e.UserAgent, btoi(e.Success), e.At,
	)
	if err != nil {
		return fmt.Errorf("create login event: %w", err)
	}
	return nil
}

// ListLoginEvents returns the most recent `limit` events, newest-first. A
// non-positive limit defaults to 100; the limit is capped at 500.
func (d *DB) ListLoginEvents(limit int) ([]LoginEvent, error) {
	if limit <= 0 {
		limit = 100
	}
	if limit > 500 {
		limit = 500
	}
	rows, err := d.sql.Query(
		`SELECT id, user_id, username, ip, user_agent, success, at FROM login_events
		 ORDER BY at DESC, id DESC LIMIT ?`,
		limit,
	)
	if err != nil {
		return nil, fmt.Errorf("list login events: %w", err)
	}
	defer rows.Close()
	var out []LoginEvent
	for rows.Next() {
		var e LoginEvent
		var success int
		if err := rows.Scan(&e.ID, &e.UserID, &e.Username, &e.IP, &e.UserAgent, &success, &e.At); err != nil {
			return nil, fmt.Errorf("scan login event: %w", err)
		}
		e.Success = success == 1
		out = append(out, e)
	}
	return out, rows.Err()
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `cd backend && go test ./internal/store/ -run TestLoginEvents -v`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add backend/internal/store/login_events.go backend/internal/store/login_events_test.go
git commit -m "feat(store): login_events audit table CRUD"
```

---

### Task 4: clientIP helper + handleLogin writes events + TouchLogin(ip)

**Files:**
- Modify: `backend/internal/server/server.go`
- Modify: `backend/internal/server/auth_handler.go`
- Test: `backend/internal/server/server_test.go` (or `auth_handler_test.go`)

**Interfaces:**
- Consumes: `store.CreateLoginEvent`, `store.TouchLogin(id,ts,ip)` (Tasks 2-3).
- Produces: `Server.clientIP(r *http.Request) string`; `handleLogin` writes a login_event on every attempt + TouchLogin(ip) on success.

- [ ] **Step 1: Write the failing tests**

Append to `backend/internal/server/server_test.go`:

```go
// TestClientIP_Priority verifies CF-Connecting-IP wins over X-Real-IP,
// X-Forwarded-For, and RemoteAddr.
func TestClientIP_Priority(t *testing.T) {
	s := newTestServer(t)
	cases := []struct {
		name   string
		headers map[string]string
		remote string
		want   string
	}{
		{"cf", map[string]string{"CF-Connecting-IP": "1.1.1.1"}, "9.9.9.9:1", "1.1.1.1"},
		{"xreal", map[string]string{"X-Real-IP": "2.2.2.2"}, "9.9.9.9:1", "2.2.2.2"},
		{"xff", map[string]string{"X-Forwarded-For": "3.3.3.3, 8.8.8.8"}, "9.9.9.9:1", "3.3.3.3"},
		{"remote", map[string]string{}, "9.9.9.9:1234", "9.9.9.9"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			req := httptest.NewRequest("GET", "/health", nil)
			req.RemoteAddr = c.remote
			for k, v := range c.headers {
				req.Header.Set(k, v)
			}
			if got := s.clientIP(req); got != c.want {
				t.Errorf("got %q, want %q", got, c.want)
			}
		})
	}
}
```

And a login-event test (append):

```go
// TestLogin_WritesAuditEvent verifies a successful login writes a login_event
// (success=1) and updates last_login_ip; a failed login writes success=0 with
// the attempted username even for an unknown user.
func TestLogin_WritesAuditEvent(t *testing.T) {
	s := newTestServer(t)

	// Successful login.
	req := httptest.NewRequest("POST", "/auth", strings.NewReader(`{"username":"alice","password":"pw123"}`))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("CF-Connecting-IP", "203.0.113.10")
	w := httptest.NewRecorder()
	s.Routes().ServeHTTP(w, req)
	if w.Code != 200 {
		t.Fatalf("login: %d", w.Code)
	}
	u, _ := s.db.GetUserByUsername("alice")
	if u.LastLoginIP != "203.0.113.10" {
		t.Errorf("LastLoginIP = %q, want 203.0.113.10", u.LastLoginIP)
	}
	evs, err := s.db.ListLoginEvents(10)
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(evs) != 1 || !evs[0].Success || evs[0].IP != "203.0.113.10" {
		t.Fatalf("event mismatch: %+v", evs)
	}

	// Failed login for unknown user → success=0, username recorded, user_id 0.
	req2 := httptest.NewRequest("POST", "/auth", strings.NewReader(`{"username":"ghost","password":"x"}`))
	req2.Header.Set("Content-Type", "application/json")
	req2.Header.Set("CF-Connecting-IP", "203.0.113.11")
	w2 := httptest.NewRecorder()
	s.Routes().ServeHTTP(w2, req2)
	if w2.Code != 401 {
		t.Fatalf("want 401, got %d", w2.Code)
	}
	evs2, _ := s.db.ListLoginEvents(10)
	if len(evs2) != 2 {
		t.Fatalf("want 2 events, got %d", len(evs2))
	}
	fail := evs2[0] // newest first
	if fail.Success || fail.Username != "ghost" || fail.UserID != 0 || fail.IP != "203.0.113.11" {
		t.Errorf("fail event mismatch: %+v", fail)
	}
}
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `cd backend && go test ./internal/server/ -run 'TestClientIP_Priority|TestLogin_WritesAuditEvent' -v`
Expected: FAIL — `clientIP` undefined; login writes no events.

- [ ] **Step 3: Add clientIP helper to server.go**

In `backend/internal/server/server.go`, add `"net"` to imports (if not present) and add the method:

```go
// clientIP returns the originating client IP for r. Priority: CF-Connecting-IP
// (Cloudflare-injected, trusted because the deployment transits CF+nginx and
// the container's 8080 port is private) > X-Real-IP > first hop of
// X-Forwarded-For > RemoteAddr host.
func (s *Server) clientIP(r *http.Request) string {
	if v := r.Header.Get("CF-Connecting-IP"); v != "" {
		return v
	}
	if v := r.Header.Get("X-Real-IP"); v != "" {
		return v
	}
	if v := r.Header.Get("X-Forwarded-For"); v != "" {
		return strings.TrimSpace(strings.Split(v, ",")[0])
	}
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return host
}
```

- [ ] **Step 4: Wire login_event writes into handleLogin**

In `backend/internal/server/auth_handler.go`, `handleLogin`. The current structure: decode body → GetUserByUsername → on ErrNotFound decoy+401; on other err decoy+401; on bad password 401; on suspended 403; on success TouchLogin + set cookie.

Add an audit write on EVERY terminal path. Truncate UA to 256. Replace the function body's terminal branches with versions that record the event first. Concretely, after decoding `b` and computing `ip := s.clientIP(r)` and `ua := r.Header.Get("User-Agent")` (truncate to 256) and `now := time.Now().Unix()`, insert before each return:

For the ErrNotFound / other-DB-err / bad-password paths (all 401):
```go
	_ = s.db.CreateLoginEvent(store.LoginEvent{
		UserID: 0, Username: b.Username, IP: ip, UserAgent: ua, Success: false, At: now,
	})
```
(For the bad-password path, `u.ID` is known — use `UserID: u.ID` there instead of 0.)

For the suspended path (403): record `Success: true, UserID: u.ID` (the credentials WERE valid; the account is just suspended — audit accordingly) then return 403.

For the success path: keep TouchLogin but pass the ip, set the cookie, THEN record the success event:
```go
	_ = s.db.TouchLogin(u.ID, now, ip)
	// ... existing cookie set ...
	_ = s.db.CreateLoginEvent(store.LoginEvent{
		UserID: u.ID, Username: u.Username, IP: ip, UserAgent: ua, Success: true, At: now,
	})
```

Add the `"github.com/ldm0206/claude-docker/backend/internal/store"` import to `auth_handler.go` if not already present.

UA truncation helper (add at file bottom or inline):
```go
func truncateUA(s string) string {
	if len(s) > 256 {
		return s[:256]
	}
	return s
}
```
Use `ua := truncateUA(r.Header.Get("User-Agent"))`.

Remove the temporary `// TODO Task 3: real IP` + `""` from Task 2 — now use the real `ip`.

- [ ] **Step 5: Run tests to verify they pass**

Run: `cd backend && go test ./internal/server/ -run 'TestClientIP_Priority|TestLogin_WritesAuditEvent' -v`
Expected: PASS.

- [ ] **Step 6: Run full server package + vet + cross-compile**

Run: `cd backend && go vet ./... && go test ./internal/server/ -v && GOOS=linux go build ./...`
Expected: PASS; linux build clean.

- [ ] **Step 7: Commit**

```bash
git add backend/internal/server/server.go backend/internal/server/auth_handler.go backend/internal/server/server_test.go
git commit -m "feat(auth): clientIP helper + login_events audit on every /auth attempt"
```

---

### Task 5: Session creation records client_ip

**Files:**
- Modify: `backend/internal/pty/manager.go`
- Modify: `backend/internal/sessions/manager.go`
- Modify: `backend/internal/server/server.go` (ensureSession)
- Modify: `backend/internal/server/sessions_api.go` (handleCreateSession)
- Test: `backend/internal/sessions/manager_test.go` (extend)

**Interfaces:**
- Consumes: `store.Session.ClientIP` (Task 2).
- Produces: `pty.Options.ClientIP string`; `sessions.Manager.Create` writes `opts.ClientIP` into the session row; both session-create call sites set `opts.ClientIP = s.clientIP(r)`.

- [ ] **Step 1: Write the failing test**

Append to `backend/internal/sessions/manager_test.go`:

```go
// TestCreate_StoresClientIP verifies the opts.ClientIP is persisted on the
// session row.
func TestCreate_StoresClientIP(t *testing.T) {
	db := newTestManagerDB(t) // the existing helper in manager_test.go (temp store + alice)
	alice, _ := db.GetUserByUsername("alice")
	factory, _ := newFakePTYFactory()
	mgr := NewManager(db, factory)

	opts := Options{Cwd: "/tmp", ClientIP: "198.51.100.42", Username: "alice"}
	id, _, err := mgr.Create("alice", alice.ID, "/tmp", nil, opts)
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	rows, _ := db.ListSessionsForUser(alice.ID)
	var found *store.Session
	for i := range rows {
		if rows[i].ID == id {
			found = &rows[i]
		}
	}
	if found == nil {
		t.Fatal("session row not found")
	}
	if found.ClientIP != "198.51.100.42" {
		t.Errorf("ClientIP = %q, want 198.51.100.42", found.ClientIP)
	}
}
```

(Inspect `manager_test.go` first: match its db-open helper name (`newTestManagerDB`) and its `Options`/`newFakePTYFactory` names exactly. The `Options` type here is `pty.Options` — if the test file uses a qualified import, match it. If `manager_test.go` already builds an `Options{}` value, follow that exact form and just add `ClientIP`.)

- [ ] **Step 2: Run test to verify it fails**

Run: `cd backend && go test ./internal/sessions/ -run TestCreate_StoresClientIP -v`
Expected: FAIL — `Options.ClientIP` undefined (compile error) or the row has empty ClientIP.

- [ ] **Step 3: Add ClientIP to pty.Options**

In `backend/internal/pty/manager.go`, add the field to `Options`:

```go
type Options struct {
	Cwd      string
	Env      func() []string
	Command  string
	Args     []string
	Cols     uint16
	Rows     uint16
	Username string
	ClientIP string
}
```

- [ ] **Step 4: Write opts.ClientIP into the session row in Manager.Create**

In `backend/internal/sessions/manager.go`, `Create`, the `db.CreateSession(store.Session{...})` call — add `ClientIP: opts.ClientIP`:

```go
	if err := m.db.CreateSession(store.Session{
		ID:         id,
		UserID:     userID,
		Name:       opts.Username,
		StartedAt:  now,
		LastSeenAt: now,
		Alive:      true,
		ClientIP:   opts.ClientIP,
	}); err != nil {
```

- [ ] **Step 5: Set opts.ClientIP at both create call sites**

In `backend/internal/server/server.go` `ensureSession`, the create-path `opts := pty.Options{...}` block — add `ClientIP: s.clientIP(r)`. But note `ensureSession` does NOT currently take `*http.Request`. Check its signature: `ensureSession(u store.User, sid string)`. The WS handler `handleTerminalWS` calls it with `(u, sid)`. Thread `r` through: change the signature to `ensureSession(u store.User, sid string, r *http.Request)` and update the WS handler call (`terminal.go`) to pass `r`. Add `ClientIP: s.clientIP(r)` to the create-path opts.

In `backend/internal/server/sessions_api.go` `handleCreateSession`, the `opts := pty.Options{...}` block — add `ClientIP: s.clientIP(r)`.

- [ ] **Step 6: Run tests to verify they pass**

Run: `cd backend && go test ./internal/sessions/ ./internal/server/ -v`
Expected: PASS. (Existing `ensureSession` tests in `server_test.go` call `ensureSession(alice, "")` — they now need the `r` arg. Update those calls to pass `httptest.NewRequest("GET", "/", nil)`. Inspect `server_test.go` for every `ensureSession(` call and add the request arg.)

- [ ] **Step 7: Run full suite + vet + cross-compile**

Run: `cd backend && go vet ./... && go test ./... && GOOS=linux go build ./...`
Expected: PASS; linux build clean.

- [ ] **Step 8: Commit**

```bash
git add backend/internal/pty/manager.go backend/internal/sessions/manager.go backend/internal/sessions/manager_test.go backend/internal/server/server.go backend/internal/server/terminal.go backend/internal/server/sessions_api.go backend/internal/server/server_test.go
git commit -m "feat(sessions): record client_ip on session creation (pty.Options.ClientIP)"
```

---

### Task 6: Admin API — login-events endpoint + extend users/sessions responses

**Files:**
- Create: `backend/internal/server/admin_audit.go`
- Create: `backend/internal/server/admin_audit_test.go`
- Modify: `backend/internal/server/server.go` (route registration)
- Modify: `backend/internal/server/admin_users.go` (list adds last_login_ip/at)
- Modify: `backend/internal/server/admin_sessions.go` (session list adds client_ip)

**Interfaces:**
- Consumes: `store.ListLoginEvents` (Task 3); `User.LastLoginIP`/`Session.ClientIP` (Task 2).
- Produces: `GET /api/admin/login-events?limit=N`; `GET /api/admin/users` includes `last_login_ip`+`last_login_at`; `GET /api/admin/users/:id/sessions` includes `client_ip`.

- [ ] **Step 1: Write the failing handler test**

Create `backend/internal/server/admin_audit_test.go`:

```go
package server

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// TestAdminLoginEvents_RequiresAdmin verifies a non-admin (alice, role user)
// gets 403 and an admin gets 200 + the events.
func TestAdminLoginEvents_RequiresAdmin(t *testing.T) {
	s := newTestServer(t)
	// alice is role "user" — seed a login event so there's something to see.
	s.db.CreateLoginEvent(storeLoginEventForTest("alice", 1, true, "1.2.3.4"))

	req := httptest.NewRequest("GET", "/api/admin/login-events", nil)
	req.AddCookie(&http.Cookie{Name: "session", Value: loginAsAlice(t, s)})
	w := httptest.NewRecorder()
	s.Routes().ServeHTTP(w, req)
	if w.Code != 403 {
		t.Fatalf("non-admin: want 403, got %d", w.Code)
	}
}

// TestAdminLoginEvents_AdminSeesEvents verifies an admin sees newest-first.
func TestAdminLoginEvents_AdminSeesEvents(t *testing.T) {
	s := newTestServer(t)
	cookie := loginAsAdmin(t, s) // helper: bootstrap an admin + login
	s.db.CreateLoginEvent(storeLoginEventForTest("alice", 1, true, "1.2.3.4"))
	s.db.CreateLoginEvent(storeLoginEventForTest("ghost", 0, false, "5.6.7.8"))

	req := httptest.NewRequest("GET", "/api/admin/login-events?limit=10", nil)
	req.AddCookie(&http.Cookie{Name: "session", Value: cookie})
	w := httptest.NewRecorder()
	s.Routes().ServeHTTP(w, req)
	if w.Code != 200 {
		t.Fatalf("admin: want 200, got %d; body=%s", w.Code, w.Body.String())
	}
	body := w.Body.String()
	if !strings.Contains(body, `"username":"ghost"`) {
		t.Fatalf("missing ghost event: %s", body)
	}
	if !strings.Contains(body, `"client_ip"`) == false {
		// events use "ip" not "client_ip" — just sanity check json has ip field
	}
}
```

**Helpers needed:** `loginAsAdmin(t, s)` and `storeLoginEventForTest(...)`. If these don't exist:
- `loginAsAdmin`: create an admin user in `newTestServer`'s db (`db.CreateUser(store.User{UID: mustUID, Username:"root", PasswordHash: hashOf("pw123"), Role:"admin", CreatedAt:1})`), then POST /auth as root, return the cookie.
- `storeLoginEventForTest(username, userID, success, ip)`: returns a `store.LoginEvent{...}`.

Inspect `server_test.go` for an existing admin-login helper first; reuse if present.

- [ ] **Step 2: Run test to verify it fails**

Run: `cd backend && go test ./internal/server/ -run TestAdminLoginEvents -v`
Expected: FAIL — route/handler undefined.

- [ ] **Step 3: Implement the handler**

Create `backend/internal/server/admin_audit.go`:

```go
package server

import (
	"net/http"
	"strconv"
)

// handleAdminLoginEvents returns the most recent login attempts (newest-first).
// Admin-only. ?limit=N (default 100, capped 500 by the store).
func (s *Server) handleAdminLoginEvents(w http.ResponseWriter, r *http.Request) {
	limit := 0
	if v := r.URL.Query().Get("limit"); v != "" {
		if n, err := strconv.Atoi(v); err == nil {
			limit = n
		}
	}
	events, err := s.db.ListLoginEvents(limit)
	if err != nil {
		writeJSON(w, 500, map[string]any{"error": "list login events failed"})
		return
	}
	out := make([]map[string]any, len(events))
	for i, e := range events {
		out[i] = map[string]any{
			"id":         e.ID,
			"userId":     e.UserID,
			"username":   e.Username,
			"ip":         e.IP,
			"userAgent":  e.UserAgent,
			"success":    e.Success,
			"at":         e.At,
		}
	}
	writeJSON(w, 200, out)
}
```

- [ ] **Step 4: Register the route**

In `backend/internal/server/server.go` `Routes()`, inside the admin group (`r.Use(s.requireAdmin)`), add:

```go
		r.Get("/api/admin/login-events", s.handleAdminLoginEvents)
```

- [ ] **Step 5: Extend admin_users.go list response**

In `handleAdminListUsers` (`admin_users.go`), add `last_login_ip` and `last_login_at` to each map:

```go
	out[i] = map[string]any{
		"id":            u.ID,
		"username":      u.Username,
		"role":          u.Role,
		"suspended":     u.Suspended,
		"lastLoginIp":   u.LastLoginIP,
		"lastLoginAt":   u.LastLoginAt,
	}
```

- [ ] **Step 6: Extend admin_sessions.go session response**

In `handleAdminListSessions` (`admin_sessions.go`), add `client_ip` to each session map:

```go
		out[i] = map[string]any{
			"id":         s.ID,
			"name":       s.Name,
			"startedAt":  s.StartedAt,
			"lastSeenAt": s.LastSeenAt,
			"alive":      s.Alive,
			"clientIp":   s.ClientIP,
		}
```
(Note: the loop variable is named `s` shadowing the receiver — rename the loop var to `sess` to avoid confusion, or keep `s` matching the existing code. Keep it as-is to minimize the diff, but be aware.)

- [ ] **Step 7: Run tests to verify they pass**

Run: `cd backend && go test ./internal/server/ -run TestAdminLoginEvents -v`
Expected: PASS.

Then verify existing admin tests still pass: `cd backend && go test ./internal/server/ -v`.

- [ ] **Step 8: Run full suite + vet + cross-compile**

Run: `cd backend && go vet ./... && go test ./... && GOOS=linux go build ./...`
Expected: PASS.

- [ ] **Step 9: Commit**

```bash
git add backend/internal/server/admin_audit.go backend/internal/server/admin_audit_test.go backend/internal/server/server.go backend/internal/server/admin_users.go backend/internal/server/admin_sessions.go
git commit -m "feat(admin): /api/admin/login-events + last_login_ip on users + client_ip on sessions"
```

---

### Task 7: Frontend — Audit view + Users Last-login column + fix traffic SFTP text

**Files:**
- Modify: `web/src/main.js`

**Interfaces:**
- Consumes: `/api/admin/login-events` (Task 6); the extended `/api/admin/users` response.
- Produces: a new "Audit" sidebar item + view; Users page "Last login" column; removes stale "SFTP" text in the traffic view.

- [ ] **Step 1: Add the Audit nav + title + view registration**

In `web/src/main.js`:
- In `renderSidebar`'s `admin` array, add `["audit", " Audit"]` (after `["captures", " Captures"]`):
  ```js
  const admin = [
    ["users", " Users"],
    ["credentials", " Credentials"],
    ["templates", " Templates"],
    ["captures", " Captures"],
    ["audit", " Audit"],
  ];
  ```
- In the `TITLES` map, add `audit: "Audit"`:
  ```js
  const TITLES = { terminal: "Terminal", files: "Files", traffic: "Traffic", users: "Users", credentials: "Credentials", templates: "Templates", captures: "Captures", audit: "Audit" };
  ```
- In the `VIEWS` registration block, add:
  ```js
  VIEWS.audit = viewAudit;
  ```

- [ ] **Step 2: Add the viewAudit function**

Add near the other view functions (e.g. after `viewTemplates`):

```js
async function viewAudit(root) {
  root.innerHTML = `<div class="card" style="overflow:auto"><table class="tbl"><thead><tr>
    <th>Time</th><th>User</th><th>IP</th><th>Result</th><th>User-Agent</th>
    </tr></thead><tbody id="abody"></tbody></table></div>`;
  const tb = document.getElementById("abody");
  let events = [];
  try { events = await getJson("/api/admin/login-events?limit=200"); } catch {}
  tb.innerHTML = "";
  for (const e of events) {
    const tr = document.createElement("tr");
    const when = e.at ? new Date(e.at * 1000).toLocaleString() : "—";
    const result = e.success
      ? '<span class="pill online">ok</span>'
      : '<span class="pill suspended">fail</span>';
    tr.innerHTML = `<td class="muted">${esc(when)}</td><td><b>${esc(e.username)}</b></td><td class="muted">${esc(e.ip || "—")}</td><td>${result}</td><td class="muted tiny">${esc((e.userAgent || "—").slice(0, 60))}</td>`;
    tb.appendChild(tr);
  }
  if (!events.length) tb.innerHTML = `<tr><td class="muted" colspan="5">No login events yet.</td></tr>`;
}
```

- [ ] **Step 3: Add a Last-login column to the Users table**

In `viewAdminUsers`, the table header — add a `<th>Last login</th>` column (after the Sessions column, before the trailing empty `<th></th>`):
```js
    <tr><th>User</th><th>Role</th><th>Status</th><th>Disk</th><th>Traffic</th><th>Sessions</th><th>Last login</th><th></th></tr>
```
In `refreshUsers`, the row `<tr>` — add a cell for last login after the sessions cell:
```js
    tr.innerHTML = `<td><b>${esc(u.username)}</b></td><td><span class="pill ${u.role==='admin'?'admin':''}">${u.role}</span></td><td>${status}</td><td class="muted" id="d-${u.id}">—</td><td class="muted" id="t-${u.id}">—</td><td class="muted" id="s-${u.id}">—</td><td class="muted" id="ll-${u.id}">—</td>`;
```
After the existing `getJson(.../usage).then(...)` block, add a last-login fill (the `/api/admin/users` response already carries `lastLoginIp`/`lastLoginAt` from Task 6 — `u` is the list element):
```js
    const ll = document.getElementById(`ll-${u.id}`);
    if (ll) {
      const when = u.lastLoginAt ? new Date(u.lastLoginAt * 1000).toLocaleString() : "never";
      ll.innerHTML = esc(when) + (u.lastLoginIp ? ` <span class="faint tiny">${esc(u.lastLoginIp)}</span>` : "");
    }
```
(Place this fill inside the `for (const u of users)` loop, after the row is appended, so `u` is in scope. Note `lastLoginAt` may come back as `null`/`0` when never logged in — the `?` check handles it; but `u.lastLoginAt` could be a JSON number 0 — guard with `u.lastLoginAt && u.lastLoginAt > 0`.)

- [ ] **Step 4: Fix stale SFTP text in the traffic view**

In `refreshTraffic` (around the line `trows.innerHTML = ...SFTP/terminal transfers...`), replace the SFTP reference:
```js
  trows.innerHTML = `<span class="muted">Per-user traffic details are visible to admins on the Users page. Your terminal and file-manager transfers are counted toward your monthly quota.</span>`;
```

- [ ] **Step 5: Build**

Run: `cd web && npm run build`
Expected: succeeds.

- [ ] **Step 6: Commit**

```bash
git add web/src/main.js
git commit -m "feat(web): admin Audit view + Users last-login column; fix stale SFTP text"
```

---

### Task 8: Dockerfile — node 22 + python3 + drop EXPOSE 22

**Files:**
- Modify: `Dockerfile`

**Interfaces:** none (image layer).

- [ ] **Step 1: Add python to the apt list**

In `Dockerfile` stage 3, the `RUN apt-get install -y --no-install-recommends` line — add `python3 python3-pip python3-venv`:

```dockerfile
RUN apt-get update && apt-get install -y --no-install-recommends \
        git ripgrep curl ca-certificates jq tini gosu sudo openssl screen tmux \
        nftables openssh-client python3 python3-pip python3-venv \
    && rm -rf /var/lib/apt/lists/*
```

- [ ] **Step 2: Add NodeSource node 22**

After the apt block (and before the claude-binary download, or after it — order is flexible; place it after the apt block for clarity), add:

```dockerfile
# Node.js 22 LTS (NodeSource) — matches the web-builder's node:22. All users
# get node/npm on the system PATH.
RUN curl -fsSL https://deb.nodesource.com/setup_22.x | bash - \
    && apt-get install -y --no-install-recommends nodejs \
    && rm -rf /var/lib/apt/lists/*
```

- [ ] **Step 3: Drop EXPOSE 22**

Change the final `EXPOSE 8080 22` to:

```dockerfile
EXPOSE 8080
```

- [ ] **Step 4: Verify the Dockerfile syntactically**

Run: `cd /c/PythonProject/claude-docker && grep -n "node\|python\|EXPOSE\|nodesource" Dockerfile`
Expected: shows python3 in the apt list, the NodeSource RUN, and `EXPOSE 8080` (no `22`).

(No image build on Windows — that is a Linux/Docker deploy step verified in Task 9.)

- [ ] **Step 5: Commit**

```bash
git add Dockerfile
git commit -m "feat(docker): preinstall node 22 + python3/pip/venv; drop leftover EXPOSE 22"
```

---

### Task 9: DEPLOY-TEST — toolchain check + audit verification + 8080-private note

**Files:**
- Modify: `DEPLOY-TEST.md`

**Interfaces:** none.

- [ ] **Step 1: Add a toolchain verification section**

In `DEPLOY-TEST.md`, add a new section (numbered to follow the existing sections):

```markdown
## N. Shell toolchain

After `docker compose up`, exec into the container as a regular user and confirm
all preinstalled tools resolve on the user PATH:
```bash
docker compose exec claude gosu alice bash -lc 'node -v && npm -v && python3 --version && git --version && claude --version'
```
Each command must print a version (node 22.x, npm 10.x, python 3.11.x, git 2.x,
claude x.y.z). A "command not found" means the image layer failed.
```

- [ ] **Step 2: Add the 8080-private note**

In the security/deployment notes section, add:

```markdown
## Network exposure (audit IP trust)

`clientIP` trusts `CF-Connecting-IP` / `X-Forwarded-For`. This is safe ONLY if
the container's 8080 port is not reachable directly from the public internet
(i.e. only nginx reaches it). If 8080 were public, a client could forge the
header. Verify: `docker compose port claude 8080` is bound to a private
interface / localhost, not 0.0.0.0:8080 exposed to the WAN.
```

- [ ] **Step 3: Add an audit verification section**

```markdown
## Audit (login IP + session IP)

- As admin, open the **Audit** sidebar item: login events appear newest-first;
  a failed login (wrong password) shows as a red "fail" row with the attempted
  username and the client IP.
- Log in as a user via Cloudflare; the Audit row's IP is the user's real IP
  (not nginx's). If it shows a private/nginx IP, the `CF-Connecting-IP` /
  `X-Forwarded-For` header is not being passed by nginx.
- Users page: each user shows a "Last login" column with timestamp + IP.
- Create a terminal session for a user; as admin, the user's session list
  (`/api/admin/users/:id/sessions`) includes `clientIp` matching the login IP.
```

- [ ] **Step 4: Commit**

```bash
git add DEPLOY-TEST.md
git commit -m "docs: DEPLOY-TEST — toolchain check, audit verification, 8080-private note"
```

---

### Task 10: Whole-branch review + final verification

**Files:** none (verification + fixes).

- [ ] **Step 1: Run the full Windows-verification gate**

Run:
```
cd backend && go vet ./... && go test ./... && GOOS=linux go build ./... && cd ../web && npm run build
```
Expected: all PASS; linux build clean; SPA builds.

- [ ] **Step 2: Dispatch a whole-branch review subagent**

Use `feature-dev:code-reviewer` (or `code-reviewer`) with model **sonnet**. Scope: the entire diff from the plan-9 start commit to HEAD. Have it verify:
- Every `/auth` path writes exactly one login_event (success OR failure; not both; not zero).
- `TouchLogin` now takes ip and the temp `""`/TODO from Task 2 is gone.
- `clientIP` priority + the 8080-private trust assumption is documented.
- `pty.Options.ClientIP` is threaded through BOTH create paths (WS ensureSession + REST handleCreateSession); the attach path does NOT overwrite the stored IP.
- `login_events` for an unknown user has user_id=0 + the attempted username.
- No regular-user endpoint leaks another user's IP.
- Dockerfile: NodeSource URL + apt package names correct; EXPOSE has no 22.
- `ensureSession` signature change (`r *http.Request`) didn't break existing callers (all updated).
- UA truncation applied before storing.

- [ ] **Step 3: Address findings (fix in new commits)**

Apply confirmed findings. Re-run the gate from Step 1.

- [ ] **Step 4: Final state**

Report what was Windows-verified vs. what awaits Linux/Docker runtime (image build + the DEPLOY-TEST steps).

---

## Self-Review

**Spec coverage:**
- Part 1 data model (users.last_login_ip, sessions.client_ip, login_events): Tasks 1-3. ✓
- Part 1 clientIP + handleLogin audit: Task 4. ✓
- Part 1 session client_ip recording: Task 5. ✓
- Part 1 admin API + Audit UI: Tasks 6-7. ✓
- Part 2 toolchain (node/python) + EXPOSE fix: Task 8. ✓
- Docs (DEPLOY-TEST + 8080-private): Task 9. ✓
- Final review: Task 10. ✓

**Placeholder scan:** No TBD/TODO except one intentional `// TODO Task 3: real IP` in Task 2 Step 6, which is explicitly removed in Task 4 Step 4. The Task 6 helpers (`loginAsAdmin`, `storeLoginEventForTest`) are described with concrete implementations, not placeholders — the implementer inspects `server_test.go` for an existing admin-login helper and reuses if present.

**Type consistency:**
- `TouchLogin(id int, ts int64, ip string)` — defined Task 2, called Task 4.
- `User.LastLoginIP string` / `Session.ClientIP string` — defined Task 2, read Task 6.
- `LoginEvent` + `CreateLoginEvent` + `ListLoginEvents(limit)` — defined Task 3, called Task 4 (create) + Task 6 (list).
- `pty.Options.ClientIP` — added Task 5, set Task 5, read Task 5.
- `clientIP(r)` — defined Task 4, called Tasks 4, 5, (Task 6 does not call it). ✓
- `ensureSession(u, sid, r)` — signature changed Task 5, callers updated Task 5. ✓
- JSON keys: `lastLoginIp`/`lastLoginAt` (Task 6 server) match `u.lastLoginIp`/`u.lastLoginAt` (Task 7 frontend); `clientIp` (Task 6) — frontend Audit uses `e.ip` (login_events) not clientIp (sessions), consistent. ✓

**Known open items (implementation-time):**
- The exact name of the db-open helper in `store/*_test.go` (`newTestDB`) and `sessions/manager_test.go` (`newTestManagerDB`) — the plan says "inspect and match the existing helper." This is a lookup, not a placeholder.
- Whether `server_test.go` already has an admin-login helper for Task 6 — inspect and reuse if present; otherwise implement as described.
