package server

import (
	"encoding/json"
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
	if !s.authed(r) {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}
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

	// pty.Manager.OnData/OnExit do not return unsubscribe funcs (the manager
	// keeps callbacks for the PTY's lifetime). The closures capture the WS
	// connection `c`; once it closes, c.Write errors are ignored by the
	// callers (readLoop/waitExit still hold the reference). Connection-scoped
	// cleanup is handled by the read loop below exiting + defer c.Close.
	s.pty.OnData(func(b []byte) {
		_ = c.Write(ctx, websocket.MessageText, b)
	})
	s.pty.OnExit(func(code int) {
		msg, _ := json.Marshal(clientMsg{Type: "pty-exit", ExitCode: code})
		_ = c.Write(ctx, websocket.MessageText, msg)
	})

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
	return []string{stripPort(host)}
}

func stripPort(h string) string {
	for i := len(h) - 1; i >= 0; i-- {
		if h[i] == ':' {
			return h[:i]
		}
	}
	return h
}

// handleCapturesWS is an inert stub (Plan 5 implements real capture): accept,
// send an empty list, then keep the socket open reading/discarding input.
func (s *Server) handleCapturesWS(w http.ResponseWriter, r *http.Request) {
	if !s.authed(r) {
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
