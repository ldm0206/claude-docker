package server

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func getTemplateUser(t *testing.T, s *Server, cookie string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest("GET", "/api/admin/settings/template-user", nil)
	req.AddCookie(&http.Cookie{Name: "session", Value: cookie})
	w := httptest.NewRecorder()
	s.Routes().ServeHTTP(w, req)
	return w
}

func putTemplateUser(t *testing.T, s *Server, cookie, body string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest("PUT", "/api/admin/settings/template-user", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.AddCookie(&http.Cookie{Name: "session", Value: cookie})
	w := httptest.NewRecorder()
	s.Routes().ServeHTTP(w, req)
	return w
}

func TestTemplateUser_NonAdmin_Forbidden(t *testing.T) {
	s, _, _ := newTestServerWithAdmin(t)
	w := getTemplateUser(t, s, userCookie(t, s))
	if w.Code != 403 {
		t.Fatalf("expected 403, got %d", w.Code)
	}
}

func TestTemplateUser_Get_Unset(t *testing.T) {
	s, _, _ := newTestServerWithAdmin(t)
	w := getTemplateUser(t, s, adminCookie(t, s))
	if w.Code != 200 {
		t.Fatalf("expected 200, got %d; body=%s", w.Code, w.Body.String())
	}
	var got map[string]any
	if err := json.NewDecoder(w.Body).Decode(&got); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if got["template_user"] != "" {
		t.Fatalf("expected empty template_user, got %v", got["template_user"])
	}
}

func TestTemplateUser_Put_ValidAdmin_RoundTrips(t *testing.T) {
	s, _, _ := newTestServerWithAdmin(t)
	cookie := adminCookie(t, s)

	// newTestServerWithAdmin seeds a fixed admin user "bob" (see admin_users_test.go:86-91).
	w := putTemplateUser(t, s, cookie, `{"template_user":"bob"}`)
	if w.Code != 200 {
		t.Fatalf("put: expected 200, got %d; body=%s", w.Code, w.Body.String())
	}

	w = getTemplateUser(t, s, cookie)
	if w.Code != 200 {
		t.Fatalf("get: expected 200, got %d", w.Code)
	}
	var got map[string]any
	_ = json.NewDecoder(w.Body).Decode(&got)
	if got["template_user"] != "bob" {
		t.Fatalf("expected %q, got %v", "bob", got["template_user"])
	}
}

func TestTemplateUser_Put_NonAdmin_Rejected(t *testing.T) {
	s, _, _ := newTestServerWithAdmin(t)
	cookie := adminCookie(t, s)

	// "alice" is the seeded regular (role=user) user.
	w := putTemplateUser(t, s, cookie, `{"template_user":"alice"}`)
	if w.Code != 400 {
		t.Fatalf("expected 400 for non-admin, got %d; body=%s", w.Code, w.Body.String())
	}
}

func TestTemplateUser_Put_Unknown_Rejected(t *testing.T) {
	s, _, _ := newTestServerWithAdmin(t)
	cookie := adminCookie(t, s)
	w := putTemplateUser(t, s, cookie, `{"template_user":"ghost"}`)
	if w.Code != 400 {
		t.Fatalf("expected 400 for unknown user, got %d; body=%s", w.Code, w.Body.String())
	}
}

func TestTemplateUser_Put_Empty_Clears(t *testing.T) {
	s, _, _ := newTestServerWithAdmin(t)
	cookie := adminCookie(t, s)

	if w := putTemplateUser(t, s, cookie, `{"template_user":"bob"}`); w.Code != 200 {
		t.Fatalf("seed put: %d %s", w.Code, w.Body.String())
	}
	w := putTemplateUser(t, s, cookie, `{"template_user":""}`)
	if w.Code != 200 {
		t.Fatalf("clear: expected 200, got %d; body=%s", w.Code, w.Body.String())
	}
	got := getTemplateUser(t, s, cookie)
	var m map[string]any
	_ = json.NewDecoder(got.Body).Decode(&m)
	if m["template_user"] != "" {
		t.Fatalf("expected cleared, got %v", m["template_user"])
	}
}
