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
});
