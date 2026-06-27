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
# ourselves into /home/claude/.local/bin (a REAL file, NOT a symlink) so it is
# not shadowed by the claude-config volume mounted at /home/claude/.claude.
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
    test -x /home/claude/.local/bin/claude; \
    echo 'export PATH="$HOME/.local/bin:$PATH"' >> /home/claude/.bashrc
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
