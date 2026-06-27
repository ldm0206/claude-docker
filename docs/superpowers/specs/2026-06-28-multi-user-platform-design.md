# Multi-User Claude Code Platform — Design Spec

- **Date:** 2026-06-28
- **Status:** Draft (pending user review)
- **Supersedes / extends:** `2026-06-24-docker-hosted-claude-code-design.md` (single-user)

## 1. Overview

Upgrade the existing single-user `claude-docker` into a **multi-user, isolated Claude Code platform** with an admin management panel. One container hosts many users; each gets an isolated Linux environment (home, workspace, Claude config), their own Claude credential binding, resource/traffic quotas, and persistent terminal sessions. The backend is rewritten in **Go** for a single lightweight static binary.

### Goals
- Multi-user with strong per-user isolation via Linux accounts + filesystem permissions.
- Admin-managed user lifecycle (create / edit / suspend / delete-with-purge).
- Reusable **credential presets** and **role/quota templates**.
- Persistent, detachable terminal sessions (close browser → session survives).
- Per-user SFTP access to workspace; admin gets a root shell.
- Per-user monthly traffic accounting + live throughput.
- Responsive Claude-style UI with light/dark themes.
- Lightweight: one Go binary, no Node runtime at serve time.

### Non-goals (this spec)
- SSO / OIDC, self-service signup.
- Hard kernel disk quotas (we do soft quotas).
- Workspace environment templates (prebuilt images / cloned repos).
- Session audit recording / replay.
- The legacy opt-in MITM request/response **capture** feature: retained conceptually as a future **admin-only** debug tool, but its multi-user redesign is deferred to a later spec. Not in v1.
- Batch user operations, CSV export.

## 2. Architecture

Single container, one Go binary running as **root** (uid 0). Root is required to `setuid` into per-user accounts — this is the unavoidable cost of filesystem-permission isolation (see §11 Security). The binary owns:

- **HTTP server** (port `${PORT:-8080}`): REST API + WebSocket (terminal, metrics) + serves the embedded SPA.
- **Embedded SSH/SFTP server** (port `${SFTP_PORT:-22}`): file transfer + admin shell. No separate `sshd` daemon.
- **Session manager**: pool of persistent PTYs keyed by `(user_id, session_id)`.
- **Quota monitor**: periodic `du` + cgroup v2 subgroups.
- **Traffic accounter**: nftables counters matched on per-user cgroups → monthly buckets + live throughput.
- **DB**: SQLite (`/data/app.db`) for all persistent state.

```
                 ┌──────────────────────── container (root) ───────────────────────┐
   browser ────► │ HTTP :8080 ──► Go binary ──► REST + WS(terminal/metrics) + SPA   │
   sftp client ─►│ SSH  :22   ──► Go binary ──► auth(DB) → chroot+setuid per user   │
                 │                       │                                         │
                 │   ┌────────────┐ ┌─────┴──────┐ ┌──────────┐ ┌──────────────┐  │
                 │   │ sessions   │ │ credential │ │ quota /  │ │ nft traffic  │  │
                 │   │ (PTY pool) │ │  presets   │ │ cgroup   │ │  accounting  │  │
                 │   └─────┬──────┘ └────────────┘ └──────────┘ └──────────────┘  │
                 │         │ gosu/setuid                                              │
                 │   ┌─────▼───────────────────────────────────────────────┐       │
                 │   │ per-user Linux acct:  /home/<u>/workspace (700)      │       │
                 │   │                       /data/<u>/claude-config (700)   │       │
                 │   └──────────────────────────────────────────────────────┘       │
                 │   SQLite /data/app.db   ·   claude /opt/claude/bin/claude        │
                 └─────────────────────────────────────────────────────────────────┘
```

## 3. Identity & Roles

- Two roles: **admin**, **user**.
- Admin-managed only; no self-registration.
- Login: username + password (argon2id hash in DB).
- **First-login forced password change**: `users.must_change_password` flag; UI blocks until changed.
- Bootstrap: on first start with empty `users` table, create one admin from `BOOTSTRAP_ADMIN_USER` / `BOOTSTRAP_ADMIN_PASSWORD` env (with `must_change_password=true`).
- Linux account name = platform username (validated to `^[a-z_][a-z0-9_-]{1,31}$`). Renaming a user is disabled (or triggers `usermod -l`, best-effort) to avoid the Linux-rename edge cases.

## 4. Data Model (SQLite)

```sql
users(
  id INTEGER PK,
  username TEXT UNIQUE NOT NULL,           -- valid linux username
  password_hash TEXT NOT NULL,             -- argon2id
  role TEXT NOT NULL CHECK(role IN ('admin','user')),
  must_change_password INTEGER NOT NULL DEFAULT 1,
  role_template_id INTEGER REFERENCES role_templates(id),
  credential_preset_id INTEGER REFERENCES credential_presets(id),
  suspended INTEGER NOT NULL DEFAULT 0,
  disk_quota_bytes INTEGER,                -- override; NULL ⇒ use template
  max_sessions INTEGER,                    -- override; NULL ⇒ use template
  created_at INTEGER, last_login_at INTEGER
)

credential_presets(
  id INTEGER PK,
  name TEXT NOT NULL,
  encrypted_blob BLOB NOT NULL,            -- AES-256-GCM JSON (see §8)
  note TEXT, created_at INTEGER
)

role_templates(
  id INTEGER PK,
  name TEXT NOT NULL,
  disk_quota_bytes INTEGER NOT NULL,
  cpu_quota TEXT NOT NULL,                 -- cgroup cpu.max, e.g. "50000 100000" or "max"
  memory_max_bytes INTEGER NOT NULL,       -- cgroup memory.max
  max_sessions INTEGER NOT NULL,
  permissions TEXT NOT NULL DEFAULT '{}',  -- JSON, future capability flags
  created_at INTEGER
)

sessions(
  id TEXT PK,                              -- uuid
  user_id INTEGER NOT NULL REFERENCES users(id),
  name TEXT,
  started_at INTEGER, last_seen_at INTEGER,
  alive INTEGER NOT NULL DEFAULT 0         -- mirror of in-memory PTY state
)

traffic(
  user_id INTEGER NOT NULL REFERENCES users(id),
  year_month TEXT NOT NULL,                -- "YYYY-MM"
  rx_bytes INTEGER NOT NULL DEFAULT 0,     -- inbound (download)
  tx_bytes INTEGER NOT NULL DEFAULT 0,     -- outbound (upload)
  PRIMARY KEY (user_id, year_month)
)

audit_log(
  id INTEGER PK,
  actor TEXT, action TEXT, target TEXT, detail TEXT, ts INTEGER
)
```

**Effective quota** for a user = per-user override if set, else the bound `role_template`'s value.

## 5. Per-User Environment & Isolation

Per user, on create:
1. Allocate uid from a counter starting at 2000.
2. `useradd -m -s /bin/bash <username>` (creates `/home/<username>`).
3. Provision layout (see below); chown user-writable parts to the user.
4. Write `~/.bashrc` adding `/opt/claude/bin` to PATH.

**Directory layout (per user):**
```
/home/<username>              root:root  0755   ← SFTP chroot root (NOT user-writable)
  workspace/                  <user>    0700    ← terminal cwd; SFTP target; user files
/data/<username>/claude-config <user>   0700    ← CLAUDE_CONFIG_DIR (outside chroot, SFTP-invisible)
```
- `HOME=/home/<username>`, `cwd=/home/<username>/workspace`, `CLAUDE_CONFIG_DIR=/data/<username>/claude-config` for the user's PTY.
- `/home/<username>` is root-owned (chroot requirement); user writes under `workspace/`. `.bashrc` is root-provisioned/read-only to the user.
- **claude binary** moves to `/opt/claude/bin/claude` (shared, read-only) — no longer under any user's home (the old `/home/claude/.local/bin` path was single-user-era).

**Delete (purge):** kill all sessions → `userdel -r <username>` (removes `/home/<username>`) → `rm -rf /data/<username>` → null credential/template bindings → `DELETE` user row → audit entry. UI requires a typed confirm (irreversible).

**Suspend:** set `suspended=1` → kill all sessions → `usermod -L` (lock Linux login) → block further HTTP login and SSH auth → audit entry. Reversible.

## 6. Session & Terminal Model

- The PTY manager holds multiple **named, persistent** PTYs per user, keyed by session id.
- **PTY survives WebSocket disconnect**: closing the browser tab only unsubscribes the WS; the PTY keeps running. Reconnecting to the same session id resumes output (detach/attach). This delivers "close browser → session not lost" and supports `screen`/`tmux` inside as a power-user extra.
- **Session cap**: `max_sessions` (effective quota) bounds the number of **alive** sessions (including detached-but-running ones); creating beyond the cap is rejected (409).
- **Admin force-offline**: `DELETE /api/admin/users/:id/sessions/:sid` kills one; `DELETE /api/admin/users/:id/sessions` kills all (used by suspend/disable).
- **Terminal process**: root spawns `gosu <username> bash -l` via `creack/pty`; env injects the user's decrypted Claude credential (§8) + `CLAUDE_CONFIG_DIR` + PATH. Claude is launched manually by the user (unchanged from current design).
- **Admin terminal**: when an admin opens a terminal, the PTY runs as **root** directly (no gosu) — admin has full container root.

Frontend terminal: xterm.js over `WS /ws/terminal?session=<id>`, with resize and a session-tab switcher.

## 7. Quotas

**Disk (soft):**
- Background loop every 60s runs `du -sb /home/<username>` per user; stores usage in memory and exposes via API/WS.
- Over `disk_quota_bytes`: panel row turns red + a banner is injected into the user's terminal. **No write-blocking by default** (blocking toggle deferred). Soft, app-level — works on any Docker storage driver.

**CPU / Memory (cgroup v2 subgroups):**
- Server creates `/sys/fs/cgroup/cu-<id>/`, writes `cpu.max` and `memory.max` from the effective quota, and writes the PTY pid into `cgroup.procs`.
- Requires cgroup v2 + writable cgroupfs (Docker default). **Graceful fallback**: if cgroup writes fail, log + skip CPU/mem limiting; disk soft quota still applies.
- Aggregate container CPU/mem/net metrics (existing readers) still shown to admin as container totals.

## 8. Credential Presets & Encryption

- Admin creates a **credential preset**: a named bundle of `ANTHROPIC_API_KEY` / `ANTHROPIC_AUTH_TOKEN` / `ANTHROPIC_BASE_URL` / `HTTP_PROXY` / `HTTPS_PROXY` / `ALL_PROXY`.
- Encrypted with **AES-256-GCM** using `MASTER_KEY` (env, not stored) → `credential_presets.encrypted_blob`.
- **Binding**: `users.credential_preset_id` points a user at a preset. A preset can bind many users (reuse).
- **At runtime**: when spawning the user's PTY, the server decrypts the bound preset and injects the values into the PTY env. Plaintext exists only in the process env, never on disk.
- **Rotation**: editing a preset takes effect on the user's next (re)started session.
- Session cookies are HMAC-signed with a separate `SESSION_SECRET` (env).

## 9. Traffic Accounting

- **Mechanism**: nftables rules with `counter` matched on each user's cgroup (`socket cgroupv2 level 2 path`), giving per-user byte counts in both directions. Requires `CAP_NET_ADMIN` (added in compose; not privileged).
- **Sampling**: a background loop reads counter deltas (e.g. every 5s), accumulates into `traffic` table keyed by `(user_id, current YYYY-MM)`, and feeds a live throughput (bytes/s) ring buffer.
- **Monthly view**: panel shows per-user (admin) / self (user) up/down (tx/rx) for the selected month; admin can reset a user's current-month counter (`POST /api/admin/users/:id/reset-traffic`) — zeroes the row.
- **Live throughput**: exposed on `/ws/metrics` alongside CPU/mem, as bytes/s up/down.

## 10. SSH / SFTP (embedded)

- Embedded Go SSH server (`gliderlabs/ssh` + `pkg/sftp`) on `${SFTP_PORT:-22}`. No external `sshd`.
- **Auth**: `PasswordHandler` / `PublicKeyHandler` verify against the DB (argon2id) and the `suspended` flag. No Linux shadow password syncing needed.
- **Regular user**: after auth, the connection is confined to its workspace via chroot to `/home/<username>` and a per-session setuid to the user's uid; only the SFTP subsystem is allowed (no shell). Implemented by serving the SFTP handler as the target user (child-process setuid pattern) so all filesystem ops carry the user's permissions.
- **Admin**: full interactive shell (PTY as root) + unrestricted SFTP; in the `sudo`/root context, no chroot.
- Connection info (host:port, username, protocol SFTP) shown per-user in the admin panel and on the user's Files page.

## 11. Security Posture & Threat Model

| Aspect | Posture |
|---|---|
| Server process | Runs as **root** inside container. Required: per-user isolation needs `setuid`, which only root (or `CAP_SETUID`) can do. Container remains the isolation boundary. |
| Capabilities | `cap_add: [NET_ADMIN]` only (for nftables traffic accounting). No `--privileged`, no Docker socket. |
| Credentials | Encrypted at rest (AES-256-GCM, `MASTER_KEY` env). Plaintext only in PTY env. |
| Auth | argon2id passwords; signed httpOnly cookies; CSRF token on state-changing requests. |
| Inter-user isolation | Filesystem 0700 + separate uids; SFTP chrooted. |
| Network egress | Same as today; outbound proxy optional via env. **Front with TLS** (Caddy/nginx/Cloudflare) for remote use. |
| Changed from before | README drops the "non-root" claim (root is now required and documented); adds NET_ADMIN; documents the multi-user threat model. |

Risks: root-in-container escape mitigated by no privileged caps / no socket; strongest isolation would need per-user containers (rejected for ops simplicity). Suspending/quota features limit blast radius of a misbehaving user.

## 12. HTTP / WS API Surface

Auth (cookie session):
- `POST /auth/login` `{username,password}` → `{role, mustChangePassword}`; sets cookie.
- `POST /auth/change-password` `{newPassword}`.
- `POST /auth/logout`.

Self (`user` and `admin`):
- `GET /api/me`, `GET /api/state`.
- `POST /api/sessions` `{name}`, `DELETE /api/sessions/:id`, `GET /api/sessions`.
- `WS  /ws/terminal?session=<id>` — `{type: input|resize}` in, PTY data out; `{type: pty-exit}` on exit.
- `WS  /ws/metrics` — live cpu/mem/throughput (self) or aggregate.
- `GET /api/traffic?month=YYYY-MM`.

Admin (`/api/admin/*`, admin role only):
- Users: `GET`, `POST` `{username,password,role,role_template_id,credential_preset_id,...}`, `PATCH /:id`, `DELETE /:id` (purge), `POST /:id/suspend`, `POST /:id/unsuspend`, `POST /:id/reset-password` `{password}`, `POST /:id/reset-traffic`.
- Credential presets: `GET`, `POST`, `PATCH /:id`, `DELETE /:id`.
- Role templates: `GET`, `POST`, `PATCH /:id`, `DELETE /:id`.
- Sessions: `GET /users/:id/sessions`, `DELETE /users/:id/sessions/:sid`, `DELETE /users/:id/sessions`.
- `GET /api/admin/online` — who's connected, active sessions, live usage.
- `GET /api/admin/traffic?user=&month=`, `GET /api/admin/audit`.

## 13. Frontend / UI

- **Stack**: keep `web/` Vite SPA; xterm.js for terminal; minimal charting (CSS bars for traffic).
- **Layout**: left sidebar (Claude.ai-style). Sections: **Terminal/Sessions**, **Files**, **Traffic**; admin extras: **Users**, **Credentials**, **Templates**, **Audit**, **Settings**.
- **Screens**: terminal (multi-session tabs, persistent, resize); Files (workspace browser + SFTP connection info); Traffic (monthly bars + live meters); Admin Users (table + edit drawer, as mocked); Credentials & Templates CRUD; Audit log.
- **Themes**: light + dark + "follow system"; toggle in top bar (☀/🌙). Palette: warm cream (`#f5f1e8` light / `#1c1b1a` dark warm), clay-orange accent (`#c15f3c` / `#d97757`), rounded cards, subtle borders.
- **Responsive**: desktop (sidebar + main side-by-side); tablet (collapsible sidebar); mobile (drawer sidebar, single column, terminal-first, panels as bottom sheets).

## 14. Image, Dockerfile, Compose

**Multi-stage Dockerfile:**
- Stage 1 `builder` (`golang:1.22-bookworm`): compile the Go binary (CGO disabled → static, thanks to `modernc.org/sqlite`).
- Stage 2 runtime (`debian:bookworm-slim`): install runtime deps — `git ripgrep curl ca-certificates jq tini gosu openssl screen tmux nftables openssh-client` (git already present today; adds screen/tmux/nftables). Download claude binary to `/opt/claude/bin/claude`. Copy the Go binary. Entrypoint = `tini -- /entrypoint.sh` (root) → runs the single Go binary.
- SPA built in the builder and embedded via `embed.FS` (Go serves `/`).

**Compose:**
```yaml
services:
  claude:
    build: .
    ports: ["8080:8080", "22:22"]     # HTTP, SFTP/SSH (ports configurable)
    cap_add: ["NET_ADMIN"]            # nftables traffic accounting only
    env_file: .env
    volumes:
      - claude-home:/home
      - claude-data:/data
    restart: unless-stopped
volumes:
  claude-home:
  claude-data:
```
The Node `server/` directory is removed; `web/` stays.

## 15. Configuration (env)

| Var | Required | Purpose |
|---|---|---|
| `MASTER_KEY` | yes | AES-256-GCM key for credential presets. |
| `SESSION_SECRET` | yes | HMAC key for session cookies. |
| `BOOTSTRAP_ADMIN_USER` / `BOOTSTRAP_ADMIN_PASSWORD` | first run only | Seed initial admin (forces password change). |
| `PORT` | no (8080) | HTTP port. |
| `SFTP_PORT` | no (22) | SSH/SFTP port. |
| `DATA_DIR` / `HOME_ROOT` | no (/data, /home) | Storage roots. |
| `CLAUDE_BIN` | no (/opt/claude/bin/claude) | Claude binary path. |

## 16. Migration from Single-User

Breaking upgrade. The old `.env` (`ACCESS_KEY` + shared `ANTHROPIC_*`) flow is replaced by the admin panel + DB. On first run, bootstrap an admin via env, then configure users/credentials/templates in the panel. Existing `/workspace` volume data is not auto-migrated; an admin can move it into a chosen user's workspace via SFTP or the admin shell. Document this in the README.

## 17. Implementation Phases (overview — writing-plans will detail)

0. **Go scaffold + single-user parity**: project layout replacing `server/`; HTTP + WS terminal; cookie auth; serve embedded SPA; `creack/pty`.
1. **Identity**: users table, login, first-login change, bootstrap admin, sessions.
2. **Per-user isolation**: Linux account lifecycle, gosu PTY, dir layout, `/opt/claude/bin`.
3. **Persistent multi-session manager** + caps + admin kill.
4. **Credential presets + role/quota templates + binding** (AES-256-GCM).
5. **Embedded SFTP/SSH** (chroot, admin shell).
6. **Quotas** (disk soft + cgroup cpu/mem) + **traffic** (nft+cgroup, monthly, live) + suspend.
7. **Admin panel + responsive Claude UI + light/dark themes**.
8. **Hardening, README/compose rewrite, tests.**

## 18. Testing Strategy

- Unit: argon2id password flow, AES-GCM encrypt/decrypt, quota-effective resolution, traffic month bucketing, session-cap enforcement, config/env parsing. (Port the existing vitest spirit to Go tests.)
- Integration: create→login→change-password→spawn session→detach→resume→kill; SFTP auth + chroot confinement (user A cannot read user B); suspend blocks login; delete purges dirs.
- Manual/UI: terminal persistence across browser close, theme toggle, responsive breakpoints, admin CRUD flows, traffic meter sanity (transfer a known-size file, check counters).

## 19. Open Questions / Risks

- **cgroup v2 availability**: depends on host kernel + Docker cgroupfs writability. Mitigation: graceful fallback (disk-only) + deployment note.
- **nftables + NET_ADMIN**: some hardened hosts restrict even with the cap. Mitigation: detect failure at startup, degrade to "traffic unavailable" rather than crash.
- **Chroot + setuid SFTP correctness**: the trickiest piece; the plan must pick the child-process-vs-in-process approach and test confinement thoroughly.
- **claude binary path change** (`/home/claude/.local/bin` → `/opt/claude/bin`): verify the download/checksum flow still works and no volume shadows it.
