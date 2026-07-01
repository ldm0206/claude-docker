package config

import "testing"

func envOf(m map[string]string) func(string) (string, bool) {
	return func(k string) (string, bool) { v, ok := m[k]; return v, ok }
}

func TestLoadValid(t *testing.T) {
	c, err := Load(envOf(map[string]string{
		"SESSION_SECRET": "sssh",
		"HTTP_PROXY":     "http://p:7890",
	}))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if c.HTTPProxy != "http://p:7890" {
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
