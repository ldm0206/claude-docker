# Shared Credential Sync Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Operator runs `claude login` once into a shared dir; every user's PTY copies those credential files into its own claude-config at spawn time.

**Architecture:** A shared root-owned dir `/data/shared/claude-config` holds the operator's `claude login` output. A new `system.SyncSharedCredentials(uid)` copies `.credentials*` files from it into each user's `/data/<user>/claude-config` on every PTY spawn (wired into the existing `EnvFactory`, so `Manager.Start`/`Restart` re-runs it). The per-user preset→env injection path (`resolveCredEnv` / `BuildUserEnv` `credEnv` param) is removed; the store table and admin UI remain as dead code.

**Tech Stack:** Go 1.26, chi, modernc.org/sqlite. Pure-Go, no new deps. Linux-only file ops guarded by `//go:build linux`.

## Global Constraints

- modernc.org/sqlite NULL-scan strictness: use `COALESCE` / `sql.Null*` for any nullable column. (Not touched here, but applies if any query changes.)
- Tests must stay green before commit. Add a regression test per bug-fix path.
- No comments unless the WHY is non-obvious. No emojis in code.
- `go` 1.26 is available locally; `docker`/`wsl` are NOT on this machine — Linux-only tests run on the host, not here. Tests that need `//go:build linux` will be compiled by the host; the engineer should still write them.
- Auto-commit-and-sync (per CLAUDE.md): after each task, run the touched package's tests, then `git add <named files>` + new commit + `git push`. Never `git add -A`. Never amend. Never force-push.
- File-path conventions from spec: shared source = `DataRoot + "/shared/claude-config"`; per-user target = `DataRoot + "/<user>/claude-config"` (already created by `ProvisionUserDirs`).

---

### Task 1: `SyncSharedCredentials` — copy `.credentials*` from shared dir to a target dir

**Files:**
- Create: `backend/internal/system/cred_sync.go`
- Create: `backend/internal/system/cred_sync_test.go` (with `//go:build linux`)

**Interfaces:**
- Consumes: `DataRoot` (package-level `var DataRoot = "/data"` in `internal/system/dirs.go:19`).
- Produces: `func SyncSharedCredentials(uid int) error` — reads `<DataRoot>/shared/claude-config/.credentials*`, writes them to `<DataRoot>/<user>/claude-config`. Wait: the function takes `uid` but the target dir is per-user — the caller knows the username. To keep the function self-contained and testable, it takes BOTH the source dir and target dir as params (so tests don't depend on `DataRoot`), plus `uid` for chown. Signature:

```go
// SyncSharedCredentials copies credential files (names matching ".credentials*")
// from srcDir into dstDir. srcDir missing or containing no matches is a no-op
// (returns nil). Files are written mode 0600 and chown'd to uid. A per-file
// failure is logged and skipped; it does not abort the sync.
//
// This is the unexported core; the exported wrapper resolves paths from DataRoot.
func syncSharedCredentials(srcDir, dstDir string, uid int) error
```

The exported wrapper resolves the real paths:

```go
// SyncSharedCredentials copies the operator's shared credential files into the
// given user's claude-config dir. Source: <DataRoot>/shared/claude-config.
// Target: <DataRoot>/<username>/claude-config. No-op if source is absent or has
// no .credentials* files. uid owns the written files (0600).
func SyncSharedCredentials(username string, uid int) error
```

- [ ] **Step 1: Write the failing test**

Create `backend/internal/system/cred_sync_test.go`:

```go
//go:build linux

package system

import (
	"os"
	"path/filepath"
	"testing"
)

func writeCredFile(t *testing.T, dir, name, content string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
}

func TestSyncSharedCredentials_HappyPath(t *testing.T) {
	src := t.TempDir()
	dst := t.TempDir()
	writeCredFile(t, src, ".credentials.json", `{"token":"abc"}`)

	if err := syncSharedCredentials(src, dst, 2000); err != nil {
		t.Fatalf("sync: %v", err)
	}
	b, err := os.ReadFile(filepath.Join(dst, ".credentials.json"))
	if err != nil {
		t.Fatalf("target file missing: %v", err)
	}
	if string(b) != `{"token":"abc"}` {
		t.Fatalf("content = %q", string(b))
	}
	fi, _ := os.Stat(filepath.Join(dst, ".credentials.json"))
	if fi.Mode().Perm() != 0o600 {
		t.Fatalf("perm = %o, want 0600", fi.Mode().Perm())
	}
}

func TestSyncSharedCredentials_Whitelist(t *testing.T) {
	src := t.TempDir()
	dst := t.TempDir()
	writeCredFile(t, src, ".credentials.json", `x`)
	writeCredFile(t, src, "settings.json", `y`)
	if err := os.MkdirAll(filepath.Join(src, "projects"), 0o755); err != nil {
		t.Fatal(err)
	}

	if err := syncSharedCredentials(src, dst, 2000); err != nil {
		t.Fatalf("sync: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dst, "settings.json")); !os.IsNotExist(err) {
		t.Fatalf("settings.json should NOT be copied: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dst, "projects")); !os.IsNotExist(err) {
		t.Fatalf("projects/ should NOT be copied: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dst, ".credentials.json")); err != nil {
		t.Fatalf(".credentials.json should be copied: %v", err)
	}
}

func TestSyncSharedCredentials_SourceMissing(t *testing.T) {
	dst := t.TempDir()
	if err := syncSharedCredentials("/nonexistent/path/xyz", dst, 2000); err != nil {
		t.Fatalf("missing source must be no-op, got: %v", err)
	}
}

func TestSyncSharedCredentials_SourceEmpty(t *testing.T) {
	src := t.TempDir() // exists, no .credentials*
	dst := t.TempDir()
	if err := syncSharedCredentials(src, dst, 2000); err != nil {
		t.Fatalf("empty source must be no-op, got: %v", err)
	}
}

func TestSyncSharedCredentials_Overwrite(t *testing.T) {
	src := t.TempDir()
	dst := t.TempDir()
	writeCredFile(t, src, ".credentials.json", `new`)
	writeCredFile(t, dst, ".credentials.json", `old`)

	if err := syncSharedCredentials(src, dst, 2000); err != nil {
		t.Fatalf("sync: %v", err)
	}
	b, _ := os.ReadFile(filepath.Join(dst, ".credentials.json"))
	if string(b) != `new` {
		t.Fatalf("target not overwritten, content = %q", string(b))
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `cd backend && go test ./internal/system/ -run TestSyncSharedCredentials -v`
Expected: FAIL / build error — `syncSharedCredentials` undefined.

- [ ] **Step 3: Write minimal implementation**

Create `backend/internal/system/cred_sync.go`:

```go
package system

import (
	"errors"
	"io/fs"
	"log"
	"os"
	"path/filepath"
	"strings"
)

// syncSharedCredentials copies credential files (names matching ".credentials*")
// from srcDir into dstDir. srcDir missing or containing no matches is a no-op.
// Files are written mode 0600 and chown'd to uid. A per-file failure is logged
// and skipped; it does not abort the sync.
func syncSharedCredentials(srcDir, dstDir string, uid int) error {
	entries, err := os.ReadDir(srcDir)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return nil
		}
		return err
	}
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		if !strings.HasPrefix(e.Name(), ".credentials") {
			continue
		}
		src := filepath.Join(srcDir, e.Name())
		dst := filepath.Join(dstDir, e.Name())
		data, err := os.ReadFile(src)
		if err != nil {
			log.Printf("[system] warning: read shared credential %s: %v", src, err)
			continue
		}
		if err := os.WriteFile(dst, data, 0o600); err != nil {
			log.Printf("[system] warning: write credential %s: %v", dst, err)
			continue
		}
		if err := os.Chown(dst, uid, uid); err != nil {
			log.Printf("[system] warning: chown credential %s: %v", dst, err)
			continue
		}
	}
	return nil
}

// SyncSharedCredentials copies the operator's shared credential files into the
// given user's claude-config dir. Source: <DataRoot>/shared/claude-config.
// Target: <DataRoot>/<username>/claude-config. No-op if source is absent or has
// no .credentials* files. uid owns the written files (0600).
func SyncSharedCredentials(username string, uid int) error {
	src := filepath.Join(DataRoot, "shared", "claude-config")
	dst := filepath.Join(DataRoot, username, "claude-config")
	return syncSharedCredentials(src, dst, uid)
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `cd backend && go test ./internal/system/ -run TestSyncSharedCredentials -v`
Expected: PASS for all five tests.

- [ ] **Step 5: Commit**

```bash
cd backend && go test ./internal/system/...
git add backend/internal/system/cred_sync.go backend/internal/system/cred_sync_test.go
git commit -m "feat(system): add SyncSharedCredentials for copy-on-spawn creds"
git push
```

---

### Task 2: Provision the shared source dir at boot

**Files:**
- Modify: `backend/internal/system/dirs.go` (add `EnsureSharedCredentialDir`)
- Modify: `backend/internal/system/dirs_test.go` (add test, `//go:build linux` already on file)
- Modify: `backend/cmd/server/main.go` (call it at boot)

**Interfaces:**
- Consumes: `DataRoot` (`internal/system/dirs.go:19`).
- Produces: `func EnsureSharedCredentialDir() error` — idempotently creates `<DataRoot>/shared/claude-config` mode `0700`, root-owned.

- [ ] **Step 1: Write the failing test**

Append to `backend/internal/system/dirs_test.go`:

```go
// TestEnsureSharedCredentialDir verifies the shared source dir is created
// 0700 root-owned and is idempotent.
func TestEnsureSharedCredentialDir(t *testing.T) {
	data := t.TempDir()
	orig := DataRoot
	t.Cleanup(func() { DataRoot = orig })
	DataRoot = data

	if err := EnsureSharedCredentialDir(); err != nil {
		t.Fatalf("ensure: %v", err)
	}
	dir := filepath.Join(data, "shared", "claude-config")
	fi, err := os.Stat(dir)
	if err != nil {
		t.Fatalf("shared dir missing: %v", err)
	}
	if fi.Mode().Perm() != 0o700 {
		t.Fatalf("perm = %o, want 0700", fi.Mode().Perm())
	}
	// Idempotent: second call must not error.
	if err := EnsureSharedCredentialDir(); err != nil {
		t.Fatalf("second ensure: %v", err)
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `cd backend && go test ./internal/system/ -run TestEnsureSharedCredentialDir -v`
Expected: FAIL — `EnsureSharedCredentialDir` undefined.

- [ ] **Step 3: Write minimal implementation**

Add to `backend/internal/system/dirs.go` (after `ProvisionUserDirs`):

```go
// EnsureSharedCredentialDir idempotently creates the shared credential source
// dir <DataRoot>/shared/claude-config at mode 0700, root-owned. The operator
// runs `claude login` against it; SyncSharedCredentials copies from it. Safe to
// call on every boot.
func EnsureSharedCredentialDir() error {
	dir := filepath.Join(DataRoot, "shared", "claude-config")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return fmt.Errorf("mkdir shared claude-config: %w", err)
	}
	if err := os.Chmod(dir, 0o700); err != nil {
		return err
	}
	return os.Chown(dir, 0, 0)
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `cd backend && go test ./internal/system/ -run TestEnsureSharedCredentialDir -v`
Expected: PASS.

- [ ] **Step 5: Wire into boot**

In `backend/cmd/server/main.go`, call `system.EnsureSharedCredentialDir()` early in boot, before `ensureUsersProvisioned`. Find the existing block (around line 57-60) and add the call right before `ensureUsersProvisioned`:

```go
	if err := system.EnsureSharedCredentialDir(); err != nil {
		log.Fatalf("[server] ensure shared credential dir: %v", err)
	}
```

- [ ] **Step 6: Build and run full system package tests**

Run: `cd backend && go build ./... && go test ./internal/system/...`
Expected: build OK, tests PASS.

- [ ] **Step 7: Commit**

```bash
git add backend/internal/system/dirs.go backend/internal/system/dirs_test.go backend/cmd/server/main.go
git commit -m "feat(system): provision shared credential dir at boot"
git push
```

---

### Task 3: Remove `credEnv` from `BuildUserEnv`

**Files:**
- Modify: `backend/internal/pty/env.go:62-107` (drop `credEnv` param + loop)
- Modify: `backend/internal/pty/env_test.go:50-97` (update call site)

**Interfaces:**
- Produces: `func BuildUserEnv(cfg *config.Config, username, claudeConfigDir string) []string` (was `..., credEnv map[string]string)`).

- [ ] **Step 1: Update the test to the new signature (this is the failing test)**

In `backend/internal/pty/env_test.go`, edit `TestBuildUserEnv`:

Remove the `credEnv` map (lines 62-64) and change the call (line 65). The test should no longer assert `ANTHROPIC_AUTH_TOKEN=user-secret-token` from credEnv; instead assert the cfg-provided token still appears. Replace lines 62-80 with:

```go
	env := BuildUserEnv(cfg, "alice", "/data/alice/claude-config")
	j := strings.Join(env, "\n")

	for _, want := range []string{
		"HOME=/home/alice",
		"CLAUDE_CONFIG_DIR=/data/alice/claude-config",
		"ANTHROPIC_AUTH_TOKEN=tok",
		"ANTHROPIC_BASE_URL=http://gw",
		"HTTP_PROXY=http://p:7890",
		"http_proxy=http://p:7890",
		"API_TIMEOUT_MS=300000",
	} {
		if !strings.Contains(j, want) {
			t.Fatalf("env missing %q\n%s", want, j)
		}
	}
```

(Leave the PATH / CLAUDE_CONFIG_DIR / HOME assertions at lines 82-96 unchanged.)

- [ ] **Step 2: Run test to verify it fails**

Run: `cd backend && go test ./internal/pty/ -run TestBuildUserEnv -v`
Expected: FAIL / build error — `BuildUserEnv` still expects 4 args.

- [ ] **Step 3: Update the implementation**

In `backend/internal/pty/env.go`, change the `BuildUserEnv` signature and drop the credEnv loop. Edit line 62:

```go
func BuildUserEnv(cfg *config.Config, username, claudeConfigDir string) []string {
```

And delete lines 98-100 (the `for k, v := range credEnv { set(k, v) }` block).

- [ ] **Step 4: Run test to verify it passes**

Run: `cd backend && go test ./internal/pty/ -v`
Expected: PASS. (The package will fail to build at `server.go` because it still passes `credEnv` — that is fixed in Task 4. Do NOT run `go build ./...` yet.)

- [ ] **Step 5: Commit**

```bash
git add backend/internal/pty/env.go backend/internal/pty/env_test.go
git commit -m "refactor(pty): drop credEnv param from BuildUserEnv"
git push
```

Note: the repo will not build end-to-end until Task 4 lands. That is expected for this task boundary — `pty` package tests pass on their own.

---

### Task 4: Wire `SyncSharedCredentials` into `EnvFactory`; remove `resolveCredEnv`

**Files:**
- Modify: `backend/internal/server/server.go:104-110` (call sync, drop credEnv) and `:155-222` (delete `resolveCredEnv` + `credEnvFromSecrets`)
- Delete: `backend/internal/server/credential_injection_test.go`
- Modify: `backend/internal/server/admin_users.go` — NO change (kept as dead code per spec)
- Audit: `backend/internal/server/server_test.go`, `sessions_api_test.go`, `admin_users_test.go` for any `credEnv`/preset-injection assertions referencing the removed runtime path.

**Interfaces:**
- Consumes: `system.SyncSharedCredentials(username string, uid int) error` (Task 1), `pty.BuildUserEnv(cfg, username, dir)` (Task 3), `store.User` (has `.Username` and `.UID` fields — confirmed in `main.go:130`).

- [ ] **Step 1: Delete the obsolete injection test**

```bash
git rm backend/internal/server/credential_injection_test.go
```

- [ ] **Step 2: Update the EnvFactory in server.go**

In `backend/internal/server/server.go`, replace the body of `buildUserEnvFactory` (lines 105-109). The new body calls sync (non-fatal on error) then builds env without credEnv:

```go
func (s *Server) buildUserEnvFactory(u store.User) sessions.EnvFactory {
	return func(_ string, sessionID string) []string {
		if err := system.SyncSharedCredentials(u.Username, u.UID); err != nil {
			log.Printf("[server] warning: sync shared credentials for %s: %v", u.Username, err)
		}
		env := pty.BuildUserEnv(s.cfg, u.Username, "/data/"+u.Username+"/claude-config")
		return s.applyCaptureRouting(env, sessionID)
	}
}
```

Also update the doc comment above it (lines 80-103): remove references to "decrypts the user's bound credential preset" and `credEnv`. Replace the comment block with:

```go
// buildUserEnvFactory returns an EnvFactory that resolves the per-user env
// slice lazily at PTY spawn time. It first syncs the operator's shared
// credential files into the user's claude-config dir (non-fatal on failure),
// then builds the env. Returning a function (not a precomputed slice) lets a
// re-login take effect on the next Create/Restart without restarting the
// server.
//
// P5-T3 — env routing: the factory ALSO consults the per-session capture flag
// (capture.IsEnabled(sessionID)) and, when on, rewrites the returned env so
// the PTY's HTTP traffic goes through the MITM proxy:
//   - sets HTTP_PROXY / HTTPS_PROXY (+lower) = capture.ProxyURL();
//   - REMOVES ALL_PROXY / all_proxy so claude doesn't bypass the proxy via SOCKS.
//
// SECURITY: the shared credential file is copied onto disk under the user's
// own claude-config (0600, user-owned). It is never logged.
```

- [ ] **Step 3: Delete `resolveCredEnv` and `credEnvFromSecrets`**

In `backend/internal/server/server.go`, delete lines 155-222 (the `resolveCredEnv` method and `credEnvFromSecrets` function), including their doc comments.

- [ ] **Step 4: Fix imports in server.go**

`secrets` package import may now be unused (it was used only by `resolveCredEnv`). Run:

```bash
cd backend && goimports -w internal/server/server.go 2>/dev/null || go build ./internal/server/...
```

If `goimports` is unavailable, manually check `import (...)` in server.go: remove `"github.com/ldm0206/claude-docker/backend/internal/secrets"` if no other reference remains. The `system` package is already imported (used by `DefaultProvisioner`); confirm `system.SyncSharedCredentials` resolves. If `system` is NOT yet imported, add it.

- [ ] **Step 5: Audit and fix dependent tests**

Search for references to the removed symbols and to credEnv injection in tests:

Run: `cd backend && go build ./...`
Expected: build errors point at any remaining `resolveCredEnv` / `credEnv` / 4-arg `BuildUserEnv` callers.

Expected callers to check (from earlier grep):
- `server_test.go`, `sessions_api_test.go` — if they construct an EnvFactory or call `BuildUserEnv`, update to the 3-arg signature.
- `admin_users_test.go:499-561` — these test `BindCredential`/`CredentialPresetID` at the STORE/handler level. Per spec the store layer stays; these tests should STILL PASS unchanged (binding still works, it just has no runtime env effect). Do not delete them. Only delete if they assert env injection (they don't — they assert DB columns).

Fix each build error by updating the call site to the new signature, or removing assertions about injected env vars.

- [ ] **Step 6: Run the full server package tests**

Run: `cd backend && go test ./internal/server/... ./internal/pty/... ./internal/system/...`
Expected: PASS.

- [ ] **Step 7: Commit**

```bash
git add backend/internal/server/server.go backend/internal/server/credential_injection_test.go
# add any other test files fixed in step 5, by name
git commit -m "feat(server): sync shared creds at PTY spawn; remove preset injection"
git push
```

(If `git rm` already staged the deletion, `git add` of the deleted path is a no-op but harmless.)

---

### Task 5: End-to-end build + full suite green

**Files:** none (verification only)

- [ ] **Step 1: Full build**

Run: `cd backend && go build ./...`
Expected: OK.

- [ ] **Step 2: Full test suite**

Run: `cd backend && go test ./...`
Expected: PASS. Linux-only test files (`//go:build linux`) are skipped on this Windows host — that's expected; the host runs them.

- [ ] **Step 3: Report to user**

Report: build green, full suite green (noting linux-only files skipped locally), and that the change is committed and pushed. Remind the user that runtime verification (actual `claude login` into `/data/shared/claude-config`, then a user session picking it up) must be done by them on the Docker host, since `docker`/`wsl` are not available on this machine.

---

## Self-Review

**Spec coverage:**
- §1 shared dir + copy-on-spawn → Task 1 (sync fn), Task 2 (provision dir), Task 4 (wire into EnvFactory). ✓
- §1 `CLAUDE_CONFIG_DIR` stays per-user → Task 4 keeps `"/data/"+u.Username+"/claude-config"` literal. ✓
- §2 whitelist `.credentials*`, 0600, chown uid, overwrite, source-missing no-op, source-empty no-op → Task 1 tests cover all five. ✓
- §3 new `SyncSharedCredentials`, boot provision, error non-fatal → Task 1, Task 2, Task 4 (warning log). ✓
- §4 remove `credEnv` param + loop, remove `resolveCredEnv`, delete `credential_injection_test.go`, keep store/UI as dead code → Task 3, Task 4. ✓
- §5 tests for sync (5 cases), env_test signature update, audit server/sessions tests → Task 1, Task 3, Task 4 step 5. ✓

**Placeholder scan:** none — every step has concrete code or exact commands.

**Type consistency:** `SyncSharedCredentials(username string, uid int) error` — defined Task 1, called Task 4 with `(u.Username, u.UID)`. `BuildUserEnv(cfg, username, dir)` — 3-arg in Task 3 impl, 3-arg in Task 4 call. `store.User` fields `.Username`/`.UID` confirmed from `main.go:130`. ✓

No gaps found.
