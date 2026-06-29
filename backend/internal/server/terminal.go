package server

import (
	"encoding/json"
	"net"
	"net/http"
	"time"

	"github.com/coder/websocket"
	"github.com/ldm0206/claude-docker/backend/internal/sessions"
)

var wsPingInterval = 30 * time.Second

type clientMsg struct {
	Type     string `json:"type"`
	Data     string `json:"data,omitempty"`
	Cols     uint16 `json:"cols,omitempty"`
	Rows     uint16 `json:"rows,omitempty"`
	ExitCode int    `json:"exitCode,omitempty"`
}

// handleTerminalWS drives a per-user, per-session PTY over a WebSocket.
//
// Flow:
//  1. authWSUser → live, non-suspended user (else 401, no upgrade).
//  2. Read ?session=<id>:
//     - present  → attach to that session (404 if unknown to this user).
//     - absent   → CREATE a new session (409 if the per-user cap is hit).
//  3. Lazy-start the PTY (ensureSession handles this) so the first emitted
//     bytes are not missed.
//  4. After the WS upgrade, send a single {type:"session",id:<sid>} message so
//     the client knows which session it is bound to (esp. on the create path).
//  5. Subscribe OnData → c.Write; OnExit → {type:"pty-exit",exitCode}. On WS
//     close, ONLY unsubscribe (the PTY survives = detach; T6 DELETE reaps it).
//
// The create/attach/start logic is delegated to Server.ensureSession so it can
// be unit-tested without a real WebSocket dial (see server_test.go).
func (s *Server) handleTerminalWS(w http.ResponseWriter, r *http.Request) {
	// WS routes are NOT under authMiddleware, so we can't rely on the cookie
	// signature check alone — authWSUser ALSO re-fetches the live user and
	// rejects suspended/deleted accounts (Fix 3). Without this, a user whose
	// account was suspended (or deleted) after the cookie was issued could
	// keep using the terminal on a stale-but-valid cookie.
	u, ok := s.authWSUser(r)
	if !ok {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}

	// Resolve the PTY BEFORE accepting the upgrade so we can return proper HTTP
	// status codes (404 unknown session, 409 cap reached). After the upgrade we
	// can only close the socket.
	sid := r.URL.Query().Get("session")
	p, effSID, status := s.ensureSession(u, sid)
	if status != http.StatusOK {
		http.Error(w, http.StatusText(status), status)
		return
	}

	c, err := websocket.Accept(w, r, &websocket.AcceptOptions{
		OriginPatterns: originPatterns(r),
	})
	if err != nil {
		return
	}
	defer c.Close(websocket.StatusNormalClosure, "")

	ctx := r.Context()

	// Tell the client which session id it's bound to. On the create path this
	// is a newly-minted id the client has never seen; on attach it echoes the
	// requested id. Either way the client needs it for /api/sessions/:id (T6).
	if err := c.Write(ctx, websocket.MessageText, mustJSON(map[string]any{
		"type": "session",
		"id":   effSID,
	})); err != nil {
		return
	}

	unsubData := p.OnData(func(b []byte) {
		_ = c.Write(ctx, websocket.MessageText, b)
	})
	unsubExit := p.OnExit(func(code int) {
		// Natural process exit: notify the client. We do NOT call
		// MarkSessionExited here — the row stays alive=1 until an explicit
		// kill (T6 DELETE) or a reconnect notices and reaps it. Acceptable
		// for Plan 3; documented in the task-5 report.
		_ = c.Write(ctx, websocket.MessageText, mustJSON(clientMsg{Type: "pty-exit", ExitCode: code}))
	})
	defer unsubData()
	defer unsubExit()
	// Keepalive: send a ping every 30s so Cloudflare (~100s idle) and nginx
	// (proxy_read_timeout 300s) do not reap an idle WS. The client ignores
	// {type:"ping"} messages (terminal.js treats parsed JSON with no terminal
	// data as a no-op). Stopped on ctx cancel / WS close.
	go func() {
		ticker := time.NewTicker(wsPingInterval)
		defer ticker.Stop()
		pingMsg := mustJSON(map[string]any{"type": "ping"})
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				if err := c.Write(ctx, websocket.MessageText, pingMsg); err != nil {
					return
				}
			}
		}
	}()

	for {
		_, data, err := c.Read(ctx)
		if err != nil {
			return // WS closed: detach. PTY keeps running; only unsubscribe (deferred above).
		}
		var msg clientMsg
		if err := json.Unmarshal(data, &msg); err != nil {
			continue
		}
		switch msg.Type {
		case "input":
			_ = p.Write([]byte(msg.Data))
		case "resize":
			_ = p.Resize(msg.Cols, msg.Rows)
		}
	}
}

// originPatterns returns ["host"] from the request Host (allow same-origin;
// same approach as the Node originHostMatches, scheme-agnostic).
func originPatterns(r *http.Request) []string {
	host := r.Host
	if host == "" {
		return nil
	}
	strippedHost, _, err := net.SplitHostPort(host)
	if err != nil {
		strippedHost = host
	}
	return []string{strippedHost}
}

// handleCapturesWS pushes captured (redacted) request/response records to the
// admin Captures panel over a WebSocket. Admin-only (authWSUser rejects
// suspended/deleted, 401). On connect it sends the current list (optionally
// filtered by ?session=<id>), then pushes each new record as it lands. The
// push wiring lives in Server.captureFanout (testable without a real WS);
// this handler just bridges it to the websocket connection.
func (s *Server) handleCapturesWS(w http.ResponseWriter, r *http.Request) {
	u, ok := s.authWSUser(r)
	if !ok || u.Role != "admin" {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}
	c, err := websocket.Accept(w, r, &websocket.AcceptOptions{OriginPatterns: originPatterns(r)})
	if err != nil {
		return
	}
	defer c.Close(websocket.StatusNormalClosure, "")
	ctx := r.Context()
	session := r.URL.Query().Get("session")
	write := func(b []byte) bool {
		return c.Write(ctx, websocket.MessageText, b) == nil
	}
	// done is closed when the client disconnects (Read returns).
	done := make(chan struct{})
	go func() { defer close(done); s.captureFanout(write, done, session) }()
	for {
		if _, _, err := c.Read(ctx); err != nil {
			return
		}
	}
}

// Compile-time guarantee that Server uses sessions.PTY (catches an interface
// drift between the manager and this handler early).
var _ = sessions.PTY(nil)
