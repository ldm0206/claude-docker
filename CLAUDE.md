# CLAUDE.md

Project-specific instructions for this repository. These override default
behavior. User instructions given in chat still take precedence over this file.

## Project

`claude-docker` — multi-user Docker host for Claude Code. Go backend (chi,
modernc sqlite, xterm.js SPA embedded via `go:embed`). Sessions/PTYs are
isolated per Linux account. See README.md for the full architecture.

## Auto-commit-and-sync after edits

**Rule:** After completing any code edit (file Write/Edit/NotebookEdit that
changes source — not pure research or reads), automatically:

1. **Verify** — run the relevant test suite for the touched package(s). For
   `backend/`, `cd backend && go test ./internal/<pkg>/...`. Do not commit if
   anything fails; report the failure and stop.
2. **Stage** — add only the files you actually changed, by name. Never
   `git add -A` / `git add .` (avoids sweeping in stray files).
3. **Commit** — create a NEW commit (never amend). Message follows the repo's
   conventional-commit style seen in `git log`:
   `type(scope): subject` (e.g. `fix(store): ...`, `feat(web): ...`).
   Use a HEREDOC for multi-line bodies. Do not add Claude co-author trailers
   unless asked.
4. **Sync** — `git push` to `origin` on the current branch. If the branch has
   no upstream, push with `-u`.
5. **Report** — one line: what committed (hash + subject) and that it pushed.

Apply this without asking. The user has explicitly opted into auto-sync here.

### When NOT to auto-commit

- Tests fail — fix first, or stop and report.
- Only docs/notes changed AND the user said not to — otherwise still commit.
- Destructive ops (force-push, reset --hard, branch -D) — never, unless the
   user explicitly asks in chat. Auto-sync is plain `git push` only.
- Pre-commit hooks fail — fix the root cause and re-commit; do NOT use
   `--no-verify`.
- The edit is inside a worktree — still commit+push the worktree branch.
- Uncommitted unrelated changes exist in the tree — stage only your own files
   by name; leave the rest untouched.

### Push failures

If `git push` is rejected (non-fast-forward), do NOT force. Run
`git fetch` then `git pull --rebase`, resolve conflicts if any, and push
again. If it still fails, stop and report — do not force-push.

## Code conventions

- Go: follow existing patterns. NULL-able TEXT/INT columns must be scanned
   via `COALESCE(col, '')` / `COALESCE(col, 0)` in the SELECT, or into
   `sql.Null*` — never scan NULL into a plain `string`/`int` (it panics the
   row with "converting NULL to string is unsupported"). See `store/users.go`
   for the COALESCE pattern.
- Tests: add a regression test for any bug fix that reproduces the failure
   path. The suite must stay green before commit.
- No comments unless the WHY is non-obvious. No emojis in code.
- Frontend edits: rebuild is handled by the Dockerfile multi-stage build; no
   manual `npm run build` needed for commits.

## Tooling notes

- `go` 1.26 is available locally; `docker`/`wsl` are NOT on this machine —
   runtime/container inspection must be done by the user on the host.
- modernc.org/sqlite is the driver (pure Go). Its NULL-scan strictness is the
   source of past 500s; always handle NULL at the SQL or scan layer.
