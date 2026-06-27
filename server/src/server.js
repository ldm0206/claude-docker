import Fastify from "fastify";
import cookie from "@fastify/cookie";
import fastifyStatic from "@fastify/static";
import { WebSocketServer } from "ws";
import { existsSync } from "node:fs";
import { readFileSync } from "node:fs";
import path from "node:path";
import { timingSafeEqualStr, signSession, verifySession } from "./auth.js";
import { requireAuth } from "./auth-middleware.js";
import {
  readCgroupCpu, readCgroupMemory, readNetDev,
} from "./metrics.js";
import { createCaptureStore } from "./capture-store.js";
import { createPtyManager } from "./pty-manager.js";
import { createDebugProxy } from "./debug-proxy.js";
import { loadConfig } from "./config.js";

const read = (p) => { try { return readFileSync(p, "utf8"); } catch { return null; } };

export async function buildServer({ config, sessionSecret, port = 8080 }) {
  const captureStore = createCaptureStore({ knownSecrets: [config.anthropicApiKey, config.anthropicAuthToken].filter(Boolean) });
  let captureOn = false;

  const debugProxy = createDebugProxy({
    store: captureStore,
    upstreamProxy: config.allProxy || config.httpsProxy || config.httpProxy,
  });

  const pty = createPtyManager({
    cwd: "/workspace",
    // LAZY env: resolved fresh inside start() on every (re)start, so a
    // capture toggle + /api/session/restart re-evaluates buildClaudeEnv and
    // picks up the current debugProxy.isUp() routing.
    env: () => buildClaudeEnv(config),
    command: "claude",
    args: [],
  });

  const fastify = Fastify();
  await fastify.register(cookie);

  // Serve built SPA if web/dist exists (no-op in tests where /app/web/dist is absent)
  const webDist = process.env.WEB_DIST || "/app/web/dist";
  if (existsSync(webDist)) {
    await fastify.register(fastifyStatic, { root: path.resolve(webDist), prefix: "/" });
  }

  // Unguarded routes
  fastify.get("/health", async () => ({ ok: true }));

  fastify.post("/auth", async (req, reply) => {
    const key = req.body?.key;
    if (typeof key !== "string" || !timingSafeEqualStr(key, config.accessKey)) {
      return reply.code(401).send({ error: "unauthorized" });
    }
    const cookie = signSession({ iat: Date.now() }, sessionSecret);
    reply.setCookie("session", cookie, {
      httpOnly: true,
      sameSite: "lax",
      path: "/",
    }).send({ ok: true });
  });

  fastify.post("/logout", async (_req, reply) => {
    reply.clearCookie("session", { path: "/" }).send({ ok: true });
  });

  // Guarded routes — each has its own preHandler
  fastify.get("/api/state", { preHandler: [requireAuth(sessionSecret)] }, async () => ({
    captureOn, sessionAlive: pty.alive,
  }));
  fastify.post("/api/capture/enable", { preHandler: [requireAuth(sessionSecret)] }, async (_req, reply) => {
    await debugProxy.start();
    debugProxy.setRecording(true);
    captureOn = true;
    reply.send({ captureOn: true, captureUp: debugProxy.isUp() });
  });
  fastify.post("/api/capture/disable", { preHandler: [requireAuth(sessionSecret)] }, async (_req, reply) => {
    debugProxy.setRecording(false);
    captureOn = false;
    reply.send({ captureOn: false });
  });
  fastify.post("/api/captures/clear", { preHandler: [requireAuth(sessionSecret)] }, async (_req, reply) => {
    captureStore.clear(); reply.send({ ok: true });
  });
  fastify.get("/api/captures", { preHandler: [requireAuth(sessionSecret)] }, async () => captureStore.list());
  let restartInProgress = false;

  fastify.post("/api/session/restart", { preHandler: [requireAuth(sessionSecret)] }, async (_req, reply) => {
    restartInProgress = true;
    pty.kill(); pty.start();
    restartInProgress = false;
    reply.send({ ok: true });
  });

  await fastify.listen({ port, host: "0.0.0.0" });
  const actualPort = fastify.server.address().port;

  // MITM proxy is started LAZILY — only by /api/capture/enable. It must NOT
  // start at boot: that would bind 127.0.0.1:8888 and mutate the trust store
  // during unit tests and contradict the "off by default" capture posture.
  // The user flow is: enable capture (starts MITM) → restart session (PTY
  // re-spawns with env now routing through the MITM).

  const wss = new WebSocketServer({ noServer: true });
  fastify.server.on("upgrade", (req, socket, head) => {
    const url = new URL(req.url, "http://x");
    const cookieVal = parseCookie(req.headers.cookie || "")["session"];
    // Origin check: compare the Origin's HOST only (allow either scheme) so
    // TLS-fronted deployments (Caddy/nginx/Cloudflare → https://) are not
    // rejected. The authoritative auth gate is verifySession below.
    if (!cookieVal || !originHostMatches(req.headers.origin, req.headers.host)) {
      socket.destroy(); return;
    }
    if (!verifySession(cookieVal, sessionSecret)) {
      socket.destroy(); return;
    }
    wss.handleUpgrade(req, socket, head, (ws) => {
      wss.emit("connection", ws, url.pathname);
    });
  });

  // terminal ws
  wss.on("connection", (ws, pathname) => {
    if (pathname === "/ws/terminal") {
      if (!pty.alive) pty.start();
      const unsubData = pty.onData((d) => ws.readyState === ws.OPEN && ws.send(d));
      const unsubExit = pty.onExit((exitCode) => {
        if (!restartInProgress && ws.readyState === ws.OPEN) ws.send(JSON.stringify({ type: "pty-exit", exitCode }));
      });
      ws.on("message", (raw) => {
        const msg = JSON.parse(raw.toString());
        if (msg.type === "resize") pty.resize(msg.cols, msg.rows);
        else if (msg.type === "input") pty.write(msg.data);
      });
      ws.on("close", () => { unsubData(); unsubExit(); });
    } else if (pathname === "/ws/captures") {
      const unsub = captureStore.subscribe((r) => ws.readyState === ws.OPEN && ws.send(JSON.stringify(r)));
      ws.send(JSON.stringify(captureStore.list()));
      ws.on("close", unsub);
    } else if (pathname === "/ws/metrics") {
      const id = setInterval(() => ws.send(JSON.stringify(snapshot())), 1500);
      ws.on("close", () => clearInterval(id));
    }
  });

  function snapshot() {
    const cpu = readCgroupCpu(read);
    const mem = readCgroupMemory(read);
    const net = readNetDev(read);
    return { cpu, mem, net, captureOn, alive: pty.alive, ts: Date.now() };
  }

  function buildClaudeEnv(cfg) {
    const CLAUDE_BIN = "/home/claude/.local/bin";
    const env = {
      ...process.env,
      HOME: "/home/claude",
      PATH: `${CLAUDE_BIN}:${process.env.PATH}`,
      CLAUDE_CONFIG_DIR: cfg.CLAUDE_CONFIG_DIR || process.env.CLAUDE_CONFIG_DIR,
      ...(cfg.anthropicApiKey ? { ANTHROPIC_API_KEY: cfg.anthropicApiKey } : {}),
      ...(cfg.anthropicAuthToken ? { ANTHROPIC_AUTH_TOKEN: cfg.anthropicAuthToken } : {}),
      ...(cfg.anthropicBaseUrl ? { ANTHROPIC_BASE_URL: cfg.anthropicBaseUrl } : {}),
      ...(cfg.httpProxy ? { HTTP_PROXY: cfg.httpProxy, http_proxy: cfg.httpProxy } : {}),
      ...(cfg.httpsProxy ? { HTTPS_PROXY: cfg.httpsProxy, https_proxy: cfg.httpsProxy } : {}),
      ...(cfg.allProxy ? { ALL_PROXY: cfg.allProxy, all_proxy: cfg.allProxy } : {}),
      NO_PROXY: cfg.noProxy, no_proxy: cfg.noProxy,
      API_TIMEOUT_MS: String(cfg.apiTimeoutMs),
    };

    // If the MITM proxy is up, override claude's proxy env to route through it
    // so we can capture HTTPS traffic. Remove ALL_PROXY to prevent claude from
    // preferring SOCKS (which the MITM cannot chain through).
    if (debugProxy.isUp()) {
      const proxyUrl = debugProxy.proxyUrl();
      env.HTTP_PROXY = proxyUrl;
      env.http_proxy = proxyUrl;
      env.HTTPS_PROXY = proxyUrl;
      env.https_proxy = proxyUrl;
      delete env.ALL_PROXY;
      delete env.all_proxy;
      Object.assign(env, debugProxy.caEnv());
    }

    return env;
  }

  return {
    fastify,
    port: actualPort,
    captureStore,
    pty,
    setCaptureOn: (v) => { captureOn = v; },
    getCaptureOn: () => captureOn,
    close: async () => { await debugProxy.stop(); pty.kill(); wss.close(); await fastify.close(); },
  };
}

function parseCookie(header) {
  const out = {};
  for (const part of header.split(";")) {
    const [k, ...rest] = part.trim().split("=");
    if (k) out[k] = decodeURIComponent(rest.join("="));
  }
  return out;
}

/**
 * Validate the WS Origin header against req host. Accepts either http/https
 * scheme (TLS-terminated frontends send https://). Absent Origin is allowed —
 * same-origin browser WS always sends Origin, but the real auth gate is the
 * signed session cookie (verifySession), not Origin.
 */
function originHostMatches(origin, host) {
  if (!origin) return true;
  try {
    const originHost = new URL(origin).host;
    return originHost === host;
  } catch {
    return false;
  }
}

// --- Main entrypoint (container: `node src/server.js`) ---
const isMain = import.meta.url === `file://${process.argv[1]}`;
if (isMain) {
  const config = loadConfig();
  const sessionSecret = process.env.SESSION_SECRET;
  if (!sessionSecret) {
    console.error("SESSION_SECRET environment variable is required");
    process.exit(1);
  }
  const port = Number(process.env.PORT) || 8080;
  buildServer({ config, sessionSecret, port })
    .then((srv) => {
      console.log(`[server] listening on :${srv.port}`);
    })
    .catch((e) => {
      console.error("[server] failed to start:", e);
      process.exit(1);
    });
}
