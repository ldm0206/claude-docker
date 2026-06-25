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

  it("returns httpsProxy when HTTPS_PROXY is set", () => {
    process.env.ACCESS_KEY = "k";
    process.env.HTTPS_PROXY = "https://proxy:8443";
    const cfg = loadConfig();
    expect(cfg.httpsProxy).toBe("https://proxy:8443");
  });

  it("defaults noProxy to localhost,127.0.0.1 when NO_PROXY is unset", () => {
    process.env.ACCESS_KEY = "k";
    // NO_PROXY is deleted in beforeEach, so it is unset
    const cfg = loadConfig();
    expect(cfg.noProxy).toBe("localhost,127.0.0.1");
  });

  it("throws on invalid API_TIMEOUT_MS values", () => {
    process.env.ACCESS_KEY = "k";
    process.env.API_TIMEOUT_MS = "-1";
    expect(() => loadConfig()).toThrow(/API_TIMEOUT_MS/);
    process.env.API_TIMEOUT_MS = "0";
    expect(() => loadConfig()).toThrow(/API_TIMEOUT_MS/);
    process.env.API_TIMEOUT_MS = "not-a-number";
    expect(() => loadConfig()).toThrow(/API_TIMEOUT_MS/);
  });

  it("returns anthropicApiKey and anthropicBaseUrl when set", () => {
    process.env.ACCESS_KEY = "k";
    process.env.ANTHROPIC_API_KEY = "sk-test-key";
    process.env.ANTHROPIC_BASE_URL = "https://custom.api.com";
    const cfg = loadConfig();
    expect(cfg.anthropicApiKey).toBe("sk-test-key");
    expect(cfg.anthropicBaseUrl).toBe("https://custom.api.com");
  });
});
