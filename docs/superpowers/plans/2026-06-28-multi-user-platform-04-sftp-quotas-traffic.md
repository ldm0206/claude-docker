# Multi-User Platform — Plan 4: SFTP + Disk/Cgroup Quotas + nftables Traffic + Cap-Drift Fix

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Add file transfer (embedded SFTP/SSH server), resource accounting (soft disk quota via `du` + cgroup v2 CPU/mem per-user), per-user monthly network traffic (nftables cgroup counters), and the suspend flow's resource reclamation — plus fix Plan 3's cap-drift (a naturally-exited PTY left its `sessions` row `alive=1`, over-counting the cap). After this plan: admins see per-user disk/CPU/mem/traffic, users can SFTP into their workspace, and quotas/templates actually constrain usage.

**Architecture:** A new `internal/ssh` package runs an embedded SSH/SFTP server (`gliderlabs/ssh` + `pkg/sftp`), authenticating against the users DB (argon2id) and confining regular users via chroot+setuid to their workspace (admins get a full root shell). `internal/quota` does the disk loop (`du`) + cgroup v2 subgroup writes (`/sys/fs/cgroup/cu-<uid>/`). `internal/traffic` installs nftables cgroup-matched counters (needs `CAP_NET_ADMIN`) and samples deltas into a `traffic` table. All Linux-only at runtime; the logic + seams are Windows-testable.

**Tech Stack:** Go 1.25, `github.com/gliderlabs/ssh`, `github.com/pkg/sftp`, `github.com/google/nftables` (or shell out to `nft` — pick per task), existing `modernc.org/sqlite`, `creack/pty`.

## Global Constraints

- Branch: `feat/plan-4-sftp-quotas-traffic`. Module `github.com/ldm0206/claude-docker/backend`, `CGO_ENABLED=0`, Dockerfile go-builder `golang:1.26` (re-sync if `go mod tidy` bumps past 1.26).
- **Two test tiers (host = Windows, no Docker/WSL):**
  - **Windows-runnable**: store (traffic table), quota/traffic *logic* with injectable filesystem/cgroup/nft seams, admin API, cap-drift fix (fake PTY). Full TDD.
  - **Linux-only** (deferred to deploy): real `du`, cgroup writes, nftables, the SSH/SFTP server, gosu/chroot. Compile-verify on Windows (`go build`, `go vet`, `GOOS=linux go test -c`); runtime GREEN on Linux deploy.
- `CAP_NET_ADMIN` is added to compose (nftables). Server still runs as root (Plan 2). No `--privileged`.
- Soft disk quota: monitor via `du -sb /home/<u>`; over-quota → panel red + terminal banner; **no write-blocking** in v1 (hard block is a later toggle).
- Traffic: nft counter matched on the user's cgroup; sampled deltas → `traffic(user_id, year_month, rx_bytes, tx_bytes)`. If nft unavailable at startup, degrade gracefully (traffic "unavailable", don't crash).
- Cap-drift fix (Plan 3 follow-up): the sessions.Manager registers an internal `OnExit` callback per PTY that calls `MarkSessionExited` + removes the live-map entry — so a naturally-exited PTY no longer counts toward the cap.
- DRY, YAGNI, TDD. **Review subagents: use `haiku`** (user pref); sonnet/opus for structural/multi-file.

## File Structure (this plan adds/changes)

```
backend/
  internal/
    store/
      schema.sql            # APPEND: traffic table
      traffic.go            # [NEW] AddTraffic/userID,month/delta), GetTraffic, ResetTraffic
      traffic_test.go
    sessions/
      manager.go            # MODIFY: register OnExit reaper in Create (cap-drift fix)
      manager_test.go       # ADD: natural-exit marks row exited + removes from map
    quota/
      quota.go              # [NEW] DiskUsage(homeRoot) reader + over-quota check; Cgroup Apply/Remove
      quota_test.go         # injectable FS + cgroup writer seams
    traffic/
      traffic.go            # [NEW] nftables counter install + sample loop → store.AddTraffic
      traffic_test.go       # injectable nft + store seams
    ssh/
      server.go             # [NEW] embedded SSH/SFTP server (gliderlabs/ssh + pkg/sftp)
      server_test.go        # auth + routing logic via seams (real server Linux-only)
    server/
      server.go             # MODIFY: hold quota/traffic refs; routes
      admin_quota.go        # [NEW] GET /api/admin/users/:id/usage (disk/cpu/mem/traffic)
      admin_traffic.go      # [NEW] GET /api/admin/traffic?user=&month=, POST .../reset-traffic
      metrics_ws.go         # MODIFY: per-user throughput in snapshot (if cheap) OR keep aggregate
  cmd/server/main.go        # MODIFY: start quota loop, traffic loop, SSH server; pass to New
entrypoint.sh               # MODIFY: ensure /sys/fs/cgroup writable hint (no-op if not); mkdir /home /data
Dockerfile                  # MODIFY: add nftables, openssh-client (for git over ssh); CAP_NET_ADMIN in compose
docker-compose.yml          # MODIFY: cap_add NET_ADMIN
```

---

### Task 1: traffic store table + CRUD

**Files:** `backend/internal/store/schema.sql` (append), `traffic.go`, `traffic_test.go`

- [ ] **Step 1: Append to `schema.sql`:**
```sql
CREATE TABLE IF NOT EXISTS traffic (
  user_id INTEGER NOT NULL,
  year_month TEXT NOT NULL,            -- "YYYY-MM"
  rx_bytes INTEGER NOT NULL DEFAULT 0,
  tx_bytes INTEGER NOT NULL DEFAULT 0,
  PRIMARY KEY (user_id, year_month)
);
```
- [ ] **Step 2: `traffic.go`** — `(*DB) AddTraffic(userID int, yearMonth string, rxDelta, txDelta int64) error` (UPSERT `INSERT ... ON CONFLICT(user_id,year_month) DO UPDATE SET rx=rx+?, tx=tx+?`); `GetTraffic(userID, yearMonth) (rx,tx int64,err)`; `ListTrafficForUser(userID) ([]TrafficRow,err)`; `ResetTraffic(userID, yearMonth) error` (zero the row); `SumTrafficForUser(userID) (rx,tx int64,err)`.
- [ ] **Step 3: Tests** (Windows): AddTraffic accumulates across calls; UPSERT creates the row on first add; ResetTraffic zeroes; SumTraffic totals all months. Add `TestSchemaCreatesAllTables` assertion for `traffic` too.
- [ ] **Step 4: GREEN; build+vet clean.**
- [ ] **Step 5: Commit** — `feat(backend): traffic store table + CRUD`.

---

### Task 2: cap-drift fix — sessions.Manager reaps naturally-exited PTYs

**Files:** `backend/internal/sessions/manager.go`, `manager_test.go`

- [ ] **Step 1: Test** (Windows, fake PTY): after `Create`, simulate a natural exit by invoking the fake PTY's registered `OnExit` callback (the fake should capture the OnExit cb so the test can fire it). Assert: the DB row is now `alive=0`, AND `Get(username, sid)` returns `(nil, false)` (removed from live map), AND `CountAliveSessionsForUser` no longer counts it (so the cap isn't consumed).
- [ ] **Step 2: RED.**
- [ ] **Step 3: Implement** — in `Create`, after storing the PTY, register an internal `OnExit` callback that (outside the lock) calls `m.db.MarkSessionExited(id)` and removes the entry from the live map under the lock. Keep it idempotent (Kill may also try — guard with a "reaped" flag or check map presence). Do NOT remove the OnData subscription here (the WS handler owns that; on natural exit the WS is usually already gone).
- [ ] **Step 4: GREEN; build+vet clean; existing session tests still pass.**
- [ ] **Step 5: Commit** — `fix(backend): reap naturally-exited sessions so the cap doesn't drift`.

---

### Task 3: quota package — disk usage + cgroup (seam-driven)

**Files:** `backend/internal/quota/quota.go`, `quota_test.go`

**Interfaces:**
- `quota.DiskUsageProvider` interface: `Usage(homeRoot, username string) (int64, error)` — real impl shells `du -sb /home/<username>` (Linux); tests inject a fake.
- `quota.CgroupWriter` interface: `Apply(uid int, cpuQuota string, memMax int64) error`; `Remove(uid int) error` — real impl writes `/sys/fs/cgroup/cu-<uid>/{cpu.max,memory.max,cgroup.procs}` (Linux); tests inject a fake; on any write failure → log + degrade (don't fail the session).
- `quota.Service` tying it together: `CheckDisk(username) (used, limit int64, over bool)`; `ApplyCgroup(uid, template) error`.

- [ ] **Step 1: Tests** (Windows, fakes): DiskUsage returns the fake's value; over-quota when used>limit; CgroupWriter.Apply called with right uid/params; Apply tolerates writer errors (logs, returns nil).
- [ ] **Step 2: RED.**
- [ ] **Step 3: Implement.** Real `DiskUsage` uses `exec.Command("du","-sb",path)`. Real `CgroupWriter` does `os.MkdirAll("/sys/fs/cgroup/cu-<uid>")` + `os.WriteFile(cpu.max...)` etc., returning error on failure (caller decides degrade).
- [ ] **Step 4: GREEN; build+vet clean; `GOOS=linux go test -c` exit 0.**
- [ ] **Step 5: Commit** — `feat(backend): quota package (disk usage + cgroup seams)`.

---

### Task 4: traffic package — nftables counters (seam-driven)

**Files:** `backend/internal/traffic/traffic.go`, `traffic_test.go`

**Interfaces:**
- `traffic.NftController` interface: `Install(uid int) error`; `Read(uid int) (rx, tx int64, err error)`; `Remove(uid int) error`. Real impl uses `github.com/google/nftables` (cgroupsv2 match + counter). If the lib is too heavy, shell out to `nft` (simpler, fewer deps) — pick one and document.
- `traffic.Service`: a sampler goroutine that every N seconds reads each user's counters and calls `store.AddTraffic(uid, currentMonth, rxDelta, txDelta)`. Tracks last-seen per uid to compute deltas.
- Graceful degrade: if `Install` fails at startup (no NET_ADMIN / nft unavailable), log + run in "no-op" mode (traffic stays 0, no crash).

- [ ] **Step 1: Tests** (Windows, fake NftController + real store): sampler reads fake counters, computes delta vs last-seen, AddTraffic accumulates correctly; a user with no counter is skipped; Install failure → no-op mode (sampler runs but writes nothing).
- [ ] **Step 2: RED.**
- [ ] **Step 3: Implement** the sampler + delta logic + the NftController interface + a real impl (nft lib or shell-out). The real impl is Linux-only; compile-verify on Windows.
- [ ] **Step 4: GREEN; build+vet; `GOOS=linux go test -c` exit 0.**
- [ ] **Step 5: Commit** — `feat(backend): traffic sampler + nftables counter seam`.

---

### Task 5: embedded SSH/SFTP server

**Files:** `backend/internal/ssh/server.go`, `server_test.go`

**Interfaces:**
- `ssh.Server` wrapping `gliderlabs/ssh.Server`; `PasswordAuth` and `PublicKeyAuth` verify against `store` (argon2id, reject suspended).
- Regular user → SFTP subsystem only, chroot+setuid to `/home/<username>` (the real confinement is a child-process setuid pattern or `internal-sftp`-equivalent — implement via `pkg/sftp` serving as the user; document the chroot mechanism). No shell for regular users.
- Admin → full interactive shell as root (a PTY) + unrestricted SFTP.
- Configurable port (`SFTP_PORT`, default 22).

- [ ] **Step 1: Tests** (Windows, with a fake auth + fake handler): the auth callback rejects wrong password / suspended / missing user, accepts correct; admin vs user routing decision is correct (do NOT stand up a real TCP listener in unit tests — test the auth + routing functions in isolation).
- [ ] **Step 2: RED.**
- [ ] **Step 3: Implement.** The actual SSH listener start is Linux-only (needs real PTY/gosu/chroot); expose `Start() error` / `Stop()` and unit-test the auth/routing seams.
- [ ] **Step 4: GREEN; build+vet; `GOOS=linux go test -c` exit 0.**
- [ ] **Step 5: Commit** — `feat(backend): embedded SSH/SFTP server (auth + chroot/admin routing)`.

---

### Task 6: admin usage/traffic API + suspend resource reclamation

**Files:** `backend/internal/server/admin_quota.go`, `admin_traffic.go`, `server.go` (routes; wire quota+traffic refs), `admin_users.go` (suspend now also kills sessions via KillAll + locks the Linux account + stops cgroup).

**Endpoints:**
- `GET /api/admin/users/:id/usage` → `{disk:{used,limit,over}, traffic:{rx,tx,thisMonth}, sessions:{alive,total}}`.
- `GET /api/admin/traffic?user=&month=` → traffic rows.
- `POST /api/admin/users/:id/reset-traffic` → zero current month.
- Suspend (`POST /api/admin/users/:id/suspend`): existing Lock + SetSuspended + KillAll (sessions.Manager already kills PTYs) + cgroup Remove. Unsuspend: Unlock + SetSuspended (cgroup re-applies on next session).

- [ ] **Step 1: Tests** (Windows, fakes): usage endpoint returns the fake quota/traffic values; reset-traffic zeroes; suspend calls KillAll + quota.RemoveCgroup.
- [ ] **Step 2: RED.**
- [ ] **Step 3: Implement + mount.**
- [ ] **Step 4: GREEN; build+vet clean.**
- [ ] **Step 5: Commit** — `feat(backend): admin usage/traffic API; suspend reclaims cgroup`.

---

### Task 7: main.go wiring + Dockerfile/compose (nftables, NET_ADMIN, openssh-client)

**Files:** `cmd/server/main.go`, `Dockerfile`, `docker-compose.yml`, `entrypoint.sh`

- [ ] **Step 1: main.go** — build quota.Service + traffic.Service + ssh.Server; start their loops/listeners (graceful degrade on Linux-only features); pass to `server.New` (extend signature). Keep build green (tests inject fakes).
- [ ] **Step 2: Dockerfile** — runtime apt add `nftables openssh-client`; keep `gosu screen tmux`.
- [ ] **Step 3: docker-compose.yml** — `cap_add: ["NET_ADMIN"]`; expose SFTP port (`"22:22"` or configurable).
- [ ] **Step 4: entrypoint.sh** — `mkdir -p /home /data /workspace`; nothing else (server is root).
- [ ] **Step 5: `go build ./...`, `go vet ./...`, `go test ./...`, `GOOS=linux go test -c ./internal/{quota,traffic,ssh,sessions,pty,system}/` all clean.**
- [ ] **Step 6: Commit** — `feat(backend): wire quota/traffic/ssh into main; Dockerfile nftables + NET_ADMIN`.

---

## Self-Review (Plan 4 vs spec §7, §9, §10, §5-suspend)
- **Coverage:** disk soft quota (§7) ✓ T3; cgroup cpu/mem (§7) ✓ T3; nft traffic monthly + live (§9) ✓ T4; SFTP embedded chroot + admin shell (§10) ✓ T5; suspend reclaims (§5) ✓ T6; cap-drift fix ✓ T2.
- **Placeholders:** none; Linux-only steps state the deferral.
- **Deferred:** write-blocking disk quota (later toggle), per-user live throughput in /ws/metrics (keep aggregate unless cheap), capture (Plan 5), UI (Plan 6).

## Notes for later plans
- Plan 5 (capture) adds the MITM proxy + per-session capture flag.
- Plan 6 (UI) consumes `/api/admin/users/:id/usage` + `/api/admin/traffic` for the meters.
