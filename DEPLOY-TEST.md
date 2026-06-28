# Deploy-Test Checklist

This is the comprehensive checklist of items to verify after `docker compose up -d --build` on a **Linux/Docker host**. The Go code was developed and unit-tested on Windows (where the Linux runtime can't run), so these are the runtime integration checks.

## 0. Build + boot

```bash
docker compose up -d --build
docker compose logs -f claude   # watch for [server] listening on :8080
```

- [ ] Container starts without crash; log shows `listening on :8080`.
- [ ] `curl localhost:8080/health` → `{"ok":true}`.
- [ ] `curl -X POST localhost:8080/auth -d '{"username":"<BOOTSTRAP_ADMIN_USER>","password":"<BOOTSTRAP_ADMIN_PASSWORD>"}' -H 'content-type: application/json'` → 200 + `Set-Cookie: session=...`.

## 1. Web UI

- [ ] Open `http://localhost:8080` → login page (Claude style, warm palette).
- [ ] Sign in with bootstrap admin → prompted to change password.
- [ ] Change password → app loads (left sidebar, Terminal active).
- [ ] Toggle theme (light/dark/system) — persists on reload.
- [ ] Resize to mobile width → sidebar becomes a drawer (≡ button).

## 2. Admin user management

- [ ] Sidebar → Users → "+ New user" → create `alice` (role: user, password).
- [ ] alice appears in the table with disk/traffic/sessions columns.
- [ ] Sign out → sign in as alice → password-change prompt (first login).
- [ ] Suspend alice (admin) → alice's cookie stops working immediately.

## 3. Per-user terminal

- [ ] As alice, open Terminal → shell starts (runs as `alice` via gosu, not root).
- [ ] Type `whoami` → `alice`. Type `id` → uid matches the allocated uid (2000+).
- [ ] Type `claude` → claude runs (if credential preset is bound).
- [ ] "+ New session" → second tab. Switch between sessions.
- [ ] Close browser → reopen → sessions resume (detach/attach works).

## 4. Credential injection

- [ ] Admin → Credentials → "+ New preset" → enter an Anthropic token.
- [ ] Create user `bob` with the preset bound.
- [ ] As bob, in terminal → `echo $ANTHROPIC_AUTH_TOKEN` shows the decrypted value.
- [ ] Admin → Credentials list does NOT show the token value (only name/note).

## 5. SFTP

- [ ] From a host SFTP client: connect to `<host>:22` as alice + password.
- [ ] alice sees `workspace/` → can upload/download files.
- [ ] alice CANNOT see `/etc`, `/root`, or other users' homes (chroot confinement).
- [ ] Admin SFTP → full filesystem access (root).

## 6. Quotas

- [ ] Admin → Users → alice's disk column shows used/limit.
- [ ] Upload a large file via SFTP → disk usage updates within 60s.
- [ ] Over-quota → panel shows "over" + terminal shows a warning banner.

## 7. Traffic

- [ ] After some claude/SFTP activity → admin → Users → traffic column shows ↓/↑ bytes.
- [ ] Monthly traffic table (`/api/admin/traffic`) shows the current month's totals.
- [ ] If `nft` is unavailable → log shows "traffic accounting in no-op mode" (graceful).

## 8. Capture (admin debug)

- [ ] Admin → Captures panel loads (empty initially).
- [ ] Enable capture on an admin session (API: `POST /api/admin/sessions/:id/capture/enable`).
- [ ] Run a claude API call → redacted req/resp appears in the Captures panel.
- [ ] Verify secrets are redacted (API keys show `[REDACTED]`).
- [ ] Disable capture → session restarts without the proxy.

## 9. Suspend / delete

- [ ] Suspend a user → their sessions die, SFTP login fails, web login blocked.
- [ ] Unsuspend → user can log in again.
- [ ] Delete a user → `/home/<user>` + `/data/<user>` removed, DB row gone.

## Known gaps (documented)

These are Linux-runtime wiring items that compile + cross-compile clean but couldn't be runtime-verified on the Windows dev host. If something doesn't work, check here first:

- **cgroup apply + pid**: the cgroup subgroup is created but the PTY's pid may not be moved into `cgroup.procs` — CPU/mem limits may not enforce. Verify + fix if needed.
- **nft counter install**: counters are installed lazily on first session-create; verify the nft ruleset shows per-user rules.
- **SSH admin shell**: currently a placeholder `Exit(0)` — admin gets the web terminal instead. Wire a root PTY if SSH admin shell is needed.
- **SFTP confinement**: `pkg/sftp` serves from `/` unless the session handler chroots+setuids. Verify alice can't escape `/home/alice`.
- **Capture Response hook**: the go-mitmproxy `Response` callback (session resolution + redaction + store.Add) may need the flow's connection context wired to resolve the session id.
