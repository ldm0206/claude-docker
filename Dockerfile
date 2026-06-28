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

# Stage 3: runtime
FROM debian:bookworm-slim
ENV DEBIAN_FRONTEND=noninteractive \
    DISABLE_AUTOUPDATER=1 \
    DISABLE_UPDATES=1 \
    CLAUDE_CONFIG_DIR=/home/claude/.claude
RUN apt-get update && apt-get install -y --no-install-recommends \
        git ripgrep curl ca-certificates jq tini gosu sudo openssl \
    && rm -rf /var/lib/apt/lists/*
RUN useradd -m -s /bin/bash claude \
    && install -d -o claude -g claude /workspace

# Download claude binary (parity: /home/claude/.local/bin)
USER claude
RUN set -e; \
    mkdir -p /home/claude/.local/bin; \
    LATEST=$(curl -fsSL https://downloads.claude.ai/claude-code-releases/latest); \
    MANIFEST=$(curl -fsSL "https://downloads.claude.ai/claude-code-releases/$LATEST/manifest.json"); \
    CHECKSUM=$(echo "$MANIFEST" | jq -r '.platforms["linux-x64"].checksum'); \
    curl -fsSL -o /tmp/claude-bin "https://downloads.claude.ai/claude-code-releases/$LATEST/linux-x64/claude"; \
    echo "$CHECKSUM  /tmp/claude-bin" | sha256sum -c; \
    chmod +x /tmp/claude-bin; \
    mv /tmp/claude-bin /home/claude/.local/bin/claude; \
    echo 'export PATH="$HOME/.local/bin:$PATH"' >> /home/claude/.bashrc
USER root

WORKDIR /workspace
COPY --from=go-builder /out/claude-docker /app/claude-docker
COPY entrypoint.sh /entrypoint.sh
RUN chmod +x /entrypoint.sh

ENTRYPOINT ["/usr/bin/tini", "--", "/entrypoint.sh"]
EXPOSE 8080
