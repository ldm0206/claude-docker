import { execSync } from "node:child_process";
import { existsSync, mkdirSync, readFileSync, writeFileSync } from "node:fs";
import http from "node:http";
import https from "node:https";
import { Proxy } from "http-mitm-proxy";
import selfsigned from "selfsigned";

// --- Constants ---
const CA_KEY = "/home/claude/.claude/debug-proxy-ca.key";
const CA_CERT_PEM = "/home/claude/.claude/debug-proxy-ca.crt";
const SYSTEM_CERT = "/usr/local/share/ca-certificates/debug-proxy.crt";
const MITM_CERTS_DIR = "/home/claude/.claude/mitm-certs";
const FIXED_PORT = 8888;
const LISTEN_HOST = "127.0.0.1";

/**
 * Load an existing CA from disk, or generate a new one with selfsigned.
 * All file I/O is wrapped in try/catch — the paths may not be writable
 * outside the container (e.g. on a Windows test host).
 */
function loadOrCreateCA() {
  try {
    if (existsSync(CA_CERT_PEM) && existsSync(CA_KEY)) {
      return {
        key: readFileSync(CA_KEY, "utf8"),
        cert: readFileSync(CA_CERT_PEM, "utf8"),
      };
    }
  } catch {
    // fall through to generate
  }

  const pem = selfsigned.generate(
    [{ name: "commonName", value: "claude-docker-debug-proxy" }],
    { keySize: 2048, days: 3650 },
  );

  try {
    mkdirSync("/home/claude/.claude", { recursive: true });
    writeFileSync(CA_KEY, pem.private);
    writeFileSync(CA_CERT_PEM, pem.cert);
  } catch {
    // directory may not be writable outside the container — best-effort
  }

  return { key: pem.private, cert: pem.cert };
}

/**
 * Install the CA cert into the container's system trust store.
 * Returns the caEnv object for child processes, or {} on failure.
 * Both the file write and update-ca-certificates are wrapped in try/catch
 * because they do not exist on a Windows test host.
 */
function installCaToContainer(certPem) {
  try {
    writeFileSync(SYSTEM_CERT, certPem);
    execSync("update-ca-certificates", { stdio: "pipe" });
    return {
      NODE_EXTRA_CA_CERTS: CA_CERT_PEM,
      SSL_CERT_FILE: "/etc/ssl/certs/ca-certificates.crt",
      REQUESTS_CA_BUNDLE: "/etc/ssl/certs/ca-certificates.crt",
    };
  } catch {
    // not in a Debian/Ubuntu container — CA install is best-effort
    return {};
  }
}

/**
 * Create a custom https.Agent that tunnels through an upstream HTTP proxy.
 * This avoids needing the `https-proxy-agent` npm package.
 * Only supports http:// upstream proxies — socks5:// is explicitly NOT supported.
 */
function createUpstreamAgent(upstreamUrl, isSSL) {
  const parsed = new URL(upstreamUrl);
  const upstreamHost = parsed.hostname;
  const upstreamPort = parseInt(parsed.port, 10) || (parsed.protocol === "https:" ? 443 : 80);

  const AgentClass = isSSL ? https.Agent : http.Agent;

  function createConnection(options, callback) {
    // For HTTPS targets: first CONNECT through the upstream proxy, then TLS-handshake
    if (isSSL) {
      const connectReq = http.request({
        host: upstreamHost,
        port: upstreamPort,
        method: "CONNECT",
        path: `${options.host}:${options.port || 443}`,
        headers: { Host: `${options.host}:${options.port || 443}` },
      });

      connectReq.on("connect", (res, socket) => {
        if (res.statusCode !== 200) {
          socket.destroy();
          const err = new Error(`Upstream proxy CONNECT failed: ${res.statusCode}`);
          return callback(err);
        }
        // Now TLS-handshake over the tunnelled socket
        const tlsOpts = { ...options, socket, servername: options.servername || options.host };
        const tlsSocket = require("node:tls").connect(tlsOpts, () => {
          callback(null, tlsSocket);
        });
        tlsSocket.on("error", (e) => callback(e));
      });

      connectReq.on("error", (e) => callback(e));
      connectReq.end();
    } else {
      // For plain HTTP: just connect to the upstream proxy directly
      const socket = http.request({
        host: upstreamHost,
        port: upstreamPort,
        method: "CONNECT",
        path: `${options.host}:${options.port || 80}`,
      });
      socket.on("connect", (res, tcpSocket) => {
        if (res.statusCode !== 200) {
          tcpSocket.destroy();
          return callback(new Error(`Upstream proxy CONNECT failed: ${res.statusCode}`));
        }
        callback(null, tcpSocket);
      });
      socket.on("error", (e) => callback(e));
      socket.end();
    }
  }

  return new AgentClass({ createConnection });
}

/**
 * Create the debug MITM proxy.
 *
 * @param {object} opts
 * @param {object} opts.store  - captureStore instance (has .add())
 * @param {string} [opts.upstreamProxy] - http:// URL of an upstream proxy (socks5:// is NOT supported)
 * @returns {object}
 */
export function createDebugProxy({ store, upstreamProxy }) {
  let proxy = null;
  let up = false;
  let recording = false;
  let cachedCaEnv = {};

  // SOCKS upstream is NOT supported — http-mitm-proxy uses http/https agents
  // and we cannot chain through a SOCKS proxy without a dedicated agent.
  const isSocksUpstream = upstreamProxy && /^socks/i.test(upstreamProxy);
  const effectiveUpstream = isSocksUpstream ? null : (upstreamProxy || null);

  return {
    /**
     * Start the MITM proxy. Idempotent and NON-THROWING (best-effort).
     * - Generates/loads a container-local CA
     * - Installs CA into system trust store (best-effort)
     * - Starts listening on 127.0.0.1:8888
     */
    async start() {
      if (up) return; // already running

      // Step 1: CA generation/load
      let ca;
      try {
        ca = loadOrCreateCA();
      } catch {
        // If we can't even generate a CA, we can't MITM
        up = false;
        return;
      }

      // Step 2: Install CA into container trust store (best-effort)
      try {
        cachedCaEnv = installCaToContainer(ca.cert);
      } catch {
        cachedCaEnv = {};
      }

      // Step 3: Create and start the proxy
      try {
        proxy = new Proxy();

        // Configure upstream proxy chaining if provided and HTTP-based
        if (effectiveUpstream) {
          try {
            proxy.options = proxy.options || {};
            proxy.options.httpAgent = createUpstreamAgent(effectiveUpstream, false);
            proxy.options.httpsAgent = createUpstreamAgent(effectiveUpstream, true);
          } catch {
            // upstream agent creation failed — proxy will try direct
          }
        }

        // onRequest handler: always forward; record only when `recording` is true
        proxy.onRequest((ctx, callback) => {
          const started = Date.now();
          const chunks = { req: [], res: [] };
          const host = ctx.clientToProxyRequest.headers.host || ctx.proxyToServerRequestOptions?.host || "unknown";
          const method = ctx.clientToProxyRequest.method;
          const url = ctx.clientToProxyRequest.url;

          if (recording) {
            ctx.onRequestData((d, next) => { chunks.req.push(d); return next(null, d); });
            ctx.onResponseData((d, next) => { chunks.res.push(d); return next(null, d); });
            ctx.onResponseEnd((cb) => {
              const reqBody = Buffer.concat(chunks.req).toString("utf8");
              const resBody = Buffer.concat(chunks.res).toString("utf8");
              store.add({
                ts: started,
                method,
                host,
                path: url,
                status: ctx.serverToProxyResponse?.statusCode,
                latencyMs: Date.now() - started,
                reqHeaders: { ...ctx.clientToProxyRequest.headers },
                reqBody,
                resHeaders: { ...(ctx.serverToProxyResponse?.headers || {}) },
                resBody,
              });
              cb();
            });
          }

          callback();
        });

        // Listen on fixed port — returns this; callback fires when ready
        await new Promise((resolve) => {
          proxy.listen(
            { host: LISTEN_HOST, port: FIXED_PORT, sslCaDir: MITM_CERTS_DIR },
            (err) => {
              if (err) {
                // listen failed — e.g. port in use or permissions
                up = false;
                proxy = null;
              } else {
                up = true;
              }
              resolve(); // always resolve — never reject (non-throwing)
            },
          );
        });
      } catch {
        up = false;
        proxy = null;
      }
    },

    /** Toggle recording on/off. Forwarding always happens when up. */
    setRecording(val) { recording = !!val; },
    isRecording() { return recording; },

    /** Did the proxy listen succeed? */
    isUp() { return up; },

    /** Env vars for child processes to trust the MITM CA. Empty {} if CA not installed. */
    caEnv() { return cachedCaEnv; },

    /** The proxy URL for HTTP_PROXY/HTTPS_PROXY. null if not up. */
    proxyUrl() { return up ? `http://${LISTEN_HOST}:${FIXED_PORT}` : null; },

    /** Stop the proxy. Idempotent, non-throwing. */
    async stop() {
      if (proxy) {
        try { proxy.close(); } catch { /* best-effort */ }
        proxy = null;
      }
      up = false;
    },
  };
}
