package pty

import (
	"fmt"
	"os"
	"sort"
	"strings"

	"github.com/ldm0206/claude-docker/backend/internal/config"
)

const claudeBin = "/home/claude/.local/bin"
const sharedClaudeBin = "/opt/claude/bin"

// proxyEnvKeys are env var names that must NOT be inherited from the server's
// own environment into a PTY. A leaked ALL_PROXY=socks5://... from the host
// (docker run -e, compose environment, etc.) makes claude's Node socks layer
// crash the OAuth handshake (protocol mismatch) or assert (ERR_ASSERTION).
// Proxying is opt-in via the explicit cfg.*Proxy fields (read from dedicated
// env in config.Load), never via os.Environ passthrough.
var proxyEnvKeys = map[string]struct{}{
	"ALL_PROXY":     {},
	"all_proxy":     {},
	"HTTP_PROXY":    {},
	"http_proxy":    {},
	"HTTPS_PROXY":   {},
	"https_proxy":   {},
	"NO_PROXY":      {},
	"no_proxy":      {},
}

// inheritedEnv snapshots os.Environ() into a map, dropping any proxy var so a
// host-side leak cannot reach the spawned shell. Proxy injection is the caller's
// explicit job via cfg.
func inheritedEnv() map[string]string {
	m := make(map[string]string, 32)
	for _, e := range os.Environ() {
		key, val, ok := strings.Cut(e, "=")
		if !ok {
			continue
		}
		if _, drop := proxyEnvKeys[key]; drop {
			continue
		}
		m[key] = val
	}
	return m
}

func BuildClaudeEnv(cfg *config.Config) []string {
	envMap := inheritedEnv()
	set := func(k, v string) {
		envMap[k] = v
	}
	set("HOME", "/home/claude")
	path := envMap["PATH"]
	set("PATH", fmt.Sprintf("%s:%s", claudeBin, path))
	// Force a 256-color terminal so programs emit full ANSI colors and, with
	// our entrypoint tmux/shim config, OSC 52 clipboard sequences that xterm.js
	// forwards to the browser clipboard.
	if _, ok := envMap["TERM"]; !ok {
		set("TERM", "xterm-256color")
	}
	if _, ok := envMap["COLORTERM"]; !ok {
		set("COLORTERM", "truecolor")
	}
	if _, ok := envMap["CLAUDE_CONFIG_DIR"]; !ok {
		set("CLAUDE_CONFIG_DIR", "/home/claude/.claude")
	}
	if cfg.AnthropicAPIKey != "" {
		set("ANTHROPIC_API_KEY", cfg.AnthropicAPIKey)
	}
	if cfg.AnthropicAuthToken != "" {
		set("ANTHROPIC_AUTH_TOKEN", cfg.AnthropicAuthToken)
	}
	if cfg.AnthropicBaseURL != "" {
		set("ANTHROPIC_BASE_URL", cfg.AnthropicBaseURL)
	}
	for _, p := range []struct{ hi, lo, val string }{
		{"HTTP_PROXY", "http_proxy", cfg.HTTPProxy},
		{"HTTPS_PROXY", "https_proxy", cfg.HTTPSProxy},
		{"ALL_PROXY", "all_proxy", cfg.AllProxy},
	} {
		if p.val != "" {
			set(p.hi, p.val)
			set(p.lo, p.val)
		}
	}
	set("NO_PROXY", cfg.NoProxy)
	set("no_proxy", cfg.NoProxy)
	set("API_TIMEOUT_MS", fmt.Sprintf("%d", cfg.APITimeoutMS))

	env := make([]string, 0, len(envMap))
	for k, v := range envMap {
		env = append(env, k+"="+v)
	}
	sort.Strings(env)
	return env
}

func BuildUserEnv(cfg *config.Config, username, claudeConfigDir string) []string {
	envMap := inheritedEnv()
	set := func(k, v string) {
		envMap[k] = v
	}
	set("HOME", fmt.Sprintf("/home/%s", username))
	path := envMap["PATH"]
	set("PATH", fmt.Sprintf("%s:%s", sharedClaudeBin, path))
	if _, ok := envMap["TERM"]; !ok {
		set("TERM", "xterm-256color")
	}
	if _, ok := envMap["COLORTERM"]; !ok {
		set("COLORTERM", "truecolor")
	}
	set("CLAUDE_CONFIG_DIR", claudeConfigDir)
	if cfg.AnthropicAPIKey != "" {
		set("ANTHROPIC_API_KEY", cfg.AnthropicAPIKey)
	}
	if cfg.AnthropicAuthToken != "" {
		set("ANTHROPIC_AUTH_TOKEN", cfg.AnthropicAuthToken)
	}
	if cfg.AnthropicBaseURL != "" {
		set("ANTHROPIC_BASE_URL", cfg.AnthropicBaseURL)
	}
	for _, p := range []struct{ hi, lo, val string }{
		{"HTTP_PROXY", "http_proxy", cfg.HTTPProxy},
		{"HTTPS_PROXY", "https_proxy", cfg.HTTPSProxy},
		{"ALL_PROXY", "all_proxy", cfg.AllProxy},
	} {
		if p.val != "" {
			set(p.hi, p.val)
			set(p.lo, p.val)
		}
	}
	set("NO_PROXY", cfg.NoProxy)
	set("no_proxy", cfg.NoProxy)
	set("API_TIMEOUT_MS", fmt.Sprintf("%d", cfg.APITimeoutMS))
	env := make([]string, 0, len(envMap))
	for k, v := range envMap {
		env = append(env, k+"="+v)
	}
	sort.Strings(env)
	return env
}
