# settings.json Copy + UI Refinement — Design

**Date:** 2026-06-30
**Status:** Approved

## Goal

Two unrelated polish asks bundled into one iteration:

1. **`settings.json` EACCES fix.** `claude` running as user `ldm` fails to
   read `/data/ldm/claude-config/settings.json` (root-owned, EACCES). The
   operator wants `settings.json` shared across users the same way
   credentials already are — **copy on every shell start**, not a
   read-only/shared file.
2. **UI refinement.** Remove the stray `▸` glyph from the Terminal nav
   item, and make the whole app read as less childlike ("幼态/不大气") —
   more restrained and mature — while **keeping the Claude visual identity**
   (warm paper background, terracotta accent, `✦` mark).

## Background

- Credential copy-on-spawn already exists:
  `internal/system/cred_sync.go` → `SyncSharedCredentials(username, uid)`
  copies `.credentials*` from `<DataRoot>/shared/claude-config` into
  `<DataRoot>/<user>/claude-config` (mode `0600`, chown to uid, overwrite,
  no-op if source absent). Wired into the per-session env factory at
  `internal/server/server.go:96`, which runs on every `Start`/`Restart`.
- Sidebar nav at `web/src/main.js:108` is `["terminal", "▸ Terminal"]`;
  siblings use a bare leading space (` Files`, ` Traffic`, …).
- Theme tokens + components live in `web/src/styles.css`. Current palette
  is warm cream (`--bg #f5f1e8`, `--sidebar #ece6d8`) with terracotta
  accents; the cream-on-cream low contrast reads as soft/childlike.
- All views share one component vocabulary (`.card`, `.btn`, `.field`,
  `.tbl`, `.pill`, `.meter`, `.crumb`, `.modal`), so a token + component
  refresh propagates without per-view rewrites.

## Decisions (confirmed with user)

1. `settings.json` is copied on every spawn, overwriting the user's copy
   (operator's shared file is the source of truth — same model as
   credentials). Local edits do not persist. Accepted.
2. The credential-sync function broadens to also carry `settings.json`,
   and is **renamed** `SyncSharedCredentials` → `SyncSharedConfig`
   (the old name is now inaccurate). One call site + tests updated.
3. Nav glyphs (`▸`, leading-space hack) are dropped from all items;
   active state becomes a 2px terracotta left accent bar + medium-weight
   label.
4. Visual direction: **keep Claude identity** (warm paper bg, terracotta
   accent, `✦ Claude` mark), reach 大气 by *restraint* — lighter/less
   saturated paper, terracotta used only as a single accent (active,
   primary action, focus), tighter radii, hairline borders, stronger type
   hierarchy, more generous spacing. Not a cold-grey swap.

## Architecture

### A. `settings.json` copy-on-spawn

`internal/system/cred_sync.go`:

- Rename `syncSharedCredentials` → `syncSharedConfig`,
  `SyncSharedCredentials` → `SyncSharedConfig`. Signature unchanged:
  `(username string, uid int) error` / internal `(srcDir, dstDir, uid)`.
- Whitelist: copy a file iff `name == "settings.json"` **or**
  `strings.HasPrefix(name, ".credentials")`. Everything else
  (`projects/`, `todos/`, `statsig/`, …) remains excluded.
- Per-file write stays: `os.ReadFile` → `os.WriteFile(dst, data, 0o600)`
  → `os.Chown(dst, uid, uid)`. Existing target overwritten. Source dir
  missing or empty → `nil` (no-op). Per-file failure → log warning,
  continue. Non-fatal, never blocks session creation.

`internal/server/server.go`:

- Update the one call site (~line 96): `system.SyncSharedConfig(u.Username,
  u.UID)`. No behavior change.

### B. Remove `▸` + nav cleanup

`web/src/main.js`:

- `["terminal", "▸ Terminal"]` → `["terminal", "Terminal"]`.
- Drop the leading space on the other labels too (` Files` → `Files`,
  ` Traffic` → `Traffic`, ` Users`, etc.) — glyph column is gone.
- Active state moves to CSS (left accent bar; see C).

### C. Token + component refinement (keep Claude identity)

`web/src/styles.css` token changes:

- Light (lighter, less saturated warm paper):
  `--bg #faf8f3`, `--surface #ffffff`, `--surface-2 #f5f1ea`,
  `--sidebar #f3efe6` (close to bg, not a heavy fill),
  `--sidebar-hover #ece5d6`, `--sidebar-active #e4dcc8`,
  `--border #e0d8c8` → crisper `#dcd4c3`, `--text #2e2a23`,
  `--text-muted #6b6358`, `--text-faint #9a9081`. Terracotta unchanged
  (`--accent #c15f3c`, `--accent-2 #d97757`) — kept as the single accent.
- Dark (drop the brown warmth, keep neutral-warm charcoal):
  `--bg #181715`, `--surface #211f1c`, `--surface-2 #272421`,
  `--sidebar #1c1a18`, `--sidebar-hover #2a2724`, `--sidebar-active #322e29`,
  `--border #322e29`. Terracotta `#d97757` stays.
- Radii: `--radius 10→8`, `--radius-sm 7→5`.
- Type: topbar `h1` 15→16px / weight 600; `.brand` weight 700, tighter
  spacing (sparkle kept, smaller).
- Subtler shadows; slightly more generous card/topbar padding.

Component refresh (all in `styles.css`, propagates to every view):

- `.sidebar`: warm-neutral surface, hairline right border.
- `.nav-item`: no glyphs; padding + a 2px **left accent bar** that
  appears on `.active` (terracotta) alongside medium-weight label.
  Hover = faint warm tint, not a heavy fill.
- `.brand`: `✦ Claude`, tighter, sparkle scaled down.
- `.topbar`: crisper hairline, 16px title.
- `.card`: white surface, hairline border, 8px radius, subtle shadow.
- `.btn` / `.btn.ghost` / `.btn.danger`: slightly more padding; ghost =
  hairline; danger = red outline.
- `.pill`, `.tbl` headers/rows, `.meter`, `.crumb`: tightened spacing
  and type.
- Terminal body (`.term-body`) stays dark.

View markup tidy-up (inline-style spots in `main.js`, `captures.js`,
`files.js`): login card centering, traffic meters, users/audit tables,
files toolbar + breadcrumbs, captures list, modal widths, empty/loading
states. Pure spacing/typography; no behavior change.

## Testing

`internal/system/cred_sync_test.go`:

1. The current "whitelist" test (asserts `settings.json` and `projects/`
   are excluded) is **flipped**: now asserts `settings.json` IS copied
   (mode `0600`, owner = uid) and `projects/` is still excluded.
2. Add: source has `settings.json`, target has a stale one → overwritten
   with the new content.
3. Existing happy-path / source-missing / source-empty cases stay green
   under the renamed function.

`pty/env_test.go`: unaffected (signature unchanged).

## Out of scope

- Sharing files other than `.credentials*` + `settings.json` (no
  `keychain.json`, `.claude.json`, etc.) — YAGNI.
- Letting users keep local `settings.json` edits (merge/preserve) —
  rejected; operator-controlled, overwrite model.
- Cold-grey/zinc palette — rejected (would lose Claude identity).
- Backend behavior beyond the rename + whitelist broadening.
