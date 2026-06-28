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

  async function refreshSessions() {
    try { sessions = await (await fetch("/api/sessions")).json(); } catch { sessions = []; }
    const tabs = document.getElementById("sts");
    tabs.innerHTML = "";
    for (const s of sessions) {
      const t = document.createElement("div");
      t.className = "session-tab" + (s.id === currentSID ? " active" : "");
      t.textContent = (s.name || s.id.slice(0, 8)) + (s.alive ? "" : " (dead)");
      t.onclick = () => attach(s.id);
      tabs.appendChild(t);
    }
  }

  function attach(sid) {
    currentSID = sid || "";
    if (ws) ws.close();
    const proto = location.protocol === "https:" ? "wss" : "ws";
    ws = new WebSocket(`${proto}://${location.host}/ws/terminal` + (sid ? `?session=${encodeURIComponent(sid)}` : ""));
    document.getElementById("term-status").textContent = "connecting…";
    ws.onopen = () => { ws.send(JSON.stringify({ type: "resize", cols: term.cols, rows: term.rows })); };
    ws.onmessage = (e) => {
      const raw = e.data;
      try {
        const m = JSON.parse(raw);
        if (m.type === "session" && m.id) { currentSID = m.id; document.getElementById("term-status").textContent = "session " + m.id.slice(0, 8); refreshSessions(); return; }
        if (m.type === "pty-exit") { document.getElementById("term-status").textContent = "session ended (exit " + (m.exitCode ?? "?") + ")"; refreshSessions(); return; }
      } catch { /* terminal data */ }
      term.write(raw);
    };
    ws.onclose = () => { document.getElementById("term-status").textContent = "disconnected"; refreshSessions(); };
  }

  term.onData((d) => ws && ws.readyState === ws.OPEN && ws.send(JSON.stringify({ type: "input", data: d })));
  window.addEventListener("resize", () => fit.fit());
  term.onResize(({ cols, rows }) => ws && ws.readyState === ws.OPEN && ws.send(JSON.stringify({ type: "resize", cols, rows })));

  document.getElementById("new-sess").onclick = () => attach("");
  document.getElementById("kill-sess").onclick = async () => {
    if (!currentSID) return;
    await fetch(`/api/sessions/${encodeURIComponent(currentSID)}`, { method: "DELETE" });
    currentSID = null; term.reset(); attach(""); refreshSessions();
  };

  // Attach the most-recent session if any, else start a new one.
  (async () => {
    await refreshSessions();
    const alive = sessions.find((s) => s.alive);
    attach(alive ? alive.id : (sessions[0]?.id || ""));
  })();
}