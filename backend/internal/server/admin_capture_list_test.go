package server

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/ldm0206/claude-docker/backend/internal/capture"
)

// TestAdminListCaptures_EmptyWhenNoCaptureService — capture==nil (pre-T5
// wiring) returns an empty list, not an error.
func TestAdminListCaptures_EmptyWhenNoCaptureService(t *testing.T) {
	s, _, _ := newTestServerWithAdmin(t)
	req := httptest.NewRequest("GET", "/api/admin/captures", nil)
	req.AddCookie(&http.Cookie{Name: "session", Value: adminCookie(t, s)})
	w := httptest.NewRecorder()
	s.Routes().ServeHTTP(w, req)
	if w.Code != 200 {
		t.Fatalf("status %d", w.Code)
	}
	var out []captureOut
	if err := json.Unmarshal(w.Body.Bytes(), &out); err != nil {
		t.Fatalf("unmarshal: %v body=%s", err, w.Body.String())
	}
	if len(out) != 0 {
		t.Fatalf("expected empty, got %d", len(out))
	}
}

// TestAdminListCaptures_ReturnsRedactedRecords — with a real capture Store
// holding records, the list endpoint returns them in the captureOut shape.
func TestAdminListCaptures_ReturnsRedactedRecords(t *testing.T) {
	s, _, _ := newTestServerWithAdmin(t)
	// Inject a real capture service backed by an in-memory store + fake runner
	// so the Store() accessor is wired.
	st := capture.NewStore()
	st.Add(capture.Record{SessionID: "sess-A", Method: "POST", Host: "api.anthropic.com", Path: "/v1/messages", Status: 200, LatencyMs: 42, Ts: 1, ReqBody: "[REDACTED]"})
	// Build a minimal capture.Service whose Store() returns st. NewService
	// requires a runner; use a nil-safe fake via the existing fakeProxyRunner
	// pattern if available — but ListCaptures only reads Store(), so a stopped
	// runner is fine.
	s.capture = newCaptureServiceWithStore(t, st)

	req := httptest.NewRequest("GET", "/api/admin/captures", nil)
	req.AddCookie(&http.Cookie{Name: "session", Value: adminCookie(t, s)})
	w := httptest.NewRecorder()
	s.Routes().ServeHTTP(w, req)
	if w.Code != 200 {
		t.Fatalf("status %d", w.Code)
	}
	var out []captureOut
	if err := json.Unmarshal(w.Body.Bytes(), &out); err != nil {
		t.Fatalf("unmarshal: %v body=%s", err, w.Body.String())
	}
	if len(out) != 1 || out[0].Method != "POST" || out[0].Status != 200 || out[0].ReqBody != "[REDACTED]" {
		t.Fatalf("unexpected %+v", out)
	}

	// Filter by session.
	req2 := httptest.NewRequest("GET", "/api/admin/captures?session=sess-A", nil)
	req2.AddCookie(&http.Cookie{Name: "session", Value: adminCookie(t, s)})
	w2 := httptest.NewRecorder()
	s.Routes().ServeHTTP(w2, req2)
	var out2 []captureOut
	_ = json.Unmarshal(w2.Body.Bytes(), &out2)
	if len(out2) != 1 {
		t.Fatalf("filter sess-A: %d", len(out2))
	}
	req3 := httptest.NewRequest("GET", "/api/admin/captures?session=other", nil)
	req3.AddCookie(&http.Cookie{Name: "session", Value: adminCookie(t, s)})
	w3 := httptest.NewRecorder()
	s.Routes().ServeHTTP(w3, req3)
	var out3 []captureOut
	_ = json.Unmarshal(w3.Body.Bytes(), &out3)
	if len(out3) != 0 {
		t.Fatalf("filter other: %d", len(out3))
	}
}

// TestAdminClearCaptures — POST clears the store.
func TestAdminClearCaptures(t *testing.T) {
	s, _, _ := newTestServerWithAdmin(t)
	st := capture.NewStore()
	st.Add(capture.Record{SessionID: "x", Method: "GET"})
	s.capture = newCaptureServiceWithStore(t, st)

	req := httptest.NewRequest("POST", "/api/admin/captures/clear", nil)
	req.AddCookie(&http.Cookie{Name: "session", Value: adminCookie(t, s)})
	w := httptest.NewRecorder()
	s.Routes().ServeHTTP(w, req)
	if w.Code != 200 {
		t.Fatalf("status %d", w.Code)
	}
	if len(st.List("")) != 0 {
		t.Fatal("store not cleared")
	}
}

// TestAdminCaptures_NonAdminForbidden — a role:user cookie is 403 on both
// the list and clear endpoints (admin-only).
func TestAdminCaptures_NonAdminForbidden(t *testing.T) {
	s, _, _ := newTestServerWithAdmin(t)

	for _, tc := range []struct{ method, path string }{
		{"GET", "/api/admin/captures"},
		{"POST", "/api/admin/captures/clear"},
	} {
		req := httptest.NewRequest(tc.method, tc.path, nil)
		req.AddCookie(&http.Cookie{Name: "session", Value: userCookie(t, s)}) // alice: role=user
		w := httptest.NewRecorder()
		s.Routes().ServeHTTP(w, req)
		if w.Code != 403 {
			t.Fatalf("%s %s: expected 403 for non-admin, got %d", tc.method, tc.path, w.Code)
		}
	}
}

// TestCaptureFanout_InitialListThenPush — the fanout writes the current
// list, then pushes new records as they land. No real WS dial. Deterministic:
// the write callback signals when the initial list has been delivered, so the
// test adds a record only AFTER the subscription is armed.
func TestCaptureFanout_InitialListThenPush(t *testing.T) {
	s, _, _ := newTestServerWithAdmin(t)
	st := capture.NewStore()
	st.Add(capture.Record{SessionID: "a", Method: "GET", Host: "h"})
	s.capture = newCaptureServiceWithStore(t, st)

	gotInitial := make(chan struct{})
	var written [][]byte
	write := func(b []byte) bool {
		written = append(written, b)
		if len(written) == 1 {
			close(gotInitial)
		}
		return true // keep writing
	}
	done := make(chan struct{})
	go func() { s.captureFanout(write, done, "") }()

	// Wait for the initial list to be delivered (= the goroutine has subscribed).
	<-gotInitial
	// Add a record; the Subscribe callback should push it.
	pushed := make(chan struct{})
	st.Subscribe(func(r capture.Record) {
		// We can't observe the fanout's push directly via a second Subscribe
		// reliably; instead just signal after a record Add and let the writes
		// slice grow. The fanout's Subscribe fires alongside this one.
		close(pushed)
	})
	st.Add(capture.Record{SessionID: "b", Method: "POST"})
	<-pushed
	close(done)

	// written[0] = initial list (sess=a); a later write = the pushed record (sess=b).
	if len(written) < 1 {
		t.Fatal("no initial list written")
	}
	var first []captureOut
	if err := json.Unmarshal(written[0], &first); err != nil {
		t.Fatalf("unmarshal initial: %v", err)
	}
	if len(first) != 1 || first[0].SessionID != "a" {
		t.Fatalf("initial list wrong: %+v", first)
	}
	// Confirm the pushed record landed in `written`.
	foundPush := false
	for i := 1; i < len(written); i++ {
		var pushedRec captureOut
		if err := json.Unmarshal(written[i], &pushedRec); err == nil && pushedRec.SessionID == "b" && pushedRec.Method == "POST" {
			foundPush = true
			break
		}
	}
	if !foundPush {
		t.Fatalf("pushed record b not found in writes: %d writes", len(written))
	}
}

// newCaptureServiceWithStore builds a *capture.Service whose Store() returns
// the given store, for tests that only exercise the Store accessor (list/clear/
// fanout). The proxy runner is a no-fake; Enable is not exercised here.
func newCaptureServiceWithStore(t *testing.T, st *capture.Store) *capture.Service {
	t.Helper()
	// capture.NewService(runner, store, db, port) — runner can be a
	// minimal fake satisfying the ProxyRunner interface.
	return capture.NewService(fakeProxyRunner{}, st, nil, 8888)
}

type fakeProxyRunner struct{}

func (fakeProxyRunner) Start(string) error { return nil }
func (fakeProxyRunner) Stop() error        { return nil }
func (fakeProxyRunner) Running() bool      { return true }