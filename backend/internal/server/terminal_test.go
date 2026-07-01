package server

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/coder/websocket"
)

// TestTerminalWSSendsPing verifies the terminal WS emits a ping message within
// a short window after connect (the keepalive ticker). Uses a real ws.Dial
// against the chi router via httptest.Server.
func TestTerminalWSSendsPing(t *testing.T) {
	s := newTestServer(t)
	wsPingInterval = 50 * time.Millisecond
	t.Cleanup(func() {
		wsPingInterval = 30 * time.Second
	})

	cookie := loginAsAlice(t, s)
	srv := httptest.NewServer(s.Routes())
	defer srv.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	wsURL := "ws" + strings.TrimPrefix(srv.URL, "http") + "/ws/terminal"
	hdr := http.Header{}
	hdr.Add("Cookie", "session="+cookie)
	ws, _, err := websocket.Dial(ctx, wsURL, &websocket.DialOptions{HTTPHeader: hdr})
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	defer ws.Close(websocket.StatusNormalClosure, "")

	// Read the first {type:"session",id} message, then expect a ping.
	gotPing := false
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) && !gotPing {
		_, data, err := ws.Read(ctx)
		if err != nil {
			t.Fatalf("read: %v", err)
		}
		if strings.Contains(string(data), `"type":"ping"`) {
			gotPing = true
		}
	}
	if !gotPing {
		t.Fatal("did not receive a ping within the window")
	}
}

// TestTerminalWSPtyDataIsBinary asserts PTY output is forwarded to the client
// as a WebSocket BINARY frame, not a text frame. This is the regression guard
// for the "lines drift sideways" bug: PTY readLoop chunks can split a
// multi-byte UTF-8 sequence (CJK, box-drawing) across two WS messages, and a
// text frame would have the spec-mandated UTF-8 check replace the dangling
// half with U+FFFD — corrupting cell widths. Binary frames carry raw bytes and
// let xterm.js reassemble the sequence across the boundary.
func TestTerminalWSPtyDataIsBinary(t *testing.T) {
	s := newTestServer(t)

	cookie := loginAsAlice(t, s)
	srv := httptest.NewServer(s.Routes())
	defer srv.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	wsURL := "ws" + strings.TrimPrefix(srv.URL, "http") + "/ws/terminal"
	hdr := http.Header{}
	hdr.Add("Cookie", "session="+cookie)
	ws, _, err := websocket.Dial(ctx, wsURL, &websocket.DialOptions{HTTPHeader: hdr})
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	defer ws.Close(websocket.StatusNormalClosure, "")

	// Drain the {type:"session",id} greeting so the next Read targets PTY data.
	if _, _, err := ws.Read(ctx); err != nil {
		t.Fatalf("read session greeting: %v", err)
	}

	// Drive the fake PTY's data callback as if bash had emitted bytes. A
	// multi-byte sequence (the box-drawing U+2500 ─, 3 UTF-8 bytes) is used
	// so the test would also catch a regression that re-splits the sequence.
	fp := s.createdPTYs()[0]
	ptyOut := []byte{0xe2, 0x94, 0x80} // ─
	fp.mu.Lock()
	cbs := append([]func([]byte){}, fp.dataCbs...)
	fp.mu.Unlock()
	for _, cb := range cbs {
		cb(ptyOut)
	}

	mt, data, err := ws.Read(ctx)
	if err != nil {
		t.Fatalf("read pty data: %v", err)
	}
	if mt != websocket.MessageBinary {
		t.Fatalf("PTY data frame type = %v, want MessageBinary (text framing corrupts split UTF-8)", mt)
	}
	if string(data) != string(ptyOut) {
		t.Fatalf("PTY data = % x, want % x", data, ptyOut)
	}
}
