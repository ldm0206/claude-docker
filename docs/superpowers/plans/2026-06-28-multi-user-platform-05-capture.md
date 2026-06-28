# Multi-User Platform — Plan 5: Admin-Only Per-Session Request/Response Capture

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development. Steps use checkbox (`- [ ]`) tracking.

**Goal:** Restore the legacy opt-in MITM capture as an **admin-only, per-session** debug feature. An admin enables capture on a specific session → that session's PTY env routes its HTTPS through a lazy-started Go MITM proxy (CA-signed) → request/response pairs are recorded (redacted against the session's credential secrets) and streamed to a Captures panel. Other sessions/users are unaffected (routing lives in each PTY's env).

**Architecture:** `internal/capture` owns a `go-mitmproxy`-based proxy (lazy-started on first enable), an in-memory store of redacted req/resp pairs tagged by session, and a per-session "capture-on" set. Enabling capture on a session marks it so the PTY env factory injects `HTTP_PROXY/HTTPS_PROXY` → MITM URL (and drops `ALL_PROXY`) on the session's next restart; disabling clears it. Redaction reuses the decrypted credential secrets (from the master key) so API keys/tokens never appear in the panel.

**Tech Stack:** Go 1.25, `github.com/lqqyt2423/go-mitmproxy` (pure-Go MITM), existing `modernc.org/sqlite`, `coder/websocket`.

## Global Constraints

- Branch: `feat/plan-5-capture`. Module `github.com/ldm0206/claude-docker/backend`, `CGO_ENABLED=0`, Dockerfile go-builder `golang:1.26` (re-sync if `go mod tidy` bumps past 1.26).
- **Admin-only**: capture endpoints under `requireAdmin`. Only admins can enable/list/clear.
- **Per-session**: capture is enabled on one session id; only that session's PTY env routes through the MITM. The shared CA (container trust store) does NOT imply shared interception.
- **Lazy MITM**: the proxy starts on first enable (not at boot), listens on `127.0.0.1:${CLAUDE_DEBUG_PROXY_PORT:-8888}`. If it fails to start (port in use / lib issue), capture-enable returns 500 and the session is NOT rerouted.
- **Redaction**: captured bodies/headers redacted against the session user's decrypted credential secrets (the same `credEnv` values). Redaction is best-effort string replacement; never log unredacted.
- **In-memory store**: captures are ephemeral (cleared on disable/clear/restart), scoped to the producing session.
- **Two test tiers (host = Windows, no Docker/WSL):** capture store + redaction + per-session flag + API logic are Windows-testable (full TDD with a fake proxy). The real MITM proxy + CA + claude routing is Linux-only (deferred).
- DRY, YAGNI, TDD. **Review subagents: haiku**; sonnet/opus for structural.

## File Structure (this plan adds/changes)

```
backend/
  internal/
    capture/
      store.go             # [NEW] in-memory redacted req/resp store + subscribe
      store_test.go
      redact.go            # [NEW] redact a string/headers against a secret set
      redact_test.go
      service.go           # [NEW] the MITM proxy lifecycle (lazy start/stop) + per-session flag set
      service_test.go      # fake proxy seam; Windows-runnable
    server/
      server.go            # MODIFY: hold *capture.Service; replace inert capture stubs
      admin_capture.go     # [NEW] POST /api/admin/sessions/:id/capture/{enable,disable}, GET /api/admin/captures, POST /api/admin/captures/clear
      terminal.go / ensureSession  # MODIFY: env factory reads capture flag → inject MITM proxy env on restart
      server_test.go       # MODIFY: drop inert-stub assertions; add capture flag tests
    sessions/
      manager.go           # (optional) Restart(username, sessionID) helper to re-spawn with new env
  cmd/server/main.go       # MODIFY: build capture.Service, pass to New
entrypoint.sh              # MODIFY: restore CA generation + trust-store install (root, before exec)
Dockerfile                 # (no change beyond what entrypoint needs; openssl already present)
```

---

### Task 1: capture store + redaction

**Files:** `backend/internal/capture/store.go`, `redact.go`, + tests

- `capture.Record{SessionID, Method, Host, Path, Status, LatencyMs, Ts int64, ReqHeaders, ResHeaders map[string]string, ReqBody, ResBody string}` (all bodies/headers already redacted by the time they're stored).
- `Store`: `Add(r Record)`, `List(sessionID string) []Record` (or all), `Clear()`, `ClearSession(id)`, `Subscribe(cb func(Record)) func()` (for the WS push). Mutex-guarded slice.
- `redact.Redact(s string, secrets []string) string` — replace each secret occurrence with `[REDACTED]` (case-sensitive exact match; best-effort). `redact.RedactHeaders(map, secrets) map`. Empty/short secrets (<4 chars) skipped (avoid clobbering common substrings).

Tests (Windows): Add/List/Clear; Subscribe receives new records; Redact replaces known secrets; short secrets skipped; RedactHeaders redacts header values.

Commit: `feat(backend): capture store + secret redaction`.

---

### Task 2: capture service (MITM proxy lifecycle + per-session flag) — seam-driven

**Files:** `backend/internal/capture/service.go`, `service_test.go`

- `ProxyRunner` interface: `Start(addr string) error`, `Stop() error`, `Running() bool`. Real impl wraps `go-mitmproxy` with a CA + a hook that captures req/resp into the store (after redaction). Tests inject a fake.
- `Service{runner ProxyRunner, store *Store, flag map[sessionID]bool, mu, masterKey, db}`.
  - `Enable(sessionID string, userID int) error` — mark flag[sessionID]=true; lazily Start the proxy on first enable (if not running); on Start failure → unmark + return error.
  - `Disable(sessionID)` — unmark; if no flags remain → Stop the proxy.
  - `IsEnabled(sessionID) bool`.
  - `ProxyURL() string` — `http://127.0.0.1:<port>` (injected into the PTY env when enabled).
  - The runner's capture hook: on each req/resp, resolve the session (by matching the source/connection — or tag by the user whose env pointed at the proxy), redact against that user's decrypted creds, `store.Add`.
- Graceful: proxy Start failure → Enable returns 500-equivalent error, session not rerouted.

Tests (Windows, fake ProxyRunner): Enable marks flag + starts proxy on first; second Enable doesn't restart; Disable unmarks + stops when none remain; IsEnabled correct; Start failure → Enable errors + flag cleared.

Commit: `feat(backend): capture service (lazy MITM + per-session flag)`.

---

### Task 3: wire env-routing — enabled session's PTY routes through MITM on restart

**Files:** `backend/internal/server/server.go` (env factory), `admin_capture.go`, `sessions/manager.go` (Restart helper)

- The PTY env factory (the `BuildUserEnv` path) gains: if `capture.IsEnabled(sessionID)`, set `HTTP_PROXY`/`HTTPS_PROXY`/`http_proxy`/`https_proxy` = `capture.ProxyURL()` and delete `ALL_PROXY`/`all_proxy` (so claude doesn't bypass via SOCKS).
- `POST /api/admin/sessions/:id/capture/enable` → `capture.Enable(sessionID, userID)`; if the proxy came up, restart that session's PTY (`mgr.Restart(username, sessionID)` — Stop + Start, which re-evaluates the env factory and picks up the proxy routing). Response `{captureOn:true, captureUp:proxy.Running(), restarted:true}`. If proxy failed → `{captureOn:false, captureUp:false, restarted:false}` + 500.
- `POST /api/admin/sessions/:id/capture/disable` → `capture.Disable(sessionID)`; restart the PTY (env now omits the proxy). Response `{captureOn:false}`.
- `mgr.Restart(username, sessionID)`: Stop the live PTY + Start a fresh one (the env factory is lazy so it re-reads the flag). Reuse the existing session id + DB row.

Tests (Windows, fake PTY + fake capture.Service): enabling → env factory output contains the proxy URL + no ALL_PROXY; the session's PTY was restarted (fake records Stop+Start). Disabling → env omits proxy.

Commit: `feat(backend): per-session MITM env routing + capture admin API`.

---

### Task 4: captures WS + list/clear endpoints; replace inert stubs

**Files:** `backend/internal/server/admin_capture.go`, `server.go` (routes), `server_test.go`

- `GET /api/admin/captures?session=` → list (redacted) records.
- `WS /ws/captures` → push new records via `store.Subscribe` (admin-only, authWSUser).
- `POST /api/admin/captures/clear` → `store.Clear()`.
- Remove the Plan-1 inert capture stubs (`/api/capture/enable|disable`, `/api/captures/clear`, `/ws/captures`) — replaced by the admin per-session endpoints. (The frontend's capture panel will be rewired in Plan 6.)

Tests (Windows): list returns redacted records; WS pushes on Add (test the subscribe wiring without a real WS dial — extract the handler logic); clear empties; admin-only (403 non-admin).

Commit: `feat(backend): captures list/WS/clear endpoints; drop inert stubs`.

---

### Task 5: restore CA generation in entrypoint + main.go wiring

**Files:** `entrypoint.sh`, `cmd/server/main.go`

- `entrypoint.sh`: restore the CA generation block (root, before exec): generate `/etc/claude-debug/ca.{crt,key}` if absent (openssl req -x509), install into `/usr/local/share/ca-certificates/` + `update-ca-certificates`, export `CLAUDE_DEBUG_CA_CERT`/`_CA_KEY`/`_SSL_CA_DIR`/`_CLAUDE_DEBUG_PROXY_PORT` for the Go process. (Mirror the original Plan-1 entrypoint that Plan 2 removed.)
- `main.go`: build `capture.NewService(...)` with the real ProxyRunner (go-mitmproxy) + masterKey + db; pass to `server.New`. Lazy proxy start happens on first Enable (not at boot).

Verify: `go build ./...`, `go vet ./...`, `go test ./...`, `GOOS=linux go test -c ./internal/capture/` exit 0.

Commit: `feat(backend): restore debug CA + wire capture service into main`.

---

## Self-Review (Plan 5 vs spec §10)
- **Coverage:** MITM proxy lazy-start ✓ T2; per-session routing ✓ T3; redaction against session creds ✓ T1/T2; captures store + WS ✓ T1/T4; admin-only ✓ T3/T4; CA trust ✓ T5.
- **Deferred to deploy:** real go-mitmproxy runtime + claude actually routing through it + CA-trust end-to-end (Linux-only).
- **Placeholders:** none; Linux-only steps state the deferral.

## Notes for later plans
- Plan 6 (UI) adds the admin Captures panel consuming `/api/admin/captures` + `/ws/captures`.
- Plan 7 (hardening) may tighten redaction (header-name allowlist, body size cap).
