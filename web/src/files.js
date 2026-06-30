import { getJson, postJson, del, uploadFile } from "./api.js";

// mountFiles(root): vanilla two-pane file manager over /api/files/*.
// Left: lazily-populated directory tree. Right: list OR icon grid, multi-
// select, keyboard nav, right-click context menu, image/text preview, drag-
// drop upload. Backend handlers are unchanged; this file owns all UX.

const TEXT_EXT = new Set(["txt","log","md","markdown","json","js","mjs","cjs","ts","tsx","jsx","go","py","rb","rs","java","c","cc","cpp","h","hpp","cs","php","sh","bash","zsh","yml","yaml","toml","ini","conf","cfg","xml","html","htm","css","scss","less","sql","env","gitignore","dockerfile","makefile","csv","tsv"]);
const IMG_EXT = new Set(["png","jpg","jpeg","gif","webp","svg","bmp","ico"]);
const PREVIEW_MAX = 2 * 1024 * 1024; // preview text files up to 2 MB
const IMG_ICON = { isDir: "📁", up: "↩", image: "🖼", text: "📄", bin: "📦" };

function isText(name) {
  const base = name.split("/").pop();
  if (base.includes(".")) return TEXT_EXT.has(base.split(".").pop().toLowerCase());
  return ["dockerfile","makefile","license","readme"].includes(base.toLowerCase());
}
function isImage(name) { return IMG_EXT.has((name.split(".").pop() || "").toLowerCase()); }
function esc(s) { return String(s).replace(/[&<>"]/g, c => ({"&":"&amp;","<":"&lt;",">":"&gt;",'"':"&quot;"}[c])); }

export function mountFiles(root) {
  const view = new FileView(root);
  view.mount();
}

class FileView {
  constructor(root) {
    this.root = root;
    this.cwd = "";
    this.history = [""];
    this.histIdx = 0;
    this.entries = [];
    this.selected = new Set(); // set of names in cwd
    this.lastAnchor = null;
    this.viewMode = localStorage.getItem("files.view") || "list"; // list | grid
    this.sortKey = localStorage.getItem("files.sort") || "name"; // name | size | mtime
    this.sortDir = 1;
    this.tree = new Map(); // dir path -> entries|null(expanded) ; null = not loaded
    this.treeOpen = new Set(); // expanded paths
    this.previewController = null;
  }

  mount() {
    this.root.innerHTML = `
      <div class="fm">
        <div class="fm-tree" id="fm-tree"></div>
        <div class="fm-main">
          <div class="fm-toolbar">
            <button class="btn tiny ghost" id="fm-back" title="Back (Alt+←)">←</button>
            <button class="btn tiny ghost" id="fm-fwd" title="Forward (Alt+→)">→</button>
            <button class="btn tiny ghost" id="fm-up" title="Up (Backspace)">↑</button>
            <div class="crumbs" id="fm-crumbs"></div>
            <span class="grow"></span>
            <input class="field fm-search" id="fm-search" placeholder="filter" style="max-width:160px">
            <button class="btn tiny ghost" id="fm-view" title="Toggle view (v)">${this.viewMode === "grid" ? "≣" : "☰"}</button>
            <button class="btn tiny ghost" id="fm-mkdir">+ Folder</button>
            <button class="btn tiny" id="fm-upload">↑ Upload</button>
          </div>
          <div class="fm-status" id="fm-status"></div>
          <div class="fm-drop" id="fm-drop"></div>
        </div>
      </div>
      <input type="file" id="fm-file-input" multiple style="display:none">`;
    this.bindToolbar();
    this.bindDrop();
    this.bindKeys();
    this.go("");
  }

  // --- navigation ---------------------------------------------------------
  join(dir, name) { return dir ? dir + "/" + name : name; }
  parent(dir) { const p = dir ? dir.split("/").slice(0, -1).join("/") : ""; return p; }

  async go(path, pushHist = true) {
    if (this.previewController) { this.previewController.close(); this.previewController = null; }
    this.cwd = path;
    this.selected.clear();
    this.lastAnchor = null;
    if (pushHist) { this.history = this.history.slice(0, this.histIdx + 1); this.history.push(path); this.histIdx = this.history.length - 1; }
    this.treeOpen.add(path || "/");
    this.renderToolbar();
    this.renderStatus("loading…");
    try {
      this.entries = await getJson(`/api/files/list?path=${encodeURIComponent(path)}`);
      if (this.entries == null) this.entries = [];
    } catch { this.entries = []; this.renderStatus("failed to list (auth?)"); }
    this.sort();
    this.renderList();
    this.renderTree();
    if (!this.entries.length) this.renderStatus("empty — drag files here or ↑ Upload");
    else this.renderStatus(`${this.entries.length} item${this.entries.length === 1 ? "" : "s"}`);
  }

  back()  { if (this.histIdx > 0) { this.histIdx--; this.go(this.history[this.histIdx], false); } }
  fwd()   { if (this.histIdx < this.history.length - 1) { this.histIdx++; this.go(this.history[this.histIdx], false); } }
  up()    { if (this.cwd) this.go(this.parent(this.cwd)); }

  // --- sorting ------------------------------------------------------------
  sort() {
    const k = this.sortKey;
    const dir = this.sortDir;
    this.entries.sort((a, b) => {
      if (a.isDir !== b.isDir) return a.isDir ? -1 : 1; // dirs first always
      let av, bv;
      if (k === "size") { av = a.size; bv = b.size; }
      else if (k === "mtime") { av = a.modTime; bv = b.modTime; }
      else { av = a.name.toLowerCase(); bv = b.name.toLowerCase(); }
      if (av < bv) return -1 * dir;
      if (av > bv) return 1 * dir;
      return 0;
    });
  }

  // --- toolbar ------------------------------------------------------------
  renderToolbar() {
    const c = document.getElementById("fm-crumbs");
    const parts = this.cwd ? this.cwd.split("/") : [];
    let html = `<span class="crumb" data-path="">workspace</span>`;
    let acc = "";
    for (const p of parts) {
      acc = acc ? acc + "/" + p : p;
      html += `<span class="crumb-sep">/</span><span class="crumb" data-path="${esc(acc)}">${esc(p)}</span>`;
    }
    c.innerHTML = html;
    c.querySelectorAll(".crumb").forEach(el => el.onclick = () => this.go(el.dataset.path));
    document.getElementById("fm-view").textContent = this.viewMode === "grid" ? "≣" : "☰";
  }

  renderStatus(msg) { const el = document.getElementById("fm-status"); if (el) el.textContent = msg; }

  bindToolbar() {
    document.getElementById("fm-back").onclick = () => this.back();
    document.getElementById("fm-fwd").onclick = () => this.fwd();
    document.getElementById("fm-up").onclick = () => this.up();
    document.getElementById("fm-mkdir").onclick = async () => {
      const name = prompt("Folder name");
      if (!name) return;
      const r = await postJson("/api/files/mkdir", { path: this.join(this.cwd, name) });
      if (!r.ok) { alert("mkdir failed (" + r.status + ")"); return; }
      this.go(this.cwd, false);
      this.refreshTreeAncestors(this.cwd);
    };
    const upload = async (files) => {
      for (const f of files) {
        const r = await uploadFile(`/api/files/upload?path=${encodeURIComponent(this.cwd)}`, f);
        if (!r.ok) { alert(`Upload ${f.name} failed (${r.status})`); return; }
      }
      document.getElementById("fm-file-input").value = "";
      this.go(this.cwd, false);
    };
    document.getElementById("fm-upload").onclick = () => document.getElementById("fm-file-input").click();
    document.getElementById("fm-file-input").onchange = (e) => upload(e.target.files);
    document.getElementById("fm-view").onclick = () => {
      this.viewMode = this.viewMode === "list" ? "grid" : "list";
      localStorage.setItem("files.view", this.viewMode);
      document.getElementById("fm-view").textContent = this.viewMode === "grid" ? "≣" : "☰";
      this.renderList();
    };
    const search = document.getElementById("fm-search");
    search.oninput = () => this.renderList();
  }

  bindDrop() {
    const drop = document.getElementById("fm-drop");
    drop.ondragover = (e) => { e.preventDefault(); drop.classList.add("drag"); };
    drop.ondragleave = () => drop.classList.remove("drag");
    drop.ondrop = async (e) => {
      e.preventDefault();
      drop.classList.remove("drag");
      if (e.dataTransfer.files && e.dataTransfer.files.length) {
        for (const f of e.dataTransfer.files) {
          const r = await uploadFile(`/api/files/upload?path=${encodeURIComponent(this.cwd)}`, f);
          if (!r.ok) alert(`Upload ${f.name} failed (${r.status})`);
        }
        this.go(this.cwd, false);
      }
    };
  }

  bindKeys() {
    this.root.tabIndex = 0;
    this.root.addEventListener("keydown", (e) => {
      if (e.target.tagName === "INPUT" && e.target.type !== "checkbox") return;
      if (e.altKey && e.key === "ArrowLeft") { e.preventDefault(); this.back(); }
      else if (e.altKey && e.key === "ArrowRight") { e.preventDefault(); this.fwd(); }
      else if (e.key === "Backspace") { e.preventDefault(); this.up(); }
      else if (e.key === "Delete") { e.preventDefault(); this.deleteSelected(); }
      else if (e.key === "F2") { e.preventDefault(); this.renameSelected(); }
      else if (e.ctrlKey && e.key === "a") { e.preventDefault(); this.selectAll(); }
      else if (e.key === "v" && !e.ctrlKey && !e.metaKey) { e.preventDefault(); document.getElementById("fm-view").click(); }
      else if (e.key === "Escape") { this.selected.clear(); this.lastAnchor = null; this.renderList(); }
    });
  }

  // --- list / grid rendering ---------------------------------------------
  filterText() { const el = document.getElementById("fm-search"); return (el?.value || "").toLowerCase(); }

  iconFor(e) { if (e.isDir) return IMG_ICON.isDir; if (isImage(e.name)) return IMG_ICON.image; if (isText(e.name)) return IMG_ICON.text; return IMG_ICON.bin; }

  renderList() {
    const drop = document.getElementById("fm-drop");
    const q = this.filterText();
    const items = this.entries.filter(e => !q || e.name.toLowerCase().includes(q));
    if (this.viewMode === "grid") drop.className = "fm-drop fm-grid";
    else drop.className = "fm-drop fm-list";

    drop.innerHTML = "";
    if (!items.length) {
      drop.innerHTML = `<div class="files-empty muted">${this.entries.length ? "no match" : "empty — drag files here"}</div>`;
      return;
    }

    for (const e of items) {
      const node = document.createElement("div");
      const full = this.join(this.cwd, e.name);
      const sel = this.selected.has(e.name);
      if (this.viewMode === "grid") {
        node.className = "fm-cell" + (sel ? " sel" : "");
        node.innerHTML = `<div class="fm-glyph">${this.iconFor(e)}</div><div class="fm-label">${esc(e.name)}</div>`;
      } else {
        node.className = "fm-row" + (sel ? " sel" : "");
        node.innerHTML = `
          <span class="fm-check"><input type="checkbox" ${sel ? "checked" : ""}></span>
          <span class="fm-glyph sm">${this.iconFor(e)}</span>
          <span class="fm-name">${esc(e.name)}</span>
          <span class="fm-meta muted">${e.isDir ? "—" : this.fmtSize(e.size)}</span>
          <span class="fm-meta muted">${this.fmtTime(e.modTime)}</span>`;
      }
      node.dataset.name = e.name;

      node.addEventListener("click", (ev) => this.onItemClick(ev, e));
      node.addEventListener("dblclick", () => this.open(e));
      node.addEventListener("contextmenu", (ev) => { ev.preventDefault(); this.openMenu(ev, e); });

      // drag a file onto a dir node to move it (move = rename)
      if (e.isDir) {
        node.draggable = false;
        node.addEventListener("dragover", (ev) => { ev.preventDefault(); node.classList.add("drop-target"); });
        node.addEventListener("dragleave", () => node.classList.remove("drop-target"));
        node.addEventListener("drop", async (ev) => {
          ev.preventDefault(); ev.stopPropagation();
          node.classList.remove("drop-target");
          const src = ev.dataTransfer.getData("text/plain");
          if (!src) return;
          const srcName = src.split("/").pop();
          const r = await postJson("/api/files/rename", { from: src, to: this.join(full, srcName) });
          if (!r.ok) alert("move failed (" + r.status + ")");
          this.go(this.cwd, false);
        });
      } else if (!e.isDir) {
        node.draggable = true;
        node.addEventListener("dragstart", (ev) => ev.dataTransfer.setData("text/plain", full));
      }
      drop.appendChild(node);
    }
  }

  onItemClick(ev, e) {
    const cb = ev.target.closest('input[type="checkbox"]');
    if (e.isDir && !ev.ctrlKey && !ev.metaKey && !ev.shiftKey && !cb && ev.detail === 1) {
      // single click on dir name (no modifiers) — open it (double-click also works)
    }
    if (ev.shiftKey && this.lastAnchor) {
      const names = this.entries.map(x => x.name);
      const a = names.indexOf(this.lastAnchor); const b = names.indexOf(e.name);
      if (a >= 0 && b >= 0) {
        const [lo, hi] = a < b ? [a, b] : [b, a];
        for (let i = lo; i <= hi; i++) this.selected.add(names[i]);
      }
    } else if (ev.ctrlKey || ev.metaKey || cb) {
      if (this.selected.has(e.name)) this.selected.delete(e.name);
      else this.selected.add(e.name);
      this.lastAnchor = e.name;
    } else {
      this.selected.clear();
      this.selected.add(e.name);
      this.lastAnchor = e.name;
    }
    this.renderList();
  }

  selectAll() { this.entries.forEach(e => this.selected.add(e.name)); this.renderList(); }

  open(e) {
    if (e.isDir) { this.go(this.join(this.cwd, e.name)); return; }
    if (isImage(e.name) || isText(e.name)) this.openPreview(e);
    else this.download([e.name]);
  }

  // --- preview ------------------------------------------------------------
  async openPreview(e) {
    if (this.previewController) this.previewController.close();
    const path = this.join(this.cwd, e.name);
    const overlay = document.createElement("div");
    overlay.className = "overlay";
    overlay.innerHTML = `<div class="modal" style="width:min(900px,94vw);max-height:92vh"><div class="hd"><b>${esc(e.name)}</b><button class="btn tiny ghost" id="pv-x">✕</button></div><div class="bd" id="pv-body" style="padding:0"></div></div>`;
    document.getElementById("app").appendChild(overlay);
    const body = overlay.querySelector("#pv-body");
    const close = () => { overlay.remove(); this.previewController = null; };
    overlay.querySelector("#pv-x").onclick = close;
    overlay.onclick = (ev) => { if (ev.target === overlay) close(); };
    this.previewController = { close };

    if (isImage(e.name)) {
      body.innerHTML = `<img style="display:block;max-width:100%;max-height:80vh;margin:auto" src="/api/files/download?path=${encodeURIComponent(path)}">`;
    } else {
      body.innerHTML = `<pre id="pv-pre" class="fm-pre">loading…</pre>`;
      try {
        const res = await fetch(`/api/files/download?path=${encodeURIComponent(path)}`);
        if (!res.ok) throw 0;
        let text = await res.text();
        if (text.length > PREVIEW_MAX) text = text.slice(0, PREVIEW_MAX) + `\n\n…[truncated ${(text.length/1024).toFixed(0)}KB shown ${(PREVIEW_MAX/1024).toFixed(0)}KB]`;
        overlay.querySelector("#pv-pre").textContent = text;
      } catch { overlay.querySelector("#pv-pre").textContent = "load failed"; }
    }
  }

  // --- actions ------------------------------------------------------------
  download(names) {
    for (const n of names) {
      const a = document.createElement("a");
      a.href = `/api/files/download?path=${encodeURIComponent(this.join(this.cwd, n))}`;
      a.download = n;
      document.body.appendChild(a); a.click(); a.remove();
    }
  }

  async deleteSelected() {
    const names = [...this.selected];
    if (!names.length) return;
    if (!confirm(`Delete ${names.length} item${names.length === 1 ? "" : "s"}? This cannot be undone.`)) return;
    for (const n of names) {
      const r = await del(`/api/files?path=${encodeURIComponent(this.join(this.cwd, n))}`);
      if (!r.ok) { alert(`delete ${n} failed (${r.status})`); break; }
    }
    this.selected.clear();
    this.go(this.cwd, false);
    this.refreshTreeAncestors(this.cwd);
  }

  async renameSelected() {
    const names = [...this.selected];
    const name = names[0];
    if (!name) return;
    const to = prompt(`Rename ${name} to`, name);
    if (!to || to === name) return;
    const r = await postJson("/api/files/rename", { from: this.join(this.cwd, name), to: this.join(this.cwd, to) });
    if (!r.ok) alert("rename failed (" + r.status + ")");
    this.selected.clear(); this.selected.add(to);
    this.go(this.cwd, false);
    this.refreshTreeAncestors(this.cwd);
  }

  // --- context menu -------------------------------------------------------
  openMenu(ev, e) {
    document.querySelectorAll(".fm-menu").forEach(m => m.remove());
    if (!this.selected.has(e.name)) { this.selected.clear(); this.selected.add(e.name); this.lastAnchor = e.name; this.renderList(); }
    const items = [];
    if (e.isDir) items.push({ label: "Open", act: () => this.open(e) });
    else {
      if (isImage(e.name) || isText(e.name)) items.push({ label: "Preview", act: () => this.openPreview(e) });
      items.push({ label: "Download", act: () => this.download([e.name]) });
    }
    items.push({ label: "Rename (F2)", act: () => this.renameSelected() });
    items.push({ label: "Delete (Del)", danger: true, act: () => this.deleteSelected() });
    const menu = document.createElement("div");
    menu.className = "fm-menu";
    menu.style.left = Math.min(ev.clientX, window.innerWidth - 180) + "px";
    menu.style.top = Math.min(ev.clientY, window.innerHeight - items.length * 34 - 10) + "px";
    for (const it of items) {
      const b = document.createElement("button");
      b.className = "fm-menu-item" + (it.danger ? " danger" : "");
      b.textContent = it.label;
      b.onclick = () => { menu.remove(); it.act(); };
      menu.appendChild(b);
    }
    document.body.appendChild(menu);
    const close = () => { menu.remove(); document.removeEventListener("mousedown", onDown); };
    const onDown = (de) => { if (!menu.contains(de.target)) close(); };
    setTimeout(() => document.addEventListener("mousedown", onDown), 0);
  }

  // --- directory tree (left pane) ----------------------------------------
  async renderTree() {
    const el = document.getElementById("fm-tree");
    const root = await this.treeEntries("");
    el.innerHTML = "";
    el.appendChild(this.treeNode("workspace", "", true));
  }

  async treeEntries(path) {
    if (!this.tree.has(path)) {
      try {
        const e = await getJson(`/api/files/list?path=${encodeURIComponent(path)}`);
        this.tree.set(path, (e || []).filter(x => x.isDir));
      } catch { this.tree.set(path, []); }
    }
    return this.tree.get(path) || [];
  }

  async refreshTreeAncestors(path) {
    this.tree.clear();
    await this.renderTree();
  }

  treeNode(label, path, isRoot) {
    const wrap = document.createElement("div");
    wrap.className = "fm-tree-node";
    const row = document.createElement("div");
    const open = this.treeOpen.has(path || "/");
    const here = (path || "") === this.cwd;
    row.className = "fm-tree-row" + (here ? " here" : "");
    row.innerHTML = `<span class="fm-tree-tw">${open && !isRoot ? "▾" : (isRoot ? "" : "▸")}</span><span class="fm-tree-lbl">${isRoot ? "🏠 " : "📁 "}${esc(label)}</span>`;
    row.onclick = async () => {
      if (!isRoot) this.treeOpen.has(path) ? this.treeOpen.delete(path) : this.treeOpen.add(path);
      this.go(path);
    };
    wrap.appendChild(row);
    const kids = document.createElement("div");
    kids.className = "fm-tree-kids";
    wrap.appendChild(kids);
    if (open || isRoot) {
      this.treeEntries(path).then(dirs => {
        for (const d of dirs) kids.appendChild(this.treeNode(d.name, this.join(path, d.name), false));
      });
    }
    return wrap;
  }

  // --- fmt helpers --------------------------------------------------------
  fmtTime(unix) { if (!unix) return "—"; return new Date(unix * 1000).toLocaleString(); }
  fmtSize(n) { if (!n) return "0"; const u = ["B","KB","MB","GB","TB"]; let i = 0; while (n >= 1024 && i < u.length - 1) { n /= 1024; i++; } return n.toFixed(i < 2 ? 0 : 1) + " " + u[i]; }
}
