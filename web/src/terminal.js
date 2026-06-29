import { Terminal } from "xterm";
import { FitAddon } from "@xterm/addon-fit";
import { WebLinksAddon } from "@xterm/addon-web-links";
import "xterm/css/xterm.css";

// mountTerminal(root): multi-session terminal. GET /api/sessions lists the
// caller's sessions; a WS to /ws/terminal?session=<id> attaches (or creates
// if id omitted). The server replies with {type:"session",id} on connect.
export function mountTerminal(root) {
  root.innerHTML = `
    <div class="session-tabs" id="sts"></div>
    <div class="term-wrap"><div class="term-body" id="termroot"></div></div>
    <div class="row" style="margin-top:8px">
      <button class="btn ghost tiny" id="new-sess">+ New session</button>
      <button class="btn danger tiny" id="kill-sess">Kill current</button>
      <span class="muted tiny" id="term-status"></span>
    </div>`;

  const term = new Terminal({
    fontFamily: "JetBrains Mono, ui-monospace, monospace",
    theme: { background: "#1f1e1d", foreground: "#d6cab6", cursor: "#d97757" },
  });
  const fit = new FitAddon();
  term.loadAddon(fit);
  term.loadAddon(new WebLinksAddon());
  term.open(document.getElementById("termroot"));
  fit.fit();

  let sessions = [];
  let currentSID = null;
  let ws = null;
  let reconnectAttempts = 0;
  let reconnectTimer = null;
  let intentionalClose = false;

  const CLOSE_REASONS = {
    1000: "closed",
    1006: "aborted (network/proxy)",
    1008: "rejected (policy/auth)",
    1011: "server error",
  };

  function status(msg) {
    const el = document.getElementById("term-status");
    if (el) el.textContent = msg;
  }

  async function refreshSessions() {
    try { sessions = await (await fetch("/api/sessions")).json(); } catch { sessions = []; }
    const tabs = document.getElementById("sts");
    if (!tabs) return;
    tabs.innerHTML = "";
    for (const s of sessions) {
      const t = document.createElement("div");
      t.className = "session-tab" + (s.id === currentSID ? " active" : "");
      t.textContent = (s.name || s.id.slice(0, 8)) + (s.alive ? "" : " (dead)");
      t.onclick = () => attach(s.id);
      tabs.appendChild(t);
    }
  }

  function scheduleReconnect(sid) {
    if (intentionalClose) return;
    if (reconnectAttempts >= 6) {
      status("giving up — reload or sign in again");
      return;
    }
    const delay = Math.min(30000, 1000 * Math.pow(2, reconnectAttempts));
    reconnectAttempts++;
    status(`reconnecting in ${Math.round(delay / 1000)}s…`);
    reconnectTimer = setTimeout(() => attach(sid), delay);
  }

  function attach(sid) {
    if (reconnectTimer) { clearTimeout(reconnectTimer); reconnectTimer = null; }
    currentSID = sid || "";
    // Tracks whether the server confirmed the session this connect. If the WS
    // closes (HTTP-level 404 surfaces as onclose 1006) BEFORE confirmation while
    // we requested a specific sid, that sid is stale (e.g. the row was lost
    // server-side and the revive path also missed) — retry once with no sid so
    // a fresh session is created instead of looping on the dead id forever.
    let sessionConfirmed = false;
    if (ws) { ws.onclose = null; ws.close(); }
    const proto = location.protocol === "https:" ? "wss" : "ws";
    ws = new WebSocket(`${proto}://${location.host}/ws/terminal` + (sid ? `?session=${encodeURIComponent(sid)}` : ""));
    status("connecting…");
    ws.onopen = () => {
      reconnectAttempts = 0;
      ws.send(JSON.stringify({ type: "resize", cols: term.cols, rows: term.rows }));
    };
    ws.onmessage = (e) => {
      const raw = e.data;
      try {
        const m = JSON.parse(raw);
        if (m.type === "ping") return;
        if (m.type === "session" && m.id) { sessionConfirmed = true; currentSID = m.id; status("session " + m.id.slice(0, 8)); refreshSessions(); return; }
        if (m.type === "pty-exit") { status("session ended (exit " + (m.exitCode ?? "?") + ")"); refreshSessions(); return; }
        return;
      } catch { /* binary/terminal data */ }
      term.write(raw);
    };
    ws.onclose = (ev) => {
      const reason = CLOSE_REASONS[ev.code] || `closed (${ev.code})`;
      status("disconnected — " + reason);
      refreshSessions();
      if (ev.code === 1008) {
        // auth/policy rejection: do not loop; tell user to sign in.
        status("session expired — reload to sign in");
        return;
      }
      // We asked for a specific sid but the server never confirmed it (closed
      // before the first session message) → the id is unknown/stale. Drop it
      // and create a fresh session instead of reconnecting to the dead id.
      if (currentSID && !sessionConfirmed) {
        currentSID = "";
        attach("");
        return;
      }
      scheduleReconnect(currentSID);
    };
  }

  term.onData((d) => ws && ws.readyState === ws.OPEN && ws.send(JSON.stringify({ type: "input", data: d })));
  window.addEventListener("resize", () => fit.fit());
  term.onResize(({ cols, rows }) => ws && ws.readyState === ws.OPEN && ws.send(JSON.stringify({ type: "resize", cols, rows })));

  document.getElementById("new-sess").onclick = () => attach("");
  document.getElementById("kill-sess").onclick = async () => {
    if (!currentSID) return;
    intentionalClose = true;
    try {
      await fetch(`/api/sessions/${encodeURIComponent(currentSID)}`, { method: "DELETE" });
      currentSID = null; term.reset();
    } finally {
      intentionalClose = false;
    }
    attach(""); refreshSessions();
  };

  // Attach the most-recent session if any, else start a new one.
  (async () => {
    await refreshSessions();
    const alive = sessions.find((s) => s.alive);
    attach(alive ? alive.id : (sessions[0]?.id || ""));
  })();
}