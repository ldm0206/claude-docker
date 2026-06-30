import { openWs, postJson, getJson } from "./api.js";

// mountCaptures(root): admin-only Captures panel. Streams redacted req/resp
// pairs from /ws/captures (initial list + push). Clear button.
export function mountCaptures(root) {
  root.innerHTML = `
    <div class="row" style="margin-bottom:12px">
      <span class="muted tiny">Redacted request/response pairs from capture-enabled sessions.</span>
      <span class="grow"></span>
      <button class="btn tiny ghost" id="cap-clear">Clear</button>
    </div>
    <div class="cap-list" id="cap-list"></div>
    <div class="card pads" id="cap-detail" style="display:none;font-family:var(--mono);font-size:12px;white-space:pre-wrap;margin-top:12px"></div>`;

  const list = document.getElementById("cap-list");
  const detail = document.getElementById("cap-detail");

  const renderRow = (r) => {
    const row = document.createElement("div");
    const meta = document.createElement("span");
    meta.className = "muted"; meta.style.float = "right"; meta.style.fontSize = "11px";
    meta.textContent = `${r.status || "—"} · ${r.latencyMs}ms`;
    const b = document.createElement("b"); b.textContent = r.method || "?";
    row.appendChild(b);
    row.appendChild(document.createTextNode(` ${r.host || ""}${r.path || ""}`));
    row.appendChild(meta);
    row.onclick = () => {
      detail.style.display = "block";
      detail.textContent =
        `REQUEST ${r.method} ${r.host}${r.path}\n` +
        JSON.stringify(r.reqHeaders || {}, null, 2) + "\n\n" + (r.reqBody || "") +
        `\n\n--- RESPONSE ${r.status || ""} ---\n` +
        JSON.stringify(r.resHeaders || {}, null, 2) + "\n\n" + (r.resBody || "");
    };
    list.prepend(row);
  };

  openWs("/ws/captures", (data) => {
    if (Array.isArray(data)) data.forEach(renderRow); else if (data && data.method) renderRow(data);
  });

  document.getElementById("cap-clear").onclick = async () => {
    await postJson("/api/admin/captures/clear");
    list.innerHTML = "";
    detail.style.display = "none";
  };
}