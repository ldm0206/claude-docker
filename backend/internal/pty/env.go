package pty

import (
	"fmt"
	"os"
	"sort"
	"strings"

	"github.com/ldm0206/claude-docker/backend/internal/config"
)

const sharedClaudeBin = "/opt/claude/bin"

// AnthropicCreds carries the three admin-managed Anthropic values that are
// injected into a user terminal only when non-empty.
type AnthropicCreds struct {
	APIKey    string
	BaseURL   string
	AuthToken string
}

func BuildUserEnv(cfg *config.Config, creds AnthropicCreds, username, claudeConfigDir string) []string {
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
	if _, ok := envMap["TERM"]; !ok {
		set("TERM", "xterm-256color")
	}
	if _, ok := envMap["COLORTERM"]; !ok {
		set("COLORTERM", "truecolor")
	}
	set("CLAUDE_CONFIG_DIR", claudeConfigDir)
	if creds.APIKey != "" {
		set("ANTHROPIC_API_KEY", creds.APIKey)
	}
	if creds.BaseURL != "" {
		set("ANTHROPIC_BASE_URL", creds.BaseURL)
	}
	if creds.AuthToken != "" {
		set("ANTHROPIC_AUTH_TOKEN", creds.AuthToken)
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
