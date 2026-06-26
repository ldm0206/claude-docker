FROM node:22-bookworm-slim AS base

ENV DEBIAN_FRONTEND=noninteractive \
    DISABLE_AUTOUPDATER=1 \
    DISABLE_UPDATES=1 \
    CLAUDE_CONFIG_DIR=/home/claude/.claude

RUN apt-get update && apt-get install -y --no-install-recommends \
        git ripgrep curl ca-certificates jq tini sudo gosu \
    && rm -rf /var/lib/apt/lists/*

# Non-root user
RUN useradd -m -u 1000 -s /bin/bash claude \
    && install -d -o claude -g claude /workspace

# Claude Code native installer
RUN curl -fsSL https://claude.ai/install.sh | bash

COPY entrypoint.sh /entrypoint.sh
RUN chmod +x /entrypoint.sh

# App source
COPY server /app/server
COPY web /app/web
RUN cd /app/server && npm install --omit=dev && cd /app/web && npm install && npm run build

USER root
ENTRYPOINT ["/usr/bin/tini", "--", "/entrypoint.sh"]
EXPOSE 8080

WORKDIR /workspace
