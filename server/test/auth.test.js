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
