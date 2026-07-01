# Anthropic credentials — web-admin managed (design)

Date: 2026-07-01

## Context

`claude-docker` currently has two parallel mechanisms for getting Anthropic
credentials into a user's terminal session:

1. **Environment variables** read at server startup into `cfg`:
   `ANTHROPIC_API_KEY`, `ANTHROPIC_AUTH_TOKEN`, `ANTHROPIC_BASE_URL`. These are
   injected into the PTY env in `pty/env.go` (`BuildClaudeEnv` and
   `BuildUserEnv`).

2. **Template-user credential copy**: an admin selects an admin user as the
   "template user" via the panel (`/api/admin/settings/template-user`). At
   session start `system.CopyTemplateCredentials` copies that user's
   `~/.claude/.credentials.json` into the target user's `~/.claude/`. Stored in
   DB `settings[template_user]`; `resolveTemplateUser()` falls back to
   `cfg.TemplateUser` (`CLAUDE_TEMPLATE_USER`).

The user wants to remove mechanism #2 entirely and remove the env-var sources
of mechanism #1. Instead, all three Anthropic values are managed by the admin
on the web panel, stored in the DB `settings` table (plaintext), and injected
into every user's terminal env. An empty value means "do not inject that
variable."

## Decisions (confirmed in brainstorming)

- **Storage**: plaintext in DB `settings` table.
- **Echo**: GET returns the current plaintext values (no masking).
- **Single endpoint**: all three values share one GET + one PUT at
  `/api/admin/settings/anthropic`.
- **No env-var fallback**: `ANTHROPIC_API_KEY` / `ANTHROPIC_AUTH_TOKEN` /
  `ANTHROPIC_BASE_URL` / `CLAUDE_TEMPLATE_USER` are no longer read at startup.
  The corresponding `config.Config` fields are deleted.
- **Template-user UI card** is replaced by the new credentials card.

## Architecture

### Settings keys (DB `settings` table)

| key                       | env var injected        |
|---------------------------|-------------------------|
| `anthropic_api_key`       | `ANTHROPIC_API_KEY`     |
| `anthropic_base_url`      | `ANTHROPIC_BASE_URL`    |
| `anthropic_auth_token`    | `ANTHROPIC_AUTH_TOKEN`  |

### Components

#### `backend/internal/config/config.go`
- Delete fields: `AnthropicAPIKey`, `AnthropicAuthToken`, `AnthropicBaseURL`,
  `TemplateUser`.
- Delete the corresponding `opt(...)` reads in `Load`.
- `config_test.go`: drop the assertions on the removed fields.

#### `backend/internal/server/admin_settings.go`
Replace the template-user group entirely with:

- `const apiKeyKey = "anthropic_api_key"`, `baseURLKey = "anthropic_base_url"`,
  `authTokenKey = "anthropic_auth_token"`.
- `type AnthropicCreds struct { APIKey, BaseURL, AuthToken string }`.
- `func (s *Server) resolveAnthropic() AnthropicCreds` — reads the three
  settings from DB; missing key == empty string.
- `handleAdminGetAnthropic` → `200 {api_key, base_url, auth_token}` plaintext.
- `handleAdminSetAnthropic` → decodes the same JSON shape and upserts each of
  the three settings. No validation (any string is valid, including empty).
  Empty string clears that key. Returns the stored shape.

#### `backend/internal/server/server.go`
- Route change: replace
  `GET/PUT /api/admin/settings/template-user` with
  `GET/PUT /api/admin/settings/anthropic`.
- `buildUserEnvFactory`: drop the `system.CopyTemplateCredentials` call (and
  its log line). Resolve creds via `s.resolveAnthropic()` and pass them into
  `pty.BuildUserEnv`.

#### `backend/internal/pty/env.go`
- Change `BuildUserEnv` signature to take the resolved credential values (an
  `AnthropicCreds`-shaped struct or three strings) instead of `*config.Config`.
  Keep `cfg` only if other `cfg` fields are still used by this function
  (proxy / timeout / `CLAUDE_CONFIG_DIR` are not `cfg`-driven here — `cfg`
  carries proxy + timeout, which remain env-var-sourced and stay). Decision:
  `BuildUserEnv` keeps `cfg *config.Config` for proxy/timeout and takes an
  additional `creds` argument.
- Injection logic unchanged: for each of the three values, `if v != "" { set(...) }`.
- `BuildClaudeEnv` has no production caller (only its own test). **Remove it**
  and its test to avoid bit-rot; it's dead code.

#### `backend/internal/system/template_cred.go`
- Delete the file. Delete `template_cred_test.go`.

#### `backend/cmd/server/main.go`
- Delete the `cfg.TemplateUser` existence-check block (lines ~60-64).

#### `web/src/main.js`
- In `viewAdminUsers`, replace the "Template user" card HTML with an
  "Anthropic credentials" card: three inputs (`API key`, `Base URL`,
  `Auth token` — the last `type=password`) + a `Save` button. Helper text:
  "Injected into every user's terminal. Leave a field empty to skip that
  variable."
- Replace `loadTemplateUser()` with `loadAnthropic()`:
  - `GET /api/admin/settings/anthropic` → fill the three inputs (plaintext).
  - Save button → `PUT /api/admin/settings/anthropic` with the three fields.

### Data flow

Admin fills card → `PUT /api/admin/settings/anthropic` → DB `settings[...]`.
User opens terminal → `buildUserEnvFactory` → `resolveAnthropic()` reads DB →
`BuildUserEnv(cfg, creds, username, configDir)` → for each non-empty field,
`set(envVar, value)` → Claude Code process inherits it.

On setting change, the next `Create`/`Restart` picks up the new values (the
factory reads DB lazily at spawn time — same property the template-user path
already had).

### Security note

The values are stored plaintext and returned plaintext on GET. Only admins
reach these endpoints (the `admin` role gate already in place for
`/api/admin/settings/*`). No logging of the values.

## Testing

- `admin_settings_test.go` (rewrite the template-user tests):
  - admin GET returns the stored shape (empty initially).
  - admin PUT round-trips all three fields.
  - admin PUT with a subset of fields empty clears only those keys.
  - non-admin GET and PUT → 403.
- `env_test.go`:
  - each of the three values, when non-empty, is injected.
  - each, when empty, is absent from the env slice.
  - `BuildClaudeEnv` test removed with the function.
- `config_test.go`: no assertions on removed fields.
- `template_cred_test.go`: deleted with the file.

## Out of scope (YAGNI)

- No masking / read-only display.
- No encryption at rest.
- No env-var fallback.
- Proxy / timeout / `CLAUDE_CONFIG_DIR` settings are unchanged (still
  env-var-sourced).
- No migration of existing `settings[template_user]` rows — admins can clear
  them manually; the key simply becomes inert.
