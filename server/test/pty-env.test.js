import { describe, it, expect, vi, beforeEach } from "vitest";

// Mock node-pty so tests never spawn real processes (ConPTY is flaky on
// Windows test hosts). vi.mock factories are hoisted above imports, so
// we must not reference outer variables — use vi.fn() inline.
vi.mock("node-pty", () => ({
  spawn: vi.fn(() => ({
    onData: vi.fn(),
    onExit: vi.fn(),
    kill: vi.fn(),
  })),
}));

// Mock node:fs so existsSync can be controlled per-test
vi.mock("node:fs", () => ({
  existsSync: vi.fn(() => false),
  readFileSync: vi.fn(),
}));

// Import after mocks are declared (vi.mock is hoisted by vitest)
import { resolveEnv, createPtyManager } from "../src/pty-manager.js";
import { spawn as mockSpawn } from "node-pty";
import { existsSync as mockExistsSync } from "node:fs";

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

describe("createPtyManager fallback", () => {
  beforeEach(() => {
    vi.clearAllMocks();
    mockExistsSync.mockReturnValue(false);
  });

  it("falls back to /bin/bash when claude binary missing", () => {
    const pty = createPtyManager({ cwd: "/tmp", env: {} });
    const captured = [];
    pty.onData((d) => captured.push(d));
    pty.start();

    // Warning should have been emitted to data callbacks
    expect(captured.some(d => d.includes("falling back to bash"))).toBe(true);
    // Should have spawned /bin/bash instead of claude
    expect(mockSpawn).toHaveBeenCalledWith("/bin/bash", [], expect.any(Object));
    pty.kill();
  });

  it("does not fall back when claude binary exists", () => {
    mockExistsSync.mockReturnValue(true);
    const pty = createPtyManager({ cwd: "/tmp", env: {} });
    pty.start();

    // Should have spawned claude (no fallback)
    expect(mockSpawn).toHaveBeenCalledWith("claude", [], expect.any(Object));
    pty.kill();
  });
});
