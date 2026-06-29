# Shared Credential Sync — Design

**Date:** 2026-06-30
**Status:** Approved (pending spec review)

## Goal

Operator runs `claude login` once; every user's PTY picks up that
credential at spawn time. No per-user login required.

## Background

Today credentials are per-user isolated:

- Each user gets `/data/<user>/claude-config`, with `~/.claude` symlinked
  to it (`internal/system/dirs.go:44-57`).
- PTY spawn sets `CLAUDE_CONFIG_DIR=/data/<user>/claude-config`
  (`internal/server/server.go:107`).
- A second path exists: admin binds a credential preset per user, and
  `resolveCredEnv` decrypts it into the PTY env (`server.go:104-110`,
  `pty.BuildUserEnv` `credEnv` param).

## Decisions (confirmed with user)

1. **Source:** the operator's own `claude login` file (not the admin
   preset path).
2. **Write access:** operator-only. Other users read-only.
3. **Ownership:** fixed path, root-owned. Operator runs `claude login`
   as root against the shared dir.
4. **Preset path:** the runtime injection path is removed. The store
   layer and admin UI remain as dead code (no DB migration).
5. **Mechanism:** copy-on-spawn (option B), not symlink or env injection.
6. **Copy timing:** at PTY spawn (every `EnvFactory` call = every
   `Manager.Start`/`Restart`).
7. **No credential present:** PTY starts anyway; claude reports
   not-logged-in. Never block session creation over a credential issue.

## Architecture

### Shared source directory

- Path: `/data/shared/claude-config` (`DataRoot + "/shared/claude-config"`).
- Owner: root. Mode: `0700`.
- Provisioned at boot (and/or in `provisionDirs`) if absent.
- The operator runs `claude login` with `CLAUDE_CONFIG_DIR` pointing
  here (or `HOME=/root` with this dir wired). `claude login` writes
  `.credentials.json` and possibly accompanying `.credentials*` files.

### Copy-on-spawn

A new function in `internal/system`:

```go
// SyncSharedCredentials copies credential files (.credentials*) from
// the shared source dir into the user's claude-config dir. Source
// missing or empty is a no-op (not an error). Files are chown'd to
// uid, mode 0600. Target path is per-username, so both are required.
func SyncSharedCredentials(username string, uid int) error
```

Behavior:
- Source: `DataRoot + "/shared/claude-config"`.
- Target: the existing per-user `DataRoot + "/<user>/claude-config"`
  (created by `ProvisionUserDirs`).
- Whitelist: copy only entries whose name matches `.credentials*`.
  `settings.json`, `projects/`, `todos/`, `statsig/` etc. are NOT
  copied — each user keeps their own.
- Per file: `os.ReadFile` (source) → `os.WriteFile` (target, mode
  `0600`) → `os.Chown(target, uid, uid)`.
- Source dir missing → return nil (no-op).
- Source dir present but no `.credentials*` → return nil (no-op).
- Per-file copy failure → log warning, continue remaining files, do
  not abort.
- Existing target file with same name → overwritten.

### Wiring into EnvFactory

In `server.go` `buildUserEnvFactory`, before `BuildUserEnv`:

```go
return func(_ string, sessionID string) []string {
    if err := system.SyncSharedCredentials(u.UID); err != nil {
        s.log.Warn("sync shared credentials", "err", err) // non-fatal
    }
    env := pty.BuildUserEnv(s.cfg, u.Username, "/data/"+u.Username+"/claude-config")
    return s.applyCaptureRouting(env, sessionID)
}
```

`EnvFactory` runs on every `Start`/`Restart`, so:
- New sessions: credential copied before claude reads it.
- Existing sessions after operator re-login: a `Restart` of the session
  re-runs the factory and picks up the new credential. No server restart
  needed.

`CLAUDE_CONFIG_DIR` stays per-user (`/data/<user>/claude-config`). Only
the credential file is shared; other config and session history remain
per-user.

### Removing the preset injection path

**Removed (runtime):**
- `pty/env.go`: drop `credEnv map[string]string` param from
  `BuildUserEnv`; delete the `for k, v := range credEnv` loop.
- `pty/env_test.go`: update call sites to the new signature.
- `server.go`: delete `resolveCredEnv`; delete the
  `credEnv := s.resolveCredEnv(u)` line in `buildUserEnvFactory`.
- `credential_injection_test.go`: delete the file (it tests the removed
  path).

**Kept as dead code (no DB migration):**
- store preset table and methods (`BindCredential`, `CreatePreset`, …).
- `admin_credentials.go` handler and its UI.

### Error handling policy

Consistent with the existing "corrupt preset is graceful" policy: a
credential problem must never block session creation. `SyncSharedCredentials`
failures are logged at WARNING and the session proceeds; claude will
report not-logged-in if no credential arrived.

## Testing

New tests for `SyncSharedCredentials` (in `internal/system`):

1. **Happy path:** source has `.credentials.json`; after call, target
   contains it, mode `0600`, owner = uid.
2. **Whitelist:** source also has `settings.json` and `projects/`;
   target gets only `.credentials*`.
3. **Source missing:** source dir absent → nil, target unchanged.
4. **Source empty:** source present, no `.credentials*` → nil, target
   unchanged.
5. **Overwrite:** target has stale `.credentials.json`; source has new
   → overwritten.

`pty/env_test.go`: assert `BuildUserEnv` new signature (no `credEnv`).

Audit `server_test.go` / `sessions_api_test.go` for any `credEnv` /
preset-injection assertions and clean them up.

## Out of scope

- Auto-push on source change (inotify) — rejected; Restart covers it.
- Deleting the preset store table / admin UI — deferred (dead code only).
- Multiple shared credentials (per-team) — not requested.
