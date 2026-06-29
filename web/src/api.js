// Thin API client. All endpoints are relative (same origin). 401 → treat as
// logged-out so the app routes back to the unlock screen.

export async function postJson(url, body) {
  const r = await fetch(url, {
    method: "POST",
    headers: { "content-type": "application/json" },
    body: JSON.stringify(body || {}),
  });
  return r;
}
export async function patchJson(url, body) {
  const r = await fetch(url, {
    method: "PATCH",
    headers: { "content-type": "application/json" },
    body: JSON.stringify(body || {}),
  });
  return r;
}
export async function del(url) {
  return fetch(url, { method: "DELETE" });
}
export async function getJson(url) {
  const r = await fetch(url);
  if (r.status === 401) throw new Error("AUTH");
  if (!r.ok) throw new Error(`${url} ${r.status}`);
  return r.json();
}
export function openWs(path, onMsg) {
  const proto = location.protocol === "https:" ? "wss" : "ws";
  const ws = new WebSocket(`${proto}://${location.host}${path}`);
  ws.onmessage = (e) => {
    const raw = e.data;
    try { onMsg(JSON.parse(raw), raw); }
    catch { onMsg(null, raw); }
  };
  return ws;
}

export async function uploadFile(url, file, onProgress) {
  return new Promise((resolve) => {
    const fd = new FormData();
    fd.append("file", file);
    const xhr = new XMLHttpRequest();
    xhr.open("POST", url);
    xhr.upload.onprogress = (e) => {
      if (e.lengthComputable && onProgress) onProgress(e.loaded / e.total);
    };
    xhr.onload = () => resolve({ ok: xhr.status >= 200 && xhr.status < 300, status: xhr.status });
    xhr.onerror = () => resolve({ ok: false, status: 0 });
    xhr.send(fd);
  });
}