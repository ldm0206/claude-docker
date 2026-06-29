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

func BuildClaudeEnv(cfg *config.Config) []string {
	envMap := make(map[string]string, 32)
	for _, e := range os.Environ() {
		if key, val, ok := strings.Cut(e, "="); ok {
			envMap[key] = val
		}
	}
	set := func(k, v string) {
		envMap[k] = v
	}
	set("HOME", "/home/claude")
	path := envMap["PATH"]
	set("PATH", fmt.Sprintf("%s:%s", claudeBin, path))
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
	envMap := make(map[string]string, 32)
	for _, e := range os.Environ() {
		if key, val, ok := strings.Cut(e, "="); ok {
			envMap[key] = val
		}
	}
	set := func(k, v string) {
		envMap[k] = v
	}
	set("HOME", fmt.Sprintf("/home/%s", username))
	path := envMap["PATH"]
	set("PATH", fmt.Sprintf("%s:%s", sharedClaudeBin, path))
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
