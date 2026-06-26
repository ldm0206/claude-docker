export function renderUnlock(app, onOk) {
  app.innerHTML = `<div class="unlock panel">
    <h1>Welcome</h1>
    <p style="color:var(--muted);margin-top:8px">Enter your access key.</p>
    <input id="key" type="password" placeholder="Access key" autofocus />
    <button id="go">Unlock</button>
    <p id="err" style="color:var(--accent);margin-top:8px"></p>
  </div>`;
  const go = async () => {
    const r = await fetch("/auth", {
      method: "POST",
      headers: { "content-type": "application/json" },
      body: JSON.stringify({ key: document.getElementById("key").value }),
    });
    if (r.ok) onOk();
    else document.getElementById("err").textContent = "Invalid key.";
  };
  document.getElementById("go").onclick = go;
  document.getElementById("key").addEventListener("keydown", (e) => { if (e.key === "Enter") go(); });
}
