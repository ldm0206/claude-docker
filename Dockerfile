FROM node:22-bookworm-slim

ENV DEBIAN_FRONTEND=noninteractive \
    DISABLE_AUTOUPDATER=1 \
    DISABLE_UPDATES=1 \
    CLAUDE_CONFIG_DIR=/home/claude/.claude

RUN apt-get update && apt-get install -y --no-install-recommends \
        git ripgrep curl ca-certificates jq tini gosu sudo openssl \
        python3 make g++ \
    && rm -rf /var/lib/apt/lists/*

RUN useradd -m -s /bin/bash claude \
    && install -d -o claude -g claude /workspace

# Download the claude binary directly.  The official install.sh delegates to
# `claude install`, which skips writing the launcher when DISABLE_UPDATES=1
# ("Updates are disabled by your administrator").  We download the binary
# ourselves and symlink it into the expected PATH location instead.
USER claude
RUN set -e; \
    CLAUDE_DL_DIR="/home/claude/.claude/downloads"; \
    mkdir -p "$CLAUDE_DL_DIR" /home/claude/.local/bin; \
    LATEST=$(curl -fsSL https://downloads.claude.ai/claude-code-releases/latest); \
    MANIFEST=$(curl -fsSL "https://downloads.claude.ai/claude-code-releases/$LATEST/manifest.json"); \
    CHECKSUM=$(echo "$MANIFEST" | jq -r '.platforms["linux-x64"].checksum'); \
    BINARY="$CLAUDE_DL_DIR/claude-$LATEST-linux-x64"; \
    curl -fsSL -o "$BINARY" "https://downloads.claude.ai/claude-code-releases/$LATEST/linux-x64/claude"; \
    echo "$CHECKSUM  $BINARY" | sha256sum -c; \
    chmod +x "$BINARY"; \
    ln -sf "$BINARY" /home/claude/.local/bin/claude; \
    test -x /home/claude/.local/bin/claude
USER root

WORKDIR /workspace

COPY server /app/server
COPY web /app/web
RUN cd /app/server && npm install --omit=dev \
    && cd /app/web && npm install && npm run build \
    && apt-get purge -y --auto-remove python3 make g++ \
    && rm -rf /var/lib/apt/lists/*

COPY entrypoint.sh /entrypoint.sh
RUN chmod +x /entrypoint.sh

ENTRYPOINT ["/usr/bin/tini", "--", "/entrypoint.sh"]
EXPOSE 8080
