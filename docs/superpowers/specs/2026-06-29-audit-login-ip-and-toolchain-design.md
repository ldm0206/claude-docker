# Plan 9 — Login IP & Session Audit + Preinstalled Shell Toolchain

**Date:** 2026-06-29
**Status:** Design
**Predecessors:** Plan 8 (terminal/files/login/UI, on `main` at `3c2ad12`).

## Goals

Two requests from the admin/operator:

1. **Admin must see users' login IP and session records.** Currently the user
   table stores only `last_login_at` (no IP) and the session table stores no
   client IP. The admin has no audit trail of who logged in from where, or from
   which IP a live session originated.
2. **The user shell must come preinstalled with `claude code`, `npm`/node,
   `python`, and `git`.** Today the runtime image has `git` and the `claude`
   binary, but **no node/npm and no python** (node exists only in the web-builder
   stage). Users cannot run `npm`/`python3` in their terminal.

## Non-goals

- No new auth model (cookie/argon2 flow unchanged).
- No per-user node/python version managers (nvm/pyenv) — global system installs.
- No live IP geolocation or rate-limiting — just recording + display.
- Not changing the per-user isolation model.

---

## Part 1 — Login IP + session IP recording + Audit UI

### Data model (SQLite)

The schema (`backend/internal/store/schema.sql`) is applied via
`CREATE TABLE IF NOT EXISTS` with **no migration framework**. New tables are
safe to add; **adding columns to existing tables requires idempotent
`ALTER TABLE ADD COLUMN`** statements (SQLite errors on a duplicate column, so
we run each ALTER and ignore the "duplicate column" error). These ALTERs run in
`store.Open()` after the base schema, once per process start (cheap, idempotent).

1. **`users.last_login_ip TEXT`** (overwrites — most-recent login IP). New
   idempotent ALTER.
2. **`sessions.client_ip TEXT`** (the IP the session was created from). New
   idempotent ALTER.
3. **New `login_events` table** (the audit stream — every login attempt):
   ```sql
   CREATE TABLE IF NOT EXISTS login_events (
     id INTEGER PRIMARY KEY AUTOINCREMENT,
     user_id INTEGER NOT NULL,   -- 0 when the username does not exist
     username TEXT NOT NULL,     -- the attempted username (audit even if no row)
     ip TEXT,
     user_agent TEXT,
     success INTEGER NOT NULL,   -- 1 success / 0 failure
     at INTEGER NOT NULL
   );
   CREATE INDEX IF NOT EXISTS idx_login_events_at ON login_events(at DESC);
   ```
   `username` is stored alongside `user_id` so a failed login for a non-existent
   username is still auditable (user_id=0).

### IP extraction (trust model)

New helper `Server.clientIP(r *http.Request) string` in `server.go`:

```go
// Priority: CF-Connecting-IP (Cloudflare-injected, hard to forge) > X-Real-IP
// > first hop of X-Forwarded-For > RemoteAddr. Trust assumes the request really
// transited Cloudflare+nginx (the documented deployment). If the container's
// 8080 port were reachable directly from the public internet, a client could
// forge CF-Connecting-IP — DEPLOY-TEST notes that 8080 must stay private
// behind nginx.
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

### Write points

- **`handleLogin`** (`auth_handler.go`): write one `login_events` row for EVERY
  attempt (success AND failure — failures include wrong-password and
  no-such-user, for brute-force auditing). On success, also
  `TouchLogin(id, ts, ip)` to update `users.last_login_ip` + `last_login_at`.
  `user_agent` is truncated to 256 chars before storing.
- **`TouchLogin` signature changes**: `TouchLogin(id int, ts int64, ip string)`.
  All call sites updated.
- **Session creation**: `sessions.client_ip` is set when a session is created.
  The WS create path (`ensureSession`) and the REST create path
  (`handleCreateSession`) set `opts.ClientIP = s.clientIP(r)` before calling
  `sessions.Manager.Create`. The manager reads `opts.ClientIP` and passes it into
  `store.CreateSession` (new column on the `store.Session` struct). The attach
  path does NOT change the stored IP (only creation records origin). A new
  `ClientIP string` field is added to `pty.Options` (mirrors how `Cwd`/`Username`
  are threaded) — purely a transport for the value; the PTY itself ignores it.

### Admin API (admin-only)

- `GET /api/admin/login-events?limit=N` (default 100, max 500) → latest login
  events (`{id, user_id, username, ip, user_agent, success, at}`), newest first.
- `GET /api/admin/users` response gains `last_login_ip` + `last_login_at`.
- `GET /api/admin/users/:id/sessions` (existing, Plan 3 T6) response gains
  `client_ip` per session.

### Admin UI — new "Audit" nav item

Sidebar → Admin group gains **"Audit"**. The Audit view shows the login-events
stream as a table: **Time / User / IP / User-Agent / Result**. Failed rows are
styled red (`.pill.suspended`). Time shown in the browser's local TZ. The view
polls/refreshes on entry (no live WS push in v1 — a manual refresh button +
auto-refresh on mount is enough).

The existing Users page additionally shows each user's `last_login_ip` +
`last_login_at` in a new "Last login" column.

### Security

- All audit endpoints are admin-only (`requireAdmin`).
- IPs/UAs are admin-visible only; never exposed to regular users about others.
- `user_agent` truncated to 256 chars on write to bound table growth.
- `login_events` rows are append-only; no delete endpoint in v1 (a future admin
  "purge" can be added; out of scope here). The `at DESC` index keeps the common
  "latest N" query fast.

### Testing (Windows-host, pure-Go + httptest)

- `store` tests: idempotent ALTERs run cleanly on a fresh DB and on a DB that
  already has the columns (re-open); `login_events` insert/list; `users.last_login_ip`
  write; `sessions.client_ip` write.
- `clientIP` test: priority order (CF > X-Real-IP > XFF > RemoteAddr) and XFF
  first-hop extraction.
- `handleLogin` test: success writes a `success=1` event + updates
  `last_login_ip`; failure writes a `success=0` event with the attempted
  username (user_id 0 for unknown user).
- `GET /api/admin/login-events` admin-only (403 for non-admin, 200 for admin,
  newest-first).

---

## Part 2 — Preinstalled shell toolchain (node/npm + python)

### Current state (Dockerfile stage 3 runtime)

Already installed: `git ripgrep curl ca-certificates jq tini gosu sudo openssl
screen tmux nftables openssh-client`, plus the `claude` binary at
`/opt/claude/bin/claude`. **Missing: node/npm, python.**

### Changes (Dockerfile)

1. **Add python to the existing apt list:**
   `python3 python3-pip python3-venv` (Debian bookworm → python 3.11).
2. **Add NodeSource node 22 LTS** after the base apt block (NodeSource needs its
   own source setup):
   ```dockerfile
   RUN curl -fsSL https://deb.nodesource.com/setup_22.x | bash - \
       && apt-get install -y --no-install-recommends nodejs \
       && rm -rf /var/lib/apt/lists/*
   ```
   Installs `/usr/bin/node` + `/usr/bin/npm` (node 22, matching the
   `node:22-bookworm-slim` web-builder). All users get it via the system PATH.
3. **Fix a Plan 8 leftover:** `EXPOSE 8080 22` → `EXPOSE 8080` (SFTP/port 22 was
   removed in Plan 8; the EXPOSE line was not updated).

### User-shell availability

The gosu PTY env (`pty.BuildUserEnv`, Plan 8) sets `PATH` to include
`/opt/claude/bin` + the inherited system PATH, so `/usr/bin/node`,
`/usr/bin/npm`, `/usr/bin/python3`, `/usr/bin/git`, and `/opt/claude/bin/claude`
are all on the user's PATH with no per-user setup. After build, in any user
terminal: `node -v`, `npm -v`, `python3 --version`, `git --version`,
`claude --version` all resolve.

### Version policy

- node: NodeSource `setup_22.x` → tracks the latest node 22 LTS patch (rolling,
  not pinned to a minor). Matches the web-builder's `node:22`.
- python: Debian bookworm's `python3` (3.11), updated via apt.
- git: Debian bookworm's `git` (already installed).

### Testing / verification

This is image-layer work; **the Windows dev host cannot build the Docker
image** (no Docker/WSL). Verification:
- Dockerfile review (correct NodeSource URL, correct apt package names, EXPOSE
  fix).
- DEPLOY-TEST.md gains a step: after `docker compose build`, exec into the
  container and confirm `node -v && npm -v && python3 --version && git --version
  && claude --version` all print versions.
- No Go/JS unit tests for this part (pure image layer).

### Image impact

node (~60 MB) + python (~30 MB) enlarge the runtime image. Acceptable cost for a
full toolchain. No multi-stage sharing of node with the web-builder (different
base images; not worth the complexity).

---

## Architecture summary

```
SQLite (/data/app.db):
  users        + last_login_ip        (ALTER, idempotent)
  sessions     + client_ip            (ALTER, idempotent)
  login_events (NEW table)            audit stream, indexed on at DESC

HTTP:
  POST /auth                      → writes login_events (success+fail) + TouchLogin(ip)
  POST /api/sessions, /ws/terminal → sessions.client_ip set on create
  GET  /api/admin/login-events     → NEW (admin-only audit stream)
  GET  /api/admin/users            → + last_login_ip/last_login_at
  GET  /api/admin/users/:id/sessions → + client_ip per session

Dockerfile (stage 3 runtime):
  + python3 python3-pip python3-venv
  + NodeSource node 22
  EXPOSE 8080 (drop leftover 22)
```

## Open items (resolve in implementation plan)

1. Audit retention: no auto-purge in v1 (append-only). A row cap or
   time-based purge is a follow-up if the table grows. Documented as a known
   non-goal here.
