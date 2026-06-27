package pty

import (
	"fmt"
	"os"

	"github.com/ldm0206/claude-docker/backend/internal/config"
)

const claudeBin = "/home/claude/.local/bin"

func BuildClaudeEnv(cfg *config.Config) []string {
	env := os.Environ()
	set := func(k, v string) { env = append(env, k+"="+v) }
	set("HOME", "/home/claude")
	set("PATH", fmt.Sprintf("%s:%s", claudeBin, os.Getenv("PATH")))
	set("CLAUDE_CONFIG_DIR", "/home/claude/.claude")
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
	return env
}
