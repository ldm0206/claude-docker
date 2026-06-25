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

  it("does not redact short sk-ant- suffix (below 6 chars)", () => {
    expect(redactBody('{"a":"sk-ant-abc"}', [])).toContain("sk-ant-abc");
  });

  it("does not redact short Bearer token (below 6 chars)", () => {
    expect(redactBody("Bearer abc", [])).toContain("Bearer abc");
  });

  it("skips known secrets shorter than 4 chars", () => {
    expect(redactBody("ab here", ["ab"])).toContain("ab");
  });

  it("does not redact benign Bearer prose (no non-alpha chars in token part)", () => {
    expect(redactBody("The Bearer authorization scheme", [])).toContain("Bearer authorization");
  });
});
