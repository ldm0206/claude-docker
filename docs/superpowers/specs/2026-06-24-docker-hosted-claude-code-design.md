# Docker-Hosted Claude Code — Design Spec

- **Date:** 2026-06-24
- **Status:** Approved (brainstorming phase)
- **Scope:** Single-container, single-user, browser-accessible Claude Code environment

## 1. Goals & Non-Goals

### Goals

1. Run Claude Code in an isolated Docker container so unrestricted (`bypassPermissions`) operation cannot touch the host system.
2. Configure a global outbound proxy (HTTP or SOCKS5) via environment variables to absorb the user's network instability.
3. Provide a browser web terminal (not a local Docker client) so a non-technical friend can be handed a URL and just start it.
4. Gate web access behind a single access key (needed because it is sometimes used on a company network).
5. Live container resource metrics (CPU %, memory used/limit, network RX/TX) shown in the web UI.
6. Optional, opt-in request/response capture for debugging the user's own Claude Code plugin/hook development — full capture with a container-local CA, off by default, with secret redaction.
7. Claude-styled web UI (cream background, serif display type, terracotta accent), matching the Claude Code aesthetic.

### Non-Goals

- Multi-user tenancy (single-user now, but directory layout is reserved for future expansion).
- Built-in TLS termination for the web service — the container exposes HTTP only; TLS is the caller's responsibility via a front proxy (Caddy / nginx / Cloudflare Tunnel).
- Host bind-mounted workspace (uses a Docker named volume instead).
- Intercepting or capturing traffic from processes other than the in-container Claude Code process.

## 2. Confirmed Decisions

| Decision | Choice |
|---|---|
| Scope | Full custom build (Node control plane), not ttyd MVP |
| Debug capture depth | Full proxy capture (request + response bodies) |
| HTTPS decryption | Container-local CA trusted only inside the container |
| User model | Single user / single deployment |
| Deployment shape | Single integrated container (web service + Claude Code together) |
| TLS | HTTP only, front proxy handles HTTPS |
| Workspace persistence | Docker named volume |
| Web terminal default | Interactive Claude Code REPL (`bypassPermissions`) |

## 3. Architecture

A single container runs two cooperating processes — a **Node control panel** and the **Claude Code process** it spawns. The control panel is the only network entry point.

```
Browser (Claude-styled SPA)
  ├─ xterm.js terminal pane ──────────── WebSocket ─┐
  ├─ Live resource metrics pane        SSE/WS ──────┤
  ├─ Debug request list (when enabled)  WS ────────┤
  └─ Proxy / settings pane (read-only display) ─────┤
Node control panel (Fastify + ws)  ◀── single HTTP port, access-key auth ─┘
  ├─ Auth: single ACCESS_KEY → signed HttpOnly session cookie
  ├─ PTY manager: node-pty spawns `claude` (interactive, bypassPermissions) in /workspace
  ├─ Metrics collector: cgroup v2 (/sys/fs/cgroup) + /proc/net/dev → WebSocket push
  ├─ Debug proxy: opt-in MITM proxy (container-local CA), captures only when toggled on
  ├─ Control API: start/stop session, resize, enable/disable capture, clear captures
  └─ Health check / logs
Claude Code process (non-root user `claude`, cwd /workspace)
Docker named volumes: /workspace (project files), /home/claude/.claude (config / credentials / sessions)
```

### Process model

- The control panel is PID 1 (or run under `tini`). It spawns the Claude Code PTY on demand.
- Control panel and Claude Code are decoupled: a crashed PTY does not take down the panel (the panel re-spawns or shows a reconnect affordance).
- Everything runs as non-root user `claude` inside the container.

## 4. Components

### 4.1 Container image

- **Base:** `debian:bookworm-slim`.
- **Installed:** Node.js 22 (from NodeSource), `git`, `ripgrep`, `curl`, `ca-certificates`, `jq`, a minimal `tini` init.
- **Claude Code:** installed via the native Linux installer (`curl -fsSL https://claude.ai/install.sh | bash`), producing a self-contained binary on `PATH`. `DISABLE_AUTOUPDATER=1` / `DISABLE_UPDATES=1` set in the image so an ephemeral container does not waste effort on self-update.
- **User:** non-root `claude` (uid 1000), home `/home/claude`.
- **Ports:** `8080/tcp` (control panel).

### 4.2 Volumes

- `claude-workspace` → `/workspace` — Claude Code working directory (project files).
- `claude-config` → `/home/claude/.claude` — settings, credentials, agents, skills, session transcripts. `CLAUDE_CONFIG_DIR` points here so auth persists across container restarts.

### 4.3 Control panel (Node)

- **Framework:** Fastify + `ws` for WebSockets; static-served SPA.
- **Auth:**
  - Single secret `ACCESS_KEY` (env). First load shows an unlock screen; on submit the server constant-time-compares and, on match, sets a signed HttpOnly `session` cookie.
  - All HTTP routes (except the unlock endpoint) and all WebSocket upgrades require the cookie (same-origin + Origin header check on WS).
- **PTY manager (`node-pty`):**
  - Spawns `claude` with `--permission-mode bypassPermissions` inside `/workspace`, env-injected with proxy + Anthropic vars.
  - Streams bytes over the terminal WebSocket both directions; handles resize messages.
  - Restart-on-exit policy: default to "show reconnect" rather than silent auto-respawn, so the user sees when a session ends.
- **Metrics collector:**
  - Detects cgroup v2 (`/sys/fs/cgroup/cgroup.controllers`); reads `cpu.stat` (`usage_usec`) and `memory.current` / `memory.max`.
  - CPU% derived from per-interval `usage_usec` delta vs wall-clock delta × num CPUs.
  - Network RX/TX from `/proc/net/dev` for the primary interface; derive bytes/sec from deltas.
  - Pushes a snapshot every ~1.5s to connected dashboard clients; never blocks the PTY.
  - Graceful fallback: if cgroup stats are unavailable, report what is readable and surface a degraded indicator rather than crashing.
- **Debug proxy (opt-in):**
  - Off by default. When toggled on in the UI, starts a local MITM/CONNECT proxy (Node) listening on a container-internal port and (re)points Claude Code's `HTTPS_PROXY` at it. A container-local CA it generated is written into the container trust store (`NODE_EXTRA_CA_CERTS` for Node-launched children, and `update-ca-certificates` for system + the Claude Code native binary which uses the system trust store).
  - Captures: timestamp, method, host, path, request headers (selected), request body, response status, response headers (selected), response body, latency; for Claude API calls it also surfaces model, stop reason, and token usage parsed from the body.
  - **Secret redaction** before any byte is stored or displayed: `x-api-key`, `Authorization`, `anthropic-*`, cookies, proxy credentials, anything matching known token shapes; bodies containing secrets are stored redacted.
  - Storage is in-memory + optionally a capped ring file under the config volume; one-click clear; explicit short retention; UI shows a prominent warning that full-body capture is active.
  - Only traffic from the in-container Claude Code process is routed through it; it binds to loopback only.
- **Control API (all auth-gated):**
  - `POST /auth` (unlock), `POST /logout`.
  - `GET /api/state` (capture on/off, proxy config display, session alive).
  - `POST /api/capture/enable|disable`, `POST /api/captures/clear`.
  - `POST /api/session/restart`.
  - WebSocket routes: `/ws/terminal`, `/ws/metrics`, `/ws/captures`.

### 4.4 SPA (Claude-styled UI)

- **Stack:** plain ES modules + a small bundler (Vite build → static), `xterm.js` (terminal) + its `@xterm/addon-fit` / `addon-web-links`. No heavy framework required, but a minimal reactive layer (Preact or hand-rolled) is acceptable.
- **Design system (matches Claude aesthetic):**
  - Background cream ≈ `#F4F1EA`; surface ≈ `#FFFFFF` / `#FAF7F0`.
  - Serif display type (Georgia / system serif stack) for headings; sans-serif body.
  - Terracotta/amber accent (≈ `#C96442` / `#D97757`), Claude's rounded corners and ample whitespace.
  - xterm configured to the same palette so the terminal blends in.
- **Layout:** terminal as the centerpiece; a collapsible sidebar/topbar with metrics, capture toggle + list, and a read-only view of active proxy/env config. Responsive, desktop-first.
- **Capture list:** real-time appended rows; click a row to expand method/url/status/latency and redacted request/response bodies; filter by status/host; clear button.

## 5. Data Flow

1. **Unlock:** Browser → `POST /auth {key}` → server constant-time compare → signed cookie → SPA loads.
2. **Terminal:** Browser opens `/ws/terminal` (cookie + Origin) → server attaches to the live `claude` PTY → keystrokes flow down, output flows up; resize messages adjust PTY cols/rows.
3. **Metrics:** collector polls cgroup + `/proc/net/dev` every ~1.5s → broadcasts on `/ws/metrics` → SPA renders gauges/sparklines.
4. **Capture:** when enabled, Claude Code's `HTTPS_PROXY` → loopback MITM proxy → upstream (through the configured global proxy chain) → MITM records redacted entry → pushes to `/ws/captures` → SPA appends to list.
5. **Proxy egress:** Claude Code (and the MITM proxy's upstream) honor the injected `HTTP_PROXY`/`HTTPS_PROXY`/`ALL_PROXY`/`NO_PROXY` so flaky-direct connections go through the user's proxy.

## 6. Configuration & Environment

Required / supported environment variables (passed via `docker run -e` or `.env`):

| Variable | Purpose |
|---|---|
| `ACCESS_KEY` | Web access secret (required). |
| `ANTHROPIC_API_KEY` *or* `ANTHROPIC_AUTH_TOKEN` | Claude Code credential (one required; `AUTH_TOKEN` used for bearer/gateway auth). |
| `ANTHROPIC_BASE_URL` | Optional gateway endpoint. |
| `HTTP_PROXY` / `HTTPS_PROXY` / `ALL_PROXY` | Outbound proxy; supports `http://` and `socks5://`. |
| `NO_PROXY` | Hosts to bypass. |
| `API_TIMEOUT_MS` | Request timeout (default 600000). Increase for slow proxies. |
| `DISABLE_AUTOUPDATER=1` | Set in image; do not self-update. |

Notes:
- `ALL_PROXY` support for SOCKS5 lets the user route a SOCKS proxy directly; HTTP proxies via `HTTP_PROXY`/`HTTPS_PROXY`.
- Proxy credentials in `http://user:pass@host` form are supported by the proxy stack but never displayed in the UI.

## 7. Security

- Non-root container user; no `--privileged`; no Docker socket mounted.
- Single access key + signed HttpOnly cookie; same-origin + Origin-checked WebSockets. The container expects to sit behind a TLS-terminating front proxy on untrusted networks.
- Debug capture defaults **off**; full-body capture is gated behind an explicit toggle with a UI warning; secrets are redacted before storage/display; capture storage is loopback-scoped and clearable.
- The container-local CA is trusted **only inside the container** — never installed on the host or browser by default.
- Only the in-container Claude Code process's traffic is routed through the debug proxy; it binds to loopback.
- Resource limits (`--memory`, `--cpus`, `--pids-limit`) recommended in the run instructions.

## 8. Error Handling

- PTY crash → panel stays up, terminal pane shows "session ended — restart"; restart is a button, not silent auto-respawn, so failures are visible.
- Metrics source missing → degrade gracefully, surface a "metrics unavailable" indicator, keep terminal working.
- Bad/missing proxy → Claude Code surfaces its own retry/error stream events; the panel passes them through to the terminal and (if capture is on) the debug list.
- Missing `ACCESS_KEY` → panel refuses to start with a clear log message.
- Auth failure → 401, no cookie; rate-limit the unlock endpoint to resist brute force.

## 9. Testing

- **Unit:** cgroup/`/proc` parsing + delta math (with fixture files), secret-redaction function, cookie signing/verification, capture-record shaping.
- **Integration:** `--bare -p` smoke test — spawn `claude --bare -p "echo hi" --output-format json` and assert a JSON result, proving the image + auth + env wiring end to end.
- **Debug proxy:** point a fixture request at the MITM proxy with the container CA and assert redaction + capture record.
- **Manual verification checklist:** unlock, terminal I/O, metrics tick, proxy boot, capture on/off + redaction + clear.

## 10. Deliverables & Run Experience

- `Dockerfile`, `.dockerignore`, `.env.example`, `README.md` with a one-line `docker run` and a `docker compose` example.
- `server/` (Fastify control panel + PTY + metrics + debug proxy), `web/` (SPA), entrypoint script.
- Friend-friendly: a `start.bat` / `start.sh` and a short "open this URL" note, since the target non-technical user should only run a launcher and visit a page.

## 11. Open Items (resolved at build time, not blocking)

- Exact MITM proxy library choice (e.g. `http-mitm-proxy` vs a hand-rolled CONNECT proxy) — pick the one that cleanly supports SOCKS-upstream and CA install; verify against the Claude Code native binary's trust store during implementation.
- Metrics refresh cadence tuning (default ~1.5s).
- Whether to persist capture records to the config volume (default: in-memory only unless the user opts into file persistence in the UI).
