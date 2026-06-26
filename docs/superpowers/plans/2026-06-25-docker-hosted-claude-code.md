# Docker-Hosted Claude Code Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** A single Docker container that runs Claude Code in an isolated, unrestricted environment, exposed through a Claude-styled web UI with access-key auth, live resource metrics, configurable proxy, and an opt-in full request/response debug capture.

**Architecture:** One Debian-based container runs a Node control panel (Fastify + `ws` + `node-pty`) as the only HTTP entry point. The panel spawns the Claude Code process in a PTY, collects cgroup/network metrics, and optionally runs a loopback MITM debug proxy with a container-local CA. A static SPA (Vite + xterm.js) provides the terminal, metrics, and capture list.

**Tech Stack:** Node.js 22, Fastify, `ws`, `node-pty`, `http-mitm-proxy`, `selfsigned`, `vitest`; Vite + xterm.js for the SPA; Debian `bookworm-slim` base; native Claude Code installer.

## Global Constraints

- Base image: `debian:bookworm-slim`. Install Node.js 22 (NodeSource). Packages: `git ripgrep curl ca-certificates jq tini`.
- Claude Code installed via native installer (`curl -fsSL https://claude.ai/install.sh | bash`); set `DISABLE_AUTOUPDATER=1` and `DISABLE_UPDATES=1`.
- Container runs as non-root user `claude` (uid 1000), home `/home/claude`.
- Single exposed port: `8080/tcp`. HTTP only — TLS is a front proxy's job.
- Two Docker named volumes: `claude-workspace` → `/workspace`, `claude-config` → `/home/claude/.claude` (`CLAUDE_CONFIG_DIR=/home/claude/.claude`).
- No `--privileged`, no Docker socket mounted, no root process.
- Access secret env var name: `ACCESS_KEY`. Auth credential env vars: `ANTHROPIC_API_KEY` or `ANTHROPIC_AUTH_TOKEN`. Proxy env vars: `HTTP_PROXY` / `HTTPS_PROXY` / `ALL_PROXY` (support `http://` and `socks5://`) / `NO_PROXY`.
- Debug capture is **off by default**; secrets redacted before storage; debug proxy binds loopback only; container-local CA is trusted **only inside the container**.
- Secret redaction must scrub: `x-api-key`, `authorization`, `anthropic-*`, `cookie`, `set-cookie`, `proxy-authorization`, and any string matching `sk-ant-...`, `Bearer ...`, and `ANTHROPIC_API_KEY`/`ANTHROPIC_AUTH_TOKEN` values present in process env.
- Commit after each task. Branch off default if on a tracked repo; this directory is not currently a git repo, so initialize one in Task 1.

---

## File Structure

```
claude-docker/
├── Dockerfile
├── .dockerignore
├── .env.example
├── docker-compose.yml
├── README.md
├── entrypoint.sh                          # container init: prep trust store, drop to node
├── server/
│   ├── package.json
│   ├── vitest.config.js
│   ├── src/
│   │   ├── config.js                      # env parsing + validation
│   │   ├── redact.js                      # secret redaction (pure)
│   │   ├── auth.js                        # signed cookie + access key compare
│   │   ├── auth-middleware.js             # fastify route guard
│   │   ├── metrics.js                     # cgroup v2 + /proc/net/dev -> deltas
│   │   ├── capture-store.js               # in-memory ring of capture records
│   │   ├── debug-proxy.js                 # MITM proxy + container-local CA
│   │   ├── pty-manager.js                 # node-pty spawn of claude
│   │   └── server.js                      # fastify + ws wiring, entrypoint
│   └── test/
│       ├── config.test.js
│       ├── redact.test.js
│       ├── auth.test.js
│       ├── metrics.test.js
│       ├── capture-store.test.js
│       └── server.test.js
├── web/
│   ├── package.json
│   ├── vite.config.js
│   ├── index.html
│   └── src/
│       ├── main.js
│       ├── styles.css
│       ├── unlock.js
│       ├── terminal.js
│       ├── metrics.js
│       └── captures.js
```

Each server module has one responsibility and is unit-testable in isolation. The SPA is split by concern (unlock, terminal, metrics, captures) rather than a framework.

---

### Task 1: Repo scaffold + base Dockerfile

**Files:**
- Create: `.gitignore`
- Create: `server/package.json`
- Create: `web/package.json`
- Create: `Dockerfile` (base layers only)
- Create: `.dockerignore`
- Modify: none

**Interfaces:**
- Consumes: none.
- Produces: a git repo, two npm package roots (`server/`, `web/`), and a buildable base image. Later tasks add to `server/src/*` and `web/src/*`.

- [ ] **Step 1: Initialize git repo**

Run:
```bash
cd /workspace
git init -b main
git config user.email "builder@example.com"
git config user.name "builder"
```
Expected: `Initialized empty Git repository`.

- [ ] **Step 2: Create `.gitignore`**

```gitignore
node_modules/
dist/
.env
*.log
.DS_Store
coverage/
```

- [ ] **Step 3: Create `server/package.json`**

```json
{
  "name": "claude-docker-server",
  "version": "1.0.0",
  "private": true,
  "type": "module",
  "main": "src/server.js",
  "scripts": {
    "start": "node src/server.js",
    "test": "vitest run"
  },
  "dependencies": {
    "@fastify/cookie": "^11.0.2",
    "@fastify/static": "^8.1.0",
    "fastify": "^5.2.1",
    "http-mitm-proxy": "^1.1.0",
    "node-pty": "^1.0.0",
    "selfsigned": "^2.4.1",
    "ws": "^8.18.0"
  },
  "devDependencies": {
    "vitest": "^2.1.8"
  }
}
```

- [ ] **Step 4: Create `web/package.json`**

```json
{
  "name": "claude-docker-web",
  "version": "1.0.0",
  "private": true,
  "type": "module",
  "scripts": {
    "dev": "vite",
    "build": "vite build",
    "preview": "vite preview"
  },
  "dependencies": {
    "@xterm/addon-fit": "^0.10.0",
    "@xterm/addon-web-links": "^0.11.0",
    "xterm": "^5.3.0"
  },
  "devDependencies": {
    "vite": "^6.0.7"
  }
}
```

- [ ] **Step 5: Create `server/vitest.config.js`**

```js
import { defineConfig } from "vitest/config";

export default defineConfig({
  test: {
    environment: "node",
    include: ["test/**/*.test.js"],
  },
});
```

- [ ] **Step 6: Create `.dockerignore`**

```
node_modules
web/node_modules
web/dist
.git
.env
*.log
docs
```

- [ ] **Step 7: Create the base `Dockerfile`**

```dockerfile
FROM node:22-bookworm-slim AS base

ENV DEBIAN_FRONTEND=noninteractive \
    DISABLE_AUTOUPDATER=1 \
    DISABLE_UPDATES=1 \
    CLAUDE_CONFIG_DIR=/home/claude/.claude

RUN apt-get update && apt-get install -y --no-install-recommends \
        git ripgrep curl ca-certificates jq tini sudo \
    && rm -rf /var/lib/apt/lists/*

# Non-root user
RUN useradd -m -u 1000 -s /bin/bash claude \
    && install -d -o claude -g claude /workspace

# Claude Code native installer
RUN curl -fsSL https://claude.ai/install.sh | bash

WORKDIR /workspace
```

- [ ] **Step 8: Verify the base image builds**

Run:
```bash
docker build -t claude-docker:base .
```
Expected: build succeeds (network required to fetch the installer). If the installer URL is unreachable in the build environment, note it and continue — the final image build in Task 12 re-verifies.

- [ ] **Step 9: Commit**

```bash
git add .gitignore server/package.json server/vitest.config.js web/package.json Dockerfile .dockerignore
git commit -m "chore: scaffold repo and base Dockerfile"
```

---

### Task 2: config.js — env parsing and validation

**Files:**
- Create: `server/src/config.js`
- Test: `server/test/config.test.js`

**Interfaces:**
- Consumes: `process.env`.
- Produces: `loadConfig()` returning `{ accessKey, anthropicApiKey, anthropicAuthToken, anthropicBaseUrl, httpProxy, httpsProxy, allProxy, noProxy, apiTimeoutMs }`. Throws `Error` when `ACCESS_KEY` is unset.

- [ ] **Step 1: Write the failing test**

`server/test/config.test.js`:
```js
import { describe, it, expect, beforeEach, afterEach } from "vitest";
import { loadConfig } from "../src/config.js";

const BASE = { ...process.env };

beforeEach(() => {
  process.env = { ...BASE };
  delete process.env.ACCESS_KEY;
  delete process.env.ANTHROPIC_API_KEY;
  delete process.env.ANTHROPIC_AUTH_TOKEN;
  delete process.env.HTTP_PROXY;
  delete process.env.HTTPS_PROXY;
  delete process.env.ALL_PROXY;
  delete process.env.NO_PROXY;
  delete process.env.ANTHROPIC_BASE_URL;
  delete process.env.API_TIMEOUT_MS;
});
afterEach(() => { process.env = { ...BASE }; });

describe("loadConfig", () => {
  it("throws when ACCESS_KEY is missing", () => {
    expect(() => loadConfig()).toThrow(/ACCESS_KEY/);
  });

  it("returns accessKey and reads anthropic creds", () => {
    process.env.ACCESS_KEY = "web-secret";
    process.env.ANTHROPIC_AUTH_TOKEN = "tok";
    const cfg = loadConfig();
    expect(cfg.accessKey).toBe("web-secret");
    expect(cfg.anthropicAuthToken).toBe("tok");
    expect(cfg.anthropicApiKey).toBeUndefined();
  });

  it("parses proxy env vars", () => {
    process.env.ACCESS_KEY = "k";
    process.env.HTTP_PROXY = "http://p:8080";
    process.env.ALL_PROXY = "socks5://s:1080";
    process.env.NO_PROXY = "localhost,127.0.0.1";
    const cfg = loadConfig();
    expect(cfg.httpProxy).toBe("http://p:8080");
    expect(cfg.allProxy).toBe("socks5://s:1080");
    expect(cfg.noProxy).toBe("localhost,127.0.0.1");
  });

  it("applies a numeric default apiTimeoutMs and accepts override", () => {
    process.env.ACCESS_KEY = "k";
    expect(loadConfig().apiTimeoutMs).toBe(600000);
    process.env.API_TIMEOUT_MS = "1200000";
    expect(loadConfig().apiTimeoutMs).toBe(1200000);
  });
});
```

- [ ] **Step 2: Run test to verify it fails**

Run: `cd server && npx vitest run test/config.test.js`
Expected: FAIL — `loadConfig` is not a function (module missing).

- [ ] **Step 3: Implement `config.js`**

`server/src/config.js`:
```js
export function loadConfig(env = process.env) {
  const accessKey = env.ACCESS_KEY;
  if (!accessKey) {
    throw new Error("ACCESS_KEY environment variable is required");
  }
  const apiTimeoutMs = env.API_TIMEOUT_MS
    ? Number(env.API_TIMEOUT_MS)
    : 600000;
  if (!Number.isFinite(apiTimeoutMs) || apiTimeoutMs <= 0) {
    throw new Error("API_TIMEOUT_MS must be a positive number");
  }
  return {
    accessKey,
    anthropicApiKey: env.ANTHROPIC_API_KEY || undefined,
    anthropicAuthToken: env.ANTHROPIC_AUTH_TOKEN || undefined,
    anthropicBaseUrl: env.ANTHROPIC_BASE_URL || undefined,
    httpProxy: env.HTTP_PROXY || undefined,
    httpsProxy: env.HTTPS_PROXY || undefined,
    allProxy: env.ALL_PROXY || undefined,
    noProxy: env.NO_PROXY || "localhost,127.0.0.1",
    apiTimeoutMs,
  };
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `cd server && npx vitest run test/config.test.js`
Expected: PASS (4 tests).

- [ ] **Step 5: Commit**

```bash
git add server/src/config.js server/test/config.test.js
git commit -m "feat(server): config loader with env validation"
```

---

### Task 3: redact.js — secret redaction for captures

**Files:**
- Create: `server/src/redact.js`
- Test: `server/test/redact.test.js`

**Interfaces:**
- Consumes: nothing (pure). Optionally an array of known secret values to scrub from bodies.
- Produces:
  - `REDACT_HEADER_KEYS` (array of lowercase header names to scrub).
  - `redactHeaders(headersObj, knownSecrets)` → object with sensitive values replaced by `"[REDACTED]"`.
  - `redactBody(stringBody, knownSecrets)` → string with matching secret substrings replaced by `"[REDACTED]"`.

- [ ] **Step 1: Write the failing test**

`server/test/redact.test.js`:
```js
import { describe, it, expect } from "vitest";
import { redactHeaders, redactBody, REDACT_HEADER_KEYS } from "../src/redact.js";

describe("redact", () => {
  it("redacts sensitive header keys", () => {
    const out = redactHeaders(
      { "x-api-key": "sk-ant-xyz", "content-type": "application/json", authorization: "Bearer abc" },
      ["sk-ant-xyz"]
    );
    expect(out["x-api-key"]).toBe("[REDACTED]");
    expect(out.authorization).toBe("[REDACTED]");
    expect(out["content-type"]).toBe("application/json");
  });

  it("REDACT_HEADER_KEYS includes the required set", () => {
    for (const k of ["x-api-key", "authorization", "cookie", "set-cookie", "proxy-authorization"]) {
      expect(REDACT_HEADER_KEYS).toContain(k);
    }
    expect(REDACT_HEADER_KEYS.some((k) => k.startsWith("anthropic-"))).toBe(true);
  });

  it("redacts known secret values anywhere in a body", () => {
    const body = JSON.stringify({ key: "sk-ant-SECRET", other: "Bearer tok-123" });
    const out = redactBody(body, ["sk-ant-SECRET", "tok-123"]);
    expect(out).not.toContain("sk-ant-SECRET");
    expect(out).not.toContain("tok-123");
    expect(out).toContain("[REDACTED]");
  });

  it("redacts sk-ant- token shapes even when not pre-known", () => {
    const out = redactBody('{"a":"sk-ant-abc123XYZ"}', []);
    expect(out).not.toContain("sk-ant-abc123XYZ");
  });

  it("redacts Bearer token shapes even when not pre-known", () => {
    const out = redactBody("Authorization: Bearer eyJhb-long-jwt", []);
    expect(out).not.toContain("eyJhb-long-jwt");
  });
});
```

- [ ] **Step 2: Run test to verify it fails**

Run: `cd server && npx vitest run test/redact.test.js`
Expected: FAIL — module missing.

- [ ] **Step 3: Implement `redact.js`**

`server/src/redact.js`:
```js
export const REDACT_HEADER_KEYS = [
  "x-api-key",
  "authorization",
  "cookie",
  "set-cookie",
  "proxy-authorization",
  "anthropic-api-key",
  "anthropic-auth-token",
  "anthropic-organization-id",
];

const SK_ANT = /sk-ant-[A-Za-z0-9_\-]{6,}/g;
const BEARER = /Bearer\s+[A-Za-z0-9_\-\.=]{6,}/g;

export function redactHeaders(headers, knownSecrets = []) {
  const out = {};
  for (const [k, v] of Object.entries(headers || {})) {
    const lk = String(k).toLowerCase();
    if (REDACT_HEADER_KEYS.includes(lk) || lk.startsWith("anthropic-")) {
      out[k] = "[REDACTED]";
    } else if (knownSecrets.some((s) => s && String(v).includes(s))) {
      out[k] = "[REDACTED]";
    } else {
      out[k] = v;
    }
  }
  return out;
}

export function redactBody(body, knownSecrets = []) {
  let s = body == null ? "" : String(body);
  for (const secret of knownSecrets) {
    if (secret && secret.length >= 4) {
      s = s.split(secret).join("[REDACTED]");
    }
  }
  s = s.replace(SK_ANT, "[REDACTED]");
  s = s.replace(BEARER, "Bearer [REDACTED]");
  return s;
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `cd server && npx vitest run test/redact.test.js`
Expected: PASS (5 tests).

- [ ] **Step 5: Commit**

```bash
git add server/src/redact.js server/test/redact.test.js
git commit -m "feat(server): secret redaction for captures"
```

---

### Task 4: auth.js — access key compare and signed cookie

**Files:**
- Create: `server/src/auth.js`
- Test: `server/test/auth.test.js`

**Interfaces:**
- Consumes: Node `crypto`.
- Produces:
  - `timingSafeEqualStr(a, b)` → boolean.
  - `signSession(payload, secret)` → signed base64url cookie string `"<b64payload>.<hmac>"`.
  - `verifySession(cookie, secret)` → payload object or `null` on bad signature/format.

- [ ] **Step 1: Write the failing test**

`server/test/auth.test.js`:
```js
import { describe, it, expect } from "vitest";
import { timingSafeEqualStr, signSession, verifySession } from "../src/auth.js";

describe("auth", () => {
  it("timingSafeEqualStr returns true only on exact match", () => {
    expect(timingSafeEqualStr("abc", "abc")).toBe(true);
    expect(timingSafeEqualStr("abc", "abd")).toBe(false);
    expect(timingSafeEqualStr("abc", "abcd")).toBe(false);
    expect(timingSafeEqualStr("", "")).toBe(true);
  });

  it("sign + verify round trips a payload", () => {
    const cookie = signSession({ role: "user", iat: 1 }, "secret");
    expect(verifySession(cookie, "secret")).toEqual({ role: "user", iat: 1 });
  });

  it("verify rejects tampered or wrong-secret cookies", () => {
    const cookie = signSession({ a: 1 }, "secret");
    expect(verifySession(cookie, "other")).toBeNull();
    expect(verifySession(cookie + "x", "secret")).toBeNull();
    expect(verifySession("garbage", "secret")).toBeNull();
  });
});
```

- [ ] **Step 2: Run test to verify it fails**

Run: `cd server && npx vitest run test/auth.test.js`
Expected: FAIL — module missing.

- [ ] **Step 3: Implement `auth.js`**

`server/src/auth.js`:
```js
import crypto from "node:crypto";

export function timingSafeEqualStr(a, b) {
  const ab = Buffer.from(String(a));
  const bb = Buffer.from(String(b));
  if (ab.length !== bb.length) {
    crypto.timingSafeEqual(ab, ab); // constant-time-ish regardless of length
    return false;
  }
  return crypto.timingSafeEqual(ab, bb);
}

export function signSession(payload, secret) {
  const b64 = Buffer.from(JSON.stringify(payload)).toString("base64url");
  const mac = crypto.createHmac("sha256", secret).update(b64).digest("base64url");
  return `${b64}.${mac}`;
}

export function verifySession(cookie, secret) {
  if (typeof cookie !== "string" || !cookie.includes(".")) return null;
  const [b64, mac] = cookie.split(".");
  if (!b64 || !mac) return null;
  const expected = crypto.createHmac("sha256", secret).update(b64).digest("base64url");
  const ok = Buffer.from(mac).length === Buffer.from(expected).length
    && crypto.timingSafeEqual(Buffer.from(mac), Buffer.from(expected));
  if (!ok) return null;
  try {
    return JSON.parse(Buffer.from(b64, "base64url").toString("utf8"));
  } catch {
    return null;
  }
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `cd server && npx vitest run test/auth.test.js`
Expected: PASS (3 tests).

- [ ] **Step 5: Commit**

```bash
git add server/src/auth.js server/test/auth.test.js
git commit -m "feat(server): access key compare and signed session cookie"
```

---

### Task 5: metrics.js — cgroup v2 and network deltas

**Files:**
- Create: `server/src/metrics.js`
- Test: `server/test/metrics.test.js`
- Create fixture: `server/test/fixtures/cgroup.cpu.stat`
- Create fixture: `server/test/fixtures/cgroup.memory.current`
- Create fixture: `server/test/fixtures/memory.max`
- Create fixture: `server/test/fixtures/net.dev`

**Interfaces:**
- Consumes: a reader function `readFileSync(path)` injected for testability.
- Produces:
  - `readCgroupCpu(read)` → `{ usageUsec }` from `cpu.stat` (sum of `usage_usec`).
  - `readCgroupMemory(read)` → `{ current, max }` from `memory.current` and `memory.max`.
  - `readNetDev(read)` → `{ rxBytes, txBytes }` sum across non-loopback interfaces from `/proc/net/dev`.
  - `computeCpuPercent(prev, cur, elapsedMs)` → number (0–100*N cpus) using per-µs delta.

- [ ] **Step 1: Create fixtures**

`server/test/fixtures/cgroup.cpu.stat`:
```
usage_usec 1000000
user_usec 600000
system_usec 400000
```

`server/test/fixtures/cgroup.memory.current`:
```
524288000
```

`server/test/fixtures/memory.max`:
```
1073741824
```

`server/test/fixtures/net.dev`:
```
Inter-|   Receive                                                |  Transmit
 face |bytes    packets errs drop fifo frame compressed multicast|bytes    packets errs drop fifo colls carrier compressed
    lo: 1234      5    0    0    0     0          0         0  4321      6    0    0    0     0       0          0
  eth0: 1000000   20    0    0    0     0          0         0 2000000   25    0    0    0     0       0          0
```

- [ ] **Step 2: Write the failing test**

`server/test/metrics.test.js`:
```js
import { describe, it, expect } from "vitest";
import { readFileSync } from "node:fs";
import path from "node:path";
import {
  readCgroupCpu,
  readCgroupMemory,
  readNetDev,
  computeCpuPercent,
} from "../src/metrics.js";

const fx = (f) => path.join("test", "fixtures", f);
const read = (p) => readFileSync(p.replace("cgroup.cpu.stat", fx("cgroup.cpu.stat"))
  .replace("cgroup.memory.current", fx("cgroup.memory.current"))
  .replace("memory.max", fx("memory.max"))
  .replace("/proc/net/dev", fx("net.dev")), "utf8");

describe("metrics", () => {
  it("reads cpu usage_usec", () => {
    expect(readCgroupCpu(read).usageUsec).toBe(1000000);
  });

  it("reads memory current and max", () => {
    const m = readCgroupMemory(read);
    expect(m.current).toBe(524288000);
    expect(m.max).toBe(1073741824);
  });

  it("sums non-loopback net bytes", () => {
    const n = readNetDev(read);
    expect(n.rxBytes).toBe(1000000);
    expect(n.txBytes).toBe(2000000);
  });

  it("computes cpu percent from deltas", () => {
    // 1 cpu assumed; 0.5s of usage over 1s wall = 50%
    expect(computeCpuPercent({ usageUsec: 0 }, { usageUsec: 500000 }, 1000, 1)).toBeCloseTo(50, 5);
    // 100% when fully busy on one cpu
    expect(computeCpuPercent({ usageUsec: 0 }, { usageUsec: 1000000 }, 1000, 1)).toBeCloseTo(100, 5);
  });
});
```

- [ ] **Step 3: Run test to verify it fails**

Run: `cd server && npx vitest run test/metrics.test.js`
Expected: FAIL — module missing.

- [ ] **Step 4: Implement `metrics.js`**

`server/src/metrics.js`:
```js
export function readCgroupCpu(read) {
  const stat = read("/sys/fs/cgroup/cpu.stat");
  const m = /usage_usec\s+(\d+)/.exec(stat);
  return { usageUsec: m ? Number(m[1]) : 0 };
}

export function readCgroupMemory(read) {
  const current = Number((read("/sys/fs/cgroup/memory.current") || "0").trim());
  const maxRaw = (read("/sys/fs/cgroup/memory.max") || "max").trim();
  return { current, max: maxRaw === "max" ? Infinity : Number(maxRaw) };
}

export function readNetDev(read) {
  const text = read("/proc/net/dev");
  let rxBytes = 0, txBytes = 0;
  for (const line of text.split("\n").slice(2)) {
    const parts = line.trim().split(":");
    if (parts.length !== 2) continue;
    const iface = parts[0].trim();
    if (iface === "lo") continue;
    const nums = parts[1].trim().split(/\s+/).map(Number);
    rxBytes += nums[0] || 0;
    txBytes += nums[8] || 0;
  }
  return { rxBytes, txBytes };
}

export function computeCpuPercent(prev, cur, elapsedMs, numCpus = 1) {
  if (!elapsedMs) return 0;
  const deltaUsec = (cur.usageUsec - prev.usageUsec);
  if (deltaUsec <= 0) return 0;
  const cpuSec = deltaUsec / 1e6;
  const wallSec = elapsedMs / 1000;
  return (cpuSec / wallSec / Math.max(1, numCpus)) * 100;
}
```

- [ ] **Step 5: Run test to verify it passes**

Run: `cd server && npx vitest run test/metrics.test.js`
Expected: PASS (4 tests).

- [ ] **Step 6: Commit**

```bash
git add server/src/metrics.js server/test/metrics.test.js server/test/fixtures
git commit -m "feat(server): cgroup and network metrics parsing"
```

---

### Task 6: capture-store.js — in-memory capture ring

**Files:**
- Create: `server/src/capture-store.js`
- Test: `server/test/capture-store.test.js`

**Interfaces:**
- Consumes: `redactBody`, `redactHeaders` from Task 3.
- Produces: `createCaptureStore({ max = 500 })` returning:
  - `add(record)` where record is `{ ts, method, host, path, status, latencyMs, reqHeaders, reqBody, resHeaders, resBody }` — stores a redacted copy, assigns `id`.
  - `list()` → array of stored records (newest first).
  - `clear()` → empties the store.
  - `subscribe(fn)` / `unsubscribe(fn)` for real-time push.

- [ ] **Step 1: Write the failing test**

`server/test/capture-store.test.js`:
```js
import { describe, it, expect } from "vitest";
import { createCaptureStore } from "../src/capture-store.js";

const rec = (n) => ({
  ts: n, method: "POST", host: "api.anthropic.com", path: "/v1/messages",
  status: 200, latencyMs: n,
  reqHeaders: { "x-api-key": "sk-ant-abc" }, reqBody: '{"model":"x"}',
  resHeaders: { "content-type": "application/json" }, resBody: '{"id":"m"}',
});

describe("capture-store", () => {
  it("stores redacted records and lists newest first", () => {
    const s = createCaptureStore({ max: 3 });
    s.add(rec(1)); s.add(rec(2));
    const list = s.list();
    expect(list).toHaveLength(2);
    expect(list[0].latencyMs).toBe(2);
    expect(list[0].reqHeaders["x-api-key"]).toBe("[REDACTED]");
    expect(list[0].id).toBeTruthy();
  });

  it("caps to max and evicts oldest", () => {
    const s = createCaptureStore({ max: 2 });
    s.add(rec(1)); s.add(rec(2)); s.add(rec(3));
    expect(s.list()).toHaveLength(2);
    expect(s.list().map((r) => r.latencyMs)).toEqual([3, 2]);
  });

  it("clear empties the store", () => {
    const s = createCaptureStore();
    s.add(rec(1));
    s.clear();
    expect(s.list()).toHaveLength(0);
  });

  it("notifies subscribers with redacted records", () => {
    const s = createCaptureStore();
    const got = [];
    const unsub = s.subscribe((r) => got.push(r));
    s.add(rec(1));
    expect(got).toHaveLength(1);
    expect(got[0].reqHeaders["x-api-key"]).toBe("[REDACTED]");
    unsub();
    s.add(rec(2));
    expect(got).toHaveLength(1);
  });
});
```

- [ ] **Step 2: Run test to verify it fails**

Run: `cd server && npx vitest run test/capture-store.test.js`
Expected: FAIL — module missing.

- [ ] **Step 3: Implement `capture-store.js`**

`server/src/capture-store.js`:
```js
import { redactBody, redactHeaders } from "./redact.js";

let counter = 0;

export function createCaptureStore({ max = 500, knownSecrets = [] } = {}) {
  const records = [];
  const subs = new Set();

  function redact(rec) {
    return {
      ...rec,
      reqHeaders: redactHeaders(rec.reqHeaders || {}, knownSecrets),
      resHeaders: redactHeaders(rec.resHeaders || {}, knownSecrets),
      reqBody: redactBody(rec.reqBody, knownSecrets),
      resBody: redactBody(rec.resBody, knownSecrets),
    };
  }

  return {
    add(rec) {
      const stored = { id: ++counter, ...redact(rec) };
      records.unshift(stored);
      while (records.length > max) records.pop();
      for (const fn of subs) fn(stored);
      return stored;
    },
    list() {
      return records.slice();
    },
    clear() {
      records.length = 0;
    },
    subscribe(fn) { subs.add(fn); return () => subs.delete(fn); },
  };
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `cd server && npx vitest run test/capture-store.test.js`
Expected: PASS (4 tests).

- [ ] **Step 5: Commit**

```bash
git add server/src/capture-store.js server/test/capture-store.test.js
git commit -m "feat(server): in-memory capture ring with redaction"
```

---

### Task 7: auth-middleware.js + pty-manager.js (no unit test — integration)

`node-pty` spawns a real process and cannot be meaningfully unit-tested; this task is verified by a smoke check in Task 8 and the full integration test in Task 12.

**Files:**
- Create: `server/src/auth-middleware.js`
- Create: `server/src/pty-manager.js`
- Modify: none

**Interfaces:**
- Consumes: `verifySession` (Task 4); `node-pty`.
- Produces:
  - `requireAuth(secret)` → Fastify preHandler that reads the `session` cookie and sends 401 when invalid.
  - `createPtyManager({ cwd, env, command = "claude", args })` → `{ start(), onData(fn), write(data), resize(cols, rows), onExit(fn), kill(), alive }`.

- [ ] **Step 1: Implement `auth-middleware.js`**

`server/src/auth-middleware.js`:
```js
import { verifySession } from "./auth.js";

export function requireAuth(secret) {
  return async function (req, reply) {
    const cookie = req.cookies?.session;
    const payload = verifySession(cookie, secret);
    if (!payload) {
      reply.code(401).send({ error: "unauthorized" });
      return;
    }
    req.session = payload;
  };
}
```

- [ ] **Step 2: Implement `pty-manager.js`**

`server/src/pty-manager.js`:
```js
import { spawn as ptySpawn } from "node-pty";

export function createPtyManager({ cwd, env, command = "claude", args = [], cols = 80, rows = 24 }) {
  let proc = null;
  const dataCbs = new Set();
  const exitCbs = new Set();

  return {
    start() {
      if (proc) return;
      proc = ptySpawn(command, args, {
        name: "xterm-256color",
        cols, rows,
        cwd,
        env,
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

- [ ] **Step 3: Commit**

```bash
git add server/src/auth-middleware.js server/src/pty-manager.js
git commit -m "feat(server): auth middleware and pty manager"
```

---

### Task 8: server.js — Fastify + ws wiring (metrics, terminal, captures, control API)

This task assembles Tasks 2–7 into a running server. It is verified by an HTTP test against the auth + state endpoints.

**Files:**
- Create: `server/src/server.js`
- Test: `server/test/server.test.js`

**Interfaces:**
- Consumes: all prior modules.
- Produces: `buildServer({ config, sessionSecret })` → a started `{ fastify, port, close() }`. Exposes:
  - `POST /auth` `{ key }` → sets `session` cookie on match.
  - `POST /logout`.
  - `GET /api/state` (auth) → `{ captureOn, sessionAlive }`.
  - `GET /health` (unauth) → `{ ok: true }`.
  - WebSocket `/ws/terminal`, `/ws/metrics`, `/ws/captures` (auth via cookie + Origin).

- [ ] **Step 1: Write the failing test**

`server/test/server.test.js`:
```js
import { describe, it, expect, beforeAll, afterAll } from "vitest";
import { buildServer } from "../src/server.js";

let srv;
beforeAll(async () => {
  srv = await buildServer({
    config: {
      accessKey: "k",
      anthropicAuthToken: "t",
      noProxy: "localhost",
      apiTimeoutMs: 600000,
    },
    sessionSecret: "sec",
    port: 0,
  });
});
afterAll(async () => { await srv.close(); });

describe("server", () => {
  it("health responds unauthenticated", async () => {
    const r = await fetch(`http://127.0.0.1:${srv.port}/health`);
    expect(r.status).toBe(200);
    expect(await r.json()).toEqual({ ok: true });
  });

  it("rejects protected route without cookie", async () => {
    const r = await fetch(`http://127.0.0.1:${srv.port}/api/state`);
    expect(r.status).toBe(401);
  });

  it("auth sets cookie and grants access", async () => {
    const r = await fetch(`http://127.0.0.1:${srv.port}/auth`, {
      method: "POST",
      headers: { "content-type": "application/json" },
      body: JSON.stringify({ key: "k" }),
    });
    expect(r.status).toBe(200);
    const cookie = r.headers.get("set-cookie").split(";")[0];
    const r2 = await fetch(`http://127.0.0.1:${srv.port}/api/state`, {
      headers: { cookie },
    });
    expect(r2.status).toBe(200);
  });

  it("rejects wrong key", async () => {
    const r = await fetch(`http://127.0.0.1:${srv.port}/auth`, {
      method: "POST",
      headers: { "content-type": "application/json" },
      body: JSON.stringify({ key: "nope" }),
    });
    expect(r.status).toBe(401);
  });
});
```

- [ ] **Step 2: Run test to verify it fails**

Run: `cd server && npx vitest run test/server.test.js`
Expected: FAIL — `buildServer` missing.

- [ ] **Step 3: Implement `server.js`**

`server/src/server.js`:
```js
import Fastify from "fastify";
import cookie from "@fastify/cookie";
import { WebSocketServer } from "ws";
import { readFileSync } from "node:fs";
import { timingSafeEqualStr, signSession } from "./auth.js";
import { requireAuth } from "./auth-middleware.js";
import {
  readCgroupCpu, readCgroupMemory, readNetDev, computeCpuPercent,
} from "./metrics.js";
import { createCaptureStore } from "./capture-store.js";
import { createPtyManager } from "./pty-manager.js";
import os from "node:os";

const read = (p) => { try { return readFileSync(p, "utf8"); } catch { return null; } };

export async function buildServer({ config, sessionSecret, port = 8080 }) {
  const captureStore = createCaptureStore({ knownSecrets: [config.anthropicApiKey, config.anthropicAuthToken].filter(Boolean) });
  let captureOn = false;

  const pty = createPtyManager({
    cwd: "/workspace",
    env: buildClaudeEnv(config),
    command: "claude",
    args: [],
  });

  const fastify = Fastify();
  await fastify.register(cookie);

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

  fastify.addHook("preHandler", requireAuth(sessionSecret))
    .decorator("skipAuth", true);

  fastify.get("/api/state", { preHandler: [requireAuth(sessionSecret)] }, async () => ({
    captureOn, sessionAlive: pty.alive,
  }));
  fastify.post("/api/capture/enable", { preHandler: [requireAuth(sessionSecret)] }, async (_req, reply) => {
    captureOn = true; reply.send({ captureOn: true });
  });
  fastify.post("/api/capture/disable", { preHandler: [requireAuth(sessionSecret)] }, async (_req, reply) => {
    captureOn = false; reply.send({ captureOn: false });
  });
  fastify.post("/api/captures/clear", { preHandler: [requireAuth(sessionSecret)] }, async (_req, reply) => {
    captureStore.clear(); reply.send({ ok: true });
  });
  fastify.get("/api/captures", { preHandler: [requireAuth(sessionSecret)] }, async () => captureStore.list());
  fastify.post("/api/session/restart", { preHandler: [requireAuth(sessionSecret)] }, async (_req, reply) => {
    pty.kill(); pty.start(); reply.send({ ok: true });
  });

  await fastify.listen({ port, host: "0.0.0.0" });
  const actualPort = fastify.server.address().port;

  const wss = new WebSocketServer({ noServer: true });
  fastify.server.on("upgrade", (req, socket, head) => {
    const url = new URL(req.url, "http://x");
    const cookieVal = parseCookie(req.headers.cookie || "")["session"];
    if (!cookieVal || req.headers.origin && req.headers.origin !== `http://${req.headers.host}`) {
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
      pty.onData((d) => ws.readyState === ws.OPEN && ws.send(d));
      ws.on("message", (raw) => {
        const msg = JSON.parse(raw.toString());
        if (msg.type === "resize") pty.resize(msg.cols, msg.rows);
        else if (msg.type === "input") pty.write(msg.data);
      });
      ws.on("close", () => {});
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
    return {
      ...process.env,
      PATH: `${process.env.HOME}/.local/bin:${process.env.PATH}`,
      CLAUDE_CONFIG_DIR: config.CLAUDE_CONFIG_DIR || process.env.CLAUDE_CONFIG_DIR,
      ...(cfg.anthropicApiKey ? { ANTHROPIC_API_KEY: cfg.anthropicApiKey } : {}),
      ...(cfg.anthropicAuthToken ? { ANTHROPIC_AUTH_TOKEN: cfg.anthropicAuthToken } : {}),
      ...(cfg.anthropicBaseUrl ? { ANTHROPIC_BASE_URL: cfg.anthropicBaseUrl } : {}),
      ...(cfg.httpProxy ? { HTTP_PROXY: cfg.httpProxy, http_proxy: cfg.httpProxy } : {}),
      ...(cfg.httpsProxy ? { HTTPS_PROXY: cfg.httpsProxy, https_proxy: cfg.httpsProxy } : {}),
      ...(cfg.allProxy ? { ALL_PROXY: cfg.allProxy, all_proxy: cfg.allProxy } : {}),
      NO_PROXY: cfg.noProxy, no_proxy: cfg.noProxy,
      API_TIMEOUT_MS: String(cfg.apiTimeoutMs),
    };
  }

  return {
    fastify,
    port: actualPort,
    captureStore,
    pty,
    setCaptureOn: (v) => { captureOn = v; },
    getCaptureOn: () => captureOn,
    close: async () => { pty.kill(); wss.close(); await fastify.close(); },
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
```

> Note: the `addHook("preHandler", requireAuth(...))` line in the draft above would guard `/auth` too. **Remove that line** before running tests — auth routes must stay unguarded. The per-route `preHandler: [requireAuth(sessionSecret)]` on each protected route is the guard. Keep `/health` and `/auth` and `/logout` unguarded.

- [ ] **Step 4: Remove the stray global preHandler line**

In `server/src/server.js`, delete this exact block (it guards everything including `/auth`, which is wrong):
```js
  fastify.addHook("preHandler", requireAuth(sessionSecret))
    .decorator("skipAuth", true);
```
Keep only the per-route `preHandler` arrays on protected routes.

- [ ] **Step 5: Run test to verify it passes**

Run: `cd server && npx vitest run test/server.test.js`
Expected: PASS (4 tests).

- [ ] **Step 6: Commit**

```bash
git add server/src/server.js server/test/server.test.js
git commit -m "feat(server): fastify + ws wiring for terminal, metrics, captures, auth"
```

---

### Task 9: debug-proxy.js — opt-in MITM proxy + container-local CA

The MITM proxy and CA install are integration-level; verified manually in Task 12. Unit-test the CA-handling helpers where feasible.

**Files:**
- Create: `server/src/debug-proxy.js`
- Modify: `server/src/server.js` (wire capture toggle into starting/stopping the proxy and repointing `HTTPS_PROXY`)
- Modify: `server/test/server.test.js` (no new assertions; ensure `/api/capture/enable` still works)

**Interfaces:**
- Consumes: `http-mitm-proxy`, `selfsigned`, the `captureStore` from Task 6.
- Produces: `createDebugProxy({ store, upstreamProxy })` → `{ async start(), async stop(), caCertPem() }`. On `start()` it generates (or loads) a container-local CA, writes it to `/usr/local/share/ca-certificates/debug-proxy.crt` and runs `update-ca-certificates`, then starts the MITM proxy on `127.0.0.1` capturing each request/response into the store.

- [ ] **Step 1: Implement `debug-proxy.js`**

`server/src/debug-proxy.js`:
```js
import { execSync } from "node:child_process";
import { existsSync, mkdirSync, readFileSync, writeFileSync } from "node:fs";
import { Proxy } from "http-mitm-proxy";
import selfsigned from "selfsigned";

const CA_KEY = "/home/claude/.claude/debug-proxy-ca.key";
const CA_CERT_PEM = "/home/claude/.claude/debug-proxy-ca.crt";
const SYSTEM_CERT = "/usr/local/share/ca-certificates/debug-proxy.crt";

function loadOrCreateCA() {
  if (existsSync(CA_CERT_PEM) && existsSync(CA_KEY)) {
    return { key: readFileSync(CA_KEY, "utf8"), cert: readFileSync(CA_CERT_PEM, "utf8") };
  }
  const pem = selfsigned.generate(
    [{ name: "commonName", value: "claude-docker-debug-proxy" }],
    { keySize: 2048, days: 3650 }
  );
  mkdirSync("/home/claude/.claude", { recursive: true });
  writeFileSync(CA_KEY, pem.private);
  writeFileSync(CA_CERT_PEM, pem.cert);
  return { key: pem.private, cert: pem.cert };
}

export function installCaToContainer(certPem) {
  writeFileSync(SYSTEM_CERT, certPem);
  try { execSync("update-ca-certificates", { stdio: "inherit" }); } catch {}
  return {
    NODE_EXTRA_CA_CERTS: CA_CERT_PEM,
    SSL_CERT_FILE: "/etc/ssl/certs/ca-certificates.crt",
    REQUESTS_CA_BUNDLE: "/etc/ssl/certs/ca-certificates.crt",
  };
}

export function createDebugProxy({ store, upstreamProxy }) {
  let proxy = null;
  let ca = null;

  return {
    async start() {
      if (proxy) return;
      ca = loadOrCreateCA();
      installCaToContainer(ca.cert);
      proxy = new Proxy();
      if (upstreamProxy) {
        proxy.onUpstream(async (ctx, next) => { ctx.proxyOptions.agent = undefined; return next(); });
      }
      proxy.onRequest((ctx, callback) => {
        const started = Date.now();
        const chunks = { req: [], res: [] };
        const host = ctx.clientToProxyRequest.headers.host || ctx.proxyToServerHostName;
        const method = ctx.clientToProxyRequest.method;
        const url = ctx.clientToProxyRequest.url;

        ctx.onRequestData((d, next) => { chunks.req.push(d); return next(); });
        ctx.onResponseData((d, next) => { chunks.res.push(d); return next(); });
        ctx.onResponseEnd((cb) => {
          const reqBody = Buffer.concat(chunks.req).toString("utf8");
          const resBody = Buffer.concat(chunks.res).toString("utf8");
          store.add({
            ts: started, method, host, path: url,
            status: ctx.serverToProxyResponse?.statusCode,
            latencyMs: Date.now() - started,
            reqHeaders: { ...ctx.clientToProxyRequest.headers },
            reqBody, resHeaders: { ...(ctx.serverToProxyResponse?.headers || {}) }, resBody,
          });
          cb();
        });
        callback();
      });
      proxy.listen({ host: "127.0.0.1", port: 0, sslCaDir: "/home/claude/.claude/mitm-certs" }, (err) => {
        if (err) console.error("debug proxy listen error", err);
      });
    },
    async stop() {
      if (proxy) { try { proxy.close(); } catch {} proxy = null; }
    },
    caCertPem() { return ca?.cert; },
    proxyUrl() {
      return "http://127.0.0.1:" + (proxy?.options?.port || 8888);
    },
  };
}
```

- [ ] **Step 2: Wire capture toggle into `server.js`**

In `server/src/server.js`, import and own a debug proxy instance:
```js
import { createDebugProxy } from "./debug-proxy.js";
```
After the `captureStore`/`pty` setup, add:
```js
const debugProxy = createDebugProxy({
  store: captureStore,
  upstreamProxy: config.allProxy || config.httpsProxy || config.httpProxy,
});
```
Replace the bodies of the capture endpoints:
```js
  fastify.post("/api/capture/enable", { preHandler: [requireAuth(sessionSecret)] }, async (_req, reply) => {
    await debugProxy.start();
    captureOn = true;
    reply.send({ captureOn: true, proxyUrl: debugProxy.proxyUrl() });
  });
  fastify.post("/api/capture/disable", { preHandler: [requireAuth(sessionSecret)] }, async (_req, reply) => {
    await debugProxy.stop();
    captureOn = false;
    reply.send({ captureOn: false });
  });
```
In `close()`, also call `await debugProxy.stop()` before `fastify.close()`.

> The toggle only controls capture/recording. Whether Claude Code actually routes through the proxy is handled at container entrypoint time via env; enabling capture starts the listener and the UI signals "capture active". (Full automatic repointing of a running `claude` is out of scope — the user restarts the session via `/api/session/restart` after toggling, which the PTY manager re-spawns with the current env.)

- [ ] **Step 3: Run server tests to confirm no regression**

Run: `cd server && npx vitest run test/server.test.js`
Expected: PASS (4 tests). The MITM proxy is not started by tests.

- [ ] **Step 4: Commit**

```bash
git add server/src/debug-proxy.js server/src/server.js
git commit -m "feat(server): opt-in debug MITM proxy with container-local CA"
```

---

### Task 10: web SPA — Claude-styled UI (terminal, metrics, captures, unlock)

**Files:**
- Create: `web/index.html`
- Create: `web/vite.config.js`
- Create: `web/src/main.js`
- Create: `web/src/styles.css`
- Create: `web/src/unlock.js`
- Create: `web/src/terminal.js`
- Create: `web/src/metrics.js`
- Create: `web/src/captures.js`
- Create: `web/src/api.js`

**Interfaces:**
- Consumes: the server's HTTP + WS endpoints (Task 8/9).
- Produces: a built static bundle (`web/dist`) that the server serves (wired in Task 12).

- [ ] **Step 1: Create `web/vite.config.js`**

```js
import { defineConfig } from "vite";

export default defineConfig({
  server: { proxy: {
    "/api": "http://127.0.0.1:8080",
    "/auth": "http://127.0.0.1:8080",
    "/logout": "http://127.0.0.1:8080",
    "/ws": { target: "ws://127.0.0.1:8080", ws: true },
  } },
  build: { outDir: "dist", emptyOutDir: true },
});
```

- [ ] **Step 2: Create `web/index.html`**

```html
<!doctype html>
<html lang="en">
<head>
  <meta charset="utf-8" />
  <meta name="viewport" content="width=device-width, initial-scale=1" />
  <title>Claude Code</title>
  <link rel="stylesheet" href="/src/styles.css" />
</head>
<body>
  <main id="app"></main>
  <script type="module" src="/src/main.js"></script>
</body>
</html>
```

- [ ] **Step 3: Create `web/src/styles.css`** (Claude aesthetic)

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
}
* { box-sizing: border-box; }
html, body { height: 100%; margin: 0; }
body {
  background: var(--bg);
  color: var(--ink);
  font-family: ui-sans-serif, system-ui, "Segoe UI", Roboto, sans-serif;
}
h1, h2, h3 { font-family: Georgia, "Times New Roman", serif; font-weight: 600; margin: 0; }
#app { display: grid; grid-template-columns: 1fr 360px; gap: 16px; padding: 16px; height: 100vh; }
.panel { background: var(--surface); border: 1px solid var(--border); border-radius: var(--radius); padding: 16px; }
.panel h2 { font-size: 1rem; color: var(--muted); margin-bottom: 12px; letter-spacing: .02em; }
.term-wrap { display: flex; flex-direction: column; min-height: 0; }
.term-wrap .xterm { flex: 1; padding: 8px; background: #1E1B16; border-radius: var(--radius); }
.unlock { max-width: 360px; margin: 12vh auto; }
.unlock input, .unlock button { width: 100%; padding: 12px; border-radius: 8px; border: 1px solid var(--border); font-size: 1rem; margin-top: 8px; }
.unlock button { background: var(--accent); color: #fff; border: none; cursor: pointer; }
.meters { display: grid; grid-template-columns: 1fr 1fr; gap: 12px; }
.meter .label { font-size: .75rem; color: var(--muted); text-transform: uppercase; letter-spacing: .05em; }
.meter .value { font-family: Georgia, serif; font-size: 1.5rem; }
.warn { background: #FBEFE7; color: var(--accent); padding: 8px; border-radius: 8px; font-size: .85rem; }
.cap-list { max-height: 260px; overflow: auto; }
.cap-row { padding: 8px; border-bottom: 1px solid var(--border); cursor: pointer; font-size: .85rem; }
.cap-row .meta { color: var(--muted); }
.cap-detail { white-space: pre-wrap; font-family: ui-monospace, monospace; font-size: .8rem; max-height: 240px; overflow: auto; background: var(--surface-2); padding: 8px; border-radius: 8px; }
button.tiny { background: var(--surface-2); border: 1px solid var(--border); border-radius: 8px; padding: 6px 10px; cursor: pointer; }
```

- [ ] **Step 4: Create `web/src/api.js`**

```js
export async function postJson(url, body) {
  const r = await fetch(url, {
    method: "POST",
    headers: { "content-type": "application/json" },
    body: JSON.stringify(body || {}),
  });
  return r;
}
export async function getJson(url) {
  const r = await fetch(url);
  if (!r.ok) throw new Error(`${url} ${r.status}`);
  return r.json();
}
export function openWs(path, onMsg) {
  const proto = location.protocol === "https:" ? "wss" : "ws";
  const ws = new WebSocket(`${proto}://${location.host}${path}`);
  ws.onmessage = (e) => onMsg(JSON.parse(e.data));
  return ws;
}
```

- [ ] **Step 5: Create `web/src/unlock.js`**

```js
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
```

- [ ] **Step 6: Create `web/src/terminal.js`**

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

- [ ] **Step 7: Create `web/src/metrics.js`**

```js
import { openWs } from "./api.js";

export function mountMetrics() {
  let prev = null;
  openWs("/ws/metrics", (m) => {
    const cpuEl = document.getElementById("cpu");
    const memEl = document.getElementById("mem");
    const netEl = document.getElementById("net");
    let cpu = 0;
    if (prev) cpu = ((m.cpu.usageUsec - prev.cpu.usageUsec) / 1e6) / ((m.ts - prev.ts) / 1000) * 100;
    prev = m;
    const memPct = m.mem.max === Infinity ? 0 : (m.mem.current / m.mem.max) * 100;
    if (cpuEl) cpuEl.textContent = cpu.toFixed(1) + "%";
    if (memEl) memEl.textContent = (m.mem.current / 1048576).toFixed(0) + " MB" + (m.mem.max !== Infinity ? ` / ${(m.mem.max / 1048576).toFixed(0)}` : "");
    if (netEl) netEl.textContent = `${(m.net.rxBytes / 1048576).toFixed(1)}↓ ${(m.net.txBytes / 1048576).toFixed(1)}↑ MB`;
  });
}
```

- [ ] **Step 8: Create `web/src/captures.js`**

```js
import { openWs, postJson } from "./api.js";

export function mountCaptures(root) {
  root.innerHTML = `
    <h2>Request capture</h2>
    <div id="cap-warn" class="warn" style="display:none">⚠ Capture active — full request/response bodies are being recorded with secrets redacted.</div>
    <div style="margin:8px 0">
      <button class="tiny" id="cap-on">Start</button>
      <button class="tiny" id="cap-off">Stop</button>
      <button class="tiny" id="cap-clear">Clear</button>
    </div>
    <div class="cap-list" id="cap-list"></div>
    <div class="cap-detail" id="cap-detail" style="display:none"></div>`;
  const list = document.getElementById("cap-list");
  const detail = document.getElementById("cap-detail");
  const warn = document.getElementById("cap-warn");

  const renderRow = (r) => {
    const row = document.createElement("div");
    row.className = "cap-row";
    row.innerHTML = `<b>${r.method}</b> ${r.host}${r.path}
      <div class="meta">${r.status || "—"} · ${r.latencyMs}ms · ${new Date(r.ts).toLocaleTimeString()}</div>`;
    row.onclick = () => {
      detail.style.display = "block";
      detail.textContent =
        `REQUEST ${r.method} ${r.host}${r.path}\n` +
        JSON.stringify(r.reqHeaders, null, 2) + "\n\n" + (r.reqBody || "") +
        `\n\n--- RESPONSE ${r.status || ""} ---\n` +
        JSON.stringify(r.resHeaders, null, 2) + "\n\n" + (r.resBody || "");
    };
    list.prepend(row);
  };

  openWs("/ws/captures", (data) => {
    if (Array.isArray(data)) data.forEach(renderRow); else renderRow(data);
  });

  document.getElementById("cap-on").onclick = async () => {
    const r = await postJson("/api/capture/enable");
    if (r.ok) warn.style.display = "block";
  };
  document.getElementById("cap-off").onclick = async () => {
    await postJson("/api/capture/disable");
    warn.style.display = "none";
  };
  document.getElementById("cap-clear").onclick = async () => {
    await postJson("/api/captures/clear");
    list.innerHTML = "";
  };
}
```

- [ ] **Step 9: Create `web/src/main.js`**

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
    <section class="panel term-wrap"><h2>Terminal</h><div></div></section>
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

- [ ] **Step 10: Build the SPA**

Run: `cd web && npm install && npm run build`
Expected: `web/dist/` containing `index.html`, assets. No build errors.

- [ ] **Step 11: Commit**

```bash
git add web/
git commit -m "feat(web): claude-styled SPA (unlock, terminal, metrics, captures)"
```

---

### Task 11: entrypoint.sh + serve static SPA

**Files:**
- Create: `entrypoint.sh`
- Modify: `server/src/server.js` (serve `web/dist` via `@fastify/static`)

**Interfaces:**
- Consumes: built SPA from Task 10.
- Produces: a container entrypoint that sets up the trust store and drops to the Node server.

- [ ] **Step 1: Modify `server.js` to serve static SPA**

At the top of `server/src/server.js` add:
```js
import fastifyStatic from "@fastify/static";
import { existsSync } from "node:fs";
import path from "node:path";
```
After `await fastify.register(cookie);` add:
```js
  const webDist = process.env.WEB_DIST || "/app/web/dist";
  if (existsSync(webDist)) {
    await fastify.register(fastifyStatic, { root: path.resolve(webDist), prefix: "/" });
  }
```

- [ ] **Step 2: Create `entrypoint.sh`**

```sh
#!/usr/bin/env bash
set -euo pipefail

# Ensure trust store reflects any pre-installed CA (no-op if none yet)
update-ca-certificates >/dev/null 2>&1 || true

mkdir -p /workspace
chown -R claude:claude /workspace

export WEB_DIST="${WEB_DIST:-/app/web/dist}"
export CLAUDE_CONFIG_DIR="${CLAUDE_CONFIG_DIR:-/home/claude/.claude}"

exec gosu claude tini -- node /app/server/src/server.js
```

- [ ] **Step 3: Make entrypoint executable + adjust Dockerfile user tooling**

Append to the base `Dockerfile` `RUN` block (the apt install line) the package `gosu`, then after the useradd step add:
```dockerfile
COPY entrypoint.sh /entrypoint.sh
RUN chmod +x /entrypoint.sh

# App source
COPY server /app/server
COPY web /app/web
RUN cd /app/server && npm install --omit=dev && cd /app/web && npm install && npm run build

USER root
ENTRYPOINT ["/usr/bin/tini", "--", "/entrypoint.sh"]
EXPOSE 8080
```

- [ ] **Step 4: Commit**

```bash
git add entrypoint.sh server/src/server.js Dockerfile
git commit -m "feat: container entrypoint and static SPA serving"
```

---

### Task 12: Finalize Dockerfile, compose, docs, and run smoke test

**Files:**
- Modify: `Dockerfile` (full, consolidated)
- Create: `.env.example`
- Create: `docker-compose.yml`
- Create: `README.md`
- Create: `start.sh`, `start.bat`

**Interfaces:**
- Consumes: everything.
- Produces: a buildable image and one-command run instructions.

- [ ] **Step 1: Write the consolidated `Dockerfile`**

```dockerfile
FROM node:22-bookworm-slim AS base

ENV DEBIAN_FRONTEND=noninteractive \
    DISABLE_AUTOUPDATER=1 \
    DISABLE_UPDATES=1 \
    CLAUDE_CONFIG_DIR=/home/claude/.claude

RUN apt-get update && apt-get install -y --no-install-recommends \
        git ripgrep curl ca-certificates jq tini gosu sudo \
    && rm -rf /var/lib/apt/lists/*

RUN useradd -m -u 1000 -s /bin/bash claude \
    && install -d -o claude -g claude /workspace

RUN curl -fsSL https://claude.ai/install.sh | bash

WORKDIR /workspace

COPY server /app/server
COPY web /app/web
COPY entrypoint.sh /entrypoint.sh
RUN chmod +x /entrypoint.sh \
    && cd /app/server && npm install --omit=dev \
    && cd /app/web && npm install && npm run build

USER root
ENTRYPOINT ["/usr/bin/tini", "--", "/entrypoint.sh"]
EXPOSE 8080
```

- [ ] **Step 2: Create `.env.example`**

```env
# Required
ACCESS_KEY=change-me-web-secret
ANTHROPIC_AUTH_TOKEN=your-anthropic-token
# or: ANTHROPIC_API_KEY=sk-ant-...

# Optional gateway
# ANTHROPIC_BASE_URL=http://127.0.0.1:15721

# Proxy (http:// or socks5://) — leave blank for direct
# HTTP_PROXY=http://host.docker.internal:7890
# HTTPS_PROXY=http://host.docker.internal:7890
# ALL_PROXY=socks5://host.docker.internal:1080
NO_PROXY=localhost,127.0.0.1

API_TIMEOUT_MS=600000
```

- [ ] **Step 3: Create `docker-compose.yml`**

```yaml
services:
  claude:
    build: .
    image: claude-docker:latest
    ports:
      - "8080:8080"
    env_file: .env
    volumes:
      - claude-workspace:/workspace
      - claude-config:/home/claude/.claude
    restart: unless-stopped
volumes:
  claude-workspace:
  claude-config:
```

- [ ] **Step 4: Create `start.sh` and `start.bat`**

`start.sh`:
```sh
#!/usr/bin/env bash
set -euo pipefail
docker compose up --build -d
echo "Open: http://localhost:8080"
```

`start.bat`:
```bat
@echo off
docker compose up --build -d
echo Open: http://localhost:8080
pause
```

- [ ] **Step 5: Create `README.md`**

```markdown
# Claude Code in Docker

An isolated, browser-accessible Claude Code environment.

## Quick start

1. Copy `.env.example` to `.env` and set `ACCESS_KEY` plus an Anthropic credential.
2. Run `./start.sh` (macOS/Linux) or `start.bat` (Windows).
3. Open http://localhost:8080 and enter your `ACCESS_KEY`.

## Features

- Web terminal running Claude Code (`bypassPermissions`) in an isolated container.
- Access-key gated (for use on untrusted networks; put it behind TLS via Caddy/nginx/Cloudflare Tunnel).
- Configurable outbound proxy (HTTP or SOCKS5) via env.
- Live CPU/memory/network metrics.
- Opt-in request/response capture for debugging (full capture with a container-local CA; secrets redacted; off by default).

## Security notes

- HTTP only — front it with a TLS proxy for remote/company-network use.
- The debug-capture CA is trusted only inside the container.
- Non-root, no privileged mode, no Docker socket.
```

- [ ] **Step 6: Build and run the smoke test**

Run:
```bash
docker compose build
docker compose up -d
sleep 3
docker compose exec claude bash -lc 'claude --bare -p "Reply with the single word OK" --output-format json'
docker compose exec claude bash -lc 'curl -sf http://127.0.0.1:8080/health'
curl -sf http://localhost:8080/health
```
Expected: the `claude --bare -p` call returns JSON with a result; both `/health` checks return `{"ok":true}`.

- [ ] **Step 7: Commit**

```bash
git add Dockerfile .env.example docker-compose.yml start.sh start.bat README.md
git commit -m "feat: finalize image, compose, launchers, and smoke test"
```

---

## Self-Review Notes

- **Spec coverage:** Goal 1 (isolation) → Tasks 1, 12 (non-root, no socket). Goal 2 (proxy) → Tasks 2, 8, `.env.example`. Goal 3 (web terminal) → Tasks 8, 10, 11. Goal 4 (access key) → Tasks 2, 4, 8, 10. Goal 5 (metrics) → Tasks 5, 8, 10. Goal 6 (debug capture) → Tasks 3, 6, 9, 10. Goal 7 (Claude UI) → Task 10. All seven goals covered.
- **Placeholder scan:** No TBD/TODO. One intentional build-time note in Task 9 (auto-repoint of running `claude` is out of scope; restart toggles env) — documented, not vague.
- **Type consistency:** `captureStore.add`/`list`/`clear`/`subscribe` names match across Tasks 6, 8, 9, 10. `readCgroupCpu`/`readCgroupMemory`/`readNetDev`/`computeCpuPercent` match Tasks 5 and 8. `signSession`/`verifySession`/`timingSafeEqualStr` match Tasks 4, 7, 8. `buildServer` signature matches Task 8 and its test.
