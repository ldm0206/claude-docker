package server

import (
	"encoding/json"
	"net/http"

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

type anthropicCredsReq struct {
	APIKey    string `json:"api_key"`
	BaseURL   string `json:"base_url"`
	AuthToken string `json:"auth_token"`
}

func (s *Server) handleAdminGetAnthropic(w http.ResponseWriter, r *http.Request) {
	c := s.resolveAnthropic()
	writeJSON(w, 200, map[string]any{
		"api_key":    c.APIKey,
		"base_url":   c.BaseURL,
		"auth_token": c.AuthToken,
	})
}

func (s *Server) handleAdminSetAnthropic(w http.ResponseWriter, r *http.Request) {
	var b anthropicCredsReq
	if err := json.NewDecoder(r.Body).Decode(&b); err != nil {
		writeJSON(w, 400, map[string]any{"error": "bad body"})
		return
	}
	for _, kv := range []struct{ k, v string }{
		{apiKeyKey, b.APIKey},
		{baseURLKey, b.BaseURL},
		{authTokenKey, b.AuthToken},
	} {
		if err := s.db.SetSetting(kv.k, kv.v); err != nil {
			writeJSON(w, 500, map[string]any{"error": "save setting"})
			return
		}
	}
	writeJSON(w, 200, map[string]any{
		"api_key":    b.APIKey,
		"base_url":   b.BaseURL,
		"auth_token": b.AuthToken,
	})
}
