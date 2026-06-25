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
