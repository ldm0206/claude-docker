import { openWs } from "./api.js";

export function mountMetrics() {
  let prev = null;
  openWs("/ws/metrics", (m) => {
    const cpuEl = document.getElementById("cpu");
    const memEl = document.getElementById("mem");
    const netEl = document.getElementById("net");
    let cpu = 0;
    if (prev) cpu = ((m.cpu.usageUsec - prev.cpu.usageUsec) / 1e6) / ((m.ts - prev.ts) / 1000) * 100;
    prev = m;
    const memPct = m.mem.max === Infinity ? 0 : (m.mem.current / m.mem.max) * 100;
    if (cpuEl) cpuEl.textContent = cpu.toFixed(1) + "%";
    if (memEl) memEl.textContent = (m.mem.current / 1048576).toFixed(0) + " MB" + (m.mem.max !== Infinity ? ` / ${(m.mem.max / 1048576).toFixed(0)}` : "");
    if (netEl) netEl.textContent = `${(m.net.rxBytes / 1048576).toFixed(1)}↓ ${(m.net.txBytes / 1048576).toFixed(1)}↑ MB`;
  });
}
