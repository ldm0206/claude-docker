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
