import { openWs, postJson, getJson } from "./api.js";

// mountCaptures(root): admin-only Captures panel. Streams redacted req/resp
// pairs from /ws/captures (initial list + push). Each session row has an
// enable/disable toggle (capture is off by default; the admin must turn it on
// per session for the MITM to route that session's traffic).
export function mountCaptures(root) {
  root.innerHTML = `
    <div class="card pads" style="margin-bottom:12px">
      <div class="row" style="margin-bottom:6px"><b>Sessions</b><span class="grow"></span><span class="muted tiny" id="cap-proxy-state"></span></div>
      <div id="cap-sessions" class="muted tiny">loading…</div>
    </div>
    <div class="row" style="margin-bottom:12px">
      <span class="muted tiny">Redacted request/response pairs from capture-enabled sessions.</span>
      <span class="grow"></span>
      <button class="btn tiny ghost" id="cap-clear">Clear</button>
    </div>
    <div class="cap-list" id="cap-list"></div>
    <div class="card pads" id="cap-detail" style="display:none;font-family:var(--mono);font-size:12px;white-space:pre-wrap;margin-top:12px"></div>`;

  const list = document.getElementById("cap-list");
  const detail = document.getElementById("cap-detail");
  const sessBox = document.getElementById("cap-sessions");
  const proxyState = document.getElementById("cap-proxy-state");

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

  async function refreshSessions() {
    let sessions = [];
    try { sessions = await getJson("/api/sessions"); } catch { sessions = []; }
    if (!sessions.length) { sessBox.textContent = "no sessions — open a terminal first"; return; }
    sessBox.innerHTML = "";
    for (const s of sessions) {
      const row = document.createElement("div");
      row.style.cssText = "display:flex;align-items:center;gap:8px;padding:4px 0;border-bottom:1px solid var(--border)";
      const sid = s.id.slice(0, 8);
      const alive = s.alive ? "●" : "○";
      row.innerHTML = `<span class="muted">${alive} ${sid}</span><span class="grow"></span>`;
      const btn = document.createElement("button");
      btn.className = "btn tiny " + (s.captureOn ? "danger" : "ghost");
      btn.textContent = s.captureOn ? "Disable" : "Enable";
      btn.onclick = async () => {
        const path = `/api/admin/sessions/${encodeURIComponent(s.id)}/capture/${s.captureOn ? "disable" : "enable"}`;
        const r = await postJson(path);
        if (!r.ok) {
          let msg = `failed (${r.status})`;
          try { msg = (await r.json()).error || msg; } catch {}
          alert(msg);
        }
        refreshSessions();
      };
      row.appendChild(btn);
      sessBox.appendChild(row);
    }
    // Proxy state line: shows whether the MITM is up for any session.
    const anyOn = sessions.some(s => s.captureOn);
    proxyState.textContent = anyOn ? "proxy: up" : "proxy: down";
  }
  refreshSessions();

  openWs("/ws/captures", (data) => {
    if (Array.isArray(data)) data.forEach(renderRow); else if (data && data.method) renderRow(data);
  });

  document.getElementById("cap-clear").onclick = async () => {
    await postJson("/api/admin/captures/clear");
    list.innerHTML = "";
    detail.style.display = "none";
  };
}