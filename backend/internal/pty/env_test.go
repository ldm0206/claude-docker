package pty

import (
	"fmt"
	"os"
	"strings"
	"testing"

	"github.com/ldm0206/claude-docker/backend/internal/config"
)

func TestBuildUserEnv_InjectsAllCreds(t *testing.T) {
	t.Setenv("PATH", "/usr/bin:/bin")
	t.Setenv("HOME", "/tmp/wrong")
	t.Setenv("CLAUDE_CONFIG_DIR", "/tmp/inherited")

	cfg := &config.Config{
		HTTPProxy:    "http://p:7890",
		NoProxy:      "localhost,127.0.0.1",
		APITimeoutMS: 300000,
	}
	creds := AnthropicCreds{
		APIKey:    "sk-abc",
		BaseURL:   "http://gw",
		AuthToken: "tok",
	}
	env := BuildUserEnv(cfg, creds, "alice", "/data/alice/claude-config")
	j := strings.Join(env, "\n")

	for _, want := range []string{
		"HOME=/home/alice",
		"CLAUDE_CONFIG_DIR=/data/alice/claude-config",
		"ANTHROPIC_API_KEY=sk-abc",
		"ANTHROPIC_BASE_URL=http://gw",
		"ANTHROPIC_AUTH_TOKEN=tok",
		"HTTP_PROXY=http://p:7890",
		"http_proxy=http://p:7890",
		"API_TIMEOUT_MS=300000",
	} {
		if !strings.Contains(j, want) {
			t.Fatalf("env missing %q\n%s", want, j)
		}
	}
}

func TestBuildUserEnv_EmptyCredsNotInjected(t *testing.T) {
	t.Setenv("PATH", "/usr/bin:/bin")
	t.Setenv("HOME", "/tmp/wrong")
	for _, k := range []string{"ANTHROPIC_API_KEY", "ANTHROPIC_BASE_URL", "ANTHROPIC_AUTH_TOKEN"} {
		v, ok := os.LookupEnv(k)
		if ok {
			t.Cleanup(func() { os.Setenv(k, v) })
		} else {
			t.Cleanup(func() { os.Unsetenv(k) })
		}
		os.Unsetenv(k)
	}

	cfg := &config.Config{APITimeoutMS: 300000}
	env := BuildUserEnv(cfg, AnthropicCreds{}, "alice", "/data/alice/claude-config")
	j := strings.Join(env, "\n")

	for _, absent := range []string{
		"ANTHROPIC_API_KEY=",
		"ANTHROPIC_BASE_URL=",
		"ANTHROPIC_AUTH_TOKEN=",
	} {
		if strings.Contains(j, absent) {
			t.Fatalf("empty cred must not be injected, found %q\n%s", absent, j)
		}
	}

	expectedPathPrefix := "/opt/claude/bin:/usr/bin:/bin"
	if !strings.Contains(j, expectedPathPrefix) {
		t.Fatalf("PATH must prepend /opt/claude/bin before inherited PATH\n%s", j)
	}
}

func TestBuildUserEnv_ParameterizedConfigDir(t *testing.T) {
	t.Setenv("CLAUDE_CONFIG_DIR", "/tmp/inherited")
	cfg := &config.Config{APITimeoutMS: 1}
	env := BuildUserEnv(cfg, AnthropicCreds{}, "alice", "/data/alice/claude-config")
	j := strings.Join(env, "\n")
	if strings.Contains(j, "CLAUDE_CONFIG_DIR=/tmp/inherited") {
		t.Fatalf("CLAUDE_CONFIG_DIR must use parameterized dir, not inherited\n%s", j)
	}
	if !strings.Contains(j, "CLAUDE_CONFIG_DIR=/data/alice/claude-config") {
		t.Fatalf("CLAUDE_CONFIG_DIR must be set to parameter\n%s", j)
	}
}

// ensure HOME is parameterized and PATH prepends /opt/claude/bin
func TestBuildUserEnv_HomeAndPath(t *testing.T) {
	t.Setenv("PATH", "/usr/bin:/bin")
	origPath := os.Getenv("PATH")
	cfg := &config.Config{APITimeoutMS: 1}
	env := BuildUserEnv(cfg, AnthropicCreds{}, "alice", "/x")
	j := strings.Join(env, "\n")
	if strings.Contains(j, "HOME=/home/claude") {
		t.Fatalf("HOME must be /home/alice, not /home/claude\n%s", j)
	}
	if !strings.Contains(j, fmt.Sprintf("/opt/claude/bin:%s", origPath)) {
		t.Fatalf("PATH must prepend /opt/claude/bin before inherited PATH\n%s", j)
	}
}
