package server

import (
	"github.com/ldm0206/claude-docker/backend/internal/pty"
)

const (
	apiKeyKey    = "anthropic_api_key"
	baseURLKey   = "anthropic_base_url"
	authTokenKey = "anthropic_auth_token"
)

// resolveAnthropic reads the three admin-managed Anthropic settings. A missing
// key resolves to "" and means that variable is not injected.
func (s *Server) resolveAnthropic() pty.AnthropicCreds {
	get := func(k string) string {
		v, err := s.db.GetSetting(k)
		if err != nil {
			return ""
		}
		return v
	}
	return pty.AnthropicCreds{
		APIKey:    get(apiKeyKey),
		BaseURL:   get(baseURLKey),
		AuthToken: get(authTokenKey),
	}
}
