# Claude Code in Docker

An isolated, browser-accessible Claude Code environment.

## Quick start

1. Copy `.env.example` to `.env` and set `ACCESS_KEY` plus an Anthropic credential.
2. Run `./start.sh` (macOS/Linux) or `start.bat` (Windows).
3. Open http://localhost:8080 and enter your `ACCESS_KEY`.

## Features

- Web terminal running Claude Code (`bypassPermissions`) in an isolated container.
- Access-key gated (for use on untrusted networks; put it behind TLS via Caddy/nginx/Cloudflare Tunnel).
- Configurable outbound proxy (HTTP or SOCKS5) via env.
- Live CPU/memory/network metrics.
- Opt-in request/response capture for debugging (full capture with a container-local CA; secrets redacted; off by default).

## Security notes

- HTTP only — front it with a TLS proxy for remote/company-network use.
- The debug-capture CA is trusted only inside the container.
- Non-root, no privileged mode, no Docker socket.
