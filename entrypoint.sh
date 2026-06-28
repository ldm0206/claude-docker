#!/usr/bin/env bash
set -euo pipefail

# Run as root: the server must setuid into per-user accounts for isolation.
# (Per-user drop happens inside PTY spawns via gosu, not here.)
mkdir -p /workspace /data /home
chmod 0755 /home

exec /app/claude-docker