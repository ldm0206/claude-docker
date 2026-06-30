package config

import (
	"fmt"
	"strconv"
)

type Config struct {
	AnthropicAPIKey        string
	AnthropicAuthToken     string
	AnthropicBaseURL       string
	HTTPProxy              string
	HTTPSProxy             string
	AllProxy               string
	NoProxy                string
	APITimeoutMS           int
	Port                   int
	SessionSecret          string
	BootstrapAdminUser     string
	BootstrapAdminPassword string
	CookieSameSite         string
	TemplateUser           string
}

func Load(get func(string) (string, bool)) (*Config, error) {
	c := &Config{APITimeoutMS: 600000, Port: 8080, NoProxy: "localhost,127.0.0.1"}
	opt := func(k string) string { v, _ := get(k); return v }
	c.SessionSecret = opt("SESSION_SECRET")
	c.AnthropicAPIKey = opt("ANTHROPIC_API_KEY")
	c.AnthropicAuthToken = opt("ANTHROPIC_AUTH_TOKEN")
	c.AnthropicBaseURL = opt("ANTHROPIC_BASE_URL")
	c.HTTPProxy = opt("HTTP_PROXY")
	c.HTTPSProxy = opt("HTTPS_PROXY")
	c.AllProxy = opt("ALL_PROXY")
	c.BootstrapAdminUser = opt("BOOTSTRAP_ADMIN_USER")
	c.BootstrapAdminPassword = opt("BOOTSTRAP_ADMIN_PASSWORD")
	c.CookieSameSite = opt("COOKIE_SAMESITE")
	c.TemplateUser = opt("CLAUDE_TEMPLATE_USER")
	if c.CookieSameSite == "" {
		c.CookieSameSite = "none"
	}
	if v, ok := get("NO_PROXY"); ok {
		c.NoProxy = v
	}
	if v, ok := get("API_TIMEOUT_MS"); ok {
		n, err := strconv.Atoi(v)
		if err != nil || n <= 0 {
			return nil, fmt.Errorf("API_TIMEOUT_MS must be a positive number")
		}
		c.APITimeoutMS = n
	}
	if v, ok := get("PORT"); ok {
		if n, err := strconv.Atoi(v); err == nil {
			c.Port = n
		}
	}
	return c, nil
}
