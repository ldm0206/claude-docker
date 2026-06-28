#!/usr/bin/env bash
set -euo pipefail

# Run as root: the server setuids into per-user accounts for isolation.

# --- CA generation + trust-store install (for the admin-only per-session
# MITM capture proxy, Plan 5). Runs as ROOT before exec. The proxy signs leaf
# certs with this CA at runtime (as uid 1000); claude must trust it, so we
# generate it once as root and install it into the system trust store here.
CA_DIR="/etc/claude-debug"
CA_CRT="${CA_DIR}/ca.crt"
CA_KEY="${CA_DIR}/ca.key"
SSL_CA_DIR="/home/claude/.claude/mitm-certs"

mkdir -p "${CA_DIR}"
if [ ! -f "${CA_CRT}" ] || [ ! -f "${CA_KEY}" ]; then
  echo "[entrypoint] generating debug-proxy CA..."
  openssl req -x509 -newkey rsa:2048 -nodes \
    -keyout "${CA_KEY}" -out "${CA_CRT}" \
    -days 3650 -subj "/CN=claude-docker-debug-proxy" >/dev/null 2>&1
fi
chmod 644 "${CA_CRT}"; chmod 600 "${CA_KEY}"
cp "${CA_CRT}" /usr/local/share/ca-certificates/claude-debug.crt
update-ca-certificates >/dev/null 2>&1 || echo "[entrypoint] WARN: update-ca-certificates failed" >&2

# Seed go-mitmproxy's sslCaDir layout so it consumes OUR CA. The dimensions
# used auth calls happen as uid 1000, who owns /home/claude, so install to the
# shared /home/claude tree here (read-only for per-user accounts).
if id -u claude >/dev/null 2>&1; then
  mkdir -p "${SSL_CA_DIR}/certs" "${SSL_CA_DIR}/keys"
  cp "${CA_CRT}" "${SSL_CA_DIR}/certs/ca.pem"
  cp "${CA_KEY}" "${SSL_CA_DIR}/keys/ca.private.key"
  openssl rsa -in "${CA_KEY}" -pubout -out "${SSL_CA_DIR}/keys/ca.public.key" >/dev/null 2>&1 || true
fi

# Hand the paths + port to the Go process so the capture proxy loads them.
export CLAUDE_DEBUG_CA_CERT="${CA_CRT}"
export CLAUDE_DEBUG_CA_KEY="${CA_KEY}"
export CLAUDE_DEBUG_SSL_CA_DIR="${SSL_CA_DIR}"

# --- Storage roots + drop into the server ---
mkdir -p /workspace /data /home
chmod 0755 /home

exec /app/claude-docker