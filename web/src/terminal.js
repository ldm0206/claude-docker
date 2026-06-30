import { Terminal } from "xterm";
import { FitAddon } from "@xterm/addon-fit";
import { WebLinksAddon } from "@xterm/addon-web-links";
import "xterm/css/xterm.css";

// mountTerminal(root): single-session terminal. On open it attaches the user's
// one alive session (listed via /api/sessions); if none, the WS create path
// spins up a fresh one. The server replies with {type:"session",id} on connect.
// There is no session-switching UI — the per-user cap is 1, so exactly one
// session exists at a time.
export function mountTerminal(root) {
  root.innerHTML = `
    <div class="term-wrap"><div class="term-body" id="termroot"></div></div>
    <div class="row" style="margin-top:8px">
      <button class="btn danger tiny" id="kill-sess">Restart terminal</button>
      <span class="muted tiny" id="term-status"></span>
    </div>`;

  // No `theme` option here: an explicit theme would override the 16-color
  // palette and the programs' own ANSI colors (ls, claude, tmux). Leaving it
  // unset lets xterm use its default palette so program colors come through.
  const term = new Terminal({
    fontFamily: "JetBrains Mono, ui-monospace, monospace",
    allowProposedApi: true,
  });
  const fit = new FitAddon();
  term.loadAddon(fit);
  term.loadAddon(new WebLinksAddon());
  term.open(document.getElementById("termroot"));
  fit.fit();

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
    // we requested a specific sid, that sid is stale — retry once with no sid so
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
        if (m.type === "session" && m.id) { sessionConfirmed = true; currentSID = m.id; status("session " + m.id.slice(0, 8)); return; }
        if (m.type === "pty-exit") { status("session ended (exit " + (m.exitCode ?? "?") + ") — restarting"); scheduleReconnect(""); return; }
        return;
      } catch { /* binary/terminal data */ }
      term.write(raw);
    };
    ws.onclose = (ev) => {
      const reason = CLOSE_REASONS[ev.code] || `closed (${ev.code})`;
      status("disconnected — " + reason);
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

  // --- Clipboard bridge ---
  // xterm.js 5.x does NOT handle OSC 52 internally; we register it via the
  // parser. Programs (claude code's "(c to copy)", tmux copy-mode, `echo x |
  // pbcopy` via the entrypoint shim) emit ESC ] 52 ; c ; <base64> BEL to WRITE
  // the clipboard. We decode and forward to the browser clipboard. Reads
  // (payload "?") are gated behind a user gesture by browsers, so we only wire
  // the write direction here — pbpaste stays best-effort server-side.
  function copyToClipboard(text) {
    if (navigator.clipboard && navigator.clipboard.writeText) {
      navigator.clipboard.writeText(text).catch(() => fallbackCopy(text));
    } else { fallbackCopy(text); }
  }
  function fallbackCopy(text) {
    const ta = document.createElement("textarea");
    ta.value = text; ta.style.position = "fixed"; ta.style.left = "-9999px";
    document.body.appendChild(ta); ta.select();
    try { document.execCommand("copy"); } catch {}
    ta.remove();
  }
  term.parser.registerOscHandler(52, (data) => {
    const semi = data.indexOf(";");
    if (semi < 0) return true;
    const payload = data.slice(semi + 1);
    if (!payload || payload === "?") return true; // read request: not wired
    try {
      const bytes = Uint8Array.from(atob(payload), (c) => c.charCodeAt(0));
      copyToClipboard(new TextDecoder("utf-8").decode(bytes));
    } catch {}
    return true;
  });

  // Right-click: copy the current selection (if any), otherwise paste.
  async function pasteClipboard() {
    try {
      const text = await navigator.clipboard.readText();
      if (text && ws && ws.readyState === WebSocket.OPEN) ws.send(JSON.stringify({ type: "input", data: text }));
    } catch { /* read blocked in non-secure context */ }
  }
  if (term.element) {
    term.element.addEventListener("contextmenu", (e) => {
      e.preventDefault();
      if (term.hasSelection()) { copyToClipboard(term.getSelection()); term.clearSelection(); }
      else { pasteClipboard(); }
    });
  }
  term.attachCustomKeyEventHandler((ev) => {
    if (ev.type !== "keydown") return true;
    // Ctrl+Shift+C = copy selection; Ctrl+Shift+V / Shift+Insert = paste.
    if (ev.ctrlKey && ev.shiftKey && (ev.key === "C" || ev.code === "KeyC") && term.hasSelection()) {
      ev.preventDefault(); copyToClipboard(term.getSelection()); return false;
    }
    const isPaste = (ev.ctrlKey && ev.shiftKey && (ev.key === "V" || ev.code === "KeyV")) || (ev.shiftKey && ev.key === "Insert");
    if (isPaste) { ev.preventDefault(); pasteClipboard(); return false; }
    return true;
  });

  window.addEventListener("resize", () => fit.fit());
  term.onResize(({ cols, rows }) => ws && ws.readyState === ws.OPEN && ws.send(JSON.stringify({ type: "resize", cols, rows })));

  document.getElementById("kill-sess").onclick = async () => {
    if (!currentSID) { attach(""); return; }
    intentionalClose = true;
    try {
      await fetch(`/api/sessions/${encodeURIComponent(currentSID)}`, { method: "DELETE" });
      currentSID = null; term.reset();
    } finally {
      intentionalClose = false;
    }
    attach("");
  };

  // Attach the single alive session if any, else start a new one.
  (async () => {
    let aliveID = "";
    try {
      const sessions = await (await fetch("/api/sessions")).json();
      const alive = (sessions || []).find((s) => s.alive);
      if (alive) aliveID = alive.id;
    } catch { /* no sessions / network — create path handles it */ }
    attach(aliveID);
  })();
}
