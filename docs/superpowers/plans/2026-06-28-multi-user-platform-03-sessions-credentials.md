# Multi-User Platform — Plan 3: Persistent Multi-Session PTY + Credential Presets + Role/Quota Templates

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Replace Plan 2's single-shared-PTY with a **per-user, persistent, multi-session** terminal manager (PTY survives WS disconnect; session cap per user; admin can list/kill), and add the **credential preset** (AES-256-GCM-encrypted Anthropic bundle, reusable, rotatable) and **role/quota template** (disk/cpu/mem/max-sessions/permissions, applied at user-create) data model + admin CRUD. After this plan: a user can hold multiple detachable sessions, their PTY env carries their bound Anthropic credential, and an admin binds credentials / templates to users.

**Architecture:** A new `internal/sessions` package owns a map keyed by `(username → sessionID → *pty.Manager)` with lazy env factories that read the user's live bound credential + quota. Three new store tables (`sessions`, `credential_presets`, `role_templates`) join `users` (whose `credential_preset_id`/`role_template_id`/`max_sessions`/`disk_quota_bytes` columns already exist from Plan 2). Credentials are encrypted with a `MASTER_KEY` (env) via AES-256-GCM and decrypted only into the PTY env at spawn time.

**Tech Stack:** Go 1.25 (`go.mod`), existing `modernc.org/sqlite`, `creack/pty`, `coder/websocket`, `golang.org/x/crypto` (argon2 already; adds `nacl/auth`? no — just `crypto/aes`+`crypto/cipher` GCM).

## Global Constraints

- Branch: `feat/plan-3-sessions-credentials`. Module `github.com/ldm0206/claude-docker/backend`, `CGO_ENABLED=0`, Dockerfile go-builder `golang:1.26-bookworm` (matches `go.mod` 1.25 — re-sync if `go mod tidy` bumps past 1.26).
- **Two test tiers (host = Windows, no Docker/WSL):**
  - **Windows-runnable** (pure Go + SQLite + httptest): store (templates/presets/sessions CRUD), AES-GCM encrypt/decrypt, session-manager logic with an injectable PTY-spawn seam, admin API. Full TDD.
  - **Linux-only** (real `creack/pty`/`gosu` spawn): the session manager wiring real PTYs; runtime deferred to deploy.
- `MASTER_KEY` env (32 raw bytes or a base64 string of 32 bytes) is REQUIRED at startup — fatal if missing/short. Used only for at-rest credential encryption; the cookie HMAC still uses `SESSION_SECRET`.
- YAGNI: this plan does NOT add quotas enforcement (Plan 4), capture (Plan 5), or UI (Plan 6). Templates STORE the quota fields; enforcement is Plan 4.
- Session cap = effective `max_sessions` (user override else template default); creating beyond the cap → 409. `max_sessions` counts ALIVE sessions (including detached).
- Credentials: plaintext exists only in the PTY env at spawn time; never persisted, never logged.
- DRY, YAGNI, TDD. Frequent commits. **Review subagents: use `haiku`** (user pref).

## File Structure (this plan adds/changes)

```
backend/
  internal/
    store/
      schema.sql            # APPEND: role_templates, credential_presets, sessions tables
      templates.go          # [NEW] RoleTemplate CRUD
      templates_test.go
      presets.go            # [NEW] CredentialPreset CRUD (encrypted_blob)
      presets_test.go
      sessions.go           # [NEW] session metadata CRUD (NOT the live PTY)
      sessions_test.go
      users.go              # MODIFY: BindCredential, BindTemplate, EffectiveMaxSessions, EffectiveDiskQuota
      users_test.go
    secrets/
      secrets.go            # [NEW] AES-256-GCM Encrypt/Decrypt with key derivation; MasterKey()
      secrets_test.go
    sessions/
      manager.go            # [NEW] per-user, multi-session PTY manager; owns map[username]map[sessionID]*pty.Manager
      manager_test.go       # injectable PTY factory → Windows-runnable
    server/
      server.go             # MODIFY: drop single shared PTY; hold *sessions.Manager; route changes
      terminal.go           # MODIFY: /ws/terminal now takes ?session=<id>; per-user multi-session attach/detach
      admin_sessions.go     # [NEW] GET /api/admin/users/:id/sessions, DELETE .../sessions/:sid, DELETE .../sessions
      admin_templates.go    # [NEW] /api/admin/templates CRUD
      admin_credentials.go  # [NEW] /api/admin/credentials CRUD
      sessions_api.go       # [NEW] user-side: POST /api/sessions (create, cap-checked), GET /api/sessions, DELETE /api/sessions/:id
      server_test.go        # MODIFY
  cmd/server/main.go        # MODIFY: load MASTER_KEY, pass to sessions.Manager; drop nothing else
```

---

### Task 1: schema — add role_templates, credential_presets, sessions tables

**Files:**
- Modify: `backend/internal/store/schema.sql`

**Interfaces:** adds 3 tables (CREATE TABLE IF NOT EXISTS, idempotent — existing `users` table unchanged, already has the FK columns from Plan 2).

- [ ] **Step 1: Append the three tables to `schema.sql`** (after the existing `users` block):

```sql
CREATE TABLE IF NOT EXISTS role_templates (
  id INTEGER PRIMARY KEY AUTOINCREMENT,
  name TEXT UNIQUE NOT NULL,
  disk_quota_bytes INTEGER NOT NULL,
  cpu_quota TEXT NOT NULL,
  memory_max_bytes INTEGER NOT NULL,
  max_sessions INTEGER NOT NULL,
  permissions TEXT NOT NULL DEFAULT '{}',
  created_at INTEGER NOT NULL
);

CREATE TABLE IF NOT EXISTS credential_presets (
  id INTEGER PRIMARY KEY AUTOINCREMENT,
  name TEXT UNIQUE NOT NULL,
  encrypted_blob BLOB NOT NULL,   -- AES-256-GCM JSON: {api_key,auth_token,base_url,http_proxy,https_proxy,all_proxy}
  note TEXT,
  created_at INTEGER NOT NULL
);

CREATE TABLE IF NOT EXISTS sessions (
  id TEXT PRIMARY KEY,            -- uuid-ish string, client-visible
  user_id INTEGER NOT NULL,
  name TEXT,
  started_at INTEGER NOT NULL,
  last_seen_at INTEGER NOT NULL,
  alive INTEGER NOT NULL DEFAULT 1
);
```

- [ ] **Step 2: Extend `store_test.go`** — add `TestSchemaCreatesAllTables` asserting `users`, `role_templates`, `credential_presets`, `sessions` all exist and are empty after `Open`. Run → RED (`sessions`/`role_templates`/`credential_presets` absent) → the Step-1 append makes it GREEN.
- [ ] **Step 3: Verify** `go test ./internal/store/` PASS; `go build ./...` clean.
- [ ] **Step 4: Commit** — `feat(backend): add role_templates/credential_presets/sessions schema`.

---

### Task 2: AES-256-GCM secrets + MasterKey

**Files:**
- Create: `backend/internal/secrets/secrets.go`, `backend/internal/secrets/secrets_test.go`

**Interfaces:**
- `secrets.MasterKey(get func(string)(string,bool)) ([]byte, error)` — reads `MASTER_KEY`; accepts a base64/std string of 32 bytes OR 32 raw bytes; errors if missing/short.
- `secrets.Encrypt(key, plaintext []byte) ([]byte, error)` — AES-256-GCM; output = `nonce(12) || ciphertext+tag`.
- `secrets.Decrypt(key, blob []byte) ([]byte, error)` — inverse; wrong key/short/corrupt → error (no panic).
- `secrets.SealJSON(key []byte, v any) ([]byte, error)` / `secrets.OpenJSON(key, blob []byte, dst any) error` — JSON marshal then Encrypt / Decrypt then Unmarshal.

- [ ] **Step 1: Tests** (Windows-runnable): `TestMasterKey` (base64 of 32 bytes OK; raw 32 OK; short → error; missing → error); `TestEncryptDecryptRoundTrip`; `TestDecryptWrongKey` (errors); `TestDecryptCorrupt` (truncated blob → error, no panic); `TestSealOpenJSON` round-trip of a struct.
- [ ] **Step 2: Run → RED.**
- [ ] **Step 3: Implement** — `crypto/aes`, `crypto/cipher` GCM, `crypto/rand` nonce, `encoding/base64`. `Encrypt` panics only on an invalid-length key (caller guarantees 32 via MasterKey); `Decrypt` returns error on any failure.
- [ ] **Step 4: Run → GREEN**; `go build ./...` + `go vet ./...` clean.
- [ ] **Step 5: Commit** — `feat(backend): AES-256-GCM secrets + MASTER_KEY loader`.

---

### Task 3: role_templates + credential_presets store CRUD

**Files:**
- Create: `backend/internal/store/templates.go`, `templates_test.go`, `presets.go`, `presets_test.go`
- Modify: `backend/internal/store/users.go` (Bind helpers), `users_test.go`

**Interfaces:**
- `store.RoleTemplate{ID,Name,DiskQuotaBytes,CPUQuota,MemoryMaxBytes,MaxSessions,Permissions string,CreatedAt int64}`.
- `(*DB) CreateTemplate(t RoleTemplate) (RoleTemplate, error)`, `GetTemplate(id int) (RoleTemplate, error)`, `ListTemplates() ([]RoleTemplate, error)`, `DeleteTemplate(id int) error`.
- `store.CredentialPreset{ID,Name,EncryptedBlob []byte,Note,CreatedAt}` (blob is already-encrypted at this layer — store is crypto-agnostic).
- `(*DB) CreatePreset(p CredentialPreset) (CredentialPreset, error)`, `GetPreset(id int) (CredentialPreset, error)`, `ListPresets() ([]CredentialPreset, error)`, `DeletePreset(id int) error`.
- `(*DB) BindCredential(userID, presetID int) error`, `BindTemplate(userID, templateID int) error`, `(*DB) EffectiveMaxSessions(userID int) (int, error)` (user.max_sessions override else template.max_sessions else default 3), `EffectiveDiskQuota(userID int) (int64, error)` (user override else template else 0).

- [ ] **Step 1: Tests** (Windows-runnable): template CRUD round-trip; preset CRUD stores/returns the blob bytes verbatim; `EffectiveMaxSessions` returns user override when set, else template's, else 3; `EffectiveDiskQuota` analogous with 0 default; Bind updates the user row.
- [ ] **Step 2: Run → RED.**
- [ ] **Step 3: Implement** all the above (parameterized SQL — no injection; blobs via `[]byte`).
- [ ] **Step 4: Run → GREEN**; build+vet clean.
- [ ] **Step 5: Commit** — `feat(backend): role_templates + credential_presets store CRUD + effective-quota resolution`.

---

### Task 4: session metadata store + sessions.Manager (with injectable PTY seam)

**Files:**
- Create: `backend/internal/store/sessions.go`, `sessions_test.go`
- Create: `backend/internal/sessions/manager.go`, `manager_test.go`

**Interfaces:**
- Store metadata: `store.Session{ID string, UserID int, Name, StartedAt, LastSeenAt int64, Alive bool}`. `(*DB) CreateSession(s Session) error` (UUID generated by caller), `GetSession(id string) (Session,error)`, `ListSessionsForUser(userID int) ([]Session,error)`, `TouchSession(id string, ts int64) error`, `MarkSessionExited(id string) error`, `DeleteSession(id string) error`.
- `sessions.Manager` — owns `map[username]map[sessionID]*pty.Manager` (mutex-guarded). Takes an injectable `PTYFactory func(opts pty.Options) *pty.Manager` so tests pass a fake (real = `pty.New`).
  - `Create(username string, userID int, envFactory sessions.EnvFactory) (sessionID string, err error)` — enforces cap via `db.EffectiveMaxSessions`; persists a `sessions` row (alive=1); spawns the PTY (not started yet — caller starts or the WS handler lazy-starts).
  - `Get(username, sessionID string) (*pty.Manager, bool)`
  - `List(username string) []SessionMeta`
  - `Kill(username, sessionID string) error` — Stop PTY + mark exited + (optionally) delete row.
  - `KillAll(username string)` — used by suspend/delete.
  - The `envFactory` is a `func() []string` resolved lazily by `(username)` so the PTY env reflects the live credential (decrypted at spawn).

> The session UUID: use `crypto/rand` → 16 bytes → base64url (no `Math.random`/uuid lib needed; deterministic enough and collision-safe).

- [ ] **Step 1: Tests** (Windows-runnable): store session CRUD round-trip; `ListSessionsForUser` filters by user; manager `Create` enforces cap (create maxSessions=2 sessions, 3rd → error); `Kill` stops the PTY (fake records Stop calls) + marks exited; `KillAll` kills all of a user. Use a fake PTY factory returning a stub `*pty.Manager` (or a minimal interface — if `*pty.Manager` is too concrete, define a tiny `sessions.PTY` interface with `Start/Stop/Write/Resize/OnData/OnExit/Alive` and have `pty.Manager` satisfy it; the fake satisfies it too).
- [ ] **Step 2: Run → RED.**
- [ ] **Step 3: Implement.** (If you introduce a `sessions.PTY` interface, put it in `sessions/manager.go` and ensure `pty.Manager` satisfies it via the existing method set — do NOT change `pty.Manager`'s public API.)
- [ ] **Step 4: Run → GREEN**; build+vet clean.
- [ ] **Step 5: Commit** — `feat(backend): session metadata store + per-user multi-session manager (injectable PTY)`.

---

### Task 5: wire sessions.Manager + MASTER_KEY into server/main; remove single-shared PTY

**Files:**
- Modify: `backend/internal/server/server.go` (drop `currentUser`/shared `pty.Manager`; hold `*sessions.Manager` + `masterKey`), `backend/internal/server/terminal.go` (`/ws/terminal?session=<id>` against the sessions.Manager), `backend/internal/server/metrics_ws.go`, `backend/internal/server/auth_handler.go` (login no longer swaps currentUser — sessions are per-request now), `backend/cmd/server/main.go` (load MASTER_KEY, build sessions.Manager, pass to New).
- Modify: `backend/internal/config/config.go` — `Config` keeps `SessionSecret` (cookie HMAC); MASTER_KEY is loaded directly in main (it's a secret, not config). Or add a `MasterKey` field — your call, but do NOT log it.

**Behavior:**
- `server.New(cfg, db, provisioner, sess *sessions.Manager)`.
- `/ws/terminal?session=<id>`: authWSUser → ensure the session belongs to the user (or create one if `session` omitted, respecting cap) → `mgr.Get(user, sid)` → lazy `Start()` (env factory decrypts the user's bound credential via the master key into `BuildUserEnv(... credEnv)`); subscribe onData/OnExit (exit → `MarkSessionExited`); on WS close, ONLY unsubscribe (PTY survives = detach). `?session=` omitted → `mgr.Create(...)` and redirect/return the new id.
- `/api/session/restart` is replaced by per-session kill+create OR kept as "kill the session named in the body" — simplest: `DELETE /api/sessions/:id` then the client creates a new one. Remove the old global `handleRestart` (or keep it as a no-op deprecated alias returning 410 Gone — your call; prefer removing and updating the SPA contract note).
- Capture stubs (`/api/capture/*`, `/ws/captures`) stay as inert stubs (Plan 5).

> ⚠️ This is a breaking change to the SPA terminal contract (query param `?session=`). The existing `web/src` opens `/ws/terminal` with NO query — so a single-session attach still works (handler creates a session when omitted). Multi-session UI is Plan 6; for Plan 3, the contract is "no query = create-or-attach-to-default".

- [ ] **Step 1: Update server tests** — adapt to `New(..., sess)`. Existing login/state tests still pass (login no longer sets currentUser; state's `sessionAlive` becomes "any alive session for any user" OR better: drop `sessionAlive` from `/api/state` and return `{captureOn:false}` — Plan 6 UI re-derives from `/api/sessions`). Pick the minimal change that keeps tests green and the build clean.
- [ ] **Step 2: Implement** the wiring. Inject a fake `sessions.Manager` (or real with fake PTY factory) in tests so Windows stays happy. Runtime real-PTY deferred.
- [ ] **Step 3: `go build ./...` + `go vet ./...` + `go test ./...` clean.**
- [ ] **Step 4: Commit** — `feat(backend): wire per-user multi-session manager; drop shared PTY`.

---

### Task 6: user-side session API + admin session management

**Files:**
- Create: `backend/internal/server/sessions_api.go`, `admin_sessions.go`
- Modify: `backend/internal/server/server.go` (routes)

**Endpoints:**
- User (authed): `POST /api/sessions {name}` → create (cap-checked, 409 if over), returns `{id,name}`; `GET /api/sessions` → list own; `DELETE /api/sessions/:id` → kill+delete own.
- Admin (requireAdmin): `GET /api/admin/users/:id/sessions`; `DELETE /api/admin/users/:id/sessions/:sid`; `DELETE /api/admin/users/:id/sessions` (kill all — used by suspend/delete).

- [ ] **Step 1: Tests** (Windows-runnable with fake PTY): user creates session → 200; over cap → 409; user A can't delete user B's session → 403/404; admin lists/kills another user's sessions.
- [ ] **Step 2: Run → RED.**
- [ ] **Step 3: Implement** + mount routes.
- [ ] **Step 4: Run → GREEN**; build+vet clean.
- [ ] **Step 5: Commit** — `feat(backend): user + admin session-management API`.

---

### Task 7: admin credential-preset + role-template CRUD (with AES-GCM)

**Files:**
- Create: `backend/internal/server/admin_templates.go`, `admin_credentials.go`
- Modify: `server.go` (routes), `admin_users.go` (create-user now accepts optional `role_template_id`/`credential_preset_id`; binds them).

**Endpoints:**
- `/api/admin/templates` GET/POST/PATCH/DELETE — CRUD over `role_templates` (plaintext fields).
- `/api/admin/credentials` GET (list returns id/name/note/created_at — NEVER the blob), POST (body `{name, api_key, auth_token, base_url, http_proxy, https_proxy, all_proxy, note}` → `secrets.SealJSON(masterKey, body)` → store encrypted_blob), PATCH (re-encrypt), DELETE.
- `POST /api/admin/users` extended: optional `role_template_id` + `credential_preset_id` → `BindTemplate`/`BindCredential`.

- [ ] **Step 1: Tests** (Windows-runnable): create preset → GET list does NOT include the secret fields; round-trip (admin can't read back the plaintext via the API — only id/name/note); template CRUD; create-user with template+preset binds them; `EffectiveMaxSessions` reflects the bound template.
- [ ] **Step 2: Run → RED.**
- [ ] **Step 3: Implement.** The handlers need `masterKey` (from `Server`). NEVER log or return decrypted credentials.
- [ ] **Step 4: Run → GREEN**; build+vet clean.
- [ ] **Step 5: Commit** — `feat(backend): admin credential-preset + role-template CRUD (AES-GCM)`.

---

### Task 8: per-user credential injection into PTY env

**Files:**
- Modify: `backend/internal/sessions/manager.go` (env factory decrypts the user's bound preset) — or wire in `server.go` if cleaner.

**Behavior:** when a session's PTY is (re)started, resolve the user's bound `credential_preset_id` → `db.GetPreset` → `secrets.OpenJSON(masterKey, blob, &creds)` → pass `creds` as `credEnv` to `pty.BuildUserEnv(cfg, username, claudeConfigDir, credEnv)`. If no preset bound, `credEnv=nil`. Decrypted creds live only in the PTY process env.

- [ ] **Step 1: Test** (Windows-runnable, fake PTY): a user with a bound preset → the env passed to the PTY factory contains `ANTHROPIC_AUTH_TOKEN=<decrypted>` (assert the fake PTY factory captured the env). A user without a preset → no ANTHROPIC_* from credEnv (only whatever's in os.Environ).
- [ ] **Step 2: Run → RED.**
- [ ] **Step 3: Implement** the env-factory wiring.
- [ ] **Step 4: Run → GREEN**; build+vet clean.
- [ ] **Step 5: Commit** — `feat(backend): inject per-user decrypted credentials into PTY env`.

---

### Task 9: main.go MASTER_KEY + final wiring + build verify

**Files:**
- Modify: `backend/cmd/server/main.go`, `backend/internal/config/config.go` (if MasterKey field added).

- [ ] **Step 1: main.go** — `masterKey, err := secrets.MasterKey(envLookup)`; fatal if err; pass to the sessions.Manager (or Server, whichever owns the env factory). Update `server.New` signature if needed.
- [ ] **Step 2: `go build ./...`, `go vet ./...`, `go test ./...`, `GOOS=linux go test -c ./internal/{sessions,pty,system}/` all clean.**
- [ ] **Step 3: Update `.env.example`** — add `MASTER_KEY=<32-byte base64>` and `BOOTSTRAP_ADMIN_USER`/`BOOTSTRAP_ADMIN_PASSWORD` (already used by Plan 2; document them).
- [ ] **Step 4: Commit** — `feat(backend): wire MASTER_KEY + sessions/credentials into main`.

---

## Self-Review (Plan 3 vs spec §6, §8)

- **Spec coverage:** Persistent multi-session PTY (§6) — detach/attach, cap, kill ✓ (T4-T6). Credential presets + binding + AES-GCM (§8) ✓ (T2-T3, T7-T8). Role/quota templates (§8) ✓ (T1, T3, T7). Per-session credential injection ✓ (T8).
- **Placeholder scan:** none; Linux-only steps state the deferral.
- **Type consistency:** `store.RoleTemplate`/`CredentialPreset`/`Session`, `secrets.MasterKey/Encrypt/Decrypt`, `sessions.Manager` — reused consistently.
- **Deferred:** quota enforcement (Plan 4), capture (Plan 5), UI/themes (Plan 6), hardening (Plan 7).

## Notes for later plans
- Plan 4 reads `EffectiveDiskQuota`/`EffectiveMaxSessions` + the template's `cpu_quota`/`memory_max_bytes` for cgroup enforcement.
- Plan 5 (capture) will add a per-session capture flag to the `sessions` row and the MITM proxy; the session manager already keys by session id.
- The `sessions.PTY` interface (if introduced in T4) is the seam Plan 5/6 extend.
