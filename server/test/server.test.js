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
