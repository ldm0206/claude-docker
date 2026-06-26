FROM node:22-bookworm-slim

ENV DEBIAN_FRONTEND=noninteractive \
    DISABLE_AUTOUPDATER=1 \
    DISABLE_UPDATES=1 \
    CLAUDE_CONFIG_DIR=/home/claude/.claude

RUN apt-get update && apt-get install -y --no-install-recommends \
        git ripgrep curl ca-certificates jq tini gosu sudo \
    && rm -rf /var/lib/apt/lists/*

RUN useradd -m -u 1000 -s /bin/bash claude \
    && install -d -o claude -g claude /workspace

RUN curl -fsSL https://claude.ai/install.sh | bash

WORKDIR /workspace

COPY server /app/server
RUN cd /app/server && npm install --omit=dev

COPY web /app/web
RUN cd /app/web && npm install && npm run build

COPY entrypoint.sh /entrypoint.sh
RUN chmod +x /entrypoint.sh

ENTRYPOINT ["/usr/bin/tini", "--", "/entrypoint.sh"]
EXPOSE 8080
