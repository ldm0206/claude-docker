#!/usr/bin/env bash
set -euo pipefail
mkdir -p /workspace
chown -R claude:claude /workspace
export CLAUDE_CONFIG_DIR="${CLAUDE_CONFIG_DIR:-/home/claude/.claude}"
exec gosu claude /app/claude-docker
