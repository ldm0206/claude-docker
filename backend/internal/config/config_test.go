package config

import "testing"

func envOf(m map[string]string) func(string) (string, bool) {
	return func(k string) (string, bool) { v, ok := m[k]; return v, ok }
}

func TestLoadValid(t *testing.T) {
	c, err := Load(envOf(map[string]string{
		"ACCESS_KEY":           "sekret",
		"SESSION_SECRET":       "sssh",
		"ANTHROPIC_AUTH_TOKEN": "tok",
	}))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if c.AccessKey != "sekret" || c.AnthropicAuthToken != "tok" {
		t.Fatalf("got %+v", c)
	}
	if c.APITimeoutMS != 600000 || c.Port != 8080 || c.NoProxy != "localhost,127.0.0.1" {
		t.Fatalf("defaults wrong: %+v", c)
	}
}

func TestLoadMissingAccessKey(t *testing.T) {
	_, err := Load(envOf(map[string]string{"SESSION_SECRET": "x"}))
	if err == nil {
		t.Fatal("expected error for missing ACCESS_KEY")
	}
}

func TestLoadBadTimeout(t *testing.T) {
	_, err := Load(envOf(map[string]string{"ACCESS_KEY": "k", "API_TIMEOUT_MS": "nope"}))
	if err == nil {
		t.Fatal("expected error for bad API_TIMEOUT_MS")
	}
}
