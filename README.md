# Claude Code — Multi-User Docker Platform

An isolated, browser-accessible Claude Code environment for **multiple users**, with per-user Linux isolation, SFTP, quotas, traffic accounting, and an admin management panel. One container, one Go binary.

## Quick start

1. Copy `.env.example` to `.env`. Set `SESSION_SECRET`, `MASTER_KEY` (32 bytes base64), and `BOOTSTRAP_ADMIN_USER` / `BOOTSTRAP_ADMIN_PASSWORD`.
   ```bash
   cp .env.example .env
   # generate secrets:
   openssl rand -hex 32   # → SESSION_SECRET
   openssl rand -base64 32 # → MASTER_KEY
   ```
2. Build and run:
   ```bash
   docker compose up -d --build
   ```
3. Open `http://localhost:8080`. Sign in with the bootstrap admin credentials. You'll be prompted to change your password.
4. As admin, create users, credential presets, and role templates via the admin panel (sidebar → Users / Credentials / Templates).

## What's included

- **Multi-user identity**: username + password (argon2id), first-login change, admin/user roles, bootstrap admin.
- **Per-user isolation**: each user gets a Linux system account, separate `/home/<user>/workspace` + `/data/<user>/claude-config`, `gosu`-dropped PTY.
- **Persistent multi-session terminals**: detach (close browser) → resume; session cap per user.
- **SFTP**: embedded SSH/SFTP server (port 22); regular users confined to their workspace, admins get a root shell.
- **Quotas**: soft disk quota (`du` monitor + panel) + cgroup v2 CPU/memory (per role template).
- **Traffic**: nftables cgroup counters → monthly up/down per user (requires `CAP_NET_ADMIN`).
- **Credential presets**: AES-256-GCM-encrypted Anthropic credentials, reusable across users, rotatable.
- **Role/quota templates**: disk/CPU/mem/max-sessions packaged for batch user creation.
- **Admin panel**: Claude-style UI with light/dark/system themes, responsive (desktop/tablet/mobile).
- **Capture** (admin-only debug): per-session MITM request/response capture with secret redaction.

## Architecture

```
Single Go binary (CGO-free, static) running as root inside the container:
  HTTP :8080   → REST API + WebSocket (terminal/metrics/captures) + embedded SPA
  SSH  :22     → embedded SFTP server (gliderlabs/ssh + pkg/sftp)
  MITM :8888   → lazy-started go-mitmproxy (admin capture only)
  SQLite       → /data/app.db (users, sessions, credentials, templates, traffic)
  nftables     → per-user cgroup traffic counters (CAP_NET_ADMIN)
```

**Capabilities**: `cap_add: [NET_ADMIN]` only. No `--privileged`, no Docker socket. Server runs as root (required for `setuid` into per-user accounts); the container is the isolation boundary.

## Environment variables

| Var | Required | Purpose |
|---|---|---|
| `SESSION_SECRET` | yes | HMAC key for session cookies. |
| `MASTER_KEY` | yes | AES-256-GCM key for credential presets (32 bytes base64). |
| `BOOTSTRAP_ADMIN_USER` / `BOOTSTRAP_ADMIN_PASSWORD` | first run | Seed initial admin (forces password change). |
| `PORT` | no (8080) | HTTP port. |
| `SFTP_PORT` | no (22) | SSH/SFTP port. |
| `CLAUDE_DEBUG_PROXY_PORT` | no (8888) | MITM capture proxy port. |

## Verify on deploy

See `DEPLOY-TEST.md` for a comprehensive checklist of Linux-runtime items to verify after `docker compose up` (gosu PTY, useradd/dir-ownership, cgroup enforcement, nft traffic, SFTP confinement, capture CA-trust, bootstrap admin).

## Security notes

- HTTP only — front with TLS (Caddy/nginx/Cloudflare) for remote/company-network use.
- The debug-capture CA is trusted only inside the container.
- Credentials encrypted at rest; plaintext only in the PTY process env.
- Login timing equalized (decoy argon2) to prevent user enumeration.
