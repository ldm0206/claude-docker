import { describe, it, expect } from "vitest";
import { resolveEnv } from "../src/pty-manager.js";

// resolveEnv is the helper that makes PTY env lazy: pty-manager resolves `env`
// (object OR function) at start() time, so a restart re-evaluates dynamic
// inputs (e.g. debugProxy.isUp() routing). We unit-test the resolution logic
// directly — node-pty spawning is too flaky on a Windows test host (ConPTY
// escape sequences / AttachConsole errors) to assert on captured output.
describe("pty-manager env resolution", () => {
  it("returns a plain object as-is", () => {
    const obj = { FOO: "1", BAR: "2" };
    expect(resolveEnv(obj)).toBe(obj);
  });

  it("calls a function and returns its result", () => {
    let calls = 0;
    const factory = () => {
      calls += 1;
      return { FOO: `call-${calls}` };
    };
    expect(resolveEnv(factory)).toEqual({ FOO: "call-1" });
    // Re-resolving re-invokes the factory (proves laziness / non-caching).
    expect(resolveEnv(factory)).toEqual({ FOO: "call-2" });
  });

  it("evaluates the factory fresh each call — captures isUp() flips", () => {
    // Simulates the C1 scenario: env depends on a mutable flag.
    let mitmUp = false;
    const envFactory = () => ({
      HTTP_PROXY: mitmUp ? "http://127.0.0.1:8888" : undefined,
    });
    expect(resolveEnv(envFactory).HTTP_PROXY).toBeUndefined();
    mitmUp = true; // capture enabled + MITM started
    expect(resolveEnv(envFactory).HTTP_PROXY).toBe("http://127.0.0.1:8888");
  });

  it("passes through null/undefined unchanged", () => {
    expect(resolveEnv(undefined)).toBeUndefined();
    expect(resolveEnv(null)).toBeNull();
  });
});
