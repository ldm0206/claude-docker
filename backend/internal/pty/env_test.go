package pty

import (
	"strings"
	"testing"

	"github.com/ldm0206/claude-docker/backend/internal/config"
)

func TestBuildClaudeEnv(t *testing.T) {
	cfg := &config.Config{
		AccessKey:          "k",
		AnthropicAuthToken: "tok",
		AnthropicBaseURL:   "http://gw",
		HTTPProxy:          "http://p:7890",
		NoProxy:            "localhost,127.0.0.1",
		APITimeoutMS:       600000,
	}
	env := BuildClaudeEnv(cfg)
	j := strings.Join(env, "\n")
	for _, want := range []string{
		"HOME=/home/claude",
		"ANTHROPIC_AUTH_TOKEN=tok",
		"ANTHROPIC_BASE_URL=http://gw",
		"HTTP_PROXY=http://p:7890",
		"http_proxy=http://p:7890",
		"API_TIMEOUT_MS=600000",
	} {
		if !strings.Contains(j, want) {
			t.Fatalf("env missing %q\n%s", want, j)
		}
	}
	if !strings.Contains(j, "/home/claude/.local/bin") {
		t.Fatalf("PATH must include claude bin\n%s", j)
	}
}
