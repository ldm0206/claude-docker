package pty

import (
	"fmt"
	"os"
	"strings"
	"testing"

	"github.com/ldm0206/claude-docker/backend/internal/config"
)

func TestBuildUserEnv_InjectsAuthToken(t *testing.T) {
	t.Setenv("PATH", "/usr/bin:/bin")
	t.Setenv("HOME", "/tmp/wrong")
	t.Setenv("CLAUDE_CONFIG_DIR", "/tmp/inherited")

	cfg := &config.Config{
		HTTPProxy:    "http://p:7890",
		NoProxy:      "localhost,127.0.0.1",
		APITimeoutMS: 300000,
	}
	env := BuildUserEnv(cfg, "tok", "alice", "/data/alice/claude-config")
	j := strings.Join(env, "\n")

	for _, want := range []string{
		"HOME=/home/alice",
		"CLAUDE_CONFIG_DIR=/data/alice/claude-config",
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

func TestBuildUserEnv_EmptyTokenNotInjected(t *testing.T) {
	t.Setenv("PATH", "/usr/bin:/bin")
	t.Setenv("HOME", "/tmp/wrong")
	v, ok := os.LookupEnv("ANTHROPIC_AUTH_TOKEN")
	if ok {
		t.Cleanup(func() { os.Setenv("ANTHROPIC_AUTH_TOKEN", v) })
	} else {
		t.Cleanup(func() { os.Unsetenv("ANTHROPIC_AUTH_TOKEN") })
	}
	os.Unsetenv("ANTHROPIC_AUTH_TOKEN")

	cfg := &config.Config{APITimeoutMS: 300000}
	env := BuildUserEnv(cfg, "", "alice", "/data/alice/claude-config")
	j := strings.Join(env, "\n")

	if strings.Contains(j, "ANTHROPIC_AUTH_TOKEN=") {
		t.Fatalf("empty token must not be injected\n%s", j)
	}

	expectedPathPrefix := "/opt/claude/bin:/usr/bin:/bin"
	if !strings.Contains(j, expectedPathPrefix) {
		t.Fatalf("PATH must prepend /opt/claude/bin before inherited PATH\n%s", j)
	}
}

func TestBuildUserEnv_ParameterizedConfigDir(t *testing.T) {
	t.Setenv("CLAUDE_CONFIG_DIR", "/tmp/inherited")
	cfg := &config.Config{APITimeoutMS: 1}
	env := BuildUserEnv(cfg, "", "alice", "/data/alice/claude-config")
	j := strings.Join(env, "\n")
	if strings.Contains(j, "CLAUDE_CONFIG_DIR=/tmp/inherited") {
		t.Fatalf("CLAUDE_CONFIG_DIR must use parameterized dir, not inherited\n%s", j)
	}
	if !strings.Contains(j, "CLAUDE_CONFIG_DIR=/data/alice/claude-config") {
		t.Fatalf("CLAUDE_CONFIG_DIR must be set to parameter\n%s", j)
	}
}

func TestBuildUserEnv_HomeAndPath(t *testing.T) {
	t.Setenv("PATH", "/usr/bin:/bin")
	origPath := os.Getenv("PATH")
	cfg := &config.Config{APITimeoutMS: 1}
	env := BuildUserEnv(cfg, "", "alice", "/x")
	j := strings.Join(env, "\n")
	if strings.Contains(j, "HOME=/home/claude") {
		t.Fatalf("HOME must be /home/alice, not /home/claude\n%s", j)
	}
	if !strings.Contains(j, fmt.Sprintf("/opt/claude/bin:%s", origPath)) {
		t.Fatalf("PATH must prepend /opt/claude/bin before inherited PATH\n%s", j)
	}
}

func TestBuildUserEnv_HostProxyNotInherited(t *testing.T) {
	t.Setenv("PATH", "/usr/bin:/bin")
	t.Setenv("ALL_PROXY", "socks5://leak:1080")
	t.Setenv("HTTP_PROXY", "http://leak:8080")
	t.Setenv("https_proxy", "http://leak:8443")
	cfg := &config.Config{APITimeoutMS: 1}
	env := BuildUserEnv(cfg, "", "alice", "/x")
	j := strings.Join(env, "\n")
	for _, leak := range []string{"socks5://leak", "http://leak:8080", "http://leak:8443"} {
		if strings.Contains(j, leak) {
			t.Fatalf("host proxy leaked into env: %q\n%s", leak, j)
		}
	}
}

func TestBuildUserEnv_HostAnthropicNotInherited(t *testing.T) {
	t.Setenv("PATH", "/usr/bin:/bin")
	t.Setenv("ANTHROPIC_API_KEY", "sk-host-leak")
	t.Setenv("ANTHROPIC_AUTH_TOKEN", "tok-host-leak")
	t.Setenv("ANTHROPIC_BASE_URL", "http://host-leak")
	cfg := &config.Config{APITimeoutMS: 1}
	env := BuildUserEnv(cfg, "", "alice", "/x")
	j := strings.Join(env, "\n")
	for _, leak := range []string{"sk-host-leak", "tok-host-leak", "http://host-leak"} {
		if strings.Contains(j, leak) {
			t.Fatalf("host ANTHROPIC var leaked into env: %q\n%s", leak, j)
		}
	}
}
