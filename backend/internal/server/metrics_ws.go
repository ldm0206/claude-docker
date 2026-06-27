package server

import (
	"net/http"
	"time"

	"github.com/coder/websocket"
	"github.com/ldm0206/claude-docker/backend/internal/metrics"
)

func (s *Server) handleMetricsWS(w http.ResponseWriter, r *http.Request) {
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
	read := metrics.ReadFileFn()
	tk := time.NewTicker(1500 * time.Millisecond)
	defer tk.Stop()
	for {
		usageUsec := metrics.ReadCgroupCPU(read)
		memCur, memMax, maxSet := metrics.ReadCgroupMemory(read)
		rx, tx := metrics.ReadNetDev(read)
		mem := map[string]any{"current": memCur}
		if maxSet {
			mem["max"] = memMax
		} else {
			mem["max"] = nil // unset → null (matches Node's JSON.stringify(Infinity))
		}
		snap := map[string]any{
			"cpu":       map[string]any{"usageUsec": usageUsec},
			"mem":       mem,
			"net":       map[string]any{"rxBytes": rx, "txBytes": tx},
			"captureOn": false,
			"alive":     s.pty.Alive(),
			"ts":        time.Now().UnixMilli(),
		}
		if err := c.Write(ctx, websocket.MessageText, mustJSON(snap)); err != nil {
			return
		}
		select {
		case <-ctx.Done():
			return
		case <-tk.C:
		}
	}
}
