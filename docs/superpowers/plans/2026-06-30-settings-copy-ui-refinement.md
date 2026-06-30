# settings.json Copy + UI Refinement Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Copy `settings.json` alongside credentials on every PTY spawn (fixing the EACCES), drop the stray `▸` glyph, and refine the UI to read as more restrained/mature while keeping the Claude visual identity (warm paper, terracotta accent, `✦` mark).

**Architecture:** Backend: broaden the existing copy-on-spawn sync (`internal/system/cred_sync.go`) to also carry `settings.json`, and rename `SyncSharedCredentials` → `SyncSharedConfig` since the scope grew. Frontend: refresh theme tokens + shared component classes in `styles.css` (lighter paper, terracotta used only as a single accent, tighter radii/hairline borders/stronger type), drop nav glyphs in `main.js`, and tidy inline-styled spots across the views.

**Tech Stack:** Go 1.26 (modernc sqlite, chi), vanilla JS + Vite, xterm.js, `go:embed` SPA.

## Global Constraints

- Go conventions: handle NULL at SQL/scan layer (N/A here, no DB changes). No comments unless WHY is non-obvious. No emojis in code.
- Tests: all `internal/system/*_test.go` are `//go:build linux` — they do **not** run on the Windows dev box; compile/vet there, run the suite in-container.
- Frontend has no JS test framework; verify with `cd web && npm run build` (catches syntax/import errors). No manual build needed for the commit per CLAUDE.md, but we run it for verification.
- Auto-commit rule (CLAUDE.md): after each task's edits, run the relevant suite, then `git add` only the files you changed by name (never `-A`), new commit (never amend), conventional-commit message, `git push`. Never commit `backend/server.exe` (untracked stray).
- Stage only your own files; leave unrelated changes untouched.

---

## File Structure

**Backend (Task 1):**
- `backend/internal/system/cred_sync.go` — rename `syncSharedCredentials`→`syncSharedConfig`, `SyncSharedCredentials`→`SyncSharedConfig`; broaden whitelist to include `settings.json`. (responsibility: copy shared config files into a user's claude-config dir)
- `backend/internal/server/server.go` — one call-site rename (~line 96). (responsibility: PTY env factory)
- `backend/internal/system/cred_sync_test.go` — rename call sites/test names, flip the whitelist assertion, add a settings.json overwrite test. (responsibility: lock the sync contract)

**Frontend (Task 2):**
- `web/src/styles.css` — token refresh (`:root` light + dark blocks) + component rule refresh (sidebar, nav, brand, topbar, card, btn, pill, tbl, meter, crumb, modal). (responsibility: theme + component vocabulary)
- `web/src/main.js` — drop nav glyphs + leading spaces; tighten brand + login/traffic/users/audit/modal inline styles. (responsibility: app shell + admin views)
- `web/src/files.js` — tighten files toolbar/breadcrumbs/modal inline styles. (responsibility: file-manager view)
- `web/src/captures.js` — tighten captures list/header inline styles. (responsibility: captures view)

---

## Task 1: Broaden shared-config sync to carry settings.json (TDD, rename)

**Files:**
- Modify: `backend/internal/system/cred_sync.go` (whole file)
- Modify: `backend/internal/server/server.go` (call site ~L94-102)
- Modify: `backend/internal/system/cred_sync_test.go` (whole file)

**Interfaces:**
- Produces: `system.SyncSharedConfig(username string, uid int) error` (public), `syncSharedConfig(srcDir, dstDir string, uid int) error` (package-private). Consumed by `server.go` `buildUserEnvFactory`.

- [ ] **Step 1: Update tests to express the new contract (fail first)**

Replace the entire contents of `backend/internal/system/cred_sync_test.go` with:

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

func TestSyncSharedConfig_HappyPath(t *testing.T) {
	src := t.TempDir()
	dst := t.TempDir()
	writeCredFile(t, src, ".credentials.json", `{"token":"abc"}`)

	if err := syncSharedConfig(src, dst, 2000); err != nil {
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

// settings.json is now in the copy whitelist; projects/ and other dirs stay excluded.
func TestSyncSharedConfig_SettingsCopied(t *testing.T) {
	src := t.TempDir()
	dst := t.TempDir()
	writeCredFile(t, src, ".credentials.json", `x`)
	writeCredFile(t, src, "settings.json", `{"permissions":{}}`)
	if err := os.MkdirAll(filepath.Join(src, "projects"), 0o755); err != nil {
		t.Fatal(err)
	}

	if err := syncSharedConfig(src, dst, 2000); err != nil {
		t.Fatalf("sync: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dst, "settings.json")); err != nil {
		t.Fatalf("settings.json should be copied: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dst, "projects")); !os.IsNotExist(err) {
		t.Fatalf("projects/ should NOT be copied: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dst, ".credentials.json")); err != nil {
		t.Fatalf(".credentials.json should be copied: %v", err)
	}
}

func TestSyncSharedConfig_SourceMissing(t *testing.T) {
	dst := t.TempDir()
	if err := syncSharedConfig("/nonexistent/path/xyz", dst, 2000); err != nil {
		t.Fatalf("missing source must be no-op, got: %v", err)
	}
}

func TestSyncSharedConfig_SourceEmpty(t *testing.T) {
	src := t.TempDir() // exists, no .credentials* / settings.json
	dst := t.TempDir()
	if err := syncSharedConfig(src, dst, 2000); err != nil {
		t.Fatalf("empty source must be no-op, got: %v", err)
	}
}

func TestSyncSharedConfig_Overwrite(t *testing.T) {
	src := t.TempDir()
	dst := t.TempDir()
	writeCredFile(t, src, ".credentials.json", `new`)
	writeCredFile(t, dst, ".credentials.json", `old`)

	if err := syncSharedConfig(src, dst, 2000); err != nil {
		t.Fatalf("sync: %v", err)
	}
	b, _ := os.ReadFile(filepath.Join(dst, ".credentials.json"))
	if string(b) != `new` {
		t.Fatalf("target not overwritten, content = %q", string(b))
	}
}

func TestSyncSharedConfig_SettingsOverwrite(t *testing.T) {
	src := t.TempDir()
	dst := t.TempDir()
	writeCredFile(t, src, "settings.json", `{"new":true}`)
	writeCredFile(t, dst, "settings.json", `{"stale":true}`)

	if err := syncSharedConfig(src, dst, 2000); err != nil {
		t.Fatalf("sync: %v", err)
	}
	b, _ := os.ReadFile(filepath.Join(dst, "settings.json"))
	if string(b) != `{"new":true}` {
		t.Fatalf("settings.json not overwritten, content = %q", string(b))
	}
}
```

- [ ] **Step 2: Verify the tests fail (undefined symbol)**

Run: `cd backend && go vet ./internal/system/`
Expected: compile error referencing `syncSharedConfig` (undefined — function is still named `syncSharedCredentials`). On Windows the `linux`-tagged tests are skipped from the build, so also confirm the production package compiles stale: it still does. The vet error confirms the test file expects the new name.

- [ ] **Step 3: Implement the rename + whitelist in cred_sync.go**

Replace the entire contents of `backend/internal/system/cred_sync.go` with:

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

// syncSharedConfig copies shared config files from srcDir into dstDir:
// entries named ".credentials*" or "settings.json". srcDir missing or
// containing no matches is a no-op. Files are written mode 0600 and
// chown'd to uid. A per-file failure is logged and skipped; it does not
// abort the sync.
func syncSharedConfig(srcDir, dstDir string, uid int) error {
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
		name := e.Name()
		if !strings.HasPrefix(name, ".credentials") && name != "settings.json" {
			continue
		}
		src := filepath.Join(srcDir, name)
		dst := filepath.Join(dstDir, name)
		data, err := os.ReadFile(src)
		if err != nil {
			log.Printf("[system] warning: read shared config %s: %v", src, err)
			continue
		}
		if err := os.WriteFile(dst, data, 0o600); err != nil {
			log.Printf("[system] warning: write config %s: %v", dst, err)
			continue
		}
		if err := os.Chown(dst, uid, uid); err != nil {
			log.Printf("[system] warning: chown config %s: %v", dst, err)
			continue
		}
	}
	return nil
}

// SyncSharedConfig copies the operator's shared config files (.credentials* and
// settings.json) into the given user's claude-config dir. Source:
// <DataRoot>/shared/claude-config. Target: <DataRoot>/<username>/claude-config.
// No-op if source is absent or has no matching files. uid owns the written
// files (0600).
func SyncSharedConfig(username string, uid int) error {
	src := filepath.Join(DataRoot, "shared", "claude-config")
	dst := filepath.Join(DataRoot, username, "claude-config")
	return syncSharedConfig(src, dst, uid)
}
```

- [ ] **Step 4: Update the server.go call site**

In `backend/internal/server/server.go`, in `buildUserEnvFactory` (~L96), replace:

```go
		if err := system.SyncSharedCredentials(u.Username, u.UID); err != nil {
			log.Printf("[server] warning: sync shared credentials for %s: %v", u.Username, err)
		}
```

with:

```go
		if err := system.SyncSharedConfig(u.Username, u.UID); err != nil {
			log.Printf("[server] warning: sync shared config for %s: %v", u.Username, err)
		}
```

Also update the doc comment above the function (~L80-93): the line "It first syncs the operator's shared credential files into the user's claude-config dir (non-fatal on failure)," → "It first syncs the operator's shared config files (credentials + settings.json) into the user's claude-config dir (non-fatal on failure),".

- [ ] **Step 5: Verify the whole backend compiles + vets clean**

Run: `cd backend && go build ./... && go vet ./...`
Expected: no output, exit 0. (The `linux`-tagged cred_sync tests run in-container; on Windows we rely on build+vet.)

- [ ] **Step 6: Commit + push**

```bash
git add backend/internal/system/cred_sync.go backend/internal/system/cred_sync_test.go backend/internal/server/server.go
git commit -m "feat(system): sync shared settings.json on spawn; rename to SyncSharedConfig"
git push
```

---

## Task 2: Drop ▸ + Claude-identity UI refinement

**Files:**
- Modify: `web/src/styles.css` (tokens + components)
- Modify: `web/src/main.js` (nav, brand, view inline styles)
- Modify: `web/src/files.js` (toolbar/crumbs/modal inline styles)
- Modify: `web/src/captures.js` (list/header inline styles)

**Interfaces:** None (pure presentation). Depends on Task 1 only in that both ship together; technically independent.

- [ ] **Step 1: Refresh theme tokens in styles.css**

In `web/src/styles.css`, replace the `:root { ... }` block (lines 1-28) with:

```css
:root {
  /* Claude palette — light theme (default). Warm paper, lighter and less
     saturated than before; terracotta kept as a single restrained accent. */
  --bg:        #faf8f3;
  --surface:   #ffffff;
  --surface-2: #f5f1ea;
  --sidebar:   #f3efe6;
  --sidebar-hover: #ece5d6;
  --sidebar-active: #e4dcc8;
  --border:    #dcd4c3;
  --text:      #2e2a23;
  --text-muted:#6b6358;
  --text-faint:#9a9081;
  --accent:    #c15f3c;
  --accent-2:  #d97757;
  --accent-fg:#ffffff;
  --ok:        #3f8f5f;
  --warn:      #d99a3e;
  --bad:       #b6533c;
  --term-bg:   #1f1e1d;
  --term-fg:   #d6cab6;

  --sidebar-w: 224px;
  --radius: 8px;
  --radius-sm: 5px;
  --shadow: 0 1px 2px rgba(46,42,35,.05);
  --mono: "JetBrains Mono", ui-monospace, SFMono-Regular, Menlo, monospace;
  --sans: ui-sans-serif, system-ui, -apple-system, "Segoe UI", Roboto, sans-serif;
}
```

Replace the `@media (prefers-color-scheme: dark) { :root:not([data-theme="light"]) { ... } }` block (lines 30-39) with:

```css
@media (prefers-color-scheme: dark) {
  :root:not([data-theme="light"]) {
    --bg: #181715; --surface: #211f1c; --surface-2: #272421;
    --sidebar: #1c1a18; --sidebar-hover: #2a2724; --sidebar-active: #322e29;
    --border: #322e29; --text: #ece6d8; --text-muted: #a8a092; --text-faint: #8a8175;
    --accent: #d97757; --accent-2: #e08a68; --accent-fg: #181715;
    --ok: #5dbb82; --warn: #d9a23e; --bad: #c96442;
    --term-bg: #1a1918; --term-fg: #ece6d8;
  }
}
```

Replace the `:root[data-theme="dark"] { ... }` block (lines 40-47) with:

```css
:root[data-theme="dark"] {
  --bg: #181715; --surface: #211f1c; --surface-2: #272421;
  --sidebar: #1c1a18; --sidebar-hover: #2a2724; --sidebar-active: #322e29;
  --border: #322e29; --text: #ece6d8; --text-muted: #a8a092; --text-faint: #8a8175;
  --accent: #d97757; --accent-2: #e08a68; --accent-fg: #181715;
  --ok: #5dbb82; --warn: #d9a23e; --bad: #c96442;
  --term-bg: #1a1918; --term-fg: #ece6d8;
}
```

- [ ] **Step 2: Refresh shell + component rules in styles.css**

Replace these existing rules (same selectors) with the new versions below. (Each is a 1:1 swap of an existing rule block — find by selector.)

`.sidebar`, `.brand`, `.nav-item`, `.nav-item:hover`, `.nav-item.active`:

```css
.sidebar { width: var(--sidebar-w); flex: 0 0 var(--sidebar-w); background: var(--sidebar);
  border-right: 1px solid var(--border); display:flex; flex-direction:column;
  padding: 16px 12px; gap: 2px; overflow-y: auto; }
.brand { font-weight: 700; font-size: 15px; letter-spacing: .2px; color: var(--text); margin: 4px 8px 18px; display:flex; align-items:center; gap:7px; }
.brand::first-letter { color: var(--accent); }
.nav-item { position: relative; display:flex; align-items:center; gap:9px; padding:8px 12px; border-radius: var(--radius-sm);
  color: var(--text-muted); cursor: pointer; border: none; background: transparent; text-align:left; width:100%; font-size: 13.5px; }
.nav-item:hover { background: var(--sidebar-hover); color: var(--text); }
.nav-item.active { background: var(--sidebar-active); color: var(--text); font-weight: 600; }
.nav-item.active::before { content:""; position:absolute; left: 0; top: 7px; bottom: 7px; width: 2px; border-radius: 2px; background: var(--accent); }
```

`.topbar`, `.topbar h1`, `.content`:

```css
.topbar { display:flex; align-items:center; gap:12px; padding:12px 18px; border-bottom:1px solid var(--border); background:var(--surface);}
.topbar h1 { font-size: 16px; margin: 0; font-weight: 600; letter-spacing: .2px; }
.content { flex:1; overflow:auto; padding:18px; }
```

`.card` and `.btn` family:

```css
.card { background: var(--surface); border:1px solid var(--border); border-radius: var(--radius); box-shadow: var(--shadow); }
.pads { padding: 16px; }
.row { display:flex; gap:10px; align-items:center; flex-wrap:wrap; }
.grow { flex:1; }
.btn { background: var(--accent); color: var(--accent-fg); border:none; border-radius: var(--radius-sm); padding:8px 15px; cursor:pointer; font-weight:600; font-size: 13px; }
.btn.ghost { background: transparent; color: var(--text); border:1px solid var(--border); }
.btn.danger { background: transparent; color: var(--bad); border:1px solid var(--bad); }
.btn.tiny { padding:5px 10px; font-size:12px; }
```

`.field` + `label.lbl`:

```css
input.field, select.field, textarea.field { background: var(--surface); border:1px solid var(--border); border-radius: var(--radius-sm); padding:8px 10px; width:100%; }
input.field:focus, select.field:focus, textarea.field:focus { border-color: var(--accent); outline: none; }
label.lbl { font-size:11px; text-transform:uppercase; letter-spacing:.5px; color:var(--text-faint); display:block; margin-bottom:5px; }
```

`table.tbl` family:

```css
table.tbl { width:100%; border-collapse: collapse; }
table.tbl th, table.tbl td { text-align:left; padding:10px 12px; border-bottom:1px solid var(--border); font-size:13px; }
table.tbl th { color: var(--text-faint); font-weight:600; font-size:11px; text-transform:uppercase; letter-spacing:.5px; }
table.tbl tr:hover td { background: var(--surface-2); }
```

`.pill`:

```css
.pill { font-size:11px; padding:2px 9px; border-radius:99px; border:1px solid var(--border); color: var(--text-muted); }
```

`.meter`:

```css
.meter { background:var(--surface); border:1px solid var(--border); border-radius:var(--radius-sm); padding:10px 14px; font-size:13px; }
.meter b { font-size:16px; color: var(--text); }
```

`.crumb` / `.crumb:hover`:

```css
.crumb { cursor:pointer; color: var(--text-muted); padding:4px 8px; border-radius: var(--radius-sm); }
.crumb:hover { background: var(--surface-2); color: var(--text); }
```

`.modal`:

```css
.modal { background:var(--surface); border:1px solid var(--border); border-radius: var(--radius); width:min(480px,94vw); max-height:88vh; overflow:auto; }
```

(The dark-contrast override at the very end of the file — `:root[data-theme="dark"], :root:not([data-theme="light"]) { --shadow: ... }` — is fine to leave, but update the rgba to neutral: `--shadow: 0 1px 3px rgba(0,0,0,.28);` stays unchanged.)

- [ ] **Step 3: Drop nav glyphs + tighten brand in main.js**

In `web/src/main.js`:

(a) In `renderSidebar` (~L107-118), replace the `items` and `admin` arrays so labels have no glyphs and no leading spaces:

```js
  const items = [
    ["terminal", "Terminal"],
    ["files", "Files"],
    ["traffic", "Traffic"],
  ];
  const admin = [
    ["users", "Users"],
    ["credentials", "Credentials"],
    ["templates", "Templates"],
    ["captures", "Captures"],
    ["audit", "Audit"],
  ];
```

(b) The brand markup (~L120) — replace:

```js
  sb.appendChild(el("div", { class: "brand" }, "✦ Claude"));
```

with:

```js
  sb.appendChild(el("div", { class: "brand" }, "Claude"));
```

(The sparkle is dropped; `.brand::first-letter` in CSS colors the capital C terracotta as a restrained mark.)

- [ ] **Step 4: Tidy inline-styled spots in main.js views**

(a) `renderLogin` (~L56-65) — give the card real padding/surface and tighten. Replace the `shell(...)` block:

```js
  shell("login-card", [
    el("h1", { style: "color:var(--accent);margin:0 0 4px;font-size:20px" }, "Claude"),
    el("p", { class: "muted", style: "margin:0 0 22px" }, "Sign in"),
    el("label", { class: "lbl" }, "Username"), user, el("div", { style: "height:10px" }),
    el("label", { class: "lbl" }, "Password"), pass, el("div", { style: "height:16px" }),
    go, el("div", { style: "height:10px" }), err,
  ]);
  app.firstElementChild.style.maxWidth = "360px";
  app.firstElementChild.style.margin = "0 auto";
```

(Also drop the `✦` from the existing login `<h1>` text — change `"✦ Claude"` → `"Claude"`. This edit is the `el("h1", ...)` first kid.)

(b) `renderApp` topbar (~L96) — the `≡` menu button is fine; no change required.

(c) `viewTraffic` (~L183-184) — tighten the monthly-traffic card heading size. Replace:

```js
    <div class="card pads" style="margin-top:14px"><h3 style="margin:0 0 8px;font-size:14px">Monthly traffic</h3><div id="trows" class="muted">—</div></div>`;
```

with:

```js
    <div class="card pads" style="margin-top:14px"><h3 style="margin:0 0 10px;font-size:13px;text-transform:uppercase;letter-spacing:.5px;color:var(--text-faint)">Monthly traffic</h3><div id="trows" class="muted">—</div></div>`;
```

(d) Modals (`userModal`, `credModal`, `tplModal`): leave markup; the shared `.modal`/`.field`/`.lbl` rule refresh in Step 2 restyles them. No JS change needed.

- [ ] **Step 5: Tidy inline-styled spots in files.js**

In `web/src/files.js`:

(a) The empty-state text (~L16) — replace:

```js
      <div class="files-empty muted" id="fempty">Empty folder. Drag files here to upload.</div>
```

with:

```js
      <div class="files-empty muted" id="fempty" style="padding:32px">Empty folder — drag files here to upload.</div>
```

(b) The editor modal (~L105) — widen slightly and tighten the textarea. Replace:

```js
    overlay.innerHTML = `<div class="modal" style="width:min(720px,94vw)"><div class="hd"><b>${esc(path)}</b></div>
      <div class="bd"><textarea class="field" id="ed-area" style="min-height:50vh;font-family:var(--mono);font-size:13px"></textarea>
      <div style="height:10px"></div><button class="btn" id="ed-save">Save</button> <button class="btn ghost" id="ed-cancel">Cancel</button>
      <span class="muted tiny" id="ed-msg" style="margin-left:8px"></span></div></div>`;
```

with:

```js
    overlay.innerHTML = `<div class="modal" style="width:min(760px,94vw)"><div class="hd"><b>${esc(path)}</b></div>
      <div class="bd"><textarea class="field" id="ed-area" style="min-height:52vh;font-family:var(--mono);font-size:13px;border-radius:6px"></textarea>
      <div style="height:12px"></div><button class="btn" id="ed-save">Save</button> <button class="btn ghost" id="ed-cancel">Cancel</button>
      <span class="muted tiny" id="ed-msg" style="margin-left:8px"></span></div></div>`;
```

- [ ] **Step 6: Tidy inline-styled spots in captures.js**

In `web/src/captures.js`, the header row (~L7-12) — replace:

```js
  root.innerHTML = `
    <div class="row" style="margin-bottom:10px">
      <span class="muted tiny">Redacted request/response pairs from capture-enabled sessions.</span>
      <span class="grow"></span>
      <button class="btn tiny ghost" id="cap-clear">Clear</button>
    </div>
    <div class="cap-list" id="cap-list"></div>
    <div class="card pads" id="cap-detail" style="display:none;font-family:var(--mono);font-size:12px;white-space:pre-wrap"></div>`;
```

with:

```js
  root.innerHTML = `
    <div class="row" style="margin-bottom:12px">
      <span class="muted tiny">Redacted request/response pairs from capture-enabled sessions.</span>
      <span class="grow"></span>
      <button class="btn tiny ghost" id="cap-clear">Clear</button>
    </div>
    <div class="cap-list" id="cap-list"></div>
    <div class="card pads" id="cap-detail" style="display:none;font-family:var(--mono);font-size:12px;white-space:pre-wrap;margin-top:12px"></div>`;
```

- [ ] **Step 7: Build the SPA to catch syntax/import errors**

Run: `cd web && npm run build`
Expected: vite builds `dist/` with no errors; exit 0.

- [ ] **Step 8: Commit + push**

```bash
git add web/src/styles.css web/src/main.js web/src/files.js web/src/captures.js
git commit -m "feat(web): restrained Claude-identity refresh; drop nav glyphs"
git push
```

---

## Self-Review

**1. Spec coverage:**
- settings.json copy-on-spawn + rename → Task 1 (Steps 3-4 implement, Steps 1-2 test, Step 5 verifies).
- Remove `▸` → Task 2 Step 3(a).
- Nav glyph cleanup + active accent bar → Task 2 Step 3(a) (labels) + Step 2 (`.nav-item.active::before`).
- Token refresh (lighter paper, terracotta restrained, tighter radii/hairline/stronger type) → Task 2 Steps 1-2.
- All-views tidy → Task 2 Steps 4-6 (main.js views, files.js, captures.js). Users/Audit/Credentials/Templates views use only shared `.card/.tbl/.btn/.pill` classes refreshed in Step 2 — covered transitively.
- Tests: whitelist flip + settings.json overwrite → Task 1 Step 1. Covered.

**2. Placeholder scan:** No TBD/TODO/"add error handling". Every code step shows full code; every CSS step shows full rule text. Commands have expected output.

**3. Type/name consistency:** `SyncSharedConfig` / `syncSharedConfig` used consistently across cred_sync.go, cred_sync_test.go, and server.go. `--radius`/`--radius-sm` new values (8/5) referenced consistently in token block and component rules. `.brand::first-letter` defined in Step 2 and relied on in Step 3(b). No drift.
