package server

import (
	"encoding/json"
	"net"
	"net/http"

	"github.com/coder/websocket"
)

type clientMsg struct {
	Type     string `json:"type"`
	Data     string `json:"data,omitempty"`
	Cols     uint16 `json:"cols,omitempty"`
	Rows     uint16 `json:"rows,omitempty"`
	ExitCode int    `json:"exitCode,omitempty"`
}

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
	// Fix 2: drive the shared PTY's identity from the live WS user. The NEXT
	// lazy Start() spawns `gosu <u.Username> bash -l` with BuildUserEnv.
	// If a different user is already running the live PTY, this is a no-op
	// until a restart — acceptable for Plan 2's single shared PTY.
	s.setCurrentUser(u.Username, u.UID, "/data/"+u.Username+"/claude-config")
	c, err := websocket.Accept(w, r, &websocket.AcceptOptions{
		OriginPatterns: originPatterns(r),
	})
	if err != nil {
		return
	}
	defer c.Close(websocket.StatusNormalClosure, "")

	if !s.pty.Alive() {
		if err := s.pty.Start(); err != nil {
			return
		}
	}
	ctx := r.Context()

	unsubData := s.pty.OnData(func(b []byte) {
		_ = c.Write(ctx, websocket.MessageText, b)
	})
	defer unsubData()
	unsubExit := s.pty.OnExit(func(code int) {
		if s.restarting.Load() || s.pty.Alive() {
			return
		}
		msg, _ := json.Marshal(clientMsg{Type: "pty-exit", ExitCode: code})
		_ = c.Write(ctx, websocket.MessageText, msg)
	})
	defer unsubExit()

	for {
		_, data, err := c.Read(ctx)
		if err != nil {
			return
		}
		var msg clientMsg
		if err := json.Unmarshal(data, &msg); err != nil {
			continue
		}
		switch msg.Type {
		case "input":
			_ = s.pty.Write([]byte(msg.Data))
		case "resize":
			_ = s.pty.Resize(msg.Cols, msg.Rows)
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

// handleCapturesWS is an inert stub (Plan 5 implements real capture): accept,
// send an empty list, then keep the socket open reading/discarding input.
// Auth is enforced via authWSUser (Fix 3) — same gate as the terminal WS.
func (s *Server) handleCapturesWS(w http.ResponseWriter, r *http.Request) {
	if _, ok := s.authWSUser(r); !ok {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}
	c, err := websocket.Accept(w, r, &websocket.AcceptOptions{OriginPatterns: originPatterns(r)})
	if err != nil {
		return
	}
	defer c.Close(websocket.StatusNormalClosure, "")
	ctx := r.Context()
	_ = c.Write(ctx, websocket.MessageText, []byte("[]"))
	for {
		if _, _, err := c.Read(ctx); err != nil {
			return
		}
	}
}
