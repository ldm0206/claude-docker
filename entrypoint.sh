#!/usr/bin/env bash
set -euo pipefail

# --- CA generation + trust-store install (runs as ROOT, before gosu) ---
# The MITM proxy signs leaf certs with this CA and claude must trust it, so we
# generate it once as root and install it into the system trust store here.
# At runtime (as uid 1000) the proxy only LOADS these files.
CA_DIR="/etc/claude-debug"
CA_CRT="${CA_DIR}/ca.crt"
CA_KEY="${CA_DIR}/ca.key"
SSL_CA_DIR="/home/claude/.claude/mitm-certs"

mkdir -p "${CA_DIR}"

if [ ! -f "${CA_CRT}" ] || [ ! -f "${CA_KEY}" ]; then
  echo "[entrypoint] generating debug-proxy CA..."
  # Self-signed CA: 3650 days, CN=claude-docker-debug-proxy
  openssl req -x509 -newkey rsa:2048 -nodes \
    -keyout "${CA_KEY}" -out "${CA_CRT}" \
    -days 3650 -subj "/CN=claude-docker-debug-proxy" \
    >/dev/null 2>&1
fi

chmod 644 "${CA_CRT}"
chmod 600 "${CA_KEY}"
chown claude:claude "${CA_CRT}" "${CA_KEY}"

# Install CA into the system trust store (root only).
cp "${CA_CRT}" /usr/local/share/ca-certificates/claude-debug.crt
update-ca-certificates >/dev/null 2>&1 || true

# Seed http-mitm-proxy's sslCaDir layout so loadCA consumes OUR CA instead of
# auto-generating a different one. Forge reads ca.pem + ca.private.key +
# ca.public.key; we derive the public key from the private key with openssl.
mkdir -p "${SSL_CA_DIR}/certs" "${SSL_CA_DIR}/keys"
cp "${CA_CRT}" "${SSL_CA_DIR}/certs/ca.pem"
cp "${CA_KEY}" "${SSL_CA_DIR}/keys/ca.private.key"
openssl rsa -in "${CA_KEY}" -pubout -out "${SSL_CA_DIR}/keys/ca.public.key" >/dev/null 2>&1 || true
chown -R claude:claude "${SSL_CA_DIR}"

# Hand the paths to the node process so debug-proxy.js loads them.
export CLAUDE_DEBUG_CA_CERT="${CA_CRT}"
export CLAUDE_DEBUG_CA_KEY="${CA_KEY}"
export CLAUDE_DEBUG_SSL_CA_DIR="${SSL_CA_DIR}"

# --- Drop to the claude user and start the server ---
mkdir -p /workspace
chown -R claude:claude /workspace

export WEB_DIST="${WEB_DIST:-/app/web/dist}"
export CLAUDE_CONFIG_DIR="${CLAUDE_CONFIG_DIR:-/home/claude/.claude}"

exec gosu claude node /app/server/src/server.js
