# Multi-User Platform — Plan 2: Identity + Per-User Isolation

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Replace Plan 1's single-user `ACCESS_KEY` auth with a multi-user identity layer (SQLite-backed users, argon2id passwords, first-login change, bootstrap admin) and add per-user Linux-account isolation (separate home/workspace/claude-config, `gosu`-dropped PTY, root-run server, shared `/opt/claude/bin`). After this plan, an admin can create users and each user logs into their own isolated environment.

**Architecture:** Adds `internal/store` (SQLite via `modernc.org/sqlite`, pure-Go) holding a `users` table; `internal/system` manages Linux account/directory lifecycle (`useradd`/`userdel`/`usermod`/chown) and runs as **root**; the PTY manager spawns `gosu <username> bash -l` so each terminal runs as that user; auth becomes username+password with a session cookie carrying `{username, role}`. The server process switches from user `claude` (Plan 1) to **root** (required for `setuid`/`gosu`), and the claude binary moves to a shared `/opt/claude/bin`.

**Tech Stack:** Go 1.23, `modernc.org/sqlite` (CGO-free), `golang.org/x/crypto/argon2` + `golang.org/x/crypto/bcrypt`-style params, existing `chi`/`coder/websocket`/`creack/pty`.

## Global Constraints

- Branch: `feat/plan-2-identity-isolation`. Module `github.com/ldm0206/claude-docker/backend`, `CGO_ENABLED=0`, Dockerfile go-builder `golang:1.23-bookworm` (matches `go.mod`).
- **Two test tiers (host is Windows, no Docker/WSL):**
  - **Windows-runnable** (pure Go + SQLite + httptest): store, password hashing, bootstrap, config, auth handlers. Full TDD (RED/GREEN) on the host.
  - **Linux-only** (`internal/system`, `gosu` PTY, Dockerfile/entrypoint root switch): compile-verify on Windows (`go build`, `go vet`, `GOOS=linux go test -c`); runtime GREEN is **deferred to the user's deploy/test** on Linux. Mark these tests `//go:build linux`.
- This plan **supersedes** Plan 1's `ACCESS_KEY` auth: `config.Load` no longer requires `ACCESS_KEY`; `/auth` accepts `{username,password}`; the session payload becomes `{username, role, iat}`.
- Deviation from spec §4 (noted): the `users` table gains a `uid INTEGER UNIQUE NOT NULL` column (allocated from 2000+) because isolation needs a deterministic uid for `useradd -u` and cgroup paths (Plan 4). All other spec fields preserved.
- Usernames validated to `^[a-z_][a-z0-9_-]{1,31}$` (valid Linux account names); creating a user creates the Linux account and DB row atomically best-effort (DB row first, then Linux account; on Linux failure, roll back the row).
- DRY, YAGNI, TDD. Frequent commits.

## File Structure (this plan adds/changes)

```
backend/
  internal/
    store/
      store.go              # Open(path) (*DB), schema bootstrap (//go:embed schema.sql)
      schema.sql            # CREATE TABLE IF NOT EXISTS users (...)  [appended by later plans]
      users.go              # CreateUser, GetUserByUsername, GetUserByID, SetPassword, VerifyPassword, AllocateUID
      users_test.go         # Windows-runnable
      store_test.go
    system/
      account.go            # CreateUserAccount(username, uid) / Delete / Lock / Unlock via useradd/userdel/usermod
      dirs.go               # ProvisionUserDirs(username, uid): /home/<u> (root:root 0755), workspace (user), /data/<u>/claude-config (user)
      account_test.go       # //go:build linux
    auth/
      password.go           # HashPassword / CheckPassword (argon2id)  [NEW; auth.go keeps sign/verify]
      password_test.go
    config/
      config.go             # MODIFY: drop ACCESS_KEY requirement; add BootstrapAdminUser/Password; keep SessionSecret required
      config_test.go        # MODIFY
    pty/
      env.go                # MODIFY: add BuildUserEnv(cfg, username, claudeConfigDir, credEnv) []string; BuildClaudeEnv deprecated/removed
      manager.go            # MODIFY: Options gains Username + uses gosu when set
    server/
      server.go             # MODIFY: New takes *store.DB; session payload {username,role}; authMiddleware sets identity in context
      auth_handler.go       # [NEW, split from server.go] /auth {username,password}, /auth/change-password, /auth/logout
      admin_users.go        # [NEW] /api/admin/users GET/POST (admin-gated); create provisions Linux account
      server_test.go        # MODIFY: login flow tests
  cmd/server/main.go        # MODIFY: open store, bootstrap admin, pass store to server
entrypoint.sh               # MODIFY: drop `gosu claude`; run binary as root; mkdir /data
Dockerfile                  # MODIFY: claude binary → /opt/claude/bin (root:root 0755); runtime runs as root; add screen, tmux
```

---

### Task 1: SQLite store + users schema

**Files:**
- Create: `backend/internal/store/store.go`, `backend/internal/store/schema.sql`, `backend/internal/store/store_test.go`
- Add dep: `modernc.org/sqlite`

**Interfaces:**
- Produces: `store.Open(path string) (*DB, error)` (applies embedded `schema.sql`); `(*DB).Close() error`; `(*DB).Sqlite() *sql.DB` (for use by later tasks/plans).

- [ ] **Step 1: Add dependency and write the failing test**

```bash
cd backend && go get modernc.org/sqlite && go mod tidy
```

`backend/internal/store/schema.sql`:
```sql
CREATE TABLE IF NOT EXISTS users (
  id INTEGER PRIMARY KEY AUTOINCREMENT,
  uid INTEGER UNIQUE NOT NULL,
  username TEXT UNIQUE NOT NULL,
  password_hash TEXT NOT NULL,
  role TEXT NOT NULL CHECK(role IN ('admin','user')),
  must_change_password INTEGER NOT NULL DEFAULT 1,
  role_template_id INTEGER,
  credential_preset_id INTEGER,
  suspended INTEGER NOT NULL DEFAULT 0,
  disk_quota_bytes INTEGER,
  max_sessions INTEGER,
  created_at INTEGER NOT NULL,
  last_login_at INTEGER
);
```

`backend/internal/store/store_test.go`:
```go
package store

import (
	"path/filepath"
	"testing"
)

func TestOpenAppliesSchema(t *testing.T) {
	d := t.TempDir()
	db, err := Open(filepath.Join(d, "test.db"))
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer db.Close()
	// users table exists and is empty
	var n int
	if err := db.Sqlite().QueryRow("SELECT count(*) FROM users").Scan(&n); err != nil {
		t.Fatalf("query users: %v", err)
	}
	if n != 0 {
		t.Fatalf("expected empty users table, got %d", n)
	}
}
```

- [ ] **Step 2: Run test to verify it fails** — `cd backend && go test ./internal/store/` → FAIL (`Open` undefined).

- [ ] **Step 3: Write minimal implementation**

`backend/internal/store/store.go`:
```go
package store

import (
	"database/sql"
	_ "embed"
	"fmt"

	_ "modernc.org/sqlite"
)

//go:embed schema.sql
var schemaSQL string

type DB struct {
	sql *sql.DB
}

func Open(path string) (*DB, error) {
	sq, err := sql.Open("sqlite", path)
	if err != nil {
		return nil, fmt.Errorf("open sqlite %s: %w", path, err)
	}
	if _, err := sq.Exec(schemaSQL); err != nil {
		sq.Close()
		return nil, fmt.Errorf("apply schema: %w", err)
	}
	return &DB{sql: sq}, nil
}

func (d *DB) Sqlite() *sql.DB { return d.sql }
func (d *DB) Close() error    { return d.sql.Close() }
```

- [ ] **Step 4: Run test to verify it passes** — `go test ./internal/store/` → PASS.
- [ ] **Step 5: Commit** — `git add backend/internal/store/ backend/go.mod backend/go.sum && git commit -m "feat(backend): add SQLite store with users schema"`

---

### Task 2: argon2id password hashing + user CRUD + uid allocation

**Files:**
- Create: `backend/internal/auth/password.go`, `backend/internal/auth/password_test.go`
- Create: `backend/internal/store/users.go`, `backend/internal/store/users_test.go`
- Add dep: `golang.org/x/crypto`

**Interfaces:**
- Produces: `auth.HashPassword(pw string) (string, error)` (argon2id, encoded); `auth.CheckPassword(pw, encoded string) bool`.
- Produces: `store.User` struct (mirrors columns); `(*DB) AllocateUID() (int, error)` (2000 if none, else max(uid)+1); `(*DB) CreateUser(u User) (User, error)`; `(*DB) GetUserByUsername(name string) (User, error)`; `(*DB) GetUserByID(id int) (User, error)`; `(*DB) SetPassword(id int, hash string) error`; `(*DB) TouchLogin(id int, ts int64) error`; `(*DB) SetSuspended(id int, suspended bool) error`.

- [ ] **Step 1: Write the failing tests**

`backend/internal/auth/password_test.go`:
```go
package auth

import "testing"

func TestHashAndCheck(t *testing.T) {
	h, err := HashPassword("correct horse")
	if err != nil {
		t.Fatalf("hash: %v", err)
	}
	if !CheckPassword("correct horse", h) {
		t.Fatal("CheckPassword should accept the right password")
	}
	if CheckPassword("wrong", h) {
		t.Fatal("CheckPassword should reject a wrong password")
	}
}
```

`backend/internal/store/users_test.go`:
```go
package store

import (
	"path/filepath"
	"testing"
)

func TestCreateAndGetUser(t *testing.T) {
	db, _ := Open(filepath.Join(t.TempDir(), "t.db"))
	defer db.Close()
	uid, err := db.AllocateUID()
	if err != nil {
		t.Fatalf("allocate uid: %v", err)
	}
	if uid != 2000 {
		t.Fatalf("first uid = %d, want 2000", uid)
	}
	u, err := db.CreateUser(User{
		UID: uid, Username: "alice", PasswordHash: "x", Role: "admin",
		MustChangePassword: true, CreatedAt: 1,
	})
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	got, err := db.GetUserByUsername("alice")
	if err != nil || got.ID != u.ID || got.UID != 2000 || got.Role != "admin" {
		t.Fatalf("get by username: got %+v err %v", got, err)
	}
	uid2, _ := db.AllocateUID()
	if uid2 != 2001 {
		t.Fatalf("second uid = %d, want 2001", uid2)
	}
}
```

- [ ] **Step 2: Run tests to verify they fail** — `go test ./internal/auth/ ./internal/store/` → FAIL (undefined).
- [ ] **Step 3: Write minimal implementation**

`backend/internal/auth/password.go`:
```go
package auth

import (
	"crypto/rand"
	"crypto/subtle"
	"encoding/base64"
	"errors"
	"fmt"
	"strings"

	"golang.org/x/crypto/argon2"
)

const (
	pwTime    = 1
	pwMemory  = 64 * 1024
	pwThreads = 2
	pwKeyLen  = 32
)

func HashPassword(pw string) (string, error) {
	salt := make([]byte, 16)
	if _, err := rand.Read(salt); err != nil {
		return "", fmt.Errorf("read salt: %w", err)
	}
	key := argon2.IDKey([]byte(pw), salt, pwTime, pwMemory, pwThreads, pwKeyLen)
	return fmt.Sprintf("$argon2id$v=19$m=%d,t=%d,p=%d$%s$%s",
		pwMemory, pwTime, pwThreads,
		base64.RawStdEncoding.EncodeToString(salt),
		base64.RawStdEncoding.EncodeToString(key)), nil
}

func CheckPassword(pw, encoded string) bool {
	parts := strings.Split(encoded, "$")
	if len(parts) != 6 || parts[1] != "argon2id" {
		return false
	}
	var m, t, p uint32
	if _, err := fmt.Sscanf(parts[3], "m=%d,t=%d,p=%d", &m, &t, &p); err != nil {
		return false
	}
	salt, err := base64.RawStdEncoding.DecodeString(parts[4])
	if err != nil {
		return false
	}
	want, err := base64.RawStdEncoding.DecodeString(parts[5])
	if err != nil {
		return false
	}
	if len(want) == 0 {
		return false
	}
	got := argon2.IDKey([]byte(pw), salt, t, m, p, uint32(len(want)))
	return subtle.ConstantTimeCompare(got, want) == 1
}

var ErrPasswordChecked = errors.New("password checked")
```

`backend/internal/store/users.go`:
```go
package store

import (
	"database/sql"
	"errors"
	"fmt"
	"time"
)

type User struct {
	ID                 int
	UID                int
	Username           string
	PasswordHash       string
	Role               string
	MustChangePassword bool
	RoleTemplateID     sql.NullInt64
	CredentialPresetID sql.NullInt64
	Suspended          bool
	DiskQuotaBytes     sql.NullInt64
	MaxSessions        sql.NullInt64
	CreatedAt          int64
	LastLoginAt        sql.NullInt64
}

var ErrNotFound = errors.New("not found")

func (d *DB) AllocateUID() (int, error) {
	var maxUID sql.NullInt64
	if err := d.sql.QueryRow("SELECT MAX(uid) FROM users").Scan(&maxUID); err != nil {
		return 0, fmt.Errorf("allocate uid: %w", err)
	}
	if !maxUID.Valid {
		return 2000, nil
	}
	return int(maxUID.Int64) + 1, nil
}

func (d *DB) CreateUser(u User) (User, error) {
	if u.CreatedAt == 0 {
		u.CreatedAt = time.Now().Unix()
	}
	res, err := d.sql.Exec(
		`INSERT INTO users (uid, username, password_hash, role, must_change_password, suspended, created_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?)`,
		u.UID, u.Username, u.PasswordHash, u.Role, btoi(u.MustChangePassword), btoi(u.Suspended), u.CreatedAt)
	if err != nil {
		return User{}, fmt.Errorf("create user: %w", err)
	}
	id, _ := res.LastInsertId()
	u.ID = int(id)
	return u, nil
}

func (d *DB) GetUserByUsername(name string) (User, error) {
	row := d.sql.QueryRow(`SELECT id, uid, username, password_hash, role, must_change_password, suspended FROM users WHERE username = ?`, name)
	return scanUser(row)
}

func (d *DB) GetUserByID(id int) (User, error) {
	row := d.sql.QueryRow(`SELECT id, uid, username, password_hash, role, must_change_password, suspended FROM users WHERE id = ?`, id)
	return scanUser(row)
}

func (d *DB) SetPassword(id int, hash string) error {
	_, err := d.sql.Exec(`UPDATE users SET password_hash = ?, must_change_password = 0 WHERE id = ?`, hash, id)
	return err
}

func (d *DB) SetSuspended(id int, suspended bool) error {
	_, err := d.sql.Exec(`UPDATE users SET suspended = ? WHERE id = ?`, btoi(suspended), id)
	return err
}

func (d *DB) TouchLogin(id int, ts int64) error {
	_, err := d.sql.Exec(`UPDATE users SET last_login_at = ? WHERE id = ?`, ts, id)
	return err
}

func scanUser(row *sql.Row) (User, error) {
	var u User
	var mcp, sus int
	err := row.Scan(&u.ID, &u.UID, &u.Username, &u.PasswordHash, &u.Role, &mcp, &sus)
	if errors.Is(err, sql.ErrNoRows) {
		return User{}, ErrNotFound
	}
	if err != nil {
		return User{}, err
	}
	u.MustChangePassword = mcp == 1
	u.Suspended = sus == 1
	return u, nil
}

func btoi(b bool) int {
	if b {
		return 1
	}
	return 0
}
```

- [ ] **Step 4: Run tests to verify they pass** — `go test ./internal/auth/ ./internal/store/` → PASS.
- [ ] **Step 5: Commit** — `git add backend/internal/auth/password.go backend/internal/auth/password_test.go backend/internal/store/users.go backend/internal/store/users_test.go backend/go.mod backend/go.sum && git commit -m "feat(backend): argon2id password hashing + users CRUD + uid allocation"`

---

### Task 3: Bootstrap admin from env

**Files:**
- Create: `backend/internal/store/bootstrap.go`, `backend/internal/store/bootstrap_test.go`

**Interfaces:**
- Produces: `store.BootstrapAdmin(db *DB, username, password string) error` — if no admin exists, hash the password and create a `role='admin'` user with `must_change_password=1`. No-op if an admin already exists.

- [ ] **Step 1: Write the failing test**

`backend/internal/store/bootstrap_test.go`:
```go
package store

import (
	"path/filepath"
	"testing"

	"github.com/ldm0206/claude-docker/backend/internal/auth"
)

func TestBootstrapAdminCreatesFirst(t *testing.T) {
	db, _ := Open(filepath.Join(t.TempDir(), "t.db"))
	defer db.Close()
	if err := BootstrapAdmin(db, "root", "initialpw", auth.HashPassword); err != nil {
		t.Fatalf("bootstrap: %v", err)
	}
	u, err := db.GetUserByUsername("root")
	if err != nil || u.Role != "admin" || !u.MustChangePassword {
		t.Fatalf("admin not created correctly: %+v %v", u, err)
	}
	if !auth.CheckPassword("initialpw", u.PasswordHash) {
		t.Fatal("bootstrap password hash mismatch")
	}
	// idempotent: a second call must not create a duplicate / overwrite
	uidBefore := u.UID
	if err := BootstrapAdmin(db, "root", "different", auth.HashPassword); err != nil {
		t.Fatalf("second bootstrap: %v", err)
	}
	u2, _ := db.GetUserByUsername("root")
	if u2.UID != uidBefore || !auth.CheckPassword("initialpw", u2.PasswordHash) {
		t.Fatal("second bootstrap must not alter the existing admin")
	}
}
```

- [ ] **Step 2: Run test to verify it fails** — `go test ./internal/store/` → FAIL.
- [ ] **Step 3: Write minimal implementation**

`backend/internal/store/bootstrap.go`:
```go
package store

import "errors"

type HashFunc func(string) (string, error)

// BootstrapAdmin creates the first admin (must_change_password=1) if none exist.
// No-op when an admin already exists.
func BootstrapAdmin(db *DB, username, password string, hash HashFunc) error {
	if username == "" || password == "" {
		return errors.New("bootstrap admin requires username and password")
	}
	var exists int
	if err := db.sql.QueryRow("SELECT count(*) FROM users WHERE role = 'admin'").Scan(&exists); err != nil {
		return err
	}
	if exists > 0 {
		return nil
	}
	hashed, err := hash(password)
	if err != nil {
		return err
	}
	uid, err := db.AllocateUID()
	if err != nil {
		return err
	}
	_, err = db.CreateUser(User{
		UID: uid, Username: username, PasswordHash: hashed,
		Role: "admin", MustChangePassword: true,
	})
	return err
}
```

- [ ] **Step 4: Run test to verify it passes** — `go test ./internal/store/` → PASS.
- [ ] **Step 5: Commit** — `git add backend/internal/store/bootstrap.go backend/internal/store/bootstrap_test.go && git commit -m "feat(backend): bootstrap admin from env on empty DB"`

---

### Task 4: Config refactor — drop ACCESS_KEY, add bootstrap env

**Files:**
- Modify: `backend/internal/config/config.go`, `backend/internal/config/config_test.go`

**Interfaces:**
- `Config` changes: drop `AccessKey`; add `BootstrapAdminUser`, `BootstrapAdminPassword string`. `SessionSecret`, `Port`, `APITimeoutMS`, `NoProxy`, `Anthropic*`, proxies remain. `Load` no longer errors on missing `ACCESS_KEY`; it still sets defaults.

- [ ] **Step 1: Update the failing test** — replace any `ACCESS_KEY`-based assertions. `config_test.go` must now assert: `Load` succeeds with only `SESSION_SECRET` (no `ACCESS_KEY`); `BootstrapAdminUser`/`Password` are read when present; defaults unchanged.

```go
func TestLoadNoAccessKeyRequired(t *testing.T) {
	c, err := Load(envOf(map[string]string{"SESSION_SECRET": "s"}))
	if err != nil {
		t.Fatalf("Load must not require ACCESS_KEY: %v", err)
	}
	if c.SessionSecret != "s" {
		t.Fatalf("session secret not read")
	}
}
func TestLoadBootstrap(t *testing.T) {
	c, _ := Load(envOf(map[string]string{
		"SESSION_SECRET": "s", "BOOTSTRAP_ADMIN_USER": "root", "BOOTSTRAP_ADMIN_PASSWORD": "p",
	}))
	if c.BootstrapAdminUser != "root" || c.BootstrapAdminPassword != "p" {
		t.Fatalf("bootstrap env not read: %+v", c)
	}
}
```

- [ ] **Step 2: Run test to verify it fails** — `go test ./internal/config/` → FAIL (old struct still has AccessKey / new fields absent).
- [ ] **Step 3: Write minimal implementation** — edit `config.go`: remove the `ACCESS_KEY` block (and its required-error); add fields + `opt("BOOTSTRAP_ADMIN_USER")` / `opt("BOOTSTRAP_ADMIN_PASSWORD")`. Keep everything else.
- [ ] **Step 4: Run test to verify it passes** — `go test ./internal/config/` → PASS. Then `go build ./...` (will fail in `server.go` referencing `cfg.AccessKey` — fixed in Task 5; do NOT commit a broken build. Either commit config alone is impossible because server.go breaks. So implement Task 5's server changes that remove AccessKey usage in the SAME commit window, OR temporarily stub. Simplest: do Task 4 + Task 5 together as one logical change before committing. For TDD isolation, write config_test + config now, run `go test ./internal/config/` (passes), then proceed to Task 5 before running `go build ./...`.)
- [ ] **Step 5: Commit** — defer the commit until Task 5 lands (so the tree builds). Commit message: `refactor(backend): drop ACCESS_KEY, add bootstrap admin env + username/password auth` (covers T4+T5).

---

### Task 5: Auth refactor — username/password login + change-password + identity in session

**Files:**
- Modify: `backend/internal/server/server.go`
- Create: `backend/internal/server/auth_handler.go`
- Modify: `backend/internal/server/server_test.go`, `backend/internal/pty/env.go` (only if it referenced `cfg.AccessKey` — it does not; leave env.go for Task 7)

**Interfaces:**
- `server.New(cfg, db *store.DB)`; session payload `{username string, role string, iat int64}`.
- `POST /auth` `{username, password}` → 401 if bad credentials or `suspended`; on success sets cookie, returns `{role, mustChangePassword}`.
- `POST /auth/change-password` `{newPassword}` (authed) → re-hash, `SetPassword`, clear `must_change_password`.
- `authMiddleware` now also rejects `suspended` users and stashes `{username, role}` in `context.Context` via `server.WithIdentity`/`server.IdentityFrom(ctx)`.
- Admin gate helper `server.requireAdmin` (used by Task 8).

- [ ] **Step 1: Write the failing tests** — in `server_test.go`, replace the ACCESS_KEY tests with:
  - wrong password → 401.
  - correct password → 200, `Set-Cookie: session=...`, body has `role`.
  - `/api/state` with the cookie → 200; without → 401.
  - `/auth/change-password` without cookie → 401.
  Build a `*store.DB` on a temp file, bootstrap or create a user directly, construct `New(cfg, db)`.

```go
func newTestServer(t *testing.T) *Server {
	db, _ := store.Open(filepath.Join(t.TempDir(), "t.db"))
	t.Cleanup(func() { db.Close() })
	h, _ := auth.HashPassword("pw123")
	u, _ := db.CreateUser(store.User{UID: mustUID(db), Username: "alice", PasswordHash: h, Role: "user", CreatedAt: 1})
	_ = u
	cfg := &config.Config{SessionSecret: "s", Port: 0}
	return New(cfg, db)
}
```
(Add a tiny `mustUID` helper using `db.AllocateUID()` with a fatal on error.)

- [ ] **Step 2: Run tests to verify they fail** — `go test ./internal/server/` → FAIL (`New` signature changed, handlers missing).
- [ ] **Step 3: Write minimal implementation**

`backend/internal/server/auth_handler.go`:
```go
package server

import (
	"context"
	"encoding/json"
	"net/http"
	"time"

	"github.com/ldm0206/claude-docker/backend/internal/auth"
	"github.com/ldm0206/claude-docker/backend/internal/store"
)

type ctxKey int

const identityKey ctxKey = 0

type Identity struct {
	Username string
	Role     string
}

func IdentityFrom(ctx context.Context) (Identity, bool) {
	v, ok := ctx.Value(identityKey).(Identity)
	return v, ok
}

type loginReq struct {
	Username, Password string
}

func (s *Server) handleLogin(w http.ResponseWriter, r *http.Request) {
	var b loginReq
	if err := json.NewDecoder(r.Body).Decode(&b); err != nil {
		writeJSON(w, 400, map[string]any{"error": "bad body"})
		return
	}
	u, err := s.db.GetUserByUsername(b.Username)
	if errors.Is(err, store.ErrNotFound) || !auth.CheckPassword(b.Password, u.PasswordHash) {
		writeJSON(w, 401, map[string]any{"error": "unauthorized"})
		return
	}
	if u.Suspended {
		writeJSON(w, 403, map[string]any{"error": "suspended"})
		return
	}
	_ = s.db.TouchLogin(u.ID, time.Now().Unix())
	cookie, _ := auth.SignSession(map[string]any{"username": u.Username, "role": u.Role, "iat": time.Now().Unix()}, s.cfg.SessionSecret)
	http.SetCookie(w, &http.Cookie{Name: "session", Value: cookie, Path: "/", HttpOnly: true, SameSite: http.SameSiteLaxMode})
	writeJSON(w, 200, map[string]any{"role": u.Role, "mustChangePassword": u.MustChangePassword})
}

type changePwReq struct{ NewPassword string }

func (s *Server) handleChangePassword(w http.ResponseWriter, r *http.Request) {
	id, _ := IdentityFrom(r.Context())
	u, err := s.db.GetUserByUsername(id.Username)
	if err != nil {
		writeJSON(w, 500, map[string]any{"error": "user gone"})
		return
	}
	var b changePwReq
	if err := json.NewDecoder(r.Body).Decode(&b); err != nil || b.NewPassword == "" {
		writeJSON(w, 400, map[string]any{"error": "bad body"})
		return
	}
	hash, err := auth.HashPassword(b.NewPassword)
	if err != nil {
		writeJSON(w, 500, map[string]any{"error": "hash failed"})
		return
	}
	if err := s.db.SetPassword(u.ID, hash); err != nil {
		writeJSON(w, 500, map[string]any{"error": "persist failed"})
		return
	}
	writeJSON(w, 200, map[string]any{"ok": true})
}

func (s *Server) authedIdentity(r *http.Request) (Identity, bool) {
	c, err := r.Cookie("session")
	if err != nil {
		return Identity{}, false
	}
	claims, ok := auth.VerifySession(c.Value, s.cfg.SessionSecret)
	if !ok {
		return Identity{}, false
	}
	uname, _ := claims["username"].(string)
	role, _ := claims["role"].(string)
	if uname == "" {
		return Identity{}, false
	}
	return Identity{Username: uname, Role: role}, true
}
```

Update `server.go`:
- `New(cfg *config.Config, db *store.DB) *Server` (store the `db`).
- Replace `authed(r) bool` usages with `authedIdentity(r)`.
- `authMiddleware`: call `authedIdentity`; if missing → 401; else `next.ServeHTTP(w, r.WithContext(context.WithValue(r.Context(), identityKey, id)))`.
- Routes: `POST /auth` → `handleLogin`; `POST /auth/change-password` (authed group); keep `/logout`, `/api/state`, `/api/session/restart`, capture stubs.
- Add `requireAdmin` middleware: `if id.Role != "admin" { 403 }`.
- Remove the old `handleAuth` (ACCESS_KEY) and `authReq`.

(`errors` import: add `"errors"` to `auth_handler.go`.)

- [ ] **Step 4: Run tests to verify they pass** — `go test ./internal/server/ ./internal/config/` → PASS; `go build ./...` → clean.
- [ ] **Step 5: Commit** — `git add backend/internal/config/ backend/internal/server/ && git commit -m "refactor(backend): drop ACCESS_KEY, add bootstrap env + username/password auth"`

---

### Task 6: Linux account lifecycle + directory provisioning  (LINUX-ONLY)

**Files:**
- Create: `backend/internal/system/account.go`, `backend/internal/system/dirs.go`, `backend/internal/system/account_test.go`

**Interfaces:**
- `system.CreateUserAccount(username string, uid int) error` — `useradd -M -u <uid> -s /bin/bash <username>` (no `-m`; dirs provisioned explicitly).
- `system.ProvisionUserDirs(username string, uid int) error` — create `/home/<username>` (root:root 0755), `/home/<username>/workspace` (uid:uid 0700), `/data/<username>/claude-config` (uid:uid 0700).
- `system.DeleteUserAccount(username string) error` — `userdel <username>` + `rm -rf /home/<username>` + `rm -rf /data/<username>`.
- `system.LockUserAccount(username string) error` / `UnlockUserAccount` — `usermod -L` / `-U`.
- Username validated before use.

- [ ] **Step 1: Write the linux-only test**

`backend/internal/system/account_test.go`:
```go
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
	dataRoot := filepath.Join(tmp, "data")
	// override package roots for test (see dirs.go)
	if err := provisionDirs(homeRoot, dataRoot, "bob", 2001); err != nil {
		t.Fatalf("provision: %v", err)
	}
	for _, p := range []string{
		filepath.Join(homeRoot, "bob"),
		filepath.Join(homeRoot, "bob", "workspace"),
		filepath.Join(dataRoot, "bob", "claude-config"),
	} {
		if _, err := os.Stat(p); err != nil {
			t.Fatalf("expected %s: %v", p, err)
		}
	}
}
```

- [ ] **Step 2: Run (linux only)** — `docker run --rm -v "$(pwd)/backend:/src" -w /src golang:1.23 go test ./internal/system/` on a Docker-capable host; on Windows, compile-check only: `GOOS=linux go test -c -o /tmp/sys ./internal/system/` (must exit 0). Expected: PASS on Linux; compiles on Windows.
- [ ] **Step 3: Write minimal implementation**

`backend/internal/system/dirs.go`:
```go
package system

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
)

var usernameRe = regexp.MustCompile(`^[a-z_][a-z0-9_-]{1,31}$`)

// HomeRoot and DataRoot default to the container layout; overridable for tests.
var (
	HomeRoot = "/home"
	DataRoot = "/data"
)

func validateUsername(name string) error {
	if !usernameRe.MatchString(name) {
		return fmt.Errorf("invalid username %q", name)
	}
	return nil
}

func provisionDirs(homeRoot, dataRoot, username string, uid int) error {
	home := filepath.Join(homeRoot, username)
	if err := os.MkdirAll(filepath.Join(home, "workspace"), 0o700); err != nil {
		return fmt.Errorf("mkdir workspace: %w", err)
	}
	// chroot root must be root-owned 0755; workspace owned by the user
	if err := os.Chmod(home, 0o755); err != nil {
		return err
	}
	if err := os.Chown(home, 0, 0); err != nil {
		return err
	}
	if err := os.Chown(filepath.Join(home, "workspace"), uid, uid); err != nil {
		return err
	}
	cfg := filepath.Join(dataRoot, username, "claude-config")
	if err := os.MkdirAll(cfg, 0o700); err != nil {
		return fmt.Errorf("mkdir claude-config: %w", err)
	}
	return os.Chown(cfg, uid, uid)
}

func ProvisionUserDirs(username string, uid int) error {
	if err := validateUsername(username); err != nil {
		return err
	}
	return provisionDirs(HomeRoot, DataRoot, username, uid)
}
```

`backend/internal/system/account.go`:
```go
package system

import (
	"fmt"
	"os/exec"
	"strconv"
)

func CreateUserAccount(username string, uid int) error {
	if err := validateUsername(username); err != nil {
		return err
	}
	out, err := exec.Command("useradd", "-M", "-u", strconv.Itoa(uid), "-s", "/bin/bash", username).CombinedOutput()
	if err != nil {
		return fmt.Errorf("useradd: %w: %s", err, out)
	}
	return nil
}

func DeleteUserAccount(username string) error {
	if err := validateUsername(username); err != nil {
		return err
	}
	if out, err := exec.Command("userdel", username).CombinedOutput(); err != nil {
		return fmt.Errorf("userdel: %w: %s", err, out)
	}
	_ = exec.Command("rm", "-rf", HomeRoot+"/"+username, DataRoot+"/"+username).Run()
	return nil
}

func LockUserAccount(username string) error {
	return runUsermod(username, "-L")
}
func UnlockUserAccount(username string) error {
	return runUsermod(username, "-U")
}
func runUsermod(username, flag string) error {
	if err := validateUsername(username); err != nil {
		return err
	}
	out, err := exec.Command("usermod", flag, username).CombinedOutput()
	if err != nil {
		return fmt.Errorf("usermod %s: %w: %s", flag, err, out)
	}
	return nil
}
```

- [ ] **Step 4: Verify** — `go build ./...` + `go vet ./...` clean on Windows; `GOOS=linux go test -c -o /tmp/sys ./internal/system/` exits 0. (Runtime GREEN deferred to Linux deploy.)
- [ ] **Step 5: Commit** — `git add backend/internal/system/ && git commit -m "feat(backend): Linux account lifecycle + per-user directory provisioning"`

---

### Task 7: Per-user PTY (gosu) + parameterized env builder  (LINUX-ONLY for runtime)

**Files:**
- Modify: `backend/internal/pty/env.go` (add `BuildUserEnv`), `backend/internal/pty/env_test.go`
- Modify: `backend/internal/pty/manager.go` (Options gains `Username`; spawns `gosu <username> bash -l` when set)
- Modify: `backend/internal/server/server.go` (construct PTY per-identity)

**Interfaces:**
- `pty.BuildUserEnv(cfg *config.Config, username, claudeConfigDir string, credEnv map[string]string) []string` — like `BuildClaudeEnv` but `HOME=/home/<username>`, `cwd`/`CLAUDE_CONFIG_DIR` parameterized, credential env (Plan 3) merged when present.
- `pty.Options` gains `Username string`; `Manager.Start()` spawns `gosu <Username> bash -l` (as root) when `Username != ""`, else the existing behavior.
- `server.New` keeps ONE shared PTY in this plan ONLY as a transitional shim — actually multi-session is Plan 3. For Plan 2, the terminal handler must spawn/attach a PTY **for the logged-in user**. Minimal approach for Plan 2: keep a single shared PTY but spawn it via `gosu` using the **first admin** (or the bootstrapped user) so the runtime path is exercised; full per-user/per-session multiplexing is Plan 3.

> ⚠️ Plan 2 deliberately keeps the single-shared-PTY shape from Plan 1 but runs it as the target user via gosu; the per-user, multi-session terminal manager is Plan 3. Do not build session multiplexing here (YAGNI).

- [ ] **Step 1: Write/update tests** — `env_test.go`: `BuildUserEnv` sets `HOME=/home/alice`, `CLAUDE_CONFIG_DIR=/data/alice/claude-config`, PATH prepends `/opt/claude/bin`, and merges a `credEnv` entry (e.g. `ANTHROPIC_AUTH_TOKEN=t`). Windows-runnable.
- [ ] **Step 2: Run test → fail.**
- [ ] **Step 3: Implement** — `BuildUserEnv` mirrors `BuildClaudeEnv` (dedup map, last-wins) but with parameterized `HOME`/`CLAUDE_CONFIG_DIR` and `claudeBin = "/opt/claude/bin"`. In `manager.go`, add `Username` to `Options`; in `Start()`, if `Username != ""`, run `exec.Command("gosu", Username, "bash", "-l")` instead of `exec.Command("bash")`.
- [ ] **Step 4: Run tests → pass** (`go test ./internal/pty/`); `go build ./...` clean. (gosu runtime deferred to Linux.)
- [ ] **Step 5: Commit** — `git add backend/internal/pty/ backend/internal/server/server.go && git commit -m "feat(backend): per-user gosu PTY + parameterized env builder"`

---

### Task 8: Admin user-management API

**Files:**
- Create: `backend/internal/server/admin_users.go`
- Modify: `backend/internal/server/server.go` (mount admin routes under `requireAdmin`)

**Interfaces:**
- `POST /api/admin/users` `{username, password, role}` → validates username, hashes password, allocates uid, `CreateUser` (DB), then `system.CreateUserAccount` + `system.ProvisionUserDirs`; on Linux failure, `DELETE` the DB row and return 500. Returns `{id, username, role}`.
- `GET /api/admin/users` → list (id, username, role, suspended).
- `DELETE /api/admin/users/:id` → `system.DeleteUserAccount` + DB delete.
- `POST /api/admin/users/:id/suspend` / `/unsuspend` → `system.Lock/Unlock` + `db.SetSuspended`.

- [ ] **Step 1: Write tests** — Windows-runnable parts via a seam: inject the account provisioner as an interface (`system.AccountProvisioner`) so the handler is testable with a fake on Windows. Assert: admin create → 201 + DB row present (with fake provisioner); non-admin → 403; missing field → 400; invalid username → 400.
- [ ] **Step 2: Run → fail.**
- [ ] **Step 3: Implement** the handlers + an `AccountProvisioner` interface in `system` (real impl calls useradd; a fake in tests). Mount under `r.Group(func(r){ r.Use(s.authMiddleware); r.Use(s.requireAdmin); ... })`.
- [ ] **Step 4: Run → pass**; `go build ./...` clean.
- [ ] **Step 5: Commit** — `git add backend/internal/server/admin_users.go backend/internal/server/server.go backend/internal/system/account.go && git commit -m "feat(backend): admin user-management API (create/list/delete/suspend)"`

---

### Task 9: Root-run server + claude at /opt/claude/bin + Dockerfile/entrypoint  (LINUX-ONLY)

**Files:**
- Modify: `entrypoint.sh`, `Dockerfile`, `backend/cmd/server/main.go`

**Changes:**
- `entrypoint.sh`: remove `gosu claude`; create `/data`, `/home`, `/workspace`; `CLAUDE_CONFIG_DIR` no longer global (per-user now); `exec /app/claude-docker` (as root).
- `Dockerfile` runtime stage: download claude binary to `/opt/claude/bin/claude` (owned root:root, 0755) — NOT `/home/claude/.local/bin`; drop the `USER claude` download block and the `gosu` dependency can stay (used at runtime to drop privileges per PTY). Add `screen`, `tmux` to apt. Run as root (no `USER claude` for the server).
- `main.go`: open the store at `${DATA_DIR:-/data}/app.db`, call `store.BootstrapAdmin(db, cfg.BootstrapAdminUser, cfg.BootstrapAdminPassword, auth.HashPassword)`, pass `db` to `server.New`.

- [ ] **Step 1: Apply the edits** (exact content below).
- [ ] **Step 2: Verify** — `cd backend && go build ./...` clean. Re-read Dockerfile/entrypoint for correctness (claude path, root run, screen/tmux added). **`docker compose build` + runtime test deferred to the user's Linux deploy.**

`entrypoint.sh`:
```bash
#!/usr/bin/env bash
set -euo pipefail
mkdir -p /workspace /data /home
chmod 0755 /home
exec /app/claude-docker
```

Dockerfile runtime stage (relevant changes — claude binary + apt):
```dockerfile
# runtime stage
FROM debian:bookworm-slim
ENV DEBIAN_FRONTEND=noninteractive DISABLE_AUTOUPDATER=1 DISABLE_UPDATES=1
RUN apt-get update && apt-get install -y --no-install-recommends \
        git ripgrep curl ca-certificates jq tini gosu sudo openssl screen tmux \
    && rm -rf /var/lib/apt/lists/*

# Download claude binary to a shared, root-owned path (used by ALL users).
RUN install -d -m 0755 /opt/claude/bin \
    && set -e; \
    LATEST=$(curl -fsSL https://downloads.claude.ai/claude-code-releases/latest); \
    MANIFEST=$(curl -fsSL "https://downloads.claude.ai/claude-code-releases/$LATEST/manifest.json"); \
    CHECKSUM=$(echo "$MANIFEST" | jq -r '.platforms["linux-x64"].checksum'); \
    curl -fsSL -o /tmp/claude-bin "https://downloads.claude.ai/claude-code-releases/$LATEST/linux-x64/claude"; \
    echo "$CHECKSUM  /tmp/claude-bin" | sha256sum -c; \
    chmod 0755 /tmp/claude-bin; \
    mv /tmp/claude-bin /opt/claude/bin/claude

WORKDIR /workspace
COPY --from=go-builder /out/claude-docker /app/claude-docker
COPY entrypoint.sh /entrypoint.sh
RUN chmod +x /entrypoint.sh
ENTRYPOINT ["/usr/bin/tini", "--", "/entrypoint.sh"]
EXPOSE 8080
```
(Server runs as root; no `USER claude`. `gosu` is used at runtime inside PTY spawns.)

- [ ] **Step 3: Commit** — `git add entrypoint.sh Dockerfile backend/cmd/server/main.go && git commit -m "build: root-run server, claude at /opt/claude/bin, add screen/tmux"`

---

## Self-Review (Plan 2 vs spec §3, §5)

- **Spec coverage:** Identity (§3) — users/login/first-login/bootstrap/roles ✓ (T1-T5, T8). Isolation (§5) — Linux accounts, dir layout, delete-purge, suspend ✓ (T6, T8, T9). Per-spec deviations documented: `uid` column added; single-shared-PTY retained pending Plan 3.
- **Placeholder scan:** No TBD/TODO. Linux-only steps state the deferral explicitly.
- **Type consistency:** `store.User`, `store.DB` methods, `auth.HashPassword/CheckPassword`, `server.Identity/IdentityFrom`, `system.AccountProvisioner` — names reused consistently.
- **Deferred to later plans:** credential presets + role templates (Plan 3, despite the table columns existing), multi-session per-user PTY (Plan 3), SFTP (Plan 4), quotas/traffic (Plan 4), capture (Plan 5), UI (Plan 6).

## Notes for later plans

- Plan 3 will replace Plan 2's transitional single-shared-PTY with the per-user, multi-session PTY manager and add credential presets + role templates.
- The `system.AccountProvisioner` interface (Task 8) is the seam that keeps admin handlers unit-testable on Windows while real provisioning runs on Linux.
