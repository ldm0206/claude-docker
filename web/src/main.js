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
      <section class="panel" id="term-section">
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
    window.dispatchEvent(new Event("resize"));
  };
}
boot();
