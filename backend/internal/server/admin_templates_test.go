package server

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// ---------------------------------------------------------------------------
// role-template admin CRUD tests (T7)
// ---------------------------------------------------------------------------

// postTemplate sends POST /api/admin/templates with the given JSON body and
// returns the recorder.
func postTemplate(t *testing.T, s *Server, cookie, body string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest("POST", "/api/admin/templates", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.AddCookie(&http.Cookie{Name: "session", Value: cookie})
	w := httptest.NewRecorder()
	s.Routes().ServeHTTP(w, req)
	return w
}

func TestAdminTemplates_NonAdmin_Forbidden(t *testing.T) {
	s, _, _ := newTestServerWithAdmin(t)
	w := postTemplate(t, s, userCookie(t, s), `{"name":"x"}`)
	if w.Code != 403 {
		t.Fatalf("expected 403, got %d", w.Code)
	}
}

func TestAdminTemplates_CreateListUpdateDelete(t *testing.T) {
	s, _, _ := newTestServerWithAdmin(t)
	cookie := adminCookie(t, s)

	// --- Create ---
	body := `{"name":"basic","disk_quota_bytes":10737418240,"cpu_quota":"1.0","memory_max_bytes":536870912,"max_sessions":5,"permissions":"{}"}`
	w := postTemplate(t, s, cookie, body)
	if w.Code != 201 {
		t.Fatalf("create: expected 201, got %d; body=%s", w.Code, w.Body.String())
	}
	var created map[string]any
	if err := json.NewDecoder(w.Body).Decode(&created); err != nil {
		t.Fatalf("decode create: %v", err)
	}
	id := int(created["id"].(float64))
	if id == 0 {
		t.Fatal("expected non-zero id")
	}
	if created["name"] != "basic" {
		t.Fatalf("expected name basic, got %v", created["name"])
	}
	if created["max_sessions"].(float64) != 5 {
		t.Fatalf("expected max_sessions 5, got %v", created["max_sessions"])
	}

	// --- List ---
	req := httptest.NewRequest("GET", "/api/admin/templates", nil)
	req.AddCookie(&http.Cookie{Name: "session", Value: cookie})
	w = httptest.NewRecorder()
	s.Routes().ServeHTTP(w, req)
	if w.Code != 200 {
		t.Fatalf("list: expected 200, got %d", w.Code)
	}
	var list []map[string]any
	if err := json.NewDecoder(w.Body).Decode(&list); err != nil {
		t.Fatalf("decode list: %v", err)
	}
	if len(list) != 1 {
		t.Fatalf("expected 1 template, got %d", len(list))
	}
	if list[0]["name"] != "basic" {
		t.Fatalf("expected name basic, got %v", list[0]["name"])
	}

	// --- Update (PATCH) ---
	patchBody := `{"name":"basic-v2","max_sessions":10}`
	req = httptest.NewRequest("PATCH", "/api/admin/templates/"+itoa(id), strings.NewReader(patchBody))
	req.Header.Set("Content-Type", "application/json")
	req.AddCookie(&http.Cookie{Name: "session", Value: cookie})
	w = httptest.NewRecorder()
	s.Routes().ServeHTTP(w, req)
	if w.Code != 200 {
		t.Fatalf("update: expected 200, got %d; body=%s", w.Code, w.Body.String())
	}
	var updated map[string]any
	if err := json.NewDecoder(w.Body).Decode(&updated); err != nil {
		t.Fatalf("decode update: %v", err)
	}
	if updated["name"] != "basic-v2" {
		t.Fatalf("expected name basic-v2, got %v", updated["name"])
	}
	if updated["max_sessions"].(float64) != 10 {
		t.Fatalf("expected max_sessions 10, got %v", updated["max_sessions"])
	}

	// --- Delete ---
	req = httptest.NewRequest("DELETE", "/api/admin/templates/"+itoa(id), nil)
	req.AddCookie(&http.Cookie{Name: "session", Value: cookie})
	w = httptest.NewRecorder()
	s.Routes().ServeHTTP(w, req)
	if w.Code != 200 {
		t.Fatalf("delete: expected 200, got %d; body=%s", w.Code, w.Body.String())
	}
	// Verify gone via list
	req = httptest.NewRequest("GET", "/api/admin/templates", nil)
	req.AddCookie(&http.Cookie{Name: "session", Value: cookie})
	w = httptest.NewRecorder()
	s.Routes().ServeHTTP(w, req)
	var list2 []map[string]any
	_ = json.NewDecoder(w.Body).Decode(&list2)
	if len(list2) != 0 {
		t.Fatalf("expected 0 templates after delete, got %d", len(list2))
	}
}

func TestAdminTemplates_CreateValidation(t *testing.T) {
	s, _, _ := newTestServerWithAdmin(t)
	cookie := adminCookie(t, s)

	cases := []struct {
		name string
		body string
	}{
		{"missing name", `{"max_sessions":1}`},
		{"max_sessions zero", `{"name":"x","max_sessions":0}`},
		{"max_sessions negative", `{"name":"x","max_sessions":-1}`},
		{"negative disk quota", `{"name":"x","max_sessions":1,"disk_quota_bytes":-1}`},
		{"negative memory", `{"name":"x","max_sessions":1,"memory_max_bytes":-1}`},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			w := postTemplate(t, s, cookie, c.body)
			if w.Code != 400 {
				t.Fatalf("expected 400 for %s, got %d; body=%s", c.name, w.Code, w.Body.String())
			}
		})
	}
}

func TestAdminTemplates_DuplicateName(t *testing.T) {
	s, _, _ := newTestServerWithAdmin(t)
	cookie := adminCookie(t, s)
	body := `{"name":"dup","max_sessions":1}`
	if w := postTemplate(t, s, cookie, body); w.Code != 201 {
		t.Fatalf("first create: expected 201, got %d", w.Code)
	}
	w := postTemplate(t, s, cookie, body)
	if w.Code == 201 {
		t.Fatalf("duplicate name should not succeed; got 201; body=%s", w.Body.String())
	}
}

func TestAdminTemplates_UpdateNotFound(t *testing.T) {
	s, _, _ := newTestServerWithAdmin(t)
	cookie := adminCookie(t, s)
	req := httptest.NewRequest("PATCH", "/api/admin/templates/9999", strings.NewReader(`{"name":"x"}`))
	req.Header.Set("Content-Type", "application/json")
	req.AddCookie(&http.Cookie{Name: "session", Value: cookie})
	w := httptest.NewRecorder()
	s.Routes().ServeHTTP(w, req)
	if w.Code != 404 {
		t.Fatalf("expected 404, got %d", w.Code)
	}
}

func TestAdminTemplates_DeleteNotFound(t *testing.T) {
	s, _, _ := newTestServerWithAdmin(t)
	cookie := adminCookie(t, s)
	req := httptest.NewRequest("DELETE", "/api/admin/templates/9999", nil)
	req.AddCookie(&http.Cookie{Name: "session", Value: cookie})
	w := httptest.NewRecorder()
	s.Routes().ServeHTTP(w, req)
	if w.Code != 404 {
		t.Fatalf("expected 404, got %d", w.Code)
	}
}

// itoa is a local strconv.Itoa alias to keep imports minimal in test files.
func itoa(i int) string {
	return strings.TrimSpace((func() string {
		// avoid pulling strconv into every test file; json round-trip is fine
		b, _ := json.Marshal(i)
		return string(b)
	})())
}
