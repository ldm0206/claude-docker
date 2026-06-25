FROM node:22-bookworm-slim AS base

ENV DEBIAN_FRONTEND=noninteractive \
    DISABLE_AUTOUPDATER=1 \
    DISABLE_UPDATES=1 \
    CLAUDE_CONFIG_DIR=/home/claude/.claude

RUN apt-get update && apt-get install -y --no-install-recommends \
        git ripgrep curl ca-certificates jq tini sudo \
    && rm -rf /var/lib/apt/lists/*

# Non-root user
RUN useradd -m -u 1000 -s /bin/bash claude \
    && install -d -o claude -g claude /workspace

# Claude Code native installer
RUN curl -fsSL https://claude.ai/install.sh | bash

WORKDIR /workspace
