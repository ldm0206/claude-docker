#!/usr/bin/env bash
set -euo pipefail

# Run as root: the server setuids into per-user accounts for isolation.

# --- Storage roots + drop into the server ---
mkdir -p /workspace /data /home
chmod 0755 /home

# /etc/profile (sourced by `bash -l`) UNCONDITIONALLY resets PATH, which would
# wipe /opt/claude/bin that the Go server injects via BuildUserEnv — leaving
# `claude` unfindable in the user's shell. Install a profile.d snippet that
# re-prepends /opt/claude/bin and the user's ~/.local/bin AFTER /etc/profile's
# reset, and ensure /etc/profile actually sources profile.d (debian base does,
# but be defensive so the fix is not silently a no-op).
mkdir -p /etc/profile.d
cat > /etc/profile.d/claude-docker.sh <<'PROFILE'
# Re-prepend tool paths wiped by /etc/profile's PATH reset.
case ":${PATH}:" in
  *":/opt/claude/bin:"*) ;;
  *) PATH="/opt/claude/bin:${PATH}" ;;
esac
if [ -d "${HOME}/.local/bin" ]; then
  case ":${PATH}:" in
    *":${HOME}/.local/bin:"*) ;;
    *) PATH="${HOME}/.local/bin:${PATH}" ;;
  esac
fi
export PATH
PROFILE
chmod 0644 /etc/profile.d/claude-docker.sh
if ! grep -q '/etc/profile.d' /etc/profile 2>/dev/null; then
  cat >> /etc/profile <<'PROFILE'

# Source profile.d snippets (claude-docker PATH fix lives there).
if [ -d /etc/profile.d ]; then
  for i in /etc/profile.d/*.sh; do
    if [ -r "$i" ]; then . "$i"; fi
  done
  unset i
fi
PROFILE
fi

# --- OSC 52 clipboard bridge ---
# The web terminal (xterm.js) implements OSC 52: a program prints
#   ESC ] 52 ; c ; <base64 payload> BEL
# and xterm.js writes the decoded bytes to the browser clipboard (when the
# page is in a secure context — https or localhost). To make `tmux` copy-mode,
# `pbcopy`/`pbpaste`, and editors that shell out to those actually reach the
# user's real clipboard, we install:
#   1. shell functions pbcopy/pbpaste/clipcopy/clippaste that emit/read OSC 52,
#   2. a global /etc/tmux.conf with `set -g set-clipboard on` (+ allow passthrough)
#      so tmux forwards OSC 52 from inner programs and its own copy-mode.
# tmux is already installed in the runtime stage (apt: tmux).
cat > /etc/profile.d/claude-clipboard.sh <<'CLIP'
# pbcopy: read stdin, base64, emit OSC 52 to the controlling terminal.
pbcopy() { local d; d=$(base64 -w0 2>/dev/null || base64); printf '\033]52;c;%s\a' "$d"; }
clipcopy() { pbcopy "$@"; }
# pbpaste: request OSC 52 read. xterm.js replies by writing the clipboard into
# the terminal input stream, so this just triggers the request and lets the
# bytes arrive as if typed. Best-effort — not all terminals answer.
pbpaste() { printf '\033]52;c;?\a'; }
clippaste() { pbpaste "$@"; }
export -f pbcopy clipcopy pbpaste clippaste
CLIP
chmod 0644 /etc/profile.d/claude-clipboard.sh

# Global tmux config: enable OSC 52 clipboard both ways and allow passthrough
# so sequences emitted inside a tmux pane reach the outer xterm.js.
cat > /etc/tmux.conf <<'TMUX'
set -g set-clipboard on
set -g default-terminal "tmux-256color"
set -ag terminal-overrides ",xterm-256color:RGB"
set -g allow-passthrough on
TMUX
chmod 0644 /etc/tmux.conf

exec /app/claude-docker