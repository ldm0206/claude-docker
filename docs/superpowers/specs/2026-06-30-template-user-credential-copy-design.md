# Template-User Credential Copy — Design

**Date:** 2026-06-30
**Status:** Approved

## Goal

Replace the shared-directory credential sync with a template-user model:
one designated administrator's `claude login` is the single source of truth,
and every user's PTY picks up that `.credentials.json` at spawn time.

## Background

- The just-merged `SyncSharedConfig` copies `.credentials*` and
  `settings.json` from `/data/shared/claude-config` into each user's
  `/data/<user>/claude-config`. The shared directory must be populated by
  hand and the `settings.json` copy turned out to be unwanted.
- Per-user dirs: `/data/<user>/claude-config`, with `~/.claude` symlinked
  to it (`internal/system/dirs.go`). PTY spawn sets
  `CLAUDE_CONFIG_DIR=/data/<user>/claude-config` (`server.go`).
- Claude stores credentials at `~/.claude/.credentials.json`, mode `0600`.

## Decisions (confirmed with user)

1. **Template source = one designated user.** A config value names an
   existing user (typically an admin). That user runs `claude login` in
   their terminal; the resulting `~/.claude/.credentials.json` is the
   template. No new account, no shared directory.
2. **Copy only `.credentials.json`.** Nothing else is copied — no
   `settings.json`, no `.credentials.json.bak`, no other files. Each
   user keeps their own everything-else.
3. **Copy timing: PTY spawn** (every `EnvFactory` call = every
   `Manager.Start`/`Restart`), overwriting the target.
4. **No new admin page.** The template user updates credentials by
   running `claude login` in their own terminal. Zero frontend work for
   this feature.
5. **Delete the shared-dir mechanism:** `EnsureSharedCredentialDir`,
   `SyncSharedConfig` (+ internal helper), the `/data/shared/claude-config`
   provisioning, and the `settings.json` copy added in the prior commit.
6. **No credential present → PTY starts anyway.** Template user has no
   `.credentials.json` (source missing) is a no-op; claude reports
   not-logged-in. Never block session creation over a credential issue.
7. **UI changes from the prior commit (drop `▸`, warm-paper token refresh)
   are retained** — out of scope for this design; not reverted.

## Architecture

### Config

A new config field, e.g. `TemplateUser string`, read from env
`CLAUDE_TEMPLATE_USER` (empty = feature disabled). Validated at boot: if
set, the named user must exist in the store (else a warning is logged;
copy becomes a no-op). The template user's config dir resolves to
`/data/<templateUser>/claude-config` via the same `DataRoot` convention.

### Copy-on-spawn

A new function in `internal/system`:

```go
// CopyTemplateCredentials copies the template user's .credentials.json
// into the target user's claude-config dir. Source: <DataRoot>/<templateUser>/
// claude-config/.credentials.json. Target: <DataRoot>/<targetUser>/claude-config/
// .credentials.json. No-op (nil) if templateUser is empty, the source file is
// absent, or the template user == target user. The copied file is mode 0600,
// chown'd to uid.
func CopyTemplateCredentials(templateUser, targetUser string, uid int) error
```

Behavior:
- `templateUser == ""` → return nil (feature disabled).
- `templateUser == targetUser` → return nil (no self-copy).
- Source missing → return nil (no-op; template user not logged in yet).
- `os.ReadFile(src)` → `os.WriteFile(dst, data, 0o600)` → `os.Chown(dst, uid, uid)`.
- Per-step failure → log warning, return nil (non-fatal — session proceeds).

### Wiring into EnvFactory

In `server.go` `buildUserEnvFactory`, replace the `SyncSharedConfig` call
with:

```go
return func(_ string, sessionID string) []string {
    if err := system.CopyTemplateCredentials(s.cfg.TemplateUser, u.Username, u.UID); err != nil {
        log.Printf("[server] warning: copy template credentials for %s: %v", u.Username, err)
    }
    env := pty.BuildUserEnv(s.cfg, u.Username, "/data/"+u.Username+"/claude-config")
    return s.applyCaptureRouting(env, sessionID)
}
```

`CLAUDE_CONFIG_DIR` stays per-user. Only `.credentials.json` is shared.

### Removing the shared-dir mechanism

**Delete:**
- `internal/system/cred_sync.go` (the whole file: `syncSharedConfig` +
  `SyncSharedConfig`).
- `internal/system/cred_sync_test.go` (the whole file).
- `internal/system/dirs.go`: `EnsureSharedCredentialDir` function + its
  boot-time call site (search for the caller; remove the call).
- The `settings.json`-specific test assertions (gone with the test file).
- Any boot/provision call to `EnsureSharedCredentialDir` (e.g. in the
  server boot path or `ensureUsersProvisioned`).

**Keep:**
- `EnsureSharedCredentialDir`'s removal does not touch `ProvisionUserDirs`.
- The prior-commit `dirs.go` doc comment that references `SyncSharedConfig`
  is updated/removed as part of deleting the function.

## Testing

New tests for `CopyTemplateCredentials` (in `internal/system`, `//go:build linux`):

1. **Happy path:** template dir has `.credentials.json`; after call,
   target has it, mode `0600`, owner = uid.
2. **Only .credentials.json copied:** template dir also has `settings.json`
   and `projects/`; target gets only `.credentials.json`.
3. **Template user empty:** `templateUser == ""` → nil, target unchanged.
4. **Self-copy skipped:** `templateUser == targetUser` → nil, no write.
5. **Source missing:** template user has no `.credentials.json` → nil,
   target unchanged.
6. **Overwrite:** target has stale `.credentials.json`; source has new →
   overwritten.

Audit `server_test.go` / `sessions_api_test.go` for any reference to the
old `SyncSharedConfig` / shared dir and clean up.

## Out of scope

- Copying any file other than `.credentials.json`.
- A web UI for managing the template credential.
- Migrating existing `/data/shared/claude-config` contents — the operator
  re-runs `claude login` as the template user.
- Cleaning up root-owned stale `settings.json` in existing user dirs —
  operator's responsibility.
