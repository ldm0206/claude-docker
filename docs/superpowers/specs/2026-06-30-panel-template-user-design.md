# Template User — Panel-Selectable Setting — Design

**Date:** 2026-06-30
**Status:** Approved

## Goal

Let an admin pick the "template user" (whose `.credentials.json` is copied to
every user at PTY spawn) from the admin Users page, persisted in the DB,
instead of only via env. The env (`CLAUDE_TEMPLATE_USER`) is retained as a
fallback. This replaces nothing from the prior commits — it is purely
additive.

## Background

- Prior commits (87e40dd..2eaf36c) added:
  - `Config.TemplateUser` from env `CLAUDE_TEMPLATE_USER` (`internal/config/config.go`).
  - `system.CopyTemplateCredentials(templateUser, targetUser, uid)` — copies ONLY `.credentials.json`, 0600, chown uid, overwrite, no-op on empty / self / missing-source, non-fatal (`internal/system/template_cred.go`).
  - Wiring in `server.go buildUserEnvFactory`: `CopyTemplateCredentials(s.cfg.TemplateUser, u.Username, u.UID)`.
  - Boot warning if `cfg.TemplateUser` names an unknown user (`cmd/server/main.go`).
- Store: embedded `schema.sql` (`//go:embed`), applied idempotently at `Open` via `CREATE TABLE IF NOT EXISTS` + defensive `ALTER TABLE` (`internal/store/store.go`). No external migration runner.
- Admin routes registered in `server.go` under `r.Group(func(r){ r.Use(s.requireAdmin); ... })`. Handlers live in `internal/server/admin_*.go`, use `s.db` (`*store.DB`).
- `store.DB` has `GetUserByUsername(name) (User, error)`, `ListUsers() ([]User, error)`, `ErrNotFound`.
- Web SPA: vanilla JS in `web/src/main.js`; admin views via `VIEWS[view]`. `viewAdminUsers` renders `/api/admin/users` into a table.

## Decisions (confirmed with user)

1. **Panel option on the existing Users page** (not a new page). A "Template
   user" `<select>` populated by all `role='admin'` users.
2. **Persisted in the DB** via a generic KV settings table.
3. **DB value takes precedence over env.** Effective template user =
   DB setting if set (non-empty); else fall back to `cfg.TemplateUser`
   (env `CLAUDE_TEMPLATE_USER`); else disabled.
4. **Candidates = all admin users** (`role='admin'`).
5. **Keep env** — `CLAUDE_TEMPLATE_USER` stays as fallback; not deleted.
6. **Prior commits retained; pure addition.** The only change to existing
   code is `buildUserEnvFactory` reading the resolved template user from a
   new resolver instead of directly from `cfg.TemplateUser`.

## Architecture

### Settings KV table

Add to `internal/store/schema.sql`:

```sql
CREATE TABLE IF NOT EXISTS settings (
  key TEXT PRIMARY KEY,
  value TEXT NOT NULL
);
```

Generic KV — future panel settings reuse it. The only key used here is
`template_user` (value = a username).

### Store methods (new file `internal/store/settings.go`)

```go
// GetSetting returns the value for key, or "" with a nil error if absent.
func (d *DB) GetSetting(key string) (string, error)
// SetSetting upserts key=value.
func (d *DB) SetSetting(key, value string) error
```

`GetSetting`: `SELECT value FROM settings WHERE key=?`; on `sql.ErrNoRows`
return `"", nil` (absent is not an error). `SetSetting`:
`INSERT INTO settings (key,value) VALUES(?,?) ON CONFLICT(key) DO UPDATE SET value=excluded.value`.

### Template-user resolver

The effective template user must be resolved at spawn time (so a panel
change takes effect on the next session without a server restart). Add a
method on `*Server` (or a small helper) used by `buildUserEnvFactory`:

```go
// resolveTemplateUser returns the effective template username: the DB
// setting if set, else cfg.TemplateUser (env). Empty = disabled.
func (s *Server) resolveTemplateUser() string {
    if v, err := s.db.GetSetting("template_user"); err == nil && v != "" {
        return v
    }
    return s.cfg.TemplateUser
}
```

`buildUserEnvFactory` becomes:

```go
if err := system.CopyTemplateCredentials(s.resolveTemplateUser(), u.Username, u.UID); err != nil {
    log.Printf("[server] warning: copy template credentials for %s: %v", u.Username, err)
}
```

### Admin API (new file `internal/server/admin_settings.go`)

Two endpoints, registered in the admin group (`r.Use(s.requireAdmin)`):

- `GET /api/admin/settings/template-user` → `200 {"template_user":"alice"}`
  (empty string when unset).
- `PUT /api/admin/settings/template-user` body `{"template_user":"alice"}`
  → validates the username is an existing `role='admin'` user
  (`db.GetUserByUsername` + role check), else `400`; on success upserts the
  setting and returns `200 {"template_user":"alice"}`.

`PUT` accepts empty string `""` to clear the setting (falls back to env).

### Frontend (`web/src/main.js`)

In `viewAdminUsers`, render a settings card above the users table with a
`<select id="tpl-user">` populated from the admin users already fetched
(`users.filter(u => u.role === 'admin')`) plus an explicit "(env / disabled)"
option with empty value. On load, `GET /api/admin/settings/template-user`
selects the current value. On change, `PUT` the new value.

## Testing

New `internal/store/settings_test.go` (`//go:build linux` not required — pure
sqlite, runs on Windows too, matching `presets_test.go`/`users_test.go`
which have no build tag):

1. `GetSetting` absent key → `"", nil`.
2. `SetSetting` then `GetSetting` → round-trips the value.
3. `SetSetting` twice (upsert) → second value wins.

New `internal/server/admin_settings_test.go` (mirror `admin_credentials_test.go`
httptest style; no build tag):

1. `GET` unset → `200 {"template_user":""}`.
2. `PUT` valid admin username → `200`; subsequent `GET` returns it.
3. `PUT` non-admin username → `400`.
4. `PUT` unknown username → `400`.
5. `PUT ""` → `200`, clears (GET returns `""`).

`resolveTemplateUser` is exercised transitively by the admin_settings tests
(GET reads it) and is trivial; no separate unit test required.

## Out of scope

- Migrating existing env deployments — env still works as fallback.
- Copying any file other than `.credentials.json`.
- A dedicated settings page — the control lives on Users.
- Per-user template overrides.
