package server

import (
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/ldm0206/claude-docker/backend/internal/config"
)

func newCfg() *config.Config {
	return &config.Config{AccessKey: "sekret", SessionSecret: "sssh", Port: 0}
}

func TestHealth(t *testing.T) {
	s := New(newCfg())
	req := httptest.NewRequest("GET", "/health", nil)
	w := httptest.NewRecorder()
	s.Routes().ServeHTTP(w, req)
	if w.Code != 200 {
		t.Fatalf("health status %d", w.Code)
	}
}

func TestAuthRejectsWrongKey(t *testing.T) {
	s := New(newCfg())
	req := httptest.NewRequest("POST", "/auth", strings.NewReader(`{"key":"wrong"}`))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	s.Routes().ServeHTTP(w, req)
	if w.Code != 401 {
		t.Fatalf("expected 401, got %d", w.Code)
	}
}

func TestStateRequiresAuth(t *testing.T) {
	s := New(newCfg())
	req := httptest.NewRequest("GET", "/api/state", nil)
	w := httptest.NewRecorder()
	s.Routes().ServeHTTP(w, req)
	if w.Code != 401 {
		t.Fatalf("expected 401, got %d", w.Code)
	}
}
