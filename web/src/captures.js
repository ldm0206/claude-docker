import { openWs, postJson } from "./api.js";

export function mountCaptures(root) {
  root.innerHTML = `
    <h2>Request capture</h2>
    <div id="cap-warn" class="warn" style="display:none">⚠ Capture active — full request/response bodies are being recorded with secrets redacted.</div>
    <div style="margin:8px 0">
      <button class="tiny" id="cap-on">Start</button>
      <button class="tiny" id="cap-off">Stop</button>
      <button class="tiny" id="cap-clear">Clear</button>
    </div>
    <div class="cap-list" id="cap-list"></div>
    <div class="cap-detail" id="cap-detail" style="display:none"></div>`;
  const list = document.getElementById("cap-list");
  const detail = document.getElementById("cap-detail");
  const warn = document.getElementById("cap-warn");

  const renderRow = (r) => {
    const row = document.createElement("div");
    row.className = "cap-row";
    const b = row.appendChild(document.createElement("b"));
    b.textContent = r.method;
    row.appendChild(document.createTextNode(` ${r.host}${r.path}`));
    const meta = row.appendChild(document.createElement("div"));
    meta.className = "meta";
    meta.textContent = `${r.status || "—"} · ${r.latencyMs}ms · ${new Date(r.ts).toLocaleTimeString()}`;
    row.onclick = () => {
      detail.style.display = "block";
      detail.textContent =
        `REQUEST ${r.method} ${r.host}${r.path}\n` +
        JSON.stringify(r.reqHeaders, null, 2) + "\n\n" + (r.reqBody || "") +
        `\n\n--- RESPONSE ${r.status || ""} ---\n` +
        JSON.stringify(r.resHeaders, null, 2) + "\n\n" + (r.resBody || "");
    };
    list.prepend(row);
  };

  openWs("/ws/captures", (data) => {
    if (Array.isArray(data)) data.forEach(renderRow); else renderRow(data);
  });

  document.getElementById("cap-on").onclick = async () => {
    const r = await postJson("/api/capture/enable");
    if (r.ok) warn.style.display = "block";
  };
  document.getElementById("cap-off").onclick = async () => {
    await postJson("/api/capture/disable");
    warn.style.display = "none";
  };
  document.getElementById("cap-clear").onclick = async () => {
    await postJson("/api/captures/clear");
    list.innerHTML = "";
  };
}
