import { getJson, postJson, del, uploadFile } from "./api.js";

// mountFiles(root): in-browser file manager over the user's workspace.
// Layout: breadcrumbs + toolbar, a table of entries, drag-drop upload, and a
// modal text editor. Pure vanilla JS, no framework.

// Ace syntax-highlight modes, lazily loaded by file extension. Keys are lower-
// cased extensions; values name the ace-builds mode module (minus mode-/-js).
const EXT_TO_ACE = {
  js: "javascript", mjs: "javascript", cjs: "javascript", jsx: "javascript",
  ts: "typescript", tsx: "typescript",
  go: "golang", py: "python", rb: "ruby", rs: "rust", java: "java",
  c: "c_cpp", cc: "c_cpp", cpp: "c_cpp", h: "c_cpp", hpp: "c_cpp",
  cs: "csharp", php: "php",
  sh: "sh", bash: "sh", zsh: "sh",
  json: "json", json5: "json5",
  html: "html", htm: "html",
  css: "css", scss: "scss", less: "less",
  xml: "xml", svg: "xml",
  md: "markdown", markdown: "markdown",
  yaml: "yaml", yml: "yaml",
  sql: "sql", toml: "toml",
  ini: "ini", conf: "ini", cfg: "ini",
  lua: "lua", dart: "dart",
};
const aceModeLoaders = {
  javascript: () => import("ace-builds/src-noconflict/mode-javascript.js"),
  typescript: () => import("ace-builds/src-noconflict/mode-typescript.js"),
  golang: () => import("ace-builds/src-noconflict/mode-golang.js"),
  python: () => import("ace-builds/src-noconflict/mode-python.js"),
  ruby: () => import("ace-builds/src-noconflict/mode-ruby.js"),
  rust: () => import("ace-builds/src-noconflict/mode-rust.js"),
  java: () => import("ace-builds/src-noconflict/mode-java.js"),
  c_cpp: () => import("ace-builds/src-noconflict/mode-c_cpp.js"),
  csharp: () => import("ace-builds/src-noconflict/mode-csharp.js"),
  php: () => import("ace-builds/src-noconflict/mode-php.js"),
  sh: () => import("ace-builds/src-noconflict/mode-sh.js"),
  json: () => import("ace-builds/src-noconflict/mode-json.js"),
  json5: () => import("ace-builds/src-noconflict/mode-json5.js"),
  html: () => import("ace-builds/src-noconflict/mode-html.js"),
  css: () => import("ace-builds/src-noconflict/mode-css.js"),
  scss: () => import("ace-builds/src-noconflict/mode-scss.js"),
  less: () => import("ace-builds/src-noconflict/mode-less.js"),
  xml: () => import("ace-builds/src-noconflict/mode-xml.js"),
  markdown: () => import("ace-builds/src-noconflict/mode-markdown.js"),
  yaml: () => import("ace-builds/src-noconflict/mode-yaml.js"),
  sql: () => import("ace-builds/src-noconflict/mode-sql.js"),
  toml: () => import("ace-builds/src-noconflict/mode-toml.js"),
  ini: () => import("ace-builds/src-noconflict/mode-ini.js"),
  lua: () => import("ace-builds/src-noconflict/mode-lua.js"),
  dart: () => import("ace-builds/src-noconflict/mode-dart.js"),
};

export function mountFiles(root) {
  root.innerHTML = `
    <div class="files-toolbar">
      <div class="crumbs" id="crumbs"></div>
      <span class="grow"></span>
      <button class="btn tiny ghost" id="mkdir-btn">+ Folder</button>
      <button class="btn tiny ghost" id="up-btn">↑ Up</button>
    </div>
    <div class="files-drop" id="drop">
      <table class="tbl" id="ftbl"><thead><tr><th>Name</th><th>Size</th><th>Modified</th><th></th></tr></thead><tbody id="fbody"></tbody></table>
      <div class="files-empty muted" id="fempty" style="padding:32px">Empty folder — drag files here to upload.</div>
    </div>
    <input type="file" id="file-input" multiple style="display:none">`;

  let cwd = ""; // relative path under workspace

  const fbody = () => document.getElementById("fbody");
  const fempty = () => document.getElementById("fempty");

  function fmtTime(unix) {
    if (!unix) return "—";
    const d = new Date(unix * 1000);
    return d.toLocaleString();
  }
  function fmtSize(n) {
    if (!n) return "0";
    const u = ["B","KB","MB","GB"]; let i = 0;
    while (n >= 1024 && i < u.length-1) { n /= 1024; i++; }
    return n.toFixed(i < 2 ? 0 : 1) + " " + u[i];
  }
  function esc(s) { return String(s).replace(/[&<>"]/g, c => ({"&":"&amp;","<":"&lt;",">":"&gt;",'"':"&quot;"}[c])); }

  function renderCrumbs() {
    const c = document.getElementById("crumbs");
    const parts = cwd ? cwd.split("/") : [];
    let html = `<span class="crumb" data-path="">workspace</span>`;
    let acc = "";
    for (const p of parts) {
      acc = acc ? acc + "/" + p : p;
      html += `<span class="crumb-sep">/</span><span class="crumb" data-path="${esc(acc)}">${esc(p)}</span>`;
    }
    c.innerHTML = html;
    c.querySelectorAll(".crumb").forEach(el => el.onclick = () => { cwd = el.dataset.path; refresh(); });
  }

  async function refresh() {
    renderCrumbs();
    const path = encodeURIComponent(cwd);
    let entries = [];
    try { entries = await getJson(`/api/files/list?path=${path}`); } catch { entries = []; }
    const tb = fbody();
    tb.innerHTML = "";
    fempty().style.display = entries.length ? "none" : "block";
    for (const e of entries) {
      const tr = document.createElement("tr");
      const icon = e.isDir ? "📁" : "📄";
      tr.innerHTML = `<td><span class="fname">${icon} ${esc(e.name)}</span></td><td class="muted">${e.isDir ? "—" : fmtSize(e.size)}</td><td class="muted">${fmtTime(e.modTime)}</td>`;
      const act = document.createElement("td");
      act.className = "files-actions";
      if (!e.isDir) {
        const dl = document.createElement("a");
        dl.className = "btn tiny ghost"; dl.textContent = "↓"; dl.href = `/api/files/download?path=${encodeURIComponent(cwd ? cwd+"/"+e.name : e.name)}`;
        act.appendChild(dl);
        const ed = document.createElement("button");
        ed.className = "btn tiny ghost"; ed.textContent = "✎";
        ed.onclick = () => openEditor(cwd ? cwd+"/"+e.name : e.name);
        act.appendChild(ed);
      } else {
        tr.querySelector(".fname").style.cursor = "pointer";
        tr.querySelector(".fname").onclick = () => { cwd = cwd ? cwd+"/"+e.name : e.name; refresh(); };
      }
      const full = cwd ? cwd + "/" + e.name : e.name;
      const rn = document.createElement("button");
      rn.className = "btn tiny ghost"; rn.textContent = "⤴"; rn.title = "Rename";
      rn.onclick = async () => {
        const name = prompt("Rename to", e.name);
        if (!name || name === e.name) return;
        const r = await postJson("/api/files/rename", { from: full, to: cwd ? cwd + "/" + name : name });
        if (r.ok) refresh();
        else alert("Rename failed (" + r.status + ")");
      };
      act.appendChild(rn);
      const rm = document.createElement("button");
      rm.className = "btn tiny danger"; rm.textContent = "✕";
      rm.onclick = async () => {
        if (!confirm(`Delete ${e.name}?`)) return;
        const p = encodeURIComponent(cwd ? cwd+"/"+e.name : e.name);
        await del(`/api/files?path=${p}`);
        refresh();
      };
      act.appendChild(rm);
      tr.appendChild(act);
      tb.appendChild(tr);
    }
  }

  async function openEditor(path) {
    const overlay = document.createElement("div");
    overlay.className = "overlay";
    overlay.innerHTML = `<div class="modal" style="width:min(820px,94vw)"><div class="hd"><b>${esc(path)}</b></div>
      <div class="bd"><div id="ed-area" style="height:58vh;width:100%;border:1px solid var(--border);border-radius:6px"></div>
      <div style="height:12px"></div><button class="btn" id="ed-save">Save</button> <button class="btn ghost" id="ed-cancel">Cancel</button>
      <span class="muted tiny" id="ed-msg" style="margin-left:8px"></span></div></div>`;
    document.getElementById("app").appendChild(overlay);
    const msg = () => overlay.querySelector("#ed-msg");
    let editor = null;

    try {
      const [res] = await Promise.all([
        fetch(`/api/files/download?path=${encodeURIComponent(path)}`).then((r) => r.text()),
        import("ace-builds/src-noconflict/ace"),
      ]);
      const ace = window.ace;
      editor = ace.edit(overlay.querySelector("#ed-area"));
      editor.setOptions({ fontFamily: "JetBrains Mono, ui-monospace, monospace", fontSize: "13px", printMargin: false, showPrintMargin: false });
      editor.session.setUseWorker(false);
      const ext = (path.split(".").pop() || "").toLowerCase();
      const modName = EXT_TO_ACE[ext];
      if (modName && aceModeLoaders[modName]) {
        await aceModeLoaders[modName]();
        editor.session.setMode(`ace/mode/${modName}`);
      }
      const dark = document.documentElement.getAttribute("data-theme") === "dark"
        || (document.documentElement.getAttribute("data-theme") !== "light" && window.matchMedia("(prefers-color-scheme: dark)").matches);
      if (dark) { await import("ace-builds/src-noconflict/theme-tomorrow_night.js"); editor.setTheme("ace/theme/tomorrow_night"); }
      editor.setValue(res, -1);
    } catch {
      msg().textContent = "load failed";
    }

    overlay.querySelector("#ed-cancel").onclick = () => overlay.remove();
    overlay.querySelector("#ed-save").onclick = async () => {
      const content = editor ? editor.getValue() : "";
      const r = await postJson("/api/files/edit", { path, content });
      if (r.ok) { overlay.remove(); refresh(); }
      else msg().textContent = "save failed (" + r.status + ")";
    };
  }

  // Upload via input + drag-drop.
  const fileInput = document.getElementById("file-input");
  document.getElementById("mkdir-btn").onclick = async () => {
    const name = prompt("Folder name");
    if (!name) return;
    await postJson("/api/files/mkdir", { path: cwd ? cwd+"/"+name : name });
    refresh();
  };
  document.getElementById("up-btn").onclick = () => {
    const parts = cwd ? cwd.split("/") : [];
    parts.pop();
    cwd = parts.join("/");
    refresh();
  };
  fileInput.onchange = () => uploadFiles(fileInput.files);
  const drop = document.getElementById("drop");
  drop.onclick = (e) => {
    if (e.target.closest("button, a")) return;
    fileInput.click();
  };
  drop.ondragover = (e) => { e.preventDefault(); drop.classList.add("drag"); };
  drop.ondragleave = () => drop.classList.remove("drag");
  drop.ondrop = (e) => {
    e.preventDefault();
    drop.classList.remove("drag");
    uploadFiles(e.dataTransfer.files);
  };

  async function uploadFiles(fileList) {
    for (const f of fileList) {
      const url = `/api/files/upload?path=${encodeURIComponent(cwd)}`;
      const r = await uploadFile(url, f);
      if (!r.ok) { alert(`Upload ${f.name} failed (${r.status})`); }
    }
    fileInput.value = "";
    refresh();
  }

  refresh();
}