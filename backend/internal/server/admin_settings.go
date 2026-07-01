package server

import (
	"encoding/json"
	"net/http"
)

// authTokenKey is the DB settings key for the admin-managed ANTHROPIC_AUTH_TOKEN
// injected into non-admin users' terminals. Empty = do not inject.
const authTokenKey = "anthropic_auth_token"

// resolveAuthToken returns the admin-managed auth token; "" when unset.
func (s *Server) resolveAuthToken() string {
	v, err := s.db.GetSetting(authTokenKey)
	if err != nil {
		return ""
	}
	return v
}

type authTokenReq struct {
	AuthToken string `json:"auth_token"`
}

func (s *Server) handleAdminGetAuthToken(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, 200, map[string]any{"auth_token": s.resolveAuthToken()})
}

func (s *Server) handleAdminSetAuthToken(w http.ResponseWriter, r *http.Request) {
	var b authTokenReq
	if err := json.NewDecoder(r.Body).Decode(&b); err != nil {
		writeJSON(w, 400, map[string]any{"error": "bad body"})
		return
	}
	if err := s.db.SetSetting(authTokenKey, b.AuthToken); err != nil {
		writeJSON(w, 500, map[string]any{"error": "save setting"})
		return
	}
	writeJSON(w, 200, map[string]any{"auth_token": b.AuthToken})
}
