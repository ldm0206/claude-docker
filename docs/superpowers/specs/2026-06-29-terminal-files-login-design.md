# Plan 8 — Terminal Reliability, Web File Manager, Native Claude Login, UI Polish

**Date:** 2026-06-29
**Status:** Design
**Predecessors:** Plan 7 (merged `25f649b`). Builds on the multi-user Go platform.

## Goals

Three user-reported problems, plus a UI polish pass:

1. **Terminal always "disconnected"** — under the Cloudflare + nginx + HTTPS
   deployment, the WebSocket dies immediately on connect.
2. **FTP should be in-browser, not port-connected** — the Files view currently
   shows SFTP connection info for an external client (FileZilla/WinSCP on port
   22). Replace it with a full in-browser Web file manager.
3. **Claude Code login is not "fill in a token"** — it is a shell-local
   `claude login` (browser OAuth, credentials stored under the Claude config
   dir). Per-user config is already isolated; symlink it to `~/.claude` so the
   native login works per-user. Keep the credential-preset env injection as a
   fallback.
4. **UI polish** — full visual refinement across all views (terminal, files,
   traffic, admin).

## Non-goals

- New auth systems (the cookie + argon2id flow stays).
- Changing the per-user Linux isolation model (gosu PTY, cgroup, nftables).
- A new transfer protocol — the Web file manager is plain Go REST over the
  existing HTTP server.

---

## Part 1 — Terminal "disconnected" root cause + reliability

### Root cause analysis

The deployment is Cloudflare → nginx (TLS 443, `proxy_pass http://claude:8080`,
WS headers correctly set: `proxy_http_version 1.1`, `Upgrade`, `Connection
'upgrade'`, `proxy_read_timeout 300s`, `proxy_buffering off`) → container.
Cloudflare WebSocket is enabled. The nginx config is correct, so the failure is
**not** a missing WS passthrough.

The prime suspect is the **session cookie** (`backend/internal/server/auth_handler.go:84`):

```go
http.SetCookie(w, &http.Cookie{
    Name:     "session",
    Path:     "/",
    HttpOnly: true,
    SameSite: http.SameSiteLaxMode,
})
```

`SameSite=Lax` with **no `Secure`** attribute. On an HTTPS deployment:

- The WS handshake is a cross-origin request from the browser's perspective.
  Under `Lax`, the cookie is not reliably attached to the WS upgrade request.
- `authWSUser` (`terminal.go:42`) then fails → returns **401 before the WS
  upgrade** → the client sees an immediate close → `ws.onclose` fires → status
  reads "disconnected". This matches the reported "一连接就断" (disconnects the
  moment it connects).

Secondary gaps that turn a single dropped connection into a dead terminal:

- `web/src/terminal.js:62` `ws.onclose` only sets text "disconnected". No close
  code/reason surfaced, no reconnect.
- `backend/internal/server/terminal.go` sends no WS ping/keepalive. Long-idle
  connections are reaped by Cloudflare (~100s) / nginx (`proxy_read_timeout
  300s`).

### Changes

**A. Cookie attributes** (`auth_handler.go`):
- Add `Secure: true`.
- `SameSite`: make it config-driven. Default `None` for HTTPS deployments
  (requires `Secure`). A new config flag / env `COOKIE_SAMESITE` (`none` |
  `lax` | `strict`, default `none`) lets a local `http://localhost:8080` dev
  setup pick `lax` (since `Secure` cookies are dropped on plain http).
- `handleLogout` clears with matching attributes.

**B. Backend WS keepalive** (`terminal.go`):
- After upgrade, start a ticker that sends `{"type":"ping"}` every 30s
  (`coder/websocket` accepts writes from a goroutine). This keeps the
  connection alive through Cloudflare/nginx idle timeouts.
- On `c.Read` error, surface the close code via a final
  `{"type":"close","code":N,"reason":"…"}` if possible (best-effort; a hard
  close may not allow a final write).

**C. Frontend resilience** (`terminal.js`):
- `onclose`: parse `event.code` → human-readable reason (1006 abnormal /
  1008 policy / 1011 server error / normal). Surface in `#term-status`.
- **Auto-reconnect** with exponential backoff (1s → 2s → 4s → … capped at
  30s). Stop retrying after N consecutive auth failures (likely 401 → tell the
  user to re-login instead of looping).
- On the create/attach path, if the first WS fails immediately with what looks
  like an auth error, show an explicit "session expired, reload to sign in"
  hint.
- Respond to backend `ping` is optional (browser WS auto-acks protocol pings);
  the app-level ping just needs to not error the client.

**D. Docs** (`README.md`, `DEPLOY-TEST.md`):
- Note that HTTPS deployments require `Secure` cookies (now default) and that
  the nginx WS block shown by the user is correct.
- Cloudflare: WebSocket must stay enabled (already is).

---

## Part 2 — Web file manager (replaces SFTP port connection)

### Scope

A full in-browser file manager over `/home/<user>/workspace`:

- Browse (list with name/size/type/mtime), breadcrumbs.
- Upload (multipart, chunked for large files, drag-and-drop, progress).
- Download (streaming, with Range support for resume).
- Mkdir, rename/move, delete (recursive for dirs).
- In-browser text edit (lightweight editor; textarea + optional syntax
  highlighting via `highlight.js` loaded from the SPA bundle).

### SFTP removal

The embedded SSH/SFTP server (`backend/internal/ssh/`, port 22) is **removed**:

- Delete `backend/internal/ssh/` (server.go + tests).
- Remove its construction/`Start` from `backend/cmd/server/main.go`.
- Remove the `SFTP_PORT` env var and the `22` port mapping from
  `docker-compose.yml` + `.env.example`.
- Drop the SFTP chroot/setuid notes from `README.md` / `DEPLOY-TEST.md`.
- The Files view's SFTP-connection-info card is replaced by the file manager.

Rationale: the Web manager covers the use case in-browser; removing port 22
shrinks the attack surface and removes a Linux-only runtime dependency
(chroot/setuid wiring was a documented DEPLOY-TEST gap).

### Backend — new `/api/files/*` (go-chi, under `authMiddleware`)

| Method | Path | Body/Query | Purpose |
|---|---|---|---|
| GET | `/api/files/list` | `?path=` | List directory entries |
| GET | `/api/files/download` | `?path=` | Stream file (Range) |
| POST | `/api/files/upload` | multipart + `?path=` | Upload one/more files |
| POST | `/api/files/mkdir` | `{path}` | Create directory |
| POST | `/api/files/rename` | `{from,to}` | Rename / move |
| POST | `/api/files/edit` | `{path,content}` | Save text file |
| DELETE | `/api/files` | `?path=` | Delete file or recursive dir |

New package `backend/internal/files/` (pure helpers, unit-testable on Windows):

- `Resolve(workspaceRoot, rel string) (abs string, err error)` — `filepath.Clean`
  + `filepath.Join` + a `strings.HasPrefix(abs+sep, root+sep)` boundary check.
  Returns an error on escape (`..` outside root). This is the **only** path
  resolver; every handler calls it.
- `List(root, rel)` → entries. `Mkdir`, `Rename`, `Delete`, `SaveText`,
  `ReadStream`, `WriteStream` helpers.

Handlers live in `backend/internal/server/files_api.go`, mirroring the existing
handler style (resolve user → `workspaceRoot = /home/<user>/workspace` → call
helper).

### Security constraints

- **Path escape**: every operation goes through `files.Resolve`; out-of-root →
  400. **Symlinks inside the workspace that point outside the workspace root
  are refused** (return 400) rather than followed — this is the safe default
  and removes the escape vector a follow-with-boundary-check would still partly
  expose (e.g. a symlink into `/etc`). A symlink that resolves **inside** the
  workspace is allowed (normal use).
- **User isolation**: workspace root derived from the authenticated
  `Identity.Username`; one user cannot address another's path.
- **Admin**: admin operates on their own workspace by default (same rule). No
  root-filesystem browsing (the previous SFTP gave admins a root shell; that
  power is intentionally dropped — admins can `su`/shell via the terminal if
  needed).

### Quota / limits integration

- **Pre-upload disk check**: `quota.Service` disk usage; reject 507 if the
  upload would exceed the soft disk quota. This is the primary quota gate and
  is reliable (disk usage is per-user, measured by `du` on the user's home).
- **Traffic accounting — explicit bookkeeping required.** The existing
  nftables counters are **per-user-cgroup**, keyed to the user's gosu PTY
  process. The Go HTTP server runs in the **server's** cgroup, so file
  transfer bytes are **NOT** automatically attributed to the user by nftables.
  The implementation must add explicit accounting: increment the user's
  traffic row (`store.RecordTraffic` / the existing traffic store seam) for
  bytes uploaded and downloaded via `/api/files/*`. This is a hard requirement,
  not an open item — without it, file transfers bypass the monthly quota.
- Per-file cap **200 MB** (413 on violation) and a per-request timeout (reuse
  server timeouts) to bound abuse.

### Frontend — Files view rewrite

- New `web/src/files.js` (`mountFiles(root)`), replacing the connection-info
  card in `main.js` `viewFiles`.
- Layout: left file tree / breadcrumbs, right entry list. Drag-and-drop upload
  zone. Progress bars per upload. Context menu (rename/delete/download).
- Double-click a text file → modal editor (textarea; syntax highlight optional
  via `highlight.js`).
- Responsive: single column with collapsible tree on mobile.

---

## Part 3 — Native Claude Code login (shell-local, per-user isolated)

### Current state

`buildUserEnvFactory` (`server.go:103`) decrypts the user's bound credential
preset and injects `ANTHROPIC_AUTH_TOKEN` / `ANTHROPIC_API_KEY` / proxy pairs
into the PTY env. `CLAUDE_CONFIG_DIR` is set to `/data/<user>/claude-config`
(`pty/env.go:75`). `/data/<user>/claude-config` is created + chowned in
`system/dirs.go:44`.

### Target

Let users run `claude login` inside their terminal; Claude's native OAuth writes
credentials to the per-user isolated config dir. Credential-preset env injection
stays as a fallback (API-key / no-OAuth scenarios).

### Changes

**A. Symlink `~/.claude` → `/data/<user>/claude-config`** (`system/dirs.go`):
- In `provisionDirs`, after creating `/data/<user>/claude-config`, create a
  symlink `/home/<user>/.claude` → `/data/<user>/claude-config`.
- If `/home/<user>/.claude` already exists as a real dir/file, **skip** (do not
  clobber existing state — log a warning). This handles re-provisioning safely.
- Both the symlink AND `CLAUDE_CONFIG_DIR` env point at the same place
  (belt-and-suspenders): Claude reads `~/.claude` by default, the env var
  forces it, and both land on the persistent `/data/<user>/` path (decoupled
  from the SFTP/file-manager-visible `workspace`).

**B. OAuth flow (terminal-native, no container browser)**:
- `claude login` prints an authorization URL and waits.
- The container has no user-local browser, so the user **selects/copies the URL
  from the terminal** (xterm.js supports mouse selection) and opens it in
  **their own machine's browser**.
- On completing the OAuth flow there, Claude in the container receives the
  token and writes `/data/<user>/claude-config/.credentials.json`.
- No backend changes required for this path — it works because the terminal is
  a real PTY with network egress. The only backend work is the symlink (A).

**C. Credential preset kept**:
- `resolveCredEnv` / `buildUserEnvFactory` unchanged. Still injects env when a
  preset is bound.
- env injection and `claude login` credentials do not conflict by construction:
  if both exist, Claude's own precedence decides (typically env vars win). The
  user picks one mode. Document this in the UI/README.

**D. UI guidance** (`terminal.js` / `main.js`):
- First-entry hint in the terminal status area: "Run `claude login` to sign in
  to Claude Code" (dismissible, non-blocking). Pure nudge.

---

## Part 4 — UI polish (full pass)

Visual refinement across **all** views (terminal, files, traffic, admin: users
/ credentials / templates / captures). Scope:

- Unify spacing scale, border radii, shadows, focus rings in
  `web/src/styles.css`.
- Add loading skeletons + empty states for tables/lists.
- Smooth transitions on theme toggle, sidebar, modals.
- Improve the terminal panel chrome (tabs, status pill) and the new file
  manager.
- Keep the existing Claude-style light/dark/system theme tokens; refine the
  dark palette contrast.
- No new component framework — stay vanilla (existing `el()` helper pattern).

---

## Architecture summary

```
Single Go binary (root in-container):
  HTTP :8080
    REST  /api/files/*      ← NEW (Web file manager)
    REST  /api/...          (existing admin/users/credentials/templates/captures/sessions)
    WS    /ws/terminal      ← + keepalive ping, + close-code surfacing
    WS    /ws/captures, /ws/metrics
    SPA   (refined UI + new file manager view)
  SSH  :22                  ← REMOVED (SFTP deleted)
  Per-user:
    /home/<user>/workspace          (file manager root; SFTP chroot gone)
    /home/<user>/.claude  →  /data/<user>/claude-config   ← NEW symlink
    CLAUDE_CONFIG_DIR=/data/<user>/claude-config          (kept)
```

## Testing strategy

- **Go unit tests** (Windows-host, pure + httptest):
  - `files.Resolve` escape cases (`..`, absolute, symlink, nested).
  - `files.List/Mkdir/Rename/Delete/SaveText` against a temp dir.
  - `files_api.go` handlers via `httptest` + fake workspace (auth → resolve →
    op).
  - Cookie attributes: assert `Secure` + configured `SameSite` on `/auth`.
  - `system/dirs.go`: assert symlink creation + skip-when-exists.
- **Cross-compile**: `GOOS=linux go test -c ./...` (existing gate).
- **Frontend**: `npm run build` (existing gate). Manual UI review is on the
  user (dev host has no browser automation noted).
- **Runtime (user, on Linux/Docker)**: WS reconnect under Cloudflare+nginx,
  file upload/download quota accounting, `claude login` OAuth round-trip.
  Documented in DEPLOY-TEST.md additions.

## Open items (to resolve in the implementation plan)

1. Symlink policy on **re-provision** of an existing user: skip (chosen) vs.
   recreate. Skip is the default — never clobber existing user state.
2. Exact `highlight.js` integration shape for the text editor (load only
   common languages to keep the bundle small) — decide at implementation time.
