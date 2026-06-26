#!/usr/bin/env bash
set -euo pipefail

# Ensure trust store reflects any pre-installed CA (no-op if none yet)
update-ca-certificates >/dev/null 2>&1 || true

mkdir -p /workspace
chown -R claude:claude /workspace

export WEB_DIST="${WEB_DIST:-/app/web/dist}"
export CLAUDE_CONFIG_DIR="${CLAUDE_CONFIG_DIR:-/home/claude/.claude}"

exec gosu claude node /app/server/src/server.js
