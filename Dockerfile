# Stage 1: build the SPA
FROM node:22-bookworm-slim AS web-builder
WORKDIR /web
COPY web/package*.json ./
RUN npm ci
COPY web/ ./
RUN npm run build

# Stage 2: build the Go binary (CGO off → static). Use 1.26 for headroom:
# deps (e.g. modernc.org/sqlite) bump the go.mod go-directive, and the builder
# must be >= go.mod. Re-sync here if go.mod exceeds the builder.
FROM golang:1.26-bookworm AS go-builder
WORKDIR /src
COPY backend/go.mod backend/go.sum ./
RUN go mod download
COPY backend/ ./
# put the built SPA into the embed dir
COPY --from=web-builder /web/dist ./internal/ui/dist
RUN CGO_ENABLED=0 GOOS=linux go build -o /out/claude-docker ./cmd/server

# Stage 3: download claude binary into an isolated stage.
# BuildKit `no-cache-filters` targets this stage by name (see the GHA workflow)
# so every build re-fetches https://downloads.claude.ai/claude-code-releases/latest
# and pulls the newest claude, while web/go/apt layers still use the GHA cache.
FROM debian:bookworm-slim AS claude-fetcher
RUN apt-get update && apt-get install -y --no-install-recommends curl jq ca-certificates \
    && rm -rf /var/lib/apt/lists/*
RUN install -d -m 0755 /opt/claude/bin \
    && set -e; \
    LATEST=$(curl -fsSL https://downloads.claude.ai/claude-code-releases/latest); \
    MANIFEST=$(curl -fsSL "https://downloads.claude.ai/claude-code-releases/$LATEST/manifest.json"); \
    CHECKSUM=$(echo "$MANIFEST" | jq -r '.platforms["linux-x64"].checksum'); \
    curl -fsSL -o /tmp/claude-bin "https://downloads.claude.ai/claude-code-releases/$LATEST/linux-x64/claude"; \
    echo "$CHECKSUM  /tmp/claude-bin" | sha256sum -c; \
    chmod 0755 /tmp/claude-bin; \
    mv /tmp/claude-bin /opt/claude/bin/claude

# Stage 4: runtime (runs as root — the server setuids into per-user accounts)
FROM debian:bookworm-slim
ENV DEBIAN_FRONTEND=noninteractive \
    DISABLE_AUTOUPDATER=1 \
    DISABLE_UPDATES=1
RUN apt-get update && apt-get install -y --no-install-recommends \
        git ripgrep curl ca-certificates jq tini gosu sudo openssl screen tmux \
        nftables openssh-client python3 python3-pip python3-venv \
    && rm -rf /var/lib/apt/lists/*

# Node.js 22 LTS (NodeSource) — matches the web-builder's node:22. All users
# get node/npm on the system PATH.
RUN curl -fsSL https://deb.nodesource.com/setup_22.x | bash - \
    && apt-get install -y --no-install-recommends nodejs \
    && rm -rf /var/lib/apt/lists/*

# claude binary from the fetcher stage (rebuilt every build).
COPY --from=claude-fetcher /opt/claude/bin/claude /opt/claude/bin/claude

WORKDIR /workspace
COPY --from=go-builder /out/claude-docker /app/claude-docker
COPY entrypoint.sh /entrypoint.sh
RUN chmod +x /entrypoint.sh

ENTRYPOINT ["/usr/bin/tini", "--", "/entrypoint.sh"]
EXPOSE 8080
