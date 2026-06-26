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

# Install the claude CLI AS the claude user so it lands in
# /home/claude/.local/bin (matches the runtime HOME and buildClaudeEnv's PATH).
USER claude
RUN curl -fsSL https://claude.ai/install.sh | bash
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
