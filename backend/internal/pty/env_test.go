package pty

import (
	"fmt"
	"os"
	"strings"
	"testing"

	"github.com/ldm0206/claude-docker/backend/internal/config"
)

func TestBuildClaudeEnv(t *testing.T) {
	origPath := os.Getenv("PATH")
	t.Setenv("PATH", "/usr/bin:/bin")
	t.Setenv("HOME", "/tmp/wrong")
	t.Setenv("CLAUDE_CONFIG_DIR", "/tmp/skip")

	cfg := &config.Config{
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
		"CLAUDE_CONFIG_DIR=/tmp/skip",
	} {
		if !strings.Contains(j, want) {
			t.Fatalf("env missing %q\n%s", want, j)
		}
	}
	expectedPathPrefix := fmt.Sprintf("/home/claude/.local/bin:/usr/bin:/bin")
	if !strings.Contains(j, expectedPathPrefix) {
		t.Fatalf("PATH must include claude bin, kept inherited\n%s", j)
	}

	if strings.Contains(j, "PATH="+origPath) {
		t.Fatalf("PATH should include inherited PATH, not replace it\n%s", j)
	}
}
