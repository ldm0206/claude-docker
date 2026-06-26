import { existsSync, mkdirSync, readFileSync, writeFileSync } from "node:fs";
import { join } from "node:path";
import http from "node:http";
import https from "node:https";
import tls from "node:tls";
import { pki as forgePki } from "node-forge";
import { Proxy } from "http-mitm-proxy";

// --- Constants ---
// CA generation + trust-store install is the ENTRYPOINT's job (it runs as
// root). At runtime we only LOAD the pre-generated CA from the env-provided
// paths. Outside the container (tests) no CA is available — caEnv() is {}.
const ENV_CA_CERT = process.env.CLAUDE_DEBUG_CA_CERT || null; // e.g. /etc/claude-debug/ca.crt
const ENV_CA_KEY = process.env.CLAUDE_DEBUG_CA_KEY || null;  // e.g. /etc/claude-debug/ca.key
const ENV_SSL_CA_DIR = process.env.CLAUDE_DEBUG_SSL_CA_DIR || null; // http-mitm-proxy sslCaDir
const SYSTEM_BUNDLE = "/etc/ssl/certs/ca-certificates.crt";
const FIXED_PORT = 8888;
const LISTEN_HOST = "127.0.0.1";

/**
 * Load the CA keypair that the entrypoint generated (as root). The same CA
 * is installed into the system trust store by the entrypoint, so the leaf
 * certs http-mitm-proxy signs with it are trusted by claude.
 *
 * We materialize the keypair into the http-mitm-proxy sslCaDir layout
 * (<dir>/certs/ca.pem, <dir>/keys/ca.private.key, ca.public.key) so that
 * http-mitm-proxy's CA loader (loadCA) reads OUR CA instead of generating
 * its own.
 *
 * If no env CA is present (tests / non-container), returns null and the proxy
 * will fall back to http-mitm-proxy's default sslCaDir (auto-generate).
 */
function loadCaKeypair() {
  if (!ENV_CA_CERT || !ENV_CA_KEY) return null;
  if (!existsSync(ENV_CA_CERT) || !existsSync(ENV_CA_KEY)) return null;
  try {
    return {
      cert: readFileSync(ENV_CA_CERT, "utf8"),
      key: readFileSync(ENV_CA_KEY, "utf8"),
    };
  } catch {
    return null;
  }
}

/**
 * Write our CA keypair into http-mitm-proxy's sslCaDir layout so its loadCA
 * path consumes our (already-trusted) CA rather than generating a fresh one.
 * Best-effort; safe no-op outside the container.
 */
function seedSslCaDir(sslCaDir, ca) {
  if (!sslCaDir || !ca) return;
  try {
    const certsDir = join(sslCaDir, "certs");
    const keysDir = join(sslCaDir, "keys");
    mkdirSync(certsDir, { recursive: true });
    mkdirSync(keysDir, { recursive: true });
    writeFileSync(join(certsDir, "ca.pem"), ca.cert);
    writeFileSync(join(keysDir, "ca.private.key"), ca.key);

    // Derive the public key FROM THE CERT, never from the private key. forge's
    // privateKeyFromPem does NOT populate .publicKey, so publicKeyToPem on it
    // throws and a buggy catch would write the PRIVATE-key PEM here — which
    // then makes http-mitm-proxy's loadCA throw at publicKeyFromPem() and the
    // proxy never listens. Deriving from the cert always yields a valid
    // "-----BEGIN PUBLIC KEY-----" PEM. If a valid public key already exists
    // (e.g. the entrypoint wrote one via `openssl rsa -pubout`), keep it.
    const pubKeyPath = join(keysDir, "ca.public.key");
    let needPub = true;
    try {
      if (existsSync(pubKeyPath)) {
        forgePki.publicKeyFromPem(readFileSync(pubKeyPath, "utf8")); // throws if invalid
        needPub = false;
      }
    } catch {
      needPub = true; // existing file is bad/empty/missing — re-derive
    }
    if (needPub) {
      const certObj = forgePki.certificateFromPem(ca.cert);
      writeFileSync(pubKeyPath, forgePki.publicKeyToPem(certObj.publicKey));
    }
  } catch {
    // best-effort
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
        const tlsSocket = tls.connect(tlsOpts, () => {
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
     * - Loads the CA generated by the entrypoint (root) from CLAUDE_DEBUG_CA_*
     *   env vars, seeding http-mitm-proxy's sslCaDir so it signs leaf certs
     *   with the SAME CA installed into the trust store. No trust-store
     *   mutation happens at runtime (that needs root).
     * - Starts listening on 127.0.0.1:8888
     */
    async start() {
      if (up) return; // already running

      // Step 1: Load pre-generated CA (if env present). Seeds sslCaDir so
      // http-mitm-proxy consumes our trusted CA instead of auto-generating.
      let ca = null;
      try {
        ca = loadCaKeypair();
        if (ca && ENV_SSL_CA_DIR) seedSslCaDir(ENV_SSL_CA_DIR, ca);
      } catch {
        ca = null;
      }

      // Step 2: caEnv — tells child processes which CA to trust. Empty {} when
      // no env CA is available (tests / non-container).
      cachedCaEnv = ca && ENV_CA_CERT
        ? {
            NODE_EXTRA_CA_CERTS: ENV_CA_CERT,
            SSL_CERT_FILE: SYSTEM_BUNDLE,
            REQUESTS_CA_BUNDLE: SYSTEM_BUNDLE,
          }
        : {};

      // Step 3: Create and start the proxy
      try {
        proxy = new Proxy();

        // Configure upstream proxy chaining if provided and HTTP-based
        let httpAgent, httpsAgent;
        if (effectiveUpstream) {
          try {
            httpAgent = createUpstreamAgent(effectiveUpstream, false);
            httpsAgent = createUpstreamAgent(effectiveUpstream, true);
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
            ctx.onRequestData((ctx, chunk, callback) => { chunks.req.push(chunk); return callback(null, chunk); });
            ctx.onResponseData((ctx, chunk, callback) => { chunks.res.push(chunk); return callback(null, chunk); });
            ctx.onResponseEnd((ctx, callback) => {
              const reqBody = Buffer.concat(chunks.req).toString("utf8");
              const resBody = Buffer.concat(chunks.res).toString("utf8");
              store.add({
                ts: started, method, host, path: url,
                status: ctx.serverToProxyResponse?.statusCode,
                latencyMs: Date.now() - started,
                reqHeaders: { ...(ctx.clientToProxyRequest?.headers || {}) },
                reqBody,
                resHeaders: { ...(ctx.serverToProxyResponse?.headers || {}) },
                resBody,
              });
              callback();
            });
          }

          callback();
        });

        // Listen on fixed port — returns this; callback fires when ready.
        // sslCaDir is seeded (above) with our trusted CA when env CA is set;
        // otherwise http-mitm-proxy auto-generates its own there.
        const sslCaDir = ENV_SSL_CA_DIR || "/tmp/claude-debug-mitm-certs";
        await new Promise((resolve) => {
          proxy.listen(
            { host: LISTEN_HOST, port: FIXED_PORT, sslCaDir, httpAgent, httpsAgent },
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
