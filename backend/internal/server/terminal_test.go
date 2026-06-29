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
