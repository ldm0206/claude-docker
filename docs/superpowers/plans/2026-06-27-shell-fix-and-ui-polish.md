# Shell 修复 & UI 美化 实施计划

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** 修复终端不可用问题（claude 二进制缺失 + PTY 退出无反馈）并美化 UI

**Architecture:** Dockerfile 安装验证防静默失败，server.js PATH 兜底 + 降级启动，前端 PTY 退出覆盖层，CSS/xterm 主题全面打磨

**Tech Stack:** Node.js (Fastify + node-pty + ws), xterm.js, Vite, Vitest

## Global Constraints

- 容器用户: claude (uid 1000), HOME=/home/claude
- claude 二进制路径: /home/claude/.local/bin/claude
- 降级 shell: /bin/bash
- Claude 风格色板: cream=#F4F1EA, ink=#2B2A27, accent=#C96442, accent-2=#D97757, surface=#FFFFFF, surface-2=#FAF7F0, border=#E4DED2, terminal-bg=#1E1B16
- 测试框架: Vitest (server/test/*.test.js)
- Web 构建: Vite (web/dist/)

---

### Task 1: Dockerfile 安装验证

**Files:**
- Modify: `Dockerfile:18-19`

**Interfaces:**
- Produces: 容器镜像中 `/home/claude/.local/bin/claude` 可执行文件存在（或构建失败中断）

- [ ] **Step 1: 修改 Dockerfile 安装步骤**

将第 18-19 行从:
```
USER claude
RUN curl -fsSL https://claude.ai/install.sh | bash
```

改为:
```dockerfile
USER claude
RUN curl -fsSL https://claude.ai/install.sh -o /tmp/install-claude.sh \
    && bash /tmp/install-claude.sh \
    && rm /tmp/install-claude.sh \
    && test -x /home/claude/.local/bin/claude
```

关键: `test -x` 验证二进制存在且可执行，失败时整个 RUN 层退出码非零，Docker 构建中断。不再用管道 `| bash` 吞掉退出码。

- [ ] **Step 2: Commit**

```bash
git add Dockerfile
git commit -m "fix: validate claude binary exists after install to prevent silent failures"
```

---

### Task 2: PATH 兜底 + 降级启动

**Files:**
- Modify: `server/src/pty-manager.js:12-23` (start 方法)
- Modify: `server/src/server.js:146-149` (buildClaudeEnv PATH/HOME)
- Test: `server/test/pty-env.test.js` (已有，需扩展)

**Interfaces:**
- Consumes: `/home/claude/.local/bin/claude` 路径（来自 Task 1 的安装验证）
- Produces: `createPtyManager` 在 `claude` 不可执行时自动降级为 `/bin/bash`，并在终端打印警告

- [ ] **Step 1: 修改 buildClaudeEnv 硬编码 PATH 和 HOME**

在 `server/src/server.js` 中，将 `buildClaudeEnv` 函数的 PATH 行 (约第 147-148 行) 从:

```js
const env = {
  ...process.env,
  PATH: `${process.env.HOME}/.local/bin:${process.env.PATH}`,
```

改为:

```js
const CLAUDE_BIN = "/home/claude/.local/bin";
const env = {
  ...process.env,
  HOME: "/home/claude",
  PATH: `${CLAUDE_BIN}:${process.env.PATH}`,
```

同时将第 150 行的 `CLAUDE_CONFIG_DIR` 保留不变。

- [ ] **Step 2: 在 createPtyManager 中添加降级逻辑**

修改 `server/src/pty-manager.js` 的 `createPtyManager` 函数签名，添加 `fallbackCommand` 参数:

```js
import { spawn as ptySpawn } from "node-pty";
import { existsSync } from "node:fs";

export function resolveEnv(env) {
  return typeof env === "function" ? env() : env;
}

const CLAUDE_BIN_PATH = "/home/claude/.local/bin/claude";

export function createPtyManager({ cwd, env, command = "claude", args = [], cols = 80, rows = 24 }) {
  let proc = null;
  const dataCbs = new Set();
  const exitCbs = new Set();
  let effectiveCommand = command;
  let effectiveArgs = args;

  return {
    start() {
      if (proc) return;
      const envObj = resolveEnv(env);
      // If claude binary is missing, fall back to bash so the user can
      // inspect the container environment and troubleshoot.
      if (effectiveCommand === "claude" && !existsSync(CLAUDE_BIN_PATH)) {
        effectiveCommand = "/bin/bash";
        effectiveArgs = [];
        // Print a warning that will appear in the terminal
        const warning = "\r\n⚠ claude not found at " + CLAUDE_BIN_PATH + ", falling back to bash\r\n\r\n";
        for (const cb of dataCbs) cb(warning);
      }
      proc = ptySpawn(effectiveCommand, effectiveArgs, {
        name: "xterm-256color",
        cols, rows,
        cwd,
        env: envObj,
      });
      for (const cb of dataCbs) proc.onData(cb);
      proc.onExit(({ exitCode }) => {
        const was = proc;
        proc = null;
        for (const cb of exitCbs) cb(exitCode);
        if (was) was.kill();
      });
    },
    onData(cb) {
      dataCbs.add(cb);
      if (proc) proc.onData(cb);
      return () => dataCbs.delete(cb);
    },
    write(data) { if (proc) proc.write(data); },
    resize(c, r) { if (proc) proc.resize(c, r); },
    onExit(cb) { exitCbs.add(cb); return () => exitCbs.delete(cb); },
    kill() { if (proc) { proc.kill(); proc = null; } },
    get alive() { return !!proc; },
  };
}
```

- [ ] **Step 3: 扩展 pty-env 测试验证降级行为**

在 `server/test/pty-env.test.js` 中添加测试:

```js
import { describe, it, expect } from "vitest";
import { resolveEnv, createPtyManager } from "../src/pty-manager.js";

describe("resolveEnv", () => {
  it("resolves plain object", () => {
    expect(resolveEnv({ FOO: "bar" })).toEqual({ FOO: "bar" });
  });
  it("resolves factory function", () => {
    expect(resolveEnv(() => ({ FOO: "bar" }))).toEqual({ FOO: "bar" });
  });
});

describe("createPtyManager fallback", () => {
  it("falls back to bash when claude binary missing", () => {
    // /home/claude/.local/bin/claude does not exist on test machines
    const pty = createPtyManager({ cwd: "/tmp", env: {} });
    const warningCb = (d) => captured.push(d);
    const captured = [];
    pty.onData(warningCb);
    pty.start();
    // The warning should have been emitted before the bash process starts
    expect(captured.some(d => d.includes("falling back to bash"))).toBe(true);
    pty.kill();
  });
});
```

- [ ] **Step 4: 运行测试验证通过**

```bash
cd server && npm test
```

Expected: 所有测试通过，包括新的 fallback 测试

- [ ] **Step 5: Commit**

```bash
git add server/src/pty-manager.js server/src/server.js server/test/pty-env.test.js
git commit -m "fix: hardcode PATH/HOME and fallback to bash when claude binary missing"
```

---

### Task 3: PTY 退出事件 — 后端 WebSocket 转发

**Files:**
- Modify: `server/src/server.js:119-128` (WebSocket terminal 连接处理)

**Interfaces:**
- Consumes: `pty.onExit(callback)` 来自 Task 2 的 pty-manager
- Produces: WebSocket 消息 `{ type: "pty-exit", exitCode: <number> }` 发送给前端终端客户端

- [ ] **Step 1: 在 terminal WebSocket 连接中转发 pty-exit 事件**

修改 `server/src/server.js` 中 `/ws/terminal` 的 connection handler (约第 119-128 行)。当前代码:

```js
wss.on("connection", (ws, pathname) => {
    if (pathname === "/ws/terminal") {
      if (!pty.alive) pty.start();
      const unsubData = pty.onData((d) => ws.readyState === ws.OPEN && ws.send(d));
      ws.on("message", (raw) => {
        const msg = JSON.parse(raw.toString());
        if (msg.type === "resize") pty.resize(msg.cols, msg.rows);
        else if (msg.type === "input") pty.write(msg.data);
      });
      ws.on("close", unsubData);
    }
```

改为:

```js
wss.on("connection", (ws, pathname) => {
    if (pathname === "/ws/terminal") {
      if (!pty.alive) pty.start();
      const unsubData = pty.onData((d) => ws.readyState === ws.OPEN && ws.send(d));
      const unsubExit = pty.onExit((exitCode) => {
        if (ws.readyState === ws.OPEN) ws.send(JSON.stringify({ type: "pty-exit", exitCode }));
      });
      ws.on("message", (raw) => {
        const msg = JSON.parse(raw.toString());
        if (msg.type === "resize") pty.resize(msg.cols, msg.rows);
        else if (msg.type === "input") pty.write(msg.data);
      });
      ws.on("close", () => { unsubData(); unsubExit(); });
    }
```

- [ ] **Step 2: Commit**

```bash
git add server/src/server.js
git commit -m "feat: forward PTY exit events to terminal WebSocket clients"
```

---

### Task 4: PTY 退出 UI — 前端覆盖层

**Files:**
- Modify: `web/src/terminal.js` (完整重写)
- Modify: `web/src/styles.css` (添加覆盖层样式)

**Interfaces:**
- Consumes: WebSocket 消息 `{ type: "pty-exit", exitCode }` 来自 Task 3
- Consumes: `POST /api/session/restart` API (已有)
- Produces: 退出覆盖层 UI（DOM 元素），重启按钮触发 `/api/session/restart`

- [ ] **Step 1: 添加覆盖层 CSS**

在 `web/src/styles.css` 末尾添加:

```css
.pty-overlay {
  position: absolute;
  inset: 0;
  background: rgba(244,241,234,0.92);
  display: flex;
  flex-direction: column;
  align-items: center;
  justify-content: center;
  z-index: 10;
  border-radius: var(--radius);
}
.pty-overlay h3 {
  font-size: 1.1rem;
  color: var(--ink);
  margin-bottom: 8px;
}
.pty-overlay .exit-code {
  color: var(--muted);
  font-size: .85rem;
  margin-bottom: 16px;
}
.pty-overlay button {
  background: var(--accent);
  color: #fff;
  border: none;
  padding: 10px 24px;
  border-radius: 8px;
  cursor: pointer;
  font-size: .9rem;
}
.pty-overlay button:hover {
  background: var(--accent-2);
}
```

- [ ] **Step 2: 重写 terminal.js 监听 pty-exit 并渲染覆盖层**

将 `web/src/terminal.js` 从:

```js
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
```

改为:

```js
import { Terminal } from "xterm";
import { FitAddon } from "@xterm/addon-fit";
import { WebLinksAddon } from "@xterm/addon-web-links";
import "xterm/css/xterm.css";
import { postJson } from "./api.js";

export function mountTerminal() {
  const term = new Terminal({
    fontFamily: "JetBrains Mono, ui-monospace, monospace",
    theme: {
      background: "#1E1B16",
      foreground: "#F4F1EA",
      cursor: "#D97757",
      cursorAccent: "#1E1B16",
      selection: "rgba(217,119,87,0.3)",
      black: "#1E1B16",
      red: "#C96442",
      green: "#7A9E7E",
      yellow: "#D4A856",
      blue: "#6B8FA3",
      magenta: "#A37E8C",
      cyan: "#7EA3A8",
      white: "#F4F1EA",
      brightBlack: "#6B6760",
      brightRed: "#D97757",
      brightGreen: "#9BBF9F",
      brightYellow: "#E4C07A",
      brightBlue: "#8BAFC3",
      brightMagenta: "#BD9EAC",
      brightCyan: "#9EC3C8",
      brightWhite: "#FFFFFF",
    },
  });
  const fit = new FitAddon();
  term.loadAddon(fit);
  term.loadAddon(new WebLinksAddon());
  const container = document.querySelector(".term-wrap");
  term.open(container.querySelector(".xterm-viewport") ? container : container);
  fit.fit();

  const ws = new WebSocket(`${location.protocol === "https:" ? "wss" : "ws"}://${location.host}/ws/terminal`);
  ws.onmessage = (e) => {
    const raw = e.data;
    // JSON messages are structured events; raw strings are terminal output
    try {
      const msg = JSON.parse(raw);
      if (msg.type === "pty-exit") showExitOverlay(msg.exitCode);
      return;
    } catch { /* not JSON — terminal data */ }
    term.write(raw);
  };
  term.onData((d) => ws.readyState === ws.OPEN && ws.send(JSON.stringify({ type: "input", data: d })));
  window.addEventListener("resize", () => fit.fit());
  term.onResize(({ cols, rows }) => ws.readyState === ws.OPEN && ws.send(JSON.stringify({ type: "resize", cols, rows })));

  function showExitOverlay(exitCode) {
    const overlay = document.createElement("div");
    overlay.className = "pty-overlay";
    overlay.innerHTML = `
      <h3>Session ended</h3>
      <div class="exit-code">Exit code: ${exitCode}</div>
      <button id="pty-restart">Restart</button>`;
    container.style.position = "relative";
    container.appendChild(overlay);
    overlay.querySelector("#pty-restart").onclick = async () => {
      overlay.remove();
      await postJson("/api/session/restart");
    };
  }
}
```

注意: xterm 的 `term.write()` 只接收字符串数据。JSON 结构消息（pty-exit）需要先 parse 再分发，普通终端输出直接 write。这与当前 `ws.onmessage = (e) => term.write(e.data)` 不同，需要 try/catch 区分。

但这里有个问题：当前后端 `pty.onData` 发送的是原始字符串（不是 JSON），而 `pty-exit` 是 JSON。前端必须区分两者。用 try/catch parse 是最简洁的方式 — 如果 parse 成功且有 type 字段就是事件，否则是终端数据。

- [ ] **Step 3: Commit**

```bash
git add web/src/terminal.js web/src/styles.css
git commit -m "feat: add PTY exit overlay with restart button and xterm Claude theme"
```

---

### Task 5: UI 顶栏品牌条 + 面板打磨

**Files:**
- Modify: `web/src/main.js` (HTML 结构重写)
- Modify: `web/src/styles.css` (全面 CSS 打磨)

**Interfaces:**
- Consumes: 所有现有模块 (terminal.js, metrics.js, captures.js)
- Produces: 顶栏 DOM 结构，侧栏折叠状态

- [ ] **Step 1: 重写 main.js HTML 结构**

将 `web/src/main.js` 从:

```js
import { renderUnlock } from "./unlock.js";
import { mountTerminal } from "./terminal.js";
import { mountMetrics } from "./metrics.js";
import { mountCaptures } from "./captures.js";
import { getJson } from "./api.js";

const app = document.getElementById("app");

async function boot() {
  try {
    await getJson("/api/state");
  } catch {
    renderUnlock(app, boot);
    return;
  }
  app.innerHTML = `
    <section class="panel term-wrap"><h2>Terminal</h2><div></div></section>
    <aside style="display:flex;flex-direction:column;gap:16px;min-height:0">
      <section class="panel">
        <h2>Resources</h2>
        <div class="meters">
          <div class="meter"><div class="label">CPU</div><div class="value" id="cpu">—</div></div>
          <div class="meter"><div class="label">Memory</div><div class="value" id="mem">—</div></div>
          <div class="meter" style="grid-column:span 2"><div class="label">Network</div><div class="value" id="net">—</div></div>
        </div>
      </section>
      <section class="panel" id="cap-panel" style="flex:1;min-height:0;display:flex;flex-direction:column"></section>
    </aside>`;
  mountTerminal();
  mountMetrics();
  mountCaptures(document.getElementById("cap-panel"));
}
boot();
```

改为:

```js
import { renderUnlock } from "./unlock.js";
import { mountTerminal } from "./terminal.js";
import { mountMetrics } from "./metrics.js";
import { mountCaptures } from "./captures.js";
import { getJson } from "./api.js";

const app = document.getElementById("app");

async function boot() {
  try {
    await getJson("/api/state");
  } catch {
    renderUnlock(app, boot);
    return;
  }
  app.innerHTML = `
    <header class="topbar">
      <span class="topbar-brand">Claude Code</span>
      <span class="topbar-status" id="session-status"></span>
      <button class="topbar-toggle" id="sidebar-toggle" title="Toggle sidebar">≡</button>
    </header>
    <div class="workspace">
      <section class="panel term-wrap" id="term-section">
        <div class="term-titlebar">
          <span>▸ Terminal</span>
        </div>
        <div class="term-body"></div>
      </section>
      <aside class="sidebar" id="sidebar">
        <section class="panel">
          <h2>Resources</h2>
          <div class="meters">
            <div class="meter">
              <div class="meter-row"><div class="label">CPU</div><div class="value" id="cpu">—</div></div>
              <div class="meter-bar"><div class="meter-fill" id="cpu-bar"></div></div>
            </div>
            <div class="meter">
              <div class="meter-row"><div class="label">Memory</div><div class="value" id="mem">—</div></div>
              <div class="meter-bar"><div class="meter-fill" id="mem-bar"></div></div>
            </div>
            <div class="meter net-meter">
              <div class="meter-row"><div class="label">Network</div><div class="value" id="net">—</div></div>
            </div>
          </div>
        </section>
        <section class="panel" id="cap-panel" style="flex:1;min-height:0;display:flex;flex-direction:column"></section>
      </aside>
    </div>`;
  mountTerminal();
  mountMetrics();
  mountCaptures(document.getElementById("cap-panel"));

  // Sidebar toggle
  const sidebar = document.getElementById("sidebar");
  document.getElementById("sidebar-toggle").onclick = () => {
    sidebar.classList.toggle("collapsed");
  };
}
boot();
```

- [ ] **Step 2: 重写 styles.css**

将 `web/src/styles.css` 完整替换为:

```css
:root {
  --bg: #F4F1EA;
  --surface: #FFFFFF;
  --surface-2: #FAF7F0;
  --ink: #2B2A27;
  --muted: #6B6760;
  --accent: #C96442;
  --accent-2: #D97757;
  --border: #E4DED2;
  --radius: 12px;
  --term-bg: #1E1B16;
  --topbar-bg: #2B2A27;
}
* { box-sizing: border-box; }
html, body { height: 100%; margin: 0; }
body {
  background: var(--bg);
  color: var(--ink);
  font-family: ui-sans-serif, system-ui, "Segoe UI", Roboto, sans-serif;
}
h1, h2, h3 { font-family: Georgia, "Times New Roman", serif; font-weight: 600; margin: 0; }

/* Topbar */
.topbar {
  display: flex;
  align-items: center;
  height: 48px;
  padding: 0 16px;
  background: var(--topbar-bg);
  color: var(--bg);
  gap: 12px;
}
.topbar-brand {
  font-family: Georgia, serif;
  font-size: 1.1rem;
  font-weight: 600;
  letter-spacing: .02em;
}
.topbar-status {
  width: 10px;
  height: 10px;
  border-radius: 50%;
  background: #4CAF50;
  transition: background 0.3s;
}
.topbar-status.dead { background: var(--muted); }
.topbar-toggle {
  margin-left: auto;
  background: none;
  border: 1px solid rgba(244,241,234,0.3);
  color: var(--bg);
  font-size: 1.2rem;
  padding: 4px 10px;
  border-radius: 6px;
  cursor: pointer;
  transition: background 0.15s;
}
.topbar-toggle:hover { background: rgba(244,241,234,0.1); }

/* Workspace layout */
.workspace {
  display: grid;
  grid-template-columns: 1fr 320px;
  gap: 16px;
  padding: 16px;
  height: calc(100vh - 48px);
  min-height: 0;
}
.sidebar.collapsed { display: none; }

/* Panels */
.panel { background: var(--surface); border: 1px solid var(--border); border-radius: var(--radius); padding: 16px; box-shadow: 0 1px 3px rgba(0,0,0,0.08); }
.panel h2 { font-size: 1rem; color: var(--muted); margin-bottom: 12px; letter-spacing: .02em; }

/* Terminal section */
#term-section { display: flex; flex-direction: column; min-height: 0; overflow: hidden; }
.term-titlebar {
  display: flex;
  align-items: center;
  justify-content: space-between;
  height: 32px;
  padding: 0 12px;
  background: var(--term-bg);
  color: #A09888;
  font-size: .8rem;
  font-family: ui-sans-serif, system-ui, sans-serif;
  border-radius: var(--radius) var(--radius) 0 0;
}
.term-body {
  flex: 1;
  min-height: 0;
  background: var(--term-bg);
  border-radius: 0 0 var(--radius) var(--radius);
  position: relative;
}
.term-body .xterm { height: 100%; padding: 4px 8px; }

/* Meters */
.meters { display: grid; grid-template-columns: 1fr 1fr; gap: 12px; }
.meter .label { font-size: .75rem; color: var(--muted); text-transform: uppercase; letter-spacing: .05em; }
.meter .value { font-family: Georgia, serif; font-size: 1.25rem; }
.meter-row { display: flex; justify-content: space-between; align-items: baseline; margin-bottom: 4px; }
.meter-bar { height: 4px; background: var(--surface-2); border-radius: 2px; overflow: hidden; }
.meter-fill { height: 100%; background: linear-gradient(90deg, var(--accent), var(--accent-2)); border-radius: 2px; width: 0%; transition: width 0.5s; }
.net-meter { grid-column: span 2; }

/* Buttons */
button, .tiny { transition: all 0.15s; }
button:hover { filter: brightness(0.92); }
button:active { transform: scale(0.97); }
button.tiny { background: var(--surface-2); border: 1px solid var(--border); border-radius: 8px; padding: 6px 10px; cursor: pointer; font-size: .85rem; }
button.tiny:hover { background: var(--border); }

/* Unlock */
.unlock { max-width: 360px; margin: 12vh auto; }
.unlock input, .unlock button { width: 100%; padding: 12px; border-radius: 8px; border: 1px solid var(--border); font-size: 1rem; margin-top: 8px; }
.unlock button { background: var(--accent); color: #fff; border: none; cursor: pointer; }
.unlock button:hover { background: var(--accent-2); }

/* Capture */
.warn { background: #FBEFE7; color: var(--accent); padding: 8px; border-radius: 8px; font-size: .85rem; }
.cap-list { max-height: 260px; overflow: auto; }
.cap-row { padding: 8px; border-bottom: 1px solid var(--border); cursor: pointer; font-size: .85rem; transition: background 0.15s; }
.cap-row:hover { background: var(--surface-2); }
.cap-row .meta { color: var(--muted); }
.cap-detail { white-space: pre-wrap; font-family: ui-monospace, monospace; font-size: .8rem; max-height: 240px; overflow: auto; background: var(--surface-2); padding: 8px; border-radius: 8px; }

/* PTY exit overlay */
.pty-overlay {
  position: absolute;
  inset: 0;
  background: rgba(244,241,234,0.92);
  display: flex;
  flex-direction: column;
  align-items: center;
  justify-content: center;
  z-index: 10;
  border-radius: var(--radius);
}
.pty-overlay h3 { font-size: 1.1rem; color: var(--ink); margin-bottom: 8px; }
.pty-overlay .exit-code { color: var(--muted); font-size: .85rem; margin-bottom: 16px; }
.pty-overlay button { background: var(--accent); color: #fff; border: none; padding: 10px 24px; border-radius: 8px; cursor: pointer; font-size: .9rem; }
.pty-overlay button:hover { background: var(--accent-2); }

/* Scrollbar */
::-webkit-scrollbar { width: 6px; }
::-webkit-scrollbar-track { background: transparent; }
::-webkit-scrollbar-thumb { background: var(--border); border-radius: 3px; }
::-webkit-scrollbar-thumb:hover { background: var(--muted); }
```

- [ ] **Step 3: 修改 terminal.js 的 mountTerminal 让 xterm 挂载到 .term-body**

在 Task 4 中 terminal.js 已经改过，需要确保 `term.open()` 挂载到 `.term-body` 而不是旧的 `.term-wrap`:

```js
const container = document.querySelector(".term-body");
term.open(container);
```

同时 `showExitOverlay` 中的 `container` 也要改为 `.term-body` (已有 position:relative).

- [ ] **Step 4: 修改 metrics.js 更新进度条和状态灯**

修改 `web/src/metrics.js`:

```js
import { openWs } from "./api.js";

export function mountMetrics() {
  let prev = null;
  openWs("/ws/metrics", (m) => {
    const cpuEl = document.getElementById("cpu");
    const memEl = document.getElementById("mem");
    const netEl = document.getElementById("net");
    const cpuBar = document.getElementById("cpu-bar");
    const memBar = document.getElementById("mem-bar");
    const statusDot = document.getElementById("session-status");

    let cpu = 0;
    if (prev) cpu = ((m.cpu.usageUsec - prev.cpu.usageUsec) / 1e6) / ((m.ts - prev.ts) / 1000) * 100;
    prev = m;
    const memPct = m.mem.max === Infinity ? 0 : (m.mem.current / m.mem.max) * 100;

    if (cpuEl) cpuEl.textContent = cpu.toFixed(1) + "%";
    if (memEl) memEl.textContent = (m.mem.current / 1048576).toFixed(0) + " MB" + (m.mem.max !== Infinity ? ` / ${(m.mem.max / 1048576).toFixed(0)}` : "");
    if (netEl) netEl.textContent = `${(m.net.rxBytes / 1048576).toFixed(1)}↓ ${(m.net.txBytes / 1048576).toFixed(1)}↑ MB`;

    if (cpuBar) cpuBar.style.width = Math.min(cpu, 100) + "%";
    if (memBar) memBar.style.width = memPct + "%";
    if (statusDot) statusDot.classList.toggle("dead", !m.alive);
  });
}
```

- [ ] **Step 5: Commit**

```bash
git add web/src/main.js web/src/styles.css web/src/metrics.js web/src/terminal.js
git commit -m "feat: add topbar, progress bars, terminal titlebar, sidebar toggle, and button polish"
```

---

### Task 6: 构建验证 + 重建镜像

**Files:**
- 无代码改动，仅验证

- [ ] **Step 1: 构建 web dist**

```bash
cd web && npm run build
```

Expected: `web/dist/` 生成，无错误

- [ ] **Step 2: 运行 server 单元测试**

```bash
cd server && npm test
```

Expected: 所有测试通过

- [ ] **Step 3: 用 --no-cache 重建 Docker 镜像（清除旧坏缓存）**

```bash
docker compose build --no-cache
```

Expected: 安装步骤不再 CACHED，claude 二进制验证通过

- [ ] **Step 4: 启动容器验证终端可用**

```bash
docker compose up -d && docker compose exec claude bash -lc 'which claude'
```

Expected: `/home/claude/.local/bin/claude`

- [ ] **Step 5: 浏览器验证 UI**

打开 http://localhost:8080:
- 顶栏显示 "Claude Code" + 状态灯
- 终端有标题栏 + Claude 风格 xterm 主题
- 指标区有进度条
- 侧栏可折叠
- PTY 退出时显示覆盖层 + 重启按钮
