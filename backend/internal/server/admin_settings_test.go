package server

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func getAnthropic(t *testing.T, s *Server, cookie string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest("GET", "/api/admin/settings/anthropic", nil)
	req.AddCookie(&http.Cookie{Name: "session", Value: cookie})
	w := httptest.NewRecorder()
	s.Routes().ServeHTTP(w, req)
	return w
}

func putAnthropic(t *testing.T, s *Server, cookie, body string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest("PUT", "/api/admin/settings/anthropic", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.AddCookie(&http.Cookie{Name: "session", Value: cookie})
	w := httptest.NewRecorder()
	s.Routes().ServeHTTP(w, req)
	return w
}

func TestAnthropic_NonAdmin_Forbidden(t *testing.T) {
	s, _, _ := newTestServerWithAdmin(t)
	if w := getAnthropic(t, s, userCookie(t, s)); w.Code != 403 {
		t.Fatalf("GET expected 403, got %d", w.Code)
	}
	if w := putAnthropic(t, s, userCookie(t, s), `{"api_key":"x"}`); w.Code != 403 {
		t.Fatalf("PUT expected 403, got %d", w.Code)
	}
}

func TestAnthropic_Get_EmptyByDefault(t *testing.T) {
	s, _, _ := newTestServerWithAdmin(t)
	w := getAnthropic(t, s, adminCookie(t, s))
	if w.Code != 200 {
		t.Fatalf("expected 200, got %d; body=%s", w.Code, w.Body.String())
	}
	var got map[string]any
	if err := json.NewDecoder(w.Body).Decode(&got); err != nil {
		t.Fatalf("decode: %v", err)
	}
	for _, k := range []string{"api_key", "base_url", "auth_token"} {
		if got[k] != "" {
			t.Fatalf("expected empty %q, got %v", k, got[k])
		}
	}
}

func TestAnthropic_Put_RoundTripsAllThree(t *testing.T) {
	s, _, _ := newTestServerWithAdmin(t)
	cookie := adminCookie(t, s)

	body := `{"api_key":"sk-1","base_url":"http://gw","auth_token":"tok-1"}`
	if w := putAnthropic(t, s, cookie, body); w.Code != 200 {
		t.Fatalf("put: expected 200, got %d; body=%s", w.Code, w.Body.String())
	}

	w := getAnthropic(t, s, cookie)
	if w.Code != 200 {
		t.Fatalf("get: expected 200, got %d", w.Code)
	}
	var got map[string]any
	_ = json.NewDecoder(w.Body).Decode(&got)
	if got["api_key"] != "sk-1" || got["base_url"] != "http://gw" || got["auth_token"] != "tok-1" {
		t.Fatalf("round-trip mismatch: %v", got)
	}
}

func TestAnthropic_Put_EmptyClearsOnlyThatKey(t *testing.T) {
	s, _, _ := newTestServerWithAdmin(t)
	cookie := adminCookie(t, s)

	if w := putAnthropic(t, s, cookie, `{"api_key":"sk-1","base_url":"http://gw","auth_token":"tok-1"}`); w.Code != 200 {
		t.Fatalf("seed: %d %s", w.Code, w.Body.String())
	}
	if w := putAnthropic(t, s, cookie, `{"api_key":"","base_url":"http://gw","auth_token":"tok-1"}`); w.Code != 200 {
		t.Fatalf("clear api_key: %d %s", w.Code, w.Body.String())
	}

	w := getAnthropic(t, s, cookie)
	var got map[string]any
	_ = json.NewDecoder(w.Body).Decode(&got)
	if got["api_key"] != "" {
		t.Fatalf("api_key should be cleared, got %v", got["api_key"])
	}
	if got["base_url"] != "http://gw" || got["auth_token"] != "tok-1" {
		t.Fatalf("other keys should be preserved: %v", got)
	}
}

func TestAnthropic_Put_BadBody(t *testing.T) {
	s, _, _ := newTestServerWithAdmin(t)
	if w := putAnthropic(t, s, adminCookie(t, s), `not json`); w.Code != 400 {
		t.Fatalf("expected 400, got %d", w.Code)
	}
}
