import { getJson, postJson, patchJson, del } from "./api.js";
import { mountTerminal } from "./terminal.js";
import { mountCaptures } from "./captures.js";

const app = document.getElementById("app");

// --- Theme ---
function applyTheme(t) {
  if (t === "system") { document.documentElement.removeAttribute("data-theme"); }
  else { document.documentElement.setAttribute("data-theme", t); }
  localStorage.setItem("theme", t);
}
applyTheme(localStorage.getItem("theme") || "system");

// --- State ---
let self = null; // {username, role, mustChangePassword?}

async function boot() {
  try { self = await getJson("/api/me"); }
  catch { return renderLogin(); }
  if (self.mustChangePassword) return renderChangePassword(self.username);
  renderApp();
}

// ---------------------------------------------------------------------------
// Login
// ---------------------------------------------------------------------------
function el(tag, attrs = {}, ...kids) {
  const e = document.createElement(tag);
  for (const [k, v] of Object.entries(attrs)) {
    if (k === "class") e.className = v;
    else if (k === "onclick") e.onclick = v;
    else if (k === "html") e.innerHTML = v;
    else e.setAttribute(k, v);
  }
  for (const kid of kids) {
    if (kid == null) continue;
    e.appendChild(typeof kid === "string" ? document.createTextNode(kid) : kid);
  }
  return e;
}

function shell(cls, kids) { app.innerHTML = ""; app.appendChild(el("div", { class: cls }, ...kids)); }

function renderLogin() {
  const err = el("p", { class: "muted", id: "err" });
  const user = el("input", { class: "field", id: "u", placeholder: "Username", autofocus: "true" });
  const pass = el("input", { class: "field", id: "p", type: "password", placeholder: "Password" });
  const go = el("button", { class: "btn" }, "Sign in");
  go.onclick = async () => {
    const r = await postJson("/auth", { username: user.value, password: pass.value });
    if (r.ok) boot();
    else err.textContent = "Invalid username or password.";
  };
  pass.addEventListener("keydown", (e) => { if (e.key === "Enter") go.click(); });
  shell("login-card", [
    el("h1", { style: "color:var(--accent);margin:0 0 4px" }, "✦ Claude"),
    el("p", { class: "muted", style: "margin:0 0 18px" }, "Sign in"),
    el("label", { class: "lbl" }, "Username"), user, el("div", { style: "height:8px" }),
    el("label", { class: "lbl" }, "Password"), pass, el("div", { style: "height:14px" }),
    go, el("div", { style: "height:10px" }), err,
  ]);
  app.firstElementChild.style.maxWidth = "340px";
  app.firstElementChild.style.margin = "0 auto";
}

function renderChangePassword(username) {
  const err = el("p", { class: "muted", id: "err" });
  const np = el("input", { class: "field", id: "np", type: "password", placeholder: "New password", autofocus: "true" });
  const go = el("button", { class: "btn" }, "Set password");
  go.onclick = async () => {
    if (np.value.length < 6) { err.textContent = "At least 6 characters."; return; }
    const r = await postJson("/auth/change-password", { newPassword: np.value });
    if (r.ok) boot(); else err.textContent = "Failed.";
  };
  shell("login-card", [
    el("h1", { style: "color:var(--accent);margin:0 0 4px" }, `Welcome, ${username}`),
    el("p", { class: "muted", style: "margin:0 0 18px" }, "Set a new password to continue"),
    el("label", { class: "lbl" }, "New password"), np, el("div", { style: "height:14px" }),
    go, err,
  ]);
  app.firstElementChild.style.maxWidth = "340px";
  app.firstElementChild.style.margin = "0 auto";
}

// ---------------------------------------------------------------------------
// App shell + routing
// ---------------------------------------------------------------------------
let current = "terminal";
let sessions = [];

function renderApp() {
  app.innerHTML = `
    <aside class="sidebar" id="sb"></aside>
    <section class="main">
      <header class="topbar"><button class="menu-btn" id="menu">≡</button><h1 id="title">Terminal</h1><span class="grow"></span><span class="nav-who" id="who"></span></header>
      <div class="content" id="view"></div>
    </section>`;
  document.getElementById("menu").onclick = () => document.getElementById("sb").classList.toggle("open");
  renderSidebar();
  document.getElementById("who").innerHTML = `● <b>${self.username}</b> <span class="${self.role === 'admin' ? 'pill admin' : 'faint'}">${self.role}</span>`;
  nav(current);
}

function renderSidebar() {
  const sb = document.getElementById("sb");
  const items = [
    ["terminal", "▸ Terminal"],
    ["files", " Files"],
    ["traffic", " Traffic"],
  ];
  const admin = [
    ["users", " Users"],
    ["credentials", " Credentials"],
    ["templates", " Templates"],
    ["captures", " Captures"],
    ["audit", " Audit"],
  ];
  sb.innerHTML = "";
  sb.appendChild(el("div", { class: "brand" }, "✦ Claude"));
  for (const [k, label] of items) sb.appendChild(navBtn(k, label));
  if (self.role === "admin") {
    sb.appendChild(el("div", { class: "nav-group-label" }, "Admin"));
    for (const [k, label] of admin) sb.appendChild(navBtn(k, label));
  }
  sb.appendChild(el("div", { class: "nav-spacer" }));
  sb.appendChild(themeToggle());
  sb.appendChild(el("button", { class: "nav-item", onclick: () => { postJson("/auth/logout"); location.reload(); } }, "⎋ Sign out"));
}

function navBtn(k, label) {
  return el("button", { class: "nav-item" + (current === k ? " active" : ""), onclick: () => nav(k) }, label);
}

function themeToggle() {
  const cur = localStorage.getItem("theme") || "system";
  const wrap = el("div", { class: "theme-toggle" });
  for (const t of ["light", "dark", "system"]) {
    const b = el("button", { class: t === cur ? "active" : "", onclick: () => { applyTheme(t); renderSidebar(); } }, t[0].toUpperCase() + t.slice(1));
    wrap.appendChild(b);
  }
  return wrap;
}

const TITLES = { terminal: "Terminal", files: "Files", traffic: "Traffic", users: "Users", credentials: "Credentials", templates: "Templates", captures: "Captures", audit: "Audit" };
function nav(view) {
  current = view;
  document.getElementById("title").textContent = TITLES[view] || view;
  renderSidebar();
  const v = document.getElementById("view");
  v.innerHTML = "";
  (VIEWS[view] || (() => {}))(v);
  if (window.innerWidth <= 760) document.getElementById("sb").classList.remove("open");
}

const VIEWS = {};
VIEWS.terminal = viewTerminal;
VIEWS.files = viewFiles;
VIEWS.traffic = viewTraffic;
VIEWS.users = viewAdminUsers;
VIEWS.credentials = viewCredentials;
VIEWS.templates = viewTemplates;
VIEWS.captures = (elRoot) => mountCaptures(elRoot);
VIEWS.audit = viewAudit;

// ---------------------------------------------------------------------------
// View: Terminal
// ---------------------------------------------------------------------------
function viewTerminal(root) { mountTerminal(root); }

// ---------------------------------------------------------------------------
// View: Files (SFTP connection info + workspace note)
// ---------------------------------------------------------------------------
function viewFiles(root) {
  // Lazy-import the module to keep the initial bundle small.
  import("./files.js").then(({ mountFiles }) => mountFiles(root));
}

// ---------------------------------------------------------------------------
// View: Traffic (per-user usage + monthly up/down)
// ---------------------------------------------------------------------------
function viewTraffic(root) {
  root.innerHTML = `<div class="meters" id="meters"><span class="muted">Loading…</span></div>
    <div class="card pads" style="margin-top:14px"><h3 style="margin:0 0 8px;font-size:14px">Monthly traffic</h3><div id="trows" class="muted">—</div></div>`;
  // usage is admin-only endpoint; for regular users show their own traffic via /api/sessions? no — there's no user-self traffic endpoint yet.
  // Use /api/admin/users/:id/usage if admin (need own id); else show a note.
  refreshTraffic(root);
}

async function refreshTraffic(root) {
  const meters = document.getElementById("meters");
  const trows = document.getElementById("trows");
  // The user's own traffic isn't exposed via a self endpoint in this plan;
  // admin can see per-user via the Users page. Show sessions count + a note.
  try {
    const sess = await (await fetch("/api/sessions")).json();
    const alive = sess.filter(s => s.alive).length;
    meters.innerHTML = `<div class="meter">Sessions <b>${sess.length}</b></div><div class="meter">Alive <b>${alive}</b></div>`;
  } catch { meters.innerHTML = `<span class="muted">—</span>`; }
  trows.innerHTML = `<span class="muted">Per-user traffic details are visible to admins on the Users page. Your terminal and file-manager transfers are counted toward your monthly quota.</span>`;
}

// ---------------------------------------------------------------------------
// View: Admin — Users
// ---------------------------------------------------------------------------
async function viewAdminUsers(root) {
  root.innerHTML = `<div class="row"><span class="grow"></span><button class="btn" id="add-user">+ New user</button></div>
    <div class="card" style="margin-top:12px;overflow:auto"><table class="tbl"><thead><tr>
      <th>User</th><th>Role</th><th>Status</th><th>Disk</th><th>Traffic</th><th>Sessions</th><th>Last login</th><th></th>
    </tr></thead><tbody id="utbody"></tbody></table></div>`;
  document.getElementById("add-user").onclick = () => userModal(null, () => viewAdminUsers(root));
  await refreshUsers();
}

async function refreshUsers() {
  const tb = document.getElementById("utbody");
  if (!tb) return;
  let users = [];
  try { users = await getJson("/api/admin/users"); } catch {}
  tb.innerHTML = "";
  for (const u of users) {
    const tr = document.createElement("tr");
    const status = u.suspended ? '<span class="pill suspended">suspended</span>' : '<span class="pill online">active</span>';
    tr.innerHTML = `<td><b>${esc(u.username)}</b></td><td><span class="pill ${u.role==='admin'?'admin':''}">${u.role}</span></td><td>${status}</td><td class="muted" id="d-${u.id}">—</td><td class="muted" id="t-${u.id}">—</td><td class="muted" id="s-${u.id}">—</td><td class="muted" id="ll-${u.id}">—</td>`;
    const act = document.createElement("td");
    const btnS = document.createElement("button");
    btnS.className = "btn tiny " + (u.suspended ? "ghost" : "danger");
    btnS.textContent = u.suspended ? "Unsuspend" : "Suspend";
    btnS.onclick = async () => { await postJson(`/api/admin/users/${u.id}/${u.suspended ? "unsuspend" : "suspend"}`); refreshUsers(); };
    const btnD = document.createElement("button");
    btnD.className = "btn tiny danger"; btnD.textContent = "Delete"; btnD.style.marginLeft = "4px";
    btnD.onclick = async () => { if (confirm(`Delete ${u.username} and ALL their data? This cannot be undone.`)) { await del(`/api/admin/users/${u.id}`); refreshUsers(); } };
    act.appendChild(btnS); act.appendChild(btnD);
    tr.appendChild(act);
    tb.appendChild(tr);
    // Fetch usage for this user (disk/traffic/sessions).
    getJson(`/api/admin/users/${u.id}/usage`).then(us => {
      const d = document.getElementById(`d-${u.id}`); if (d) d.innerHTML = fmtBytes(us.disk.used) + " / " + fmtBytes(us.disk.limit) + (us.disk.over ? ' <span class="pill suspended">over</span>' : "");
      const t = document.getElementById(`t-${u.id}`); if (t) t.textContent = "↓" + fmtBytes(us.traffic.rx) + " ↑" + fmtBytes(us.traffic.tx);
      const s = document.getElementById(`s-${u.id}`); if (s) s.textContent = `${us.sessions.alive}/${us.sessions.total}`;
    }).catch(() => {});
    const ll = document.getElementById(`ll-${u.id}`);
    if (ll) {
      const when = (u.lastLoginAt && u.lastLoginAt > 0) ? new Date(u.lastLoginAt * 1000).toLocaleString() : "never";
      ll.innerHTML = esc(when) + (u.lastLoginIp ? ` <span class="faint tiny">${esc(u.lastLoginIp)}</span>` : "");
    }
  }
}

function userModal(_existing, _onDone) {
  const overlay = el("div", { class: "overlay" });
  const modal = el("div", { class: "modal" });
  modal.innerHTML = `<div class="hd"><b>New user</b></div><div class="bd">
    <label class="lbl">Username</label><input class="field" id="nu" placeholder="alice"><div style="height:8px"></div>
    <label class="lbl">Initial password</label><input class="field" id="np" type="password" placeholder="(user must change on first login)"><div style="height:8px"></div>
    <label class="lbl">Role</label><select class="field" id="nr"><option value="user">user</option><option value="admin">admin</option></select>
    <div style="height:14px"></div><button class="btn" id="nc">Create</button> <button class="btn ghost" id="nx">Cancel</button>
    <p class="muted tiny" id="ne" style="margin-top:8px"></p></div>`;
  overlay.appendChild(modal); app.appendChild(overlay);
  const close = () => overlay.remove();
  document.getElementById("nx").onclick = close;
  document.getElementById("nc").onclick = async () => {
    const r = await postJson("/api/admin/users", { username: document.getElementById("nu").value, password: document.getElementById("np").value, role: document.getElementById("nr").value });
    if (r.ok) { close(); refreshUsers(); }
    else document.getElementById("ne").textContent = "Failed (" + r.status + ")";
  };
}

// ---------------------------------------------------------------------------
// View: Admin — Credential presets
// ---------------------------------------------------------------------------
async function viewCredentials(root) {
  root.innerHTML = `<div class="row"><span class="grow"></span><button class="btn" id="add-cred">+ New preset</button></div>
    <div class="card" style="margin-top:12px;overflow:auto"><table class="tbl"><thead><tr><th>Name</th><th>Note</th><th></th></tr></thead><tbody id="ctbody"></tbody></table></div>`;
  document.getElementById("add-cred").onclick = credModal;
  await refreshCreds();
}
async function refreshCreds() {
  const tb = document.getElementById("ctbody"); if (!tb) return;
  let creds = []; try { creds = await getJson("/api/admin/credentials"); } catch {}
  tb.innerHTML = "";
  for (const c of creds) {
    const tr = document.createElement("tr");
    tr.innerHTML = `<td><b>${esc(c.name)}</b></td><td class="muted">${esc(c.note || "")}</td>`;
    const act = document.createElement("td");
    const btn = document.createElement("button"); btn.className = "btn tiny danger"; btn.textContent = "Delete";
    btn.onclick = async () => { if (confirm("Delete preset?")) { await del(`/api/admin/credentials/${c.id}`); refreshCreds(); } };
    act.appendChild(btn); tr.appendChild(act); tb.appendChild(tr);
  }
}
function credModal() {
  const overlay = el("div", { class: "overlay" });
  overlay.innerHTML = `<div class="modal"><div class="hd"><b>New credential preset</b></div><div class="bd">
    <label class="lbl">Name</label><input class="field" id="cn" placeholder="team-main"><div style="height:8px"></div>
    <label class="lbl">Anthropic auth token</label><input class="field" id="ct" placeholder="sk-ant-..."><div style="height:8px"></div>
    <label class="lbl">or API key</label><input class="field" id="ck" placeholder="(optional)"><div style="height:8px"></div>
    <label class="lbl">Base URL (optional)</label><input class="field" id="cb" placeholder="https://..."><div style="height:8px"></div>
    <label class="lbl">Note</label><input class="field" id="cno"><div style="height:14px"></div>
    <button class="btn" id="cc">Create</button> <button class="btn ghost" id="cx">Cancel</button>
    <p class="muted tiny" id="ce" style="margin-top:8px">Sealed with AES-256-GCM; secrets are never readable back.</p></div></div>`;
  app.appendChild(overlay);
  overlay.querySelector("#cx").onclick = () => overlay.remove();
  overlay.querySelector("#cc").onclick = async () => {
    const r = await postJson("/api/admin/credentials", { name: overlay.querySelector("#cn").value, auth_token: overlay.querySelector("#ct").value, api_key: overlay.querySelector("#ck").value, base_url: overlay.querySelector("#cb").value, note: overlay.querySelector("#cno").value });
    if (r.ok) { overlay.remove(); refreshCreds(); }
    else overlay.querySelector("#ce").textContent = "Failed (" + r.status + ")";
  };
}

// ---------------------------------------------------------------------------
// View: Admin — Role / quota templates
// ---------------------------------------------------------------------------
async function viewTemplates(root) {
  root.innerHTML = `<div class="row"><span class="grow"></span><button class="btn" id="add-tpl">+ New template</button></div>
    <div class="card" style="margin-top:12px;overflow:auto"><table class="tbl"><thead><tr><th>Name</th><th>Disk</th><th>Max sessions</th><th>CPU</th><th>Mem</th><th></th></tr></thead><tbody id="ttbody"></tbody></table></div>`;
  document.getElementById("add-tpl").onclick = tplModal;
  await refreshTpls();
}
async function refreshTpls() {
  const tb = document.getElementById("ttbody"); if (!tb) return;
  let tpls = []; try { tpls = await getJson("/api/admin/templates"); } catch {}
  tb.innerHTML = "";
  for (const t of tpls) {
    const tr = document.createElement("tr");
    tr.innerHTML = `<td><b>${esc(t.name)}</b></td><td class="muted">${fmtBytes(t.disk_quota_bytes)}</td><td class="muted">${t.max_sessions}</td><td class="muted">${esc(t.cpu_quota||"—")}</td><td class="muted">${fmtBytes(t.memory_max_bytes)}</td>`;
    const act = document.createElement("td");
    const btn = document.createElement("button"); btn.className = "btn tiny danger"; btn.textContent = "Delete";
    btn.onclick = async () => { await del(`/api/admin/templates/${t.id}`); refreshTpls(); };
    act.appendChild(btn); tr.appendChild(act); tb.appendChild(tr);
  }
}
function tplModal() {
  const overlay = el("div", { class: "overlay" });
  overlay.innerHTML = `<div class="modal"><div class="hd"><b>New role template</b></div><div class="bd">
    <label class="lbl">Name</label><input class="field" id="tn" placeholder="standard"><div style="height:8px"></div>
    <div class="grid2"><div><label class="lbl">Disk quota (GB)</label><input class="field" id="td" type="number" value="10"></div>
    <div><label class="lbl">Max sessions</label><input class="field" id="ts" type="number" value="3"></div></div>
    <div class="grid2"><div><label class="lbl">CPU quota (cgroup)</label><input class="field" id="tc" value="max"></div>
    <div><label class="lbl">Memory max (MB)</label><input class="field" id="tm" type="number" value="1024"></div></div>
    <div style="height:14px"></div><button class="btn" id="tgo">Create</button> <button class="btn ghost" id="tx">Cancel</button>
    <p class="muted tiny" id="te" style="margin-top:8px"></p></div></div>`;
  app.appendChild(overlay);
  overlay.querySelector("#tx").onclick = () => overlay.remove();
  overlay.querySelector("#tgo").onclick = async () => {
    const gb = parseInt(overlay.querySelector("#td").value) || 10;
    const r = await postJson("/api/admin/templates", {
      name: overlay.querySelector("#tn").value,
      disk_quota_bytes: gb * 1024 * 1024 * 1024,
      max_sessions: parseInt(overlay.querySelector("#ts").value) || 3,
      cpu_quota: overlay.querySelector("#tc").value || "max",
      memory_max_bytes: (parseInt(overlay.querySelector("#tm").value) || 1024) * 1024 * 1024,
    });
    if (r.ok) { overlay.remove(); refreshTpls(); }
    else overlay.querySelector("#te").textContent = "Failed (" + r.status + ")";
  };
}

// ---------------------------------------------------------------------------
// View: Admin — Audit (login-events stream)
// ---------------------------------------------------------------------------
async function viewAudit(root) {
  root.innerHTML = `<div class="card" style="overflow:auto"><table class="tbl"><thead><tr>
    <th>Time</th><th>User</th><th>IP</th><th>Result</th><th>User-Agent</th>
    </tr></thead><tbody id="abody"></tbody></table></div>`;
  const tb = document.getElementById("abody");
  let events = [];
  try { events = await getJson("/api/admin/login-events?limit=200"); } catch {}
  tb.innerHTML = "";
  for (const e of events) {
    const tr = document.createElement("tr");
    const when = e.at ? new Date(e.at * 1000).toLocaleString() : "—";
    const result = e.success
      ? '<span class="pill online">ok</span>'
      : '<span class="pill suspended">fail</span>';
    tr.innerHTML = `<td class="muted">${esc(when)}</td><td><b>${esc(e.username)}</b></td><td class="muted">${esc(e.ip || "—")}</td><td>${result}</td><td class="muted tiny">${esc((e.userAgent || "—").slice(0, 60))}</td>`;
    tb.appendChild(tr);
  }
  if (!events.length) tb.innerHTML = `<tr><td class="muted" colspan="5">No login events yet.</td></tr>`;
}

// --- helpers ---
function esc(s) { return String(s).replace(/[&<>"]/g, c => ({"&":"&amp;","<":"&lt;",">":"&gt;",'"':"&quot;"}[c])); }
function fmtBytes(n) {
  if (!n || n <= 0) return "0";
  const u = ["B","KB","MB","GB","TB"]; let i = 0; while (n >= 1024 && i < u.length-1) { n /= 1024; i++; }
  return n.toFixed(i < 2 ? 0 : 1) + " " + u[i];
}

boot();