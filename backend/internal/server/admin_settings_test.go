package server

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/ldm0206/claude-docker/backend/internal/store"
)

func getAuthToken(t *testing.T, s *Server, cookie string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest("GET", "/api/admin/settings/auth-token", nil)
	req.AddCookie(&http.Cookie{Name: "session", Value: cookie})
	w := httptest.NewRecorder()
	s.Routes().ServeHTTP(w, req)
	return w
}

func putAuthToken(t *testing.T, s *Server, cookie, body string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest("PUT", "/api/admin/settings/auth-token", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.AddCookie(&http.Cookie{Name: "session", Value: cookie})
	w := httptest.NewRecorder()
	s.Routes().ServeHTTP(w, req)
	return w
}

func TestAuthToken_NonAdmin_Forbidden(t *testing.T) {
	s, _, _ := newTestServerWithAdmin(t)
	if w := getAuthToken(t, s, userCookie(t, s)); w.Code != 403 {
		t.Fatalf("GET expected 403, got %d", w.Code)
	}
	if w := putAuthToken(t, s, userCookie(t, s), `{"auth_token":"x"}`); w.Code != 403 {
		t.Fatalf("PUT expected 403, got %d", w.Code)
	}
}

func TestAuthToken_Get_EmptyByDefault(t *testing.T) {
	s, _, _ := newTestServerWithAdmin(t)
	w := getAuthToken(t, s, adminCookie(t, s))
	if w.Code != 200 {
		t.Fatalf("expected 200, got %d; body=%s", w.Code, w.Body.String())
	}
	var got map[string]any
	if err := json.NewDecoder(w.Body).Decode(&got); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if got["auth_token"] != "" {
		t.Fatalf("expected empty auth_token, got %v", got["auth_token"])
	}
}

func TestAuthToken_Put_RoundTrips(t *testing.T) {
	s, _, _ := newTestServerWithAdmin(t)
	cookie := adminCookie(t, s)

	if w := putAuthToken(t, s, cookie, `{"auth_token":"tok-1"}`); w.Code != 200 {
		t.Fatalf("put: expected 200, got %d; body=%s", w.Code, w.Body.String())
	}
	w := getAuthToken(t, s, cookie)
	if w.Code != 200 {
		t.Fatalf("get: expected 200, got %d", w.Code)
	}
	var got map[string]any
	_ = json.NewDecoder(w.Body).Decode(&got)
	if got["auth_token"] != "tok-1" {
		t.Fatalf("expected tok-1, got %v", got["auth_token"])
	}
}

func TestAuthToken_Put_EmptyClears(t *testing.T) {
	s, _, _ := newTestServerWithAdmin(t)
	cookie := adminCookie(t, s)

	if w := putAuthToken(t, s, cookie, `{"auth_token":"tok-1"}`); w.Code != 200 {
		t.Fatalf("seed: %d %s", w.Code, w.Body.String())
	}
	if w := putAuthToken(t, s, cookie, `{"auth_token":""}`); w.Code != 200 {
		t.Fatalf("clear: expected 200, got %d; body=%s", w.Code, w.Body.String())
	}
	w := getAuthToken(t, s, cookie)
	var got map[string]any
	_ = json.NewDecoder(w.Body).Decode(&got)
	if got["auth_token"] != "" {
		t.Fatalf("expected cleared, got %v", got["auth_token"])
	}
}

func TestAuthToken_Put_BadBody(t *testing.T) {
	s, _, _ := newTestServerWithAdmin(t)
	if w := putAuthToken(t, s, adminCookie(t, s), `not json`); w.Code != 400 {
		t.Fatalf("expected 400, got %d", w.Code)
	}
}

// TestBuildUserEnvFactory_SkipsAdmin verifies the shared auth token is NOT
// injected into an admin user's terminal even when the DB setting is set.
func TestBuildUserEnvFactory_SkipsAdmin(t *testing.T) {
	s, _, _ := newTestServerWithAdmin(t)
	if err := s.db.SetSetting("anthropic_auth_token", "shared-tok"); err != nil {
		t.Fatalf("seed setting: %v", err)
	}

	admin := store.User{Username: "rootadmin", UID: 5000, Role: "admin"}
	factory := s.buildUserEnvFactory(admin)
	env := factory("", "")
	for _, e := range env {
		if strings.HasPrefix(e, "ANTHROPIC_AUTH_TOKEN=") {
			t.Fatalf("admin terminal must not receive shared token, got %q", e)
		}
	}

	regular := store.User{Username: "alice", UID: 5001, Role: "user"}
	factory = s.buildUserEnvFactory(regular)
	env = factory("", "")
	found := false
	for _, e := range env {
		if e == "ANTHROPIC_AUTH_TOKEN=shared-tok" {
			found = true
		}
	}
	if !found {
		t.Fatalf("non-admin terminal must receive shared token; env:\n%s", strings.Join(env, "\n"))
	}
}
