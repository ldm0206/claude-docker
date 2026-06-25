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
const read = (p) => readFileSync(p.replace("/sys/fs/cgroup/cpu.stat", fx("cgroup.cpu.stat"))
  .replace("/sys/fs/cgroup/memory.current", fx("cgroup.memory.current"))
  .replace("/sys/fs/cgroup/memory.max", fx("memory.max"))
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

  it("returns Infinity when memory.max is the sentinel 'max'", () => {
    const readMax = (p) => readFileSync(
      p.replace("/sys/fs/cgroup/memory.current", fx("cgroup.memory.current"))
        .replace("/sys/fs/cgroup/memory.max", (() => { throw new Error("not a file"); })()),
      "utf8"
    );
    // Build a read shim that returns "max" for the memory.max path
    const readSentinel = (p) => {
      if (p === "/sys/fs/cgroup/memory.max") return "max";
      return read(p);
    };
    const m = readCgroupMemory(readSentinel);
    expect(m.current).toBe(524288000);
    expect(m.max).toBe(Infinity);
  });

  it("computeCpuPercent returns 0 when elapsedMs is 0", () => {
    expect(computeCpuPercent({ usageUsec: 0 }, { usageUsec: 500000 }, 0, 1)).toBe(0);
  });

  it("computeCpuPercent returns 0 when delta is negative (cur < prev)", () => {
    expect(computeCpuPercent({ usageUsec: 500000 }, { usageUsec: 400000 }, 1000, 1)).toBe(0);
  });
});
