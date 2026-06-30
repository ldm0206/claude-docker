# Template-User Credential Copy Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Replace the shared-directory credential sync with a template-user model — copy `.credentials.json` only, from a designated template user's claude-config into each user's at PTY spawn.

**Architecture:** Add a `TemplateUser` config field (env `CLAUDE_TEMPLATE_USER`). At PTY spawn, `CopyTemplateCredentials(templateUser, targetUser, uid)` reads `<DataRoot>/<templateUser>/claude-config/.credentials.json` and writes it to `<DataRoot>/<targetUser>/claude-config/.credentials.json` (mode 0600, chown uid, overwrite, no-op if source absent or template==target). Delete the entire shared-dir mechanism (`EnsureSharedCredentialDir`, `SyncSharedConfig` + tests, boot call, the prior `settings.json` copy).

**Tech Stack:** Go 1.26 (chi, modernc sqlite), `//go:build linux` test files run in-container only.

## Global Constraints

- Go conventions: no comments unless WHY is non-obvious; no emojis in code.
- `internal/system/*_test.go` are `//go:build linux` — not runnable on the Windows dev host; compile/vet on host (`go build ./... && go vet ./...`), run the suite in-container.
- Auto-commit rule (CLAUDE.md): after each task, stage ONLY the files you changed by name (never `-A`/`.`); never commit `backend/server.exe` (untracked stray); new commit (never amend); conventional-commit message; `git push` to origin/main.
- The copy whitelist is EXACTLY the single file `.credentials.json` — nothing else. Files written mode `0600`, chown'd to uid, overwrite existing, source missing/template==target = no-op, per-step failure logged + skipped (non-fatal, never blocks session creation).
- `CLAUDE_CONFIG_DIR` stays per-user (`/data/<user>/claude-config`); only `.credentials.json` is shared.

---

## File Structure

- `backend/internal/config/config.go` — add `TemplateUser` field + env read. (responsibility: app config)
- `backend/internal/system/cred_sync.go` — **delete the file**; replaced by `template_cred.go`. (responsibility: gone)
- `backend/internal/system/cred_sync_test.go` — **delete the file**. (responsibility: gone)
- `backend/internal/system/template_cred.go` — **create**; holds `CopyTemplateCredentials`. (responsibility: copy template user's .credentials.json into a target user's dir)
- `backend/internal/system/template_cred_test.go` — **create**; `//go:build linux` tests for the copy. (responsibility: lock the copy contract)
- `backend/internal/system/dirs.go` — delete `EnsureSharedCredentialDir` + its doc comment. (responsibility: per-user dir provisioning only)
- `backend/internal/system/dirs_test.go` — delete `TestEnsureSharedCredentialDir`. (responsibility: dirs tests only)
- `backend/cmd/server/main.go` — delete the `EnsureSharedCredentialDir` boot call. (responsibility: server boot)
- `backend/internal/server/server.go` — replace the `SyncSharedConfig` call with `CopyTemplateCredentials`; update the doc comment. (responsibility: PTY env factory)

---

## Task 1: Add TemplateUser config field (TDD-free wiring, verified by build)

**Files:**
- Modify: `backend/internal/config/config.go`

**Interfaces:**
- Produces: `Config.TemplateUser string` (read from env `CLAUDE_TEMPLATE_USER`). Consumed by `server.go` in Task 3.

- [ ] **Step 1: Add the field + env read**

In `backend/internal/config/config.go`, add the field to the `Config` struct (after `CookieSameSite`):

```go
type Config struct {
	AnthropicAPIKey        string
	AnthropicAuthToken     string
	AnthropicBaseURL       string
	HTTPProxy              string
	HTTPSProxy             string
	AllProxy               string
	NoProxy                string
	APITimeoutMS           int
	Port                   int
	SessionSecret          string
	BootstrapAdminUser     string
	BootstrapAdminPassword string
	CookieSameSite         string
	TemplateUser           string
}
```

In `Load`, add the read (after the `c.CookieSameSite = opt("COOKIE_SAMESITE")` line, before the `if c.CookieSameSite == ""` block):

```go
	c.TemplateUser = opt("CLAUDE_TEMPLATE_USER")
```

- [ ] **Step 2: Verify build + vet**

Run: `cd backend && go build ./... && go vet ./...`
Expected: clean, exit 0.

- [ ] **Step 3: Commit + push**

```bash
git -C C:/PythonProject/claude-docker add backend/internal/config/config.go
git -C C:/PythonProject/claude-docker commit -m "feat(config): add TemplateUser field (CLAUDE_TEMPLATE_USER)"
git -C C:/PythonProject/claude-docker push
```

---

## Task 2: Implement CopyTemplateCredentials (TDD)

**Files:**
- Create: `backend/internal/system/template_cred.go`
- Create: `backend/internal/system/template_cred_test.go`

**Interfaces:**
- Consumes: `DataRoot` (package var in `internal/system/dirs.go`).
- Produces: `CopyTemplateCredentials(templateUser, targetUser string, uid int) error` — public; consumed by `server.go` in Task 3. No-op (nil) if `templateUser == ""`, `templateUser == targetUser`, or source file absent. Writes `0600` + chown uid; per-step failure logged + skipped (non-fatal).

- [ ] **Step 1: Write the failing tests**

Create `backend/internal/system/template_cred_test.go` with:

```go
//go:build linux

package system

import (
	"os"
	"path/filepath"
	"testing"
)

func writeCred(t *testing.T, dir, name, content string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
}

func TestCopyTemplateCredentials_HappyPath(t *testing.T) {
	data := t.TempDir()
	orig := DataRoot
	t.Cleanup(func() { DataRoot = orig })
	DataRoot = data

	srcDir := filepath.Join(data, "tpl", "claude-config")
	dstDir := filepath.Join(data, "bob", "claude-config")
	if err := os.MkdirAll(srcDir, 0o700); err != nil { t.Fatal(err) }
	if err := os.MkdirAll(dstDir, 0o700); err != nil { t.Fatal(err) }
	writeCred(t, srcDir, ".credentials.json", `{"token":"abc"}`)

	if err := CopyTemplateCredentials("tpl", "bob", 2000); err != nil {
		t.Fatalf("copy: %v", err)
	}
	b, err := os.ReadFile(filepath.Join(dstDir, ".credentials.json"))
	if err != nil { t.Fatalf("target missing: %v", err) }
	if string(b) != `{"token":"abc"}` { t.Fatalf("content = %q", string(b)) }
	fi, _ := os.Stat(filepath.Join(dstDir, ".credentials.json"))
	if fi.Mode().Perm() != 0o600 { t.Fatalf("perm = %o, want 0600", fi.Mode().Perm()) }
}

// Only .credentials.json is copied; other files in the template dir are ignored.
func TestCopyTemplateCredentials_OnlyCredentialsCopied(t *testing.T) {
	data := t.TempDir()
	orig := DataRoot
	t.Cleanup(func() { DataRoot = orig })
	DataRoot = data

	srcDir := filepath.Join(data, "tpl", "claude-config")
	dstDir := filepath.Join(data, "bob", "claude-config")
	if err := os.MkdirAll(srcDir, 0o700); err != nil { t.Fatal(err) }
	if err := os.MkdirAll(dstDir, 0o700); err != nil { t.Fatal(err) }
	writeCred(t, srcDir, ".credentials.json", `x`)
	writeCred(t, srcDir, "settings.json", `y`)
	if err := os.MkdirAll(filepath.Join(srcDir, "projects"), 0o755); err != nil { t.Fatal(err) }

	if err := CopyTemplateCredentials("tpl", "bob", 2000); err != nil {
		t.Fatalf("copy: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dstDir, "settings.json")); !os.IsNotExist(err) {
		t.Fatalf("settings.json must NOT be copied: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dstDir, "projects")); !os.IsNotExist(err) {
		t.Fatalf("projects/ must NOT be copied: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dstDir, ".credentials.json")); err != nil {
		t.Fatalf(".credentials.json should be copied: %v", err)
	}
}

func TestCopyTemplateCredentials_EmptyTemplateUser(t *testing.T) {
	data := t.TempDir()
	orig := DataRoot
	t.Cleanup(func() { DataRoot = orig })
	DataRoot = data
	dstDir := filepath.Join(data, "bob", "claude-config")
	if err := os.MkdirAll(dstDir, 0o700); err != nil { t.Fatal(err) }

	if err := CopyTemplateCredentials("", "bob", 2000); err != nil {
		t.Fatalf("empty templateUser must be no-op, got: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dstDir, ".credentials.json")); !os.IsNotExist(err) {
		t.Fatalf("nothing should be copied: %v", err)
	}
}

func TestCopyTemplateCredentials_SelfCopySkipped(t *testing.T) {
	data := t.TempDir()
	orig := DataRoot
	t.Cleanup(func() { DataRoot = orig })
	DataRoot = data
	dir := filepath.Join(data, "tpl", "claude-config")
	if err := os.MkdirAll(dir, 0o700); err != nil { t.Fatal(err) }
	writeCred(t, dir, ".credentials.json", `orig`)

	if err := CopyTemplateCredentials("tpl", "tpl", 2000); err != nil {
		t.Fatalf("self-copy must be no-op, got: %v", err)
	}
	// File untouched.
	b, _ := os.ReadFile(filepath.Join(dir, ".credentials.json"))
	if string(b) != `orig` { t.Fatalf("self-copy clobbered content: %q", string(b)) }
}

func TestCopyTemplateCredentials_SourceMissing(t *testing.T) {
	data := t.TempDir()
	orig := DataRoot
	t.Cleanup(func() { DataRoot = orig })
	DataRoot = data
	srcDir := filepath.Join(data, "tpl", "claude-config")
	dstDir := filepath.Join(data, "bob", "claude-config")
	if err := os.MkdirAll(srcDir, 0o700); err != nil { t.Fatal(err) } // exists, no .credentials.json
	if err := os.MkdirAll(dstDir, 0o700); err != nil { t.Fatal(err) }

	if err := CopyTemplateCredentials("tpl", "bob", 2000); err != nil {
		t.Fatalf("missing source must be no-op, got: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dstDir, ".credentials.json")); !os.IsNotExist(err) {
		t.Fatalf("nothing should be copied: %v", err)
	}
}

func TestCopyTemplateCredentials_Overwrite(t *testing.T) {
	data := t.TempDir()
	orig := DataRoot
	t.Cleanup(func() { DataRoot = orig })
	DataRoot = data
	srcDir := filepath.Join(data, "tpl", "claude-config")
	dstDir := filepath.Join(data, "bob", "claude-config")
	if err := os.MkdirAll(srcDir, 0o700); err != nil { t.Fatal(err) }
	if err := os.MkdirAll(dstDir, 0o700); err != nil { t.Fatal(err) }
	writeCred(t, srcDir, ".credentials.json", `new`)
	writeCred(t, dstDir, ".credentials.json", `old`)

	if err := CopyTemplateCredentials("tpl", "bob", 2000); err != nil {
		t.Fatalf("copy: %v", err)
	}
	b, _ := os.ReadFile(filepath.Join(dstDir, ".credentials.json"))
	if string(b) != `new` { t.Fatalf("not overwritten, content = %q", string(b)) }
}
```

- [ ] **Step 2: Verify the tests fail (undefined symbol)**

Run: `cd backend && GOOS=linux go vet ./internal/system/`
Expected: compile error referencing `CopyTemplateCredentials` (undefined). (On Windows the `linux`-tagged tests are excluded from the default build, so cross-compile vet with `GOOS=linux` is the RED signal.)

- [ ] **Step 3: Implement CopyTemplateCredentials**

Create `backend/internal/system/template_cred.go` with:

```go
package system

import (
	"io/fs"
	"log"
	"os"
	"path/filepath"
)

// CopyTemplateCredentials copies the template user's .credentials.json into the
// target user's claude-config dir. Source: <DataRoot>/<templateUser>/claude-config/
// .credentials.json. Target: <DataRoot>/<targetUser>/claude-config/.credentials.json.
// No-op (nil) if templateUser is empty, templateUser == targetUser, or the source
// file is absent. The copied file is mode 0600, chown'd to uid. A per-step failure
// is logged and skipped; it never blocks session creation.
func CopyTemplateCredentials(templateUser, targetUser string, uid int) error {
	if templateUser == "" || templateUser == targetUser {
		return nil
	}
	src := filepath.Join(DataRoot, templateUser, "claude-config", ".credentials.json")
	dst := filepath.Join(DataRoot, targetUser, "claude-config", ".credentials.json")
	data, err := os.ReadFile(src)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return nil
		}
		log.Printf("[system] warning: read template credential %s: %v", src, err)
		return nil
	}
	if err := os.WriteFile(dst, data, 0o600); err != nil {
		log.Printf("[system] warning: write credential %s: %v", dst, err)
		return nil
	}
	if err := os.Chown(dst, uid, uid); err != nil {
		log.Printf("[system] warning: chown credential %s: %v", dst, err)
	}
	return nil
}
```

Add `"errors"` to the import block (after `"io/fs"`):

```go
import (
	"errors"
	"io/fs"
	"log"
	"os"
	"path/filepath"
)
```

- [ ] **Step 4: Verify build + vet (host) and linux-tagged compile**

Run: `cd backend && go build ./... && go vet ./... && GOOS=linux go vet ./internal/system/`
Expected: clean, exit 0. (The `linux`-tagged tests now compile; the suite runs in-container on the host.)

- [ ] **Step 5: Commit + push**

```bash
git -C C:/PythonProject/claude-docker add backend/internal/system/template_cred.go backend/internal/system/template_cred_test.go
git -C C:/PythonProject/claude-docker commit -m "feat(system): add CopyTemplateCredentials for .credentials.json copy-on-spawn"
git -C C:/PythonProject/claude-docker push
```

---

## Task 3: Wire into EnvFactory + delete the shared-dir mechanism

**Files:**
- Modify: `backend/internal/server/server.go` (call site ~L95-102 + doc comment ~L79-94)
- Modify: `backend/internal/system/dirs.go` (delete `EnsureSharedCredentialDir` + its doc comment, ~L68-81)
- Modify: `backend/internal/system/dirs_test.go` (delete `TestEnsureSharedCredentialDir`, ~L70-93)
- Modify: `backend/cmd/server/main.go` (delete the boot call ~L60-62)
- Delete: `backend/internal/system/cred_sync.go`
- Delete: `backend/internal/system/cred_sync_test.go`

**Interfaces:**
- Consumes: `CopyTemplateCredentials` (Task 2), `s.cfg.TemplateUser` (Task 1).
- Produces: none new.

- [ ] **Step 1: Replace the EnvFactory call site + doc comment in server.go**

In `backend/internal/server/server.go`, replace the doc comment block above `buildUserEnvFactory` (lines ~79-94, from `// buildUserEnvFactory returns an EnvFactory...` through `// own claude-config (0600, user-owned). They are never logged.`) with:

```go
// buildUserEnvFactory returns an EnvFactory that resolves the per-user env
// slice lazily at PTY spawn time. It first copies the template user's
// .credentials.json into the user's claude-config dir (non-fatal on failure),
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
// SECURITY: the template credential file is copied onto disk under the user's
// own claude-config (0600, user-owned). It is never logged.
```

Replace the call inside the returned closure (lines ~96-99):

```go
	return func(_ string, sessionID string) []string {
		if err := system.CopyTemplateCredentials(s.cfg.TemplateUser, u.Username, u.UID); err != nil {
			log.Printf("[server] warning: copy template credentials for %s: %v", u.Username, err)
		}
```

(Leave the `env := pty.BuildUserEnv(...)` and `return s.applyCaptureRouting(...)` lines unchanged.)

- [ ] **Step 2: Delete EnsureSharedCredentialDir from dirs.go**

In `backend/internal/system/dirs.go`, delete the entire `EnsureSharedCredentialDir` function and its preceding doc comment (lines ~68-81, from `// EnsureSharedCredentialDir idempotently creates...` through the closing `}` of the function). The file ends after `ProvisionUserDirs`.

Resulting `dirs.go` tail (after `ProvisionUserDirs`):

```go
func ProvisionUserDirs(username string, uid int) error {
	if err := validateUsername(username); err != nil {
		return err
	}
	return provisionDirs(HomeRoot, DataRoot, username, uid)
}
```

- [ ] **Step 3: Delete TestEnsureSharedCredentialDir from dirs_test.go**

In `backend/internal/system/dirs_test.go`, delete the entire `TestEnsureSharedCredentialDir` function (lines ~70-93, from its `// TestEnsureSharedCredentialDir verifies...` comment through the closing `}`). Keep `TestProvisionDirs_CreatesClaudeSymlink` and `TestProvisionDirs_SkipsExistingClaude`.

- [ ] **Step 4: Delete the boot call in main.go**

In `backend/cmd/server/main.go`, delete these three lines (~L60-62):

```go
	if err := system.EnsureSharedCredentialDir(); err != nil {
		log.Fatalf("[server] ensure shared credential dir: %v", err)
	}
```

Leave the surrounding `BootstrapAdmin` and `ensureUsersProvisioned` calls intact.

- [ ] **Step 5: Delete cred_sync.go and cred_sync_test.go**

Run:

```bash
git -C C:/PythonProject/claude-docker rm backend/internal/system/cred_sync.go backend/internal/system/cred_sync_test.go
```

- [ ] **Step 6: Verify build + vet (host and linux-tagged)**

Run: `cd backend && go build ./... && go vet ./... && GOOS=linux go vet ./...`
Expected: clean, exit 0. No dangling references to `SyncSharedConfig`, `SyncSharedCredentials`, `EnsureSharedCredentialDir`, or `shared/claude-config`.

- [ ] **Step 7: Commit + push**

```bash
git -C C:/PythonProject/claude-docker add backend/internal/server/server.go backend/internal/system/dirs.go backend/internal/system/dirs_test.go backend/cmd/server/main.go
git -C C:/PythonProject/claude-docker commit -m "$(cat <<'EOF'
feat(server): copy template user's .credentials.json on spawn; drop shared dir

Replace SyncSharedConfig with CopyTemplateCredentials wired via
cfg.TemplateUser. Delete EnsureSharedCredentialDir, the shared/claude-config
provisioning, and the cred_sync package.
EOF
)"
git -C C:/PythonProject/claude-docker push
```

(The `git rm` in Step 5 already staged the two deletions; the `git add` here stages the four modified files. All six changes ship in one commit.)

---

## Self-Review

**1. Spec coverage:**
- Config `TemplateUser` from `CLAUDE_TEMPLATE_USER` → Task 1.
- `CopyTemplateCredentials` (only `.credentials.json`, 0600, chown, overwrite, no-op on empty/self/missing, non-fatal) → Task 2 (tests in Step 1 cover all six cases; impl in Step 3).
- Wire into `buildUserEnvFactory` → Task 3 Step 1.
- Delete `EnsureSharedCredentialDir` + boot call + `SyncSharedConfig` + `cred_sync*.go` + `settings.json` copy → Task 3 Steps 2-5.
- `dirs_test.go` cleanup (drop `TestEnsureSharedCredentialDir`) → Task 3 Step 3.
- Audit `server_test.go`/`sessions_api_test.go`: grep in Task 3 Step 6 catches any dangling reference; the only prior references were the now-deleted call site — none in tests (verified during planning). Covered.
- No new admin page → no frontend task. Covered (by absence).
- UI changes retained → no revert. Covered.

**2. Placeholder scan:** No TBD/TODO/"add error handling". Every code step shows full code; commands have expected output. The `git rm` and `git add` are concrete.

**3. Type/name consistency:** `CopyTemplateCredentials(templateUser, targetUser string, uid int) error` — same signature in Task 2 impl, Task 2 tests, and Task 3 call site (`s.cfg.TemplateUser, u.Username, u.UID`). `Config.TemplateUser` (Task 1) matches `s.cfg.TemplateUser` (Task 3). `DataRoot` package var reused consistently. No drift.
