import { Terminal } from "xterm";
import { FitAddon } from "@xterm/addon-fit";
import { WebLinksAddon } from "@xterm/addon-web-links";
import "xterm/css/xterm.css";
import { postJson } from "./api.js";

export function mountTerminal() {
  const term = new Terminal({
    fontFamily: "JetBrains Mono, ui-monospace, monospace",
    theme: {
      background: "#1E1B16",
      foreground: "#F4F1EA",
      cursor: "#D97757",
      cursorAccent: "#1E1B16",
      selection: "rgba(217,119,87,0.3)",
      black: "#1E1B16",
      red: "#C96442",
      green: "#7A9E7E",
      yellow: "#D4A856",
      blue: "#6B8FA3",
      magenta: "#A37E8C",
      cyan: "#7EA3A8",
      white: "#F4F1EA",
      brightBlack: "#6B6760",
      brightRed: "#D97757",
      brightGreen: "#9BBF9F",
      brightYellow: "#E4C07A",
      brightBlue: "#8BAFC3",
      brightMagenta: "#BD9EAC",
      brightCyan: "#9EC3C8",
      brightWhite: "#FFFFFF",
    },
  });
  const fit = new FitAddon();
  term.loadAddon(fit);
  term.loadAddon(new WebLinksAddon());
  const container = document.querySelector(".term-body");
  term.open(container);
  fit.fit();

  const ws = new WebSocket(`${location.protocol === "https:" ? "wss" : "ws"}://${location.host}/ws/terminal`);
  ws.onmessage = (e) => {
    const raw = e.data;
    // JSON messages are structured events; raw strings are terminal output
    try {
      const msg = JSON.parse(raw);
      if (msg.type === "pty-exit") showExitOverlay(msg.exitCode);
      return;
    } catch { /* not JSON — terminal data */ }
    term.write(raw);
  };
  term.onData((d) => ws.readyState === ws.OPEN && ws.send(JSON.stringify({ type: "input", data: d })));
  window.addEventListener("resize", () => fit.fit());
  term.onResize(({ cols, rows }) => ws.readyState === ws.OPEN && ws.send(JSON.stringify({ type: "resize", cols, rows })));

  function showExitOverlay(exitCode) {
    const overlay = document.createElement("div");
    overlay.className = "pty-overlay";
    overlay.innerHTML = `
      <h3>Session ended</h3>
      <div class="exit-code">Exit code: ${exitCode}</div>
      <button id="pty-restart">Restart</button>`;
    container.appendChild(overlay);
    overlay.querySelector("#pty-restart").onclick = async () => {
      overlay.remove();
      await postJson("/api/session/restart");
    };
  }
}
