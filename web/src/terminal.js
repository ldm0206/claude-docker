import { Terminal } from "xterm";
import { FitAddon } from "@xterm/addon-fit";
import { WebLinksAddon } from "@xterm/addon-web-links";
import "xterm/css/xterm.css";

export function mountTerminal() {
  const term = new Terminal({ fontFamily: "ui-monospace, monospace", theme: { background: "#1E1B16", foreground: "#F4F1EA", cursor: "#D97757" } });
  const fit = new FitAddon();
  term.loadAddon(fit);
  term.loadAddon(new WebLinksAddon());
  term.open(document.querySelector(".term-wrap"));
  fit.fit();

  const ws = new WebSocket(`${location.protocol === "https:" ? "wss" : "ws"}://${location.host}/ws/terminal`);
  ws.onmessage = (e) => term.write(e.data);
  term.onData((d) => ws.readyState === ws.OPEN && ws.send(JSON.stringify({ type: "input", data: d })));
  window.addEventListener("resize", () => fit.fit());
  term.onResize(({ cols, rows }) => ws.readyState === ws.OPEN && ws.send(JSON.stringify({ type: "resize", cols, rows })));
}
