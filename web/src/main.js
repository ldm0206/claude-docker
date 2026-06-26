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
