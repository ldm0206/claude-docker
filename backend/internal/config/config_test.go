package config

import "testing"

func envOf(m map[string]string) func(string) (string, bool) {
	return func(k string) (string, bool) { v, ok := m[k]; return v, ok }
}

func TestLoadValid(t *testing.T) {
	c, err := Load(envOf(map[string]string{
		"SESSION_SECRET":       "sssh",
		"ANTHROPIC_AUTH_TOKEN": "tok",
	}))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if c.AnthropicAuthToken != "tok" {
		t.Fatalf("got %+v", c)
	}
	if c.APITimeoutMS != 600000 || c.Port != 8080 || c.NoProxy != "localhost,127.0.0.1" {
		t.Fatalf("defaults wrong: %+v", c)
	}
}

func TestLoadNoAccessKeyRequired(t *testing.T) {
	c, err := Load(envOf(map[string]string{"SESSION_SECRET": "s"}))
	if err != nil {
		t.Fatalf("Load must not require ACCESS_KEY: %v", err)
	}
	if c.SessionSecret != "s" {
		t.Fatalf("session secret not read")
	}
}

func TestLoadBootstrap(t *testing.T) {
	c, _ := Load(envOf(map[string]string{
		"SESSION_SECRET":          "s",
		"BOOTSTRAP_ADMIN_USER":    "root",
		"BOOTSTRAP_ADMIN_PASSWORD": "p",
	}))
	if c.BootstrapAdminUser != "root" || c.BootstrapAdminPassword != "p" {
		t.Fatalf("bootstrap env not read: %+v", c)
	}
}

func TestLoadBadTimeout(t *testing.T) {
	_, err := Load(envOf(map[string]string{"SESSION_SECRET": "k", "API_TIMEOUT_MS": "nope"}))
	if err == nil {
		t.Fatal("expected error for bad API_TIMEOUT_MS")
	}
}

// TestLoadIgnoresGenericProxyEnv is the regression test for the ERR_ASSERTION
// connection bug. A host leaked HTTP_PROXY / HTTPS_PROXY / ALL_PROXY into the
// server's own env (docker run -e, compose env_file) MUST NOT be picked up by
// config.Load — those names are exactly what pty.inheritedEnv strips, and
// re-injecting them via cfg.*Proxy would crash claude's Node socks layer on a
// fresh OAuth handshake. Proxy is opt-in via the dedicated CLAUDE_*_PROXY names.
func TestLoadIgnoresGenericProxyEnv(t *testing.T) {
	c, err := Load(envOf(map[string]string{
		"SESSION_SECRET": "s",
		"HTTP_PROXY":     "http://host.docker.internal:7890",
		"HTTPS_PROXY":    "http://host.docker.internal:7890",
		"ALL_PROXY":      "socks5://host.docker.internal:1080",
		"NO_PROXY":       "host.docker.internal",
	}))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if c.HTTPProxy != "" || c.HTTPSProxy != "" || c.AllProxy != "" {
		t.Fatalf("generic *_PROXY leaked into cfg: %+v", c)
	}
	if c.NoProxy != "localhost,127.0.0.1" {
		t.Fatalf("NO_PROXY should be the default, got %q", c.NoProxy)
	}
}

// TestLoadReadsDedicatedProxyEnv confirms the opt-in path works.
func TestLoadReadsDedicatedProxyEnv(t *testing.T) {
	c, err := Load(envOf(map[string]string{
		"SESSION_SECRET":    "s",
		"CLAUDE_HTTP_PROXY": "http://p:7890",
		"CLAUDE_ALL_PROXY":  "socks5://p:1080",
		"CLAUDE_NO_PROXY":   "localhost,10.0.0.0/8",
	}))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if c.HTTPProxy != "http://p:7890" || c.AllProxy != "socks5://p:1080" {
		t.Fatalf("dedicated proxy env not read: %+v", c)
	}
	if c.NoProxy != "localhost,10.0.0.0/8" {
		t.Fatalf("CLAUDE_NO_PROXY not honored: %q", c.NoProxy)
	}
}
